package wallet

import (
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

// ValidateNativeRecoveryOnlyRequest rejects every authorization that could
// publish a parent transaction. Operation handlers must also reject their
// separate splicing/payment signatures and revocation-secret fields.
func ValidateNativeRecoveryOnlyRequest(request *wwire.CommitSigInfo) error {
	if request == nil || !request.RecoveryOnly || request.RecoveryCommitTxID == "" ||
		len(request.CommitSig) != 0 || len(request.CommitDeAnchorSig) != 0 ||
		len(request.CommitPrevTxSig) != 0 || len(request.CommitNextTxSig) != 0 || len(request.NativeOtherTxs) != 0 {
		return fmt.Errorf("recovery preparation contains parent authorization or incomplete context")
	}
	return nil
}

// NativeCommitmentRecoveryRequest filters an independently constructed remote
// commitment down to its dependent-sweep authorization. Returning nil preserves
// the existing request/response flow for protocols without this capability.
func (p *Manager) NativeCommitmentRecoveryRequest(info *RevocationInfo) (*wwire.CommitSigInfo, error) {
	if info == nil || info.Channel == nil || info.Channel.RemoteCommitment == nil {
		return nil, fmt.Errorf("missing remote commitment preparation context")
	}
	if !needsSTPNativeOtherExchange(info.Channel, info.Channel.RemoteCommitment) {
		return nil, nil
	}
	if info.OldChannel == nil || info.Channel.RemoteCommitment.CommitTx == nil ||
		info.OldChannel.ChannelId != info.Channel.ChannelId || info.OldChannel.CommitHeight != info.Channel.CommitHeight {
		return nil, fmt.Errorf("missing safe previous channel for native recovery preparation")
	}
	batch, err := NewAtomicWriteBatch(p.db)
	if err != nil {
		return nil, err
	}
	batch.Close()
	source := info.RemoteCommitInfo.CommitSigInfo
	request := &wwire.CommitSigInfo{
		RecoveryOnly: true, RecoveryCommitTxID: info.Channel.RemoteCommitment.CommitTx.TxID(),
		CommitAssetProofs:    cloneSTPAssetProofs(source.CommitAssetProofs),
		CommitOtherPrevTxSig: source.CommitOtherPrevTxSig, CommitOtherTxSig: source.CommitOtherTxSig,
	}
	return request, ValidateNativeRecoveryOnlyRequest(request)
}

func cloneSTPRecoveryChannel(channel *Channel) (*Channel, error) {
	if channel == nil {
		return nil, fmt.Errorf("missing recovery channel")
	}
	raw, err := EncodeToBytes(&channel.ChannelInDB)
	if err != nil {
		return nil, err
	}
	var stored ChannelInDB
	if err := DecodeFromBytes(raw, &stored); err != nil {
		return nil, err
	}
	return &Channel{ChannelInDB: stored, PeerRPC: channel.PeerRPC, manager: channel.manager, localWallet: channel.localWallet}, nil
}

// PrepareNativeCommitmentRecovery signs only dependent Other transactions.
// The serving operation supplies its independently updated candidate; this
// function clones it before construction and does not advance any live state.
func (p *Manager) PrepareNativeCommitmentRecovery(info *RevocationInfo, request *wwire.CommitSigInfo) ([][]string, error) {
	if err := ValidateNativeRecoveryOnlyRequest(request); err != nil {
		return nil, err
	}
	if info == nil || info.Channel == nil || info.Channel.CommitHeight < 0 || p.GetWallet() == nil ||
		!needsSTPNativeOtherExchange(info.Channel, info.Channel.LocalCommitment) {
		return nil, fmt.Errorf("channel does not support native recovery preparation")
	}
	channel, err := cloneSTPRecoveryChannel(info.Channel)
	if err != nil {
		return nil, err
	}
	secret := p.GetWallet().GetCommitSecret(channel.PeerNodeId, uint32(channel.CommitHeight+1))
	if secret == nil {
		return nil, fmt.Errorf("missing next commitment construction key")
	}
	var reveal *secp256k1.PrivateKey
	if len(info.RevealPrivKey) != 0 {
		reveal, _ = btcec.PrivKeyFromBytes(info.RevealPrivKey)
	}
	bootstrap := p.GetBootstrapNodePaymentPubKey()
	keys := DeriveCommitmentKeys(secret.PubKey(), 0, bootstrap, reveal, channel)
	tx, _, _, others, err := p.CreateCommitTxWithOthers(0, channel, keys, bootstrap, info.FeeRate)
	if err != nil {
		return nil, err
	}
	if tx.TxID() != request.RecoveryCommitTxID || !equalSTPAssetProofs(channel.LocalCommitment.AssetProofs, request.CommitAssetProofs) {
		return nil, fmt.Errorf("recovery preparation differs from the independently constructed commitment")
	}
	if err := p.verifySTPChannelProofs(channel, tx, channel.LocalCommitment.AssetProofs, STPOpCommit); err != nil {
		return nil, err
	}
	if _, err := p.signAndVerifyCommitmentOthers(channel, tx, nil, others, request.CommitOtherPrevTxSig, request.CommitOtherTxSig); err != nil {
		return nil, err
	}
	channel.LocalCommitment.CommitTx, channel.LocalCommitment.Others = tx, others
	return encodeSTPNativeOtherTransactions(channel)
}

type stpNativePreparedRecoveryRecord struct {
	Version    uint32
	ChannelID  string
	Height     uint64
	CommitTxID string
	OldChannel *ChannelInDB
	Candidate  *ChannelInDB
}

func stpNativePreparedRecoveryKey(channel *Channel) []byte {
	return []byte(fmt.Sprintf("%sstp-native-prepared-recovery-%s-%d", GetDBKeyPrefix(), channel.ChannelId, channel.CommitHeight+1))
}

// AcceptNativeCommitmentRecovery is the durable barrier before the normal
// request can release a parent, splicing, deanchor or revocation signature.
func (p *Manager) AcceptNativeCommitmentRecovery(info *RevocationInfo, response *wwire.CommitSigInfo) error {
	request, err := p.NativeCommitmentRecoveryRequest(info)
	if err != nil {
		return err
	}
	if request == nil || response == nil || !response.RecoveryOnly || response.RecoveryCommitTxID != request.RecoveryCommitTxID ||
		len(response.CommitSig) != 0 || len(response.CommitDeAnchorSig) != 0 || len(response.CommitPrevTxSig) != 0 ||
		len(response.CommitNextTxSig) != 0 || len(response.CommitOtherPrevTxSig) != 0 || len(response.CommitOtherTxSig) != 0 ||
		len(response.CommitAssetProofs) != 0 {
		return fmt.Errorf("invalid native recovery preparation response")
	}
	return p.acceptSTPNativeOtherTransactionsWithJournal(info.Channel, response.NativeOtherTxs,
		func(candidate *Channel) ([]byte, []byte, error) {
			key := stpNativePreparedRecoveryKey(candidate)
			record := stpNativePreparedRecoveryRecord{Version: 1, ChannelID: candidate.ChannelId,
				Height: uint64(candidate.CommitHeight) + 1, CommitTxID: response.RecoveryCommitTxID,
				OldChannel: &info.OldChannel.ChannelInDB, Candidate: &candidate.ChannelInDB}
			previous, err := p.db.Read(key)
			if err == nil {
				var existing stpNativePreparedRecoveryRecord
				if err := DecodeFromBytes(previous, &existing); err != nil {
					return nil, nil, err
				}
				if existing.Version != record.Version || existing.ChannelID != record.ChannelID || existing.Height != record.Height ||
					existing.CommitTxID != record.CommitTxID || existing.Candidate == nil ||
					existing.Candidate.AssetEntropy != candidate.AssetEntropy || !equalSTPAssetProofs(existing.Candidate.AssetFunding, candidate.AssetFunding) ||
					!equalSTPCommitmentPackage(existing.Candidate.RemoteCommitment, candidate.RemoteCommitment) {
					return nil, nil, fmt.Errorf("cannot replace an unresolved native parent authorization")
				}
				return key, previous, nil
			} else if !errors.Is(err, indexer.ErrKeyNotFound) {
				return nil, nil, err
			}
			raw, err := EncodeToBytes(&record)
			return key, raw, err
		})
}
