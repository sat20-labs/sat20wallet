package wallet

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func (p *Manager) readSTPNativePreparedRecovery(channel *Channel) (*stpNativePreparedRecoveryRecord, error) {
	if channel == nil || p.db == nil || channel.CommitHeight < 0 {
		return nil, nil
	}
	raw, err := p.db.Read(stpNativePreparedRecoveryKey(channel))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record stpNativePreparedRecoveryRecord
	if err := DecodeFromBytes(raw, &record); err != nil {
		return nil, err
	}
	if record.Version != 1 || record.ChannelID != channel.ChannelId || record.Height != uint64(channel.CommitHeight)+1 ||
		record.OldChannel == nil || record.Candidate == nil || record.OldChannel.ChannelId != record.ChannelID ||
		record.Candidate.ChannelId != record.ChannelID || record.OldChannel.CommitHeight != channel.CommitHeight ||
		record.Candidate.CommitHeight != channel.CommitHeight || record.Candidate.RemoteCommitment == nil ||
		record.Candidate.RemoteCommitment.CommitTx == nil || record.Candidate.RemoteCommitment.DeAnchorTx == nil ||
		record.Candidate.RemoteCommitment.CommitTx.TxID() != record.CommitTxID {
		return nil, fmt.Errorf("native prepared recovery has inconsistent channel identity")
	}
	if record.OldChannel.GetLocalPubKey() == nil || record.OldChannel.GetRemotePubKey() == nil ||
		channel.GetLocalPubKey() == nil || channel.GetRemotePubKey() == nil ||
		!record.OldChannel.GetLocalPubKey().IsEqual(channel.GetLocalPubKey()) ||
		!record.OldChannel.GetRemotePubKey().IsEqual(channel.GetRemotePubKey()) ||
		!bytes.Equal(record.OldChannel.RedeemScript, channel.RedeemScript) {
		return nil, fmt.Errorf("native prepared recovery changed channel ownership")
	}
	old, next := record.OldChannel, record.Candidate
	if next.GetLocalPubKey() == nil || next.GetRemotePubKey() == nil ||
		!next.GetLocalPubKey().IsEqual(old.GetLocalPubKey()) || !next.GetRemotePubKey().IsEqual(old.GetRemotePubKey()) ||
		!bytes.Equal(next.RedeemScript, old.RedeemScript) || !bytes.Equal(next.PeerNodeId, old.PeerNodeId) ||
		next.IsInitiator != old.IsInitiator || next.LocalWalletId != old.LocalWalletId ||
		next.LocalChanCfg.WalletId != old.LocalChanCfg.WalletId || next.CsvDelay != old.CsvDelay ||
		next.ChannelType != old.ChannelType || next.Address != old.Address ||
		(old.AssetEntropy != ([32]byte{}) && next.AssetEntropy != old.AssetEntropy) ||
		(channel.AssetEntropy != ([32]byte{}) && next.AssetEntropy != channel.AssetEntropy) {
		return nil, fmt.Errorf("native prepared candidate changed immutable channel identity")
	}
	candidate := &Channel{ChannelInDB: *record.Candidate}
	if !needsSTPNativeOtherExchange(candidate, candidate.RemoteCommitment) {
		return nil, fmt.Errorf("prepared recovery has no protocol obligation")
	}
	// The parent may have only our signature, but every dependent sweep must
	// already be independently broadcastable once that exact parent confirms.
	check := *candidate
	check.LocalCommitment = candidate.RemoteCommitment
	if _, err := encodeSTPNativeOtherTransactions(&check); err != nil {
		return nil, err
	}
	// Validate the separate dormant RGB obligation alongside, without changing
	// which legacy Other packages the existing ARC reader accepts or consumes.
	if _, err := encodeRGBClientRecovery(&check, check.LocalCommitment); err != nil {
		return nil, err
	}
	return &record, nil
}

// Prefer a newer local obligation if an old secret may already have left this
// node. Otherwise a prepared remote package blocks new signing while retaining
// the original local commitment together with its original funding context.
func (p *Manager) LoadSTPNativeRevocationRecovery(stored *ChannelInDB) (*ChannelInDB, error) {
	recovered, err := p.loadSTPNativeRevocationRecovery(stored)
	if err != nil || recovered != nil {
		return recovered, err
	}
	return p.LoadSTPNativePreparedRecovery(stored)
}

func nativePreparedRecoveryTerminal(status ChannelStatus) bool {
	return status <= CS_CLOSED || status >= CS_CLOSE_FORCELY_BROADCASTED && status <= CS_CLOSE_FORCELY_SWEEP_CONFIRMED
}

func (p *Manager) LoadSTPNativePreparedRecovery(stored *ChannelInDB) (*ChannelInDB, error) {
	if stored == nil || nativePreparedRecoveryTerminal(stored.Status) {
		return nil, nil
	}
	record, err := p.readSTPNativePreparedRecovery(&Channel{ChannelInDB: *stored})
	if err != nil || record == nil {
		return nil, err
	}
	copy := *record.OldChannel
	copy.Status = CS_NATIVE_RECOVERY
	return &copy, nil
}

func (p *Manager) canonicalNativePreparedTransactions(txs []*wire.MsgTx) ([]*wire.MsgTx, error) {
	result := make([]*wire.MsgTx, len(txs))
	for i, tx := range txs {
		if tx == nil || !p.GetIndexerRPCClient().IsTxConfirmed(tx.TxID()) {
			return nil, fmt.Errorf("prepared funding predecessor is not confirmed")
		}
		raw, err := p.GetIndexerRPCClient().GetRawTx(tx.TxID())
		if err != nil {
			return nil, err
		}
		actual, err := DecodeMsgTx(raw)
		if err != nil || actual.TxHash() != tx.TxHash() {
			return nil, fmt.Errorf("prepared funding predecessor changed transaction identity")
		}
		result[i] = actual
	}
	return result, nil
}

// RetryNativePreparedRecovery observes the exact peer parent before switching
// funding contexts. It never signs or asks the peer for additional material.
func (p *Manager) RetryNativePreparedRecovery(channel *Channel) (bool, error) {
	if channel == nil || p.db == nil {
		return false, nil
	}
	channel.Mutex.RLock()
	snapshot := &Channel{ChannelInDB: channel.ChannelInDB}
	current := p.GetChannel(channel.ChannelId) == channel
	channel.Mutex.RUnlock()
	if !current {
		return true, nil // An operation installed a different channel while this tick waited.
	}
	if nativePreparedRecoveryTerminal(snapshot.Status) {
		return false, nil
	}
	record, err := p.readSTPNativePreparedRecovery(snapshot)
	if err != nil {
		return true, err
	}
	if record == nil {
		return false, nil
	}
	if p.GetIndexerRPCClient() == nil {
		return true, fmt.Errorf("native prepared recovery has no Bitcoin evidence client")
	}
	if !p.GetIndexerRPCClient().IsTxConfirmed(record.CommitTxID) {
		channel.Mutex.Lock()
		if p.GetChannel(channel.ChannelId) == channel && channel.CommitHeight == snapshot.CommitHeight && channel.Status == CS_READY {
			channel.Status = CS_NATIVE_RECOVERY
		}
		channel.Mutex.Unlock()
		// The old funding context remains authoritative until this exact peer
		// parent confirms. Its current/revoked commitment still needs the
		// ordinary safety monitor, even though signing is held.
		return false, nil
	}
	raw, err := p.GetIndexerRPCClient().GetRawTx(record.CommitTxID)
	if err != nil {
		return true, err
	}
	actual, err := DecodeMsgTx(raw)
	if err != nil || actual.TxID() != record.CommitTxID {
		return true, fmt.Errorf("canonical prepared peer commitment is unavailable")
	}
	candidate := &Channel{ChannelInDB: *record.Candidate, manager: p, localWallet: channel.LocalWallet(), PeerRPC: channel.PeerRPC}
	candidate.NativeParents, err = p.canonicalNativePreparedTransactions(candidate.NativeParents)
	if err != nil {
		return true, err
	}
	if err := verifySTPNativeParents(candidate); err != nil {
		return true, err
	}
	candidate.RemoteCommitment.PrevTxs, err = p.canonicalNativePreparedTransactions(candidate.RemoteCommitment.PrevTxs)
	if err != nil {
		return true, err
	}
	fetcher := candidate.GetCommitmentPrefetchor()
	for _, previous := range candidate.RemoteCommitment.PrevTxs {
		for _, input := range previous.TxIn {
			if input == nil || fetcher.FetchPrevOutput(input.PreviousOutPoint) == nil {
				return true, fmt.Errorf("prepared predecessor has no verified input")
			}
		}
		if err := VerifySignedTx(previous, fetcher); err != nil {
			return true, err
		}
		AddTxOutputsToPrevFetcher(fetcher, previous)
	}
	for _, input := range actual.TxIn {
		if input == nil || fetcher.FetchPrevOutput(input.PreviousOutPoint) == nil {
			return true, fmt.Errorf("prepared peer commitment has no verified input")
		}
	}
	if err := VerifySignedTx(actual, fetcher); err != nil {
		return true, err
	}
	if err := p.verifySTPChannelProofs(candidate, actual, candidate.RemoteCommitment.AssetProofs, STPOpForceClose); err != nil {
		return true, err
	}
	candidate.RemoteCommitment.CommitTx = actual
	candidate.Status = CS_NATIVE_RECOVERY
	channel.Mutex.Lock()
	if p.GetChannel(channel.ChannelId) != channel || channel.CommitHeight != snapshot.CommitHeight || nativePreparedRecoveryTerminal(channel.Status) {
		channel.Mutex.Unlock()
		return false, nil
	}
	channel.ChannelInDB = candidate.ChannelInDB
	channel.Mutex.Unlock()
	generation, err := captureChannelSafetyGeneration(channel)
	if err != nil {
		return true, err
	}
	return true, p.finalizePeerForceClose(channel, generation, record.CommitTxID)
}

func (p *Manager) importSTPNativePreparedFunding(channel *Channel) error {
	record, err := p.readSTPNativePreparedRecovery(channel)
	if err != nil || record == nil {
		return err
	}
	for _, protocol := range stpAssetProtocols.ordered {
		if len(channel.AssetFunding[protocol.Protocol()]) == 0 {
			continue
		}
		if settlement, ok := protocol.(STPAssetPreparedFundingSettlement); ok {
			if err := settlement.ImportPreparedChannelFunding(p, channel, channel.NativeParents); err != nil {
				return err
			}
		}
	}
	return nil
}
