package wallet

import (
	"crypto/sha256"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestSatsNetPSBTPrevoutValidationBeforeSigning(t *testing.T) {
	wallet, _, err := NewInteralWallet(GetChainParam())
	require.NoError(t, err)
	key := wallet.getPaymentPrivKey()
	script, err := GetP2TRpkScript(key.PubKey())
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		change func(*spsbt.Packet)
	}{
		{"txid", func(p *spsbt.Packet) { p.UnsignedTx.TxIn[0].PreviousOutPoint.Hash[0] ^= 1 }},
		{"vout", func(p *spsbt.Packet) { p.UnsignedTx.TxIn[0].PreviousOutPoint.Index = 1 }},
		{"value", func(p *spsbt.Packet) { p.Inputs[0].WitnessUtxo.Value++ }},
		{"script", func(p *spsbt.Packet) { p.Inputs[0].WitnessUtxo.PkScript[2] ^= 1 }},
		{"protocol", func(p *spsbt.Packet) { p.Inputs[0].WitnessUtxo.Assets[0].Name.Protocol = "other" }},
		{"type", func(p *spsbt.Packet) { p.Inputs[0].WitnessUtxo.Assets[0].Name.Type = "nft" }},
		{"ticker", func(p *spsbt.Packet) { p.Inputs[0].WitnessUtxo.Assets[0].Name.Ticker = "other" }},
		{"amount", func(p *spsbt.Packet) { p.Inputs[0].WitnessUtxo.Assets[0].Amount = *indexer.NewDecimal(11, 0) }},
		{"binding", func(p *spsbt.Packet) { p.Inputs[0].WitnessUtxo.Assets[0].BindingSat++ }},
		{"order", func(p *spsbt.Packet) { a := p.Inputs[0].WitnessUtxo.Assets; a[0], a[1] = a[1], a[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := satsNetPrevoutPacket(script)
			tc.change(p)
			require.NotPanics(t, func() { require.Error(t, wallet.signPsbt_SatsNet(key, p)) })
			require.Empty(t, p.Inputs[0].TaprootKeySpendSig)
			require.Empty(t, p.Inputs[0].PartialSigs)
		})
	}
	for _, mode := range []string{"both", "witness", "nonwitness"} {
		t.Run(mode, func(t *testing.T) {
			p := satsNetPrevoutPacket(script)
			if mode == "witness" {
				p.Inputs[0].NonWitnessUtxo = nil
			}
			if mode == "nonwitness" {
				p.Inputs[0].WitnessUtxo = nil
			}
			require.NoError(t, wallet.signPsbt_SatsNet(key, p))
			require.NotEmpty(t, p.Inputs[0].TaprootKeySpendSig)
			p.UnsignedTx.TxIn[0].Witness = swire.TxWitness{p.Inputs[0].TaprootKeySpendSig}
			prev := satsNetPrevoutPacket(script).Inputs[0].NonWitnessUtxo.TxOut[0]
			fetcher := stxscript.NewCannedPrevOutputFetcher(prev.PkScript, prev.Value, prev.Assets)
			hashes := stxscript.NewTxSigHashes(p.UnsignedTx, fetcher)
			vm, err := stxscript.NewEngine(prev.PkScript, p.UnsignedTx, 0, stxscript.StandardVerifyFlags, nil, hashes, prev.Value, prev.Assets, fetcher)
			require.NoError(t, err)
			require.NoError(t, vm.Execute())
		})
	}
	t.Run("segwit-v0", func(t *testing.T) {
		witnessScript := append(append([]byte{stxscript.OP_DATA_33}, key.PubKey().SerializeCompressed()...), stxscript.OP_CHECKSIG)
		hash := sha256.Sum256(witnessScript)
		pkScript := append([]byte{stxscript.OP_0, stxscript.OP_DATA_32}, hash[:]...)
		p := satsNetPrevoutPacket(pkScript)
		p.Inputs[0].WitnessScript = witnessScript
		p.Inputs[0].SighashType = stxscript.SigHashAll
		require.NoError(t, wallet.signPsbt_SatsNet(key, p))
		require.Len(t, p.Inputs[0].PartialSigs, 1)
		p.UnsignedTx.TxIn[0].Witness = swire.TxWitness{p.Inputs[0].PartialSigs[0].Signature, witnessScript}
		prev := p.Inputs[0].NonWitnessUtxo.TxOut[0]
		fetcher := stxscript.NewCannedPrevOutputFetcher(prev.PkScript, prev.Value, prev.Assets)
		vm, err := stxscript.NewEngine(prev.PkScript, p.UnsignedTx, 0, stxscript.StandardVerifyFlags, nil, stxscript.NewTxSigHashes(p.UnsignedTx, fetcher), prev.Value, prev.Assets, fetcher)
		require.NoError(t, err)
		require.NoError(t, vm.Execute())
	})
}

func satsNetPrevoutPacket(script []byte) *spsbt.Packet {
	prev := swire.NewMsgTx(2)
	prev.AddTxIn(&swire.TxIn{})
	prev.AddTxOut(swire.NewTxOut(1000, swire.TxAssets{
		{Name: swire.AssetName{Protocol: "ordx", Type: "ft", Ticker: "a"}, Amount: *indexer.NewDecimal(10, 0), BindingSat: 1},
		{Name: swire.AssetName{Protocol: "ordx", Type: "ft", Ticker: "b"}, Amount: *indexer.NewDecimal(20, 0)},
	}, append([]byte(nil), script...)))
	tx := swire.NewMsgTx(2)
	tx.AddTxIn(&swire.TxIn{PreviousOutPoint: swire.OutPoint{Hash: prev.TxHash()}, Sequence: swire.MaxTxInSequenceNum})
	tx.AddTxOut(prev.Copy().TxOut[0])
	return &spsbt.Packet{UnsignedTx: tx, Inputs: []spsbt.PInput{{NonWitnessUtxo: prev, WitnessUtxo: prev.Copy().TxOut[0]}}, Outputs: []spsbt.POutput{{}}}
}
