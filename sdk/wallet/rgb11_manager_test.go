package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	"github.com/sat20-labs/rgb11/seals"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"math/big"
	"testing"
	"time"
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
	if err != nil || !outspend.Spent || outspend.SpendingTx != "" {
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

func TestRGB11WitnessInvoicesUseCurrentAccountKey(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create test wallet")
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	evidence := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO),
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	manager := newRGB11FlowManager(t, wallet, rpc, evidence, 70)
	first, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", AmountRaw: "1", WitnessVout: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", AmountRaw: "2", WitnessVout: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.RequestID == second.RequestID {
		t.Fatal("consecutive RGB11 witness invoices reused one request id")
	}
	activeScript, err := AddrToPkScript(wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []*corewallet.ReceiveRequest{first, second} {
		if !bytes.Equal(request.WitnessScript, activeScript) {
			t.Fatalf("RGB11 witness invoice script=%x expected=%x", request.WitnessScript, activeScript)
		}
		invoice, err := invoicing.Parse(request.Invoice)
		if err != nil {
			t.Fatal(err)
		}
		invoiceScript, err := invoice.Beneficiary.WitnessScript()
		if err != nil || !bytes.Equal(invoiceScript, activeScript) {
			t.Fatalf("RGB11 invoice beneficiary script=%x expected=%x err=%v", invoiceScript, activeScript, err)
		}
	}
	if _, err := manager.rgbManager.projectionStore.LoadReceiveKey(activeScript); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("fixed-address witness invoice persisted receive-key state: %v", err)
	}
	projection, err := manager.rgbManager.projectionStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := manager.rgbManager.engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	restoredWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	restored := newRGB11FlowManager(t, restoredWallet, rpc, evidence, 70)
	if err := restored.rgbManager.projectionStore.ImportSnapshot(projection); err != nil {
		t.Fatal(err)
	}
	if err := restored.rgbManager.engineStore.ImportSnapshot(engine); err != nil {
		t.Fatal(err)
	}
	for _, request := range []*corewallet.ReceiveRequest{first, second} {
		stored, err := restored.rgbManager.engine.LoadReceive(request.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stored.WitnessScript, activeScript) {
			t.Fatalf("restored witness script=%x expected=%x", stored.WitnessScript, activeScript)
		}
	}
}

func TestRGB11StandardWitnessInvoicesUseIndependentReceiveKeys(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create test wallet")
	}
	manager := newRGB11FlowManager(t, wallet, &rgb11FlowIndexer{
		outputs: make(map[string]*TxOutput),
	}, &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte),
		spendingTx: make(map[string]string),
	}, 72)
	requests := make([]*corewallet.ReceiveRequest, 0, 2)
	for _, amount := range []string{"1", "2"} {
		request, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: RGB11ProxyTransport,
			TransportEndpoints: []string{testRGB11ProxyTransport},
			AmountRaw:          amount, WitnessVout: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
	}
	if bytes.Equal(requests[0].WitnessScript, requests[1].WitnessScript) {
		t.Fatal("standard witness invoices reused one receive key")
	}
	activeScript, err := AddrToPkScript(wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range requests {
		if bytes.Equal(request.WitnessScript, activeScript) {
			t.Fatal("standard witness invoice reused the active account key")
		}
		key, err := manager.rgbManager.projectionStore.LoadReceiveKey(request.WitnessScript)
		if err != nil {
			t.Fatal(err)
		}
		if key.Change != 1 || key.RequestID != request.RequestID {
			t.Fatalf("unexpected receive key: %+v", key)
		}
	}
}

func TestRGB11StandardProxyInvoiceUsesBlindedBeneficiary(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create test wallet")
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	evidence := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO),
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	script, err := AddrToPkScript(wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	const receiveOutpoint = "7171717171717171717171717171717171717171717171717171717171717171:0"
	output := indexer.NewTxOutput(100_000)
	output.OutPointStr = receiveOutpoint
	output.OutValue.PkScript = append([]byte(nil), script...)
	rpc.outputs[receiveOutpoint] = output
	rpc.plain = []*indexerwire.TxOutputInfo{{
		OutPoint: receiveOutpoint, Value: 100_000, PkScript: append([]byte(nil), script...),
	}}
	evidence.utxos[receiveOutpoint] = &rgb11wallet.BitcoinUTXO{
		OutPoint: receiveOutpoint, Value: 100_000,
		PkScript: append([]byte(nil), script...), Confirmations: 6,
	}
	manager := newRGB11FlowManager(t, wallet, rpc, evidence, 71)
	request, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "blind", TransportMode: RGB11ProxyTransport,
		TransportEndpoints: []string{testRGB11ProxyTransport}, AmountRaw: "2", WitnessVout: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := invoicing.Parse(request.Invoice)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Beneficiary.Kind != invoicing.BeneficiaryBlindedSeal {
		t.Fatalf("beneficiary kind = %v", parsed.Beneficiary.Kind)
	}
	if len(parsed.Transports) != 1 || parsed.Transports[0].String() != testRGB11ProxyTransport {
		t.Fatalf("unexpected transports: %+v", parsed.Transports)
	}
	if len(parsed.UnknownQuery) != 0 {
		t.Fatalf("standard invoice leaked SAT20 query parameters: %+v", parsed.UnknownQuery)
	}
	if request.Seal.TxID == nil || request.Seal.Vout != 0 {
		t.Fatalf("standard blind invoice did not reserve an absolute seal: %+v", request.Seal)
	}
	reservations, err := manager.rgbManager.projectionStore.ListReceiveReservations()
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 1 || reservations[0].RequestID != request.RequestID ||
		reservations[0].OutPoint != receiveOutpoint {
		t.Fatalf("unexpected receive reservations: %+v", reservations)
	}
	if lock := manager.utxoLockerL1.GetLockedUtxoList()[receiveOutpoint]; lock == nil ||
		lock.Reason != rgb11wallet.LockReasonPending {
		t.Fatalf("standard blind receive UTXO was not reserved: %+v", lock)
	}
	reservations[0].Expiry = time.Now().Add(-time.Second).Unix()
	if err := manager.rgbManager.projectionStore.SaveReceiveReservation(reservations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lock := manager.utxoLockerL1.GetLockedUtxoList()[receiveOutpoint]; lock != nil {
		t.Fatalf("expired standard blind receive UTXO remains reserved: %+v", lock)
	}
	if _, err := manager.rgbManager.projectionStore.LoadReceiveReservation(request.RequestID); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("expired receive reservation remains stored: %v", err)
	}
}

func TestRGB11PlainSelectionExcludesPendingChangeOutput(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create test wallet")
	}
	script, err := AddrToPkScript(wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{9}, Index: 1}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(10_000, script))
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	pendingOutpoint := fmt.Sprintf("%s:0", tx.TxHash())
	freeOutpoint := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff:0"
	rpc := &rgb11FlowIndexer{outputs: map[string]*TxOutput{}, plain: []*indexerwire.TxOutputInfo{
		{OutPoint: pendingOutpoint, Value: 10_000, PkScript: append([]byte(nil), script...)},
		{OutPoint: freeOutpoint, Value: 20_000, PkScript: append([]byte(nil), script...)},
	}}
	evidence := &rgb11FlowEvidence{utxos: map[string]*rgb11wallet.BitcoinUTXO{
		pendingOutpoint: {OutPoint: pendingOutpoint, Value: 10_000, PkScript: append([]byte(nil), script...), Confirmations: 2},
		freeOutpoint:    {OutPoint: freeOutpoint, Value: 20_000, PkScript: append([]byte(nil), script...), Confirmations: 2},
	}, rawTx: make(map[string][]byte), spendingTx: make(map[string]string)}
	manager := newRGB11FlowManager(t, wallet, rpc, evidence, 72)
	recipient := []byte("recipient")
	recipientHash := sha256.Sum256(recipient)
	pending := &rgb11wallet.PendingTransfer{
		State: rgb11wallet.TransferState{
			TransferID: "pending-change", Direction: "send", Status: "broadcast",
			WitnessTxID: tx.TxHash().String(), InputOutPoints: []string{tx.TxIn[0].PreviousOutPoint.String()},
			OutputOutPoints: []string{pendingOutpoint}, ConsignmentHash: hex.EncodeToString(recipientHash[:]),
		},
		RecipientConsignment: recipient, LocalConsignment: []byte("local"), SignedTx: raw.Bytes(),
		SignedPSBT: []byte{1}, ChangeSeals: []seals.GraphBlindSeal{{Vout: 0, Blinding: 1}},
	}
	if err := manager.rgbManager.projectionStore.SavePendingTransfer(pending); err != nil {
		t.Fatal(err)
	}
	selected, err := manager.rgbManager.selectRGB11IssueOutpoints(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != freeOutpoint {
		t.Fatalf("selected pending RGB11 change output: %v", selected)
	}
	if err := manager.rgbManager.rebuildRGB11Locks(); err != nil {
		t.Fatal(err)
	}
	lock := manager.utxoLockerL1.GetLockedUtxoList()[pendingOutpoint]
	if lock == nil || lock.Reason != rgb11wallet.LockReasonPending {
		t.Fatalf("pending RGB11 change output was not restored: %+v", lock)
	}
}

func TestRGB11SignsInternalCarrierAndActiveFeeInput(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create test wallet")
	}
	wallet.SetSubAccount(3)
	const receiveIndex = uint32(27)
	internalAddress := wallet.GetAddressByPath(1, receiveIndex)
	internalScript, err := AddrToPkScript(internalAddress, &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	activeScript, err := AddrToPkScript(wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	const sourceOutpoint = "3333333333333333333333333333333333333333333333333333333333333333:0"
	const feeOutpoint = "4444444444444444444444444444444444444444444444444444444444444444:0"
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput), plain: []*indexerwire.TxOutputInfo{
		{OutPoint: feeOutpoint, Value: 10_000, PkScript: activeScript},
	}}
	for _, item := range []struct {
		outpoint string
		value    int64
		script   []byte
	}{
		{sourceOutpoint, rgb11CarrierValue, internalScript},
		{feeOutpoint, 10_000, activeScript},
	} {
		output := indexer.NewTxOutput(item.value)
		output.OutPointStr = item.outpoint
		output.OutValue.PkScript = append([]byte(nil), item.script...)
		rpc.outputs[item.outpoint] = output
	}
	evidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {OutPoint: sourceOutpoint, Value: rgb11CarrierValue, PkScript: internalScript, Confirmations: 6},
			feeOutpoint:    {OutPoint: feeOutpoint, Value: 10_000, PkScript: activeScript, Confirmations: 6},
		},
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	manager := newRGB11FlowManager(t, wallet, rpc, evidence, 71)
	internalPubKey := wallet.GetPubKeyByPath(1, receiveIndex).SerializeCompressed()
	binding := &rgb11wallet.CarrierBinding{
		DerivationIndex: rgb11InternalReceiveIndexFlag | receiveIndex,
		LogicalAddress:  wallet.GetAddress(), OutPoint: sourceOutpoint,
		ActualPkScript: internalScript, ActualOutputKey: append([]byte(nil), internalScript[2:]...),
		InternalPubKey: append([]byte(nil), internalPubKey[1:]...), CommitmentMethod: "opret1st",
	}
	if !manager.rgbManager.ownsRGB11Carrier(binding, activeScript) {
		t.Fatal("independent RGB11 receive carrier is not owned by its logical account")
	}
	selected := []rgb11SpendAllocation{{proof: &rgb11wallet.AllocationProof{
		OutPoint: sourceOutpoint, CarrierBinding: binding,
	}}}
	mpcCommitment := sha256.Sum256([]byte("SAT20 RGB11 mixed-key transaction"))
	tx, prevFetcher, _, signingKeys, _, err := manager.rgbManager.buildRGB11WitnessTx(
		selected, [][]byte{activeScript}, activeScript, anchors.OpretScript(mpcCommitment), 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.TxIn) != 2 || len(signingKeys) != 1 ||
		signingKeys[0].Change != 1 || signingKeys[0].Index != receiveIndex {
		t.Fatalf("unexpected mixed-key transaction inputs=%d keys=%+v", len(tx.TxIn), signingKeys)
	}
	packet, err := CreatePsbt(tx, prevFetcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignRGB11Psbt(packet, signingKeys); err != nil {
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
	if len(roots) != 1 || roots[0].Change != 0 || roots[0].Index != derivationIndex ||
		!bytes.Equal(roots[0].TaprootMerkleRoot, root[:]) {
		t.Fatalf("Tapret signing roots=%x", roots)
	}
	packet, err := CreatePsbt(tx, prevFetcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.SignRGB11Psbt(packet, roots); err != nil {
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
