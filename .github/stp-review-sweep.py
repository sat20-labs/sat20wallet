from pathlib import Path


def replace(path, old, new):
    p = Path(path)
    text = p.read_text()
    if text.count(old) != 1:
        raise SystemExit(f"Expected one reviewed preimage in {path}, got {text.count(old)}")
    p.write_text(text.replace(old, new))


replace('sdk/wire/types_contract.go', '''type RemoteSignMoreData struct {
	Tx       []*TxSignInfo `json:"tx1"`
	Witness  []byte        `json:"witness"`
	Action   string        `json:"action"`
	MoreData []byte        `json:"more"` // 有时候Tx中无法放入足够数据
}''', '''type RemoteSignMoreData struct {
	Tx       []*TxSignInfo `json:"tx1"`
	Witness  []byte        `json:"witness"`
	Action   string        `json:"action"`
	MoreData []byte        `json:"more"` // 有时候Tx中无法放入足够数据
	// Only public consignment material belongs in a sweep signing request.
	// Private channel entropy and alternative recovery branches stay local.
	RGB11Proof *RGB11SigningProof `json:"rgb11,omitempty"`
}''')

path = 'sdk/wallet/rgb11_channel_proof.go'
replace(path, '''func (p *Manager) RebuildRGB11TxOutput(tx *wire.MsgTx, proof *wwire.RGB11SigningProof) ([]*TxOutput, []*TxOutput, error) {
''', '''func (p *Manager) RebuildRGB11TxOutput(tx *wire.MsgTx, proof *wwire.RGB11SigningProof) ([]*TxOutput, []*TxOutput, error) {
	return p.rebuildRGB11TxOutput(tx, proof, false)
}

// Mixed custody sweeps may carry other protocols on separate inputs. Their
// assignments must be reconstructed independently, never inferred from RGB.
// Ordinary single-asset sends retain their stricter preservation boundary.
func (p *Manager) rebuildRGB11TxOutput(tx *wire.MsgTx, proof *wwire.RGB11SigningProof, mixedSweep bool) ([]*TxOutput, []*TxOutput, error) {
''')
replace(path, '''	inputs := make([]*TxOutput, 0, len(tx.TxIn))
	knownRGB := make(map[indexer.AssetName]bool)''', '''	inputs := make([]*TxOutput, 0, len(tx.TxIn))
	publicInputs := make(map[string]*TxOutput, len(tx.TxIn))
	knownRGB := make(map[indexer.AssetName]bool)''')
replace(path, '''		for _, asset := range output.Assets {
			if !indexer.IsPlainAsset(&asset.Name) {
				return nil, nil, ErrRGB11AssetPreservation
			}
		}''', '''		for _, asset := range output.Assets {
			if !indexer.IsPlainAsset(&asset.Name) &&
				(!mixedSweep || len(nativeInputs[input.PreviousOutPoint.String()]) != 0 || asset.Name.Protocol == rgb11wallet.Protocol) {
				return nil, nil, ErrRGB11AssetPreservation
			}
		}
		publicInputs[input.PreviousOutPoint.String()] = output.Clone()''')
replace(path, '''	return inputs, outputs, nil
}''', '''	if mixedSweep {
		publicIn, publicOut, err := p.RebuildTxOutput(tx, publicInputs)
		if err != nil {
			return nil, nil, err
		}
		if len(publicIn) != len(inputs) || len(publicOut) != len(outputs) {
			return nil, nil, ErrRGB11AssetPreservation
		}
		for i, input := range inputs {
			if publicIn[i].OutPointStr != input.OutPointStr {
				return nil, nil, ErrRGB11AssetPreservation
			}
			for _, asset := range input.Assets {
				if err := publicIn[i].Assets.Add(&asset); err != nil {
					return nil, nil, err
				}
			}
		}
		for i, output := range outputs {
			for _, asset := range output.Assets {
				if err := publicOut[i].Assets.Add(&asset); err != nil {
					return nil, nil, err
				}
			}
		}
		return publicIn, publicOut, nil
	}
	return inputs, outputs, nil
}''')

p = Path('sdk/wallet/stp_sweep_verify.go')
if p.exists():
    raise SystemExit('Refusing to overwrite sweep validation')
p.write_text('''package wallet

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
''')

p = Path('sdk/wallet/stp_sweep_verify_test.go')
if p.exists():
    raise SystemExit('Refusing to overwrite sweep regression')
p.write_text('''package wallet

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/stretchr/testify/require"
)

func TestSTPServerSweepChecksARC20ConservationAndRecipient(t *testing.T) {
	atomicalsTestEnvironment(t)
	manager, plan, client := atomicalsTestTransfer(t, 1000, 1400, 100, 400)
	script := plan.Tx.TxOut[0].PkScript
	inputs, err := manager.ValidateSTPServerSweep(plan.Tx, script, client.outputs, nil)
	require.NoError(t, err)
	require.Len(t, inputs, len(plan.Tx.TxIn))
	broken := plan.Tx.Copy()
	broken.TxOut = broken.TxOut[:1]
	_, err = manager.ValidateSTPServerSweep(broken, script, client.outputs, nil)
	require.Error(t, err, "signing must reject ARC20 burns")
	broken = plan.Tx.Copy()
	broken.TxOut[0].PkScript = []byte{txscript.OP_TRUE}
	_, err = manager.ValidateSTPServerSweep(broken, script, client.outputs, nil)
	require.Error(t, err, "signing must preserve the fixed pool recipient")
	broken = plan.Tx.Copy()
	broken.AddTxOut(wire.NewTxOut(1, []byte{txscript.OP_RETURN}))
	_, err = manager.ValidateSTPServerSweep(broken, script, client.outputs, nil)
	require.Error(t, err, "commitment outputs cannot destroy Bitcoin value")
	broken = plan.Tx.Copy()
	broken.AddTxIn(broken.TxIn[0])
	_, err = manager.ValidateSTPServerSweep(broken, script, client.outputs, nil)
	require.Error(t, err, "duplicate input must be rejected before asset processing")
}

func TestSTPSweepRGB11ExportsOnlyBoundPublicMaterial(t *testing.T) {
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 1}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(330, []byte{txscript.OP_TRUE}))
	var raw bytes.Buffer
	require.NoError(t, tx.SerializeNoWitness(&raw))
	pkg := &rgb11wallet.ChannelPackage{Version: 1, Funding: []rgb11wallet.ChannelFunding{{}},
		Steps: []rgb11wallet.ChannelStep{{Tx: raw.Bytes(), Consignment: []byte("public proof"), Reveals: [][]byte{[]byte("public seal")},
			Context: rgb11wallet.ChannelContext{Entropy: [32]byte{99}, ChannelID: "private-channel"}}}}
	encoded, err := rgb11wallet.EncodeChannelPackage(pkg)
	require.NoError(t, err)
	proof, err := STPSweepRGB11Proof(tx, map[string][]byte{"rgb11": encoded})
	require.NoError(t, err)
	require.Equal(t, []byte("public proof"), proof.Consignment)
	require.Equal(t, [][]byte{[]byte("public seal")}, proof.ChangeSeals)
	// Returned slices must not alias the private lifecycle record.
	proof.Consignment[0] = 'x'
	proof2, err := STPSweepRGB11Proof(tx, map[string][]byte{"rgb11": encoded})
	require.NoError(t, err)
	require.Equal(t, []byte("public proof"), proof2.Consignment)
	changed := tx.Copy()
	changed.TxOut[0].Value++
	_, err = STPSweepRGB11Proof(changed, map[string][]byte{"rgb11": encoded})
	require.Error(t, err)
	_, err = STPSweepRGB11Proof(tx, map[string][]byte{"rgb11": []byte("invalid")})
	require.Error(t, err)
	proof, err = STPSweepRGB11Proof(tx, nil)
	require.NoError(t, err)
	require.Nil(t, proof)
	_ = indexer.ASSET_PLAIN_SAT
}
''')
