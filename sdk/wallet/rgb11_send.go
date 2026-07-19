package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/anchors"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	"github.com/sat20-labs/rgb11/operations"
	corepsbt "github.com/sat20-labs/rgb11/psbt"
	"github.com/sat20-labs/rgb11/seals"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

const rgb11CarrierValue int64 = 330

var (
	ErrRGB11InsufficientBalance = errors.New("insufficient RGB11 balance")
	ErrRGB11HistoryMerge        = errors.New("selected RGB11 allocations require a history merge")
	ErrRGB11AckRequired         = errors.New("valid recipient ACK is required before broadcast")
	ErrRGB11BatchAckRequired    = errors.New("all RGB11 batch recipient ACKs are required before broadcast")
	ErrRGB11AssetPreservation   = errors.New("RGB11 input contains another asset that cannot be preserved")
)

type RGB11SendRequest struct {
	Invoice          string   `json:"invoice,omitempty"`
	Invoices         []string `json:"invoices,omitempty"`
	FeeRate          int64    `json:"fee_rate"`
	MinConfirmations uint8    `json:"min_confirmations"`
}

type RGB11PreparedTransfer struct {
	State                *rgb11wallet.TransferState   `json:"state"`
	States               []*rgb11wallet.TransferState `json:"states,omitempty"`
	RecipientConsignment string                       `json:"recipient_consignment"`
	SignedPSBT           string                       `json:"signed_psbt"`
	TxID                 string                       `json:"txid"`
}

type rgb11SendRecipient struct {
	raw         string
	invoice     *invoicing.Invoice
	amount      uint64
	recipientID string
	relayKey    string
	ackKey      string
	script      []byte
	vout        uint32
	transport   string
}

type rgb11SpendAllocation struct {
	proof  *rgb11wallet.AllocationProof
	asset  *indexer.AssetInfo
	target bool
}

// PrepareRGB11Transfer builds one client-side state transition for one asset.
// A request may contain multiple witness invoices; all recipients share one
// Bitcoin transaction and one official RGB consignment, while transport and
// ACK state remains recipient-specific. It intentionally does not relay or
// broadcast. RBF replacement is outside the first release scope.
func (p *Manager) PrepareRGB11Transfer(ctx context.Context, request RGB11SendRequest) (*RGB11PreparedTransfer, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil || p.wallet == nil {
		return nil, ErrRGB11Inconsistent
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	invoiceTexts := append([]string(nil), request.Invoices...)
	if len(invoiceTexts) == 0 && strings.TrimSpace(request.Invoice) != "" {
		invoiceTexts = []string{request.Invoice}
	}
	if len(invoiceTexts) == 0 || len(invoiceTexts) > 32 {
		return nil, invoicing.ErrInvalidInvoice
	}
	recipients := make([]rgb11SendRecipient, 0, len(invoiceTexts))
	seenRecipient := make(map[string]struct{}, len(invoiceTexts))
	seenRelay := make(map[string]struct{}, len(invoiceTexts))
	seenAck := make(map[string]struct{}, len(invoiceTexts))
	var contractID consensus.ContractID
	var totalAmount uint64
	for index, raw := range invoiceTexts {
		trimmed := strings.TrimSpace(raw)
		invoice, err := invoicing.Parse(trimmed)
		if err != nil {
			return nil, err
		}
		if err := invoice.Validate(time.Now().Unix()); err != nil {
			return nil, err
		}
		currentContract, amount, recipientID, relayKey, ackKey, transport, err := validateRGB11SendInvoice(invoice)
		if err != nil {
			return nil, err
		}
		if index == 0 {
			contractID = currentContract
		} else if currentContract != contractID {
			return nil, invoicing.ErrInvalidInvoice
		}
		if len(invoiceTexts) > 1 && invoice.Beneficiary.Kind != invoicing.BeneficiaryWitnessVout {
			return nil, fmt.Errorf("RGB11 batch send requires witness invoices")
		}
		if _, ok := seenRecipient[recipientID]; ok {
			return nil, fmt.Errorf("RGB11 batch contains duplicate recipient")
		}
		if index > 0 && transport != recipients[0].transport {
			return nil, fmt.Errorf("RGB11 batch cannot mix transport modes")
		}
		if relayKey != "" {
			if _, ok := seenRelay[relayKey]; ok {
				return nil, fmt.Errorf("RGB11 batch contains duplicate relay key")
			}
			seenRelay[relayKey] = struct{}{}
		}
		if ackKey != "" {
			if _, ok := seenAck[ackKey]; ok {
				return nil, fmt.Errorf("RGB11 batch contains duplicate ACK key")
			}
			seenAck[ackKey] = struct{}{}
		}
		seenRecipient[recipientID] = struct{}{}
		if ^uint64(0)-totalAmount < amount {
			return nil, rgb11wallet.ErrInvalidProof
		}
		totalAmount += amount
		var script []byte
		if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
			script, err = invoice.Beneficiary.WitnessScript()
		} else {
			recipientPubKey, _ := hex.DecodeString(recipientID)
			script, err = HexPubKeyToP2TRPkScript(recipientPubKey)
		}
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, rgb11SendRecipient{
			raw: trimmed, invoice: invoice, amount: amount, recipientID: recipientID,
			relayKey: relayKey, ackKey: ackKey, script: script, vout: uint32(index + 1), transport: transport,
		})
	}
	if request.MinConfirmations == 0 {
		request.MinConfirmations = 1
	}

	selected, _, targetAsset, targetClass, targetType, targetTotal, err :=
		p.selectRGB11Allocations(contractID.String(), totalAmount, request.MinConfirmations)
	if err != nil {
		return nil, err
	}
	if len(recipients) > 1 && targetClass != "fungible" {
		return nil, fmt.Errorf("RGB11 batch send supports fungible state only")
	}
	base, parentStateHash, err := p.mergeRGB11SpendHistories(selected)
	if err != nil {
		return nil, err
	}

	reveals := make([]seals.GraphBlindSeal, 0, len(selected))
	inputs := make([]operations.TransitionInput, 0, len(selected))
	for _, allocation := range selected {
		operationID, err := hex.DecodeString(allocation.proof.OperationID)
		if err != nil || len(operationID) != 32 || allocation.proof.AssignmentType > 0xffff || allocation.proof.AssignmentIndex > 0xffff {
			return nil, rgb11wallet.ErrInvalidProof
		}
		var operation [32]byte
		copy(operation[:], operationID)
		inputs = append(inputs, operations.TransitionInput{
			OperationID: operation, AssignmentType: uint16(allocation.proof.AssignmentType),
			Index: uint16(allocation.proof.AssignmentIndex),
		})
		graph, err := seals.DecodeGraphBlindSeal(allocation.proof.SealDisclosure)
		if err != nil {
			return nil, rgb11wallet.ErrInvalidProof
		}
		reveals = append(reveals, graph)
	}
	if len(reveals) > 0 {
		if _, err := base.RevealGraphSeals(reveals); err != nil {
			return nil, err
		}
	}

	changeSeals := make([]seals.GraphBlindSeal, 0)
	changeSecrets := make([][32]byte, 0)
	recipientSecrets := make([][32]byte, 0, 1)
	outputs := make([]operations.TransitionOutput, 0, len(recipients)+1)
	var structuredData []byte
	if targetClass == "structured" {
		for _, allocation := range selected {
			if allocation.target {
				structuredData = append([]byte(nil), allocation.proof.StateData...)
				break
			}
		}
	}
	for _, recipient := range recipients {
		output := operations.TransitionOutput{
			AssignmentType: uint16(targetType), Class: targetClass, Amount: recipient.amount,
			Data: append([]byte(nil), structuredData...),
		}
		switch recipient.invoice.Beneficiary.Kind {
		case invoicing.BeneficiaryBlindedSeal:
			copy(output.SecretSeal[:], recipient.invoice.Beneficiary.BlindedSeal[:])
			recipientSecrets = append(recipientSecrets, [32]byte(recipient.invoice.Beneficiary.BlindedSeal))
		case invoicing.BeneficiaryWitnessVout:
			seal, err := seals.RandomWitnessBlindSeal(recipient.vout)
			if err != nil {
				return nil, err
			}
			output.RevealedSeal = &seal
		default:
			return nil, invoicing.ErrInvalidInvoice
		}
		outputs = append(outputs, output)
	}
	changeVout := uint32(len(recipients) + 1)
	if targetTotal > totalAmount {
		if targetClass != "fungible" {
			return nil, ErrRGB11InsufficientBalance
		}
		change, secret, err := newRGB11ChangeOutput(changeVout, uint16(targetType), targetClass, targetTotal-totalAmount, nil)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, change)
		changeSeals = append(changeSeals, secret.seal)
		changeSecrets = append(changeSecrets, secret.secret)
	}
	for _, allocation := range selected {
		if allocation.target {
			continue
		}
		stateAmount, err := decimalUint64(&allocation.asset.Amount)
		if err != nil {
			return nil, err
		}
		change, secret, err := newRGB11ChangeOutput(
			changeVout, uint16(allocation.proof.AssignmentType), allocation.proof.StateClass,
			stateAmount, allocation.proof.StateData,
		)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, change)
		changeSeals = append(changeSeals, secret.seal)
		changeSecrets = append(changeSecrets, secret.secret)
	}

	transition, transitionCommitment, err := operations.BuildTransition(operations.TransitionSpec{
		ContractID: [32]byte(contractID), Nonce: uint64(time.Now().UnixNano()),
		TransitionType: 10_000, Inputs: inputs, Outputs: outputs,
	})
	if err != nil {
		return nil, err
	}
	bundleValue, bundleCommitment, err := operations.BuildBundle(inputs, transition)
	if err != nil {
		return nil, err
	}
	mpcProof, err := anchors.NewMPCProof([32]byte(contractID), 3)
	if err != nil {
		return nil, err
	}
	mpcCommitment, err := anchors.ConvolveMPC(mpcProof, [32]byte(contractID), bundleCommitment.BundleID)
	if err != nil {
		return nil, err
	}

	recipientScripts := make([][]byte, 0, len(recipients))
	for _, recipient := range recipients {
		recipientScripts = append(recipientScripts, recipient.script)
	}
	changeScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}
	tx, prevFetcher, inputOutpoints, taprootRoots, _, err := p.buildRGB11WitnessTx(
		selected, recipientScripts, changeScript, anchors.OpretScript(mpcCommitment), request.FeeRate,
	)
	if err != nil {
		return nil, err
	}
	reserved := make([]string, 0, len(inputOutpoints))
	for _, outpoint := range inputOutpoints {
		if p.utxoLockerL1.IsLocked(outpoint) {
			continue
		}
		if err := p.utxoLockerL1.LockUtxo(outpoint, rgb11wallet.LockReasonPending); err != nil {
			for _, release := range reserved {
				_ = p.utxoLockerL1.UnlockUtxo(release)
			}
			return nil, err
		}
		reserved = append(reserved, outpoint)
	}
	reservationCommitted := false
	defer func() {
		if reservationCommitted {
			return
		}
		for _, outpoint := range reserved {
			_ = p.utxoLockerL1.UnlockUtxo(outpoint)
		}
	}()
	packet, signedTx, signedPSBT, err := p.signRGB11PSBT(
		tx, prevFetcher, [32]byte(contractID), transitionCommitment.OperationID,
		transition.Encoded, bundleCommitment.BundleID, mpcProof, mpcCommitment, inputs, taprootRoots,
	)
	if err != nil {
		return nil, err
	}
	_ = packet
	witnessBundle, err := operations.BuildOpretWitnessBundleWithTx(signedTx, bundleValue, mpcProof)
	if err != nil {
		return nil, err
	}
	recipientContainer, err := coreconsignment.BuildTransfer(base, witnessBundle, recipientSecrets)
	if err != nil {
		return nil, err
	}
	localContainer, err := coreconsignment.BuildTransfer(base, witnessBundle, changeSecrets)
	if err != nil {
		return nil, err
	}
	recipientArmor, err := coreconsignment.EncodeArmor(recipientContainer.Value)
	if err != nil {
		return nil, err
	}
	localArmor, err := coreconsignment.EncodeArmor(localContainer.Value)
	if err != nil {
		return nil, err
	}
	recipientDecoded, err := coreconsignment.DecodeArmor(recipientArmor)
	if err != nil {
		return nil, err
	}

	var signedRaw bytes.Buffer
	if err := signedTx.Serialize(&signedRaw); err != nil {
		return nil, err
	}
	objectHash := sha256.Sum256([]byte(recipientArmor))
	targetPrecision := 0
	for _, allocation := range selected {
		if allocation.target {
			targetPrecision = allocation.asset.Amount.Precision
			break
		}
	}
	batchID := recipientDecoded.Armor.ID
	transferIDs := make([]string, len(recipients))
	for index := range recipients {
		if len(recipients) == 1 {
			transferIDs[index] = batchID
			continue
		}
		child := sha256.Sum256([]byte(fmt.Sprintf("SAT20-RGB11-BATCH-RECIPIENT-V1:%s:%d", batchID, index)))
		transferIDs[index] = hex.EncodeToString(child[:])
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, len(recipients))
	states := make([]*rgb11wallet.TransferState, 0, len(recipients))
	for index, recipient := range recipients {
		state := rgb11wallet.TransferState{
			TransferID: transferIDs[index], Direction: "send",
			Asset: indexer.AssetInfo{Name: targetAsset, Amount: indexer.Decimal{
				Precision: targetPrecision, Value: new(big.Int).SetUint64(recipient.amount),
			}, BindingSat: 0},
			RecipientID: recipient.recipientID, Invoice: recipient.raw, InputOutPoints: append([]string(nil), inputOutpoints...),
			OutputOutPoints: []string{
				fmt.Sprintf("%s:%d", signedTx.TxID(), recipient.vout),
				fmt.Sprintf("%s:%d", signedTx.TxID(), changeVout),
			},
			MinConfirmations: request.MinConfirmations, Expiry: *recipient.invoice.Expiry,
			ConsignmentHash: hex.EncodeToString(objectHash[:]), WitnessTxID: signedTx.TxID(),
			AckStatus: "awaiting", Status: "prepared", RelayRecordKey: recipient.relayKey, AckRecordKey: recipient.ackKey,
			RelayDurability: "LOCAL_ONLY", RelayExpiry: *recipient.invoice.Expiry,
			ParentStateHash: parentStateHash, RecipientVout: recipient.vout, BatchSize: len(recipients),
			BatchTransferIDs: append([]string(nil), transferIDs...), TransportMode: recipient.transport,
		}
		if len(recipients) > 1 {
			state.BatchID = batchID
		}
		pending := &rgb11wallet.PendingTransfer{
			State: state, RecipientConsignment: []byte(recipientArmor), LocalConsignment: []byte(localArmor),
			SignedTx: append([]byte(nil), signedRaw.Bytes()...), SignedPSBT: append([]byte(nil), signedPSBT...),
			ChangeSeals: append([]seals.GraphBlindSeal(nil), changeSeals...), CreatedAt: time.Now().Unix(),
		}
		pendingList = append(pendingList, pending)
		states = append(states, &pending.State)
	}
	if err := p.rgbManager.projectionStore.SavePendingTransfers(pendingList); err != nil {
		return nil, err
	}
	reservationCommitted = true
	p.autoBackupRGB11AfterMutation()
	return &RGB11PreparedTransfer{
		State: states[0], States: states, RecipientConsignment: recipientArmor,
		SignedPSBT: hex.EncodeToString(signedPSBT), TxID: signedTx.TxID(),
	}, nil
}

func validateRGB11SendInvoice(invoice *invoicing.Invoice) (consensus.ContractID, uint64, string, string, string, string, error) {
	if invoice == nil || invoice.Contract == nil || invoice.Assignment == nil || invoice.Expiry == nil ||
		(invoice.Beneficiary.Kind != invoicing.BeneficiaryBlindedSeal && invoice.Beneficiary.Kind != invoicing.BeneficiaryWitnessVout) ||
		invoice.Assignment.Kind != invoicing.StateAmount {
		return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
	}
	wantNetwork := rgb11InvoiceNetwork(GetChainParam())
	if invoice.Beneficiary.Network != wantNetwork || invoice.Assignment.Amount == 0 {
		return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
	}
	values := make(map[string]string, len(invoice.UnknownQuery))
	for _, param := range invoice.UnknownQuery {
		values[param.Key] = param.Value
	}
	hasSAT20 := values["sat20_recipient"] != "" || values["sat20_relay"] != "" || values["sat20_ack"] != ""
	recipientID, relayKey, ackKey, transport := values["sat20_recipient"], values["sat20_relay"], values["sat20_ack"], "sat20-dkvs"
	if hasSAT20 {
		if recipientID == "" || relayKey == "" || ackKey == "" {
			return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
		}
		if invoice.Beneficiary.Kind == invoicing.BeneficiaryBlindedSeal && values["sat20_vout"] != "1" {
			return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
		}
		pubkey, err := hex.DecodeString(recipientID)
		if err != nil || len(pubkey) != 33 {
			return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
		}
		if _, err := HexPubKeyToP2TRPkScript(pubkey); err != nil {
			return consensus.ContractID{}, 0, "", "", "", "", err
		}
	} else {
		if len(invoice.Transports) != 0 || invoice.Beneficiary.Kind != invoicing.BeneficiaryWitnessVout {
			return consensus.ContractID{}, 0, "", "", "", "", fmt.Errorf("RGB11 external send requires an out-of-band witness invoice")
		}
		recipientID = invoice.Beneficiary.String()
		transport = "out-of-band"
	}
	if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
		if _, err := invoice.Beneficiary.WitnessScript(); err != nil {
			return consensus.ContractID{}, 0, "", "", "", "", err
		}
	}
	return *invoice.Contract, uint64(invoice.Assignment.Amount), recipientID, relayKey, ackKey, transport, nil
}

func (p *Manager) selectRGB11Allocations(contractID string, amount uint64, minConfirmations uint8) (
	[]rgb11SpendAllocation, string, indexer.AssetName, string, uint32, uint64, error,
) {
	excluded := make(map[string]struct{})
	for {
		selected, baseHash, targetAsset, targetClass, targetType, total, err :=
			p.selectRGB11AllocationsOnce(contractID, amount, minConfirmations, excluded)
		if err != nil {
			return nil, "", indexer.AssetName{}, "", 0, total, err
		}
		base, _, err := p.mergeRGB11SpendHistories(selected)
		if err != nil {
			return nil, "", indexer.AssetName{}, "", 0, 0, err
		}
		checked := make([]operations.Opout, 0, len(selected))
		byOpout := make(map[operations.Opout]*rgb11wallet.AllocationProof)
		for _, allocation := range selected {
			if !allocation.target {
				continue
			}
			opout, err := rgb11ProofOpout(allocation.proof)
			if err != nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, err
			}
			checked = append(checked, opout)
			byOpout[opout] = allocation.proof
		}
		err = p.checkRGB11RejectPolicy(base, checked)
		var violation *RGB11RejectListViolation
		if errors.As(err, &violation) {
			proof := byOpout[violation.Checked]
			if proof == nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, err
			}
			proof.PolicyStatus = "rejected"
			proof.PolicyReason = violation.Rejected.String()
			_ = p.rgbManager.projectionStore.SaveProofState(proof)
			excluded[proof.OutPoint] = struct{}{}
			continue
		}
		if err != nil {
			for _, proof := range byOpout {
				proof.PolicyStatus = "unknown"
				proof.PolicyReason = err.Error()
				_ = p.rgbManager.projectionStore.SaveProofState(proof)
			}
			return nil, "", indexer.AssetName{}, "", 0, 0, err
		}
		for _, proof := range byOpout {
			proof.PolicyStatus = "allowed"
			proof.PolicyReason = ""
			_ = p.rgbManager.projectionStore.SaveProofState(proof)
		}
		return selected, baseHash, targetAsset, targetClass, targetType, total, nil
	}
}

func (p *Manager) selectRGB11AllocationsOnce(contractID string, amount uint64, minConfirmations uint8,
	excluded map[string]struct{}) (
	[]rgb11SpendAllocation, string, indexer.AssetName, string, uint32, uint64, error,
) {
	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return nil, "", indexer.AssetName{}, "", 0, 0, err
	}
	byOutpoint := make(map[string][]*rgb11wallet.AllocationProof)
	var targets []*rgb11wallet.AllocationProof
	for _, proof := range proofs {
		if proof.Status != "valid" && proof.Status != "settled" {
			continue
		}
		byOutpoint[proof.OutPoint] = append(byOutpoint[proof.OutPoint], proof)
		official, err := rgb11wallet.OfficialAssetID(proof.AssetName)
		if err == nil && official == contractID && proof.AssignmentType == 4000 && proof.AssetName.Type != "control" {
			targets = append(targets, proof)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].OutPoint < targets[j].OutPoint })
	selectedOutpoints := make(map[string]bool)
	selected := make([]rgb11SpendAllocation, 0)
	var baseHash, targetClass string
	var targetAsset indexer.AssetName
	var targetType uint32
	var total uint64
	for _, target := range targets {
		if _, skip := excluded[target.OutPoint]; skip {
			continue
		}
		if selectedOutpoints[target.OutPoint] {
			continue
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(target.OutPoint)
		if err != nil || utxo == nil || utxo.Confirmations < int64(minConfirmations) {
			continue
		}
		group := byOutpoint[target.OutPoint]
		for _, proof := range group {
			official, err := rgb11wallet.OfficialAssetID(proof.AssetName)
			if err != nil || official != contractID {
				return nil, "", indexer.AssetName{}, "", 0, 0, ErrRGB11HistoryMerge
			}
			if baseHash == "" {
				baseHash = proof.ConsignmentHash
			}
			output, err := p.rgbManager.projectionStore.LoadOutput(proof.OutPoint)
			if err != nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, err
			}
			amount := output.GetAsset(&proof.AssetName)
			if amount == nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, rgb11wallet.ErrInvalidProof
			}
			asset := &indexer.AssetInfo{Name: proof.AssetName, Amount: *amount.Clone(), BindingSat: 0}
			isTarget := proof.AssetName == target.AssetName && proof.AssignmentType == target.AssignmentType
			selected = append(selected, rgb11SpendAllocation{proof: proof, asset: asset, target: isTarget})
			if isTarget {
				value, err := decimalUint64(&asset.Amount)
				if err != nil || ^uint64(0)-total < value {
					return nil, "", indexer.AssetName{}, "", 0, 0, rgb11wallet.ErrInvalidProof
				}
				if targetClass == "" {
					targetClass, targetType, targetAsset = proof.StateClass, proof.AssignmentType, proof.AssetName
				}
				if proof.StateClass != targetClass || proof.AssignmentType != targetType || proof.AssetName != targetAsset {
					return nil, "", indexer.AssetName{}, "", 0, 0, ErrRGB11HistoryMerge
				}
				total += value
			}
		}
		selectedOutpoints[target.OutPoint] = true
		if total >= amount {
			break
		}
	}
	if total < amount || len(selected) == 0 {
		return nil, "", indexer.AssetName{}, "", 0, total, ErrRGB11InsufficientBalance
	}
	return selected, baseHash, targetAsset, targetClass, targetType, total, nil
}

func (p *Manager) mergeRGB11SpendHistories(selected []rgb11SpendAllocation) (*coreconsignment.Container, string, error) {
	hashes := make(map[string]struct{})
	for _, allocation := range selected {
		if allocation.proof == nil || allocation.proof.ConsignmentHash == "" {
			return nil, "", rgb11wallet.ErrInvalidProof
		}
		hashes[allocation.proof.ConsignmentHash] = struct{}{}
	}
	ordered := make([]string, 0, len(hashes))
	for hash := range hashes {
		ordered = append(ordered, hash)
	}
	sort.Strings(ordered)
	containers := make([]*coreconsignment.Container, 0, len(ordered))
	stateHasher := sha256.New()
	for _, hash := range ordered {
		raw, err := p.rgbManager.projectionStore.LoadObject(hash)
		if err != nil {
			return nil, "", err
		}
		container, err := coreconsignment.Decode(raw)
		if err != nil {
			return nil, "", err
		}
		receipt, err := p.rgbManager.projectionStore.LoadValidationReceipt(hash)
		if err != nil {
			return nil, "", err
		}
		containers = append(containers, container)
		stateHasher.Write(receipt.StateHash[:])
	}
	merged, err := coreconsignment.MergeHistories(containers...)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrRGB11HistoryMerge, err)
	}
	return merged, hex.EncodeToString(stateHasher.Sum(nil)), nil
}

type rgb11ChangeSecret struct {
	seal   seals.GraphBlindSeal
	secret [32]byte
}

func newRGB11ChangeOutput(vout uint32, assignmentType uint16, class string, amount uint64, data []byte) (operations.TransitionOutput, rgb11ChangeSecret, error) {
	seal, err := seals.RandomWitnessBlindSeal(vout)
	if err != nil {
		return operations.TransitionOutput{}, rgb11ChangeSecret{}, err
	}
	secret, err := seal.Conceal()
	if err != nil {
		return operations.TransitionOutput{}, rgb11ChangeSecret{}, err
	}
	return operations.TransitionOutput{
		AssignmentType: assignmentType, Class: class, Amount: amount,
		Data: append([]byte(nil), data...), SecretSeal: [32]byte(secret),
	}, rgb11ChangeSecret{seal: seal, secret: [32]byte(secret)}, nil
}

func decimalUint64(value *indexer.Decimal) (uint64, error) {
	if value == nil || value.Value == nil || value.Value.Sign() < 0 || !value.Value.IsUint64() {
		return 0, rgb11wallet.ErrInvalidProof
	}
	return value.Value.Uint64(), nil
}

func (p *Manager) buildRGB11WitnessTx(selected []rgb11SpendAllocation, recipientScripts [][]byte, changeScript, opretScript []byte, feeRate int64) (
	*wire.MsgTx, *txscript.MultiPrevOutFetcher, []string, map[int][]byte, int64, error,
) {
	if len(recipientScripts) == 0 || len(recipientScripts) > 32 {
		return nil, nil, nil, nil, 0, invoicing.ErrInvalidInvoice
	}
	if feeRate <= 0 {
		feeRate = p.GetFeeRate()
	}
	if feeRate <= 0 {
		feeRate = 1
	}
	unique := make(map[string]int)
	inputOutpoints := make([]string, 0)
	taprootRoots := make(map[int][]byte)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	tx := wire.NewMsgTx(2)
	var inputValue int64
	addInput := func(outpoint string, value int64, pkScript []byte) (int, error) {
		if index, ok := unique[outpoint]; ok {
			return index, nil
		}
		wireOutpoint, err := wire.NewOutPointFromString(outpoint)
		if err != nil {
			return 0, err
		}
		index := len(tx.TxIn)
		txIn := wire.NewTxIn(wireOutpoint, nil, nil)
		txIn.Sequence = wire.MaxTxInSequenceNum - 2
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*wireOutpoint, &wire.TxOut{Value: value, PkScript: append([]byte(nil), pkScript...)})
		unique[outpoint] = index
		inputOutpoints = append(inputOutpoints, outpoint)
		inputValue += value
		return index, nil
	}
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}
	for _, allocation := range selected {
		view, err := p.getL1TxOutput(allocation.proof.OutPoint)
		if err != nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("resolve complete asset view for %s: %w", allocation.proof.OutPoint, err)
		}
		if view == nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("resolve complete asset view for %s: %w", allocation.proof.OutPoint, ErrRGB11Inconsistent)
		}
		for index := range view.Assets {
			assetName := &view.Assets[index].Name
			if assetName.Protocol == rgb11wallet.Protocol || indexer.IsPlainAsset(assetName) {
				continue
			}
			return nil, nil, nil, nil, 0, fmt.Errorf("%w: %s carries %s", ErrRGB11AssetPreservation,
				allocation.proof.OutPoint, assetName.String())
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.proof.OutPoint)
		if err != nil || utxo == nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("resolve RGB11 carrier %s: %w", allocation.proof.OutPoint, err)
		}
		var taprootRoot []byte
		if binding := allocation.proof.CarrierBinding; binding != nil && binding.CommitmentMethod == "tapret1st" {
			if len(binding.TapretRoot) != sha256.Size || !bytes.Equal(binding.ActualPkScript, utxo.PkScript) ||
				!p.ownsRGB11Carrier(binding, walletScript) {
				return nil, nil, nil, nil, 0, fmt.Errorf("RGB11 Tapret carrier %s is not controlled by active wallet", allocation.proof.OutPoint)
			}
			taprootRoot = append([]byte(nil), binding.TapretRoot...)
		} else if !bytes.Equal(utxo.PkScript, walletScript) {
			return nil, nil, nil, nil, 0, fmt.Errorf("RGB11 carrier %s is not controlled by active wallet", allocation.proof.OutPoint)
		}
		inputIndex, err := addInput(utxo.OutPoint, utxo.Value, utxo.PkScript)
		if err != nil {
			return nil, nil, nil, nil, 0, err
		}
		if len(taprootRoot) != 0 {
			if existing := taprootRoots[inputIndex]; len(existing) != 0 && !bytes.Equal(existing, taprootRoot) {
				return nil, nil, nil, nil, 0, rgb11wallet.ErrInvalidProof
			}
			taprootRoots[inputIndex] = taprootRoot
		}
	}
	tx.AddTxOut(wire.NewTxOut(0, opretScript))
	for _, recipientScript := range recipientScripts {
		if len(recipientScript) == 0 {
			return nil, nil, nil, nil, 0, invoicing.ErrInvalidInvoice
		}
		tx.AddTxOut(wire.NewTxOut(rgb11CarrierValue, recipientScript))
	}
	tx.AddTxOut(wire.NewTxOut(0, changeScript))
	changeIndex := len(tx.TxOut) - 1
	recipientValue := int64(len(recipientScripts)) * rgb11CarrierValue

	plain := p.l1IndexerClient.GetUtxoListWithTicker(p.wallet.GetAddress(), &indexer.ASSET_PLAIN_SAT)
	p.utxoLockerL1.Reload(p.wallet.GetAddress())
	plainIndex := len(plain) - 1
	for {
		var estimate utils.TxWeightEstimator
		for range tx.TxIn {
			estimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
		for _, output := range tx.TxOut {
			estimate.AddTxOutput(output)
		}
		fee := estimate.Fee(feeRate)
		if inputValue >= recipientValue+rgb11CarrierValue+fee {
			tx.TxOut[changeIndex].Value = inputValue - recipientValue - fee
			return tx, prevFetcher, inputOutpoints, taprootRoots, fee, nil
		}
		var candidateFound bool
		for plainIndex >= 0 {
			candidate := plain[plainIndex]
			plainIndex--
			if _, used := unique[candidate.OutPoint]; used || p.utxoLockerL1.IsLocked(candidate.OutPoint) ||
				!bytes.Equal(candidate.PkScript, walletScript) {
				continue
			}
			if _, err := addInput(candidate.OutPoint, candidate.Value, candidate.PkScript); err != nil {
				return nil, nil, nil, nil, 0, err
			}
			candidateFound = true
			break
		}
		if !candidateFound {
			return nil, nil, nil, nil, 0, fmt.Errorf("insufficient plain sats for RGB11 fee")
		}
	}
}

func (p *Manager) signRGB11PSBT(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	contractID, transitionID [32]byte, transition []byte, bundleID [32]byte,
	mpcProof anchors.MPCProof, mpcCommitment [32]byte,
	inputs []operations.TransitionInput, taprootRoots map[int][]byte) (*psbt.Packet, *wire.MsgTx, []byte, error) {
	packet, err := CreatePsbt(tx, prevFetcher, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	transitionKey, err := corepsbt.RGBTransition(transitionID).RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	closeKey, err := corepsbt.RGBCloseMethod().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	packet.Unknowns = append(packet.Unknowns,
		&psbt.Unknown{Key: transitionKey, Value: append([]byte(nil), transition...)},
		&psbt.Unknown{Key: closeKey, Value: []byte{2}}, // DbcProof::Opret
	)
	consumedKey, err := corepsbt.RGBConsumedBy(contractID).RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	consumed := make([]byte, 2+36*len(inputs))
	binary.LittleEndian.PutUint16(consumed[:2], uint16(len(inputs)))
	for index, input := range inputs {
		offset := 2 + index*36
		copy(consumed[offset:offset+32], input.OperationID[:])
		binary.LittleEndian.PutUint16(consumed[offset+32:offset+34], input.AssignmentType)
		binary.LittleEndian.PutUint16(consumed[offset+34:offset+36], input.Index)
	}
	packet.Unknowns = append(packet.Unknowns, &psbt.Unknown{Key: consumedKey, Value: consumed})
	if len(packet.Outputs) == 0 {
		return nil, nil, nil, fmt.Errorf("RGB11 PSBT has no commitment output")
	}
	messageKey, err := corepsbt.MPCMessage(contractID).RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	depthKey, err := corepsbt.MPCMinTreeDepth().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	commitmentKey, err := corepsbt.MPCCommitment().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	proofKey, err := corepsbt.MPCProof().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	opretHostKey, err := corepsbt.OpretHost().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	opretCommitmentKey, err := corepsbt.OpretCommitment().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	proofValue := make([]byte, 7+32*len(mpcProof.Path))
	binary.LittleEndian.PutUint32(proofValue[:4], mpcProof.Position)
	binary.LittleEndian.PutUint16(proofValue[4:6], mpcProof.Cofactor)
	proofValue[6] = byte(len(mpcProof.Path))
	for index, node := range mpcProof.Path {
		copy(proofValue[7+index*32:], node[:])
	}
	packet.Outputs[0].Unknowns = append(packet.Outputs[0].Unknowns,
		&psbt.Unknown{Key: messageKey, Value: append([]byte(nil), bundleID[:]...)},
		&psbt.Unknown{Key: depthKey, Value: []byte{byte(len(mpcProof.Path))}},
		&psbt.Unknown{Key: commitmentKey, Value: append([]byte(nil), mpcCommitment[:]...)},
		&psbt.Unknown{Key: proofKey, Value: proofValue},
		&psbt.Unknown{Key: opretHostKey, Value: []byte{1}},
		&psbt.Unknown{Key: opretCommitmentKey, Value: append([]byte(nil), mpcCommitment[:]...)},
	)
	if len(taprootRoots) == 0 {
		if err := p.wallet.SignPsbt(packet); err != nil {
			return nil, nil, nil, err
		}
	} else {
		signer, ok := p.wallet.(interface {
			SignPsbtWithTaprootMerkleRootsAtIndex(*psbt.Packet, map[int][]byte, uint32) error
		})
		if !ok {
			return nil, nil, nil, fmt.Errorf("active wallet does not support RGB11 Tapret signing")
		}
		if err := signer.SignPsbtWithTaprootMerkleRootsAtIndex(packet, taprootRoots, p.wallet.GetSubAccount()); err != nil {
			return nil, nil, nil, err
		}
	}
	if err := psbt.MaybeFinalizeAll(packet); err != nil {
		return nil, nil, nil, err
	}
	var encoded bytes.Buffer
	if err := packet.Serialize(&encoded); err != nil {
		return nil, nil, nil, err
	}
	finalTx, err := psbt.Extract(packet)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := VerifySignedTx(finalTx, prevFetcher); err != nil {
		return nil, nil, nil, err
	}
	return packet, finalTx, encoded.Bytes(), nil
}

func queryValue(invoice *invoicing.Invoice, key string) string {
	if invoice == nil {
		return ""
	}
	for _, param := range invoice.UnknownQuery {
		if param.Key == key {
			return param.Value
		}
	}
	return ""
}

func parseOutpointVout(outpoint string) (uint32, error) {
	separator := strings.LastIndexByte(outpoint, ':')
	if separator < 0 {
		return 0, fmt.Errorf("invalid outpoint")
	}
	value, err := strconv.ParseUint(outpoint[separator+1:], 10, 32)
	return uint32(value), err
}
