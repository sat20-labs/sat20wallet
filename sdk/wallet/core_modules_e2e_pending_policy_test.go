package wallet

import (
	"bytes"
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func coreRemoteStableRGB(t *testing.T, manager *Manager, cfg coreE2EConfig) map[string]string {
	t.Helper()
	closure := coreReadRGBClosure(t, manager, cfg)
	defer coreZeroRGBPayloads(closure.payloads)
	result := make(map[string]string, len(closure.payloads))
	for scope, payload := range closure.payloads {
		result[scope] = coreDigest(payload)
	}
	return result
}

type coreRGBClosure struct {
	records  map[string]*swire.DKVSRecord
	payloads map[string][]byte
}

func coreReadRGBClosure(t *testing.T, manager *Manager, cfg coreE2EConfig) coreRGBClosure {
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
	network := manager.cfg.Chain
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	coreRequire(t, "stable reference root", err)
	coreAssert(t, root != nil, "stable reference root wallet is missing")
	stateKey, err := manager.accountManagedStateKey(root)
	coreRequire(t, "stable reference state key", err)
	indexKey, err := account.ManagedIndexKey(profileAccountID)
	coreRequire(t, "stable reference index key", err)
	client := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	stateRecord, err := client.GetRecordDirect(stateKey)
	coreRequire(t, "read authoritative stable state", err)
	indexRecord, err := client.GetRecordDirect(indexKey)
	coreRequire(t, "read authoritative stable index", err)
	coreRequire(t, "verify stable state signature", dkvsindexer.VerifySignature(stateRecord))
	coreRequire(t, "verify stable index signature", dkvsindexer.VerifySignature(indexRecord))
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
	coreAssert(t, state.RootFingerprint == profileRootFingerprint,
		"stable catalog root does not match its account owner")
	index, err := account.OpenManagedIndex(secret, profileAccountID, network, indexRecord.Value)
	coreRequire(t, "verify stable index", err)
	walletsFound := false
	result := coreRGBClosure{
		records:  map[string]*swire.DKVSRecord{stateKey: stateRecord, indexKey: indexRecord},
		payloads: make(map[string][]byte),
	}
	for _, entry := range index.Entries {
		if entry.Deleted {
			continue
		}
		if entry.Provider == "wallets" && entry.Scope == AccountManagedDataGlobalScope {
			coreAssert(t, entry.Key == stateKey, "stable index points to a different wallet catalog")
			walletsFound = true
		}
		if entry.Provider != rgb11AccountManagedProviderID {
			continue
		}
		moduleKey, err := account.ManagedModuleKey(profileAccountID, entry.Provider, entry.Scope)
		coreRequire(t, "derive stable RGB module key", err)
		coreAssert(t, moduleKey == entry.Key, "stable RGB entry has an invalid module key")
		record, err := client.GetRecordDirect(entry.Key)
		coreRequire(t, "read authoritative RGB module", err)
		coreRequire(t, "verify RGB module signature", dkvsindexer.VerifySignature(record))
		blob, err := DecodeDKVSBlobValue(record.Value)
		coreRequire(t, "decode stable RGB blob", err)
		module, err := account.OpenManagedModule(secret, profileAccountID, network, entry.Key, blob.Data)
		coreRequire(t, "verify stable RGB module", err)
		coreAssert(t, module.Provider == entry.Provider && module.Scope == entry.Scope && module.Codec == entry.Codec,
			"stable RGB module does not match its directory entry")
		// The account-encrypted stable payload is the authoritative recovery
		// closure, including the private material needed to resume unfinished
		// invoice and transfer obligations.
		_, err = rgb11wallet.DecodeRecoveryPackage(module.Payload)
		coreRequire(t, "decode authoritative RGB recovery closure", err)
		result.records[entry.Key] = record
		result.payloads[entry.Scope] = module.Payload
	}
	coreAssert(t, walletsFound, "stable index omits the wallet catalog")
	return result
}

func coreLocalRGBPayloads(t *testing.T, manager *Manager) map[string][]byte {
	t.Helper()
	catalog, err := manager.accountManagedDataCatalog()
	coreRequire(t, "enumerate local checkpoint scopes", err)
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	payloads, err := provider.Export(catalog)
	coreRequire(t, "export complete local checkpoint material", err)
	result := make(map[string][]byte, len(payloads))
	for _, payload := range payloads {
		result[payload.Scope] = payload.Payload
	}
	return result
}

func coreZeroRGBPayloads(payloads map[string][]byte) {
	for _, payload := range payloads {
		zeroBytes(payload)
	}
}

func coreDescribeRGBTransfer(t *testing.T, label, key string, want, got []byte) {
	t.Helper()
	left, leftErr := rgb11wallet.TransfersFromProjectionSnapshot([]rgb11wallet.SnapshotRecord{{Key: key, Value: want}})
	right, rightErr := rgb11wallet.TransfersFromProjectionSnapshot([]rgb11wallet.SnapshotRecord{{Key: key, Value: got}})
	leftOK, rightOK := leftErr == nil && len(left) == 1, rightErr == nil && len(right) == 1
	t.Logf("core recovery transfer phase=%s key_tag=%s before_valid=%t after_valid=%t",
		label, coreDigest(key)[:12], leftOK, rightOK)
	if !leftOK || !rightOK {
		return
	}
	if left[0].AckStatus != right[0].AckStatus {
		t.Logf("core recovery ACK phase=%s key_tag=%s before_id_tag=%s after_id_tag=%s before_ack=%q after_ack=%q before_direction=%q after_direction=%q before_status=%q after_status=%q before_address_mode=%t after_address_mode=%t",
			label, coreDigest(key)[:12], coreDigest(left[0].TransferID)[:12], coreDigest(right[0].TransferID)[:12],
			left[0].AckStatus, right[0].AckStatus, left[0].Direction, right[0].Direction,
			left[0].Status, right[0].Status, left[0].AddressMode, right[0].AddressMode)
	}
	a, b := reflect.ValueOf(*left[0]), reflect.ValueOf(*right[0])
	for i := 0; i < a.NumField(); i++ {
		t.Logf("core recovery transfer phase=%s key_tag=%s field=%s equal=%t",
			label, coreDigest(key)[:12], a.Type().Field(i).Name,
			reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()))
	}
	amountEqual := left[0].Asset.Amount.Value == nil && right[0].Asset.Amount.Value == nil ||
		left[0].Asset.Amount.Value != nil && right[0].Asset.Amount.Value != nil &&
			left[0].Asset.Amount.Value.Cmp(right[0].Asset.Amount.Value) == 0
	for field, equal := range map[string]bool{
		"Asset.Name":             left[0].Asset.Name == right[0].Asset.Name,
		"Asset.Amount.Precision": left[0].Asset.Amount.Precision == right[0].Asset.Amount.Precision,
		"Asset.Amount.Value":     amountEqual,
		"Asset.BindingSat":       left[0].Asset.BindingSat == right[0].Asset.BindingSat,
	} {
		t.Logf("core recovery transfer phase=%s key_tag=%s field=%s equal=%t",
			label, coreDigest(key)[:12], field, equal)
	}
}

func coreDescribeRGBRecords(t *testing.T, label, kind string,
	want, got []rgb11wallet.SnapshotRecord) {
	t.Helper()
	left, right := make(map[string][]byte), make(map[string][]byte)
	for _, record := range want {
		left[record.Key] = record.Value
	}
	for _, record := range got {
		right[record.Key] = record.Value
	}
	counts := make(map[string][3]int)
	reported := make(map[string]int)
	keys := make(map[string]bool)
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}
	for key := range keys {
		category := kind
		if kind == "projection" {
			category = "other"
			for _, prefix := range []string{"proof-", "pending-", "transfer-", "object-", "validation-", "output-",
				"prepared-receive-", "receive-key-", "receive-reservation-", "contract-object-", "local-name-"} {
				if strings.HasPrefix(key, prefix) {
					category = strings.TrimSuffix(prefix, "-")
					break
				}
			}
		}
		a, had := left[key]
		b, has := right[key]
		if had && has && bytes.Equal(a, b) {
			continue
		}
		count := counts[category]
		switch {
		case !had:
			count[0]++
		case !has:
			count[1]++
		default:
			count[2]++
		}
		counts[category] = count
		if reported[category] < 3 {
			t.Logf("core recovery difference phase=%s kind=%s key_tag=%s before_bytes=%d after_bytes=%d before_tag=%s after_tag=%s",
				label, category, coreDigest(key)[:12], len(a), len(b), coreDigest(a)[:12], coreDigest(b)[:12])
			if category == "transfer" && had && has {
				coreDescribeRGBTransfer(t, label, key, a, b)
			}
			reported[category]++
		}
	}
	categories := make([]string, 0, len(counts))
	for category := range counts {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	for _, category := range categories {
		count := counts[category]
		t.Logf("core recovery difference phase=%s kind=%s added=%d removed=%d changed=%d",
			label, category, count[0], count[1], count[2])
	}
}

func coreCheckRGBPayloads(t *testing.T, label string, want, got map[string][]byte) {
	t.Helper()
	coreAssert(t, coreDescribeRGBPayloads(t, label, want, got), label+" changed complete canonical recovery material")
}

func coreDescribeRGBPayloads(t *testing.T, label string, want, got map[string][]byte) bool {
	t.Helper()
	scopes := make(map[string]bool)
	for scope := range want {
		scopes[scope] = true
	}
	for scope := range got {
		scopes[scope] = true
	}
	equal := len(want) == len(got)
	for scope := range scopes {
		a, had := want[scope]
		b, has := got[scope]
		if had && has && bytes.Equal(a, b) {
			continue
		}
		equal = false
		t.Logf("core recovery difference phase=%s scope_tag=%s before_present=%t after_present=%t before_bytes=%d after_bytes=%d before_tag=%s after_tag=%s",
			label, coreDigest(scope)[:12], had, has, len(a), len(b), coreDigest(a)[:12], coreDigest(b)[:12])
		left, leftErr := rgb11wallet.DecodeRecoveryPackage(a)
		right, rightErr := rgb11wallet.DecodeRecoveryPackage(b)
		if leftErr != nil || rightErr != nil {
			t.Logf("core recovery difference phase=%s before_valid=%t after_valid=%t", label, leftErr == nil, rightErr == nil)
			continue
		}
		t.Logf("core recovery difference phase=%s version_equal=%t wallet_equal=%t account_equal=%t build_equal=%t",
			label, left.Version == right.Version, left.WalletID == right.WalletID,
			left.AccountIndex == right.AccountIndex, left.EngineBuildID == right.EngineBuildID)
		coreDescribeRGBRecords(t, label, "projection", left.ProjectionRecords, right.ProjectionRecords)
		coreDescribeRGBRecords(t, label, "engine", left.EngineRecords, right.EngineRecords)
	}
	return equal
}

func coreCheckRGBBaseline(t *testing.T, label string, want, got coreRGBClosure) {
	t.Helper()
	equal := len(want.records) == len(got.records)
	for key, before := range want.records {
		after := got.records[key]
		if after != nil && dkvsindexer.RecordHash(before) == dkvsindexer.RecordHash(after) {
			continue
		}
		equal = false
		var afterSeq uint64
		if after != nil {
			afterSeq = after.Seq
		}
		t.Logf("core recovery remote difference phase=%s key_tag=%s before_seq=%d after_seq=%d after_present=%t",
			label, coreDigest(key)[:12], before.Seq, afterSeq, after != nil)
	}
	coreCheckRGBPayloads(t, label, want.payloads, got.payloads)
	coreAssert(t, equal, label+" rewrote a signed remote baseline")
}

// Diagnose every public-view component without printing invoices, addresses,
// consignments or transaction material. Preserve the exact comparison oracle.
func coreDescribeRGBView(t *testing.T, label string, before, after coreRGBView,
	beforeBroadcasts, afterBroadcasts int) {
	t.Helper()
	t.Logf("core checkpoint view phase=%s broadcasts_before=%d broadcasts_after=%d", label, beforeBroadcasts, afterBroadcasts)
	a, b := reflect.ValueOf(before), reflect.ValueOf(after)
	for i := 0; i < a.NumField(); i++ {
		field := a.Type().Field(i).Name
		left, right := a.Field(i), b.Field(i)
		equal := reflect.DeepEqual(left.Interface(), right.Interface())
		t.Logf("core checkpoint view phase=%s field=%s equal=%t", label, field, equal)
		if !equal && left.Kind() == reflect.String {
			t.Logf("core checkpoint state phase=%s field=%s before=%q after=%q", label, field, left.String(), right.String())
		} else if !equal && left.Kind() == reflect.Bool {
			t.Logf("core checkpoint state phase=%s field=%s before=%t after=%t", label, field, left.Bool(), right.Bool())
		}
		if equal || left.Kind() != reflect.Map {
			continue
		}
		want, got := left.Interface().(map[string]string), right.Interface().(map[string]string)
		keys := make(map[string]bool, len(want)+len(got))
		for key := range want {
			keys[key] = true
		}
		for key := range got {
			keys[key] = true
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			old, had := want[key]
			newValue, has := got[key]
			if had == has && old == newValue {
				continue
			}
			t.Logf("core checkpoint item phase=%s field=%s key_tag=%s before_present=%t after_present=%t before_tag=%s after_tag=%s",
				label, field, coreDigest(key)[:12], had, has, coreDigest(old)[:12], coreDigest(newValue)[:12])
			switch field {
			case "Assets", "Available", "Pending":
				t.Logf("core checkpoint amount phase=%s field=%s key_tag=%s before=%s after=%s", label, field, coreDigest(key)[:12], old, newValue)
			case "Tasks":
				oldParts, newParts := strings.Split(old, "|"), strings.Split(newValue, "|")
				oldStatus, oldACK, newStatus, newACK := "", "", "", ""
				if len(oldParts) >= 2 {
					oldStatus, oldACK = oldParts[0], oldParts[1]
				}
				if len(newParts) >= 2 {
					newStatus, newACK = newParts[0], newParts[1]
				}
				t.Logf("core checkpoint task phase=%s key_tag=%s before_status=%q after_status=%q before_ack=%q after_ack=%q",
					label, coreDigest(key)[:12], oldStatus, newStatus, oldACK, newACK)
			}
		}
	}
}

// Every public business phase is covered by the complete stable recovery
// closure. Reopen the local RGB engine, then recover into a distinct database
// and require the same invoice/operation/ownership/lock view. Recovery is
// observational: it must never turn a pending transaction into a broadcast.
func corePendingCheckpoint(t *testing.T, name string, source *Manager, cfg coreE2EConfig,
	chain *coreE2EChain, material *coreRecoveryMaterial) *Manager {

	t.Helper()
	enterBroadcasts := chain.broadcastCount()
	// Close the old device's automatic receiver before taking either source
	// snapshot. Stop joins already-running mailbox/ACK/broadcast work too.
	source.StopDKVSSync()
	beforeBroadcasts := chain.broadcastCount()
	t.Logf("core checkpoint handoff phase=%s broadcasts_enter=%d broadcasts_joined=%d", name, enterBroadcasts, beforeBroadcasts)
	_, err := source.RefreshRGB11State(context.Background())
	coreRequire(t, "freshly reconcile source before checkpoint", err)
	before := coreRGBSemantic(t, source)
	coreAssert(t, len(before.Tasks) != 0 || len(before.Invoices) != 0,
		"pending checkpoint did not reach a real local transaction/invoice")
	localBefore := coreLocalRGBPayloads(t, source)
	defer coreZeroRGBPayloads(localBefore)
	// Reopen the RGB module over the same local DB before waiting for the final
	// backup. These real reconciliations may commit another local generation.
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
	if coreDigest(before) != coreDigest(after) || chain.broadcastCount() != beforeBroadcasts {
		coreDescribeRGBView(t, name+" same DB reopen", before, after, beforeBroadcasts, chain.broadcastCount())
	}
	coreAssert(t, coreDigest(before) == coreDigest(after),
		"local RGB reopen and fresh reconciliation changed ownership, readiness, invoice, task, proof or spending restriction")
	// Revalidate the stopped session and finish its final backup without
	// resuming the automatic RGB mailbox worker.
	coreSuspendRGBDevice(t, source)
	localAfter := coreLocalRGBPayloads(t, source)
	defer coreZeroRGBPayloads(localAfter)
	coreCheckRGBPayloads(t, name+" local reopen", localBefore, localAfter)
	baseline := coreReadRGBClosure(t, source, cfg)
	defer coreZeroRGBPayloads(baseline.payloads)
	coreAssert(t, len(baseline.payloads) > 0, "pending phase has no authoritative RGB recovery closure")
	coreCheckRGBPayloads(t, name+" source backup", baseline.payloads, localAfter)
	coreVerifyPaidRecords(t, source, cfg)
	catalog := coreCanonicalCatalog(source)
	sourceDB := source.db
	identity := source.GetAccountManagementStatus()
	// Keep the stopped source DB and identity for an explicit later device
	// return. The flow owner restarts it after cold participants are closed.
	// Use the caller's lifetime so checkpoint cleanup cannot close the Manager
	// which is returned as the next operation participant.
	recovered := coreRestoreE2E(t, cfg, chain, material, false)
	// Verify the real device initialization before starting its automatic
	// mailbox worker. Explicit account synchronization still uses the network.
	imported := coreLocalRGBPayloads(t, recovered)
	defer coreZeroRGBPayloads(imported)
	passed := t.Run(name, func(t *testing.T) {
		importEqual := coreDescribeRGBPayloads(t, name+" after Import", baseline.payloads, imported)
		coreSynchronizeRestoredAccount(t, recovered)
		synchronized := coreLocalRGBPayloads(t, recovered)
		defer coreZeroRGBPayloads(synchronized)
		syncEqual := coreDescribeRGBPayloads(t, name+" after Sync", baseline.payloads, synchronized)
		_, err = recovered.RefreshRGB11State(context.Background())
		coreRequire(t, "freshly reconcile cold restored projection", err)
		local := coreLocalRGBPayloads(t, recovered)
		defer coreZeroRGBPayloads(local)
		refreshEqual := coreDescribeRGBPayloads(t, name+" after Refresh", baseline.payloads, local)
		first := "none"
		switch {
		case !importEqual:
			first = "Import"
		case !syncEqual:
			first = "Sync"
		case !refreshEqual:
			first = "Refresh"
		}
		t.Logf("core recovery stages checkpoint=%s import_equal=%t sync_equal=%t refresh_equal=%t first_observed_difference=%s",
			name, importEqual, syncEqual, refreshEqual, first)
		coreAssert(t, sourceDB != recovered.db, "pending recovery reused the source database")
		coreAssert(t, coreDigest(catalog) == coreDigest(coreCanonicalCatalog(recovered)),
			"stable recovery lost the wallet/account catalog")
		view := coreRGBSemantic(t, recovered)
		if coreDigest(view) != coreDigest(after) || chain.broadcastCount() != beforeBroadcasts {
			coreDescribeRGBView(t, name+" cold takeover", after, view, beforeBroadcasts, chain.broadcastCount())
		}
		coreAssert(t, coreDigest(view) == coreDigest(after),
			"cold recovery omitted or changed the committed invoice/transfer/proof/lock phase")
		coreAssert(t, coreDigest(view.Invoices) == coreDigest(after.Invoices) &&
			coreDigest(view.Tasks) == coreDigest(after.Tasks) &&
			coreDigest(view.Locked) == coreDigest(after.Locked),
			"cold recovery changed request identity, transfer phase, or input reservation")
		coreAssert(t, refreshEqual, name+" cold local changed complete canonical recovery material")
		remote := coreReadRGBClosure(t, recovered, cfg)
		defer coreZeroRGBPayloads(remote.payloads)
		coreCheckRGBBaseline(t, name+" cold restore", baseline, remote)
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
		!profileAccountMatchesRoot || !rootWalletMatchesProfile ||
		recoveredAccountID != identity.AccountID || recoveredRootFingerprint != identity.RootFingerprint {
		t.Fatalf("core-e2e: returned cold manager lost its unlocked account owner (secret_len=%d account_id_len=%d profile_account_matches_root=%t root_wallet_matches_profile=%t)",
			recoveredSecretLen, len(recoveredAccountID), profileAccountMatchesRoot,
			rootWalletMatchesProfile)
	}
	returnedClosure := coreReadRGBClosure(t, recovered, cfg)
	defer coreZeroRGBPayloads(returnedClosure.payloads)
	coreCheckRGBBaseline(t, name+" returned cold", baseline, returnedClosure)
	coreRequire(t, "start initialized cold checkpoint device", recovered.StartDKVSSync())
	coreWaitAccountOnline(t, recovered)
	return recovered
}
