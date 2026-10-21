package wallet

import (
	"context"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func coreRemoteStableRGB(t *testing.T, manager *Manager, cfg coreE2EConfig) map[string]string {
	t.Helper()
	manager.mutex.RLock()
	rootInfo, err := manager.accountManagementRootWalletLocked()
	var root common.Wallet
	var profileAccountID, profileRootFingerprint string
	if err == nil && rootInfo != nil {
		root = cloneWalletAtAccountZero(rootInfo.Wallet)
	}
	if manager.accountProfile != nil {
		profileAccountID = manager.accountProfile.AccountID
		profileRootFingerprint = manager.accountProfile.RootFingerprint
	}
	secret := append([]byte(nil), manager.accountSecret...)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	coreRequire(t, "stable reference root", err)
	coreAssert(t, root != nil, "stable reference root wallet is missing")
	stateKey, err := manager.accountManagedStateKey(root)
	coreRequire(t, "stable reference state key", err)
	blobKey, err := manager.accountManagedDataBlobKey(root)
	coreRequire(t, "stable reference blob key", err)
	client := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	stateRecord, err := client.GetRecordDirect(stateKey)
	coreRequire(t, "read authoritative stable state", err)
	blobRecord, err := client.GetRecordDirect(blobKey)
	coreRequire(t, "read authoritative stable bundle", err)
	rootAccountID, err := dkvsAccountID(root)
	coreRequire(t, "stable reference root account ID", err)
	profileAccountMatchesRoot := profileAccountID == rootAccountID
	rootWalletMatchesProfile := walletFingerprint(root) == profileRootFingerprint
	if len(secret) != 32 || len(profileAccountID) != 64 ||
		!profileAccountMatchesRoot || !rootWalletMatchesProfile {
		t.Fatalf("core-e2e: stable managed state auth binding mismatch (secret_len=%d account_id_len=%d profile_account_matches_root=%t root_wallet_matches_profile=%t)",
			len(secret), len(profileAccountID), profileAccountMatchesRoot,
			rootWalletMatchesProfile)
	}
	state, err := account.OpenManagedState(secret, profileAccountID, stateRecord.Value)
	coreRequire(t, "verify stable managed state", err)
	blob, err := DecodeDKVSBlobValue(blobRecord.Value)
	coreRequire(t, "decode stable blob", err)
	bundle, err := account.OpenManagedDataBundle(secret, profileAccountID, blob.Data)
	coreRequire(t, "verify stable bundle", err)
	// ManagedState.DataHash commits the bundle content, not its revision.
	// Revision is a separate part of the authenticated cross-record reference.
	hash, err := accountManagedDataContentHash(bundle.Items)
	coreRequire(t, "hash stable bundle content", err)
	coreAssert(t, state.DataRevision == bundle.Revision && state.DataHash == hash,
		"stable state and bundle are from different commits")
	result := make(map[string]string)
	for _, item := range bundle.Items {
		if item.Provider != rgb11AccountManagedProviderID {
			continue
		}
		// The account-encrypted stable payload is the authoritative recovery
		// closure, including the private material needed to resume unfinished
		// invoice and transfer obligations.
		_, err := rgb11wallet.DecodeRecoveryPackage(item.Payload)
		coreRequire(t, "decode authoritative RGB recovery closure", err)
		result[item.Scope] = coreDigest(item.Payload)
	}
	return result
}

// Every public business phase is covered by the complete stable recovery
// closure. Reopen the local RGB engine, then recover into a distinct database
// and require the same invoice/operation/ownership/lock view. Recovery is
// observational: it must never turn a pending transaction into a broadcast.
func corePendingCheckpoint(t *testing.T, name string, source *Manager, cfg coreE2EConfig,
	chain *coreE2EChain, material *coreRecoveryMaterial) *Manager {

	t.Helper()
	// The cold Manager is returned to the caller and becomes the next operation
	// participant. Give it the caller's lifetime: creating it inside the nested
	// checkpoint subtest would run coreNewE2EManager's t.Cleanup(manager.Close)
	// before the caller could use it, which clears its unlocked account session.
	recovered := coreRestoreE2E(t, cfg, chain, material)
	var coldClosureAtCheckpoint map[string]string
	passed := t.Run(name, func(t *testing.T) {
		beforeBroadcasts := chain.broadcastCount()
		_, err := source.RefreshRGB11State(context.Background())
		coreRequire(t, "freshly reconcile source before checkpoint", err)
		before := coreRGBSemantic(t, source)
		coreAssert(t, len(before.Tasks) != 0 || len(before.Invoices) != 0,
			"pending checkpoint did not reach a real local transaction/invoice")
		closureBefore := coreRemoteStableRGB(t, source, cfg)
		coreAssert(t, len(closureBefore) > 0, "pending phase has no authoritative RGB recovery closure")
		// Recreate the RGB module over the SAME local DB, without importing a
		// snapshot or using a copied DB. The remaining flow uses this new module.
		if old := source.rgbManager.scopeStates; old != nil {
			old.stopReconciliations()
		}
		reopened, err := newRGB11Manager(source, source.db, source.utxoLockerL1, chain)
		coreRequire(t, "reopen local RGB module", err)
		reopened.scopeStates.stopReconciliations()
		source.rgbManager = reopened
		coreRequire(t, "select reopened local scope", reopened.selectRGB11Scope())
		coreRequire(t, "rebuild local transaction locks", reopened.rebuildRGB11Locks())
		_, err = reopened.RefreshRGB11State(context.Background())
		coreRequire(t, "freshly reconcile reopened local projection", err)
		after := coreRGBSemantic(t, source)
		coreAssert(t, coreDigest(before) == coreDigest(after),
			"local RGB reopen and fresh reconciliation changed ownership, readiness, invoice, task, proof or spending restriction")
		closureAfterLocalReopen := coreRemoteStableRGB(t, source, cfg)
		coreAssert(t, coreDigest(closureBefore) == coreDigest(closureAfterLocalReopen),
			"local RGB reopen changed the complete authoritative recovery closure")
		coreVerifyPaidRecords(t, source, cfg)

		// The separate DB must restore the exact source phase and its lock. The
		// cold device becomes the next operation participant in its caller.
		coreSynchronizeRestoredAccount(t, recovered)
		_, err = recovered.RefreshRGB11State(context.Background())
		coreRequire(t, "freshly reconcile cold restored projection", err)
		coreAssert(t, source.db != recovered.db, "pending recovery reused the source database")
		coreAssert(t, coreDigest(coreCanonicalCatalog(source)) == coreDigest(coreCanonicalCatalog(recovered)),
			"stable recovery lost the wallet/account catalog")
		view := coreRGBSemantic(t, recovered)
		coreAssert(t, coreDigest(view) == coreDigest(after),
			"cold recovery omitted or changed the committed invoice/transfer/proof/lock phase")
		coreAssert(t, coreDigest(view.Invoices) == coreDigest(after.Invoices) &&
			coreDigest(view.Tasks) == coreDigest(after.Tasks) &&
			coreDigest(view.Locked) == coreDigest(after.Locked),
			"cold recovery changed request identity, transfer phase, or input reservation")
		closureAfterColdRestore := coreRemoteStableRGB(t, recovered, cfg)
		coreAssert(t, coreDigest(closureAfterColdRestore) == coreDigest(closureAfterLocalReopen),
			"cold recovery omitted or changed necessary canonical recovery material")
		coldClosureAtCheckpoint = closureAfterColdRestore
		coreAssert(t, chain.broadcastCount() == beforeBroadcasts,
			"local reopen or cold recovery unexpectedly broadcast a transaction")
	})
	if !passed || recovered == nil {
		t.Fatalf("core-e2e: checkpoint %q failed; stopping the parent flow", name)
	}
	recovered.mutex.RLock()
	recoveredSecretLen := len(recovered.accountSecret)
	var recoveredAccountID, recoveredRootFingerprint string
	var recoveredRoot common.Wallet
	var recoveredRootErr error
	if recovered.accountProfile != nil {
		recoveredAccountID = recovered.accountProfile.AccountID
		recoveredRootFingerprint = recovered.accountProfile.RootFingerprint
		recoveredRootInfo, err := recovered.accountManagementRootWalletLocked()
		recoveredRootErr = err
		if err == nil && recoveredRootInfo != nil {
			recoveredRoot = cloneWalletAtAccountZero(recoveredRootInfo.Wallet)
		}
	}
	recovered.mutex.RUnlock()
	recoveredRootAccountID, recoveredRootIDErr := dkvsAccountID(recoveredRoot)
	profileAccountMatchesRoot := recoveredRootIDErr == nil && recoveredAccountID == recoveredRootAccountID
	rootWalletMatchesProfile := recoveredRootErr == nil && recoveredRoot != nil &&
		walletFingerprint(recoveredRoot) == recoveredRootFingerprint
	if recoveredSecretLen != 32 || len(recoveredAccountID) != 64 ||
		!profileAccountMatchesRoot || !rootWalletMatchesProfile {
		t.Fatalf("core-e2e: returned cold manager lost its unlocked account owner (secret_len=%d account_id_len=%d profile_account_matches_root=%t root_wallet_matches_profile=%t)",
			recoveredSecretLen, len(recoveredAccountID), profileAccountMatchesRoot,
			rootWalletMatchesProfile)
	}
	returnedClosure := coreRemoteStableRGB(t, recovered, cfg)
	coreAssert(t, len(returnedClosure) > 0 && coreDigest(returnedClosure) == coreDigest(coldClosureAtCheckpoint),
		"returned cold manager cannot verify the same authoritative recovery bundle after checkpoint cleanup")
	return recovered
}
