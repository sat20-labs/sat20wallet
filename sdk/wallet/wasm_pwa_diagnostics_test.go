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
	wallet := readRepoTextForTest(t, "account_pwa.go")

	if strings.Contains(sdk, "pendingStorageAuthorization") {
		t.Fatal("PWA must not own pending account storage authorization state")
	}
	if strings.Contains(sdk, "walletStorage") || strings.Contains(sdk, "localStorage") {
		t.Fatal("unfinished storage authorization must not be persisted by the PWA SDK")
	}
	if !strings.Contains(page, "resumePendingStorageAuthorization()") ||
		!strings.Contains(page, "仅复用现有 AUTOPAY 授权（不充值）") {
		t.Fatal("account setup page does not expose SDK resume/reuse behavior")
	}
	for _, want := range []string{
		"PendingAccountStorageAuthorization()",
		"CancelPendingAccountStorageAuthorization()",
		"ReusePaidAccountStorage",
	} {
		if !strings.Contains(wasm+wallet, want) {
			t.Fatalf("SDK-owned account storage support missing %q", want)
		}
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
	facade := readRepoTextForTest(t, "../../pwa/utils/rgb11Address.ts")
	dkvs := readRepoTextForTest(t, "rgb11_dkvs.go")
	reservations := readRepoTextForTest(t, "rgb11_transfer_reservation.go")
	broadcast := readRepoTextForTest(t, "rgb11_broadcast.go")

	for _, forbidden := range []string{
		"directAutoTried", "canResumeDirect", "directAttemptKey",
		"needsDirectResume", "resumeRGB11Task(task, true)", "beforeDispatch",
	} {
		if strings.Contains(tabs+facade, forbidden) {
			t.Fatalf("PWA still owns Direct lifecycle state %q", forbidden)
		}
	}
	for _, want := range []string{
		"resumeReadyRGB11AddressTransfers()",
		"BroadcastRGB11AddressTransfer(pending.State.TransferID)",
		"owner.SubscribeDKVSPrefix(mailboxTarget)",
	} {
		if !strings.Contains(dkvs, want) {
			t.Fatalf("SDK Direct recovery path missing %q", want)
		}
	}
	if !strings.Contains(reservations, "State         *rgb11wallet.TransferState") ||
		!strings.Contains(broadcast, "rgb11StatusBroadcastAttempted") {
		t.Fatal("Direct durable lifecycle is not anchored in RGB11 reservation/broadcast state")
	}
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
