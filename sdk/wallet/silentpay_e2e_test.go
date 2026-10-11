package wallet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

const silentPayE2EMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
const silentPayOtherMnemonic = "legal winner thank year wave sausage worth useful legal winner thank yellow"

func TestSilentPaySubaccountsDeriveIndependentlyAndRestore(t *testing.T) {
	root := NewInternalWalletWithMnemonic(silentPayE2EMnemonic, "", &chaincfg.MainNetParams)
	if root == nil {
		t.Fatal("mnemonic wallet unavailable")
	}
	one, err := root.GetSilentPayAddress(0)
	if err != nil { t.Fatal(err) }
	two, err := root.GetSilentPayAddress(1)
	if err != nil { t.Fatal(err) }
	if one == two || len(one) != 116 {
		t.Fatalf("subaccount separation failed: %s %s", one, two)
	}
	first, err := DecodeSilentPayAddress(one, &chaincfg.MainNetParams)
	if err != nil { t.Fatal(err) }
	second, err := DecodeSilentPayAddress(two, &chaincfg.MainNetParams)
	if err != nil { t.Fatal(err) }
	if bytes.Equal(first.Scan.SerializeCompressed(), second.Scan.SerializeCompressed()) ||
		bytes.Equal(first.Spend.SerializeCompressed(), second.Spend.SerializeCompressed()) {
		t.Fatal("different accounts share a BIP352 scan/spend key")
	}
	restored := NewInternalWalletWithMnemonic(silentPayE2EMnemonic, "", &chaincfg.MainNetParams)
	restored.SetSubAccount(8)
	for index, want := range []string{one, two} {
		got, err := restored.GetSilentPayAddress(uint32(index))
		if err != nil || got != want {
			t.Fatalf("selected subaccount changed deterministic SP address: %s %v", got, err)
		}
	}
	other := NewInternalWalletWithMnemonic(silentPayOtherMnemonic, "", &chaincfg.MainNetParams)
	addressOther, err := other.GetSilentPayAddress(0)
	if err != nil || addressOther == one { t.Fatal("mnemonic separation failed", err) }
	testWallet := NewInternalWalletWithMnemonic(silentPayE2EMnemonic, "", &chaincfg.TestNet4Params)
	testAddress, err := testWallet.GetSilentPayAddress(0)
	if err != nil || len(testAddress) != 117 || testAddress[:4] != "tsp1" {
		t.Fatal("testnet SP address or coin type incorrect", err, testAddress)
	}
	if _, err := DecodeSilentPayAddress(testAddress, &chaincfg.MainNetParams); err == nil {
		t.Fatal("mainnet accepted testnet SP address")
	}
	if _, err := DecodeSilentPayAddress(one[:len(one)-1]+"q", &chaincfg.MainNetParams); err == nil {
		// Some valid addresses end in q, so make a guaranteed non-charset
		// tamper as the next check if this candidate happens to be valid.
		if _, err = DecodeSilentPayAddress(one[:len(one)-1]+"!", &chaincfg.MainNetParams); err == nil {
			t.Fatal("accepted an invalid Bech32m checksum")
		}
	}
}

func TestSilentPaySignedBitcoinSendReceiveAndSpendE2E(t *testing.T) {
	receiver := NewInternalWalletWithMnemonic(silentPayE2EMnemonic, "", &chaincfg.RegressionNetParams)
	keys, err := receiver.DeriveSilentPayKeys(1)
	if err != nil { t.Fatal(err) }
	address, err := receiver.GetSilentPayAddress(1)
	if err != nil { t.Fatal(err) }
	target, err := DecodeSilentPayAddress(address, &chaincfg.RegressionNetParams)
	if err != nil { t.Fatal(err) }

	payerRaw := make([]byte, 32)
	payerRaw[31] = 19
	payer, _ := btcec.PrivKeyFromBytes(payerRaw)
	// Direct eligible P2TR input: use the x-only output key even-Y form.
	if payer.PubKey().SerializeCompressed()[0] == 3 {
		negated := payer.Key
		negated.Negate()
		payer = btcec.PrivKeyFromScalar(&negated)
	}
	outpoint := wire.OutPoint{Hash: chainhash.Hash{1, 2, 3}, Index: 7}
	inputs := []SilentPaySenderInput{{Outpoint: outpoint, Key: payer}}
	script, err := SilentPayDestinationScript(target, inputs, 0)
	if err != nil { t.Fatal(err) }
	if len(script) != 34 || script[0] != txscript.OP_1 || script[1] != 0x20 {
		t.Fatalf("invalid P2TR script %x", script)
	}

	// Produce the public Indexer T=(input_hash*A) independently of the sender
	// function's ECDH, using the outpoint and eligible input public key.
	var serialized [36]byte
	copy(serialized[:32], outpoint.Hash[:])
	binary.LittleEndian.PutUint32(serialized[32:], outpoint.Index)
	hash, err := silentPayCheckedScalar(silentPayTaggedHash("BIP0352/Inputs", serialized[:], payer.PubKey().SerializeCompressed()))
	if err != nil { t.Fatal(err) }
	T, err := silentPaySharedSecretPoint(&hash, payer.PubKey())
	if err != nil { t.Fatal(err) }
	outputs := []SilentPayCandidate{
		{Vout: 2, OutputKey: append([]byte(nil), script[2:]...)},
		{Vout: 3, OutputKey: make([]byte, 32)},
	}
	matches, err := MatchSilentPayOutputs(keys, T.SerializeCompressed(), outputs)
	if err != nil { t.Fatal(err) }
	if len(matches) != 1 || matches[0].Vout != 2 || matches[0].K != 0 {
		t.Fatalf("receiver missed expected BTC output: %+v", matches)
	}
	wrongKeys, err := receiver.DeriveSilentPayKeys(0)
	if err != nil { t.Fatal(err) }
	wrong, err := MatchSilentPayOutputs(wrongKeys, T.SerializeCompressed(), outputs)
	if err != nil || len(wrong) != 0 { t.Fatal("wrong subaccount recognized payment", err) }

	spendKey, err := SilentPayOutputSpendKey(keys, matches[0])
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(schnorr.SerializePubKey(spendKey.PubKey()), script[2:]) {
		t.Fatal("BIP352 spend key does not correspond to received UTXO")
	}
	// Real Taproot script-engine verification, not merely matching keys.
	prev := wire.NewTxOut(100000, script)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{5}, Index: 2}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(99000, []byte{txscript.OP_TRUE}))
	fetcher := txscript.NewCannedPrevOutputFetcher(prev.PkScript, prev.Value)
	sigHashes := txscript.NewTxSigHashes(tx, fetcher)
	digest, err := txscript.CalcTaprootSignatureHash(sigHashes, txscript.SigHashDefault, tx, 0, fetcher)
	if err != nil { t.Fatal(err) }
	sig, err := schnorr.Sign(spendKey, digest)
	if err != nil { t.Fatal(err) }
	tx.TxIn[0].Witness = wire.TxWitness{sig.Serialize()}
	engine, err := txscript.NewEngine(prev.PkScript, tx, 0, txscript.StandardVerifyFlags, nil, sigHashes, prev.Value, fetcher)
	if err != nil { t.Fatal(err) }
	if err := engine.Execute(); err != nil { t.Fatalf("SP UTXO failed real Bitcoin spend: %v", err) }

	changedInputs := []SilentPaySenderInput{{Outpoint: wire.OutPoint{Hash: chainhash.Hash{3, 4, 5}, Index: 9}, Key: payer}}
	changed, err := SilentPayDestinationScript(target, changedInputs, 0)
	if err != nil || bytes.Equal(script, changed) {
		t.Fatal("RBF-style changed inputs reused original SP output", err)
	}
	other, err := SilentPayDestinationScript(target, inputs, 1)
	if err != nil || bytes.Equal(script, other) { t.Fatal("k increment did not alter output", err) }
	badT := append([]byte(nil), T.SerializeCompressed()...)
	badT[0] = 0x07
	if _, err := MatchSilentPayOutputs(keys, badT, outputs); err == nil {
		t.Fatal("malformed public Indexer T was trusted")
	}
}

func TestSilentPayNonMnemonicWalletRejected(t *testing.T) {
	key := make([]byte, 32)
	key[31] = 11
	wallet, _, err := NewInternalWalletWithPrivKey(key, &chaincfg.MainNetParams)
	if err != nil { t.Fatal(err) }
	if _, err := wallet.GetSilentPayAddress(0); !errors.Is(err, ErrSilentPayUnsupported) {
		t.Fatalf("private key wallet unexpectedly derived BIP352 keys: %v", err)
	}
}
