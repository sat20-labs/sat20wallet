package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

func (protocol rgb11STPProtocol) ImportPreparedChannelFunding(p *Manager, channel *Channel, parents []*wire.MsgTx) error {
	inventory, err := stpRGB11Inventory(channel)
	if err != nil {
		return err
	}
	if len(inventory.LastPackage) == 0 {
		return nil
	}
	pkg, err := rgb11wallet.DecodeChannelPackage(inventory.LastPackage)
	if err != nil {
		return err
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	expected := new(wire.MsgTx)
	if err := expected.Deserialize(bytes.NewReader(last.Tx)); err != nil {
		return err
	}
	for _, parent := range parents {
		if parent != nil && parent.TxHash() == expected.TxHash() {
			return protocol.ImportChannelSettlement(p, parent, inventory.LastPackage)
		}
	}
	// Expansion may refer to a previously imported funding output without a
	// newly published funding transaction or new ordinary-wallet change.
	return nil
}

func (protocol rgb11STPProtocol) ImportChannelSettlement(p *Manager, tx *wire.MsgTx, raw []byte) (err error) {
	defer func() {
		if err != nil && tx != nil {
			Log.Warningf("RGB11 channel settlement %s import failed: %v", tx.TxID(), err)
		}
	}()
	pkg, err := rgb11wallet.DecodeChannelPackage(raw)
	if err != nil {
		return err
	}
	channel, owner, err := p.stpRGB11SettlementOwner(pkg.Steps[len(pkg.Steps)-1].Context.ChannelID)
	if err != nil {
		return err
	}
	if err := verifySTPRGB11PackageContext(channel, pkg); err != nil {
		return err
	}
	// A late confirmation or restart may revisit a mined splice after a newer
	// channel inventory has already consumed its outputs. Current inventory
	// authorizes candidate signing, while this import separately requires the
	// complete observed history and exact current ownership of each terminal.
	if _, err := protocol.VerifyChannelProof(p, nil, tx, raw, STPOpForceClose); err != nil {
		return err
	}
	evidence, err := stpRGB11Evidence(p)
	if err != nil {
		return err
	}
	// Prepared graph validity is insufficient here. Every terminal is checked
	// against an actually mined witness, even if this account owns none of it.
	last := pkg.Steps[len(pkg.Steps)-1]
	for _, assignment := range last.Assignments {
		funding, err := rgb11wallet.ChannelFundingFromStep(last, assignment.OutputIndex, assignment.Amount)
		if err != nil {
			return err
		}
		if _, err := rgb11wallet.ValidateChannelFunding(context.Background(), funding, evidence, nativeproof.Historical, GetChainParam().Name); err != nil {
			return err
		}
	}
	_, err = runRGB11ManagedOperationWithManager(p, context.Background(), rgb11ManagedOperationContinue,
		func() (*rgb11Manager, error) { return p.stpRGB11Scope(owner) },
		func(manager *rgb11Manager) (*RGB11ImportResult, error) {
			return manager.importObservedRGB11ChannelTransfer(context.Background(), tx, pkg)
		})
	if err != nil {
		return err
	}
	return p.registerObservedRGB11ChannelPoolSources(context.Background(), owner, channel.RedeemScript, tx, pkg, evidence)
}

func (p *Manager) stpRGB11SettlementOwner(channelID string) (*Channel, common.Wallet, error) {
	channel := p.GetChannel(channelID)
	if channel != nil && channel.LocalWallet() != nil {
		return channel, channel.LocalWallet(), nil
	}
	stored, err := p.LoadChannelInDB(channelID)
	if err != nil {
		stored, err = LoadBackupChannelInDB(p.db, channelID)
		if err != nil {
			return nil, nil, err
		}
	}
	if stored == nil || stored.ChannelId != channelID || stored.LocalChanCfg.PaymentKey == nil {
		return nil, nil, ErrRGB11Inconsistent
	}
	for _, account := range p.localRGB11Accounts() {
		if account.WalletID == stored.LocalWalletId && account.AccountIndex == stored.LocalChanCfg.WalletId &&
			account.Wallet.GetPaymentPubKey().IsEqual(stored.LocalChanCfg.PaymentKey) {
			channel = &Channel{ChannelInDB: *stored, manager: p, localWallet: account.Wallet}
			return channel, account.Wallet, nil
		}
	}
	return nil, nil, fmt.Errorf("native settlement has no matching local wallet")
}

// Historical validation supplies exact graph facts. Current ownership is
// established independently for each final output, so an already spent sibling
// neither breaks import nor resurrects an ancestor as a spendable balance.
func (p *rgb11Manager) importObservedRGB11ChannelTransfer(ctx context.Context, tx *wire.MsgTx, pkg *rgb11wallet.ChannelPackage) (*RGB11ImportResult, error) {
	return retryNativeProjection(ctx, func() (*RGB11ImportResult, error) {
		return p.importObservedRGB11ChannelTransferAttempt(ctx, tx, pkg)
	})
}

func (p *rgb11Manager) importObservedRGB11ChannelTransferAttempt(ctx context.Context, tx *wire.MsgTx, pkg *rgb11wallet.ChannelPackage) (*RGB11ImportResult, error) {
	if p == nil || p.projectionStore == nil || p.evidence == nil || p.wallet == nil || tx == nil || pkg == nil || len(pkg.Steps) == 0 {
		return nil, ErrRGB11Inconsistent
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	material, err := rgb11wallet.PublicChannelConsignment(last.Consignment, last.Reveals)
	if err != nil {
		return nil, err
	}
	var reveals []seals.GraphBlindSeal
	for _, raw := range last.Reveals {
		seal, err := seals.DecodeGraphBlindSeal(raw)
		if err != nil {
			return nil, err
		}
		reveals = append(reveals, seal)
	}
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(reveals...)
	history, err := validator.ValidateHistoricalConsignmentReport(ctx, material, p.evidence)
	if err != nil || history == nil || history.MaterialReceipt == nil {
		return nil, fmt.Errorf("validate native settlement history: %w", errRGB11ProjectionEvidence(err))
	}
	container, err := coreconsignment.Decode(material)
	if err != nil {
		return nil, err
	}
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	current, _, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return nil, err
	}
	receipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(current.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	previousProofs, err := rgb11wallet.ProofsFromProjectionSnapshot(current.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	previousOutputs, err := rgb11wallet.OutputsFromProjectionSnapshot(current.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	byPoint := make(map[string]*rgb11wallet.AllocationProof)
	for _, proof := range previousProofs {
		if proof.AssetName == last.Sources[0].AssetName {
			if byPoint[proof.OutPoint] != nil {
				return nil, rgb11wallet.ErrInvalidProof
			}
			byPoint[proof.OutPoint] = proof
		}
	}
	existing := receipts[history.MaterialReceipt.ConsignmentHash]
	if existing != nil && !sameRGB11ValidationReceiptIdentity(existing, history.MaterialReceipt) {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	type ownedAllocation struct {
		allocation rgb11wallet.ValidatedAllocation
		utxo       *rgb11wallet.BitcoinUTXO
		binding    *rgb11wallet.CarrierBinding
	}
	var owned []ownedAllocation
	var needed []rgb11wallet.ValidatedAllocation
	seen := make(map[string]bool)
	for _, item := range history.Allocations {
		allocation := item.Allocation
		point, err := wire.NewOutPointFromString(allocation.OutPoint)
		if err != nil || point.Hash.String() != tx.TxID() {
			continue
		}
		if uint64(point.Index) >= uint64(len(tx.TxOut)) {
			return nil, rgb11wallet.ErrValidationReceipt
		}
		if !txscript.IsPayToTaproot(tx.TxOut[point.Index].PkScript) || !bytes.Equal(tx.TxOut[point.Index].PkScript, walletScript) {
			// Private P2WSH funding, delayed outputs and the public pool
			// have separate authorization and are never ordinary balances.
			continue
		}
		if seen[allocation.OutPoint] {
			return nil, fmt.Errorf("multiple RGB11 allocations share a wallet projection carrier")
		}
		seen[allocation.OutPoint] = true
		spend, err := p.evidence.GetOutspend(allocation.OutPoint)
		if err != nil || spend == nil {
			return nil, fmt.Errorf("native settlement outspend unavailable: %w", errRGB11ProjectionEvidence(err))
		}
		if spend.Spent {
			if !rgb11KnownObservedChannelSpend(p.evidence, allocation.OutPoint, spend.SpendingTx) {
				return nil, fmt.Errorf("native settlement has no exact observed spender")
			}
			continue
		}
		if spend.SpendingTx != "" {
			return nil, fmt.Errorf("contradictory native settlement outspend")
		}
		funding, err := rgb11wallet.ChannelFundingFromStep(last, point.Index, allocation.Amount)
		if err != nil {
			return nil, err
		}
		if _, err := rgb11wallet.ValidateChannelFunding(ctx, funding, p.evidence, nativeproof.Current, GetChainParam().Name); err != nil {
			return nil, err
		}
		utxo, err := p.evidence.GetUTXO(allocation.OutPoint)
		if err != nil || utxo == nil || utxo.Value != funding.Value || !bytes.Equal(utxo.PkScript, walletScript) {
			return nil, fmt.Errorf("native settlement current carrier changed")
		}
		binding, err := p.rgb11CarrierBinding(allocation, utxo)
		if err != nil {
			return nil, err
		}
		// Exact P2TR matching above deliberately avoids the wider generic
		// ownsRGB11Carrier path, which also recognizes private channels.
		owned = append(owned, ownedAllocation{allocation, utxo, binding})
		needed = append(needed, allocation)
	}
	projectionReceipt, err := rgb11wallet.SelectHistoricalValidationReceipt(history, existing, needed)
	if err != nil {
		return nil, err
	}
	projectionRecords := current.ProjectionRecords
	if existing != nil {
		oldHash, err := existing.Hash()
		if err != nil {
			return nil, err
		}
		newHash, err := projectionReceipt.Hash()
		if err != nil {
			return nil, err
		}
		if oldHash != newHash {
			projectionRecords, err = rgb11wallet.RebindValidatedConsignmentReceiptSnapshot(projectionRecords, existing, projectionReceipt)
			if err != nil {
				return nil, err
			}
		}
	}
	receiptHash, err := projectionReceipt.Hash()
	if err != nil {
		return nil, err
	}
	var replacements []rgb11wallet.ProjectionReplacement
	for _, item := range owned {
		allocation := item.allocation
		if previous := byPoint[allocation.OutPoint]; previous != nil {
			// Managed sends can already own this exact Opout under their
			// original object hash. Keep that recovery identity immutable;
			// the complete public object is added separately below.
			retained := receipts[previous.ConsignmentHash]
			if retained == nil || retained.ContractID != history.MaterialReceipt.ContractID || retained.SchemaID != history.MaterialReceipt.SchemaID {
				return nil, rgb11wallet.ErrValidationReceipt
			}
			hash, err := retained.Hash()
			output := previousOutputs[allocation.OutPoint]
			if err != nil || hash != previous.ValidationHash || previous.WitnessTxID != tx.TxID() ||
				!sameRGB11CarrierBinding(previous.CarrierBinding, item.binding) || output == nil ||
				output.Value() != item.utxo.Value || !bytes.Equal(output.OutValue.PkScript, item.utxo.PkScript) ||
				!rgb11ProofMatchesHistoricalReceipt(previous, retained, output, history) {
				return nil, fmt.Errorf("existing private settlement projection differs from independently validated history")
			}
			continue
		}
		commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
		proof := &rgb11wallet.AllocationProof{OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType, AssignmentIndex: allocation.AssignmentIndex,
			StateClass: allocation.StateClass, StateData: append([]byte(nil), allocation.StateData...),
			SealCommitment: hex.EncodeToString(commitment[:]), SealDisclosure: append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: projectionReceipt.ConsignmentHash, ValidationHash: receiptHash,
			WitnessTxID: tx.TxID(), Status: "valid", CarrierBinding: item.binding, Confirmations: item.utxo.Confirmations}
		output := indexer.NewTxOutput(item.utxo.Value)
		output.OutPointStr, output.OutValue.PkScript = allocation.OutPoint, append([]byte(nil), item.utxo.PkScript...)
		replacements = append(replacements, rgb11wallet.ProjectionReplacement{Output: output,
			Asset: &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}, Proof: proof})
	}
	projectionRecords, err = rgb11wallet.AddValidatedConsignmentProjectionSnapshot(projectionRecords, material, projectionReceipt, replacements)
	if err != nil {
		return nil, err
	}
	contractFile, _, err := rgb11ContractFromValidatedHistory(material, projectionReceipt.ContractID)
	if err != nil {
		return nil, err
	}
	projectionRecords, err = rgb11wallet.AddContractObjectSnapshotReference(projectionRecords, projectionReceipt.ContractID, contractFile)
	if err != nil {
		return nil, err
	}
	// Reconcile only locally projected ancestors whose exact Bitcoin spend
	// is observed. Keep their immutable objects and receipts for recovery.
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(projectionRecords)
	if err != nil {
		return nil, err
	}
	spent := make(map[string]bool)
	for _, step := range pkg.Steps {
		for _, source := range step.Sources {
			spent[source.OutPoint] = true
		}
	}
	var updates []rgb11wallet.SnapshotProofUpdate
	for _, proof := range proofs {
		if !spent[proof.OutPoint] {
			continue
		}
		outspend, err := p.evidence.GetOutspend(proof.OutPoint)
		if err != nil || outspend == nil || !outspend.Spent || !rgb11KnownObservedChannelSpend(p.evidence, proof.OutPoint, outspend.SpendingTx) {
			return nil, fmt.Errorf("native consumed source is not independently observed")
		}
		updates = append(updates, rgb11wallet.SnapshotProofUpdate{OutPoint: proof.OutPoint, AssetName: proof.AssetName,
			Status: "spending", Confirmations: proof.Confirmations})
	}
	if len(updates) != 0 {
		projectionRecords, err = rgb11wallet.UpdateProjectionSnapshotProofs(projectionRecords, updates)
		if err != nil {
			return nil, err
		}
	}
	candidate := *current
	candidate.ProjectionRecords = projectionRecords
	changed := !sameNativeProjectionRecords(current.ProjectionRecords, projectionRecords)
	if owner := p.accountManagementOwner(); changed && owner != nil {
		scopeID, err := p.rgb11AccountManagedScopeID()
		if err != nil {
			return nil, err
		}
		if _, err := owner.preflightRGB11TerminalRecovery(scopeID, &candidate); err != nil {
			return nil, err
		}
	}
	if changed {
		if _, err := p.applyRGB11CandidateSnapshot(ctx, current, &candidate); err != nil {
			return nil, nativeProjectionPublicationError(err)
		}
		if err := p.autoBackupRGB11AfterMutation(); err != nil {
			return nil, err
		}
	}
	return &RGB11ImportResult{ContractID: container.ContractID, SchemaID: container.SchemaID,
		AssetName: last.Sources[0].AssetName, Receipt: projectionReceipt, Projected: len(replacements)}, nil
}

func rgb11KnownObservedChannelSpend(evidence rgb11wallet.BitcoinEvidenceProvider, point, txid string) bool {
	if evidence == nil || txid == "" {
		return false
	}
	status, err := evidence.GetTxStatus(txid)
	if err != nil || status == nil || status.TxID != txid || (!status.Confirmed && !status.InMempool) {
		return false
	}
	raw, err := evidence.GetRawTx(txid)
	if err != nil {
		return false
	}
	tx := new(wire.MsgTx)
	reader := bytes.NewReader(raw)
	if tx.Deserialize(reader) != nil || reader.Len() != 0 || tx.TxID() != txid {
		return false
	}
	matches := 0
	for _, input := range tx.TxIn {
		if input.PreviousOutPoint.String() == point {
			matches++
		}
	}
	return matches == 1
}
