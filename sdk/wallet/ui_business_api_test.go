package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type managerUINameIndexer struct {
	IndexerRPCClient
	info  *indexerwire.OrdinalsName
	err   error
	calls []string
}

func (f *managerUINameIndexer) GetNameInfo(name string) (*indexerwire.OrdinalsName, error) {
	f.calls = append(f.calls, name)
	return f.info, f.err
}

// The UI's DID preflight and operation history are SDK contracts. This test
// calls the public Manager API directly with an in-memory wallet and indexer;
// it does not launch WASM, a browser, or a node.
func TestDIDPreflightAndHistory(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })

	manager := newAccountManagementAutoTestManager(t)
	walletID, err := manager.ImportWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"test-password",
	)
	if err != nil {
		t.Fatal(err)
	}
	if manager.GetCurrentWalletId() != walletID {
		t.Fatalf("current wallet=%d, imported wallet=%d", manager.GetCurrentWalletId(), walletID)
	}

	lookup := &managerUINameIndexer{}
	manager.l1IndexerClient = NewIndexerRPCClientMgr()
	manager.l1IndexerClient.Set(lookup)

	status, err := manager.CheckDIDName("not a valid name")
	if err != nil || status != "invalid" || len(lookup.calls) != 0 {
		t.Fatalf("invalid DID preflight status=%q err=%v lookups=%v", status, err, lookup.calls)
	}

	name := "8888.btc"
	lookup.err = fmt.Errorf("can't find name %s", name)
	status, err = manager.CheckDIDName("  8888.BTC ")
	if err != nil || status != "available" {
		t.Fatalf("explicit NotFound status=%q err=%v", status, err)
	}

	finished, err := manager.BeginOperationLog(OperationLogCreate{
		Category: "rgb", Action: "rgb_send", Title: "Finished transfer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateOperationLog(finished.ID, OperationLogUpdate{
		Status: OperationLogSucceeded, Message: "Transfer finished",
	}); err != nil {
		t.Fatal(err)
	}
	did, err := manager.BeginOperationLog(OperationLogCreate{
		Category: "did", Action: "inscribe_name", Title: "Register DID",
		Parameters: map[string]string{"name": name, "network": manager.GetChain()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateOperationLog(did.ID, OperationLogUpdate{
		Status: OperationLogSucceeded, Message: "Reveal submitted",
		Result: map[string]string{"reveal_txid": "reveal-tx"},
	}); err != nil {
		t.Fatal(err)
	}

	status, err = manager.CheckDIDName(name)
	if err != nil || status != "submitted" {
		t.Fatalf("unindexed local submission status=%q err=%v", status, err)
	}
	views, err := manager.GetOperationViews()
	if err != nil {
		t.Fatal(err)
	}
	viewStatus := map[string]string{}
	for _, view := range views {
		viewStatus[view.ID] = view.Status
	}
	if viewStatus[did.ID] != string(OperationLogPending) || viewStatus[finished.ID] != string(OperationLogSucceeded) {
		t.Fatalf("DID/indexer reconciliation statuses=%v", viewStatus)
	}
	if err := manager.DeleteOperationHistory(); err != nil {
		t.Fatal(err)
	}
	remaining, err := manager.GetOperationLogs()
	if err != nil || len(remaining) != 1 || remaining[0].ID != did.ID {
		t.Fatalf("history cleanup removed pending DID or retained finished history: logs=%+v err=%v", remaining, err)
	}

	lookup.info, lookup.err = &indexerwire.OrdinalsName{}, nil
	status, err = manager.CheckDIDName(name)
	if err != nil || status != "registered" {
		t.Fatalf("indexed DID status=%q err=%v", status, err)
	}
	views, err = manager.GetOperationViews()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].ID != did.ID || views[0].Status != string(OperationLogSucceeded) {
		t.Fatalf("indexed DID view=%+v", views)
	}
	if err := manager.DeleteOperationHistory(); err != nil {
		t.Fatal(err)
	}
	remaining, err = manager.GetOperationLogs()
	if err != nil || len(remaining) != 0 {
		t.Fatalf("finished history was not removed: logs=%+v err=%v", remaining, err)
	}
	status, err = manager.CheckDIDName(name)
	if err != nil || status != "registered" {
		t.Fatalf("clearing history bypassed indexed duplicate guard: status=%q err=%v", status, err)
	}

	lookup.info, lookup.err = nil, nil
	status, err = manager.CheckDIDName(name)
	if err == nil || status == "available" {
		t.Fatalf("nil index result was treated as availability: status=%q err=%v", status, err)
	}
	lookup.err = errors.New("temporary indexer outage")
	status, err = manager.CheckDIDName(name)
	if err == nil || status == "available" {
		t.Fatalf("indexer failure was treated as availability: status=%q err=%v", status, err)
	}
}

// The UI's manual receive journey is an SDK Manager/API contract, independent
// of WASM and the PWA. Its existing fixture uses a real Manager and RGB engine
// over in-memory indexer, DKVS, and Bitcoin evidence implementations.
func TestRGBManualInvoiceTransfer(t *testing.T) {
	sender, recipient, imported, evidence, _ := rgb11SendFixture(t, 10_000, 2)
	ctx := context.Background()
	const amount = "200.25"
	sourceOutpoint := rgb11GenericFixtureSource(t, sender, imported)

	contractFiles, err := sender.ExportRGBContract(imported.ContractID)
	if err != nil || len(contractFiles) != 2 {
		t.Fatalf("export public contract files: files=%+v err=%v", contractFiles, err)
	}
	var contractData []byte
	for _, file := range contractFiles {
		if file.Name == "rgb-contract.rgb" {
			contractData = file.Data
		}
	}
	if len(contractData) == 0 {
		t.Fatalf("public contract export omitted its binary file: %+v", contractFiles)
	}
	contractView, err := recipient.ImportRGBContract(ctx, contractData)
	if err != nil || contractView == nil {
		t.Fatalf("import exported public contract: view=%+v err=%v", contractView, err)
	}
	if contractView.ContractID != imported.ContractID || contractView.Asset != imported.AssetName.String() ||
		contractView.Precision != 2 || contractView.Kind != "NIA" || !strings.HasPrefix(contractView.Ticker, "send@") {
		t.Fatalf("imported contract did not preserve the public asset view: %+v", contractView)
	}

	invoice, err := recipient.CreateRGBInvoice(ctx, RGBInvoiceRequest{
		Asset: imported.AssetName.String(), Amount: amount, ReceiveMode: "witness", Transport: "manual",
	})
	if err != nil || invoice == nil {
		t.Fatalf("create public manual invoice: view=%+v err=%v", invoice, err)
	}
	if invoice.ID == "" || invoice.Invoice == "" || invoice.Amount.Amount != amount ||
		invoice.Amount.Precision != 2 || invoice.ReceiveMode != "witness" || invoice.Transport != "manual" || !invoice.CanImport {
		t.Fatalf("public invoice view lost requested amount or transport: %+v", invoice)
	}

	prepared, err := sender.PrepareRGBTransfer(ctx, RGBSendRequest{
		Recipients: []RGBRecipient{{Destination: invoice.Invoice}}, FeeRate: 2,
	})
	if err != nil || prepared == nil {
		t.Fatalf("prepare public invoice transfer: view=%+v err=%v", prepared, err)
	}
	if prepared.ID == "" || prepared.Status != "awaiting_receipts" || !prepared.NeedsReceipts ||
		len(prepared.Recipients) != 1 || prepared.Recipients[0].Amount.Amount != amount || prepared.Recipients[0].Amount.Precision != 2 {
		t.Fatalf("public transfer preview lost receipt/amount contract: %+v", prepared)
	}

	files, err := sender.ExportRGBTransfer(prepared.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("export public manual-transfer files: files=%+v err=%v", files, err)
	}
	var consignment []byte
	for _, file := range files {
		if file.RecipientIndex == 0 && strings.HasSuffix(file.File.Name, ".asc") {
			consignment = file.File.Data
		}
	}
	if len(consignment) == 0 {
		t.Fatalf("public transfer export omitted the recipient consignment: %+v", files)
	}

	evidence.mu.Lock()
	initialBroadcasts := evidence.broadcastCount
	evidence.mu.Unlock()
	if _, err := sender.ConfirmRGBTransfer(ctx, prepared.ID, nil); err == nil {
		t.Fatal("manual transfer broadcast without a recipient receipt")
	}
	evidence.mu.Lock()
	if evidence.broadcastCount != initialBroadcasts {
		evidence.mu.Unlock()
		t.Fatal("receipt rejection broadcast a transaction")
	}
	evidence.mu.Unlock()

	validated, err := recipient.PrepareRGBConsignment(ctx, invoice.ID, consignment)
	if err != nil || validated == nil {
		t.Fatalf("prepare public receive: view=%+v err=%v", validated, err)
	}
	if validated.Status != "awaiting_confirmation" || !validated.CanAccept || validated.Receipt == "" ||
		validated.Amount.Amount != amount || validated.Amount.Precision != 2 {
		t.Fatalf("public receive view omitted validated receipt or amount: %+v", validated)
	}

	confirmed, err := sender.ConfirmRGBTransfer(ctx, prepared.ID, []string{validated.Receipt})
	if err != nil || confirmed == nil || confirmed.ID != prepared.ID || confirmed.TxID == "" {
		t.Fatalf("confirm public transfer with matching receipt: view=%+v err=%v", confirmed, err)
	}
	evidence.mu.Lock()
	broadcasts := evidence.broadcastCount
	evidence.mu.Unlock()
	if broadcasts != initialBroadcasts+1 {
		t.Fatalf("matching receipt should broadcast exactly once: before=%d after=%d", initialBroadcasts, broadcasts)
	}

	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.ID)
	if err != nil || pending == nil {
		t.Fatalf("load controlled pending transaction for fake-chain evidence: pending=%+v err=%v", pending, err)
	}
	witness := wire.NewMsgTx(wire.TxVersion)
	if err := witness.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	if witness.TxHash().String() != confirmed.TxID || len(witness.TxOut) < 2 {
		t.Fatalf("confirmed transaction id/output mismatch: txid=%s outputs=%d", confirmed.TxID, len(witness.TxOut))
	}
	evidence.mu.Lock()
	evidence.rawTx[confirmed.TxID] = append([]byte(nil), pending.SignedTx...)
	evidence.spendingTx[sourceOutpoint] = confirmed.TxID
	for index, output := range witness.TxOut {
		outpoint := fmt.Sprintf("%s:%d", confirmed.TxID, index)
		evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: output.Value,
			PkScript: append([]byte(nil), output.PkScript...), Confirmations: 0,
		}
	}
	evidence.mu.Unlock()

	accepted, err := recipient.AcceptRGBConsignment(ctx, invoice.ID)
	if err != nil || accepted == nil || accepted.ID != invoice.ID {
		t.Fatalf("accept persisted public consignment: view=%+v err=%v", accepted, err)
	}
	view, err := recipient.GetRGBView()
	if err != nil {
		t.Fatal(err)
	}
	foundAsset := false
	for _, asset := range view.Assets {
		if asset.Asset == imported.AssetName.String() {
			foundAsset = true
			if asset.Available != "0" || asset.Pending != amount || asset.Precision != 2 {
				t.Fatalf("unconfirmed receive should remain pending in the public RGB view: %+v", asset)
			}
		}
	}
	if !foundAsset {
		t.Fatalf("accepted asset missing from public RGB view: %+v", view.Assets)
	}
	for _, receive := range view.Receives {
		if receive.ID == invoice.ID {
			if receive.Status != "pending_chain" {
				t.Fatalf("accepted receive has unexpected public status: %+v", receive)
			}
			return
		}
	}
	t.Fatalf("accepted receive missing from public RGB view: %+v", view.Receives)
}
