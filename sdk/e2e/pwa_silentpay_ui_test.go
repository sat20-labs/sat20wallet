//go:build pwa

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// The browser has already shown the aggregate and checked both outpoints.
// Sweep those exact outputs through the native SDK, where spending is exposed.
func spSweepSharedPayments(t *testing.T, receiver *wallet.Manager, actor map[string]string,
	destination, miningAddress, logPath string, mine func(string, int)) {
	t.Helper()
	log, err := os.ReadFile(logPath)
	require.NoError(t, err)
	var receipt struct {
		Baseline int64
		Payments []struct {
			TxID   string
			Amount int64
		}
	}
	for _, line := range bytes.Split(log, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte(`{"event":"sp-multiple-payments",`)) {
			require.Empty(t, receipt.Payments, "expected one receipt for the pair")
			require.NoError(t, json.Unmarshal(line, &receipt))
		}
	}
	require.Len(t, receipt.Payments, 2)

	ctx := t.Context()
	receiver.Start()
	defer receiver.Stop()
	_, err = receiver.RecoverAccountManagementFromRootMnemonic(ctx, actor["mnemonic"], actor["password"])
	require.NoError(t, err)
	requireE2EAccountWrite(t, receiver)
	require.Equal(t, actor["address"], receiver.GetWallet().GetAddress())
	id, account := receiver.GetCurrentWalletId(), receiver.GetCurrentAccountId()
	require.Zero(t, account)
	_, err = receiver.EnableSilentPay(id, account)
	require.NoError(t, err)
	_, err = receiver.SyncSilentPayLive(ctx)
	require.NoError(t, err)
	before, err := receiver.LastSyncedSilentPay(id, account)
	require.NoError(t, err)
	require.True(t, before.Complete)
	require.Equal(t, receipt.Baseline+receipt.Payments[0].Amount+receipt.Payments[1].Amount, before.Balance)

	const feeRate = int64(2)
	sweeps := make([]string, 0, len(receipt.Payments))
	for _, payment := range receipt.Payments {
		value, _, unspent := bitcoinUnspentAt(t, payment.TxID, 0)
		require.True(t, unspent)
		require.Equal(t, payment.Amount, value)
		outpoint := fmt.Sprintf("%s:0", payment.TxID)
		txid, err := receiver.SendSilentPaySweep(ctx, id, account, outpoint, destination, feeRate)
		require.NoError(t, err, "spend the confirmed SP output %s", outpoint)
		require.Len(t, txid, 64)
		sweeps = append(sweeps, txid)
	}
	var pending []string
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &pending))
	require.ElementsMatch(t, sweeps, pending)
	var tip int
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getblockcount"), &tip))
	// The first block confirms both sweeps; later blocks advance SP's durable view.
	mine(miningAddress, 1+2*72)
	destScript, err := wallet.GetPkScriptFromAddress(destination)
	require.NoError(t, err)
	for i, txid := range sweeps {
		payment := receipt.Payments[i]
		raw := strings.TrimSpace(string(realRegtestBitcoinCLI(t, "getrawtransaction", txid)))
		tx, err := wallet.DecodeMsgTx(raw)
		require.NoError(t, err)
		require.Len(t, tx.TxIn, 1)
		require.Equal(t, payment.TxID+":0", tx.TxIn[0].PreviousOutPoint.String())
		require.Len(t, tx.TxOut, 1)
		value, script, unspent := bitcoinUnspentAt(t, txid, 0)
		require.True(t, unspent, "ordinary BTC destination must receive the confirmed sweep")
		require.Equal(t, destScript, script)
		require.Equal(t, payment.Amount-feeRate*wallet.GetTxVirtualSize2(tx), value)
		_, _, unspent = bitcoinUnspentAt(t, payment.TxID, 0)
		require.False(t, unspent, "the selected SP outpoint must be spent")
		t.Logf("confirmed SP sweep: %s:0 (%d sats) -> %s:0 (%d sats)", payment.TxID, payment.Amount, txid, value)
	}
	_, err = receiver.SyncSilentPayLive(ctx)
	require.NoError(t, err)
	after, err := receiver.LastSyncedSilentPay(id, account)
	require.NoError(t, err)
	require.True(t, after.Complete)
	require.Greater(t, after.Anchor.Height, tip, "SP snapshot must include the sweep block")
	require.Equal(t, receipt.Baseline, after.Balance, "only the two swept payments leave the private balance")
}

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
