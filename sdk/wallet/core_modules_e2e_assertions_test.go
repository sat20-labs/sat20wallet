package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func coreAssets(assets indexer.TxAssets) map[string]string {
	result := make(map[string]string)
	for _, a := range assets {
		result[a.Name.String()] = a.Amount.Value.String()
	}
	return result
}

type coreRGBView struct {
	Initialized                              bool
	SyncStatus                               string
	ConsistencyStatus                        string
	Assets, Available, Pending               map[string]string
	Outputs, Proofs, Tasks, Invoices, Locked map[string]string
}

// This public ownership/readiness view intentionally omits settled historical
// proof rows. The complete durable recovery closure is compared separately by
// corePendingCheckpoint; this view remains useful for checking spendable
// balance and restriction behavior after fresh chain reconciliation.
func coreConfirmedHistoricalOutput(t *testing.T, manager *Manager, point string) bool {
	t.Helper()
	chain, ok := manager.rgbManager.evidence.(*coreE2EChain)
	coreAssert(t, ok, "semantic checker lost its controlled Bitcoin evidence")
	// This is the test oracle, not SDK evidence: confirmed spender identity is
	// deliberately unavailable from GetOutspend, but remains in our ledger.
	chain.mu.Lock()
	defer chain.mu.Unlock()
	spender := chain.spent[point]
	status := chain.status[spender]
	return spender != "" && status.Confirmed && status.Confirmations >= 6
}

func coreRGBSemantic(t *testing.T, manager *Manager) coreRGBView {
	t.Helper()
	state, err := manager.GetRGB11State()
	coreRequire(t, "read actual RGB state", err)
	view := coreRGBView{
		Initialized: state.Initialized, SyncStatus: state.SyncStatus,
		ConsistencyStatus: state.ConsistencyStatus,
		Assets:            coreAssets(state.Assets), Available: coreAssets(state.AvailableAssets), Pending: coreAssets(state.PendingAssets),
		Outputs: map[string]string{}, Proofs: map[string]string{}, Tasks: map[string]string{},
		Invoices: map[string]string{}, Locked: map[string]string{},
	}
	for _, output := range state.Outputs {
		if output != nil {
			view.Outputs[output.OutPointStr] = coreDigest(coreAssets(output.Assets))
		}
	}
	locks := manager.GetUtxoLocker().GetLockedUtxoList()
	for _, p := range state.Proofs {
		if p.Status == "spent" || coreConfirmedHistoricalOutput(t, manager, p.OutPoint) {
			continue
		}
		key := p.OutPoint + "|" + p.AssetName.String() + "|" + p.OperationID + fmt.Sprint("|", p.AssignmentType, "|", p.AssignmentIndex)
		view.Proofs[key] = fmt.Sprintf("%s|%s|%s", p.Status, p.ConsignmentHash, p.WitnessTxID)
		if lock := locks[p.OutPoint]; lock != nil {
			view.Locked[p.OutPoint] = fmt.Sprint(lock.Reason)
		}
	}
	for _, r := range state.Reservations {
		view.Invoices[r.RequestID] = r.Invoice + "|" + r.Status
	}
	for _, task := range state.Transfers {
		if task.Status == "settled" || task.Status == "rejected" || task.Status == "cancelled" || task.Status == "expired" {
			continue
		}
		view.Tasks[task.Direction+"|"+task.TransferID] = fmt.Sprintf("%s|%s|%s|%s|%v|%v", task.Status, task.AckStatus,
			task.WitnessTxID, task.ConsignmentHash, task.InputOutPoints, task.OutputOutPoints)
		for _, point := range task.InputOutPoints {
			if lock := locks[point]; lock != nil {
				view.Locked[point] = fmt.Sprint(lock.Reason)
			}
		}
	}
	return view
}

func coreDigest(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func coreCanonicalCatalog(manager *Manager) map[string]string {
	result := map[string]string{}
	for _, entry := range manager.GetWalletCatalog() {
		result[entry.Fingerprint] = entry.Name + "|" + coreDigest(entry.Accounts)
	}
	return result
}

func coreVerifyPaidRecords(t *testing.T, source *Manager, cfg coreE2EConfig) {
	t.Helper()
	root, err := source.accountManagementRootWallet()
	coreRequire(t, "read root", err)
	stateKey, err := source.accountManagedStateKey(root)
	coreRequire(t, "managed state key", err)
	accountID, err := dkvsAccountID(root)
	coreRequire(t, "managed account ID", err)
	indexKey, err := account.ManagedIndexKey(accountID)
	coreRequire(t, "managed index key", err)
	wrapperKey, err := accountRootWrapperKey(root)
	coreRequire(t, "root wrapper key", err)
	client := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	indexRecord, err := client.GetRecordDirect(indexKey)
	coreRequire(t, "read published directory", err)
	coreAssert(t, indexRecord != nil, "published directory is missing")
	coreRequire(t, "verify published directory signature", dkvsindexer.VerifySignature(indexRecord))
	source.mutex.RLock()
	secret := append([]byte(nil), source.accountSecret...)
	network := source.cfg.Chain
	source.mutex.RUnlock()
	defer zeroBytes(secret)
	index, err := account.OpenManagedIndex(secret, accountID, network, indexRecord.Value)
	coreRequire(t, "authenticate published directory", err)
	keys := []string{indexKey, wrapperKey}
	walletsFound := false
	for _, entry := range index.Entries {
		if entry.Deleted {
			continue
		}
		keys = append(keys, entry.Key)
		if entry.Provider == "wallets" && entry.Scope == AccountManagedDataGlobalScope {
			coreAssert(t, entry.Key == stateKey, "directory wallet catalog key mismatch")
			walletsFound = true
		}
	}
	coreAssert(t, walletsFound, "directory must include the actual wallet catalog")
	for _, key := range keys {
		record, err := client.GetRecordDirect(key)
		coreRequire(t, "read published paid record", err)
		coreAssert(t, record != nil, "published paid record is missing")
		coreRequire(t, "verify published paid record signature", dkvsindexer.VerifySignature(record))
		proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
		coreRequire(t, "parse AUTOPAY proof", err)
		coreAssert(t, record.TTL == 0 && proof.Mode == dkvsindexer.FeeModeAutopay && proof.PoolContract == cfg.Contract,
			"managed data silently fell back to temporary storage")
		for _, plain := range []string{coreE2ESenderMnemonic, coreE2EReceiverMnemonic, coreE2EChildMnemonic, "-----BEGIN RGB"} {
			coreAssert(t, !bytes.Contains(record.Value, []byte(plain)), "remote recovery data exposes plaintext")
		}
	}
}

func coreReplayManagedOutbox(t *testing.T, target *Manager) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err := target.syncDKVSOnce()
		if err != nil && !errors.Is(err, ErrDKVSPathNotSynced) {
			coreRequire(t, "replay original managed outbox", err)
		}
		err = target.SyncAccountManagementState(context.Background())
		if err != nil && !errors.Is(err, ErrDKVSPathNotSynced) {
			coreRequire(t, "confirm replayed account baseline", err)
		}
		plan, planErr := target.currentAccountManagedOutboxPlan()
		coreRequire(t, "read replayed managed outbox", planErr)
		if err == nil && !plan.Pending && !coreCaptureManagedProfile(target).dirty {
			return
		}
		coreAssert(t, time.Now().Before(deadline),
			"original managed outbox did not reach a clean confirmed baseline")
		time.Sleep(100 * time.Millisecond)
	}
}

func coreSynchronizeRestoredAccount(t *testing.T, target *Manager) {
	t.Helper()
	client, err := target.ensureDKVSManager().primaryClient()
	coreRequire(t, "restored SDK replica client", err)
	id, err := target.RootAccountID()
	coreRequire(t, "restored account identity", err)
	_, _, err = client.SubscribePrefix("/mail/" + id)
	coreRequire(t, "restored mailbox directory readiness", err)
	for attempt := 0; attempt < 3; attempt++ {
		err = target.SyncAccountManagementState(context.Background())
		if err == nil || errors.Is(err, ErrRGB11ManagedOperationActive) {
			return
		}
		if !errors.Is(err, ErrDKVSPathNotSynced) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	coreRequire(t, "synchronize restored account", err)
}

func coreDescribeDifference(t *testing.T, source, recovered coreRGBView) {
	t.Helper()
	missing, changedStatus, changedReceipt, changedWitness := 0, 0, 0, 0
	for key, value := range source.Proofs {
		other, exists := recovered.Proofs[key]
		if !exists {
			missing++
			continue
		}
		a, b := strings.Split(value, "|"), strings.Split(other, "|")
		if a[0] != b[0] {
			changedStatus++
		}
		if a[1] != b[1] {
			changedReceipt++
		}
		if a[2] != b[2] {
			changedWitness++
		}
	}
	t.Logf("core-e2e: diagnostic counts: proofs=%d/%d missing=%d status_changes=%d receipt_changes=%d witness_changes=%d tasks=%d/%d invoices=%d/%d locks=%d/%d",
		len(source.Proofs), len(recovered.Proofs), missing, changedStatus, changedReceipt, changedWitness,
		len(source.Tasks), len(recovered.Tasks), len(source.Invoices), len(recovered.Invoices), len(source.Locked), len(recovered.Locked))
}

func coreCheckpoint(t *testing.T, name string, source *Manager, cfg coreE2EConfig, chain *coreE2EChain, material *coreRecoveryMaterial) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		beforeBroadcasts := chain.broadcastCount()
		sourceWallet, sourceAccount := source.status.CurrentWallet, source.status.CurrentAccount
		sourceStopped := false
		// Register before constructing target: its Close cleanup runs first.
		t.Cleanup(func() {
			coreRequire(t, "restore source wallet selection", source.SwitchWallet(sourceWallet, coreE2EPassword))
			coreRequire(t, "restore source account selection", source.SwitchAccount(sourceAccount))
			if sourceStopped {
				coreRequire(t, "resume source after checkpoint target closed", source.StartDKVSSync())
				coreSynchronizeRestoredAccount(t, source)
				coreWaitPrivateBackup(t, source)
				coreAssert(t, chain.broadcastCount() == beforeBroadcasts, "checkpoint source resume caused an unexpected broadcast")
			}
		})
		coreWaitPrivateBackup(t, source)
		entries := source.GetWalletCatalog()
		expected := make(map[string]map[uint32]coreRGBView, len(entries))
		for _, entry := range entries {
			coreRequire(t, "select source scope", source.SwitchWallet(entry.ID, coreE2EPassword))
			expected[entry.Fingerprint] = make(map[uint32]coreRGBView, len(entry.Accounts))
			for _, sub := range entry.Accounts {
				coreRequire(t, "select source account", source.SwitchAccount(sub.Index))
				_, err := source.RefreshRGB11State(context.Background())
				coreRequire(t, "refresh source checkpoint scope", err)
				expected[entry.Fingerprint][sub.Index] = coreRGBSemantic(t, source)
			}
		}
		// Confirm all freshly reconciled scopes before handing this account to
		// its independent cold device. The original worker remains stopped.
		coreWaitPrivateBackup(t, source)
		coreVerifyPaidRecords(t, source, cfg)
		catalog := coreCanonicalCatalog(source)
		source.StopDKVSSync()
		sourceStopped = true
		target := coreRestoreE2E(t, cfg, chain, material)
		coreSynchronizeRestoredAccount(t, target)
		coreAssert(t, coreDigest(catalog) == coreDigest(coreCanonicalCatalog(target)), "recovered wallet/subaccount catalog is not exact")
		targetCatalog := target.GetWalletCatalog()
		for _, entry := range entries {
			var restoredID int64
			for _, candidate := range targetCatalog {
				if candidate.Fingerprint == entry.Fingerprint {
					restoredID = candidate.ID
				}
			}
			coreAssert(t, restoredID != 0, "missing restored wallet fingerprint")
			coreRequire(t, "select restored scope", target.SwitchWallet(restoredID, coreE2EPassword))
			for _, sub := range entry.Accounts {
				coreRequire(t, "select restored account", target.SwitchAccount(sub.Index))
				_, err := target.RefreshRGB11State(context.Background())
				coreRequire(t, "refresh restored checkpoint scope", err)
				a, b := expected[entry.Fingerprint][sub.Index], coreRGBSemantic(t, target)
				if coreDigest(a) != coreDigest(b) {
					coreDescribeDifference(t, a, b)
					t.Errorf("core-e2e: %s scope account=%d recovery mismatch: assets=%t available=%t pending=%t proofs=%t tasks=%t invoices=%t locks=%t",
						name, sub.Index, coreDigest(a.Assets) == coreDigest(b.Assets), coreDigest(a.Available) == coreDigest(b.Available), coreDigest(a.Pending) == coreDigest(b.Pending),
						coreDigest(a.Proofs) == coreDigest(b.Proofs), coreDigest(a.Tasks) == coreDigest(b.Tasks), coreDigest(a.Invoices) == coreDigest(b.Invoices), coreDigest(a.Locked) == coreDigest(b.Locked))
				}
			}
		}
		// Finish the cold device's actual refresh backup before its Close.
		coreWaitPrivateBackup(t, target)
		coreAssert(t, chain.broadcastCount() == beforeBroadcasts, "recovery synchronization caused an unexpected broadcast")
	})
}

type coreOfflineHTTP struct{ HttpClient }

func (coreOfflineHTTP) SendGetRequest(*URL) ([]byte, error) {
	return nil, errors.New("intentional offline read")
}
func (coreOfflineHTTP) SendPostRequest(*URL, []byte) ([]byte, error) {
	return nil, errors.New("intentional offline write")
}
