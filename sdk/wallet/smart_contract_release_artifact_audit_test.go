package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// An explicit release artifact audit, not a production build or deployment.
// The fresh WASM is created only under t.TempDir and never copied to the apps.
func TestSmartContractReleaseArtifactAudit(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), "TestSmartContractReleaseArtifactAudit") {
		t.Skip("explicit release artifact audit")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok { t.Fatal("locate SDK") }
	sdk := filepath.Dir(filepath.Dir(source))
	root := filepath.Dir(filepath.Dir(sdk))
	report := map[string]any{"started_at": time.Now().Format(time.RFC3339)}
	read := func(relative string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil { t.Fatal(err) }
		return data
	}
	hash := func(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
	beforeMod, beforeSum := hash(read("sat20wallet/sdk/go.mod")), hash(read("sat20wallet/sdk/go.sum"))
	inspect := func(data []byte) map[string]any {
		return map[string]any{"bytes": len(data), "sha256": hash(data),
			"contains_reserved_asset_rejection": bytes.Contains(data, []byte("satoshi (::) funding must use value, not Assets")),
			"contains_default_multi_asset_rejection": bytes.Contains(data, []byte("default invoke supports only one business asset")),
			"contains_funding_validation_symbol": bytes.Contains(data, []byte("wallet.validateContractFundingRequest"))}
	}
	artifacts := map[string]any{}
	for _, path := range []string{"sat20wallet/client/public/wasm/sat20wallet.wasm", "sat20wallet/pwa/public/wasm/sat20wallet.wasm"} {
		artifacts[path] = inspect(read(path))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fresh := filepath.Join(t.TempDir(), "release-audit.wasm")
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", fresh, "./wasm")
	cmd.Dir = sdk
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GOOS=") && !strings.HasPrefix(item, "GOARCH=") { cmd.Env = append(cmd.Env, item) }
	}
	cmd.Env = append(cmd.Env, "GOOS=js", "GOARCH=wasm")
	output, err := cmd.CombinedOutput()
	if err != nil { t.Fatalf("temporary WASM compilation failed: %v; %.1200s", err, output) }
	data, err := os.ReadFile(fresh)
	if err != nil { t.Fatal(err) }
	artifacts["temporary_build_from_current_sources"] = inspect(data)
	report["artifacts"] = artifacts
	report["manifest_text"] = string(read("sat20wallet/pwa/public/integrity-manifest.json"))
	report["version_text"] = string(read("sat20wallet/pwa/public/version.json"))

	type excerpt struct { Path string; SHA256 string; Lines []string }
	var excerpts []excerpt
	targets := []struct { path, needle string; before, after int }{
		{"sat20wallet/sdk/wallet/interface_contract_unified.go", "func (p *Manager) EstimateEVMDeployContract", 0, 50},
		{"sat20wallet/sdk/wallet/interface_contract_unified.go", "func (p *Manager) evmGasAssetAmount", 0, 28},
		{"satoshinet/contract/evm/backend.go", "if !ready {", 3, 5},
		{"satoshinet/mining/mining.go", "g.policy.ContractResultBuilder(ContractBuildRequest", 2, 12},
		{"satoshinet/blockchain/validate.go", "totalInTxAssets.Merge(utxoTxAssets)", 3, 6},
		{"satoshinet/blockchain/validate.go", "totalInTxAssets.Split(txOut.Assets)", 3, 7},
		{"transcend/stp/contract_invoke.go", "p.SaveReservationWithLock(resv)", 3, 5},
		{"transcend/stp/contract_deploy.go", "func (p *STPManager) handleDeployContractStarted", 0, 32},
	}
	for _, target := range targets {
		data := read(target.path)
		lines := strings.Split(string(data), "\n")
		count := 0
		for i, line := range lines {
			if !strings.Contains(line, target.needle) { continue }
			lo, hi := i-target.before, i+target.after
			if lo < 0 { lo = 0 }; if hi >= len(lines) { hi = len(lines)-1 }
			entry := excerpt{Path: target.path, SHA256: hash(data)}
			for j := lo; j <= hi; j++ { entry.Lines = append(entry.Lines, fmt.Sprintf("%d: %s", j+1, lines[j])) }
			excerpts = append(excerpts, entry)
			count++; if count >= 3 { break }
		}
	}
	report["source_excerpts"] = excerpts
	report["dependency_files_unchanged"] = beforeMod == hash(read("sat20wallet/sdk/go.mod")) && beforeSum == hash(read("sat20wallet/sdk/go.sum"))
	report["finished_at"] = time.Now().Format(time.RFC3339)
	report["boundary"] = "byte markers and hashes are artifact inspection evidence, not a browser execution test or a full reproducible-build attestation"
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil { t.Fatal(err) }
	path := filepath.Join(sdk, "review-evidence", "smart-contract-release-artifact-audit.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil { t.Fatal(err) }
	if report["dependency_files_unchanged"] != true { t.Fatal("dependency files changed unexpectedly") }
}
