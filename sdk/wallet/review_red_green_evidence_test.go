package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This opt-in evidence test uses Go's compile overlay, never git checkout or
// worktree rewrites. Each negative control reinstates the specific pre-fix
// behavior identified in the review; it is not presented as a historic commit.
// All business assertions run unchanged against the negative control and the
// current implementation. Compile failures, timeouts and missing tests do not
// count as successful red evidence. Ordinary regression tests remain enabled
// in go test ./...; only this deliberately expensive control experiment is opt-in.
type reviewEvidenceRun struct {
	Expected string `json:"expected"`
	ExitCode int `json:"exit_code"`
	Seconds float64 `json:"seconds"`
	TestsPassed int `json:"tests_passed"`
	TestsFailed int `json:"tests_failed"`
	AssertionEvidence []string `json:"assertion_evidence,omitempty"`
	RootPassed bool `json:"root_passed"`
}

type reviewEvidenceCase struct {
	Test string `json:"test"`
	Control string `json:"control"`
	Red reviewEvidenceRun `json:"red"`
	Green reviewEvidenceRun `json:"green"`
	Verified bool `json:"verified"`
}

type reviewEvidenceReport struct {
	GeneratedAt string `json:"generated_at"`
	Scope string `json:"scope"`
	Sources map[string]string `json:"source_sha256"`
	Cases []reviewEvidenceCase `json:"cases"`
	WorktreeUnchanged bool `json:"worktree_sources_unchanged"`
}

func reviewReplaceFunction(t *testing.T, source, name, replacement string) string {
	t.Helper()
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "overlay.go", source, 0)
	if err != nil { t.Fatal(err) }
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			start, end := set.Position(function.Pos()).Offset, set.Position(function.End()).Offset
			return source[:start] + replacement + source[end:]
		}
	}
	t.Fatalf("negative control cannot locate function %s", name)
	return ""
}

func reviewReplaceIf(t *testing.T, source, condition, bodyNeedle, replacement string) string {
	t.Helper()
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "overlay.go", source, 0)
	if err != nil { t.Fatal(err) }
	start, end, count := 0, 0, 0
	ast.Inspect(file, func(node ast.Node) bool {
		statement, ok := node.(*ast.IfStmt)
		if !ok { return true }
		conditionText := source[set.Position(statement.Cond.Pos()).Offset:set.Position(statement.Cond.End()).Offset]
		body := source[set.Position(statement.Body.Pos()).Offset:set.Position(statement.Body.End()).Offset]
		if strings.Contains(conditionText, condition) && strings.Contains(body, bodyNeedle) {
			start, end = set.Position(statement.Pos()).Offset, set.Position(statement.End()).Offset
			count++
			return false
		}
		return true
	})
	if count != 1 { t.Fatalf("negative control requires one matching branch, found %d (%s)", count, condition) }
	return source[:start] + replacement + source[end:]
}

func reviewRunEvidenceChild(t *testing.T, moduleDir, testName, overlay string, markers []string) reviewEvidenceRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	args := []string{"test", "-json", "-count=1", "-timeout=90s"}
	if overlay != "" { args = append(args, "-overlay="+overlay) }
	args = append(args, "./wallet", "-run", "^"+testName+"$")
	command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
	command.Dir = moduleDir
	started := time.Now()
	output, err := command.CombinedOutput()
	result := reviewEvidenceRun{Seconds: time.Since(started).Seconds()}
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok { t.Fatalf("cannot execute regression child: %v", err) }
		result.ExitCode = exit.ExitCode()
	}
	if ctx.Err() != nil { t.Fatalf("regression child timed out; this is not red evidence: %s", testName) }
	text := string(output)
	if strings.Contains(text, "[build failed]") || strings.Contains(text, "panic: test timed out") {
		t.Fatalf("regression control failed outside the asserted behavior:\n%s", text)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for decoder.More() {
		var event struct { Action, Test, Output string }
		if err := decoder.Decode(&event); err != nil { t.Fatalf("invalid go test JSON: %v\n%s", err, text) }
		if event.Test != "" && event.Action == "pass" { result.TestsPassed++ }
		if event.Test != "" && event.Action == "fail" { result.TestsFailed++ }
		if event.Test == testName && event.Action == "pass" { result.RootPassed = true }
		if event.Test != "" {
			for _, marker := range markers {
				if strings.Contains(event.Output, marker) {
					result.AssertionEvidence = append(result.AssertionEvidence, strings.TrimSpace(event.Output))
					break
				}
			}
		}
	}
	return result
}

func TestReviewRedGreenEvidence(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), "TestReviewRedGreenEvidence") {
		t.Skip("explicit negative-control experiment; run -run '^TestReviewRedGreenEvidence$'")
	}
	walletDir, err := os.Getwd()
	if err != nil { t.Fatal(err) }
	moduleDir := filepath.Dir(walletDir)
	sources := make(map[string]string)
	report := reviewEvidenceReport{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Scope: "Synthetic wallet/network fixtures only. Negative controls reinstate reviewed pre-fix behavior in temporary Go overlays; no worktree/index rollback, live wallet or chain transaction.",
		Sources: make(map[string]string), WorktreeUnchanged: true,
	}
	for _, name := range []string{
		"account_management_state.go", "account_management_rebase.go", "rgb11_managed_operation.go",
		"account_storage_operation.go", "account_pwa.go", "rgb11_reservation_consistency.go",
		"rgb11_transfer_reservation.go", "rgb11/reservation_store.go", "rgb11/engine_store.go",
		"../wasm/account_management.go",
	} {
		raw, err := os.ReadFile(filepath.Join(walletDir, name))
		if err != nil { t.Fatal(err) }
		sources[name] = string(raw)
		digest := sha256.Sum256(raw)
		report.Sources[name] = hex.EncodeToString(digest[:])
	}
	defer func() {
		for name, before := range sources {
			after, err := os.ReadFile(filepath.Join(walletDir, name))
			if err != nil || string(after) != before {
				report.WorktreeUnchanged = false
				t.Errorf("production source changed during evidence collection: %s", name)
			}
		}
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil { t.Error(err); return }
		directory := filepath.Join(moduleDir, "review-evidence")
		if err := os.MkdirAll(directory, 0700); err != nil { t.Error(err); return }
		if err := os.WriteFile(filepath.Join(directory, "red-green-20260930.json"), append(raw, '\n'), 0600); err != nil { t.Error(err) }
	}()

	cases := []struct {
		test, file, control string
		markers []string
		patch func(*testing.T, string) string
	}{
		{
			test: "TestReviewAccountMergeAppliesRemoteIncrement", file: "account_management_state.go",
			control: "Remove the pre-publication local rebase; retain the reviewed own-ACK baseline-only finalization.",
			markers: []string{"remote wallet is absent", "omitted the remote subaccount", "provider review.alpha was not applied"},
			patch: func(t *testing.T, source string) string {
				return reviewReplaceIf(t, source, "remoteBaselineChanged", "applyAccountManagedRebaseForSync", "")
			},
		},
		{
			test: "TestReviewRGB11BaselineRechecksOperationPreconditions", file: "rgb11_managed_operation.go",
			control: "Restore the reviewed sync-then-execute sequence without reselecting the source or rechecking imported active transitions.",
			markers: []string{"source changed during baseline sync but operation executed", "new operation crossed the imported active-transition barrier"},
			patch: func(t *testing.T, source string) string {
				for _, line := range []string{
					"\t\trequestedScope := manager.rgb11ScopeKey()\n",
					"\t\trequestedFingerprint := walletFingerprint(manager.wallet)\n",
					"\t\trequestedNetwork := _chain\n",
				} {
					if strings.Count(source, line) != 1 { t.Fatalf("cannot identify captured source field: %q", line) }
					source = strings.Replace(source, line, "", 1)
				}
				return reviewReplaceIf(t, source, "!activeBefore", "requestedScope", `if !activeBefore {
					if err := p.syncAccountManagementState(ctx, 0, false); err != nil {
						return fmt.Errorf("confirm RGB11 account-managed baseline: %w", err)
					}
				}`)
			},
		},
		{
			test: "TestReviewStorageAuthorizationOperationOwnership", file: "account_storage_operation.go",
			control: "Apply the reviewed WASM peek/use/unconditional-cancel semantics to its replacement shared SDK entry point; no atomic claim or conditional consumption.",
			markers: []string{"second operation claimed", "cancelled owner reported success", "old completion erased", "silently reused for guardian setup"},
			patch: func(t *testing.T, source string) string {
				return reviewReplaceFunction(t, source, "UseAccountStorageAuthorization", `func (p *Manager) UseAccountStorageAuthorization(purpose AccountStoragePurpose,
					operation func(*AccountStorageAuthorization) error) error {
					if operation == nil || (purpose != AccountStoragePurposeRecovery && purpose != AccountStoragePurposeGuardian) {
						return fmt.Errorf("invalid account storage operation")
					}
					value, err := p.PendingAccountStorageAuthorization()
					if err != nil { return err }
					if err := operation(value); err != nil { return err }
					p.CancelPendingAccountStorageAuthorization()
					return nil
				}`)
			},
		},
		{
			test: "TestReviewStorageCancelInvalidatesInflightConfirmation", file: "account_pwa.go",
			control: "Create the authorization slot only after the network policy response, reproducing late confirmation after cancellation.",
			markers: []string{"cancelled confirmation must report invalid authorization", "late confirmation resurrected a cancelled authorization"},
			patch: func(t *testing.T, source string) string {
				return reviewReplaceFunction(t, source, "ConfirmAccountStorage", `func (p *Manager) ConfirmAccountStorage(optionID string, recordCount uint64) (*AccountStorageAuthorization, error) {
					if p == nil { return nil, fmt.Errorf("wallet is not created/unlocked") }
					mode := strings.ToLower(strings.TrimSpace(optionID))
					p.mutex.RLock()
					downgrade := p.accountProfile != nil && p.accountProfile.StorageMode == AccountStoragePaid && mode == AccountStorageTemporary
					p.mutex.RUnlock()
					if downgrade { return nil, ErrAccountStorageModeDowngrade }
					location, err := p.AccountIndexerLocation()
					if err != nil { return nil, err }
					store, err := p.accountDKVSStore()
					if err != nil { return nil, err }
					var authorization *AccountStorageAuthorization
					switch mode {
					case AccountStorageTemporary:
						policy, height, _, err := store.ConfigWithVerificationHeight()
						if err != nil { return nil, err }
						if policy == nil || !policy.Enabled || policy.MaxTTL == 0 { return nil, fmt.Errorf("temporary DKVS cache unavailable") }
						authorization = &AccountStorageAuthorization{
							Mode: AccountStorageTemporary,
							RecordOptions: dkvsindexer.RecordOptions{Seq: 1, TTL: policy.MaxTTL},
							Summary: AccountStorageOption{ID: AccountStorageTemporary, Mode: AccountStorageTemporary,
								Available: true, TTLBlocks: policy.MaxTTL, EstimatedExpiryHeight: estimatedDKVSExpiryHeight(height, policy.MaxTTL)},
							Location: location, Policy: policy,
						}
					case AccountStoragePaid:
						authorization, err = p.confirmPaidAccountStorage(location, recordCount)
						if err != nil { return nil, err }
					default:
						return nil, fmt.Errorf("unsupported account storage option %q", optionID)
					}
					return p.rememberAccountStorageAuthorization(authorization)
				}`)
			},
		},
		{
			test: "TestReviewRGB11ReadDoesNotMixAtomicInvoiceGenerations", file: "rgb11_reservation_consistency.go",
			control: "Restore the absence of a coherent-read/local-commit gate; retain the actual atomic DB batch and strict missing-reservation checks.",
			markers: []string{"valid atomic invoice commit was reported as inconsistent"},
			patch: func(t *testing.T, source string) string {
				source = reviewReplaceFunction(t, source, "LockReservationCommit", `func (p *rgb11Manager) LockReservationCommit() func() { return func() {} }`)
				return reviewReplaceFunction(t, source, "beginRGB11ReservationRead", `func (p *rgb11Manager) beginRGB11ReservationRead() func() { return func() {} }`)
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.test, func(t *testing.T) {
			entry := reviewEvidenceCase{Test: testCase.test, Control: testCase.control}
			defer func() { report.Cases = append(report.Cases, entry) }()
			directory := t.TempDir()
			modified := testCase.patch(t, sources[testCase.file])
			if modified == sources[testCase.file] { t.Fatal("negative control did not modify compilation input") }
			backing := filepath.Join(directory, "control.go")
			if err := os.WriteFile(backing, []byte(modified), 0600); err != nil { t.Fatal(err) }
			overlayBytes, err := json.Marshal(map[string]any{"Replace": map[string]string{filepath.Join(walletDir, testCase.file): backing}})
			if err != nil { t.Fatal(err) }
			overlay := filepath.Join(directory, "overlay.json")
			if err := os.WriteFile(overlay, overlayBytes, 0600); err != nil { t.Fatal(err) }
			entry.Red = reviewRunEvidenceChild(t, moduleDir, testCase.test, overlay, testCase.markers)
			entry.Red.Expected = "behavioral failure"
			if entry.Red.ExitCode == 0 || entry.Red.TestsFailed == 0 || len(entry.Red.AssertionEvidence) == 0 {
				t.Fatalf("control did not demonstrate the reviewed failure: %+v", entry.Red)
			}
			entry.Green = reviewRunEvidenceChild(t, moduleDir, testCase.test, "", testCase.markers)
			entry.Green.Expected = "pass"
			if entry.Green.ExitCode != 0 || !entry.Green.RootPassed || entry.Green.TestsFailed != 0 {
				t.Fatalf("current implementation did not pass identical assertions: %+v", entry.Green)
			}
			entry.Verified = true
			t.Logf("RED behavioral assertions=%d; GREEN tests=%d", len(entry.Red.AssertionEvidence), entry.Green.TestsPassed)
		})
	}
	if len(report.Cases) != len(cases) { t.Error(fmt.Errorf("incomplete red/green case execution")) }
}
