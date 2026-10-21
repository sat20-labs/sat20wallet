package e2e

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Only the AUTOPAY signature acceptance case uses these actors. The shared
// fixture still bootstraps its existing network and asset sources unchanged.
func newAutopaySignedFixture(t *testing.T, feeAsset string) (*templateFixture, *templateGasBank, []wire.OutPoint) {
	t.Helper()
	f := newTemplateFixture(t, map[string]int64{feeAsset: 1000})
	legacyA := f.A
	signed := func(actor *templateActor) *templateActor {
		address, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(txscript.ComputeTaprootKeyNoScript(actor.Key.PubKey())), &chaincfg.TestNetParams)
		require.NoError(t, err)
		script, err := txscript.PayToAddrScript(address)
		require.NoError(t, err)
		return &templateActor{Key: actor.Key, Address: address.EncodeAddress(), PkScript: script}
	}
	f.A, f.B, f.C = signed(f.A), signed(f.B), signed(f.C)
	// Existing bootstrap coins are transferred once into genuine key-path
	// outputs. All subsequent AUTOPAY deploy/config/close inputs require signing.
	gasOuts := f.split(t, legacyA, f.gasAnchor, contractcommon.GetGasAssetName(),
		[]int64{300000, 300000, 300000, 300000}, []int64{10000, 10000, 10000, 10000},
		[]*templateActor{f.A, f.A, f.A, f.C})
	bank := &templateGasBank{outs: map[*templateActor][]wire.OutPoint{f.A: gasOuts[:3], f.C: gasOuts[3:]}}
	feeOuts := f.split(t, legacyA, f.assetAnchors[feeAsset], feeAsset,
		[]int64{30, 15}, []int64{10000, 10000}, []*templateActor{f.A, f.C})
	return f, bank, feeOuts
}

// Mirror the signed SatoshiNet scenario helper: fetch actual predecessor
// quantities, sign every input, and execute standard script validation.
func signAutopayKeyInputs(t *testing.T, f *templateFixture, tx *wire.MsgTx, actor *templateActor) {
	t.Helper()
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	for _, input := range tx.TxIn {
		previous, err := f.Network.Bootstrap.Client.GetRawTransaction(&input.PreviousOutPoint.Hash)
		require.NoError(t, err)
		output := previous.MsgTx().TxOut[input.PreviousOutPoint.Index]
		require.Equal(t, actor.PkScript, output.PkScript, "input must belong to the BIP86 signer")
		fetcher.AddPrevOut(input.PreviousOutPoint, output)
	}
	hashes := txscript.NewTxSigHashes(tx, fetcher)
	for n, input := range tx.TxIn {
		previous := fetcher.FetchPrevOutput(input.PreviousOutPoint)
		input.SignatureScript = nil
		var err error
		input.Witness, err = txscript.TaprootWitnessSignature(tx, hashes, n, previous.Value, previous.Assets,
			previous.PkScript, txscript.SigHashDefault, actor.Key)
		require.NoError(t, err)
		engine, err := txscript.NewEngine(previous.PkScript, tx, n, txscript.StandardVerifyFlags,
			nil, hashes, previous.Value, previous.Assets, fetcher)
		require.NoError(t, err)
		require.NoError(t, engine.Execute(), "AUTOPAY input %d must have a genuine valid signature", n)
	}
}

func requireAutopayTamperedSignatureRejected(t *testing.T, f *templateFixture, tx *wire.MsgTx) {
	t.Helper()
	bad := tx.Copy()
	require.Len(t, bad.TxIn[0].Witness, 1, "key-path spend contains a Schnorr signature")
	require.Len(t, bad.TxIn[0].Witness[0], schnorr.SignatureSize)
	bad.TxIn[0].Witness[0] = append([]byte(nil), bad.TxIn[0].Witness[0]...)
	bad.TxIn[0].Witness[0][0] ^= 1
	_, err := f.Network.Bootstrap.Client.SendRawTransaction(bad, false)
	require.ErrorContains(t, err, "failed to validate input", "node must reject the modified AUTOPAY signature during script validation")
	t.Logf("tampered AUTOPAY signature rejected by node: %v", err)
	for _, input := range tx.TxIn {
		unspent, err := f.Network.Bootstrap.Client.GetTxOut(&input.PreviousOutPoint.Hash, input.PreviousOutPoint.Index, true)
		require.NoError(t, err)
		require.NotNil(t, unspent, "rejected signature must leave each input unspent")
	}
}
