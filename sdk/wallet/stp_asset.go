package wallet

import (
	"bytes"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

// STPAssetProtocol is the asset boundary of the Bitcoin/SatoshiNet channel
// implementation. Channel roles, scripts, signatures, revocations, persistence
// and broadcasting remain the responsibility of STP. Implementations must not
// release signatures to peers, broadcast or advance a channel while planning.
// A protocol is registered only when all of its advertised operations, including
// unilateral recovery, are implemented. Wallet transfer support alone is not
// sufficient to admit an asset into an STP channel.
type STPAssetProtocol interface {
	STPAssetRules
	STPAssetPlanner
	STPAssetLifecycle
	STPAssetVerifier
}

type STPAssetPlanner interface {
	PrepareInputs(*Manager, STPAssetInputRequest) (*STPAssetInputs, error)
	BuildTransfer(*Manager, STPAssetTransferRequest) (*STPAssetPlan, error)
	BuildCommitment(*Manager, STPAssetCommitmentRequest) (*STPAssetPlan, error)
	EstimateSplicePayload(*AssetName, int) ([]byte, error)
	BindSplice(*Manager, STPAssetSpliceRequest) error
}

// STPAssetSplicePlanner is implemented by protocols whose allocations depend
// on the whole transaction rather than ordinal offsets. It validates ownership,
// co-carried assets and balances before filling the existing reservation.
type STPAssetSplicePlanner interface {
	PlanSplice(*Manager, *SplicingReservation, []*TxOutput, []*TxOutput, int, int64, STPAssetOperation) error
}

// SplitOutputs creates a transaction-local asset view. It does not attest that
// an allocation is valid: BindSplice and the final protocol verifier must check
// the completed transaction against the protocol's native evidence.
type STPAssetOutputSplitter interface {
	SplitOutputs([]*TxOutput, *AssetName, int64, *Decimal) (*TxOutput, *TxOutput, error)
}

// ClassifyOutputs restores protocol-specific allocations after ordinal-only
// output accounting. Inputs retain the complete independently verified state.
type STPAssetOutputClassifier interface {
	ClassifyOutputs(*Manager, *wire.MsgTx, []*TxOutput, []*TxOutput) error
}

// PrepareLayout reserves protocol commitments that must precede other outputs.
// Asset inputs and allocations are still added in the normal ordered planning
// pass, so ordinal-bound protocols retain matching input/output sat prefixes.
type STPAssetLayoutPreparer interface {
	PrepareLayout(*Manager, STPAssetCommitmentRequest) error
}

// Recovery preparation is part of commitment construction, before STP releases
// any signature or revokes the previous commitment. The returned transactions
// use the existing PrevTxs/NextTxs/Others persistence and recovery paths.
type STPAssetLifecycle interface {
	ReserveRecovery(*Manager, STPAssetCommitmentRequest, *STPAssetPlan) (*wire.TxOut, error)
	CompleteRecovery(*Manager, STPAssetCommitmentRequest, *STPAssetPlan) error
	PrepareOtherSweep(*Manager, STPAssetOtherSweepRequest) (*STPAssetOtherSweepPlan, error)
	RecoveryInputs(*ChannelCommitment, map[AssetName]*Decimal) (STPAssetRecoveryInputs, error)
}

// Verification is deliberately independent of Manager, wallet keys and network
// reads. Current indexer-backed protocols verify the layout supplied by their
// planner; a client-validated protocol must additionally validate its native
// evidence here before it can be registered for STP operations.
type STPAssetVerifier interface {
	VerifyInput(*AssetName, *TxOutput) error
	Verify(STPAssetVerification) error
}

type STPAssetInputRequest struct {
	Asset          *indexer.AssetName
	Amount         *Decimal
	FundingAddress string
	AssetAddress   string
	FundingUtxos   *UtxoMgr
	ExcludedUtxos  map[string]bool
	PreviousInputs []string
	RevealKey      []byte
	FeeRate        int64
	LockInputs     bool
	Operation      STPAssetOperation
}

type STPAssetInputs struct {
	Inputs   []*TxOutput
	Inscribe *InscribeResv
}

type STPAssetTransferRequest struct {
	Wallet                  common.Wallet
	Address                 string
	Destinations            []*SendAssetInfo
	Asset                   *AssetName
	FeeRate                 int64
	Memo                    []byte
	ExcludeRecentBlock      bool
	PayFeeByCurrentAddress  bool
	MaxConfirmedInputHeight int
}

// STP chooses both spending scripts. An asset implementation may allocate
// assets to them, but cannot replace them or choose a different recovery branch.
type STPAssetCommitmentRequest struct {
	Operation     STPAssetOperation
	Channel       *Channel
	Tx            *wire.MsgTx
	LocalBalance  map[AssetName]*Decimal
	RemoteBalance map[AssetName]*Decimal
	LocalScript   []byte
	RemoteScript  []byte
	WitnessScript []byte
	RevealKey     []byte
	KeyRing       *CommitmentKeyRing
	FeeRate       int64
	Weight        *utils.TxWeightEstimator
	ServerCommit  bool
	// Prefix counts protect protocols with transaction-wide input/output
	// allocation rules from later adapters that insert their own carriers.
	PrefixInputs  int
	PrefixOutputs int
}

type STPAssetSpliceRequest struct {
	Operation   STPAssetOperation
	Reservation *SplicingReservation
	Tx          *wire.MsgTx
}

// STPAssetPlan describes a transaction package rather than a single transfer.
// Plans are transient: STP signs and stores their transactions in reservation
// and commitment records. Native adapters also persist proof sidecars and a
// recovery journal; legacy assets keep their existing transaction encoding.
type STPAssetPlan struct {
	Tx             *wire.MsgTx
	PrevFetcher    *txscript.MultiPrevOutFetcher
	Fee            int64
	PrevTxs        []*InscribeResv
	NextTxs        []*InscribeResv
	OtherTxs       []*CommitmentTx
	PlainInputs    []*TxOutput
	ExtraPlainSats int64
	// Native carrier value belongs to the channel initiator in both views of
	// a commitment; it is distinct from the commitment owner's plain balance.
	InitiatorPlainSats int64
	NativeProofs       map[string][]byte
	// ProtocolData is ephemeral planner/verifier state, never wire or DB data.
	ProtocolData   any
	FixedPrefix    bool
	LocalOutputs   []*CommitAssetOutput
	RemoteOutputs  []*CommitAssetOutput
	RecoveryOutput *wire.TxOut
	Layout         []STPAssetOutputConstraint
	Inputs         []wire.OutPoint
}

// Output points to the planned output, allowing earlier protocols' outputs to
// move when Runes prepends its carrier. FixedIndex is required for outputs whose
// ordinal is committed by the asset protocol, such as runestone edicts.
type STPAssetOutputConstraint struct {
	Output     *wire.TxOut
	Value      int64
	PkScript   []byte
	Index      int
	FixedIndex bool
}

type STPAssetVerification struct {
	Operation STPAssetOperation
	Tx        *wire.MsgTx
	Plan      *STPAssetPlan
}

type STPAssetRecoveryInputs struct {
	// LeadingPlainOutputs are commitment outputs representing an account
	// balance, whose spendable transfers were prepared in NextTxs.
	LeadingPlainOutputs int
	Transfers           []*TxOutput
}

type STPAssetOtherSweepRequest struct {
	ChannelAddress string
	CommitTx       *wire.MsgTx
	Outputs        []*CommitAssetOutput
	FeeInputs      []*TxOutput
	RevealKey      []byte
	FeeRate        int64
}

type STPAssetOtherSweepPlan struct {
	PrevTxs   []*wire.MsgTx
	Transfers []*TxOutput
	FeeInputs []*TxOutput
}

// STPAssetRegistry is immutable after construction. Its explicit order is part
// of deterministic transaction construction, not Go map iteration order.
type STPAssetRegistry struct {
	ordered []STPAssetProtocol
	byName  map[string]STPAssetProtocol
}

func NewSTPAssetRegistry(protocols ...STPAssetProtocol) (*STPAssetRegistry, error) {
	r := &STPAssetRegistry{byName: make(map[string]STPAssetProtocol)}
	for _, protocol := range protocols {
		if protocol == nil {
			return nil, fmt.Errorf("nil STP asset protocol")
		}
		name := protocol.Protocol()
		if _, exists := r.byName[name]; exists {
			return nil, fmt.Errorf("duplicate STP asset protocol %q", name)
		}
		r.byName[name] = protocol
		r.ordered = append(r.ordered, protocol)
	}
	return r, nil
}

func (r *STPAssetRegistry) Resolve(asset *indexer.AssetName, operation STPAssetOperation) (STPAssetProtocol, error) {
	if asset == nil {
		return nil, fmt.Errorf("nil STP asset")
	}
	protocol, ok := r.byName[asset.Protocol]
	if !ok {
		if asset.Protocol == "rgb11" {
			// Preserve the public error until proof transport, recovery and
			// settlement verification are implemented by an RGB adapter.
			return nil, ErrRGB11STPUnavailable
		}
		return nil, fmt.Errorf("unsupported STP asset protocol %q", asset.Protocol)
	}
	if err := protocol.ValidateAsset(asset, operation); err != nil {
		return nil, err
	}
	return protocol, nil
}

func newBuiltinSTPAssetRegistry() *STPAssetRegistry {
	// ARC20 occupies the first spendable outputs. Preserve the relative order
	// of inscriptions, sat-bound assets and Runes; RGB reserves its zero-value
	// commitment marker before planning and adds its carriers after them.
	r, err := NewSTPAssetRegistry(atomicalsSTPProtocol{}, brc20STPProtocol{}, ordxSTPProtocol{}, runesSTPProtocol{}, rgb11STPProtocol{}, btcSTPProtocol{})
	if err != nil {
		panic(err)
	}
	return r
}

var stpAssetProtocols = newBuiltinSTPAssetRegistry()

func STPAssetProtocolFor(asset *indexer.AssetName, operation STPAssetOperation) (STPAssetProtocol, error) {
	return stpAssetProtocols.Resolve(asset, operation)
}

func ValidateSTPAsset(asset *indexer.AssetName, operation STPAssetOperation) error {
	_, err := STPAssetProtocolFor(asset, operation)
	return err
}

func STPPolicyFor(asset *AssetName) STPAssetPolicy {
	if asset == nil {
		return STPAssetPolicy{}
	}
	protocol, err := STPAssetProtocolFor(&asset.AssetName, STPOpCommit)
	if err != nil {
		return STPAssetPolicy{}
	}
	return protocol.Policy(asset)
}

func (p *Manager) PrepareSTPAssetInputs(req STPAssetInputRequest) (*STPAssetInputs, error) {
	protocol, err := STPAssetProtocolFor(req.Asset, req.Operation)
	if err != nil {
		return nil, err
	}
	return protocol.PrepareInputs(p, req)
}

func snapshotSTPAssetLayout(tx *wire.MsgTx, beforeInputs []*wire.TxIn, beforeOutputs []*wire.TxOut, fixedIndex bool, plan *STPAssetPlan) {
	inputs := make(map[*wire.TxIn]bool, len(beforeInputs))
	for _, input := range beforeInputs {
		inputs[input] = true
	}
	for _, input := range tx.TxIn {
		if !inputs[input] {
			plan.Inputs = append(plan.Inputs, input.PreviousOutPoint)
		}
	}
	outputs := make(map[*wire.TxOut]bool, len(beforeOutputs))
	for _, output := range beforeOutputs {
		outputs[output] = true
	}
	for index, output := range tx.TxOut {
		if !outputs[output] {
			plan.Layout = append(plan.Layout, STPAssetOutputConstraint{
				Output: output, Value: output.Value, PkScript: append([]byte(nil), output.PkScript...),
				Index: index, FixedIndex: fixedIndex,
			})
		}
	}
}

func verifySTPAssetLayout(req STPAssetVerification) error {
	if req.Tx == nil || req.Plan == nil {
		return fmt.Errorf("missing STP asset transaction plan")
	}
	inputs := make(map[wire.OutPoint]int, len(req.Tx.TxIn))
	for _, input := range req.Tx.TxIn {
		if input == nil {
			return fmt.Errorf("nil STP transaction input")
		}
		inputs[input.PreviousOutPoint]++
		if inputs[input.PreviousOutPoint] != 1 {
			return fmt.Errorf("duplicate STP transaction input %s", input.PreviousOutPoint)
		}
	}
	for _, input := range req.Plan.Inputs {
		if inputs[input] != 1 {
			return fmt.Errorf("STP asset input %s changed after planning", input)
		}
	}
	for _, constraint := range req.Plan.Layout {
		found := -1
		for index, output := range req.Tx.TxOut {
			if output == constraint.Output {
				found = index
				break
			}
		}
		if found < 0 || constraint.Output.Value != constraint.Value || !bytes.Equal(constraint.Output.PkScript, constraint.PkScript) {
			return fmt.Errorf("STP asset output changed after planning")
		}
		if constraint.FixedIndex && found != constraint.Index {
			return fmt.Errorf("STP asset output index changed from %d to %d", constraint.Index, found)
		}
	}
	for _, output := range req.Tx.TxOut {
		if output == nil || output.Value < 0 {
			return fmt.Errorf("invalid STP transaction output")
		}
	}
	return nil
}

type stpPlannedAsset struct {
	protocol STPAssetProtocol
	plan     *STPAssetPlan
}

func (p *Manager) planSTPCommitmentAssets(req STPAssetCommitmentRequest) ([]stpPlannedAsset, error) {
	for name, inputs := range req.Channel.FundingUtxos {
		protocol, err := STPAssetProtocolFor(&name.AssetName, req.Operation)
		if err != nil {
			return nil, err
		}
		for _, input := range inputs {
			if err := protocol.VerifyInput(&name, input); err != nil {
				return nil, err
			}
		}
	}
	for _, balances := range []map[AssetName]*Decimal{req.LocalBalance, req.RemoteBalance} {
		for name, amount := range balances {
			if amount.Sign() != 0 {
				if err := ValidateSTPAsset(&name.AssetName, req.Operation); err != nil {
					return nil, err
				}
			}
		}
	}
	var plans []stpPlannedAsset
	for _, protocol := range stpAssetProtocols.ordered {
		if preparer, ok := protocol.(STPAssetLayoutPreparer); ok {
			if err := preparer.PrepareLayout(p, req); err != nil {
				return nil, err
			}
		}
	}
	// Layout preparation may reserve only zero-value unspendable outputs.
	for _, output := range req.Tx.TxOut {
		if output.Value != 0 || !txscript.IsUnspendable(output.PkScript) {
			return nil, fmt.Errorf("asset layout preparation added a spendable output")
		}
	}
	if len(req.Tx.TxIn) != 0 {
		return nil, fmt.Errorf("asset layout preparation added inputs")
	}
	req.PrefixOutputs = len(req.Tx.TxOut)
	for _, protocol := range stpAssetProtocols.ordered {
		beforeInputs := append([]*wire.TxIn(nil), req.Tx.TxIn...)
		beforeOutputs := append([]*wire.TxOut(nil), req.Tx.TxOut...)
		plan, err := protocol.BuildCommitment(p, req)
		if err != nil {
			return nil, fmt.Errorf("plan %s commitment: %w", protocol.Protocol(), err)
		}
		if plan == nil {
			return nil, fmt.Errorf("missing %s commitment plan", protocol.Protocol())
		}
		snapshotSTPAssetLayout(req.Tx, beforeInputs, beforeOutputs, protocol.Policy(nil).FixedOutputIndexes, plan)
		plans = append(plans, stpPlannedAsset{protocol: protocol, plan: plan})
		if plan.FixedPrefix {
			req.PrefixInputs = len(req.Tx.TxIn)
			req.PrefixOutputs = len(req.Tx.TxOut)
		}
	}
	return plans, nil
}

func verifySTPCommitmentAssets(req STPAssetCommitmentRequest, plans []stpPlannedAsset) error {
	fetcher := req.Channel.GetCommitmentPrefetchor()
	for _, item := range plans {
		for _, inscribe := range item.plan.PrevTxs {
			if inscribe == nil {
				return fmt.Errorf("nil materialized STP input")
			}
			for _, tx := range []*wire.MsgTx{inscribe.CommitTx, inscribe.RevealTx} {
				if tx == nil {
					continue
				}
				for i := range tx.TxOut {
					output := indexer.GenerateTxOutput(tx, i)
					fetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
				}
			}
		}
	}
	for _, item := range plans {
		item.plan.PrevFetcher = fetcher
		if err := item.protocol.Verify(STPAssetVerification{Operation: req.Operation, Tx: req.Tx, Plan: item.plan}); err != nil {
			return fmt.Errorf("verify %s commitment: %w", item.protocol.Protocol(), err)
		}
	}
	return nil
}

func collectSTPAssetProofs(plans []stpPlannedAsset) (map[string][]byte, error) {
	var result map[string][]byte
	for _, item := range plans {
		for protocol, proof := range item.plan.NativeProofs {
			if protocol != item.protocol.Protocol() || len(proof) == 0 {
				return nil, fmt.Errorf("invalid %s native plan proof", protocol)
			}
			if result == nil {
				result = make(map[string][]byte)
			}
			if _, exists := result[protocol]; exists {
				return nil, fmt.Errorf("duplicate %s native proof", protocol)
			}
			result[protocol] = append([]byte(nil), proof...)
		}
	}
	return result, nil
}

func stpRecoveryInputs(commit *ChannelCommitment, balances map[AssetName]*Decimal, operation STPAssetOperation) (STPAssetRecoveryInputs, error) {
	for name, amount := range balances {
		if amount.Sign() != 0 {
			if err := ValidateSTPAsset(&name.AssetName, operation); err != nil {
				return STPAssetRecoveryInputs{}, err
			}
		}
	}
	var result STPAssetRecoveryInputs
	for _, protocol := range stpAssetProtocols.ordered {
		inputs, err := protocol.RecoveryInputs(commit, balances)
		if err != nil {
			return STPAssetRecoveryInputs{}, err
		}
		result.LeadingPlainOutputs += inputs.LeadingPlainOutputs
		result.Transfers = append(result.Transfers, inputs.Transfers...)
	}
	return result, nil
}
