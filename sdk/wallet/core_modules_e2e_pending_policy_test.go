package wallet

import (
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type coreStableReference struct {
	view coreRGBView
	payloadHashes map[string]string
}

func coreRemoteStableRGB(t *testing.T, manager *Manager, cfg coreE2EConfig) map[string]string {
	t.Helper()
	root, err := manager.accountManagementRootWallet()
	coreRequire(t, "stable reference root", err)
	stateKey, err := manager.accountManagedStateKey(root)
	coreRequire(t, "stable reference state key", err)
	blobKey, err := manager.accountManagedDataBlobKey(root)
	coreRequire(t, "stable reference blob key", err)
	client := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	stateRecord, err := client.GetRecordDirect(stateKey)
	coreRequire(t, "read authoritative stable state", err)
	blobRecord, err := client.GetRecordDirect(blobKey)
	coreRequire(t, "read authoritative stable bundle", err)
	id, err := manager.RootAccountID()
	coreRequire(t, "stable reference account ID", err)
	state, err := account.OpenManagedState(manager.accountSecret, id, stateRecord.Value)
	coreRequire(t, "verify stable managed state", err)
	blob, err := DecodeDKVSBlobValue(blobRecord.Value)
	coreRequire(t, "decode stable blob", err)
	bundle, err := account.OpenManagedDataBundle(manager.accountSecret, id, blob.Data)
	coreRequire(t, "verify stable bundle", err)
	// ManagedState.DataHash commits the bundle content, not its revision.
	// Revision is a separate part of the authenticated cross-record reference.
	hash, err := accountManagedDataContentHash(bundle.Items)
	coreRequire(t, "hash stable bundle content", err)
	coreAssert(t, state.DataRevision == bundle.Revision && state.DataHash == hash,
		"stable state and bundle are from different commits")
	result := make(map[string]string)
	for _, item := range bundle.Items {
		if item.Provider != rgb11AccountManagedProviderID { continue }
		// The stable format itself rejects invoice secrets, signed transactions
		// and unfinished lifecycle records. Do not expand it to satisfy a test.
		_, err := rgb11wallet.DecodeRecoveryPackage(item.Payload)
		coreRequire(t, "stable RGB payload respects the minimal format", err)
		result[item.Scope] = coreDigest(item.Payload)
	}
	return result
}

func coreCaptureStableReference(t *testing.T, source *Manager, cfg coreE2EConfig) coreStableReference {
	t.Helper()
	return coreStableReference{view: coreRGBSemantic(t, source), payloadHashes: coreRemoteStableRGB(t, source, cfg)}
}

// Incomplete RGB transfers are intentionally NOT cross-device continuations.
// This checkpoint checks real local persistence and the last committed stable
// backup separately. It never counts recovery of the old balance as proof that
// another device may spend concurrently with the original business writer.
func corePendingCheckpoint(t *testing.T, name string, source *Manager, cfg coreE2EConfig,
	chain *coreE2EChain, material *coreRecoveryMaterial, baseline coreStableReference) {

	t.Helper()
	t.Run(name, func(t *testing.T) {
		beforeBroadcasts := chain.broadcastCount()
		before := coreRGBSemantic(t, source)
		coreAssert(t, len(before.Tasks) != 0 || len(before.Invoices) != 0,
			"pending checkpoint did not reach a real local transaction/invoice")
		// Recreate the RGB module over the SAME local DB, without importing a
		// snapshot or using a copied DB. The remaining flow uses this new module.
		if old := source.rgbManager.scopeStates; old != nil { old.stopReconciliations() }
		reopened, err := newRGB11Manager(source, source.db, source.utxoLockerL1, chain)
		coreRequire(t, "reopen local RGB module", err)
		reopened.scopeStates.stopReconciliations()
		source.rgbManager = reopened
		coreRequire(t, "select reopened local scope", reopened.selectRGB11Scope())
		coreRequire(t, "rebuild local transaction locks", reopened.rebuildRGB11Locks())
		after := coreRGBSemantic(t, source)
		coreAssert(t, coreDigest(before) == coreDigest(after),
			"local RGB reopen lost invoice, task, proof or spending restriction")
		coreVerifyPaidRecords(t, source, cfg)
		coreAssert(t, coreDigest(coreRemoteStableRGB(t, source, cfg)) == coreDigest(baseline.payloadHashes),
			"incomplete transition overwrote the last stable ownership backup")

		// Passive independent recovery is a stable-snapshot observer only. Do
		// not run a send/resume command or a second account writer on this root.
		target := coreRestoreE2E(t, cfg, chain, material)
		coreAssert(t, coreDigest(coreCanonicalCatalog(source)) == coreDigest(coreCanonicalCatalog(target)),
			"stable recovery lost the wallet/account catalog")
		view := coreRGBSemantic(t, target)
		coreAssert(t, coreDigest(view.Assets) == coreDigest(baseline.view.Assets) &&
			coreDigest(view.Proofs) == coreDigest(baseline.view.Proofs),
			"independent recovery differs from the last stable ownership snapshot")
		coreAssert(t, chain.broadcastCount() == beforeBroadcasts,
			"local reopen or passive stable recovery unexpectedly broadcast a transaction")
	})
}
