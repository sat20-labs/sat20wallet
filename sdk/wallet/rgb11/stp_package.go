package rgb11wallet

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/anchors"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/operations"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

// ChannelContext is private protocol construction context. Entropy is shared
// by the two channel peers and must never be exported in public asset receipts.
type ChannelContext struct {
	ChannelID      string   `json:"channel_id"`
	Operation      string   `json:"operation"`
	Height         uint64   `json:"height"`
	Branch         string   `json:"branch"`
	Entropy        [32]byte `json:"entropy"`
	BitcoinNetwork string   `json:"bitcoin_network"`
}

type ChannelAssignment struct {
	OutputIndex uint32          `json:"output_index"`
	Amount      indexer.Decimal `json:"amount"`
}

type ChannelStep struct {
	Context     ChannelContext      `json:"context"`
	Sources     []ChannelFunding    `json:"sources"`
	Assignments []ChannelAssignment `json:"assignments"`
	DBCIndex    uint32              `json:"dbc_index"`
	Tx          []byte              `json:"tx"`
	Consignment []byte              `json:"consignment"`
	Reveals     [][]byte            `json:"reveals"`
}

// ChannelPackage retains complete real transitions for the candidate and its
// recovery ancestors. It is private signing/recovery material, not an ordinary
// wallet ValidationReceipt and not a public pool receipt.
type ChannelPackage struct {
	Version uint32           `json:"version"`
	Funding []ChannelFunding `json:"funding"`
	Steps   []ChannelStep    `json:"steps"`
}

type ChannelPackageValidation struct {
	TerminalAllocations []ValidatedAllocation
	Outputs             map[string]*BitcoinUTXO
	History             consignment.HistoryReport
}

func EncodeChannelPackage(pkg *ChannelPackage) ([]byte, error) {
	if pkg == nil || pkg.Version != 1 || len(pkg.Funding) == 0 || len(pkg.Funding) > 1024 || len(pkg.Steps) == 0 || len(pkg.Steps) > 64 {
		return nil, fmt.Errorf("invalid RGB11 channel package")
	}
	raw, err := json.Marshal(pkg)
	if err != nil || len(raw) > MaxChannelProofSize {
		return nil, fmt.Errorf("RGB11 channel package exceeds limits")
	}
	return raw, nil
}

func DecodeChannelPackage(raw []byte) (*ChannelPackage, error) {
	var pkg ChannelPackage
	if err := decodeChannelJSON(raw, &pkg); err != nil {
		return nil, err
	}
	canonical, err := EncodeChannelPackage(&pkg)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, fmt.Errorf("noncanonical RGB11 channel package")
	}
	return &pkg, nil
}

// ReserveChannelDBC places a zero-valued placeholder before the first Bitcoin
// commitment carrier. The caller must use actual indexes after insertion.
func ReserveChannelDBC(tx *wire.MsgTx, index int) (*wire.TxOut, error) {
	if tx == nil || index < 0 || index > len(tx.TxOut) {
		return nil, fmt.Errorf("invalid RGB11 DBC position")
	}
	for i := 0; i < index; i++ {
		if channelCarrier(tx.TxOut[i].PkScript) {
			return nil, fmt.Errorf("RGB11 DBC follows another carrier")
		}
	}
	output := wire.NewTxOut(0, anchors.OpretScript([32]byte{}))
	tx.TxOut = append(tx.TxOut, nil)
	copy(tx.TxOut[index+1:], tx.TxOut[index:])
	tx.TxOut[index] = output
	return output, nil
}

// BuildChannelStep binds exact RGB source Opouts and destination quantities
// to the final Bitcoin transaction. It never signs, broadcasts or imports a
// balance. Repeated construction by either channel perspective is identical.
func BuildChannelStep(ctx context.Context, sources []ChannelFunding, tx *wire.MsgTx, dbcIndex uint32,
	assignments []ChannelAssignment, channel ChannelContext, evidence BitcoinEvidenceProvider) (*ChannelStep, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tx == nil || len(sources) == 0 || len(sources) > 1024 || len(assignments) == 0 || len(assignments) > 1024 ||
		uint64(dbcIndex) >= uint64(len(tx.TxOut)) || channel.ChannelID == "" || channel.Operation == "" || channel.Entropy == ([32]byte{}) {
		return nil, fmt.Errorf("incomplete RGB11 channel construction context")
	}
	if tx.TxOut[dbcIndex] == nil || tx.TxOut[dbcIndex].Value != 0 {
		return nil, fmt.Errorf("invalid RGB11 DBC output")
	}
	for i := uint32(0); i < dbcIndex; i++ {
		if tx.TxOut[i] == nil || channelCarrier(tx.TxOut[i].PkScript) {
			return nil, fmt.Errorf("RGB11 DBC must be the first Bitcoin carrier")
		}
	}
	ordered := append([]ChannelFunding(nil), sources...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].OutPoint < ordered[j].OutPoint })
	seenInputs := make(map[string]bool)
	for _, input := range tx.TxIn {
		if input == nil || seenInputs[input.PreviousOutPoint.String()] {
			return nil, fmt.Errorf("duplicate RGB11 Bitcoin input")
		}
		seenInputs[input.PreviousOutPoint.String()] = true
	}
	var containers []*consignment.Container
	var inputs []operations.TransitionInput
	seenRefs := make(map[schemas.InputRef]bool)
	total := new(big.Int)
	var asset indexer.AssetName
	var precision int
	var contractID consensus.ContractID
	for i, source := range ordered {
		if !seenInputs[source.OutPoint] || (i > 0 && ordered[i-1].OutPoint == source.OutPoint) {
			return nil, fmt.Errorf("native source is absent or duplicated in Bitcoin inputs")
		}
		validated, err := ValidateChannelFunding(ctx, source, evidence, nativeproof.Prepared, channel.BitcoinNetwork)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			asset, precision = validated.Asset.Name, validated.Asset.Amount.Precision
			contractID, err = consensus.ParseContractID(validated.ContractID)
			if err != nil {
				return nil, err
			}
		} else if asset != validated.Asset.Name || precision != validated.Asset.Amount.Precision {
			return nil, fmt.Errorf("mixed RGB11 contracts require a multi-contract Bitcoin commitment")
		}
		total.Add(total, validated.Asset.Amount.Value)
		point, err := wire.NewOutPointFromString(source.OutPoint)
		if err != nil {
			return nil, err
		}
		for _, state := range validated.History.HistoricalStates {
			if state.Outpoint.TxID != [32]byte(point.Hash) || state.Outpoint.Vout != point.Index {
				continue
			}
			if seenRefs[state.Reference] {
				return nil, fmt.Errorf("duplicate RGB11 source Opout")
			}
			seenRefs[state.Reference] = true
			inputs = append(inputs, operations.TransitionInput{OperationID: state.Reference.OperationID,
				AssignmentType: uint16(state.Reference.AssignmentType), Index: state.Reference.Index})
		}
		container, err := consignment.Decode(source.Consignment)
		if err != nil {
			return nil, err
		}
		var reveals []seals.GraphBlindSeal
		for _, raw := range source.Reveals {
			seal, err := seals.DecodeGraphBlindSeal(raw)
			if err != nil {
				return nil, err
			}
			reveals = append(reveals, seal)
		}
		if _, err := container.RevealGraphSeals(reveals); err != nil {
			return nil, err
		}
		containers = append(containers, container)
	}
	base, err := consignment.MergeHistories(containers...)
	if err != nil {
		return nil, err
	}
	orderedAssignments := append([]ChannelAssignment(nil), assignments...)
	sort.Slice(orderedAssignments, func(i, j int) bool { return orderedAssignments[i].OutputIndex < orderedAssignments[j].OutputIndex })
	contextBytes, err := json.Marshal(channel)
	if err != nil {
		return nil, err
	}
	seed := channelSeed(channel.Entropy, contextBytes, "transition", 0)
	var outputs []operations.TransitionOutput
	var reveals [][]byte
	var terminals [][32]byte
	assigned := new(big.Int)
	for i, output := range orderedAssignments {
		if uint64(output.OutputIndex) >= uint64(len(tx.TxOut)) || output.OutputIndex == dbcIndex ||
			(i > 0 && output.OutputIndex == orderedAssignments[i-1].OutputIndex) || output.Amount.Validate() != nil ||
			output.Amount.Precision != precision || output.Amount.Sign() <= 0 || !output.Amount.Value.IsUint64() ||
			tx.TxOut[output.OutputIndex] == nil || tx.TxOut[output.OutputIndex].Value < 330 || txscript.IsUnspendable(tx.TxOut[output.OutputIndex].PkScript) {
			return nil, fmt.Errorf("invalid RGB11 destination allocation")
		}
		entropy := channelSeed(channel.Entropy, contextBytes, "seal", output.OutputIndex)
		seal := seals.NewWitnessBlindSeal(output.OutputIndex, binary.LittleEndian.Uint64(entropy[:8]))
		secret, err := seal.Conceal()
		if err != nil {
			return nil, err
		}
		raw, err := seal.StrictBytes()
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, operations.TransitionOutput{AssignmentType: 4000, Class: "fungible", Amount: output.Amount.Value.Uint64(), SecretSeal: [32]byte(secret)})
		reveals, terminals = append(reveals, raw), append(terminals, [32]byte(secret))
		assigned.Add(assigned, output.Amount.Value)
	}
	if assigned.Cmp(total) != 0 {
		return nil, fmt.Errorf("RGB11 channel assignments do not conserve source quantity")
	}
	transition, _, err := operations.BuildTransition(operations.TransitionSpec{ContractID: [32]byte(contractID), Nonce: binary.LittleEndian.Uint64(seed[:8]),
		TransitionType: 10000, Inputs: inputs, Outputs: outputs})
	if err != nil {
		return nil, err
	}
	bundle, commitment, err := operations.BuildBundle(inputs, transition)
	if err != nil {
		return nil, err
	}
	var entropy bytes.Buffer
	for i := uint32(0); i < 5; i++ {
		part := channelSeed(channel.Entropy, contextBytes, "mpc", i)
		entropy.Write(part[:])
	}
	mpc, err := anchors.NewMPCProofFrom([32]byte(contractID), 5, &entropy)
	if err != nil {
		return nil, err
	}
	dbc, err := anchors.ConvolveMPC(mpc, [32]byte(contractID), commitment.BundleID)
	if err != nil {
		return nil, err
	}
	tx.TxOut[dbcIndex].PkScript = anchors.OpretScript(dbc)
	unsigned := tx.Copy()
	for _, input := range unsigned.TxIn {
		input.Witness = nil
	}
	witness, err := operations.BuildOpretWitnessBundleWithTx(unsigned, bundle, mpc)
	if err != nil {
		return nil, err
	}
	transfer, err := consignment.BuildTransfer(base, witness, terminals)
	if err != nil {
		return nil, err
	}
	raw, err := canonicalChannelConsignment(transfer)
	if err != nil {
		return nil, err
	}
	encoded, err := channelTxBytes(unsigned)
	if err != nil {
		return nil, err
	}
	return &ChannelStep{Context: channel, Sources: ordered, Assignments: orderedAssignments, DBCIndex: dbcIndex,
		Tx: encoded, Consignment: raw, Reveals: reveals}, nil
}

// ValidateChannelPackage reconstructs every committed transition from its
// expected source quantities and private context, then asks RGB core to
// validate the complete final graph. It returns material facts only.
func ValidateChannelPackage(ctx context.Context, pkg *ChannelPackage, tx *wire.MsgTx,
	evidence BitcoinEvidenceProvider) (*ChannelPackageValidation, error) {
	if _, err := EncodeChannelPackage(pkg); err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, fmt.Errorf("missing RGB11 settlement transaction")
	}
	// Every step must consume the exact inventory established by the package
	// roots or a preceding step. A valid but unrelated proof is not authority
	// to replace one of the channel's funding sources.
	available := make(map[string]ChannelFunding)
	for _, source := range pkg.Funding {
		if err := checkChannelFundingShape(source); err != nil {
			return nil, err
		}
		if _, exists := available[source.OutPoint]; exists {
			return nil, fmt.Errorf("duplicate RGB11 package root")
		}
		available[source.OutPoint] = source
	}
	for i := range pkg.Steps {
		step := &pkg.Steps[i]
		for _, source := range step.Sources {
			expected, exists := available[source.OutPoint]
			if !exists || !sameChannelFunding(source, expected) {
				return nil, fmt.Errorf("RGB11 step does not consume its authenticated inventory")
			}
			delete(available, source.OutPoint)
		}
		candidate := new(wire.MsgTx)
		reader := bytes.NewReader(step.Tx)
		if err := candidate.Deserialize(reader); err != nil || reader.Len() != 0 {
			return nil, fmt.Errorf("invalid RGB11 package Bitcoin transaction")
		}
		rebuilt, err := BuildChannelStep(ctx, step.Sources, candidate, step.DBCIndex, step.Assignments, step.Context, evidence)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(rebuilt.Tx, step.Tx) || !bytes.Equal(rebuilt.Consignment, step.Consignment) || !equalReveals(rebuilt.Reveals, step.Reveals) {
			return nil, fmt.Errorf("RGB11 package differs from its deterministic transition")
		}
		for _, assignment := range step.Assignments {
			source, err := ChannelFundingFromStep(*step, assignment.OutputIndex, assignment.Amount)
			if err != nil {
				return nil, err
			}
			if _, exists := available[source.OutPoint]; exists {
				return nil, fmt.Errorf("duplicate RGB11 step output")
			}
			available[source.OutPoint] = source
		}
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	actual, err := channelTxBytes(tx)
	if err != nil || !bytes.Equal(actual, last.Tx) {
		return nil, fmt.Errorf("RGB11 package is for another Bitcoin transaction")
	}
	var reveals []seals.GraphBlindSeal
	for _, raw := range last.Reveals {
		seal, err := seals.DecodeGraphBlindSeal(raw)
		if err != nil {
			return nil, err
		}
		reveals = append(reveals, seal)
	}
	validator := NewNativeConsensusValidatorWithReveals(reveals...)
	history, err := validator.ValidatePreparedHistoryConsignmentReport(ctx, last.Consignment, evidence)
	if err != nil {
		return nil, err
	}
	result := &ChannelPackageValidation{Outputs: make(map[string]*BitcoinUTXO)}
	seen := make(map[schemas.InputRef]bool)
	for _, historical := range history.HistoricalAllocations {
		allocation := historical.Allocation
		point, err := wire.NewOutPointFromString(allocation.OutPoint)
		if err != nil || point.Hash.String() != tx.TxID() {
			continue
		}
		if uint64(point.Index) >= uint64(len(tx.TxOut)) {
			return nil, ErrValidationReceipt
		}
		id, err := hex.DecodeString(allocation.OperationID)
		if err != nil || len(id) != 32 {
			return nil, fmt.Errorf("invalid RGB11 operation ID")
		}
		ref := schemas.InputRef{OperationID: [32]byte(id), AssignmentType: uint64(allocation.AssignmentType), Index: uint16(allocation.AssignmentIndex)}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		result.TerminalAllocations = append(result.TerminalAllocations, allocation)
		output := tx.TxOut[point.Index]
		result.Outputs[allocation.OutPoint] = &BitcoinUTXO{OutPoint: allocation.OutPoint, Value: output.Value, PkScript: append([]byte(nil), output.PkScript...)}
	}
	if len(result.TerminalAllocations) != len(last.Assignments) {
		return nil, fmt.Errorf("incomplete RGB11 channel terminal graph")
	}
	return result, nil
}

func ChannelFundingFromStep(step ChannelStep, outputIndex uint32, amount indexer.Decimal) (ChannelFunding, error) {
	tx := new(wire.MsgTx)
	reader := bytes.NewReader(step.Tx)
	if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 || uint64(outputIndex) >= uint64(len(tx.TxOut)) || len(step.Sources) == 0 {
		return ChannelFunding{}, fmt.Errorf("invalid RGB11 step output")
	}
	found := false
	for _, assignment := range step.Assignments {
		if assignment.OutputIndex == outputIndex && assignment.Amount.Precision == amount.Precision && assignment.Amount.Cmp(&amount) == 0 {
			found = true
		}
	}
	if !found {
		return ChannelFunding{}, fmt.Errorf("RGB11 output has no matching native allocation")
	}
	output := tx.TxOut[outputIndex]
	return ChannelFunding{OutPoint: fmt.Sprintf("%s:%d", tx.TxID(), outputIndex), Value: output.Value, PkScript: append([]byte(nil), output.PkScript...),
		AssetName: step.Sources[0].AssetName, Amount: *amount.Clone(), Consignment: append([]byte(nil), step.Consignment...), Reveals: cloneReveals(step.Reveals)}, nil
}

func channelSeed(secret [32]byte, context []byte, domain string, index uint32) [32]byte {
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte("sat20/rgb11/channel/v1\x00" + domain + "\x00"))
	mac.Write(context)
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], index)
	mac.Write(encoded[:])
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}

func channelTxBytes(tx *wire.MsgTx) ([]byte, error) {
	if tx == nil {
		return nil, fmt.Errorf("nil Bitcoin transaction")
	}
	var encoded bytes.Buffer
	if err := tx.SerializeNoWitness(&encoded); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func channelCarrier(script []byte) bool {
	return txscript.IsPayToTaproot(script) || (len(script) > 0 && script[0] == txscript.OP_RETURN)
}
func cloneReveals(values [][]byte) [][]byte {
	result := make([][]byte, len(values))
	for i, value := range values {
		result[i] = append([]byte(nil), value...)
	}
	return result
}
func equalReveals(left, right [][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !bytes.Equal(left[i], right[i]) {
			return false
		}
	}
	return true
}

func sameChannelFunding(left, right ChannelFunding) bool {
	return left.OutPoint == right.OutPoint && left.Value == right.Value &&
		bytes.Equal(left.PkScript, right.PkScript) && left.AssetName == right.AssetName &&
		left.Amount.Precision == right.Amount.Precision && left.Amount.Cmp(&right.Amount) == 0 &&
		bytes.Equal(left.Consignment, right.Consignment) && equalReveals(left.Reveals, right.Reveals)
}
