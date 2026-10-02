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
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func coreAssets(assets indexer.TxAssets) map[string]string {
	result := make(map[string]string)
	for _, a := range assets { result[a.Name.String()] = a.Amount.Value.String() }
	return result
}

type coreRGBView struct {
	Assets, Available, Pending map[string]string
	Proofs, Tasks, Invoices, Locked map[string]string
}

// A confirmed, actually signature-verified spend turns its old allocation into
// history, which account recovery deliberately compacts. Do NOT use this for
// mempool spends: their input proofs, reservations and spending restrictions
// must remain recoverable until the transition has completed.
func coreConfirmedHistoricalOutput(t *testing.T, manager *Manager, point string) bool {
	t.Helper()
	chain, ok := manager.rgbManager.evidence.(*coreE2EChain)
	coreAssert(t, ok, "semantic checker lost its controlled Bitcoin evidence")
	spend, err := chain.GetOutspend(point)
	coreRequire(t, "check allocation spending evidence", err)
	if !spend.Spent || spend.SpendingTx == "" { return false }
	status, err := chain.GetTxStatus(spend.SpendingTx)
	coreRequire(t, "check confirmed spending transaction", err)
	return status.Confirmed && status.Confirmations >= 6
}

func coreRGBSemantic(t *testing.T, manager *Manager) coreRGBView {
	t.Helper()
	state, err := manager.GetRGB11State()
	coreRequire(t, "read actual RGB state", err)
	view := coreRGBView{
		Assets: coreAssets(state.Assets), Available: coreAssets(state.AvailableAssets), Pending: coreAssets(state.PendingAssets),
		Proofs: map[string]string{}, Tasks: map[string]string{}, Invoices: map[string]string{}, Locked: map[string]string{},
	}
	locks := manager.GetUtxoLocker().GetLockedUtxoList()
	for _, p := range state.Proofs {
		if p.Status == "spent" || coreConfirmedHistoricalOutput(t, manager, p.OutPoint) { continue }
		key := p.OutPoint + "|" + p.AssetName.String() + "|" + p.OperationID + fmt.Sprint("|", p.AssignmentType, "|", p.AssignmentIndex)
		view.Proofs[key] = fmt.Sprintf("%s|%s|%s", p.Status, p.ConsignmentHash, p.WitnessTxID)
		if lock := locks[p.OutPoint]; lock != nil { view.Locked[p.OutPoint] = fmt.Sprint(lock.Reason) }
	}
	for _, r := range state.Reservations { view.Invoices[r.RequestID] = r.Invoice + "|" + r.Status }
	for _, task := range state.Transfers {
		if task.Status == "settled" || task.Status == "rejected" || task.Status == "cancelled" || task.Status == "expired" { continue }
		view.Tasks[task.Direction+"|"+task.TransferID] = fmt.Sprintf("%s|%s|%s|%s|%v|%v", task.Status, task.AckStatus,
			task.WitnessTxID, task.ConsignmentHash, task.InputOutPoints, task.OutputOutPoints)
		for _, point := range task.InputOutPoints {
			if lock := locks[point]; lock != nil { view.Locked[point] = fmt.Sprint(lock.Reason) }
		}
	}
	return view
}

func coreDigest(v any) string {
	raw, err := json.Marshal(v)
	if err != nil { panic(err) }
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func coreCanonicalCatalog(manager *Manager) map[string]string {
	result := map[string]string{}
	for _, entry := range manager.GetWalletCatalog() { result[entry.Fingerprint] = entry.Name + "|" + coreDigest(entry.Accounts) }
	return result
}

func coreVerifyPaidRecords(t *testing.T, source *Manager, cfg coreE2EConfig) {
	t.Helper()
	root, err := source.accountManagementRootWallet()
	coreRequire(t, "read root", err)
	stateKey, err := source.accountManagedStateKey(root)
	coreRequire(t, "managed state key", err)
	blobKey, err := source.accountManagedDataBlobKey(root)
	coreRequire(t, "managed blob key", err)
	wrapperKey, err := accountRootWrapperKey(root)
	coreRequire(t, "root wrapper key", err)
	client := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	for _, key := range []string{stateKey, blobKey, wrapperKey} {
		record, err := client.GetRecordDirect(key)
		coreRequire(t, "read published paid record", err)
		proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
		coreRequire(t, "parse AUTOPAY proof", err)
		coreAssert(t, record.TTL == 0 && proof.Mode == dkvsindexer.FeeModeAutopay && proof.PoolContract == cfg.Contract,
			"managed data silently fell back to temporary storage")
		for _, plain := range []string{coreE2ESenderMnemonic, coreE2EReceiverMnemonic, coreE2EChildMnemonic, "-----BEGIN RGB"} {
			coreAssert(t, !bytes.Contains(record.Value, []byte(plain)), "remote recovery data exposes plaintext")
		}
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
		if err == nil || errors.Is(err, ErrRGB11ManagedOperationActive) { return }
		if !errors.Is(err, ErrDKVSPathNotSynced) { break }
		time.Sleep(50 * time.Millisecond)
	}
	coreRequire(t, "synchronize restored account", err)
}

func coreDescribeDifference(t *testing.T, source, recovered coreRGBView) {
	t.Helper()
	missing, changedStatus, changedReceipt, changedWitness := 0, 0, 0, 0
	for key, value := range source.Proofs {
		other, exists := recovered.Proofs[key]
		if !exists { missing++; continue }
		a, b := strings.Split(value, "|"), strings.Split(other, "|")
		if a[0] != b[0] { changedStatus++ }
		if a[1] != b[1] { changedReceipt++ }
		if a[2] != b[2] { changedWitness++ }
	}
	t.Logf("core-e2e: diagnostic counts: proofs=%d/%d missing=%d status_changes=%d receipt_changes=%d witness_changes=%d tasks=%d/%d invoices=%d/%d locks=%d/%d",
		len(source.Proofs), len(recovered.Proofs), missing, changedStatus, changedReceipt, changedWitness,
		len(source.Tasks), len(recovered.Tasks), len(source.Invoices), len(recovered.Invoices), len(source.Locked), len(recovered.Locked))
}

func coreCheckpoint(t *testing.T, name string, source *Manager, cfg coreE2EConfig, chain *coreE2EChain, material *coreRecoveryMaterial) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		beforeBroadcasts := chain.broadcastCount()
		coreVerifyPaidRecords(t, source, cfg)
		target := coreRestoreE2E(t, cfg, chain, material)
		coreSynchronizeRestoredAccount(t, target)
		coreAssert(t, coreDigest(coreCanonicalCatalog(source)) == coreDigest(coreCanonicalCatalog(target)), "recovered wallet/subaccount catalog is not exact")
		sourceWallet, sourceAccount := source.status.CurrentWallet, source.status.CurrentAccount
		defer func() { _ = source.SwitchWallet(sourceWallet, coreE2EPassword); source.SwitchAccount(sourceAccount) }()
		targetCatalog := target.GetWalletCatalog()
		for _, entry := range source.GetWalletCatalog() {
			var restoredID int64
			for _, candidate := range targetCatalog {
				if candidate.Fingerprint == entry.Fingerprint { restoredID = candidate.ID }
			}
			coreAssert(t, restoredID != 0, "missing restored wallet fingerprint")
			coreRequire(t, "select source scope", source.SwitchWallet(entry.ID, coreE2EPassword))
			coreRequire(t, "select restored scope", target.SwitchWallet(restoredID, coreE2EPassword))
			for _, sub := range entry.Accounts {
				source.SwitchAccount(sub.Index)
				target.SwitchAccount(sub.Index)
				a, b := coreRGBSemantic(t, source), coreRGBSemantic(t, target)
				if coreDigest(a) != coreDigest(b) {
					coreDescribeDifference(t, a, b)
					t.Errorf("core-e2e: %s scope account=%d recovery mismatch: assets=%t available=%t pending=%t proofs=%t tasks=%t invoices=%t locks=%t",
						name, sub.Index, coreDigest(a.Assets) == coreDigest(b.Assets), coreDigest(a.Available) == coreDigest(b.Available), coreDigest(a.Pending) == coreDigest(b.Pending),
						coreDigest(a.Proofs) == coreDigest(b.Proofs), coreDigest(a.Tasks) == coreDigest(b.Tasks), coreDigest(a.Invoices) == coreDigest(b.Invoices), coreDigest(a.Locked) == coreDigest(b.Locked))
				}
			}
		}
		coreAssert(t, chain.broadcastCount() == beforeBroadcasts, "recovery synchronization caused an unexpected broadcast")
	})
}

type coreOfflineHTTP struct { HttpClient }
func (coreOfflineHTTP) SendGetRequest(*URL) ([]byte, error) { return nil, errors.New("intentional offline read") }
func (coreOfflineHTTP) SendPostRequest(*URL, []byte) ([]byte, error) { return nil, errors.New("intentional offline write") }
