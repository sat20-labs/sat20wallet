//go:build pwa

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Browser/WASM conversion and actual-page assertions stay opt-in. Native
// Manager journeys are the default SDK tests and need no browser fixtures.
func TestPWAUIBridge(t *testing.T) {
	runSDKUICases(t, []sdkUICase{
		{"RGBManual", "SDK UI: RGB manual transfer inflation cancellation and reload"},
		{"RGBDirectOffline", "SDK UI: Direct offline receive ignores another spent output"},
		{"RGBBlindUDA", "SDK UI: RGB blind receive and UDA lifecycle"},
		{"RGBProxy", "SDK UI: RGB proxy witness blind and resume"},
		{"DIDHistory", "SDK UI: DID availability submission and history"},
		{"TwoFactorRecovery", "SDK UI: two-factor backup cancel reload and cold recovery"},
		{"WalletReimportPending", "SDK UI: wallet reimport while deletion pending"},
		{"WalletReimportConfirmed", "SDK UI: wallet reimport after deletion confirmed"},
	})
}

func TestPWAPaidUIBridge(t *testing.T) {
	runSDKUICases(t, []sdkUICase{{"PaidTwoFactorRecovery", "SDK UI: paid two-factor recovery restore and renewal"}})
	// Verify the real Core's signed record after renewal and cold recovery,
	// independently of the SDK account view used by the browser.
	var observed struct {
		AccountID   string `json:"account_id"`
		ModuleValue string `json:"module_value"`
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("SAT20_REAL_REGTEST_RUNTIME"), "paid-policy.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &observed))
	var cfg sdkcommon.Config
	data, err = os.ReadFile(os.Getenv("SAT20_ACCOUNT_UI_CONFIG"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &cfg))
	require.NotNil(t, cfg.IndexerL2)
	endpoint := cfg.IndexerL2
	client := wallet.NewSatsNetDKVSClient(endpoint.Scheme, endpoint.Host, endpoint.Proxy,
		&wallet.NetClient{Client: &http.Client{Timeout: 30 * time.Second}})
	key, err := dkvsindexer.AccountPersonalKey(observed.AccountID, "account/state")
	require.NoError(t, err)
	record, err := client.GetRecordDirect(key)
	require.NoError(t, err)
	proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
	require.NoError(t, err)
	require.Equal(t, dkvsindexer.FeeModeAutopay, proof.Mode)
	require.Zero(t, record.TTL)
	require.Equal(t, observed.ModuleValue, base64.StdEncoding.EncodeToString(record.Value))
}

func TestPWAPartialRestore(t *testing.T) {
	// Inject only a module import refusal during cold restoration. Production
	// authentication, persistent error handling, WASM and PWA stay in the path.
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	original := filepath.Join(filepath.Dir(filepath.Dir(source)), "wallet", "account_module_sync.go")
	data, err := os.ReadFile(original)
	require.NoError(t, err)
	before := "if err := p.importAccountPayload(entry, catalog, payload); err != nil {"
	require.Equal(t, 1, strings.Count(string(data), before))
	// Refuse the same module after its durable Restoring marker on both the
	// initial restore and startup retry. All error persistence remains real.
	// The browser also calls L1 send, BRC20 deploy and RGB refresh, asserting
	// synchronization errors and no broadcast while local views/logs work.
	after := `if entry.Provider == "rgb11" {
    return errors.New("E2E injected RGB module import failure")
   }
   ` + before
	candidate := []byte(strings.Replace(string(data), before, after, 1))
	dir := t.TempDir()
	replacement := filepath.Join(dir, "account_module_sync.go")
	require.NoError(t, os.WriteFile(replacement, candidate, 0600))
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{original: replacement}})
	require.NoError(t, err)
	overlayPath := filepath.Join(dir, "restore-fault.json")
	require.NoError(t, os.WriteFile(overlayPath, overlay, 0600))
	require.NotContains(t, os.Getenv("GOFLAGS"), "-overlay", "run this failure case without another source overlay")
	t.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -overlay="+overlayPath))
	t.Setenv("SAT20_UI_RESTORE_FAULT", "1")
	t.Logf("module import failure build: original=%s original_sha256=%x candidate_sha256=%x", original, sha256.Sum256(data), sha256.Sum256(candidate))
	runSDKUICases(t, []sdkUICase{{"PartialRecovery", "SDK UI: partial recovery stays incomplete"}})
}

func runSDKUICases(t *testing.T, cases []sdkUICase) {
	t.Helper()
	configPath := os.Getenv("SAT20_ACCOUNT_UI_CONFIG")
	fixturePath := os.Getenv("SAT20_ACCOUNT_UI_FIXTURE")
	require.NotEmpty(t, configPath, "provide the real regtest config")
	require.NotEmpty(t, fixturePath, "provide the private account actor fixture")
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	sdk := filepath.Dir(filepath.Dir(source))
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	wasm, js := buildPWAWalletRuntime(t, ctx, sdk, 32)
	data, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	var fixture map[string]any
	require.NoError(t, json.Unmarshal(data, &fixture))
	prepared, ok := fixture["walletFixture"].(map[string]any)
	require.True(t, ok)
	prepared["wasmPath"], prepared["wasmRuntimePath"] = wasm, js
	prepared["uiBusinessOnly"] = true
	evidence := os.Getenv("SAT20_REAL_REGTEST_RUNTIME")
	require.NotEmpty(t, evidence)
	require.NoError(t, os.MkdirAll(evidence, 0700))
	setupSDKUITransport(t, prepared, configPath, cases)
	actual := writeSDKPWARegtestPrivateJSON(t, evidence, "account-ui-fixture.json", fixture)
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			runSDKPWARegtestBrowserGroup(t, filepath.Join(sdk, "..", "pwa"), configPath,
				actual, evidence, item.name, []string{item.journey}, "--sdk-wasm")
		})
	}
}
