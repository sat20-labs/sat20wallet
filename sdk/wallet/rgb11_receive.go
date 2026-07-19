package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/baid64"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	"github.com/sat20-labs/rgb11/operations"
	"github.com/sat20-labs/rgb11/seals"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"strconv"
	"strings"
	"time"
)

var ErrRGB11WalletLocked = errors.New("RGB11 wallet must be unlocked")

type RGB11InvoiceRequest struct {
	Mode           string `json:"mode,omitempty"`
	ContractID     string `json:"contract_id"`
	SchemaID       string `json:"schema_id"`
	AmountRaw      string `json:"amount_raw"`
	AssignmentName string `json:"assignment_name"`
	Expiry         int64  `json:"expiry"`
	WitnessVout    uint32 `json:"witness_vout"`
}

func (p *Manager) CreateRGB11Invoice(request RGB11InvoiceRequest) (*corewallet.ReceiveRequest, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil || p.wallet == nil {
		return nil, ErrRGB11WalletLocked
	}
	if request.Expiry == 0 {
		request.Expiry = time.Now().Add(24 * time.Hour).Unix()
	}
	network := rgb11InvoiceNetwork(GetChainParam())
	pubkey := p.wallet.GetPubKey()
	if pubkey == nil {
		return nil, ErrRGB11WalletLocked
	}
	amount, err := strconv.ParseUint(request.AmountRaw, 10, 64)
	if err != nil {
		return nil, err
	}
	mode := corewallet.ReceiveMode(strings.ToLower(strings.TrimSpace(request.Mode)))
	if mode == "" {
		mode = corewallet.ReceiveBlind
	}
	var witnessScript []byte
	var internalXOnly *[32]byte
	if mode == corewallet.ReceiveWitness {
		derivationIndex := p.wallet.GetSubAccount()
		address := p.wallet.GetAddressByIndex(derivationIndex)
		if address == "" || address != p.wallet.GetAddress() {
			return nil, errors.New("RGB11 witness address is not the active BIP86 subaccount")
		}
		witnessScript, err = AddrToPkScript(address, GetChainParam())
		if err != nil {
			return nil, err
		}
		internal := p.wallet.GetPubKeyByIndex(derivationIndex)
		if internal == nil {
			return nil, ErrRGB11WalletLocked
		}
		compressed := internal.SerializeCompressed()
		var xonly [32]byte
		copy(xonly[:], compressed[1:])
		internalXOnly = &xonly
	}
	receive, err := p.rgbManager.engine.CreateReceive(corewallet.ReceiveParams{
		Mode: mode, ContractID: request.ContractID, SchemaID: request.SchemaID, Network: network,
		Amount: &amount, AssignmentName: request.AssignmentName,
		RecipientID: hex.EncodeToString(pubkey.SerializeCompressed()),
		WitnessVout: request.WitnessVout, WitnessScript: witnessScript,
		InternalXOnly: internalXOnly, Expiry: request.Expiry,
	})
	if err != nil {
		return nil, err
	}
	p.autoBackupRGB11AfterMutation()
	return receive, nil
}

func rgb11InvoiceNetwork(params *chaincfg.Params) invoicing.ChainNet {
	if params == nil {
		return invoicing.BitcoinTestnet4
	}
	switch params.Net {
	case chaincfg.MainNetParams.Net:
		return invoicing.BitcoinMainnet
	case chaincfg.TestNet3Params.Net:
		return invoicing.BitcoinTestnet3
	case chaincfg.RegressionNetParams.Net:
		return invoicing.BitcoinRegtest
	case chaincfg.SigNetParams.Net:
		return invoicing.BitcoinSignet
	default:
		return invoicing.BitcoinTestnet4
	}
}

func (p *Manager) GetRGB11ReceiveRequest(requestID string) (*corewallet.ReceiveRequest, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager.engine.LoadReceive(requestID)
}

var (
	ErrRGB11InvoiceMismatch = errors.New("RGB11 consignment does not satisfy the wallet invoice")
	ErrRGB11NoAllocation    = errors.New("RGB11 consignment has no allocation for this wallet")
)

// ValidateRGB11Consignment validates and stores an immutable receipt without
// projecting any balance. It is useful for contract import and diagnostics.
func (p *Manager) ValidateRGB11Consignment(ctx context.Context, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager.projectionStore.ValidateAndStoreConsignment(ctx, rgb11wallet.NewNativeConsensusValidator(), p.rgbManager.evidence, raw)
}

// AcceptRGB11Consignment validates the complete client-side history and then
// projects only the allocation matching the wallet's pre-persisted invoice
// seal. A valid consignment for another wallet never becomes local balance.
func (p *Manager) AcceptRGB11Consignment(ctx context.Context, requestID string, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	return p.acceptRGB11Consignment(ctx, requestID, raw, true)
}

func (p *Manager) acceptRGB11Consignment(ctx context.Context, requestID string, raw []byte,
	autoBackup bool) (*rgb11wallet.ValidationReceipt, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil {
		return nil, ErrRGB11Inconsistent
	}
	request, err := p.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		return nil, err
	}
	invoice, err := invoicing.Parse(request.Invoice)
	if err != nil {
		return nil, err
	}
	if err := invoice.Validate(time.Now().Unix()); err != nil {
		return nil, err
	}
	var invoiceSeal [32]byte
	var validator rgb11wallet.ConsensusValidator
	var witnessScript []byte
	switch invoice.Beneficiary.Kind {
	case invoicing.BeneficiaryBlindedSeal:
		concealed, err := request.Seal.Conceal()
		if err != nil || !bytes.Equal(invoice.Beneficiary.BlindedSeal[:], concealed[:]) {
			return nil, ErrRGB11InvoiceMismatch
		}
		invoiceSeal = [32]byte(concealed)
		validator = rgb11wallet.NewNativeConsensusValidatorWithReveals(request.Seal)
	case invoicing.BeneficiaryWitnessVout:
		var err error
		witnessScript, err = invoice.Beneficiary.WitnessScript()
		if err != nil || !bytes.Equal(request.WitnessScript, witnessScript) {
			return nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidator()
	default:
		return nil, ErrRGB11InvoiceMismatch
	}
	receipt, err := p.rgbManager.projectionStore.ValidateAndStoreConsignment(ctx, validator, p.rgbManager.evidence, raw)
	if err != nil {
		return nil, err
	}
	if invoice.Contract != nil && invoice.Contract.String() != receipt.ContractID {
		return nil, ErrRGB11InvoiceMismatch
	}
	if invoice.Schema != nil {
		decoded, decodeErr := decodeReceiptSchema(receipt.SchemaID)
		if decodeErr != nil || decoded != *invoice.Schema {
			return nil, ErrRGB11InvoiceMismatch
		}
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, err
	}
	checked, err := p.rgb11ReceivedPolicyOpouts(receipt, request, invoice, invoiceSeal, witnessScript)
	if err != nil {
		return nil, err
	}
	if err := p.checkRGB11RejectPolicy(container, checked); err != nil {
		return nil, err
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return nil, err
	}
	projected := 0
	var receivedAsset *indexer.AssetInfo
	var receivedOutpoint string
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}
	for _, allocation := range receipt.Allocations {
		if allocation.AssignmentType != 4000 {
			continue
		}
		vout, ok := outpointVout(allocation.OutPoint)
		if !ok || !allocation.WitnessTxPtr {
			continue
		}
		candidateSeal := seals.NewWitnessBlindSeal(vout, allocation.SealBlinding)
		candidateSecret, concealErr := candidateSeal.Conceal()
		if concealErr != nil {
			return nil, concealErr
		}
		if invoice.Beneficiary.Kind == invoicing.BeneficiaryBlindedSeal {
			if vout != request.Seal.Vout || allocation.SealBlinding != request.Seal.Blinding ||
				!bytes.Equal(candidateSecret[:], invoiceSeal[:]) {
				continue
			}
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
		if err != nil {
			return nil, err
		}
		binding, err := p.rgb11CarrierBinding(allocation, utxo)
		if err != nil || !p.ownsRGB11Carrier(binding, walletScript) ||
			(invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout && !bytes.Equal(utxo.PkScript, witnessScript)) {
			if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
				continue
			}
			return nil, ErrRGB11InvoiceMismatch
		}
		if invoice.Assignment != nil {
			switch invoice.Assignment.Kind {
			case invoicing.StateAmount:
				if allocation.Amount.Value.Sign() < 0 || !allocation.Amount.Value.IsUint64() ||
					allocation.Amount.Value.Uint64() != uint64(invoice.Assignment.Amount) {
					return nil, ErrRGB11InvoiceMismatch
				}
			case invoicing.StateAllocation:
				if allocation.Amount.Value.Cmp(indexer.NewDefaultDecimal(1).Value) != 0 {
					return nil, ErrRGB11InvoiceMismatch
				}
			}
		}
		asset := &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}
		proof := &rgb11wallet.AllocationProof{
			OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
			AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
			StateData:       append([]byte(nil), allocation.StateData...),
			SealCommitment:  hex.EncodeToString(candidateSecret[:]),
			SealDisclosure:  append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
			WitnessTxID: strings.SplitN(allocation.OutPoint, ":", 2)[0], Status: "valid",
			CarrierBinding: binding,
		}
		if err := p.ProjectRGB11Allocation(allocation.OutPoint, asset, proof); err != nil {
			return nil, err
		}
		receivedAsset = &indexer.AssetInfo{Name: asset.Name, Amount: *asset.Amount.Clone(), BindingSat: 0}
		receivedOutpoint = allocation.OutPoint
		projected++
	}
	if projected == 0 {
		return nil, ErrRGB11NoAllocation
	}
	transferID := receipt.TransferID
	if transferID == "" {
		transferID = receipt.ConsignmentHash
	}
	if err := p.rgbManager.engine.MarkRelayAccepted(requestID, transferID, receipt.ConsignmentHash); err != nil {
		return nil, fmt.Errorf("mark RGB11 receive accepted: %w", err)
	}
	expiry := int64(0)
	if invoice.Expiry != nil {
		expiry = *invoice.Expiry
	}
	state := &rgb11wallet.TransferState{
		TransferID: transferID, Direction: "receive", Asset: *receivedAsset,
		RecipientID: request.RecipientID, Invoice: request.Invoice,
		OutputOutPoints: []string{receivedOutpoint}, MinConfirmations: 1, Expiry: expiry,
		ConsignmentHash: receipt.ConsignmentHash, WitnessTxID: allocationOutpointTxID(receivedOutpoint),
		AckStatus: "accepted", Status: "pending", RelayRecordKey: request.RelayKey,
		AckRecordKey: request.AckKey, RelayDurability: "LOCAL_ONLY", RelayExpiry: expiry,
	}
	if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		return nil, err
	}
	if autoBackup {
		p.autoBackupRGB11AfterMutation()
	}
	return receipt, nil
}

func (p *Manager) rgb11ReceivedPolicyOpouts(receipt *rgb11wallet.ValidationReceipt,
	request *corewallet.ReceiveRequest, invoice *invoicing.Invoice, invoiceSeal [32]byte,
	witnessScript []byte) ([]operations.Opout, error) {
	checked := make([]operations.Opout, 0, 1)
	for _, allocation := range receipt.Allocations {
		if allocation.AssignmentType != 4000 || !allocation.WitnessTxPtr {
			continue
		}
		vout, ok := outpointVout(allocation.OutPoint)
		if !ok {
			continue
		}
		candidate := false
		switch invoice.Beneficiary.Kind {
		case invoicing.BeneficiaryBlindedSeal:
			seal := seals.NewWitnessBlindSeal(vout, allocation.SealBlinding)
			secret, err := seal.Conceal()
			if err != nil {
				return nil, err
			}
			candidate = vout == request.Seal.Vout && allocation.SealBlinding == request.Seal.Blinding &&
				bytes.Equal(secret[:], invoiceSeal[:])
		case invoicing.BeneficiaryWitnessVout:
			utxo, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
			if err != nil {
				return nil, err
			}
			candidate = utxo != nil && bytes.Equal(utxo.PkScript, witnessScript)
		}
		if !candidate {
			continue
		}
		opout, err := operations.ParseOpout(fmt.Sprintf("%s/%d/%d",
			allocation.OperationID, allocation.AssignmentType, allocation.AssignmentIndex))
		if err != nil {
			return nil, err
		}
		checked = append(checked, opout)
	}
	if len(checked) == 0 {
		return nil, ErrRGB11NoAllocation
	}
	return checked, nil
}

func outpointVout(outpoint string) (uint32, bool) {
	_, text, ok := strings.Cut(outpoint, ":")
	if !ok {
		return 0, false
	}
	value, err := strconv.ParseUint(text, 10, 32)
	return uint32(value), err == nil
}

func decodeReceiptSchema(schemaID string) ([32]byte, error) {
	return baid64.Decode32(schemaID, baid64.SchemaIDOptions())
}
