package wallet

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/anchors"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

type stpRGB11CommitPlan struct {
	Sources     []rgb11wallet.ChannelFunding
	DBC         *wire.TxOut
	Assignments []*CommitAssetOutput
	Context     rgb11wallet.ChannelContext
	Proof       []byte
	Tx          []byte
}

func stpRGB11ActiveFunding(channel *Channel) []*AssetToOutput {
	var result []*AssetToOutput
	for _, group := range channel.GetFundingOutputWithProtocol(rgb11wallet.Protocol) {
		if len(group.Outputs) != 0 {
			result = append(result, group)
		}
	}
	return result
}

func (rgb11STPProtocol) PrepareLayout(_ *Manager, req STPAssetCommitmentRequest) error {
	if req.Channel == nil || req.Tx == nil || req.Weight == nil {
		return fmt.Errorf("missing RGB11 commitment layout")
	}
	if len(stpRGB11ActiveFunding(req.Channel)) == 0 {
		return nil
	}
	output, err := rgb11wallet.ReserveChannelDBC(req.Tx, 0)
	if err != nil {
		return err
	}
	req.Weight.AddTxOutput(output)
	return nil
}

func (rgb11STPProtocol) BuildCommitment(_ *Manager, req STPAssetCommitmentRequest) (*STPAssetPlan, error) {
	plan := &STPAssetPlan{Tx: req.Tx}
	if req.Channel == nil || req.Tx == nil || req.Weight == nil {
		return nil, fmt.Errorf("missing RGB11 commitment context")
	}
	funding := stpRGB11ActiveFunding(req.Channel)
	inventory, err := stpRGB11Inventory(req.Channel)
	if err != nil {
		return nil, err
	}
	if len(funding) == 0 {
		if len(inventory.Sources) != 0 {
			return nil, fmt.Errorf("RGB11 proof inventory has no matching channel funding")
		}
		for _, balances := range []map[AssetName]*Decimal{req.LocalBalance, req.RemoteBalance} {
			for name, amount := range balances {
				if name.Protocol == rgb11wallet.Protocol && amount.Sign() != 0 {
					return nil, fmt.Errorf("RGB11 channel balance has no native funding")
				}
			}
		}
		return plan, nil
	}
	if len(funding) != 1 {
		return nil, fmt.Errorf("multiple RGB11 contracts require a multi-contract DBC")
	}
	if len(req.Tx.TxOut) == 0 || req.Tx.TxOut[0].Value != 0 || !bytes.Equal(req.Tx.TxOut[0].PkScript, anchors.OpretScript([32]byte{})) {
		return nil, fmt.Errorf("RGB11 commitment is missing its reserved DBC")
	}
	state := &stpRGB11CommitPlan{DBC: req.Tx.TxOut[0], Sources: append([]rgb11wallet.ChannelFunding(nil), inventory.Sources...)}
	byPoint := make(map[string]rgb11wallet.ChannelFunding)
	for _, source := range inventory.Sources {
		byPoint[source.OutPoint] = source
	}
	asset := funding[0].AssetName
	if asset == nil {
		return nil, fmt.Errorf("missing RGB11 funding asset identity")
	}
	total := indexer.NewDecimal(0, asset.N)
	for _, input := range funding[0].Outputs {
		if input == nil {
			return nil, fmt.Errorf("nil RGB11 channel source")
		}
		source, exists := byPoint[input.OutPointStr]
		if !exists || source.AssetName != asset.AssetName || source.Amount.Precision != asset.N ||
			source.Amount.Cmp(input.GetAsset(&asset.AssetName)) != 0 || source.Value != input.Value() ||
			!bytes.Equal(source.PkScript, input.OutValue.PkScript) || !bytes.Equal(source.PkScript, req.Channel.GetChannelPkScript()) {
			return nil, fmt.Errorf("RGB11 channel funding differs from its proof inventory")
		}
		delete(byPoint, input.OutPointStr)
		req.Tx.AddTxIn(input.TxIn())
		req.Weight.AddWitnessInput(utils.MultiSigWitnessSize)
		plan.InitiatorPlainSats += input.Value()
		total = total.Add(&source.Amount)
	}
	if len(byPoint) != 0 || len(state.Sources) == 0 {
		return nil, fmt.Errorf("RGB11 inventory contains unaccounted sources")
	}
	assigned := indexer.NewDecimal(0, asset.N)
	for i, side := range []struct {
		balance map[AssetName]*Decimal
		script  []byte
	}{
		{req.LocalBalance, req.LocalScript}, {req.RemoteBalance, req.RemoteScript},
	} {
		amount := side.balance[*asset]
		if amount == nil || amount.Sign() == 0 {
			continue
		}
		if amount.Validate() != nil || amount.Precision != asset.N || amount.Sign() < 0 || len(side.script) == 0 {
			return nil, fmt.Errorf("invalid RGB11 commitment balance")
		}
		output := wire.NewTxOut(330, append([]byte(nil), side.script...))
		assignment := &CommitAssetOutput{Index: len(req.Tx.TxOut), TxOut: output, AssetName: *asset, Amt: amount.Clone()}
		req.Tx.AddTxOut(output)
		req.Weight.AddTxOutput(output)
		state.Assignments = append(state.Assignments, assignment)
		if i == 0 {
			plan.LocalOutputs = append(plan.LocalOutputs, assignment)
		} else {
			plan.RemoteOutputs = append(plan.RemoteOutputs, assignment)
		}
		assigned = assigned.Add(amount)
		plan.InitiatorPlainSats -= output.Value
	}
	if assigned.Cmp(total) != 0 {
		return nil, fmt.Errorf("RGB11 commitment balances do not conserve channel quantity")
	}
	branch := "client"
	if req.ServerCommit {
		branch = "server"
	}
	state.Context = rgb11wallet.ChannelContext{ChannelID: req.Channel.ChannelId, Operation: string(req.Operation),
		Height: uint64(req.Channel.CommitHeight) + 1, Branch: branch, Entropy: req.Channel.AssetEntropy, BitcoinNetwork: GetChainParam().Name}
	plan.ProtocolData = state
	return plan, nil
}

func (rgb11STPProtocol) CompleteRecovery(p *Manager, req STPAssetCommitmentRequest, plan *STPAssetPlan) error {
	state, ok := plan.ProtocolData.(*stpRGB11CommitPlan)
	if !ok {
		return nil
	}
	evidence, err := stpRGB11Evidence(p)
	if err != nil {
		return err
	}
	dbc, err := stpOutputIndex(req.Tx, state.DBC)
	if err != nil {
		return err
	}
	var assignments []rgb11wallet.ChannelAssignment
	for _, allocation := range state.Assignments {
		index, err := stpOutputIndex(req.Tx, allocation.TxOut)
		if err != nil {
			return err
		}
		allocation.Index = index
		assignments = append(assignments, rgb11wallet.ChannelAssignment{OutputIndex: uint32(index), Amount: *allocation.Amt.Clone()})
	}
	step, err := rgb11wallet.BuildChannelStep(context.Background(), state.Sources, req.Tx, uint32(dbc), assignments, state.Context, evidence)
	if err != nil {
		return fmt.Errorf("build native RGB11 commitment: %w", err)
	}
	pkg := &rgb11wallet.ChannelPackage{Version: 1, Funding: state.Sources, Steps: []rgb11wallet.ChannelStep{*step}}
	if _, err := rgb11wallet.ValidateChannelPackage(context.Background(), pkg, req.Tx, evidence); err != nil {
		return err
	}
	raw, err := rgb11wallet.EncodeChannelPackage(pkg)
	if err != nil {
		return err
	}
	state.Proof, state.Tx = raw, append([]byte(nil), step.Tx...)
	plan.NativeProofs = map[string][]byte{rgb11wallet.Protocol: raw}
	// The prepass reserved this output before the normal layout snapshot.
	plan.Layout = append(plan.Layout, STPAssetOutputConstraint{Output: state.DBC, Value: 0,
		PkScript: append([]byte(nil), state.DBC.PkScript...), Index: dbc, FixedIndex: true})
	return nil
}

func (rgb11STPProtocol) Verify(req STPAssetVerification) error {
	if err := verifySTPAssetLayout(req); err != nil {
		return err
	}
	state, ok := req.Plan.ProtocolData.(*stpRGB11CommitPlan)
	if !ok {
		return nil
	}
	var raw bytes.Buffer
	if err := req.Tx.SerializeNoWitness(&raw); err != nil {
		return err
	}
	if !bytes.Equal(raw.Bytes(), state.Tx) || len(state.Proof) == 0 || !bytes.Equal(state.Proof, req.Plan.NativeProofs[rgb11wallet.Protocol]) {
		return fmt.Errorf("RGB11 commitment changed after native validation")
	}
	return nil
}

func (rgb11STPProtocol) BindSplice(p *Manager, req STPAssetSpliceRequest) error {
	resv := req.Reservation
	if resv == nil || resv.Channel == nil || req.Tx == nil || len(req.Tx.TxOut) == 0 || resv.AssetName == nil ||
		resv.SplicingAmt == nil || resv.SplicingAmt.Sign() <= 0 {
		return fmt.Errorf("incomplete RGB11 splice context")
	}
	if req.Operation != STPOpSpliceIn && req.Operation != STPOpSpliceOut {
		return fmt.Errorf("invalid RGB11 splice operation")
	}
	evidence, err := stpRGB11Evidence(p)
	if err != nil {
		return err
	}
	inventory, err := stpRGB11Inventory(resv.Channel)
	if err != nil {
		return err
	}
	used := make(map[string]bool)
	for _, input := range req.Tx.TxIn {
		used[input.PreviousOutPoint.String()] = true
	}
	var sources, remaining []rgb11wallet.ChannelFunding
	for _, source := range inventory.Sources {
		if used[source.OutPoint] {
			sources = append(sources, source)
		} else {
			remaining = append(remaining, source)
		}
	}
	if len(sources) == 0 {
		return fmt.Errorf("RGB11 splice has no authenticated source")
	}
	main := req.Tx.TxOut[0]
	var change *wire.TxOut
	var changeAmount *Decimal
	if resv.SplicingChangeOutput != nil {
		changeAmount = resv.SplicingChangeOutput.GetAsset(&resv.AssetName.AssetName)
		if changeAmount.Sign() > 0 {
			if len(req.Tx.TxOut) < 2 {
				return fmt.Errorf("RGB11 splice has no native change carrier")
			}
			change = req.Tx.TxOut[1]
		}
	}
	dbcIndex := 0
	if req.Operation == STPOpSpliceIn {
		dbcIndex = 1
	}
	if _, err := rgb11wallet.ReserveChannelDBC(req.Tx, dbcIndex); err != nil {
		return err
	}
	mainIndex, err := stpOutputIndex(req.Tx, main)
	if err != nil {
		return err
	}
	assignments := []rgb11wallet.ChannelAssignment{{OutputIndex: uint32(mainIndex), Amount: *resv.SplicingAmt.Clone()}}
	changeIndex := -1
	if change != nil {
		changeIndex, err = stpOutputIndex(req.Tx, change)
		if err != nil {
			return err
		}
		assignments = append(assignments, rgb11wallet.ChannelAssignment{OutputIndex: uint32(changeIndex), Amount: *changeAmount.Clone()})
	}
	channelContext := rgb11wallet.ChannelContext{ChannelID: resv.Channel.ChannelId, Operation: string(req.Operation),
		Height: uint64(resv.Channel.CommitHeight) + 1, Branch: "funding", Entropy: resv.Channel.AssetEntropy, BitcoinNetwork: GetChainParam().Name}
	step, err := rgb11wallet.BuildChannelStep(context.Background(), sources, req.Tx, uint32(dbcIndex), assignments, channelContext, evidence)
	if err != nil {
		return err
	}
	pkg := &rgb11wallet.ChannelPackage{Version: 1, Funding: sources, Steps: []rgb11wallet.ChannelStep{*step}}
	if _, err := rgb11wallet.ValidateChannelPackage(context.Background(), pkg, req.Tx, evidence); err != nil {
		return err
	}
	raw, err := rgb11wallet.EncodeChannelPackage(pkg)
	if err != nil {
		return err
	}
	if req.Operation == STPOpSpliceIn {
		if !bytes.Equal(main.PkScript, resv.Channel.GetChannelPkScript()) {
			return fmt.Errorf("RGB11 splice funding changed channel script")
		}
		funding, err := rgb11wallet.ChannelFundingFromStep(*step, uint32(mainIndex), *resv.SplicingAmt)
		if err != nil {
			return err
		}
		remaining = append(remaining, funding)
		resv.NativeAnchorProof, err = funding.NativeProof()
		if err != nil {
			return err
		}
	} else if change != nil {
		if !bytes.Equal(change.PkScript, resv.Channel.GetChannelPkScript()) {
			return fmt.Errorf("RGB11 splice change changed channel script")
		}
		funding, err := rgb11wallet.ChannelFundingFromStep(*step, uint32(changeIndex), *changeAmount)
		if err != nil {
			return err
		}
		remaining = append(remaining, funding)
	}
	updated, err := rgb11wallet.EncodeChannelInventoryWithLast(remaining, raw)
	if err != nil {
		return err
	}
	if resv.AssetProofs == nil {
		resv.AssetProofs = make(map[string][]byte)
	}
	resv.AssetProofs[rgb11wallet.Protocol] = raw
	if resv.Channel.AssetFunding == nil {
		resv.Channel.AssetFunding = make(map[string][]byte)
	}
	resv.Channel.AssetFunding[rgb11wallet.Protocol] = updated
	resv.AssetFunding = cloneSTPAssetProofs(resv.Channel.AssetFunding)
	resv.AssetEntropy = resv.Channel.AssetEntropy
	return nil
}

func (rgb11STPProtocol) VerifyChannelProof(p *Manager, channel *Channel, tx *wire.MsgTx, raw []byte, operation STPAssetOperation) ([]*TxOutput, error) {
	pkg, err := rgb11wallet.DecodeChannelPackage(raw)
	if err != nil {
		return nil, err
	}
	evidence, err := stpRGB11Evidence(p)
	if err != nil {
		return nil, err
	}
	if channel != nil {
		if err := verifySTPRGB11PackageContext(channel, pkg); err != nil {
			return nil, err
		}
		inventory, err := stpRGB11Inventory(channel)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(raw, inventory.LastPackage) {
			left, err := rgb11wallet.EncodeChannelInventory(pkg.Funding)
			if err != nil {
				return nil, err
			}
			right, err := rgb11wallet.EncodeChannelInventory(inventory.Sources)
			if err != nil || !bytes.Equal(left, right) {
				return nil, fmt.Errorf("RGB11 proof is not rooted in channel funding")
			}
		}
	}
	knownSources := make(map[string]indexer.AssetName, len(pkg.Funding))
	for _, source := range pkg.Funding {
		knownSources[source.OutPoint] = source.AssetName
	}
	if err := verifySTPRGB11KnownCoassets(p, knownSources); err != nil {
		return nil, err
	}
	report, err := rgb11wallet.ValidateChannelPackage(context.Background(), pkg, tx, evidence)
	if err != nil {
		return nil, err
	}
	outputs := make([]*TxOutput, len(tx.TxOut))
	for i := range outputs {
		outputs[i] = indexer.GenerateTxOutput(tx, i)
	}
	for _, allocation := range report.TerminalAllocations {
		point, err := wire.NewOutPointFromString(allocation.OutPoint)
		if err != nil || point.Hash.String() != tx.TxID() || uint64(point.Index) >= uint64(len(outputs)) {
			return nil, rgb11wallet.ErrValidationReceipt
		}
		if err := outputs[point.Index].Assets.Add(&indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}); err != nil {
			return nil, err
		}
	}
	return outputs, nil
}

func verifySTPRGB11PackageContext(channel *Channel, pkg *rgb11wallet.ChannelPackage) error {
	if channel == nil || pkg == nil || channel.AssetEntropy == ([32]byte{}) {
		return fmt.Errorf("missing native channel context")
	}
	for _, step := range pkg.Steps {
		if step.Context.ChannelID != channel.ChannelId || step.Context.Entropy != channel.AssetEntropy || step.Context.BitcoinNetwork != GetChainParam().Name ||
			step.Context.Height > uint64(channel.CommitHeight)+1 {
			return fmt.Errorf("RGB11 proof has foreign channel construction context")
		}
	}
	return nil
}

func (protocol rgb11STPProtocol) VerifyChannelJournalContext(p *Manager, channel *Channel) error {
	inventory, err := stpRGB11Inventory(channel)
	if err != nil {
		return err
	}
	if channel.AssetEntropy == ([32]byte{}) {
		return fmt.Errorf("missing RGB11 channel entropy")
	}
	// An empty current inventory does not erase the final native exit. It
	// is valid only after every active carrier and both balances are empty;
	// LastPackage below still proves the complete final transition.
	if len(inventory.Sources) == 0 {
		if len(stpRGB11ActiveFunding(channel)) != 0 {
			return fmt.Errorf("RGB11 journal lost its active funding inventory")
		}
		for _, balances := range []map[AssetName]*Decimal{channel.GetCommitLocalBalance(), channel.GetCommitRemoteBalance()} {
			for name, amount := range balances {
				if name.Protocol == rgb11wallet.Protocol && amount.Sign() != 0 {
					return fmt.Errorf("RGB11 journal lost a nonzero channel balance")
				}
			}
		}
	}
	if len(inventory.LastPackage) == 0 {
		if len(inventory.Sources) == 0 {
			return fmt.Errorf("RGB11 channel has no funding or lifecycle proof")
		}
		return nil
	}
	pkg, err := rgb11wallet.DecodeChannelPackage(inventory.LastPackage)
	if err != nil {
		return err
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	tx := new(wire.MsgTx)
	if err := tx.Deserialize(bytes.NewReader(last.Tx)); err != nil {
		return err
	}
	_, err = protocol.VerifyChannelProof(p, channel, tx, inventory.LastPackage, STPOpCommit)
	if err != nil {
		return err
	}
	// Every current source produced by that step must retain its exact public
	// history. The final exit has none, but its last complete step stays here.
	byPoint := make(map[string]rgb11wallet.ChannelFunding)
	for _, source := range inventory.Sources {
		byPoint[source.OutPoint] = source
	}
	for _, assignment := range last.Assignments {
		if !bytes.Equal(tx.TxOut[assignment.OutputIndex].PkScript, channel.GetChannelPkScript()) {
			continue
		}
		funding, err := rgb11wallet.ChannelFundingFromStep(last, assignment.OutputIndex, assignment.Amount)
		if err != nil {
			return err
		}
		stored, exists := byPoint[funding.OutPoint]
		if !exists || !sameSTPRGB11Funding(stored, funding) {
			return fmt.Errorf("RGB11 journal lost its channel allocation")
		}
	}
	return nil
}

func (rgb11STPProtocol) VerifyUndisclosedChannelInputs(p *Manager, tx *wire.MsgTx) error {
	if p == nil || tx == nil {
		return fmt.Errorf("missing RGB11 input verification context")
	}
	inputs := make(map[string]bool)
	for _, input := range tx.TxIn {
		inputs[input.PreviousOutPoint.String()] = true
	}
	for _, account := range p.localRGB11Accounts() {
		manager, err := p.newScopedRGB11Manager(account)
		if err != nil {
			return err
		}
		proofs, err := manager.projectionStore.ListProofs()
		if err != nil {
			return err
		}
		for _, proof := range proofs {
			if proof != nil && inputs[proof.OutPoint] {
				return fmt.Errorf("known RGB11 carrier %s has no native channel proof", proof.OutPoint)
			}
		}
	}
	return nil
}

func sortedSTPRGB11Indexes(indexes []int) ([]int, error) {
	result := append([]int(nil), indexes...)
	sort.Ints(result)
	for i, index := range result {
		if index < 0 || (i != 0 && index == result[i-1]) {
			return nil, fmt.Errorf("invalid or duplicate native recovery output")
		}
	}
	return result, nil
}
