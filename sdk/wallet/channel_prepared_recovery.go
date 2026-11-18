package wallet

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func (p *Manager) readSTPPreparedRecovery(channel *Channel) (*stpPreparedRecoveryRecord, error) {
	if channel == nil || p.db == nil || channel.CommitHeight < 0 {
		return nil, nil
	}
	raw, err := p.db.Read(stpPreparedRecoveryKey(channel))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record stpPreparedRecoveryRecord
	if err := DecodeFromBytes(raw, &record); err != nil {
		return nil, err
	}
	if record.Version != 1 || record.ChannelID != channel.ChannelId || record.Height != uint64(channel.CommitHeight)+1 ||
		record.OldChannel == nil || record.Candidate == nil || record.OldChannel.ChannelId != record.ChannelID ||
		record.Candidate.ChannelId != record.ChannelID || record.Candidate.FundingTime != channel.FundingTime || record.OldChannel.FundingTime != channel.FundingTime || record.OldChannel.CommitHeight != channel.CommitHeight ||
		record.Candidate.CommitHeight != channel.CommitHeight || record.Candidate.RemoteCommitment == nil ||
		record.Candidate.RemoteCommitment.CommitTx == nil || record.Candidate.RemoteCommitment.DeAnchorTx == nil ||
		record.Candidate.RemoteCommitment.CommitTx.TxID() != record.CommitTxID {
		return nil, fmt.Errorf("channel prepared recovery has inconsistent channel identity")
	}
	if record.OldChannel.GetLocalPubKey() == nil || record.OldChannel.GetRemotePubKey() == nil ||
		channel.GetLocalPubKey() == nil || channel.GetRemotePubKey() == nil ||
		!record.OldChannel.GetLocalPubKey().IsEqual(channel.GetLocalPubKey()) ||
		!record.OldChannel.GetRemotePubKey().IsEqual(channel.GetRemotePubKey()) ||
		!bytes.Equal(record.OldChannel.RedeemScript, channel.RedeemScript) {
		return nil, fmt.Errorf("channel prepared recovery changed channel ownership")
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
		return nil, fmt.Errorf("channel prepared candidate changed immutable channel identity")
	}
	candidate := &Channel{ChannelInDB: *record.Candidate}
	if !needsRecoveryExchange(candidate, candidate.RemoteCommitment) {
		return nil, fmt.Errorf("prepared recovery has no protocol obligation")
	}
	// The parent may have only our signature, but every dependent sweep must
	// already be independently broadcastable once that exact parent confirms.
	check := *candidate
	check.LocalCommitment = candidate.RemoteCommitment
	if _, err := encodeSTPOtherTransactions(&check); err != nil {
		return nil, err
	}
	// Validate the separate dormant RGB obligation alongside, without changing
	// which legacy Other packages the existing ARC reader accepts or consumes.
	if _, err := encodeRGBClientRecovery(&check, check.LocalCommitment); err != nil {
		return nil, err
	}
	return &record, nil
}

// Keep the old funding generation under observation until the prepared peer
// parent confirms. Local force-close independently selects the unrevoked exit.
func (p *Manager) LoadSTPRevocationRecovery(stored *ChannelInDB) (*ChannelInDB, error) {
	prepared, err := p.LoadSTPPreparedRecovery(stored)
	if err != nil || prepared != nil {
		return prepared, err
	}
	return p.loadSTPRevocationRecovery(stored)
}

func preparedRecoveryTerminal(status ChannelStatus) bool {
	return status <= CS_CLOSED || status >= CS_CLOSE_FORCELY_BROADCASTED && status <= CS_CLOSE_FORCELY_SWEEP_CONFIRMED
}

func (p *Manager) LoadSTPPreparedRecovery(stored *ChannelInDB) (*ChannelInDB, error) {
	if stored == nil || preparedRecoveryTerminal(stored.Status) {
		return nil, nil
	}
	record, err := p.readSTPPreparedRecovery(&Channel{ChannelInDB: *stored})
	if err != nil || record == nil {
		return nil, err
	}
	copy := *record.OldChannel
	copy.Status = CS_COMMIT_RECOVERY
	return &copy, nil
}

func (p *Manager) confirmedPreparedTxs(txs []*wire.MsgTx) ([]*wire.MsgTx, error) {
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

// RetryPreparedRecovery observes the exact peer parent before switching
// funding contexts. It never signs or asks the peer for additional material.
func (p *Manager) RetryPreparedRecovery(channel *Channel) (bool, error) {
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
	if preparedRecoveryTerminal(snapshot.Status) {
		return false, nil
	}
	record, err := p.readSTPPreparedRecovery(snapshot)
	if err != nil {
		return true, err
	}
	if record == nil {
		return false, nil
	}
	if p.GetIndexerRPCClient() == nil {
		return true, fmt.Errorf("channel prepared recovery has no Bitcoin evidence client")
	}
	if !p.GetIndexerRPCClient().IsTxConfirmed(record.CommitTxID) {
		channel.Mutex.Lock()
		if p.GetChannel(channel.ChannelId) == channel && channel.CommitHeight == snapshot.CommitHeight && channel.Status == CS_READY {
			channel.Status = CS_COMMIT_RECOVERY
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
	candidate.FundingParents, err = p.confirmedPreparedTxs(candidate.FundingParents)
	if err != nil {
		return true, err
	}
	if err := verifySTPParents(candidate); err != nil {
		return true, err
	}
	candidate.RemoteCommitment.PrevTxs, err = p.confirmedPreparedTxs(candidate.RemoteCommitment.PrevTxs)
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
	candidate.Status = CS_COMMIT_RECOVERY
	channel.Mutex.Lock()
	if p.GetChannel(channel.ChannelId) != channel || channel.CommitHeight != snapshot.CommitHeight || preparedRecoveryTerminal(channel.Status) {
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

func (p *Manager) importSTPPreparedFunding(channel *Channel) error {
	record, err := p.readSTPPreparedRecovery(channel)
	if err != nil || record == nil {
		return err
	}
	for _, protocol := range stpAssetProtocols.ordered {
		if len(channel.AssetFunding[protocol.Protocol()]) == 0 {
			continue
		}
		if rgb, ok := protocol.(rgb11STPProtocol); ok {
			rgb.prepared = record.Candidate
			protocol = rgb
		}
		if settlement, ok := protocol.(STPAssetPreparedFundingSettlement); ok {
			if err := settlement.ImportPreparedChannelFunding(p, channel, channel.FundingParents); err != nil {
				return err
			}
		}
	}
	return nil
}
