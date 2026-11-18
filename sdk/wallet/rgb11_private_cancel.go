package wallet

import (
	"bytes"
	"fmt"
	"slices"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func unpublishedRGB11Batch(local, base *rgb11wallet.RecoveryPackage, transferID string) ([]*rgb11wallet.PendingTransfer, error) {
	if local == nil || base == nil || local.WalletID != base.WalletID || local.AccountIndex != base.AccountIndex ||
		local.EngineBuildID != base.EngineBuildID || !sameRGB11SnapshotRecords(local.EngineRecords, base.EngineRecords) {
		return nil, ErrRGB11Inconsistent
	}
	pendings, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(local.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*rgb11wallet.PendingTransfer, len(pendings))
	for _, pending := range pendings {
		byID[pending.State.TransferID] = pending
	}
	first := byID[transferID]
	if first == nil {
		return nil, ErrRGB11OutOfBandRequired
	}
	ids := first.State.BatchTransferIDs
	if len(ids) == 0 {
		ids = []string{transferID}
	}
	if len(ids) > 32 || len(ids) != first.State.BatchSize || len(ids) > 1 && first.State.BatchID == "" {
		return nil, ErrRGB11Inconsistent
	}
	allowed := make(map[string]bool, len(ids)*3)
	batch := make([]*rgb11wallet.PendingTransfer, 0, len(ids))
	for index, id := range ids {
		pending := byID[id]
		if pending == nil || pending.State.Direction != "send" || pending.State.Status != "prepared" ||
			pending.State.TransportMode != "out-of-band" || pending.State.AddressMode ||
			pending.State.AckStatus != "awaiting" || pending.State.DeliveryAcknowledged || pending.ChannelSend != nil ||
			pending.State.NetworkBackupRef != "" || pending.State.DKVSOperationID != "" ||
			pending.State.WitnessTxID != first.State.WitnessTxID || pending.State.BatchID != first.State.BatchID ||
			pending.State.BatchSize != len(ids) || !slices.Equal(pending.State.BatchTransferIDs, first.State.BatchTransferIDs) ||
			pending.ReservationID == "" || pending.ReservationID != first.ReservationID ||
			!bytes.Equal(pending.SignedTx, first.SignedTx) || validateRGB11PendingTransaction(pending) != nil {
			return nil, ErrRGB11OutOfBandRequired
		}
		if len(ids) > 1 && (id != rgb11BatchRecipientID(first.State.BatchID, uint32(index)) || pending.State.RecipientVout != uint32(index+1)) {
			return nil, ErrRGB11Inconsistent
		}
		if allowed["pending-"+id] {
			return nil, ErrRGB11Inconsistent
		}
		allowed["pending-"+id] = true
		allowed["object-"+pending.RecipientObjectHash] = true
		allowed["object-"+pending.LocalObjectHash] = true
		batch = append(batch, pending)
	}
	if !allowed["pending-"+transferID] {
		return nil, ErrRGB11Inconsistent
	}
	baseRecords := make(map[string][]byte, len(base.ProjectionRecords))
	for _, record := range base.ProjectionRecords {
		if allowed[record.Key] {
			return nil, ErrRGB11OutOfBandRequired
		}
		baseRecords[record.Key] = record.Value
	}
	remaining := 0
	for _, record := range local.ProjectionRecords {
		if allowed[record.Key] {
			continue
		}
		if expected, ok := baseRecords[record.Key]; !ok || !bytes.Equal(expected, record.Value) {
			return nil, fmt.Errorf("%w: private cancel would discard unrelated RGB11 recovery changes", ErrRGB11Inconsistent)
		}
		remaining++
	}
	if remaining != len(baseRecords) {
		return nil, ErrRGB11Inconsistent
	}
	return batch, nil
}
