package wallet

import (
	"bytes"
	"strings"
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
	pkg := &rgb11wallet.ChannelPackage{Version: 1, Funding: []rgb11wallet.ChannelFunding{{Amount: *indexer.NewDefaultDecimal(1)}},
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
}

func TestSTPServerSweepRejectsUnknownProoflessCommitmentsBeforeIO(t *testing.T) {
	destination := []byte{txscript.OP_TRUE}
	for _, script := range [][]byte{
		append([]byte{txscript.OP_RETURN, txscript.OP_DATA_32}, make([]byte, 32)...),
		{txscript.OP_RETURN, txscript.OP_DATA_1, 1},
		{txscript.OP_RETURN, txscript.OP_13, txscript.OP_PUSHDATA1},
	} {
		tx := wire.NewMsgTx(2)
		tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 1}, nil, nil))
		tx.AddTxOut(wire.NewTxOut(330, destination))
		tx.AddTxOut(wire.NewTxOut(0, script))
		// No indexer/database is installed. Unknown commitments must fail
		// before reconstruction could treat invisible assets as plain sats.
		_, err := new(Manager).ValidateSTPServerSweep(tx, destination, nil, nil)
		require.ErrorContains(t, err, "unrecognized commitment without a native proof")
	}
	runes, err := GetRunesEstimatePayload(nil, 0)
	require.NoError(t, err)
	require.True(t, recognizesSTPSweepDataCarrier(runes), "the registered Runes parser must still admit its supported data carrier")
}

func stpSweepHistoricalBRC20Fixture(t *testing.T, historical bool) (*Manager, *wire.MsgTx, []byte, map[string]*TxOutput, indexer.AssetName) {
	t.Helper()
	manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
	client := atomicalsTestConfigure(manager)
	script, err := GetPkScriptFromAddress(manager.wallet.GetAddress())
	require.NoError(t, err)
	name := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}
	tx := wire.NewMsgTx(2)
	add := func(position int, quantity int64, invalid bool) {
		output := atomicalsTestInput(position, nil, 0, 330, script)
		asset := indexer.AssetInfo{Name: name, Amount: *indexer.NewDefaultDecimal(quantity), BindingSat: 0}
		output.Assets = indexer.TxAssets{asset}
		output.Offsets[name] = indexer.AssetOffsets{{Start: 0, End: 1}}
		output.SatBindingMap[0] = asset.Clone()
		output.Invalids[name] = invalid
		client.outputs[output.OutPointStr] = output
		tx.AddTxIn(output.TxIn())
		tx.AddTxOut(wire.NewTxOut(330, script))
	}
	if historical {
		add(901, 300, true) // independently observed, already-consumed display record
	}
	add(902, 7, false) // a new valid transfer of the same ticker must count exactly once
	fee := atomicalsTestInput(903, nil, 0, 10_000, script)
	client.outputs[fee.OutPointStr] = fee
	tx.AddTxIn(fee.TxIn())
	tx.AddTxOut(wire.NewTxOut(9000, script))
	return manager, tx, script, client.outputs, name
}

func TestSTPServerSweepHistoricalBRC20View(t *testing.T) {
	t.Run("false-marker-is-valid", func(t *testing.T) {
		manager, tx, script, facts, name := stpSweepHistoricalBRC20Fixture(t, false)
		inputs, err := manager.ValidateSTPServerSweep(tx, script, facts, nil)
		require.NoError(t, err)
		require.EqualValues(t, 7, inputs[0].GetAsset(&name).Int64())
	})
	t.Run("history-and-new-same-name-transfer", func(t *testing.T) {
		manager, tx, script, facts, name := stpSweepHistoricalBRC20Fixture(t, true)
		inputs, err := manager.ValidateSTPServerSweep(tx, script, facts, nil)
		require.NoError(t, err)
		require.Nil(t, inputs[0].GetAsset(&name), "consumed display data is not a spendable BRC20 allocation")
		require.EqualValues(t, 7, inputs[1].GetAsset(&name).Int64(), "the new valid transfer must remain independently conserved")
	})
	for _, marker := range []struct {
		name  string
		asset indexer.AssetName
	}{
		{"native-true-is-not-history", indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: strings.Repeat("a", 64)}},
		{"unknown-true-is-not-history", indexer.AssetName{Protocol: "unknown-native", Type: indexer.ASSET_TYPE_FT, Ticker: "token"}},
	} {
		t.Run(marker.name, func(t *testing.T) {
			manager, tx, script, facts, _ := stpSweepHistoricalBRC20Fixture(t, false)
			facts[tx.TxIn[len(tx.TxIn)-1].PreviousOutPoint.String()].Invalids[marker.asset] = true
			_, err := manager.ValidateSTPServerSweep(tx, script, facts, nil)
			require.ErrorContains(t, err, "invalid STP sweep asset state")
		})
	}
	t.Run("history-does-not-hide-loss-of-valid-transfer", func(t *testing.T) {
		manager, tx, script, facts, _ := stpSweepHistoricalBRC20Fixture(t, true)
		tx.TxOut = tx.TxOut[:1] // only the historical carrier prefix is returned
		_, err := manager.ValidateSTPServerSweep(tx, script, facts, nil)
		require.ErrorContains(t, err, "discarded an asset", "a historical flag must not excuse burning the new valid transfer")
	})
	t.Run("historical-marker-needs-matching-record", func(t *testing.T) {
		manager, tx, script, facts, name := stpSweepHistoricalBRC20Fixture(t, false)
		facts[tx.TxIn[len(tx.TxIn)-1].PreviousOutPoint.String()].Invalids[name] = true
		_, err := manager.ValidateSTPServerSweep(tx, script, facts, nil)
		require.ErrorContains(t, err, "invalid STP sweep asset state", "a marker without its historical asset record must not be silently accepted")
	})
}
