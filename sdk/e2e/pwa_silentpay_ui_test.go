//go:build pwa

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

func TestSPSilentPayBrowserE2E(t *testing.T) {
	f := newSPManagerRegtest(t)
	sender, receiver := f.manager(t, minerMnemonic), f.manager(t, coreMnemonic)
	address, err := receiver.EnableSilentPay(receiver.GetWallet().GetId(), 0)
	require.NoError(t, err)
	f.fund(t, sender.GetWallet().GetAddress(), 1000000)
	_, err = sender.SendSilentPayment(address, 5000, 2)
	require.NoError(t, err, "real SP payment is a prerequisite for failure/preservation cases")
	var mining string
	require.NoError(t, f.miner.call("getnewaddress", nil, &mining))
	require.NoError(t, f.core.call("generatetoaddress", []any{1, mining}, nil))
	require.NoError(t, f.sync())
	actor := func(m *wallet.Manager, mnemonic string) map[string]string {
		return map[string]string{"mnemonic": mnemonic, "password": "sp-browser-test-only", "address": m.GetWallet().GetAddress()}
	}
	sp := map[string]any{"sender": actor(sender, minerMnemonic), "receiver": actor(receiver, coreMnemonic), "address": address, "amount": 2000}
	sender.Close()
	receiver.Close()
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	sdk := filepath.Dir(filepath.Dir(source))
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	wasm, runtimeJS := buildPWAWalletRuntime(t, ctx, sdk)
	fixture := map[string]any{"config": f.cfg, "silentPay": sp, "wasmPath": wasm, "wasmRuntimePath": runtimeJS,
		"browserPort": sdkPWARegtestBrowserPort(t)}
	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	dir := testCaseDirectory(t)
	fixturePath := filepath.Join(dir, "sp-fixture.json")
	require.NoError(t, os.WriteFile(fixturePath, raw, 0600))
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	cases := []string{
		"SP PWA: home receive exposes the private receiving method",
		"SP PWA: enable QR clipboard and reload preserve the same subaccount",
		"SP PWA: reviewed BTC send confirms once and receiver refresh discovers exact funds",
		"SP PWA: late scan cannot overwrite another selected subaccount",
		"SP PWA: network boundary rejects a public switch during a delayed scan",
		"SP PWA: locking during a delayed scan hides private receive data",
		"SP PWA: interrupted refresh preserves verified funds and retries successfully",
		"SP PWA: invalid SP checksum is rejected without broadcasting",
		"SP PWA: cancelling SP confirmation never broadcasts",
	}
	args := append([]string{"scripts/verify/account-management-e2e.mjs", "--pos-fixture", fixturePath, "--wallet-cases"}, cases...)
	logDir := os.Getenv("SAT20_SP_EVIDENCE_DIR")
	if logDir == "" {
		logDir = testCaseDirectory(t)
	}
	require.NoError(t, os.MkdirAll(logDir, 0700))
	log, err := os.OpenFile(filepath.Join(logDir, "sp-pwa.jsonl"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	require.NoError(t, err)
	command := exec.CommandContext(ctx, node, args...)
	command.Dir = filepath.Join(sdk, "..", "pwa")
	command.Stdout, command.Stderr = log, log
	err = command.Run()
	require.NoError(t, log.Close())
	t.Logf("SP browser evidence: %s", log.Name())
	require.NoError(t, err, "SP browser production failures are retained in %s", log.Name())
}
