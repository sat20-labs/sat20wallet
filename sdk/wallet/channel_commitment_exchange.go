package wallet

import (
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

func (p *Manager) SignNextCommitment(info *RevocationInfo) error {
	channel := info.Channel

	// 本地先用对端给出的下一版 revocation point 构造对端视角的下一版
	// commitment tx，并只返回本地的部分签名。等对端验签通过后，对端才会
	// revoke 旧状态。
	commitPoint, err := btcec.ParsePubKey(info.RemoteNextRevKey)
	if err != nil {
		return err
	}
	var revealKey *secp256k1.PrivateKey
	if info.RevealPrivKey != nil {
		revealKey, _ = btcec.PrivKeyFromBytes(info.RevealPrivKey)
		if revealKey == nil {
			return fmt.Errorf("invalid reveal key")
		}
	}

	bootstrapKey := p.GetBootstrapNodePaymentPubKey()
	keyRing := DeriveCommitmentKeys(commitPoint, 1, bootstrapKey, revealKey, channel)

	commitTx, inscribes, next, others, err := p.CreateCommitTxWithOthers(1, channel, keyRing, bootstrapKey, info.FeeRate)
	if err != nil {
		Log.Errorf("CreateCommitTx failed. %v", err)
		return err
	}
	sigs, preTxSigs, nextTxSigs, otherPrevTxSigs, otherTxSigs, err := PartialSignCommitTxWithOthers(channel, commitTx, inscribes, next, others)
	if err != nil {
		Log.Errorf("PartialSignCommitTx failed. %v", err)
		return err
	}

	info.RemoteCommitInfo.CommitTx = commitTx
	info.RemoteCommitInfo.CommitSig = sigs
	info.RemoteCommitInfo.CommitPrevTxs = nil
	info.RemoteCommitInfo.CommitPrevTxSig = nil
	info.RemoteCommitInfo.CommitNextTxs = nil
	info.RemoteCommitInfo.CommitNextTxSig = nil
	info.RemoteCommitInfo.CommitOthers = nil
	info.RemoteCommitInfo.CommitOtherPrevTxSig = nil
	info.RemoteCommitInfo.CommitOtherTxSig = nil

	channel.RemoteCommitment.CommitTx = commitTx
	channel.RemoteCommitment.CommitSig = sigs
	channel.RemoteCommitment.PrevTxs = nil
	channel.RemoteCommitment.NextTxs = nil
	channel.RemoteCommitment.Others = nil

	if len(inscribes) != 0 {
		for _, insc := range inscribes {
			info.RemoteCommitInfo.CommitPrevTxs = append(info.RemoteCommitInfo.CommitPrevTxs, insc.CommitTx, insc.RevealTx)
			channel.RemoteCommitment.PrevTxs = append(channel.RemoteCommitment.PrevTxs, insc.CommitTx, insc.RevealTx)
		}
		info.RemoteCommitInfo.CommitPrevTxSig = preTxSigs
	}
	if len(next) != 0 {
		for _, insc := range next {
			info.RemoteCommitInfo.CommitNextTxs = append(info.RemoteCommitInfo.CommitNextTxs, insc.CommitTx, insc.RevealTx)
			channel.RemoteCommitment.NextTxs = append(channel.RemoteCommitment.NextTxs, insc.CommitTx, insc.RevealTx)
		}
		info.RemoteCommitInfo.CommitNextTxSig = nextTxSigs
	}
	if len(others) != 0 {
		info.RemoteCommitInfo.CommitOthers = others
		info.RemoteCommitInfo.CommitOtherPrevTxSig = otherPrevTxSigs
		info.RemoteCommitInfo.CommitOtherTxSig = otherTxSigs
		channel.RemoteCommitment.Others = others
	}

	// 如果未来对端广播这笔 commitment tx，L2 侧 channel UTXO 也必须通过
	// deanchor tx 更新，所以 commitment 签名阶段就要一并构造和保存它的签名。
	deAnchorTx, prefetcher, err := CreateClosingDeAnchorTx(channel, commitTx.TxID(), p.GetDAOPkScript(channel))
	if err != nil {
		return err
	}
	deAnchorSig, err := PartialSignTxWithChannel_SatsNet(channel, deAnchorTx, prefetcher)
	if err != nil {
		return err
	}

	info.RemoteCommitInfo.CommitDeAnchorSig = deAnchorSig
	info.Channel.RemoteCommitment.DeAnchorTx = deAnchorTx
	info.Channel.RemoteCommitment.DeAnchorSig = deAnchorSig
	return nil
}

func (p *Manager) ReceiveNewCommitment(info *RevocationInfo, commitSig *wwire.CommitSigInfo, prevSignedTx []*wire.MsgTx) error {
	channel := info.Channel
	localWallet := p.GetWallet()

	// 对端发来的是本地视角的下一版 commitment tx 签名。本地用下一版
	// commitment secret 重新构造同一笔交易并验签，避免接受无法广播或脚本无效
	// 的承诺交易。
	nextHeight := uint32(channel.CommitHeight + 1)
	commitSecret := localWallet.GetCommitSecret(info.Channel.PeerNodeId, nextHeight)
	if commitSecret == nil {
		return fmt.Errorf("GetRevocationSecret failed")
	}
	var revealKey *secp256k1.PrivateKey
	if info.RevealPrivKey != nil {
		revealKey, _ = btcec.PrivKeyFromBytes(info.RevealPrivKey)
		if revealKey == nil {
			return fmt.Errorf("invalid reveal key")
		}
	}

	bootstrapKey := p.GetBootstrapNodePaymentPubKey()
	commitPoint := commitSecret.PubKey()
	keyRing := DeriveCommitmentKeys(commitPoint, 0, bootstrapKey, revealKey, info.Channel)

	commitTx, inscribes, next, others, err := p.CreateCommitTxWithOthers(0, channel, keyRing, bootstrapKey, info.FeeRate)
	if err != nil {
		Log.Errorf("CreateCommitTx failed. %v", err)
		return err
	}
	err = p.SignAndVerifyCommitTxWithOthers(info.Channel, prevSignedTx, commitTx, commitSig.CommitSig,
		inscribes, commitSig.CommitPrevTxSig, next, commitSig.CommitNextTxSig,
		others, commitSig.CommitOtherPrevTxSig, commitSig.CommitOtherTxSig, true)
	if err != nil {
		Log.Errorf("VerifyCommitTx failed. %v", err)
		return err
	}

	info.LocalCommitInfo.CommitTx = commitTx
	info.LocalCommitInfo.CommitSig = commitSig.CommitSig
	info.LocalCommitInfo.CommitPrevTxs = nil
	info.LocalCommitInfo.CommitPrevTxSig = nil
	info.LocalCommitInfo.CommitNextTxs = nil
	info.LocalCommitInfo.CommitNextTxSig = nil
	info.LocalCommitInfo.CommitOthers = nil

	channel.LocalCommitment.CommitTx = commitTx
	channel.LocalCommitment.CommitSig = commitSig.CommitSig
	channel.LocalCommitment.PrevTxs = nil
	channel.LocalCommitment.NextTxs = nil
	channel.LocalCommitment.Others = nil

	if len(inscribes) != 0 {
		for _, insc := range inscribes {
			info.LocalCommitInfo.CommitPrevTxs = append(info.LocalCommitInfo.CommitPrevTxs, insc.CommitTx, insc.RevealTx)
			channel.LocalCommitment.PrevTxs = append(channel.LocalCommitment.PrevTxs, insc.CommitTx, insc.RevealTx)
		}
	}
	if len(next) != 0 {
		for _, insc := range next {
			info.LocalCommitInfo.CommitNextTxs = append(info.LocalCommitInfo.CommitNextTxs, insc.CommitTx, insc.RevealTx)
			channel.LocalCommitment.NextTxs = append(channel.LocalCommitment.NextTxs, insc.CommitTx, insc.RevealTx)
		}
	}
	if len(others) != 0 {
		info.LocalCommitInfo.CommitOthers = others
		channel.LocalCommitment.Others = others
	}

	// commitment tx 本身只锁定 L1 输出；对应的 deanchor tx 负责在强制关闭
	// 场景下更新 L2 的 channel UTXO，所以这里必须验证对端给出的 deanchor 签名。
	deAnchorTx, prefetcher, err := CreateClosingDeAnchorTx(channel, commitTx.TxID(), p.GetDAOPkScript(channel))
	if err != nil {
		Log.Errorf("CreateClosingDeAnchorTx failed. %v", err)
		return err
	}
	_, err = SignAndVerifyTxWithChannel_SatsNet(channel, deAnchorTx, prefetcher, commitSig.CommitDeAnchorSig)
	if err != nil {
		Log.Errorf("SignAndVerifyTxWithChannel_SatsNet failed. %v", err)
		return err
	}

	info.LocalCommitInfo.CommitSigInfo = *commitSig
	info.Channel.LocalCommitment.DeAnchorSig = commitSig.CommitDeAnchorSig
	info.Channel.LocalCommitment.DeAnchorTx = deAnchorTx
	return nil
}

func (p *Manager) RevokeCurrentCommitment(info *RevocationInfo) (*wwire.RevokeAndAck, error) {
	channel := info.Channel
	localWallet := p.GetWallet()

	// 只有在对端的新 commitment 已经验证通过后，才释放当前 commitment secret。
	// 这个 secret 会让对端具备惩罚本方旧 commitment tx 的能力。
	revocationMsg := &wwire.RevokeAndAck{}
	commitSecret := localWallet.GetCommitSecret(info.Channel.PeerNodeId, uint32(channel.CommitHeight))
	if commitSecret == nil {
		return nil, fmt.Errorf("GetRevocationSecret failed")
	}
	copy(revocationMsg.Revocation[:], commitSecret.Serialize())
	info.Channel.LocalCommitment.Revocation = revocationMsg.Revocation[:]

	var err error
	revocationMsg.NextRevKey, err = p.GetNextRevocationKey(info.Channel)
	return revocationMsg, err
}

func (p *Manager) ReceiveRevocation(info *RevocationInfo, revMsg *wwire.RevokeAndAck) error {
	_, err := secp256k1.ParsePubKey(revMsg.NextRevKey)
	if err != nil {
		return err
	}

	revkey, err := secp256k1.ParsePubKey(info.RemoteRevKey)
	if err != nil {
		return err
	}

	_, derivedCommitPoint := btcec.PrivKeyFromBytes(revMsg.Revocation[:])
	if !derivedCommitPoint.IsEqual(revkey) {
		Log.Errorf("revocation key mismatch")
		return fmt.Errorf("revocation key mismatch")
	}

	info.RemoteRev = revMsg

	// 收到对端旧 secret 后，立即构造并验签 punish tx。这样如果对端之后广播
	// 已撤销的 remote commitment，watchtower 已经有可直接使用的惩罚交易链。
	signedPunishTx, err := p.CreateAndVerifyPunishTx(info.OldChannel, revMsg.Revocation[:], p.GetFeeRate())
	if err != nil {
		Log.Errorf("VerifyPunishTx failed. %v", err)
		return err
	}

	info.SignedPunishTx = signedPunishTx
	info.Channel.RemoteCommitment.Revocation = revMsg.Revocation[:]
	if signedPunishTx != nil {
		// BRC20 等资产可能需要先广播 commitment.NextTxs 中的 reveal tx，其他
		// commitment-adjacent tx 也要随 punish tx 一起交给 watchtower。
		punishTxs := append([]*wire.MsgTx{}, info.OldChannel.RemoteCommitment.NextTxs...)
		punishTxs = append(punishTxs, signedPunishTx)
		punishTxs = appendCommitmentOtherTxs(punishTxs, info.OldChannel.RemoteCommitment)
		if err := p.GetWatchTower().AddCommitTx(info.Channel, info.OldChannel.RemoteCommitment.CommitTx, punishTxs); err != nil {
			return err
		}
	}

	return nil
}

func appendCommitmentOtherTxs(txs []*wire.MsgTx, commitment *ChannelCommitment) []*wire.MsgTx {
	if commitment == nil {
		return txs
	}
	for _, other := range commitment.Others {
		if other == nil {
			continue
		}
		txs = append(txs, other.PrevTxs...)
		if other.CommitTx != nil {
			txs = append(txs, other.CommitTx)
		}
	}
	return txs
}
