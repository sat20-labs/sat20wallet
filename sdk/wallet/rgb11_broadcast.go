package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

const rgb11StatusBroadcastAttempted = "broadcast-attempted"

var ErrRGB11BroadcastResultUnknown = errors.New("RGB11 transaction broadcast result is unknown")

// RGB11BroadcastResultUnknownError means the signed transaction may already
// have reached the Bitcoin network, but the backend response did not establish
// a reliable result. Callers must retain TxID and must not treat this error as
// proof that rebroadcast or cancellation is safe.
type RGB11BroadcastResultUnknownError struct {
	TxID string
	Err  error
}

func (e *RGB11BroadcastResultUnknownError) Error() string {
	if e == nil {
		return ErrRGB11BroadcastResultUnknown.Error()
	}
	return fmt.Sprintf("%s: txid=%s: %v", ErrRGB11BroadcastResultUnknown, e.TxID, e.Err)
}

func (e *RGB11BroadcastResultUnknownError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *RGB11BroadcastResultUnknownError) Is(target error) bool {
	return target == ErrRGB11BroadcastResultUnknown
}

func rgb11BroadcastCompleteStatus(status string) bool {
	switch status {
	case "broadcast", "pending", "settled":
		return true
	default:
		return false
	}
}

func sameRGB11ChannelSendIntent(left, right *rgb11wallet.ChannelSendData) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ChannelID == right.ChannelID && left.Reason == right.Reason &&
		left.PayFeeByLocal == right.PayFeeByLocal &&
		bytes.Equal(left.WitnessScript, right.WitnessScript) &&
		bytes.Equal(left.PeerPubKey, right.PeerPubKey) && bytes.Equal(left.MoreData, right.MoreData)
}

func sameRGB11BroadcastOperation(left, right *rgb11wallet.PendingTransfer) bool {
	if left == nil || right == nil {
		return left == right
	}
	a, b := left.State, right.State
	return a.TransferID == b.TransferID && a.Direction == b.Direction &&
		a.TransportMode == b.TransportMode && a.AddressMode == b.AddressMode &&
		a.BatchID == b.BatchID && a.WitnessTxID == b.WitnessTxID &&
		a.ConsignmentHash == b.ConsignmentHash && a.RecipientID == b.RecipientID &&
		a.SenderAccountID == b.SenderAccountID && a.ReceiverAccountID == b.ReceiverAccountID &&
		a.AddressMessageID == b.AddressMessageID &&
		slices.Equal(a.BatchTransferIDs, b.BatchTransferIDs) &&
		slices.Equal(a.InputOutPoints, b.InputOutPoints) &&
		slices.Equal(a.OutputOutPoints, b.OutputOutPoints) &&
		left.ReservationID == right.ReservationID &&
		left.RecipientObjectHash == right.RecipientObjectHash &&
		left.LocalObjectHash == right.LocalObjectHash &&
		sameRGB11ChannelSendIntent(left.ChannelSend, right.ChannelSend)
}

func sameRGB11BroadcastIntent(left, right *rgb11wallet.PendingTransfer) bool {
	if left == nil || right == nil {
		return left == right
	}
	return sameRGB11BroadcastOperation(left, right) &&
		bytes.Equal(left.SignedTx, right.SignedTx) &&
		bytes.Equal(left.SignedPSBT, right.SignedPSBT) &&
		sameRGB11ChannelSend(left.ChannelSend, right.ChannelSend)
}

func cloneRGB11BroadcastIntent(pending *rgb11wallet.PendingTransfer) *rgb11wallet.PendingTransfer {
	if pending == nil {
		return nil
	}
	cloned := *pending
	cloned.State.BatchTransferIDs = append([]string(nil), pending.State.BatchTransferIDs...)
	cloned.State.InputOutPoints = append([]string(nil), pending.State.InputOutPoints...)
	cloned.State.OutputOutPoints = append([]string(nil), pending.State.OutputOutPoints...)
	cloned.SignedTx = append([]byte(nil), pending.SignedTx...)
	cloned.SignedPSBT = append([]byte(nil), pending.SignedPSBT...)
	if pending.ChannelSend != nil {
		channel := *pending.ChannelSend
		channel.WitnessScript = append([]byte(nil), pending.ChannelSend.WitnessScript...)
		channel.PeerPubKey = append([]byte(nil), pending.ChannelSend.PeerPubKey...)
		channel.MoreData = append([]byte(nil), pending.ChannelSend.MoreData...)
		cloned.ChannelSend = &channel
	}
	return &cloned
}

func validRGB11SigningTransition(unsigned, signed *rgb11wallet.PendingTransfer) bool {
	if unsigned == nil || signed == nil || !sameRGB11BroadcastOperation(unsigned, signed) || len(unsigned.SignedPSBT) == 0 ||
		!bytes.Equal(unsigned.SignedPSBT, signed.SignedPSBT) {
		return false
	}
	if signed.State.Status != rgb11StatusBroadcastAttempted && !rgb11BroadcastCompleteStatus(signed.State.Status) {
		return false
	}
	if unsigned.ChannelSend != nil && (unsigned.ChannelSend.Signed || signed.ChannelSend == nil || !signed.ChannelSend.Signed) {
		return false
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(unsigned.SignedPSBT), false)
	if err != nil || packet == nil || packet.UnsignedTx == nil ||
		packet.UnsignedTx.TxID() != unsigned.State.WitnessTxID {
		return false
	}
	unsignedTx, err := decodeRGB11BroadcastTx(unsigned.SignedTx)
	if err != nil || unsignedTx.HasWitness() || unsignedTx.TxID() != unsigned.State.WitnessTxID ||
		!sameRGB11UnsignedTx(packet.UnsignedTx, unsignedTx) {
		return false
	}
	signedTx, err := decodeRGB11BroadcastTx(signed.SignedTx)
	if err != nil || !signedTx.HasWitness() || signedTx.TxID() != unsigned.State.WitnessTxID ||
		!sameRGB11UnsignedTx(unsignedTx, signedTx) {
		return false
	}
	if len(packet.Inputs) != len(packet.UnsignedTx.TxIn) || len(signedTx.TxIn) != len(packet.Inputs) {
		return false
	}
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for i, input := range packet.UnsignedTx.TxIn {
		if packet.Inputs[i].WitnessUtxo == nil ||
			input.PreviousOutPoint != signedTx.TxIn[i].PreviousOutPoint ||
			input.Sequence != signedTx.TxIn[i].Sequence {
			return false
		}
		prevFetcher.AddPrevOut(input.PreviousOutPoint, packet.Inputs[i].WitnessUtxo)
	}
	return VerifySignedTx(signedTx, prevFetcher) == nil
}

func rgb11PendingHasWitness(pending *rgb11wallet.PendingTransfer) bool {
	if pending == nil {
		return false
	}
	tx, err := decodeRGB11BroadcastTx(pending.SignedTx)
	return err == nil && tx.HasWitness() && (pending.ChannelSend == nil || pending.ChannelSend.Signed)
}

func cloneRGB11PendingBatch(pending []*rgb11wallet.PendingTransfer) []*rgb11wallet.PendingTransfer {
	result := make([]*rgb11wallet.PendingTransfer, len(pending))
	for i, value := range pending {
		result[i] = cloneRGB11BroadcastIntent(value)
	}
	return result
}

func decodeRGB11BroadcastTx(raw []byte) (*wire.MsgTx, error) {
	if len(raw) == 0 {
		return nil, ErrRGB11Inconsistent
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	return tx, nil
}

func sameRGB11UnsignedTx(left, right *wire.MsgTx) bool {
	if left == nil || right == nil || left.TxID() != right.TxID() {
		return false
	}
	var leftRaw, rightRaw bytes.Buffer
	return left.SerializeNoWitness(&leftRaw) == nil && right.SerializeNoWitness(&rightRaw) == nil &&
		bytes.Equal(leftRaw.Bytes(), rightRaw.Bytes())
}

// reloadRGB11BroadcastIntent prevents a stack-local pending pointer from
// surviving a stable account sync that imported a cancellation or another
// device's newer phase. The CAS barrier proves the account bundle; this reload
// verifies that the exact tx/batch still has a broadcastable phase locally.
func (p *rgb11Manager) reloadRGB11BroadcastIntent(expected []*rgb11wallet.PendingTransfer) (
	[]*rgb11wallet.PendingTransfer, bool, error) {
	return p.reloadRGB11BroadcastIntentWithSigning(expected, false)
}

func (p *rgb11Manager) reloadRGB11BroadcastIntentWithSigning(
	expected []*rgb11wallet.PendingTransfer, allowSigning bool,
) ([]*rgb11wallet.PendingTransfer, bool, error) {
	current := make([]*rgb11wallet.PendingTransfer, 0, len(expected))
	allComplete := len(expected) != 0
	for _, old := range expected {
		if old == nil || old.State.TransferID == "" {
			return nil, false, ErrRGB11Inconsistent
		}
		pending, err := p.projectionStore.LoadPendingTransfer(old.State.TransferID)
		if err != nil {
			return nil, false, err
		}
		sameIntent := sameRGB11BroadcastIntent(old, pending)
		if !sameIntent && allowSigning {
			sameIntent = validRGB11SigningTransition(old, pending)
		}
		if !sameIntent {
			return nil, false, ErrRGB11Inconsistent
		}
		if rgb11BroadcastCompleteStatus(pending.State.Status) {
			current = append(current, pending)
			continue
		}
		allComplete = false
		if pending.State.Status != rgb11StatusBroadcastAttempted {
			return nil, false, ErrRGB11Inconsistent
		}
		current = append(current, pending)
	}
	return current, allComplete, nil
}

// broadcastRGB11PendingBatch persists an irreversible broadcast intent before
// calling the backend. All outcomes are keyed to the locally computed witness
// txid; a backend error or mismatched returned txid can never roll the state
// back to a safely cancellable pre-broadcast status.
func (p *rgb11Manager) broadcastRGB11PendingBatch(
	pendingList []*rgb11wallet.PendingTransfer,
	onBroadcast func(*rgb11wallet.PendingTransfer),
) (string, error) {
	if p == nil || p.projectionStore == nil || p.evidence == nil || len(pendingList) == 0 {
		return "", ErrRGB11Inconsistent
	}
	first := pendingList[0]
	if first == nil || first.State.WitnessTxID == "" || len(first.SignedTx) == 0 {
		return "", ErrRGB11Inconsistent
	}
	expectedTxID := first.State.WitnessTxID
	allComplete := true
	intentChanged := false
	intentBaseline := cloneRGB11PendingBatch(pendingList)
	for _, pending := range pendingList {
		if pending == nil || pending.State.TransferID == "" ||
			pending.State.WitnessTxID != expectedTxID ||
			!bytes.Equal(pending.SignedTx, first.SignedTx) {
			return "", ErrRGB11Inconsistent
		}
		if err := validateRGB11PendingTransaction(pending); err != nil {
			return "", err
		}
		if rgb11BroadcastCompleteStatus(pending.State.Status) {
			continue
		}
		allComplete = false
		if pending.State.Status != rgb11StatusBroadcastAttempted {
			pending.State.Status = rgb11StatusBroadcastAttempted
			intentChanged = true
		}
	}
	if allComplete {
		if !rgb11PendingHasWitness(first) {
			if err := p.recoverRGB11BroadcastWitness(pendingList); err != nil {
				return expectedTxID, &RGB11BroadcastResultUnknownError{TxID: expectedTxID, Err: err}
			}
		}
		return expectedTxID, nil
	}
	if intentChanged {
		if err := p.projectionStore.CompareAndSavePendingTransferStates(intentBaseline, pendingList); err != nil {
			// No backend call has occurred, so a normal persistence error is
			// sufficient and the caller may safely retry after repairing storage.
			return "", err
		}
		if err := p.autoBackupRGB11AfterMutation(); err != nil {
			return "", err
		}
	}
	// The prepared transaction and every transition dependency must be accepted
	// by the account's CoreNode before the first irreversible backend call.
	if owner := p.accountManagementOwner(); owner != nil {
		if err := p.syncRGB11OperationRecovery(context.Background()); err != nil {
			return "", err
		}
	}
	currentPending, allComplete, reloadErr := p.reloadRGB11BroadcastIntent(pendingList)
	if reloadErr != nil {
		return "", reloadErr
	}
	pendingList = currentPending
	if allComplete {
		first = pendingList[0]
		if !rgb11PendingHasWitness(first) {
			if err := p.recoverRGB11BroadcastWitness(pendingList); err != nil {
				return expectedTxID, &RGB11BroadcastResultUnknownError{TxID: expectedTxID, Err: err}
			}
		}
		return expectedTxID, nil
	}
	first = pendingList[0]
	if first.State.WitnessTxID != expectedTxID {
		return "", ErrRGB11Inconsistent
	}

	// A retry after an ambiguous response first resolves the locally computed
	// witness txid. This avoids treating an already accepted transaction as a
	// fresh operation merely because the previous response was lost.
	_, visible := p.expectedRGB11TransactionStatus(first)
	var backendTxID string
	var broadcastErr error
	unsignedTx, decodeErr := decodeRGB11BroadcastTx(first.SignedTx)
	if decodeErr != nil {
		return "", decodeErr
	}
	if !visible && (!unsignedTx.HasWitness() || first.ChannelSend != nil && !first.ChannelSend.Signed) {
		signBaseline := make([]*rgb11wallet.PendingTransfer, 0, len(pendingList))
		for _, pending := range pendingList {
			signBaseline = append(signBaseline, cloneRGB11BroadcastIntent(pending))
		}
		if first.ChannelSend != nil {
			broadcastErr = p.signRGB11ChannelBatch(pendingList)
		} else {
			broadcastErr = p.signRGB11OrdinaryBatch(pendingList)
		}
		if broadcastErr == nil {
			// Signing changes the private pending payload after the initial
			// intent barrier. Persist that exact signed witness under the same
			// account CAS before making the local backend broadcast attempt.
			if owner := p.accountManagementOwner(); owner != nil {
				broadcastErr = p.syncRGB11OperationRecovery(context.Background())
			}
			if broadcastErr == nil {
				var allCompleteAfterSign bool
				pendingList, allCompleteAfterSign, broadcastErr =
					p.reloadRGB11BroadcastIntentWithSigning(signBaseline, true)
				if broadcastErr == nil && allCompleteAfterSign {
					// A peer or another device may already have completed this exact
					// witness while joint signing was in flight.
					visible = true
				}
				if broadcastErr == nil {
					first = pendingList[0]
				}
			}
		}
	}
	if !visible && broadcastErr == nil {
		broadcastErr = p.checkRGB11OperationSource()
		if broadcastErr == nil {
			backendTxID, broadcastErr = p.evidence.Broadcast(first.SignedTx)
		}
		if broadcastErr == nil && backendTxID != expectedTxID {
			broadcastErr = fmt.Errorf("RGB11 backend returned witness txid %s, expected %s",
				backendTxID, expectedTxID)
		}
	}
	if broadcastErr != nil {
		// The response may have been lost after the backend accepted the raw
		// transaction. Resolve immediately when evidence already sees it;
		// otherwise retain the durable ambiguous state for reconciliation.
		if _, visible = p.expectedRGB11TransactionStatus(first); !visible {
			backupErr := p.autoBackupRGB11AfterMutation()
			p.scheduleRGB11ChainReconciliation()
			return expectedTxID, &RGB11BroadcastResultUnknownError{
				TxID: expectedTxID, Err: errors.Join(broadcastErr, backupErr),
			}
		}
	}
	if !rgb11PendingHasWitness(first) {
		if err := p.recoverRGB11BroadcastWitness(pendingList); err != nil {
			return expectedTxID, &RGB11BroadcastResultUnknownError{TxID: expectedTxID, Err: err}
		}
	}

	completionBaseline := cloneRGB11PendingBatch(pendingList)
	for _, pending := range pendingList {
		if !rgb11BroadcastCompleteStatus(pending.State.Status) {
			pending.State.Status = "broadcast"
			if onBroadcast != nil {
				onBroadcast(pending)
			}
		}
	}
	if err := p.projectionStore.CompareAndSavePendingTransferStates(completionBaseline, pendingList); err != nil {
		backupErr := p.autoBackupRGB11AfterMutation()
		p.scheduleRGB11ChainReconciliation()
		return expectedTxID, &RGB11BroadcastPersistenceError{TxID: expectedTxID, Err: errors.Join(err, backupErr)}
	}
	backupErr := p.autoBackupRGB11AfterMutation()
	p.scheduleRGB11ChainReconciliation()
	if backupErr != nil {
		return expectedTxID, &RGB11BroadcastPersistenceError{TxID: expectedTxID, Err: backupErr}
	}
	return expectedTxID, nil
}

// signRGB11OrdinaryBatch shares the channel path's durable broadcast boundary.
// Signing keys are rederived from the existing ownership proofs, including
// Tapret/internal receive carriers, so cold recovery needs no extra metadata.
func (p *rgb11Manager) signRGB11OrdinaryBatch(batch []*rgb11wallet.PendingTransfer) error {
	if len(batch) == 0 || p.wallet == nil {
		return ErrRGB11Inconsistent
	}
	first := batch[0]
	for _, item := range batch {
		if item == nil || item.ChannelSend != nil || item.State.Status != rgb11StatusBroadcastAttempted ||
			item.State.WitnessTxID != first.State.WitnessTxID || !bytes.Equal(item.SignedTx, first.SignedTx) || !bytes.Equal(item.SignedPSBT, first.SignedPSBT) {
			return ErrRGB11Inconsistent
		}
		switch item.State.TransportMode {
		case "out-of-band":
			if item.State.AckStatus != "accepted-out-of-band" {
				return ErrRGB11BatchAckRequired
			}
		case RGB11AddressTransport:
			if !item.State.DeliveryAcknowledged || item.State.AckStatus != "accepted" || item.State.DeliveryRecordKey == "" || item.State.DeliveryRecordHash == "" {
				return ErrRGB11AddressDeliveryRequired
			}
		case RGB11ProxyTransport:
			if item.State.AckStatus != "accepted" {
				return ErrRGB11BatchAckRequired
			}
		default:
			return ErrRGB11Inconsistent
		}
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(first.SignedPSBT), false)
	if err != nil {
		return err
	}
	unsigned, err := decodeRGB11BroadcastTx(first.SignedTx)
	if err != nil || unsigned.HasWitness() || !sameRGB11UnsignedTx(unsigned, packet.UnsignedTx) || len(packet.Inputs) != len(unsigned.TxIn) {
		return ErrRGB11Inconsistent
	}
	proofs, err := p.projectionStore.ListProofs()
	if err != nil {
		return err
	}
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return err
	}
	keys := make(map[int]RGB11InputSigningKey)
	prev := txscript.NewMultiPrevOutFetcher(nil)
	for i, input := range unsigned.TxIn {
		utxo := packet.Inputs[i].WitnessUtxo
		if utxo == nil || len(input.SignatureScript) != 0 {
			return ErrRGB11Inconsistent
		}
		key := RGB11InputSigningKey{Index: p.wallet.GetSubAccount()}
		if !bytes.Equal(utxo.PkScript, walletScript) {
			found := false
			for _, proof := range proofs {
				if proof == nil || proof.OutPoint != input.PreviousOutPoint.String() || proof.CarrierBinding == nil {
					continue
				}
				binding := proof.CarrierBinding
				if !bytes.Equal(binding.ActualPkScript, utxo.PkScript) || !p.ownsRGB11Carrier(binding, walletScript) {
					return ErrRGB11Inconsistent
				}
				candidate := RGB11InputSigningKey{}
				candidate.Change, candidate.Index = rgb11CarrierPath(binding.DerivationIndex)
				if binding.CommitmentMethod == "tapret1st" {
					if len(binding.TapretRoot) != 32 {
						return ErrRGB11Inconsistent
					}
					candidate.TaprootMerkleRoot = append([]byte(nil), binding.TapretRoot...)
				}
				if found && (key.Change != candidate.Change || key.Index != candidate.Index || !bytes.Equal(key.TaprootMerkleRoot, candidate.TaprootMerkleRoot)) {
					return ErrRGB11Inconsistent
				}
				key, found = candidate, true
			}
			if !found {
				return ErrRGB11Inconsistent
			}
		}
		keys[i] = key
		prev.AddPrevOut(input.PreviousOutPoint, utxo)
	}
	signer, ok := p.wallet.(interface {
		SignRGB11Psbt(*psbt.Packet, map[int]RGB11InputSigningKey) error
	})
	if !ok {
		return fmt.Errorf("active wallet does not support RGB11 input signing")
	}
	if err := p.checkRGB11OperationSource(); err != nil {
		return err
	}
	if err := signer.SignRGB11Psbt(packet, keys); err != nil {
		return err
	}
	if err := psbt.MaybeFinalizeAll(packet); err != nil {
		return err
	}
	signed, err := psbt.Extract(packet)
	if err != nil {
		return err
	}
	if !sameRGB11UnsignedTx(unsigned, signed) {
		return ErrRGB11Inconsistent
	}
	if err := VerifySignedTx(signed, prev); err != nil {
		return err
	}
	var raw bytes.Buffer
	if err := signed.Serialize(&raw); err != nil {
		return err
	}
	baseline := cloneRGB11PendingBatch(batch)
	for _, item := range batch {
		item.SignedTx = append([]byte(nil), raw.Bytes()...)
	}
	return p.projectionStore.CompareAndSavePendingTransferStates(baseline, batch)
}
