package wallet

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
