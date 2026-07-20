package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	corerelay "github.com/sat20-labs/rgb11/relay"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"time"
)

// BuildRGB11RelayRecord builds the signed temporary locator. Consignment
// bytes and private seal disclosures are deliberately excluded.
func (p *Manager) BuildRGB11RelayRecord(transferID, sourcePeerID string) (*corerelay.RelayRecord, error) {
	if p == nil || p.wallet == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, err
	}
	if pending.State.TransportMode == "out-of-band" {
		return nil, fmt.Errorf("RGB11 transfer uses out-of-band delivery")
	}
	objectHash, err := decodeRGB11Hash(pending.State.ConsignmentHash)
	if err != nil {
		return nil, err
	}
	if sourcePeerID == "" {
		sourcePeerID = "sat20-wallet"
	}
	record := &corerelay.RelayRecord{
		Version: corerelay.RecordVersion, TransferID: pending.State.TransferID,
		RecipientID: pending.State.RecipientID, ObjectHash: objectHash,
		ObjectSize:    uint64(len(pending.RecipientConsignment)),
		LocalObjectID: pending.State.ConsignmentHash, SourcePeerID: sourcePeerID,
		WitnessTxID: pending.State.WitnessTxID, AckRecordKey: pending.State.AckRecordKey,
		Expiry: pending.State.Expiry,
	}
	if err := SignRGB11RelayRecord(p.wallet, record); err != nil {
		return nil, err
	}
	return record, nil
}

func (p *Manager) RelayRGB11Transfer(client *SatsNetDKVSClient, transferID, sourcePeerID string,
	opts dkvsindexer.RecordOptions) (*corerelay.RelayRecord, *swire.DKVSRecord, error) {
	if client == nil || opts.TTL == 0 {
		return nil, nil, fmt.Errorf("RGB11 relay client and TTL are required")
	}
	record, err := p.BuildRGB11RelayRecord(transferID, sourcePeerID)
	if err != nil {
		return nil, nil, err
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, nil, err
	}
	written, err := client.PutRGB11RelayRecord(p.wallet, pending.State.RelayRecordKey, record, opts)
	if err != nil {
		return nil, nil, err
	}
	pending.State.Status = "relayed"
	pending.State.RelayDurability = "RELAYED_TEMP"
	pending.State.RelayExpiry = record.Expiry
	if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		return nil, nil, err
	}
	p.autoBackupRGB11AfterMutation()
	return record, written, nil
}

func (p *Manager) PublishRGB11RelayRecord(transferID, sourcePeerID string,
	opts dkvsindexer.RecordOptions) (*corerelay.RelayRecord, *swire.DKVSRecord, error) {
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return nil, nil, err
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, nil, err
	}
	return p.RelayRGB11Transfer(client, transferID, sourcePeerID, opts)
}

// AcceptRGB11RelayConsignment authenticates the relay envelope, validates the
// consignment locally and returns an ACK signed only by the receiving wallet.
func (p *Manager) AcceptRGB11RelayConsignment(ctx context.Context, requestID string,
	record *corerelay.RelayRecord, raw []byte) (*rgb11wallet.ValidationReceipt, *corerelay.AckRecord, error) {
	if p == nil || p.wallet == nil || p.rgbManager.engine == nil || record == nil {
		return nil, nil, ErrRGB11Inconsistent
	}
	request, err := p.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		return nil, nil, err
	}
	if request.RecipientID != record.RecipientID || record.AckRecordKey != request.AckKey {
		return nil, nil, ErrRGB11InvoiceMismatch
	}
	if err := record.Verify(record.SenderPubKey, time.Now().Unix(), rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, nil, err
	}
	hash := sha256.Sum256(raw)
	if hash != record.ObjectHash || uint64(len(raw)) != record.ObjectSize {
		return nil, nil, ErrRGB11InvoiceMismatch
	}
	receipt, err := p.acceptRGB11Consignment(ctx, requestID, raw, false)
	if err != nil {
		var violation *RGB11RejectListViolation
		if !errors.As(err, &violation) {
			return nil, nil, err
		}
		ack, nackErr := p.buildRGB11RecipientDecision(record, false, RGB11RejectReasonList)
		if nackErr != nil {
			return nil, nil, nackErr
		}
		if nackErr := p.rgbManager.projectionStore.DiscardValidatedObject(hex.EncodeToString(record.ObjectHash[:])); nackErr != nil {
			return nil, nil, nackErr
		}
		if nackErr := p.recordRGB11ReceiveRejection(requestID, request.Invoice, record,
			RGB11RejectReasonList, []string{violation.Rejected.String()}); nackErr != nil {
			return nil, nil, nackErr
		}
		return nil, ack, nil
	}
	if state, loadErr := p.rgbManager.projectionStore.LoadTransferState(receipt.TransferID); loadErr == nil {
		state.RelayDurability = "RELAYED_TEMP"
		state.RelayExpiry = record.Expiry
		if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
			return nil, nil, err
		}
		p.autoBackupRGB11AfterMutation()
	}
	ack, err := p.buildRGB11RecipientDecision(record, true, "")
	if err != nil {
		return nil, nil, err
	}
	return receipt, ack, nil
}

// RejectRGB11RelayConsignment records an explicit user refusal and returns a
// wallet-signed NACK. The consignment body is not required or persisted.
func (p *Manager) RejectRGB11RelayConsignment(requestID string,
	record *corerelay.RelayRecord) (*corerelay.AckRecord, error) {
	if p == nil || p.wallet == nil || p.rgbManager.engine == nil || record == nil {
		return nil, ErrRGB11Inconsistent
	}
	request, err := p.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		return nil, err
	}
	if request.RecipientID != record.RecipientID || request.AckKey != record.AckRecordKey {
		return nil, ErrRGB11InvoiceMismatch
	}
	if err := record.Verify(record.SenderPubKey, time.Now().Unix(), rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, err
	}
	ack, err := p.buildRGB11RecipientDecision(record, false, RGB11RejectReasonUser)
	if err != nil {
		return nil, err
	}
	if err := p.recordRGB11ReceiveRejection(requestID, request.Invoice, record,
		RGB11RejectReasonUser, nil); err != nil {
		return nil, err
	}
	return ack, nil
}

func (p *Manager) buildRGB11RecipientDecision(record *corerelay.RelayRecord,
	accepted bool, reason string) (*corerelay.AckRecord, error) {
	relayHash, err := record.Hash()
	if err != nil {
		return nil, err
	}
	ack := &corerelay.AckRecord{
		Version: corerelay.RecordVersion, TransferID: record.TransferID,
		RecipientID: record.RecipientID, RelayRecordHash: relayHash,
		ConsignmentHash: record.ObjectHash, Accepted: accepted, ReasonCode: reason,
	}
	if err := SignRGB11AckRecord(p.wallet, ack); err != nil {
		return nil, err
	}
	return ack, nil
}

func (p *Manager) recordRGB11ReceiveRejection(requestID, invoice string,
	record *corerelay.RelayRecord, reason string, rejectedOpouts []string) error {
	objectHash := hex.EncodeToString(record.ObjectHash[:])
	if err := p.rgbManager.engine.MarkRelayRejected(requestID, record.TransferID, objectHash, reason); err != nil {
		return err
	}
	state := &rgb11wallet.TransferState{
		TransferID: record.TransferID, Direction: "receive", RecipientID: record.RecipientID,
		Invoice: invoice, ConsignmentHash: objectHash, WitnessTxID: record.WitnessTxID,
		AckStatus: "rejected", Status: "rejected", RelayRecordKey: "",
		AckRecordKey: record.AckRecordKey, RelayDurability: "RELAYED_TEMP", RelayExpiry: record.Expiry,
		RejectReason: reason, RejectedOpouts: append([]string(nil), rejectedOpouts...),
	}
	if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		return err
	}
	p.autoBackupRGB11AfterMutation()
	return nil
}

func (p *Manager) RelayRGB11Ack(client *SatsNetDKVSClient, key string, ack *corerelay.AckRecord,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if p == nil || p.wallet == nil || client == nil || opts.TTL == 0 {
		return nil, ErrRGB11Inconsistent
	}
	return client.PutRGB11AckRecord(p.wallet, key, ack, opts)
}

func (p *Manager) PublishRGB11AckRecord(key string, ack *corerelay.AckRecord,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return nil, err
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, err
	}
	return p.RelayRGB11Ack(client, key, ack, opts)
}

func (p *Manager) FetchRGB11AckRecord(transferID string,
	verifyOpts dkvsindexer.RecordVerificationOptions) (*corerelay.AckRecord, *swire.DKVSRecord, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, nil, ErrRGB11Inconsistent
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, nil, err
	}
	recipientPubKey, err := hex.DecodeString(pending.State.RecipientID)
	if err != nil {
		return nil, nil, ErrRGB11AckRequired
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, nil, err
	}
	return client.GetRGB11AckRecord(pending.State.AckRecordKey, recipientPubKey, verifyOpts)
}

// BroadcastRGB11Transfer is ACK-gated. The signed witness transaction was
// already persisted by PrepareRGB11Transfer, so a process restart cannot lose
// the transfer object or its local change seals between ACK and broadcast.
func (p *Manager) BroadcastRGB11Transfer(transferID string, relayRecord *corerelay.RelayRecord,
	ack *corerelay.AckRecord) (string, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || relayRecord == nil || ack == nil {
		return "", ErrRGB11AckRequired
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return "", err
	}
	if pending.State.BatchSize > 1 {
		return "", ErrRGB11BatchAckRequired
	}
	return p.BroadcastRGB11Batch(
		[]string{transferID}, []*corerelay.RelayRecord{relayRecord}, []*corerelay.AckRecord{ack},
	)
}

// BroadcastRGB11Batch verifies an ACK from every recipient before publishing
// the single shared Bitcoin transaction. The caller must provide exactly the
// sibling transfer IDs persisted by PrepareRGB11Transfer.
func (p *Manager) BroadcastRGB11Batch(transferIDs []string, relayRecords []*corerelay.RelayRecord,
	acks []*corerelay.AckRecord) (string, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil || p.wallet == nil ||
		len(transferIDs) == 0 || len(transferIDs) != len(relayRecords) || len(transferIDs) != len(acks) {
		return "", ErrRGB11BatchAckRequired
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, len(transferIDs))
	seen := make(map[string]struct{}, len(transferIDs))
	for index, transferID := range transferIDs {
		if transferID == "" || relayRecords[index] == nil || acks[index] == nil {
			return "", ErrRGB11BatchAckRequired
		}
		if _, ok := seen[transferID]; ok {
			return "", ErrRGB11BatchAckRequired
		}
		seen[transferID] = struct{}{}
		pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
		if err != nil {
			return "", err
		}
		pendingList = append(pendingList, pending)
	}
	first := pendingList[0]
	expectedIDs := first.State.BatchTransferIDs
	if len(expectedIDs) == 0 {
		expectedIDs = []string{first.State.TransferID}
	}
	if len(expectedIDs) != len(transferIDs) || (first.State.BatchSize > 0 && first.State.BatchSize != len(transferIDs)) {
		return "", ErrRGB11BatchAckRequired
	}
	for _, expected := range expectedIDs {
		if _, ok := seen[expected]; !ok {
			return "", ErrRGB11BatchAckRequired
		}
	}
	for index, pending := range pendingList {
		if pending.State.WitnessTxID != first.State.WitnessTxID || pending.State.BatchID != first.State.BatchID ||
			pending.State.ConsignmentHash != first.State.ConsignmentHash ||
			!bytes.Equal(pending.SignedTx, first.SignedTx) {
			return "", ErrRGB11BatchAckRequired
		}
		if err := p.verifyRGB11RecipientDecision(pending, relayRecords[index], acks[index]); err != nil {
			return "", err
		}
		if !acks[index].Accepted {
			if err := p.cancelRGB11PendingBatch(pendingList, acks[index].ReasonCode, nil); err != nil {
				return "", err
			}
			return "", ErrRGB11Rejected
		}
	}
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return "", err
	}
	txID, err := p.rgbManager.evidence.Broadcast(first.SignedTx)
	if err != nil {
		return "", err
	}
	if txID != "" && txID != first.State.WitnessTxID {
		return "", fmt.Errorf("RGB11 backend returned witness txid %s, expected %s", txID, first.State.WitnessTxID)
	}
	for _, pending := range pendingList {
		pending.State.AckStatus = "accepted"
		pending.State.Status = "broadcast"
	}
	if err := p.rgbManager.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return "", err
	}
	p.autoBackupRGB11AfterMutation()
	return first.State.WitnessTxID, nil
}

// BroadcastRGB11OutOfBand is the official out-of-band ACK counterpart. The
// user calls it only after every external wallet recipient has confirmed that
// it accepted the consignment. No synthetic DKVS ACK or NACK is created.
func (p *Manager) BroadcastRGB11OutOfBand(transferIDs []string) (string, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil || len(transferIDs) == 0 {
		return "", ErrRGB11BatchAckRequired
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, len(transferIDs))
	seen := make(map[string]struct{}, len(transferIDs))
	for _, transferID := range transferIDs {
		if _, ok := seen[transferID]; transferID == "" || ok {
			return "", ErrRGB11BatchAckRequired
		}
		seen[transferID] = struct{}{}
		pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
		if err != nil {
			return "", err
		}
		pendingList = append(pendingList, pending)
	}
	first := pendingList[0]
	expectedIDs := first.State.BatchTransferIDs
	if len(expectedIDs) == 0 {
		expectedIDs = []string{first.State.TransferID}
	}
	if len(expectedIDs) != len(transferIDs) {
		return "", ErrRGB11BatchAckRequired
	}
	for _, expected := range expectedIDs {
		if _, ok := seen[expected]; !ok {
			return "", ErrRGB11BatchAckRequired
		}
	}
	for _, pending := range pendingList {
		if pending.State.TransportMode != "out-of-band" || pending.State.Expiry <= time.Now().Unix() ||
			pending.State.WitnessTxID != first.State.WitnessTxID || pending.State.BatchID != first.State.BatchID ||
			!bytes.Equal(pending.SignedTx, first.SignedTx) {
			return "", ErrRGB11BatchAckRequired
		}
		pending.State.AckStatus = "accepted-out-of-band"
	}
	if err := p.rgbManager.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return "", err
	}
	p.autoBackupRGB11AfterMutation()
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return "", err
	}
	txID, err := p.rgbManager.evidence.Broadcast(first.SignedTx)
	if err != nil {
		return "", err
	}
	if txID != "" && txID != first.State.WitnessTxID {
		return "", fmt.Errorf("RGB11 backend returned witness txid %s, expected %s", txID, first.State.WitnessTxID)
	}
	for _, pending := range pendingList {
		pending.State.Status = "broadcast"
	}
	if err := p.rgbManager.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return "", err
	}
	p.autoBackupRGB11AfterMutation()
	return first.State.WitnessTxID, nil
}

func (p *Manager) verifyRGB11RecipientAck(pending *rgb11wallet.PendingTransfer,
	relayRecord *corerelay.RelayRecord, ack *corerelay.AckRecord) error {
	if err := p.verifyRGB11RecipientDecision(pending, relayRecord, ack); err != nil || !ack.Accepted {
		return ErrRGB11AckRequired
	}
	return nil
}

func (p *Manager) verifyRGB11RecipientDecision(pending *rgb11wallet.PendingTransfer,
	relayRecord *corerelay.RelayRecord, ack *corerelay.AckRecord) error {
	if pending == nil || relayRecord == nil || ack == nil {
		return ErrRGB11AckRequired
	}
	if relayRecord.TransferID != pending.State.TransferID || relayRecord.ObjectHash != ack.ConsignmentHash ||
		ack.TransferID != pending.State.TransferID || ack.RecipientID != pending.State.RecipientID {
		return ErrRGB11AckRequired
	}
	relayHash, err := relayRecord.Hash()
	if err != nil || relayHash != ack.RelayRecordHash {
		return ErrRGB11AckRequired
	}
	recipientPubKey, err := hex.DecodeString(pending.State.RecipientID)
	if err != nil || ack.Verify(recipientPubKey, rgb11wallet.VerifyWalletSignature) != nil {
		return ErrRGB11AckRequired
	}
	if err := relayRecord.Verify(relayRecord.SenderPubKey, time.Now().Unix(), rgb11wallet.VerifyWalletSignature); err != nil {
		return err
	}
	if !bytes.Equal(relayRecord.SenderPubKey, rgb11wallet.WalletPubKey(p.wallet)) {
		return ErrRGB11AckRequired
	}
	return nil
}

// CancelRGB11BatchByNack authenticates one recipient NACK and atomically
// terminates all sibling transfers that share the unbroadcast Bitcoin tx.
func (p *Manager) CancelRGB11BatchByNack(transferID string, relayRecord *corerelay.RelayRecord,
	nack *corerelay.AckRecord) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || transferID == "" || nack == nil || nack.Accepted {
		return ErrRGB11AckRequired
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return err
	}
	if err := p.verifyRGB11RecipientDecision(pending, relayRecord, nack); err != nil {
		return err
	}
	ids := pending.State.BatchTransferIDs
	if len(ids) == 0 {
		ids = []string{pending.State.TransferID}
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, len(ids))
	for _, id := range ids {
		item, err := p.rgbManager.projectionStore.LoadPendingTransfer(id)
		if err != nil {
			return err
		}
		if item.State.BatchID != pending.State.BatchID || item.State.WitnessTxID != pending.State.WitnessTxID ||
			item.State.ConsignmentHash != pending.State.ConsignmentHash {
			return ErrRGB11BatchAckRequired
		}
		pendingList = append(pendingList, item)
	}
	return p.cancelRGB11PendingBatch(pendingList, nack.ReasonCode, nil)
}

func (p *Manager) cancelRGB11PendingBatch(pendingList []*rgb11wallet.PendingTransfer,
	reason string, rejectedOpouts []string) error {
	if len(pendingList) == 0 {
		return ErrRGB11BatchAckRequired
	}
	if reason == "" {
		reason = RGB11RejectReasonUser
	}
	ids := make([]string, 0, len(pendingList))
	inputs := make(map[string]struct{})
	for _, pending := range pendingList {
		pending.State.AckStatus = "rejected"
		pending.State.Status = "rejected"
		pending.State.RejectReason = reason
		pending.State.RejectedOpouts = append([]string(nil), rejectedOpouts...)
		ids = append(ids, pending.State.TransferID)
		for _, outpoint := range pending.State.InputOutPoints {
			inputs[outpoint] = struct{}{}
		}
	}
	if err := p.rgbManager.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return err
	}
	locked := p.utxoLockerL1.GetLockedUtxoList()
	for outpoint := range inputs {
		if item := locked[outpoint]; item != nil && item.Reason == rgb11wallet.LockReasonPending {
			if err := p.utxoLockerL1.UnlockUtxo(outpoint); err != nil {
				return err
			}
		}
	}
	if err := p.rgbManager.projectionStore.CompactRejectedTransfers(ids); err != nil {
		return err
	}
	p.autoBackupRGB11AfterMutation()
	return nil
}

func decodeRGB11Hash(value string) ([32]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return [32]byte{}, fmt.Errorf("invalid RGB11 hash")
	}
	var result [32]byte
	copy(result[:], decoded)
	return result, nil
}

func SignRGB11RelayRecord(wallet common.Wallet, record *corerelay.RelayRecord) error {
	if wallet == nil || record == nil {
		return corerelay.ErrInvalidRecord
	}
	record.SenderPubKey = rgb11wallet.WalletPubKey(wallet)
	return record.Sign(rgb11wallet.WalletSigner{Wallet: wallet})
}

func SignRGB11AckRecord(wallet common.Wallet, record *corerelay.AckRecord) error {
	if wallet == nil || record == nil {
		return corerelay.ErrInvalidRecord
	}
	record.RecipientPubKey = rgb11wallet.WalletPubKey(wallet)
	return record.Sign(rgb11wallet.WalletSigner{Wallet: wallet})
}

func (p *SatsNetDKVSClient) PutRGB11RelayRecord(wallet common.Wallet, key string, value *corerelay.RelayRecord, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if err := corerelay.ValidateTemporaryKey(key); err != nil {
		return nil, err
	}
	pubKey := rgb11wallet.WalletPubKey(wallet)
	if err := value.Verify(pubKey, time.Now().Unix(), rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, err
	}
	if opts.TTL == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	encoded, err := value.MarshalBinary()
	if err != nil {
		return nil, err
	}
	record, err := NewDKVSSignedRecord(wallet, key, encoded, opts)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) GetRGB11RelayRecord(key string, expectedSenderPubKey []byte, verifyOpts dkvsindexer.RecordVerificationOptions) (*corerelay.RelayRecord, *swire.DKVSRecord, error) {
	if err := corerelay.ValidateTemporaryKey(key); err != nil {
		return nil, nil, err
	}
	verifyOpts.ExpectedKey = key
	record, err := p.GetVerifiedRecord(key, verifyOpts)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(record.PubKey, expectedSenderPubKey) {
		return nil, nil, dkvsindexer.ErrPermissionDenied
	}
	value, err := corerelay.UnmarshalRelayRecord(record.Value)
	if err != nil {
		return nil, nil, err
	}
	if err := value.Verify(expectedSenderPubKey, time.Now().Unix(), rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, nil, err
	}
	return value, record, nil
}

func (p *SatsNetDKVSClient) PutRGB11AckRecord(wallet common.Wallet, key string, value *corerelay.AckRecord, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if err := corerelay.ValidateTemporaryKey(key); err != nil {
		return nil, err
	}
	pubKey := rgb11wallet.WalletPubKey(wallet)
	if err := value.Verify(pubKey, rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, err
	}
	if opts.TTL == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	encoded, err := value.MarshalBinary()
	if err != nil {
		return nil, err
	}
	record, err := NewDKVSSignedRecord(wallet, key, encoded, opts)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) GetRGB11AckRecord(key string, expectedRecipientPubKey []byte, verifyOpts dkvsindexer.RecordVerificationOptions) (*corerelay.AckRecord, *swire.DKVSRecord, error) {
	if err := corerelay.ValidateTemporaryKey(key); err != nil {
		return nil, nil, err
	}
	verifyOpts.ExpectedKey = key
	record, err := p.GetVerifiedRecord(key, verifyOpts)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(record.PubKey, expectedRecipientPubKey) {
		return nil, nil, dkvsindexer.ErrPermissionDenied
	}
	value, err := corerelay.UnmarshalAckRecord(record.Value)
	if err != nil {
		return nil, nil, err
	}
	if err := value.Verify(expectedRecipientPubKey, rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, nil, err
	}
	return value, record, nil
}

func (p *SatsNetDKVSClient) SubscribeRGB11Transfer(relayKey, ackKey string) ([]*swire.DKVSRecord, error) {
	if relayKey == ackKey {
		return nil, corerelay.ErrInvalidKey
	}
	if err := corerelay.ValidateTemporaryKey(relayKey); err != nil {
		return nil, err
	}
	if err := corerelay.ValidateTemporaryKey(ackKey); err != nil {
		return nil, err
	}
	relayRecords, _, err := p.SubscribeKey(relayKey)
	if err != nil {
		return nil, err
	}
	ackRecords, _, err := p.SubscribeKey(ackKey)
	if err != nil {
		return nil, err
	}
	return append(relayRecords, ackRecords...), nil
}
