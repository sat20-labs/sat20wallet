package wallet

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/rgb11/anchors"
	"github.com/sat20-labs/rgb11/invoicing"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"math/big"
	"testing"
)

func TestRejectRGB11STPAsset(t *testing.T) {
	if err := rejectRGB11STPAsset(&indexer.AssetName{Protocol: rgb11wallet.Protocol}); !errors.Is(err, ErrRGB11STPUnavailable) {
		t.Fatalf("expected RGB11 STP rejection, got %v", err)
	}
	if err := rejectRGB11STPAsset(&indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX}); err != nil {
		t.Fatalf("non-RGB11 asset unexpectedly rejected: %v", err)
	}
}

func TestRGB11AtomicAmountKeepsDisplayPrecision(t *testing.T) {
	amount := &indexer.Decimal{Precision: 8, Value: new(big.Int).SetUint64(123456789)}
	value, err := decimalUint64(amount)
	if err != nil || value != 123456789 {
		t.Fatalf("atomic amount=%d err=%v", value, err)
	}
}

type rgb11EvidenceRPCStub struct {
	*TestIndexerClient
	calls map[string]int
}

func (s *rgb11EvidenceRPCStub) mark(name string) { s.calls[name]++ }

func (s *rgb11EvidenceRPCStub) GetBitcoinUTXOStatus(outpoint string) (*indexerwire.BitcoinUTXOStatus, error) {
	s.mark("utxo")
	return &indexerwire.BitcoinUTXOStatus{
		Outpoint: outpoint, Exists: true, Unspent: true, Value: 900,
		PkScript: "51200000000000000000000000000000000000000000000000000000000000000000", Confirmations: 2,
	}, nil
}

func (s *rgb11EvidenceRPCStub) GetBitcoinRawTx(txid string) (*indexerwire.BitcoinRawTx, error) {
	s.mark("rawtx")
	return &indexerwire.BitcoinRawTx{TxID: txid, RawTx: "0001"}, nil
}

func (s *rgb11EvidenceRPCStub) GetBitcoinTxStatus(txid string) (*indexerwire.BitcoinTxStatus, error) {
	s.mark("txstatus")
	return &indexerwire.BitcoinTxStatus{TxID: txid, Exists: true, Confirmed: true, BlockHeight: 12, Confirmations: 3}, nil
}

func (s *rgb11EvidenceRPCStub) GetBitcoinOutspend(outpoint string) (*indexerwire.BitcoinOutspend, error) {
	s.mark("outspend")
	return &indexerwire.BitcoinOutspend{Outpoint: outpoint, Exists: true, Spent: true}, nil
}

func (s *rgb11EvidenceRPCStub) GetBitcoinTip() (*indexerwire.BitcoinTip, error) {
	s.mark("tip")
	return &indexerwire.BitcoinTip{Height: 20, BlockHash: "tip"}, nil
}

func (s *rgb11EvidenceRPCStub) BroadcastBitcoinTx(rawTx []byte) (string, error) {
	s.mark("broadcast")
	if !bytes.Equal(rawTx, []byte{2, 3}) {
		return "", ErrRGB11Inconsistent
	}
	return "broadcast-txid", nil
}

func TestRGB11EvidenceUsesBitcoinFactsV3Adapter(t *testing.T) {
	client := &rgb11EvidenceRPCStub{TestIndexerClient: &TestIndexerClient{}, calls: make(map[string]int)}
	provider := newIndexerBitcoinEvidenceProvider(client)
	outpoint := "0000000000000000000000000000000000000000000000000000000000000001:0"
	utxo, err := provider.GetUTXO(outpoint)
	if err != nil || utxo.Value != 900 || utxo.Confirmations != 2 || len(utxo.PkScript) != 34 {
		t.Fatalf("utxo=%#v err=%v", utxo, err)
	}
	raw, err := provider.GetRawTx("tx")
	if err != nil || !bytes.Equal(raw, []byte{0, 1}) {
		t.Fatalf("raw=%x err=%v", raw, err)
	}
	status, err := provider.GetTxStatus("tx")
	if err != nil || !status.Confirmed || status.BlockHeight != 12 {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	outspend, err := provider.GetOutspend(outpoint)
	if err != nil || !outspend.Spent || outspend.SpendingTx != "unknown" {
		t.Fatalf("outspend=%#v err=%v", outspend, err)
	}
	tip, err := provider.GetTip()
	if err != nil || tip.Height != 20 {
		t.Fatalf("tip=%#v err=%v", tip, err)
	}
	if txid, err := provider.Broadcast([]byte{2, 3}); err != nil || txid != "broadcast-txid" {
		t.Fatalf("broadcast txid=%q err=%v", txid, err)
	}
	for _, name := range []string{"utxo", "rawtx", "txstatus", "outspend", "tip", "broadcast"} {
		if client.calls[name] != 1 {
			t.Fatalf("%s calls=%d", name, client.calls[name])
		}
	}
}

func TestRGB11OfficialNetworkMappings(t *testing.T) {
	tests := []struct {
		name     string
		params   *chaincfg.Params
		invoice  invoicing.ChainNet
		issuance coreissuance.ChainNet
	}{
		{"mainnet", &chaincfg.MainNetParams, invoicing.BitcoinMainnet, coreissuance.BitcoinMainnet},
		{"testnet3", &chaincfg.TestNet3Params, invoicing.BitcoinTestnet3, coreissuance.BitcoinTestnet3},
		{"testnet4", &chaincfg.TestNet4Params, invoicing.BitcoinTestnet4, coreissuance.BitcoinTestnet4},
		{"regtest", &chaincfg.RegressionNetParams, invoicing.BitcoinRegtest, coreissuance.BitcoinRegtest},
		{"signet", &chaincfg.SigNetParams, invoicing.BitcoinSignet, coreissuance.BitcoinSignet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := rgb11InvoiceNetwork(test.params); got != test.invoice {
				t.Fatalf("invoice network=%s, want %s", got, test.invoice)
			}
			if got := rgb11IssuanceNetwork(test.params); got != test.issuance {
				t.Fatalf("issuance network=%d, want %d", got, test.issuance)
			}
		})
	}
}

func TestGetChainParamSupportsRegtest(t *testing.T) {
	previous := _chain
	_chain = "regtest"
	t.Cleanup(func() { _chain = previous })
	if GetChainParam().Net != chaincfg.RegressionNetParams.Net {
		t.Fatalf("regtest chain mapped to %s", GetChainParam().Name)
	}
}

func TestInternalWalletSignsTapretCarrierWithActiveSubaccountKey(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create test wallet")
	}
	const derivationIndex = uint32(7)
	wallet.SetSubAccount(derivationIndex)
	if wallet.GetAddress() != wallet.GetAddressByIndex(derivationIndex) ||
		!wallet.GetPubKey().IsEqual(wallet.GetPubKeyByIndex(derivationIndex)) {
		t.Fatal("active P2TR address and internal key do not share the derivation index")
	}
	root := sha256.Sum256([]byte("SAT20 RGB11 Tapret carrier root"))
	outputKey := txscript.ComputeTaprootOutputKey(wallet.GetPubKey(), root[:])
	carrierScript, err := txscript.PayToTaprootScript(outputKey)
	if err != nil {
		t.Fatal(err)
	}
	changeScript, err := AddrToPkScript(wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}

	previousHash := chainhash.Hash{1}
	previousOutpoint := wire.OutPoint{Hash: previousHash, Index: 0}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&previousOutpoint, nil, nil))
	tx.AddTxOut(wire.NewTxOut(9_000, changeScript))
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	prevFetcher.AddPrevOut(previousOutpoint, wire.NewTxOut(10_000, carrierScript))
	packet, err := CreatePsbt(tx, prevFetcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignPsbtWithTaprootMerkleRoots(packet, map[int][]byte{0: root[:]}); err != nil {
		t.Fatal(err)
	}
	if err := psbt.MaybeFinalizeAll(packet); err != nil {
		t.Fatal(err)
	}
	signed, err := psbt.Extract(packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignedTx(signed, prevFetcher); err != nil {
		t.Fatal(err)
	}

	wrongIndexPacket, err := CreatePsbt(tx, prevFetcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignPsbtWithTaprootMerkleRootsAtIndex(
		wrongIndexPacket, map[int][]byte{0: root[:]}, derivationIndex-1,
	); err == nil {
		t.Fatal("Tapret carrier signed with a different BIP86 derivation index")
	}

	wrongRoot := sha256.Sum256([]byte("wrong RGB11 Tapret root"))
	wrongPacket, err := CreatePsbt(tx, prevFetcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignPsbtWithTaprootMerkleRoots(wrongPacket, map[int][]byte{0: wrongRoot[:]}); err == nil {
		t.Fatal("Tapret carrier signed with the wrong merkle root")
	}
}

func TestRGB11CarrierBindingUsesActiveBIP86DerivationIndex(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create test wallet")
	}
	const derivationIndex = uint32(5)
	wallet.SetSubAccount(derivationIndex)
	root := sha256.Sum256([]byte("SAT20 RGB11 carrier binding root"))
	internal := wallet.GetPubKeyByIndex(derivationIndex)
	outputKey := txscript.ComputeTaprootOutputKey(internal, root[:])
	carrierScript, err := txscript.PayToTaprootScript(outputKey)
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{wallet: wallet}
	manager.rgbManager = &rgb11Manager{Manager: manager}
	const outpoint = "1111111111111111111111111111111111111111111111111111111111111111:0"
	binding, err := manager.rgbManager.rgb11CarrierBinding(rgb11wallet.ValidatedAllocation{
		OutPoint: outpoint, CommitmentMethod: "tapret1st",
		CarrierInternalKey: internal.SerializeCompressed()[1:], TapretRoot: root[:],
	}, &rgb11wallet.BitcoinUTXO{OutPoint: outpoint, PkScript: carrierScript})
	if err != nil {
		t.Fatal(err)
	}
	walletScript, err := AddrToPkScript(wallet.GetAddressByIndex(derivationIndex), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	if binding.DerivationIndex != derivationIndex ||
		binding.LogicalAddress != wallet.GetAddressByIndex(derivationIndex) ||
		!manager.rgbManager.ownsRGB11Carrier(binding, walletScript) {
		t.Fatalf("Tapret binding does not preserve BIP86 derivation identity: %+v", binding)
	}

	foreignWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	foreignInternal := foreignWallet.GetPubKey().SerializeCompressed()[1:]
	foreignOutput := txscript.ComputeTaprootOutputKey(foreignWallet.GetPubKey(), root[:])
	foreignScript, err := txscript.PayToTaprootScript(foreignOutput)
	if err != nil {
		t.Fatal(err)
	}
	foreignBinding, err := manager.rgbManager.rgb11CarrierBinding(rgb11wallet.ValidatedAllocation{
		OutPoint: outpoint, CommitmentMethod: "tapret1st",
		CarrierInternalKey: foreignInternal, TapretRoot: root[:],
	}, &rgb11wallet.BitcoinUTXO{OutPoint: outpoint, PkScript: foreignScript})
	if err != nil {
		t.Fatal(err)
	}
	if manager.rgbManager.ownsRGB11Carrier(foreignBinding, walletScript) {
		t.Fatal("foreign Tapret carrier was assigned to the active BIP86 derivation index")
	}
}

func TestRGB11WitnessBuilderSpendsTapretCarrier(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create test wallet")
	}
	const derivationIndex = uint32(9)
	wallet.SetSubAccount(derivationIndex)
	root := sha256.Sum256([]byte("SAT20 RGB11 imported Tapret root"))
	outputKey := txscript.ComputeTaprootOutputKey(wallet.GetPubKey(), root[:])
	carrierScript, err := txscript.PayToTaprootScript(outputKey)
	if err != nil {
		t.Fatal(err)
	}
	walletScript, err := AddrToPkScript(wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	const sourceOutpoint = "2222222222222222222222222222222222222222222222222222222222222222:0"
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	base := indexer.NewTxOutput(10_000)
	base.OutPointStr = sourceOutpoint
	base.OutValue.PkScript = carrierScript
	rpc.outputs[sourceOutpoint] = base
	evidence := &rgb11FlowEvidence{utxos: map[string]*rgb11wallet.BitcoinUTXO{
		sourceOutpoint: {OutPoint: sourceOutpoint, Value: 10_000, PkScript: carrierScript, Confirmations: 6},
	}, rawTx: make(map[string][]byte), spendingTx: make(map[string]string)}
	manager := newRGB11FlowManager(t, wallet, rpc, evidence, 1)
	internalKey := wallet.GetPubKey().SerializeCompressed()[1:]
	binding := &rgb11wallet.CarrierBinding{
		DerivationIndex: derivationIndex, LogicalAddress: wallet.GetAddressByIndex(derivationIndex),
		OutPoint: sourceOutpoint, ActualPkScript: carrierScript,
		ActualOutputKey: outputKey.SerializeCompressed()[1:], InternalPubKey: internalKey,
		TapretRoot: root[:], CommitmentMethod: "tapret1st",
	}
	if !manager.rgbManager.ownsRGB11Carrier(binding, walletScript) {
		t.Fatal("Tapret carrier is not bound to its ordinary BIP86 address derivation index")
	}
	wrongBinding := *binding
	wrongBinding.DerivationIndex--
	if manager.rgbManager.ownsRGB11Carrier(&wrongBinding, walletScript) {
		t.Fatal("Tapret carrier accepted with a mismatched derivation index")
	}
	selected := []rgb11SpendAllocation{{proof: &rgb11wallet.AllocationProof{
		OutPoint:       sourceOutpoint,
		CarrierBinding: binding,
	}}}
	mpcCommitment := sha256.Sum256([]byte("SAT20 RGB11 outgoing Opret commitment"))
	tx, prevFetcher, _, roots, _, err := manager.rgbManager.buildRGB11WitnessTx(
		selected, [][]byte{walletScript}, walletScript, anchors.OpretScript(mpcCommitment), 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || !bytes.Equal(roots[0], root[:]) {
		t.Fatalf("Tapret signing roots=%x", roots)
	}
	packet, err := CreatePsbt(tx, prevFetcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignPsbtWithTaprootMerkleRootsAtIndex(packet, roots, derivationIndex); err != nil {
		t.Fatal(err)
	}
	if err := psbt.MaybeFinalizeAll(packet); err != nil {
		t.Fatal(err)
	}
	signed, err := psbt.Extract(packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignedTx(signed, prevFetcher); err != nil {
		t.Fatal(err)
	}
}
