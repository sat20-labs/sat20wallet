package wallet

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// This opt-in test capture never imports or repairs the captured browser DB.
// Raw RGB records stay in a private file; summaries contain hashes and booleans.
type coreRGBBrowserDiagnostic struct {
	dir       string
	baselines []coreRGBBrowserBaseline
	stages    []any
}
type coreRGBBrowserBaseline struct {
	scope      string
	snapshot   *RGB11WalletSnapshot
	resolver   *rgb11Manager
	canonical  map[string]string
	identities map[string]string
	original   map[string]string
}

func newCoreRGBBrowserDiagnostic(t *testing.T) *coreRGBBrowserDiagnostic {
	t.Helper()
	parent := os.Getenv("SAT20_RGB_DIAGNOSTIC_DIR")
	if parent == "" {
		return nil
	}
	coreAssert(t, filepath.IsAbs(parent), "RGB diagnostic directory must be absolute")
	coreRequire(t, "create RGB diagnostic directory", os.MkdirAll(parent, 0700))
	dir, err := os.MkdirTemp(parent, "cold-hash-")
	coreRequire(t, "create private RGB diagnostic capture", err)
	d := &coreRGBBrowserDiagnostic{dir: dir}
	flags, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "env", "GOFLAGS").Output()
	coreRequire(t, "read effective diagnostic GOFLAGS", err)
	wasmPath := os.Getenv("SAT20_PWA_E2E_WASM")
	wasm, err := os.ReadFile(wasmPath)
	coreRequire(t, "hash diagnostic WASM", err)
	d.write(t, "build.json", map[string]any{"effective_goflags": string(flags), "wasm_path": wasmPath,
		"wasm_sha256": coreRGBDiagnosticHash(wasm), "native_goos": runtime.GOOS, "native_goarch": runtime.GOARCH,
		"go_version": runtime.Version()})
	return d
}
func (d *coreRGBBrowserDiagnostic) browserPath() string {
	if d == nil {
		return ""
	}
	return filepath.Join(d.dir, "browser-rgb.json")
}
func (d *coreRGBBrowserDiagnostic) write(t *testing.T, name string, value any) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	coreRequire(t, "encode RGB diagnostic artifact", err)
	coreRequire(t, "write private RGB diagnostic artifact", os.WriteFile(filepath.Join(d.dir, name), encoded, 0600))
}
func coreRGBDiagnosticHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func coreRGBDiagnosticIdentity(proof *rgb11wallet.AllocationProof) string {
	// Only derived receipt/status fields are excluded from this diagnostic digest.
	// The original full-equality browser assertion remains unchanged.
	copy := *proof
	copy.ValidationHash = ""
	copy.Status = ""
	copy.Confirmations = 0
	copy.PolicyStatus = ""
	copy.PolicyReason = ""
	data, _ := json.Marshal(copy)
	return coreRGBDiagnosticHash(data)
}
func (d *coreRGBBrowserDiagnostic) capture(t *testing.T, source *Manager, state *RGB11State) {
	if d == nil {
		return
	}
	t.Helper()
	account, err := source.rgbManager.fixedRGB11ScopeAccount()
	coreRequire(t, "capture diagnostic scope", err)
	scoped, err := source.newScopedRGB11Manager(account)
	coreRequire(t, "open diagnostic scope reader", err)
	id, err := scoped.RGB11WalletID()
	coreRequire(t, "read diagnostic RGB identity", err)
	snapshot, _, err := scoped.exportRGB11WalletSnapshot(id)
	coreRequire(t, "capture diagnostic RGB snapshot", err)
	recovery, err := rgb11wallet.RecoveryPackageFromSnapshot(snapshot, 0)
	coreRequire(t, "canonicalize diagnostic baseline", err)
	receipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(recovery.ProjectionRecords)
	coreRequire(t, "read canonical receipts", err)
	base := coreRGBBrowserBaseline{scope: rgb11StorageScope(account.WalletID, account.AccountIndex), snapshot: snapshot,
		resolver: scoped, canonical: map[string]string{}, identities: map[string]string{}, original: map[string]string{}}
	for object, receipt := range receipts {
		hash, err := receipt.Hash()
		coreRequire(t, "hash canonical receipt", err)
		base.canonical[object] = hash
	}
	for _, proof := range state.Proofs {
		key := proof.OutPoint + "|" + proof.AssetName.String()
		base.identities[key] = coreRGBDiagnosticIdentity(proof)
		base.original[key] = proof.ValidationHash
	}
	d.baselines = append(d.baselines, base)
	audit, err := base.audit(snapshot, false)
	coreRequire(t, "bind original diagnostic proofs", err)
	d.stages = append(d.stages, map[string]any{"phase": "local_scopes", "scope": base.scope, "audit": audit})
	d.write(t, "summary.json", d.stages)
}

func (b *coreRGBBrowserBaseline) audit(snapshot *RGB11WalletSnapshot, resolve bool) (map[string]any, error) {
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, fmt.Errorf("proof_decode")
	}
	receipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, fmt.Errorf("receipt_decode")
	}
	result := map[string]any{"proof_count": len(proofs), "expected_proof_count": len(b.identities)}
	rows := make([]any, 0, len(proofs))
	allBound, allIdentity := true, len(proofs) == len(b.identities)
	for _, proof := range proofs {
		receipt := receipts[proof.ConsignmentHash]
		hash := ""
		validatedAt := int64(0)
		if receipt != nil {
			hash, err = receipt.Hash()
			if err != nil {
				return nil, fmt.Errorf("receipt_hash")
			}
			validatedAt = receipt.ValidatedAt
		}
		object := rgb11SnapshotObject(snapshot.ProjectionRecords, proof.ConsignmentHash)
		bound := receipt != nil && hash == proof.ValidationHash && len(object) > 0 && coreRGBDiagnosticHash(object) == proof.ConsignmentHash
		identity := b.identities[proof.OutPoint+"|"+proof.AssetName.String()] == coreRGBDiagnosticIdentity(proof)
		allBound = allBound && bound
		allIdentity = allIdentity && identity
		rows = append(rows, map[string]any{"consignment_hash": proof.ConsignmentHash, "proof_hash": proof.ValidationHash,
			"receipt_hash": hash, "validated_at": validatedAt, "canonical_hash": b.canonical[proof.ConsignmentHash],
			"expected_local_proof_hash": b.original[proof.OutPoint+"|"+proof.AssetName.String()],
			"matches_local_scopes_hash": proof.ValidationHash == b.original[proof.OutPoint+"|"+proof.AssetName.String()],
			"equals_baseline_canonical": hash != "" && hash == b.canonical[proof.ConsignmentHash], "bound": bound, "identity_equal": identity})
	}
	result["proofs"], result["all_bound"], result["all_identity_equal"] = rows, allBound, allIdentity
	if resolve {
		resolved, resolveErr := b.resolver.resolveRGB11RecoveryProjection(context.Background(), snapshot)
		result["resolver_valid"] = resolveErr == nil
		if resolveErr == nil {
			result["resolver_complete"] = resolved.complete
			result["evidence_still_current"] = resolved.checkEvidence() == nil
		}
	}
	return result, nil
}
func (d *coreRGBBrowserDiagnostic) captureRecords(t *testing.T, phase string, records map[string]string, resolve bool) {
	if d == nil {
		return
	}
	t.Helper()
	database := newMemoryKVDB()
	for key, encoded := range records {
		if !strings.HasPrefix(key, "rgb11-") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		coreRequire(t, "decode private RGB record", err)
		coreRequire(t, "stage isolated RGB diagnostic record", database.Write([]byte(key), raw))
	}
	for i := range d.baselines {
		base := &d.baselines[i]
		projection := rgb11wallet.NewProjectionStore(database, nil)
		engine := rgb11wallet.NewEngineStore(database)
		coreRequire(t, "select diagnostic projection", projection.SetScope(base.scope))
		coreRequire(t, "select diagnostic engine", engine.SetScope(base.scope))
		snapshot := *base.snapshot
		var err error
		snapshot.ProjectionRecords, err = projection.ExportSnapshot()
		failure := ""
		if err != nil {
			failure = "projection_export"
		} else {
			snapshot.EngineRecords, err = engine.ExportSnapshot()
			if err != nil {
				failure = "engine_export"
			}
		}
		var audit map[string]any
		if failure == "" {
			audit, err = base.audit(&snapshot, resolve)
			if err != nil {
				failure = err.Error()
			}
		}
		d.stages = append(d.stages, map[string]any{"phase": phase, "scope": base.scope, "audit": audit, "failure": failure})
	}
	d.write(t, "summary.json", d.stages)
}
func (d *coreRGBBrowserDiagnostic) finish(t *testing.T) {
	if d == nil {
		return
	}
	t.Helper()
	var captures []struct {
		Phase   string            `json:"phase"`
		Records map[string]string `json:"records"`
	}
	raw, err := os.ReadFile(d.browserPath())
	if err != nil {
		d.stages = append(d.stages, map[string]any{"failure": "browser_capture_missing"})
	} else if json.Unmarshal(raw, &captures) != nil {
		d.stages = append(d.stages, map[string]any{"failure": "browser_capture_decode"})
	} else {
		for _, capture := range captures {
			d.captureRecords(t, capture.Phase, capture.Records, capture.Phase == "verify-fail" || capture.Phase == "post-retry")
		}
	}
	d.write(t, "summary.json", d.stages)
	t.Logf("RGB receipt diagnostic artifacts: %s (private RGB records; original assertion unchanged)", d.dir)
}
