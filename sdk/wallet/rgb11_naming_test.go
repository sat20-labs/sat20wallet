package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type namingObservationEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	reads int
}

func (e *namingObservationEvidence) GetRawTx(string) ([]byte, error) {
	e.reads++
	return nil, errors.New("UI observation must not read the network")
}

func (e *namingObservationEvidence) GetUTXO(string) (*rgb11wallet.BitcoinUTXO, error) {
	e.reads++
	return nil, errors.New("UI observation must not read the network")
}

func TestRGB11NamingIssueRenameAndReceiverOrigin(t *testing.T) {
	previous := _chain
	_chain = "testnet4"
	t.Cleanup(func() { _chain = previous })
	// Public deterministic test mnemonic, never used with a live wallet.
	issuerWallet := NewInternalWalletWithMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", &chaincfg.TestNet4Params)
	if issuerWallet == nil {
		t.Fatal("create fixture wallet")
	}
	address := issuerWallet.GetAddress()
	script, err := AddrToPkScript(address, &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 0xffffffff}, []byte{1, 1}, nil))
	tx.AddTxOut(wire.NewTxOut(10000, script))
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	origin := (&wire.OutPoint{Hash: tx.TxHash(), Index: 0}).String()
	evidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{origin: {OutPoint: origin, Value: 10000, PkScript: script, Confirmations: 6}},
		rawTx: map[string][]byte{tx.TxHash().String(): raw.Bytes()}, spendingTx: make(map[string]string),
	}
	output := indexer.NewTxOutput(10000)
	output.OutPointStr, output.OutValue.PkScript = origin, script
	rpc := &rgb11FlowIndexer{
		outputs: map[string]*TxOutput{origin: output},
		plain:   []*indexerwire.TxOutputInfo{{OutPoint: origin, Value: 10000, PkScript: script}},
	}
	manager := newRGB11FlowManager(t, issuerWallet, rpc, evidence, 41)
	issued, err := manager.rgbManager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "NAMING", Name: "Naming test", Precision: 0, Amounts: []uint64{1000}, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation := &namingObservationEvidence{BitcoinEvidenceProvider: evidence}
	manager.rgbManager.evidence = observation
	before, err := manager.GetRGB11State()
	if err != nil || len(before.TickerInfos) != 1 {
		t.Fatalf("state unavailable: %+v %v", before, err)
	}
	info := before.TickerInfos[0]
	want := "naming@" + address[len(address)-12:]
	if info.Ticker != want || info.AssetKey != issued.AssetName.String() || info.ContractID != issued.ContractID ||
		info.GenesisAddress != address || info.CanonicalName != "" || info.Verified {
		t.Fatalf("wrong local naming view: %+v want=%s", info, want)
	}
	beforeAssets, err := json.Marshal(before.Assets)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetRGB11LocalAssetName(issued.ContractID, "my local RGB asset"); err != nil {
		t.Fatal(err)
	}
	after, err := manager.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	afterAssets, err := json.Marshal(after.Assets)
	if err != nil {
		t.Fatal(err)
	}
	if after.TickerInfos[0].Ticker != "my local RGB asset" || after.TickerInfos[0].AssetKey != info.AssetKey ||
		after.TickerInfos[0].ContractID != info.ContractID || !bytes.Equal(beforeAssets, afterAssets) || observation.reads != 0 {
		t.Fatalf("local rename affected identity/balance or read network: %+v reads=%d", after.TickerInfos[0], observation.reads)
	}
	if err := manager.SetRGB11LocalAssetName("not-a-contract", "bad"); err == nil {
		t.Fatal("invalid identity accepted by public rename API")
	}

	// A different wallet importing the same contract must not substitute its
	// own address (and must not inherit the first wallet's local custom label).
	recipientWallet := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	if recipientWallet == nil {
		t.Fatal("create recipient fixture")
	}
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 42)
	if _, err := recipient.rgbManager.ImportRGB11Contract(context.Background(), []byte(issued.Armor)); err != nil {
		t.Fatal(err)
	}
	received, err := recipient.GetRGB11State()
	if err != nil || len(received.TickerInfos) != 1 || received.TickerInfos[0].Ticker != want || received.TickerInfos[0].GenesisAddress != address {
		t.Fatalf("receiver changed provider origin or inherited local alias: %+v %v", received, err)
	}

	// Canonical names require the future authenticated registry; accepting
	// arbitrary imported metadata here would let a caller impersonate a DID.
	forged := *info.TickerInfo
	var ext rgb11wallet.TickerExt
	if err := json.Unmarshal(forged.Content, &ext); err != nil {
		t.Fatal(err)
	}
	ext.CanonicalName = "rgb11:f:naming@alice"
	forged.Content, err = json.Marshal(ext)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.rgbManager.RegisterRGB11TickerInfo(&forged); !errors.Is(err, rgb11wallet.ErrRGB11STPUnavailable) {
		t.Fatalf("untrusted canonical name accepted: %v", err)
	}
}
