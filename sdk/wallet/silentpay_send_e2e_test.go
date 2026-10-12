package wallet

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	satsnetwire "github.com/sat20-labs/satoshinet/wire"
)

type spPlainBTCIndexer struct {
	IndexerRPCClient
	plain []*indexerwire.TxOutputInfo
}

func (c *spPlainBTCIndexer) GetUtxoListWithTicker(_ string, _ *satsnetwire.AssetName) []*indexerwire.TxOutputInfo {
	return c.plain
}

// This is the real Manager -> BTC-only coin selector -> BIP352 output
// construction -> existing PSBT signer, verified with Bitcoin's script engine.
// Only the public Indexer UTXO list is faked; no network broadcast takes place.
func TestSilentPayManagerBuildAndSignBitcoinTransactionE2E(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	mgr, sender := spE2EAccountManager(t)
	receiver := NewInternalWalletWithMnemonic(silentPayOtherMnemonic, "", &chaincfg.TestNet4Params)
	address, err := receiver.GetSilentPayAddress(0)
	if err != nil {
		t.Fatal(err)
	}
	payerAddress := sender.GetAddress()
	btcAddress, err := btcutil.DecodeAddress(payerAddress, &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	sourcePkScript, err := txscript.PayToAddrScript(btcAddress)
	if err != nil {
		t.Fatal(err)
	}
	op := chainhash.Hash{18, 19, 20}
	client := &spPlainBTCIndexer{plain: []*indexerwire.TxOutputInfo{{
		OutPoint: op.String() + ":2", Value: 200_000,
		PkScript: sourcePkScript, UtxoId: 1001,
	}}}
	mgr.l1IndexerClient.SetMaster(client)
	mgr.utxoLockerL1 = NewUtxoLocker(mgr.db, client, L1_NETWORK_BITCOIN)
	mgr.utxoLockerL1.Init()

	tx, fetcher, err := mgr.BuildSilentPayment(address, 100_000, 2)
	if err != nil {
		t.Fatalf("manager failed to build SP output: %v", err)
	}
	if len(tx.TxIn) != 1 || len(tx.TxOut) == 0 || tx.TxOut[0].Value != 100_000 {
		t.Fatalf("unexpected selected transaction: inputs=%d outputs=%d", len(tx.TxIn), len(tx.TxOut))
	}
	beforeHash := tx.TxHash()
	signed, err := mgr.SignTx(tx, fetcher)
	if err != nil {
		t.Fatalf("existing signer cannot sign SP sender transaction: %v", err)
	}
	if signed.TxHash() != beforeHash {
		t.Fatal("BIP352 transaction identity changed during signing")
	}
	sighashes := txscript.NewTxSigHashes(signed, fetcher)
	preout := fetcher.FetchPrevOutput(signed.TxIn[0].PreviousOutPoint)
	engine, err := txscript.NewEngine(preout.PkScript, signed, 0, txscript.StandardVerifyFlags,
		nil, sighashes, preout.Value, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Execute(); err != nil {
		t.Fatalf("ordinary input failed Bitcoin script verification: %v", err)
	}

	// Independently derive T from the finalized P2TR input output key.
	base := sender.getPaymentPrivKeyWithIndex(0)
	tap := txscript.TweakTaprootPrivKey(*base, nil)
	normalized := tap.Key
	if tap.PubKey().SerializeCompressed()[0] == 0x03 {
		normalized.Negate()
	}
	var smallest [36]byte
	copy(smallest[:32], signed.TxIn[0].PreviousOutPoint.Hash[:])
	binary.LittleEndian.PutUint32(smallest[32:], signed.TxIn[0].PreviousOutPoint.Index)
	a := btcec.PrivKeyFromScalar(&normalized).PubKey()
	inputHash, err := silentPayCheckedScalar(silentPayTaggedHash("BIP0352/Inputs", smallest[:], a.SerializeCompressed()))
	if err != nil {
		t.Fatal(err)
	}
	T, err := silentPaySharedSecretPoint(&inputHash, a)
	if err != nil {
		t.Fatal(err)
	}
	receiverKeys, err := receiver.DeriveSilentPayKeys(0)
	if err != nil {
		t.Fatal(err)
	}
	candidate := SilentPayCandidate{Vout: 0, OutputKey: append([]byte(nil), signed.TxOut[0].PkScript[2:]...)}
	matches, err := MatchSilentPayOutputs(receiverKeys, T.SerializeCompressed(), []SilentPayCandidate{candidate})
	if err != nil || len(matches) != 1 || matches[0].Vout != 0 {
		t.Fatalf("recipient cannot discover the exact signed payment: %v %+v", err, matches)
	}
	priv, err := SilentPayOutputSpendKey(receiverKeys, matches[0])
	if err != nil || !bytes.Equal(schnorr.SerializePubKey(priv.PubKey()), candidate.OutputKey) {
		t.Fatalf("recipient cannot derive spendable private key: %v", err)
	}
}
