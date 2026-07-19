package wallet

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/operations"
	"github.com/sat20-labs/rgb11/rejectlist"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrRGB11IssueUTXOUnavailable = errors.New("not enough confirmed plain Bitcoin UTXOs for RGB11 issuance")
	ErrRGB11IFAMainnet           = errors.New("IFA issuance is disabled on Bitcoin mainnet by the frozen wallet API")
)

type RGB11IssueRequest struct {
	Schema           string   `json:"schema"`
	Ticker           string   `json:"ticker,omitempty"`
	Name             string   `json:"name"`
	Details          string   `json:"details,omitempty"`
	Precision        uint8    `json:"precision"`
	Terms            string   `json:"terms,omitempty"`
	Amounts          []uint64 `json:"amounts"`
	InflationAmounts []uint64 `json:"inflation_amounts,omitempty"`
	RejectListURL    string   `json:"reject_list_url,omitempty"`
	MinConfirmations int64    `json:"min_confirmations,omitempty"`
}

// UnmarshalJSON accepts atomic u64 amounts as either JSON numbers or decimal
// strings. PWA callers use strings so values above JavaScript's safe-integer
// range remain exact.
func (r *RGB11IssueRequest) UnmarshalJSON(data []byte) error {
	type wireRequest struct {
		Schema           string            `json:"schema"`
		Ticker           string            `json:"ticker"`
		Name             string            `json:"name"`
		Details          string            `json:"details"`
		Precision        uint8             `json:"precision"`
		Terms            string            `json:"terms"`
		Amounts          []json.RawMessage `json:"amounts"`
		InflationAmounts []json.RawMessage `json:"inflation_amounts"`
		RejectListURL    string            `json:"reject_list_url"`
		MinConfirmations int64             `json:"min_confirmations"`
	}
	var wire wireRequest
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	amounts, err := parseRGB11AtomicAmounts(wire.Amounts)
	if err != nil {
		return err
	}
	inflation, err := parseRGB11AtomicAmounts(wire.InflationAmounts)
	if err != nil {
		return err
	}
	*r = RGB11IssueRequest{
		Schema: wire.Schema, Ticker: wire.Ticker, Name: wire.Name, Details: wire.Details,
		Precision: wire.Precision, Terms: wire.Terms, Amounts: amounts, InflationAmounts: inflation,
		RejectListURL: wire.RejectListURL, MinConfirmations: wire.MinConfirmations,
	}
	return nil
}

func parseRGB11AtomicAmounts(values []json.RawMessage) ([]uint64, error) {
	result := make([]uint64, 0, len(values))
	for _, raw := range values {
		text := strings.TrimSpace(string(raw))
		if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
			var decoded string
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil, err
			}
			text = decoded
		}
		amount, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid RGB11 atomic amount %q: %w", text, err)
		}
		result = append(result, amount)
	}
	return result, nil
}

type RGB11IssueResult struct {
	ContractID string                         `json:"contract_id"`
	SchemaID   string                         `json:"schema_id"`
	AssetName  indexer.AssetName              `json:"asset_name"`
	Armor      string                         `json:"armor"`
	OutPoints  []string                       `json:"outpoints"`
	Receipt    *rgb11wallet.ValidationReceipt `json:"receipt"`
	Projected  int                            `json:"projected"`
}

// IssueRGB11Asset selects wallet-owned confirmed plain UTXOs, creates a
// canonical standard-schema genesis, validates it against Bitcoin evidence,
// and imports its allocations into the native wallet projection.
func (p *Manager) IssueRGB11Asset(ctx context.Context, request RGB11IssueRequest) (*RGB11IssueResult, error) {
	if p == nil || p.wallet == nil || p.l1IndexerClient == nil || p.rgbManager.evidence == nil || p.utxoLockerL1 == nil {
		return nil, ErrRGB11Inconsistent
	}
	kind, err := parseRGB11IssueSchema(request.Schema)
	if err != nil {
		return nil, err
	}
	params := GetChainParam()
	if kind == schemas.IFA && params.Net == chaincfg.MainNetParams.Net {
		return nil, ErrRGB11IFAMainnet
	}
	if request.MinConfirmations <= 0 {
		request.MinConfirmations = 1
	}
	amountCount := len(request.Amounts) + len(request.InflationAmounts)
	if kind == schemas.UDA {
		if len(request.Amounts) == 0 {
			request.Amounts = []uint64{1}
		}
		amountCount = len(request.Amounts)
	}
	selected, err := p.selectRGB11IssueOutpoints(amountCount, request.MinConfirmations)
	if err != nil {
		return nil, err
	}
	allocations, err := rgb11IssueAllocations(selected[:len(request.Amounts)], request.Amounts)
	if err != nil {
		return nil, err
	}
	inflation, err := rgb11IssueAllocations(selected[len(request.Amounts):], request.InflationAmounts)
	if err != nil {
		return nil, err
	}
	issued, err := coreissuance.Issue(coreissuance.Spec{
		Kind: kind, Network: rgb11IssuanceNetwork(params),
		Ticker: request.Ticker, Name: request.Name, Details: request.Details,
		Precision: request.Precision, Terms: request.Terms,
		Allocations: allocations, InflationRights: inflation, RejectListURL: request.RejectListURL,
	})
	if err != nil {
		return nil, err
	}
	imported, err := p.ImportRGB11Contract(ctx, []byte(issued.Armor))
	if err != nil {
		return nil, err
	}
	if imported.Projected != len(selected) {
		return nil, fmt.Errorf("%w: issued %d allocations but projected %d", ErrRGB11Inconsistent, len(selected), imported.Projected)
	}
	return &RGB11IssueResult{
		ContractID: imported.ContractID, SchemaID: imported.SchemaID, AssetName: imported.AssetName,
		Armor: issued.Armor, OutPoints: selected, Receipt: imported.Receipt, Projected: imported.Projected,
	}, nil
}

func parseRGB11IssueSchema(value string) (schemas.Kind, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "NIA":
		return schemas.NIA, nil
	case "IFA":
		return schemas.IFA, nil
	case "UDA":
		return schemas.UDA, nil
	default:
		return "", coreissuance.ErrUnsupportedSchema
	}
}

func (p *Manager) selectRGB11IssueOutpoints(count int, minConfirmations int64) ([]string, error) {
	if count <= 0 {
		return nil, coreissuance.ErrInvalidSpec
	}
	address := p.wallet.GetAddress()
	walletScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		return nil, err
	}
	candidates := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].OutPoint < candidates[j].OutPoint })
	p.utxoLockerL1.Reload(address)
	selected := make([]string, 0, count)
	for _, candidate := range candidates {
		if candidate == nil || candidate.OutPoint == "" || p.utxoLockerL1.IsLocked(candidate.OutPoint) {
			continue
		}
		evidence, err := p.rgbManager.evidence.GetUTXO(candidate.OutPoint)
		if err != nil || evidence == nil {
			continue
		}
		if evidence.Confirmations < minConfirmations || !bytes.Equal(evidence.PkScript, walletScript) {
			continue
		}
		if _, err := wire.NewOutPointFromString(candidate.OutPoint); err != nil {
			continue
		}
		selected = append(selected, candidate.OutPoint)
		if len(selected) == count {
			return selected, nil
		}
	}
	return nil, fmt.Errorf("%w: need %d, found %d", ErrRGB11IssueUTXOUnavailable, count, len(selected))
}

func rgb11IssueAllocations(outpoints []string, amounts []uint64) ([]coreissuance.Allocation, error) {
	if len(outpoints) != len(amounts) {
		return nil, coreissuance.ErrInvalidSpec
	}
	allocations := make([]coreissuance.Allocation, 0, len(outpoints))
	for index, outpointText := range outpoints {
		outpoint, err := wire.NewOutPointFromString(outpointText)
		if err != nil {
			return nil, err
		}
		var entropy [8]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return nil, err
		}
		seal, err := seals.NewGraphBlindSeal(outpoint.Hash[:], outpoint.Index, binary.LittleEndian.Uint64(entropy[:]))
		if err != nil {
			return nil, err
		}
		allocations = append(allocations, coreissuance.Allocation{Seal: seal, Amount: amounts[index]})
	}
	return allocations, nil
}

func rgb11IssuanceNetwork(params *chaincfg.Params) coreissuance.ChainNet {
	if params != nil && params.Net == chaincfg.MainNetParams.Net {
		return coreissuance.BitcoinMainnet
	}
	if params != nil && params.Net == chaincfg.TestNet3Params.Net {
		return coreissuance.BitcoinTestnet3
	}
	if params != nil && params.Net == chaincfg.RegressionNetParams.Net {
		return coreissuance.BitcoinRegtest
	}
	if params != nil && params.Net == chaincfg.SigNetParams.Net {
		return coreissuance.BitcoinSignet
	}
	return coreissuance.BitcoinTestnet4
}

type RGB11ImportResult struct {
	ContractID string                         `json:"contract_id"`
	SchemaID   string                         `json:"schema_id"`
	AssetName  indexer.AssetName              `json:"asset_name"`
	Receipt    *rgb11wallet.ValidationReceipt `json:"receipt"`
	Projected  int                            `json:"projected"`
}

// ImportRGB11Contract validates a complete contract consignment and imports
// only revealed allocations whose Bitcoin output is controlled by the active
// wallet. The Indexer contributes UTXO facts only; it never contributes RGB
// balances or allocation state.
func (p *Manager) ImportRGB11Contract(ctx context.Context, raw []byte) (*RGB11ImportResult, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil || p.wallet == nil {
		return nil, ErrRGB11Inconsistent
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, err
	}
	if container.Armor == nil || container.Armor.Type != "contract" {
		return nil, fmt.Errorf("RGB11 import requires a contract consignment")
	}
	receipt, err := p.rgbManager.projectionStore.ValidateAndStoreConsignment(
		ctx, rgb11wallet.NewNativeConsensusValidator(), p.rgbManager.evidence, raw,
	)
	if err != nil {
		return nil, err
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return nil, err
	}
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}

	projected := 0
	for _, allocation := range receipt.Allocations {
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
		if err != nil {
			return nil, err
		}
		if utxo == nil {
			continue
		}
		binding, err := p.rgb11CarrierBinding(allocation, utxo)
		if err != nil {
			return nil, err
		}
		if !p.ownsRGB11Carrier(binding, walletScript) {
			continue
		}
		commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
		asset := &indexer.AssetInfo{
			Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0,
		}
		proof := &rgb11wallet.AllocationProof{
			OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
			AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
			StateData:       append([]byte(nil), allocation.StateData...),
			SealCommitment:  hex.EncodeToString(commitment[:]),
			SealDisclosure:  append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
			WitnessTxID: allocationOutpointTxID(allocation.OutPoint), Status: "valid",
			CarrierBinding: binding,
		}
		if err := p.ProjectRGB11Allocation(allocation.OutPoint, asset, proof); err != nil {
			return nil, err
		}
		projected++
	}

	descriptor, err := schemas.ByKind(container.GenesisReport.Kind)
	if err != nil {
		return nil, err
	}
	assetType := indexer.ASSET_TYPE_FT
	if !descriptor.Fungible {
		assetType = indexer.ASSET_TYPE_NFT
	}
	assetName, err := rgb11wallet.NewAssetName(container.ContractID, assetType)
	if err != nil {
		return nil, err
	}
	schemaValue, _ := container.Value.Field("schema")
	typeSystem, _ := container.Value.Field("types")
	genesisValue, _ := container.Value.Field("genesis")
	metadata, err := schemas.ExtractGenesisAssetMetadata(schemaValue, typeSystem, genesisValue)
	if err != nil {
		return nil, err
	}
	ext := rgb11wallet.TickerExt{
		AssetName: assetName, OriginalAssetID: container.ContractID,
		SchemaID: container.SchemaID, ContractID: container.ContractID,
		ContractHash: receipt.ConsignmentHash, RejectListURL: metadata.RejectListURL,
		ControlMode: descriptor.DefaultControlMode,
		STPAllowed:  false, ValidationStatus: "valid",
	}
	extContent, err := json.Marshal(ext)
	if err != nil {
		return nil, err
	}
	info := &indexer.TickerInfo{
		AssetName: assetName, DisplayName: metadata.DisplayName, Divisibility: int(metadata.Precision),
		DeployTx:    allocationOutpointTxID(firstAllocationOutpoint(receipt.Allocations)),
		TotalMinted: fmt.Sprintf("%d", metadata.IssuedSupply), MaxSupply: fmt.Sprintf("%d", metadata.MaxSupply), Content: extContent,
	}
	if err := p.RegisterRGB11TickerInfo(info); err != nil {
		return nil, err
	}
	result := &RGB11ImportResult{
		ContractID: container.ContractID, SchemaID: container.SchemaID,
		AssetName: assetName, Receipt: receipt, Projected: projected,
	}
	p.autoBackupRGB11AfterMutation()
	return result, nil
}

func allocationOutpointTxID(outpoint string) string {
	for index := len(outpoint) - 1; index >= 0; index-- {
		if outpoint[index] == ':' {
			return outpoint[:index]
		}
	}
	return ""
}

func firstAllocationOutpoint(allocations []rgb11wallet.ValidatedAllocation) string {
	if len(allocations) == 0 {
		return ""
	}
	return allocations[0].OutPoint
}

const (
	RGB11RejectReasonList = "reject-list"
	RGB11RejectReasonUser = "user-rejected"
)

var (
	ErrRGB11RejectListUnavailable = errors.New("RGB11 reject list is unavailable")
	ErrRGB11Rejected              = errors.New("RGB11 allocation rejected by issuer policy")
)

// RGB11RejectListProvider makes the network policy injectable for deterministic
// wallet tests. The default implementation permits plain HTTP only on loopback
// while the wallet is configured for regtest.
type RGB11RejectListProvider interface {
	Fetch(string) (rejectlist.List, error)
}

type RGB11RejectListViolation struct {
	Checked  operations.Opout
	Rejected operations.Opout
}

func (e *RGB11RejectListViolation) Error() string {
	if e == nil {
		return ErrRGB11Rejected.Error()
	}
	return fmt.Sprintf("%s: checked %s, rejected ancestor %s", ErrRGB11Rejected, e.Checked, e.Rejected)
}

func (e *RGB11RejectListViolation) Unwrap() error { return ErrRGB11Rejected }

func (p *Manager) rgb11RejectListProvider() RGB11RejectListProvider {
	if p != nil && p.rgbManager != nil && p.rgbManager.rejectLists != nil {
		return p.rgbManager.rejectLists
	}
	params := GetChainParam()
	allowLoopback := params != nil && params.Net == chaincfg.RegressionNetParams.Net
	return rejectlist.Client{AllowLoopbackHTTP: allowLoopback}
}

func rgb11ContainerRejectListURL(container *coreconsignment.Container) (string, error) {
	if container == nil {
		return "", coreconsignment.ErrContainerType
	}
	schema, okSchema := container.Value.Field("schema")
	types, okTypes := container.Value.Field("types")
	genesis, okGenesis := container.Value.Field("genesis")
	if !okSchema || !okTypes || !okGenesis {
		return "", coreconsignment.ErrContainerType
	}
	metadata, err := schemas.ExtractGenesisAssetMetadata(schema, types, genesis)
	if err != nil {
		return "", err
	}
	return metadata.RejectListURL, nil
}

func (p *Manager) checkRGB11RejectPolicy(container *coreconsignment.Container,
	checked []operations.Opout) error {
	url, err := rgb11ContainerRejectListURL(container)
	if err != nil || url == "" {
		return err
	}
	list, err := p.rgb11RejectListProvider().Fetch(url)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRGB11RejectListUnavailable, err)
	}
	dag, err := container.OperationDAG()
	if err != nil {
		return err
	}
	for _, opout := range checked {
		if rejected, ok := list.RejectedAncestor(opout, dag); ok {
			return &RGB11RejectListViolation{Checked: opout, Rejected: rejected}
		}
	}
	return nil
}

func rgb11ProofOpout(proof *rgb11wallet.AllocationProof) (operations.Opout, error) {
	if proof == nil || proof.AssignmentType > 0xffff || proof.AssignmentIndex > 0xffff {
		return operations.Opout{}, rgb11wallet.ErrInvalidProof
	}
	operationID, err := hex.DecodeString(proof.OperationID)
	if err != nil || len(operationID) != 32 {
		return operations.Opout{}, rgb11wallet.ErrInvalidProof
	}
	return operations.ParseOpout(fmt.Sprintf("%s/%d/%d", proof.OperationID, proof.AssignmentType, proof.AssignmentIndex))
}
