package wallet

import (
	"os"
	"strings"
	"testing"
)

func readRepoTextForTest(t *testing.T, relative string) string {
	t.Helper()
	raw, err := os.ReadFile(relative)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestWASMRuntimeExitDiagnosticsAreSafeAndActionable(t *testing.T) {
	wasm := readRepoTextForTest(t, "../../pwa/utils/wasm.ts")
	sat20 := readRepoTextForTest(t, "../../pwa/utils/sat20.ts")
	accountFacade := readRepoTextForTest(t, "../../pwa/utils/accountManagement.ts")
	diagnostics := readRepoTextForTest(t, "../../pwa/utils/wasmRuntimeDiagnostics.ts")
	goWasm := readRepoTextForTest(t, "../wasm/main.go")

	for _, want := range []string{
		"const originalExit = go.exit.bind(go)",
		"kind: 'go-runtime-exit'",
		"snapshotWasmRuntimeDiagnostics()",
		"exit code",
	} {
		if !strings.Contains(wasm, want) {
			t.Fatalf("WASM runtime diagnostics missing %q", want)
		}
	}
	for _, want := range []string{
		"noteWasmOperation(methodName, 'dispatch')",
		"noteWasmOperation(methodName, 'returned')",
		"noteWasmOperation(methodName, 'failed')",
	} {
		if !strings.Contains(sat20, want) {
			t.Fatalf("wallet request breadcrumb missing %q", want)
		}
	}
	for _, want := range []string{
		"const diagnosticMethod = `account.${methodName}`",
		"noteWasmOperation(diagnosticMethod, 'dispatch')",
		"noteWasmOperation(diagnosticMethod, 'returned')",
		"noteWasmOperation(diagnosticMethod, 'failed')",
	} {
		if !strings.Contains(accountFacade, want) {
			t.Fatalf("account request breadcrumb missing %q", want)
		}
	}
	if !strings.Contains(diagnostics, "const maxBreadcrumbs = 32") {
		t.Fatal("runtime breadcrumb buffer must remain bounded")
	}
	lower := strings.ToLower(diagnostics)
	for _, forbidden := range []string{"args", "payload", "mnemonic", "password", "proof"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("runtime diagnostics may expose sensitive field %q", forbidden)
		}
	}
	if !strings.Contains(goWasm, "debug.Stack()") ||
		!strings.Contains(goWasm, "wasm async panic") {
		t.Fatal("exported WASM panic path must retain a Go stack diagnostic")
	}
}

func TestAccountStorageAuthorizationIsSDKOwnedAndNonFunding(t *testing.T) {
	sdk := readRepoTextForTest(t, "../../pwa/utils/accountManagement.ts")
	page := readRepoTextForTest(t, "../../pwa/entrypoints/popup/pages/wallet/settings/account-management/Index.vue")
	wasm := readRepoTextForTest(t, "../wasm/account_management.go")
	views := readRepoTextForTest(t, "account_ui_status.go")
	wallet := readRepoTextForTest(t, "account_pwa.go")

	if strings.Contains(sdk, "pendingStorageAuthorization") {
		t.Fatal("PWA must not own pending account storage authorization state")
	}
	if strings.Contains(sdk, "walletStorage") || strings.Contains(sdk, "localStorage") {
		t.Fatal("unfinished storage authorization must not be persisted by the PWA SDK")
	}
	if !strings.Contains(page, "(await accountSDK.getAccountView()).storage") ||
		!strings.Contains(page, "accountSDK.confirmStorage(option)") ||
		!strings.Contains(page, "accountSDK.cancelStorageSetup()") {
		t.Fatal("account setup page must resume, confirm and cancel through the SDK")
	}
	for _, want := range []string{
		"p.PendingAccountStorageAuthorization()",
		"p.CancelPendingAccountStorageAuthorization()",
		"p.ReusePaidAccountStorage(confirmed.RecordCount)",
	} {
		if !strings.Contains(views, want) {
			t.Fatalf("SDK-owned account storage support missing %q", want)
		}
	}
	reuseCall := strings.Index(views, "p.ReusePaidAccountStorage(confirmed.RecordCount)")
	confirmCall := strings.Index(views, "p.confirmAccountStorage(confirmed.ID, confirmed.RecordCount, &confirmed.TTLBlocks)")
	if confirmCall <= reuseCall || !strings.Contains(views[reuseCall:confirmCall], "return p.storageView(auth), nil") {
		t.Fatal("a reusable paid authorization must return before a fresh storage confirmation")
	}
	if strings.Contains(wasm, "accountSessions.storage") ||
		strings.Contains(wasm, "StorageAuthorizationID") {
		t.Fatal("WASM bridge must not keep a second storage-authorization authority")
	}
	start := strings.Index(wallet, "func (p *Manager) ReusePaidAccountStorage")
	if start < 0 {
		t.Fatal("non-funding paid storage reuse API missing")
	}
	end := strings.Index(wallet[start+1:], "\nfunc ")
	if end < 0 {
		end = len(wallet) - start - 1
	}
	reuse := wallet[start : start+1+end]
	if strings.Contains(reuse, "fundAccountAutopayWithWallet") {
		t.Fatal("paid storage reuse must never fund AUTOPAY")
	}
}

func TestRGB11DirectLifecycleAuthorityIsSDKOwned(t *testing.T) {
	tabs := readRepoTextForTest(t, "../../pwa/components/asset/L1AssetsTabs.vue")
	dialog := readRepoTextForTest(t, "../../pwa/components/wallet/RGB11SendDialog.vue")
	store := readRepoTextForTest(t, "../../pwa/store/rgb11.ts")
	facade := readRepoTextForTest(t, "../../pwa/utils/sat20.ts")
	requireSteps := func(label, source string, steps ...string) {
		t.Helper()
		for _, step := range steps {
			_, rest, ok := strings.Cut(source, step)
			if !ok {
				t.Fatalf("%s: Direct lifecycle step missing or out of order: %q", label, step)
			}
			source = rest
		}
	}

	pwa := strings.Join([]string{tabs, dialog, store, facade}, "\n")
	for _, forbidden := range []string{
		"directAutoTried", "canResumeDirect", "directAttemptKey",
		"needsDirectResume", "resumeRGB11Task(task, true)", "beforeDispatch",
	} {
		if strings.Contains(pwa, forbidden) {
			t.Fatalf("PWA still owns Direct lifecycle state %q", forbidden)
		}
	}
	requireSteps("task list", tabs, `@click="resumeRGB11Task(task)"`, "resumeTaskId.value = task.id", "walletManager.refreshRGB11State()")
	requireSteps("send dialog", dialog, `v-if="transfer.can_confirm"`, `@click="confirm"`, "const confirm =", "const id = transfer.value.id", "walletManager.confirmRGB11Transfer(id, receipts.value)")
	// Reopening a saved task reads its SDK view. Only the explicit confirmation may dispatch it.
	_, onOpen, _ := strings.Cut(dialog, "watch([isOpen, assetKey]")
	onOpen, _, _ = strings.Cut(onOpen, "\n},")
	requireSteps("reopened task", onOpen, "if (props.resumeTransferId)", "walletManager.getRGB11View()", "item.id === props.resumeTransferId")
	if strings.Contains(onOpen, "confirm(") || strings.Count(tabs+store+dialog, ".confirmRGB11Transfer(") != 1 {
		t.Fatal("the PWA must not confirm a Direct transfer when reopening or refreshing it")
	}
	wasm := readRepoTextForTest(t, "../wasm/rgb_ui.go")
	view := readRepoTextForTest(t, "rgb_ui.go")
	requireSteps("refresh facade", facade, "async refreshRGB11State(", "this._handleRequest('refreshRGB11State')")
	requireSteps("confirm facade", facade, "async confirmRGB11Transfer(", "this._handleRequest('confirmRGB11Transfer'")
	requireSteps("refresh bridge", wasm, "func refreshRGB11State(", "_mgr.RefreshRGBState(")
	requireSteps("confirm bridge", wasm, "func confirmRGB11Transfer(", "_mgr.ConfirmRGBTransfer(")
	requireSteps("SDK refresh", view, "func (p *Manager) RefreshRGBState(", "p.SyncConfiguredRGB11AddressMailbox(")
	requireSteps("SDK confirmation", view, "func (p *Manager) ConfirmRGBTransfer(", "case first.AddressMode:", "p.DeliverAndBroadcastConfiguredRGB11AddressTransfer(first.TransferID)")
	requireSteps("account mailbox", readRepoTextForTest(t, "account_rgb_messages.go"), "mailboxSubscriptionTarget(id)", "p.ensureOwnedDKVSPrefix(target)", "p.watchAccountMailbox(wallet)", "store.ListMailboxVerified(id, verify)")
	requireSteps("mailbox registration", readRepoTextForTest(t, "dkvs_registry.go"), "func (p *Manager) ensureOwnedDKVSPrefix(", "p.SubscribeDKVSPrefix(prefix)")
	requireSteps("SDK Direct recovery", readRepoTextForTest(t, "rgb11_dkvs.go"),
		"readAccountMessages(p.wallet, verify)", "p.resumeReadyRGB11AddressTransfers()", "p.continueRGB11AddressSend(state.TransferID)",
		"p.scopeStates.sendFlights.Do(key,", "p.readRGB11AddressBatch(transferID)", "for _, item := range batch", "!rgbDeliveryReady(item)",
		"p.broadcastRGB11PendingBatch(batch, nil)", "result, err := p.continueRGB11AddressSend(transferID)")
	requireSteps("Direct delivery", readRepoTextForTest(t, "rgb11_dkvs.go"), "func rgbDeliveryReady(", "item.State.TransportMode == RGB11AddressTransport", "item.State.RelayDurability == rgbMailboxCommitted")
	requireSteps("broadcast", readRepoTextForTest(t, "rgb11_broadcast.go"), "func (p *rgb11Manager) broadcastRGB11PendingBatch(",
		"pending.State.Status = rgb11StatusBroadcastAttempted", "CompareAndSavePendingTransferStates(intentBaseline, pendingList)",
		"p.expectedRGB11TransactionStatus(first)", "if !visible && broadcastErr == nil", "p.evidence.Broadcast(first.SignedTx)")
}

func TestRGB11GenericReservationWASMViewDoesNotExposeInternalJSON(t *testing.T) {
	wasm := readRepoTextForTest(t, "../wasm/main.go")
	for _, want := range []string{
		"resv.GetType() != wallet.RESV_TYPE_RGB11",
		"RGB11 reservation details are exposed only through the redacted RGB11 state API",
	} {
		if !strings.Contains(wasm, want) {
			t.Fatalf("generic reservation WASM view is not RGB11-redacted: missing %q", want)
		}
	}
}
