package wallet

import (
	"bytes"
	"fmt"
	"math"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

// STPSweepRGB11Proof exports only the public terminal transition for the exact
// sweep. Never serialize the private ChannelPackage into a signing request.
func STPSweepRGB11Proof(tx *wire.MsgTx, proofs map[string][]byte) (*wwire.RGB11SigningProof, error) {
	if len(proofs[rgb11wallet.Protocol]) == 0 {
		return nil, nil
	}
	pkg, err := rgb11wallet.DecodeChannelPackage(proofs[rgb11wallet.Protocol])
	if err != nil {
		return nil, err
	}
	if tx == nil || len(pkg.Steps) == 0 {
		return nil, fmt.Errorf("native sweep has no terminal transaction")
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	declared := new(wire.MsgTx)
	reader := bytes.NewReader(last.Tx)
	if declared.Deserialize(reader) != nil || reader.Len() != 0 || declared.TxHash() != tx.TxHash() || len(last.Consignment) == 0 {
		return nil, fmt.Errorf("native sweep proof does not bind the exact transaction")
	}
	proof := &wwire.RGB11SigningProof{Consignment: append([]byte(nil), last.Consignment...)}
	for _, seal := range last.Reveals {
		proof.ChangeSeals = append(proof.ChangeSeals, append([]byte(nil), seal...))
	}
	return proof, nil
}

// ValidateSTPServerSweep independently reconstructs every asset before the
// server releases a sweep signature. Previous contains only authenticated
// predecessor outputs, not amounts or prevouts claimed by a remote caller.
// No manager/channel mutex may be held: reconstruction can access chain data.
func (p *Manager) ValidateSTPServerSweep(tx *wire.MsgTx, destination []byte,
	previous map[string]*TxOutput, proof *wwire.RGB11SigningProof) ([]*TxOutput, error) {
	if p == nil || tx == nil || len(tx.TxIn) == 0 || len(tx.TxOut) == 0 || len(destination) == 0 {
		return nil, fmt.Errorf("incomplete STP sweep request")
	}
	seen := make(map[wire.OutPoint]bool, len(tx.TxIn))
	for _, input := range tx.TxIn {
		if input == nil || seen[input.PreviousOutPoint] {
			return nil, fmt.Errorf("nil or duplicate STP sweep input")
		}
		seen[input.PreviousOutPoint] = true
	}
	for _, output := range tx.TxOut {
		if output == nil || output.Value < 0 ||
			(txscript.IsUnspendable(output.PkScript) && output.Value != 0) ||
			(!txscript.IsUnspendable(output.PkScript) && !bytes.Equal(output.PkScript, destination)) {
			return nil, fmt.Errorf("STP sweep changed its recipient or valued commitment")
		}
	}
	var inputs, outputs []*TxOutput
	var err error
	if proof != nil {
		inputs, outputs, err = p.rebuildRGB11TxOutput(tx, proof, true)
	} else {
		for _, protocol := range stpAssetProtocols.ordered {
			if verifier, ok := protocol.(STPAssetHiddenInputVerifier); ok {
				if err := verifier.VerifyUndisclosedChannelInputs(p, tx); err != nil {
					return nil, err
				}
			}
		}
		inputs, outputs, err = p.RebuildTxOutput(tx, previous)
	}
	if err != nil {
		return nil, err
	}
	if len(inputs) != len(tx.TxIn) || len(outputs) != len(tx.TxOut) {
		return nil, fmt.Errorf("incomplete STP sweep asset view")
	}
	inTotals, outTotals := make(map[indexer.AssetName]*Decimal), make(map[indexer.AssetName]*Decimal)
	add := func(totals map[indexer.AssetName]*Decimal, output *TxOutput) error {
		if output == nil || len(output.Invalids) != 0 {
			return fmt.Errorf("invalid STP sweep asset state")
		}
		for _, asset := range output.Assets {
			if asset.Amount.Validate() != nil || asset.Amount.Sign() < 0 {
				return fmt.Errorf("invalid STP sweep asset quantity")
			}
			if indexer.IsPlainAsset(&asset.Name) || asset.Amount.Sign() == 0 {
				continue
			}
			if err := ValidateSTPAsset(&asset.Name, STPOpForceClose); err != nil {
				return err
			}
			if asset.Name.Protocol == rgb11wallet.Protocol && proof == nil {
				return fmt.Errorf("known RGB11 sweep input requires a native proof")
			}
			totals[asset.Name] = totals[asset.Name].Add(&asset.Amount)
		}
		return nil
	}
	var value int64
	for i, input := range inputs {
		if input == nil || input.OutPoint() == nil || *input.OutPoint() != tx.TxIn[i].PreviousOutPoint ||
			input.Value() < 0 || value > math.MaxInt64-input.Value() {
			return nil, fmt.Errorf("invalid STP sweep previous output")
		}
		value += input.Value()
		if err := add(inTotals, input); err != nil {
			return nil, err
		}
	}
	for i, output := range outputs {
		if output == nil || output.Value() != tx.TxOut[i].Value || !bytes.Equal(output.OutValue.PkScript, tx.TxOut[i].PkScript) || output.Value() > value {
			return nil, fmt.Errorf("STP sweep output view or Bitcoin conservation mismatch")
		}
		value -= output.Value()
		if txscript.IsUnspendable(output.OutValue.PkScript) && output.HasAsset() {
			return nil, fmt.Errorf("STP sweep must not burn assets in a commitment output")
		}
		if err := add(outTotals, output); err != nil {
			return nil, err
		}
	}
	if len(inTotals) != len(outTotals) {
		return nil, fmt.Errorf("STP sweep discarded an asset")
	}
	for name, amount := range inTotals {
		if amount.Cmp(outTotals[name]) != 0 {
			return nil, fmt.Errorf("STP sweep does not conserve %s", name.String())
		}
	}
	return inputs, nil
}
