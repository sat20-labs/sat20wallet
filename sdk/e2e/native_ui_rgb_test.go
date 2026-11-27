package e2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	btcwire "github.com/btcsuite/btcd/wire"
	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

func nativeUIFund(t *testing.T, f *nativeIfaceChain, address string) {
	t.Helper()
	// Eight confirmed plain carriers from the group's post-Vintage SDK faucet.
	txid, _, err := f.faucet.BatchSendPlainSats(address, 100000, 8, 1, nil)
	require.NoError(t, err)
	require.Len(t, txid, 64)
	f.mine(t, 1)
	outputs := f.faucet.GetIndexerClient().GetUtxoListWithTicker(address, &indexercommon.ASSET_PLAIN_SAT)
	require.Len(t, outputs, 8)
	var total int64
	var expected, actual []string
	for i, out := range outputs {
		require.Equal(t, int64(100000), out.Value)
		require.Empty(t, out.Assets)
		total += out.Value
		expected = append(expected, fmt.Sprintf("%s:%d", txid, i))
		actual = append(actual, out.OutPoint)
	}
	require.ElementsMatch(t, expected, actual)
	require.Equal(t, int64(800000), total)
}
func nativeUIRGBPair(t *testing.T, f *nativeIfaceChain, seed byte) (*nativeUIDevice, *nativeUIDevice) {
	t.Helper()
	a := newNativeUIDevice(t, f.cfg, accountSyncMnemonic(t, seed))
	b := newNativeUIDevice(t, f.cfg, accountSyncMnemonic(t, seed+1))
	nativeUIFund(t, f, a.mgr.GetWallet().GetAddress())
	nativeUIFund(t, f, b.mgr.GetWallet().GetAddress())
	return a, b
}
func nativeUIIssue(t *testing.T, d *nativeUIDevice, kind, ticker string) *wallet.RGBAssetView {
	t.Helper()
	req := wallet.RGBIssueRequest{Kind: kind, Ticker: ticker, Name: "Native UI " + ticker,
		Details: "Isolated real regtest E2E", Precision: 2, Amounts: []string{"100"}}
	if kind == "IFA" {
		req.Amounts, req.Inflation = []string{"40", "60"}, []string{"10", "10"}
	}
	if kind == "UDA" {
		req.Precision, req.Amounts = 0, []string{"1"}
	}
	asset, err := d.mgr.IssueRGBAsset(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, kind, asset.Kind)
	require.NotEmpty(t, asset.ContractID)
	return asset
}
func nativeUIContract(t *testing.T, owner *nativeUIDevice, asset *wallet.RGBAssetView, suffix string) []byte {
	t.Helper()
	files, err := owner.mgr.ExportRGBContract(asset.ContractID)
	require.NoError(t, err)
	for _, f := range files {
		if strings.HasSuffix(f.Name, suffix) {
			require.NotEmpty(t, f.Data)
			return f.Data
		}
	}
	t.Fatalf("missing %s contract export", suffix)
	return nil
}
func nativeUIImport(t *testing.T, owner, target *nativeUIDevice, asset *wallet.RGBAssetView, suffix string) {
	t.Helper()
	imported, err := target.mgr.ImportRGBContract(context.Background(), nativeUIContract(t, owner, asset, suffix))
	require.NoError(t, err)
	require.Equal(t, asset.ContractID, imported.ContractID)
	require.Equal(t, "0", imported.Available)
}
func nativeUIInvoice(t *testing.T, d *nativeUIDevice, asset *wallet.RGBAssetView, amount, mode, proxy string) *wallet.RGBReceiveView {
	t.Helper()
	transport := "manual"
	if proxy != "" {
		transport = "proxy"
	}
	view, err := d.mgr.CreateRGBInvoice(context.Background(), wallet.RGBInvoiceRequest{
		Asset: asset.Asset, Amount: amount, ReceiveMode: mode, Transport: transport, ProxyEndpoint: proxy})
	require.NoError(t, err)
	require.Equal(t, amount, view.Amount.Amount)
	require.Equal(t, mode, view.ReceiveMode)
	require.Equal(t, transport, view.Transport)
	require.NotEmpty(t, view.Invoice)
	return view
}
func nativeUIBalance(t *testing.T, d *nativeUIDevice, asset *wallet.RGBAssetView, amount string) {
	t.Helper()
	var last *wallet.RGBView
	var lastErr error
	ok := false
	defer func() {
		if !ok {
			t.Logf("last RGB refresh: %+v; error=%v", last, lastErr)
		}
	}()
	require.Eventually(t, func() bool {
		last, lastErr = d.mgr.RefreshRGBState(context.Background())
		if lastErr != nil || last.ErrorCode != "" {
			return false
		}
		for _, a := range last.Assets {
			if a.Asset == asset.Asset {
				return a.Available == amount
			}
		}
		return false
	}, 90*time.Second, 200*time.Millisecond, "RGB amount must be %s", amount)
	ok = true
}
func nativeUITransfer(t *testing.T, d *nativeUIDevice, id string) wallet.RGBTransferView {
	t.Helper()
	view, err := d.mgr.GetRGBView()
	require.NoError(t, err)
	for _, transfer := range view.Transfers {
		if transfer.ID == id {
			return transfer
		}
	}
	t.Fatalf("transfer %s missing after restart", id)
	return wallet.RGBTransferView{}
}
func nativeUIProof(t *testing.T, files []wallet.RGBTransferFile, recipient int, format string) []byte {
	t.Helper()
	suffix := format
	if suffix == "json" {
		suffix = ".asc"
	}
	for _, f := range files {
		if f.RecipientIndex == recipient && strings.HasSuffix(f.File.Name, suffix) {
			require.NotEmpty(t, f.File.Data)
			if format == "json" {
				data, err := json.Marshal(map[string]string{"transport_mode": "out-of-band", "consignment": string(f.File.Data)})
				require.NoError(t, err)
				return data
			}
			return f.File.Data
		}
	}
	t.Fatalf("missing recipient %d %s proof", recipient, format)
	return nil
}
func nativeUINoBroadcast(t *testing.T, f *nativeIfaceChain, d *nativeUIDevice, before int64, txid string) {
	t.Helper()
	require.Equal(t, before, d.http.broadcasts.Load(), "rejection must never call the broadcast backend")
	var pool []string
	require.NoError(t, f.core.call("getrawmempool", nil, &pool))
	require.NotContains(t, pool, txid)
}
func nativeUITransaction(t *testing.T, f *nativeIfaceChain, txid string) *btcwire.MsgTx {
	t.Helper()
	var encoded string
	require.NoError(t, f.core.call("getrawtransaction", []any{txid, false}, &encoded))
	raw, err := hex.DecodeString(encoded)
	require.NoError(t, err)
	tx := btcwire.NewMsgTx(2)
	require.NoError(t, tx.Deserialize(bytes.NewReader(raw)))
	require.Equal(t, txid, tx.TxHash().String())
	for _, in := range tx.TxIn {
		require.NotEmpty(t, in.Witness, "real RGB transaction must carry signatures")
	}
	return tx
}
func nativeUISend(t *testing.T, f *nativeIfaceChain, owner, receiver *nativeUIDevice,
	asset *wallet.RGBAssetView, amount, mode, format string, inflate bool) *wallet.RGBTransferView {
	t.Helper()
	confirmed, invoice := nativeUIBroadcast(t, f, owner, receiver, asset, amount, mode, format, inflate)
	f.mine(t, 1)
	accepted, err := receiver.mgr.AcceptRGBConsignment(context.Background(), invoice.ID)
	require.NoError(t, err)
	require.Equal(t, confirmed.TxID, accepted.TxID)
	return confirmed
}

func nativeUIBroadcast(t *testing.T, f *nativeIfaceChain, owner, receiver *nativeUIDevice,
	asset *wallet.RGBAssetView, amount, mode, format string, inflate bool) (*wallet.RGBTransferView, *wallet.RGBReceiveView) {
	t.Helper()
	ctx := context.Background()
	invoice := nativeUIInvoice(t, receiver, asset, amount, mode, "")
	var prepared *wallet.RGBTransferView
	var err error
	if inflate {
		prepared, err = owner.mgr.PrepareRGBInflation(ctx, invoice.Invoice, 2)
	} else {
		prepared, err = owner.mgr.PrepareRGBTransfer(ctx, wallet.RGBSendRequest{Recipients: []wallet.RGBRecipient{{Destination: invoice.Invoice}}, FeeRate: 2})
	}
	require.NoError(t, err)
	require.True(t, prepared.NeedsReceipts)
	require.Equal(t, amount, prepared.Recipients[0].Amount.Amount)
	broadcasts := owner.http.broadcasts.Load()
	files, err := owner.mgr.ExportRGBTransfer(prepared.ID)
	require.NoError(t, err)
	_, err = receiver.mgr.PrepareRGBConsignment(ctx, invoice.ID, []byte("invalid proof"))
	require.Error(t, err)
	received, err := receiver.mgr.PrepareRGBConsignment(ctx, invoice.ID, nativeUIProof(t, files, 0, format))
	require.NoError(t, err)
	require.True(t, received.CanAccept)
	require.NotEmpty(t, received.Receipt)
	if mode == "blind" {
		owner.reopen(t)
		nativeUIReady(t, owner.mgr)
		if receiver != owner {
			receiver.reopen(t)
			nativeUIReady(t, receiver.mgr)
		}
		require.True(t, nativeUITransfer(t, owner, prepared.ID).CanConfirm)
		saved, err := receiver.mgr.GetRGBView()
		require.NoError(t, err)
		found := false
		for _, r := range saved.Receives {
			if r.ID == invoice.ID {
				require.Equal(t, received.Receipt, r.Receipt)
				found = true
			}
		}
		require.True(t, found, "validated receipt must survive a physical DB restart")
	}
	_, err = owner.mgr.ConfirmRGBTransfer(ctx, prepared.ID, []string{"invalid receipt"})
	require.Error(t, err)
	nativeUINoBroadcast(t, f, owner, broadcasts, prepared.TxID)
	confirmed, err := owner.mgr.ConfirmRGBTransfer(ctx, prepared.ID, []string{received.Receipt})
	require.NoError(t, err)
	require.Equal(t, prepared.ID, confirmed.ID)
	require.Equal(t, prepared.TxID, confirmed.TxID)
	nativeUITransaction(t, f, confirmed.TxID)
	return confirmed, invoice
}

func runNativeRGBManual(t *testing.T, f *nativeIfaceChain) {
	ctx := context.Background()
	owner, receiver := nativeUIRGBPair(t, f, 225)
	asset := nativeUIIssue(t, owner, "IFA", "UIMAN")
	require.True(t, asset.CanInflate)
	nativeUIImport(t, owner, receiver, asset, ".rgb")
	nativeUIBalance(t, owner, asset, "100")
	sent := nativeUISend(t, f, owner, receiver, asset, "10.25", "witness", ".asc", false)
	nativeUIBalance(t, receiver, asset, "10.25")
	nativeUIBalance(t, owner, asset, "89.75")
	nativeUISend(t, f, owner, receiver, asset, "2.5", "witness", ".asc", true)
	nativeUIBalance(t, receiver, asset, "12.75")
	nativeUIBalance(t, owner, asset, "89.75")
	invoice := nativeUIInvoice(t, receiver, asset, "1", "witness", "")
	pending, err := owner.mgr.PrepareRGBTransfer(ctx, wallet.RGBSendRequest{Recipients: []wallet.RGBRecipient{{Destination: invoice.Invoice}}, FeeRate: 2})
	require.NoError(t, err)
	root := owner.mgr.GetCurrentWalletId()
	require.NoError(t, owner.mgr.EnsureAccount(root, 1, "Separate account", ""))
	require.NoError(t, owner.mgr.SwitchAccount(1))
	other, err := owner.mgr.GetRGBView()
	require.NoError(t, err)
	for _, transfer := range other.Transfers {
		require.NotEqual(t, pending.ID, transfer.ID)
	}
	_, err = owner.mgr.ConfirmRGBTransfer(ctx, pending.ID, nil)
	require.Error(t, err)
	require.NoError(t, owner.mgr.SwitchAccount(0))
	owner.reopen(t)
	nativeUIReady(t, owner.mgr)
	require.NoError(t, owner.mgr.DeleteOperationHistory())
	require.True(t, nativeUITransfer(t, owner, pending.ID).CanCancel)
	logs, err := owner.mgr.GetOperationViews()
	require.NoError(t, err)
	running := false
	for _, entry := range logs {
		running = running || entry.Status == "running"
	}
	require.True(t, running, "clearing history must keep unfinished operation records")
	cancelled, err := owner.mgr.CancelRGBTransfer(ctx, pending.ID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", cancelled.Status)
	owner.http.l1Offline.Store(true)
	owner.reopen(t)
	nativeUIReady(t, owner.mgr)
	saved, err := owner.mgr.GetRGBView()
	require.NoError(t, err)
	found := false
	for _, a := range saved.Assets {
		if a.Asset == asset.Asset {
			require.Equal(t, "89.75", a.Available)
			require.True(t, a.CanSend)
			found = true
		}
	}
	require.True(t, found, "local saved allocation must be readable without L1 refresh")
	owner.http.l1Offline.Store(false)
	receiver.reopen(t)
	nativeUIReady(t, receiver.mgr)
	nativeUIBalance(t, owner, asset, "89.75")
	nativeUIBalance(t, receiver, asset, "12.75")
	require.Equal(t, "completed", nativeUITransfer(t, owner, sent.ID).Status)
	files, err := owner.mgr.ExportRGBTransfer(sent.ID)
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, file := range files {
		require.NotEmpty(t, file.File.Data)
	}
	require.NoError(t, owner.mgr.DeleteOperationHistory())
	logs, err = owner.mgr.GetOperationViews()
	require.NoError(t, err)
	for _, entry := range logs {
		require.NotContains(t, []string{"succeeded", "cancelled"}, entry.Status)
	}
	first := nativeUIInvoice(t, receiver, asset, "1.25", "witness", "")
	blind := nativeUIInvoice(t, receiver, asset, "2.5", "blind", "")
	_, err = owner.mgr.PrepareRGBTransfer(ctx, wallet.RGBSendRequest{Recipients: []wallet.RGBRecipient{{Destination: first.Invoice}, {Destination: blind.Invoice}}, FeeRate: 2})
	require.EqualError(t, err, "RGB11 batch send requires witness invoices")
	second := nativeUIInvoice(t, receiver, asset, "2.5", "witness", "")
	batch, err := owner.mgr.PrepareRGBTransfer(ctx, wallet.RGBSendRequest{Recipients: []wallet.RGBRecipient{{Destination: first.Invoice}, {Destination: second.Invoice}}, FeeRate: 2})
	require.NoError(t, err)
	require.Len(t, batch.Recipients, 2)
	require.Equal(t, "1.25", batch.Recipients[0].Amount.Amount)
	require.Equal(t, "2.5", batch.Recipients[1].Amount.Amount)
	files, err = owner.mgr.ExportRGBTransfer(batch.ID)
	require.NoError(t, err)
	invoices := []*wallet.RGBReceiveView{first, second}
	var receipts []string
	for i, request := range invoices {
		received, err := receiver.mgr.PrepareRGBConsignment(ctx, request.ID, nativeUIProof(t, files, i, ".asc"))
		require.NoError(t, err)
		require.NotEmpty(t, received.Receipt)
		receipts = append(receipts, received.Receipt)
	}
	before := owner.http.broadcasts.Load()
	_, err = owner.mgr.ConfirmRGBTransfer(ctx, batch.ID, receipts[:1])
	require.Error(t, err)
	_, err = owner.mgr.ConfirmRGBTransfer(ctx, batch.ID, []string{receipts[0], receipts[0]})
	require.Error(t, err)
	nativeUINoBroadcast(t, f, owner, before, batch.TxID)
	confirmed, err := owner.mgr.ConfirmRGBTransfer(ctx, batch.ID, []string{receipts[1], receipts[0]})
	require.NoError(t, err)
	require.Equal(t, batch.ID, confirmed.ID)
	require.Equal(t, batch.TxID, confirmed.TxID)
	f.mine(t, 1)
	for _, request := range invoices {
		received, err := receiver.mgr.AcceptRGBConsignment(ctx, request.ID)
		require.NoError(t, err)
		require.Equal(t, batch.TxID, received.TxID)
	}
	owner.reopen(t)
	receiver.reopen(t)
	nativeUIReady(t, owner.mgr)
	nativeUIReady(t, receiver.mgr)
	nativeUIBalance(t, owner, asset, "86")
	nativeUIBalance(t, receiver, asset, "16.5")

	// Recovering the root on a completely empty device restores actual remote
	// allocations and inflation authority; no database or proof file is copied.
	owner.mgr.Start()
	nativeUIReady(t, owner.mgr)
	catalog := owner.mgr.GetWalletCatalog()
	nativeUIConfirm(t, owner.mgr, nativeUIStorage(t, owner.mgr, wallet.AccountStorageTemporary, 0))
	ids := make([]int64, 0, len(catalog))
	for _, entry := range catalog {
		ids = append(ids, entry.ID)
	}
	activated, err := owner.mgr.ActivateRootAccount(ctx, owner.password, ids)
	require.NoError(t, err)
	require.Empty(t, activated.Locator)
	require.Empty(t, activated.RecoveryMode)
	requireE2EFirstBackup(t, owner.mgr)
	state, err := owner.mgr.GetRGBView()
	require.NoError(t, err)
	sort.Slice(state.Assets, func(i, j int) bool { return state.Assets[i].Asset < state.Assets[j].Asset })
	mnemonic := owner.mgr.GetMnemonic(root, owner.password)
	require.NotEmpty(t, mnemonic)
	source := owner.mgr.GetAccountManagementStatus()
	require.True(t, source.RecoveryConfigured)
	require.NotEmpty(t, source.AccountID)
	require.Empty(t, source.PackageID)
	require.Equal(t, wallet.AccountStorageTemporary, source.StorageMode)
	owner.close()
	recovered := newNativeUIDevice(t, f.cfg, "")
	require.Empty(t, recovered.mgr.GetWalletCatalog())
	// Do not create a fresh local profile or request a backup before the
	// authenticated recovery has restored the existing account backup.
	requireAccountPWARootRecovery(t, recovered.mgr, mnemonic, recovered.password)
	account := recovered.mgr.GetAccountManagementStatus()
	require.True(t, account.RecoveryConfigured)
	require.Equal(t, source.AccountID, account.AccountID)
	require.Equal(t, source.PackageID, account.PackageID)
	require.Equal(t, source.StorageMode, account.StorageMode)
	nativeUIReady(t, recovered.mgr)
	nativeUIBackup(t, recovered.mgr)
	require.Equal(t, source.AccountID, recovered.mgr.GetAccountManagementStatus().AccountID)
	nativeUICatalog(t, recovered.mgr, catalog)
	restored, err := recovered.mgr.GetRGBView()
	require.NoError(t, err)
	sort.Slice(restored.Assets, func(i, j int) bool { return restored.Assets[i].Asset < restored.Assets[j].Asset })
	require.Equal(t, state.Assets, restored.Assets, "remote state must restore allocations and IFA authority")
	t.Logf("root-only regtest restore: schema=%s contract=%s balance_before=86 balance_after=86 inflation_authority=%t storage=%s",
		asset.Kind, asset.ContractID, asset.CanInflate, account.StorageMode)
	resent := nativeUISend(t, f, recovered, receiver, asset, "1.25", "witness", ".asc", false)
	require.NotEqual(t, sent.TxID, resent.TxID)
	nativeUIBalance(t, recovered, asset, "84.75")
	nativeUIBalance(t, receiver, asset, "17.75")
	nativeUISend(t, f, recovered, receiver, asset, "2.5", "witness", ".asc", true)
	nativeUIBalance(t, receiver, asset, "20.25")
	nativeUIBalance(t, recovered, asset, "84.75")
	nativeUIBackup(t, recovered.mgr)
	recovered.reopen(t)
	nativeUIReady(t, recovered.mgr)
	nativeUIBalance(t, recovered, asset, "84.75")
}

func runNativeRGBBlind(t *testing.T, f *nativeIfaceChain) {
	owner, receiver := nativeUIRGBPair(t, f, 227)
	ctx := context.Background()
	asset := nativeUIIssue(t, owner, "NIA", "UIBLIND")
	_, err := receiver.mgr.ImportRGBContract(ctx, []byte("invalid contract"))
	require.Error(t, err)
	nativeUIImport(t, owner, receiver, asset, ".asc")
	_, err = receiver.mgr.CreateRGBInvoice(ctx, wallet.RGBInvoiceRequest{Asset: asset.Asset, Amount: "0", ReceiveMode: "blind", Transport: "manual"})
	require.Error(t, err)
	_, err = owner.mgr.PrepareRGBTransfer(ctx, wallet.RGBSendRequest{Recipients: []wallet.RGBRecipient{{Destination: "invalid address", Asset: asset.Asset, Amount: "1"}}, FeeRate: 2})
	require.Error(t, err)
	nativeUISend(t, f, owner, receiver, asset, "10.25", "blind", ".rgb", false)
	nativeUIBalance(t, owner, asset, "89.75")
	nativeUIBalance(t, receiver, asset, "10.25")
	uda := nativeUIIssue(t, owner, "UDA", "UIUNIQUE")
	require.Equal(t, "1", uda.Available)
	require.False(t, uda.CanInflate)
	nativeUIImport(t, owner, receiver, uda, ".rgb")
	nativeUISend(t, f, owner, receiver, uda, "1", "blind", "json", false)
	nativeUIBalance(t, owner, uda, "0")
	nativeUIBalance(t, receiver, uda, "1")
	receiver.reopen(t)
	nativeUIReady(t, receiver.mgr)
	nativeUIBalance(t, receiver, asset, "10.25")
	nativeUIBalance(t, receiver, uda, "1")
}

func runNativeRGBProxy(t *testing.T, f *nativeIfaceChain) {
	ctx := context.Background()
	owner, receiver := nativeUIRGBPair(t, f, 229)
	asset := nativeUIIssue(t, owner, "NIA", "UIPROXY")
	nativeUIImport(t, owner, receiver, asset, ".rgb")
	proxy := startPwaRGBProxy(t)
	for _, item := range []struct{ mode, amount, received, remaining string }{
		{"witness", "10.25", "10.25", "89.75"}, {"blind", "2.5", "12.75", "87.25"},
	} {
		invoice := nativeUIInvoice(t, receiver, asset, item.amount, item.mode, proxy)
		prepared, err := owner.mgr.PrepareRGBTransfer(ctx, wallet.RGBSendRequest{Recipients: []wallet.RGBRecipient{{Destination: invoice.Invoice}}, FeeRate: 2})
		require.NoError(t, err)
		require.False(t, prepared.NeedsReceipts)
		receiver.close()
		before := owner.http.broadcasts.Load()
		delivered, err := owner.mgr.ConfirmRGBTransfer(ctx, prepared.ID, nil)
		require.NoError(t, err)
		require.Equal(t, "awaiting_ack", delivered.Status)
		nativeUINoBroadcast(t, f, owner, before, prepared.TxID)
		owner.reopen(t)
		nativeUIReady(t, owner.mgr)
		require.Equal(t, prepared.TxID, nativeUITransfer(t, owner, prepared.ID).TxID)
		receiver.reopen(t)
		nativeUIReady(t, receiver.mgr)
		_, err = receiver.mgr.RefreshRGBState(ctx)
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			continued, e := owner.mgr.ConfirmRGBTransfer(ctx, prepared.ID, nil)
			if e != nil {
				return false
			}
			require.Equal(t, prepared.ID, continued.ID)
			require.Equal(t, prepared.TxID, continued.TxID)
			return continued.Status == "pending_chain"
		}, 60*time.Second, 200*time.Millisecond)
		f.mine(t, 1)
		nativeUIBalance(t, receiver, asset, item.received)
		view, err := receiver.mgr.GetRGBView()
		require.NoError(t, err)
		found := false
		for _, r := range view.Receives {
			if r.ID == invoice.ID {
				require.Equal(t, "completed", r.Status)
				found = true
			}
		}
		require.True(t, found)
		// Read-only polling exercises the existing sender monitor. Refreshing the
		// sender here would hide a missing automatic change reconciliation.
		require.Eventually(t, func() bool {
			view, e := owner.mgr.GetRGBView()
			if e != nil {
				return false
			}
			for _, a := range view.Assets {
				if a.Asset == asset.Asset {
					return a.Available == item.remaining
				}
			}
			return false
		}, 90*time.Second, 200*time.Millisecond)
	}
	nativeUIBalance(t, owner, asset, "87.25")
	receiver.reopen(t)
	nativeUIReady(t, receiver.mgr)
	nativeUIBalance(t, receiver, asset, "12.75")
}

func runNativeRGBDirect(t *testing.T, f *nativeIfaceChain) {
	ctx := context.Background()
	owner, receiver := nativeUIRGBPair(t, f, 231)
	nativeUILocalBackup(t, owner)
	nativeUILocalBackup(t, receiver)
	asset := nativeUIIssue(t, owner, "NIA", "UIDIR")
	nativeUIImport(t, owner, receiver, asset, ".rgb")
	nativeUIBalance(t, receiver, asset, "0")
	targetAddress := receiver.mgr.GetWallet().GetAddress()
	ownScript, err := wallet.PubKeyToPkScript(receiver.mgr.GetWallet().GetPubKey())
	require.NoError(t, err)
	otherScript, err := wallet.PubKeyToPkScript(owner.mgr.GetWallet().GetPubKey())
	require.NoError(t, err)
	nativeUIBackup(t, receiver.mgr)
	receiver.close()
	prepared, err := owner.mgr.PrepareRGBTransfer(ctx, wallet.RGBSendRequest{Recipients: []wallet.RGBRecipient{{Destination: targetAddress, Asset: asset.Asset, Amount: "10.25"}}, FeeRate: 2})
	require.NoError(t, err)
	before := owner.http.broadcasts.Load()
	nativeUINoBroadcast(t, f, owner, before, prepared.TxID)
	// The receiver wallet is closed, but its Core can durably accept the consignment.
	broadcast, err := owner.mgr.ConfirmRGBTransfer(ctx, prepared.ID, nil)
	require.NoError(t, err)
	require.Equal(t, prepared.ID, broadcast.ID)
	require.Equal(t, prepared.TxID, broadcast.TxID)
	require.Equal(t, "pending_chain", broadcast.Status)
	require.Equal(t, before+1, owner.http.broadcasts.Load())
	tx := nativeUITransaction(t, f, broadcast.TxID)
	owner.close()
	receiver.reopen(t)
	nativeUIReady(t, receiver.mgr)
	_, err = receiver.mgr.RefreshRGBState(ctx)
	require.NoError(t, err)
	nativeUIBackup(t, receiver.mgr)
	receiver.close()
	owner.reopen(t)
	nativeUIReady(t, owner.mgr)
	replayed, err := owner.mgr.ConfirmRGBTransfer(ctx, prepared.ID, nil)
	require.NoError(t, err)
	require.Equal(t, broadcast.ID, replayed.ID)
	require.Equal(t, broadcast.TxID, replayed.TxID)
	require.Equal(t, "pending_chain", replayed.Status)
	require.Equal(t, before+1, owner.http.broadcasts.Load(), "restart and confirmation must not rebroadcast")
	f.mine(t, 1)
	ownIndex, otherIndex := -1, -1
	for i, out := range tx.TxOut {
		if bytes.Equal(out.PkScript, ownScript) {
			ownIndex = i
		}
		if bytes.Equal(out.PkScript, otherScript) {
			otherIndex = i
		}
	}
	require.GreaterOrEqual(t, ownIndex, 0)
	require.GreaterOrEqual(t, otherIndex, 0)
	nativeUIBalance(t, owner, asset, "89.75")
	spent, invoice := nativeUIBroadcast(t, f, owner, owner, asset, "1", "witness", ".asc", false)
	f.mine(t, 1)
	nativeUIRepeatSelfReceive(t, owner, spent, invoice, "89.75")
	spendTx := nativeUITransaction(t, f, spent.TxID)
	consumed := false
	for _, input := range spendTx.TxIn {
		consumed = consumed || input.PreviousOutPoint.Hash.String() == broadcast.TxID && int(input.PreviousOutPoint.Index) == otherIndex
	}
	require.True(t, consumed, "the second valid RGB transfer must spend the sibling RGB change")
	var own, other json.RawMessage
	require.NoError(t, f.core.call("gettxout", []any{broadcast.TxID, ownIndex, true}, &own))
	require.NoError(t, f.core.call("gettxout", []any{broadcast.TxID, otherIndex, true}, &other))
	require.NotEqual(t, "null", string(own), "receiver's actual output remains unspent")
	require.NotEmpty(t, own)
	require.Equal(t, "null", string(other), "sibling change is genuinely spent")
	receiver.reopen(t)
	nativeUIReady(t, receiver.mgr)
	nativeUIBalance(t, receiver, asset, "10.25")
	done := make(chan error, 1)
	go func() { _, e := receiver.mgr.RefreshRGBState(ctx); done <- e }()
	f.mine(t, 1)
	require.NoError(t, <-done)
	nativeUIBalance(t, receiver, asset, "10.25")
}

func runNativeRGBBlindSelf(t *testing.T, f *nativeIfaceChain) {
	owner := newNativeUIDevice(t, f.cfg, accountSyncMnemonic(t, 234))
	nativeUIFund(t, f, owner.mgr.GetWallet().GetAddress())
	asset := nativeUIIssue(t, owner, "NIA", "UIBS")
	sent, invoice := nativeUIBroadcast(t, f, owner, owner, asset, "1", "blind", ".asc", false)
	f.mine(t, 1)
	nativeUIRepeatSelfReceive(t, owner, sent, invoice, "100")
}

func nativeUIRepeatSelfReceive(t *testing.T, owner *nativeUIDevice, sent *wallet.RGBTransferView,
	invoice *wallet.RGBReceiveView, balance string) {
	t.Helper()
	ctx := context.Background()
	_, err := owner.mgr.RefreshRGBState(ctx)
	require.NoError(t, err)
	state, err := owner.mgr.GetRGB11State()
	require.NoError(t, err)
	var receiveOut, changeOut, consignmentHash string
	for _, transfer := range state.Transfers {
		if transfer.TransferID != sent.ID {
			continue
		}
		// A single-recipient self-transfer has the same ID on both sides.
		// Refresh processes the receive first when their IDs are equal.
		require.Equal(t, "settled", transfer.Status)
		if transfer.Direction == "receive" {
			require.Equal(t, invoice.ID, transfer.ReceiveRequestID)
			require.Len(t, transfer.OutputOutPoints, 1)
			receiveOut, consignmentHash = transfer.OutputOutPoints[0], transfer.ConsignmentHash
		} else {
			require.NotEmpty(t, transfer.OutputOutPoints)
			changeOut = transfer.OutputOutPoints[len(transfer.OutputOutPoints)-1]
		}
	}
	require.NotEmpty(t, receiveOut)
	require.NotEmpty(t, changeOut)
	if invoice.ReceiveMode == "blind" {
		require.False(t, strings.HasPrefix(receiveOut, sent.TxID+":"), "blind carrier predates the RGB witness")
	}
	var projected []string
	for _, proof := range state.Proofs {
		if proof.ConsignmentHash == consignmentHash && proof.AssetName.String() == invoice.Amount.Asset {
			projected = append(projected, proof.OutPoint)
		}
	}
	require.ElementsMatch(t, []string{receiveOut, changeOut}, projected)
	accepted, err := owner.mgr.AcceptRGBConsignment(ctx, invoice.ID)
	require.NoError(t, err)
	require.Equal(t, sent.TxID, accepted.TxID)
	require.Equal(t, "completed", accepted.Status)
	view, err := owner.mgr.GetRGBView()
	require.NoError(t, err)
	for _, asset := range view.Assets {
		if asset.Asset == invoice.Amount.Asset {
			require.Equal(t, balance, asset.Available)
			return
		}
	}
	t.Fatal("self-transfer asset missing after repeated acceptance")
}

func runNativeDID(t *testing.T, f *nativeIfaceChain) {
	owner := newNativeUIDevice(t, f.cfg, accountSyncMnemonic(t, 233))
	nativeUIFund(t, f, owner.mgr.GetWallet().GetAddress())
	name := fmt.Sprintf("ui%x.sats", time.Now().UnixNano())
	status, err := owner.mgr.CheckDIDName("bad name!")
	require.NoError(t, err)
	require.Equal(t, "invalid", status)
	status, err = owner.mgr.CheckDIDName(name)
	require.NoError(t, err)
	require.Equal(t, "available", status)
	_, err = owner.mgr.InscribeName(name, 2)
	require.NoError(t, err)
	status, err = owner.mgr.CheckDIDName(name)
	require.NoError(t, err)
	require.Equal(t, "submitted", status)
	_, err = owner.mgr.InscribeName(name, 2)
	require.Error(t, err)
	logs, err := owner.mgr.GetOperationViews()
	require.NoError(t, err)
	submitted := false
	for _, log := range logs {
		if log.Kind == "inscribe_name" {
			require.NotEmpty(t, log.TxIDs)
			require.Equal(t, "pending", log.Status)
			submitted = true
		}
	}
	require.True(t, submitted)
	f.mine(t, 1)
	require.Eventually(t, func() bool { s, e := owner.mgr.CheckDIDName(name); return e == nil && s == "registered" }, 60*time.Second, 200*time.Millisecond)
	require.NoError(t, owner.mgr.DeleteOperationHistory())
	status, err = owner.mgr.CheckDIDName(name)
	require.NoError(t, err)
	require.Equal(t, "registered", status)
}
