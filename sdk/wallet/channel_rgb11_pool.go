package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// A core's observed private settlement may return its own allocation to the
// bootstrap pool. Only a public source proof and observed L2 locators cross
// that boundary; the private package and channel entropy remain local.
func (p *Manager) registerObservedRGB11ChannelPoolSources(ctx context.Context, owner common.Wallet,
	privateWitness []byte, tx *wire.MsgTx, pkg *rgb11wallet.ChannelPackage, evidence rgb11wallet.BitcoinEvidenceProvider) error {
	if p == nil || p.cfg == nil || p.cfg.Mode != SERVER_NODE {
		return nil
	}
	if owner == nil || owner.GetPaymentPubKey() == nil || tx == nil || pkg == nil || len(pkg.Steps) == 0 {
		return ErrRGB11Inconsistent
	}
	bootstrap := p.GetBootstrapNodePaymentPubKey()
	if bootstrap == nil {
		return fmt.Errorf("native private settlement has no bootstrap payment key")
	}
	_, script, err := GetP2WSHscript(owner.GetPaymentPubKey().SerializeCompressed(), bootstrap.SerializeCompressed())
	if err != nil {
		return err
	}
	channelID, err := AddrFromPkScript(script)
	if err != nil {
		return err
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	if last.Context.ChannelID == channelID {
		return nil
	}
	var descends []string
	for _, assignment := range last.Assignments {
		if uint64(assignment.OutputIndex) >= uint64(len(tx.TxOut)) {
			return rgb11wallet.ErrValidationReceipt
		}
		output := tx.TxOut[assignment.OutputIndex]
		if output == nil || !bytes.Equal(output.PkScript, script) {
			continue
		}
		funding, err := rgb11wallet.ChannelFundingFromStep(last, assignment.OutputIndex, assignment.Amount)
		if err != nil {
			return err
		}
		spend, err := evidence.GetOutspend(funding.OutPoint)
		if err != nil || spend == nil {
			return fmt.Errorf("native pool settlement outspend unavailable: %w", errRGB11ProjectionEvidence(err))
		}
		if spend.Spent {
			if !rgb11KnownObservedChannelSpend(evidence, funding.OutPoint, spend.SpendingTx) {
				return fmt.Errorf("native pool settlement has no exact observed spender")
			}
			continue
		}
		if spend.SpendingTx != "" {
			return fmt.Errorf("contradictory native pool outspend")
		}
		proof, err := funding.NativeProof()
		if err != nil {
			return err
		}
		if descends == nil {
			descends, err = p.collectRGB11PrivateSettlementDescends(owner, last.Context.ChannelID, pkg)
			if err != nil {
				return err
			}
		}
		sourceOutput := indexer.NewTxOutput(output.Value)
		sourceOutput.OutPointStr, sourceOutput.OutValue.PkScript = funding.OutPoint, append([]byte(nil), output.PkScript...)
		sourceOutput.Assets = indexer.TxAssets{{Name: funding.AssetName, Amount: *funding.Amount.Clone(), BindingSat: 0}}
		var locators []string
		previous, err := p.loadNativeChannelSource(channelID, funding.OutPoint)
		if err == nil {
			locators = append(locators, previous.DescendTxIDs...)
		} else if !errors.Is(err, indexer.ErrKeyNotFound) {
			return err
		}
		seen := make(map[string]bool)
		for _, id := range locators {
			seen[id] = true
		}
		for _, id := range descends {
			if !seen[id] {
				locators = append(locators, id)
				seen[id] = true
			}
		}
		source := &NativeChannelSource{Version: 1, ChannelID: channelID, Output: sourceOutput, PublicProof: proof,
			DescendTxIDs: locators, PrivateWitness: append([]byte(nil), privateWitness...)}
		if err := p.RegisterNativeChannelSettlementSource(ctx, owner, channelID, source); err != nil {
			return err
		}
	}
	return nil
}
