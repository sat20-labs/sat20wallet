package wallet

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func finalDKVSTestManager(t *testing.T) (*Manager, *rgb11MemoryDKVSHTTP) {
	t.Helper()
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 700)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(manager, remote)
	return manager, remote
}

func accountTestKey(t *testing.T, manager *Manager, path string) string {
	t.Helper()
	accountID, err := dkvsAccountID(manager.wallet)
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.AccountPersonalKey(accountID, path)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func freeLocalRecord(t *testing.T, manager *Manager, key string, seq uint64, value string) *dkvsindexer.Record {
	t.Helper()
	record, err := newDKVSAccountSignedRecordWithFreeLocal(manager.wallet, key, []byte(value),
		dkvsindexer.RecordOptions{Seq: seq, IssueHeight: 1, TTL: testRGB11FreeLocalTTL})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func canonicalTestRecord(t *testing.T, manager *Manager, key string, seq uint64, value string) *dkvsindexer.Record {
	t.Helper()
	record, err := NewDKVSSignedRecord(manager.wallet, key, []byte(value),
		dkvsindexer.RecordOptions{Seq: seq, IssueHeight: 1})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestDKVSFinalManagedPrefixesPollGenerations(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	keyA := accountTestKey(t, manager, "wallet/catalog")
	keyB := accountTestKey(t, manager, "settings/ui")
	prefixA, _, err := dkvsManagedPathForKey(keyA)
	if err != nil {
		t.Fatal(err)
	}
	prefixB, _, err := dkvsManagedPathForKey(keyB)
	if err != nil {
		t.Fatal(err)
	}
	seed := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	if _, err := seed.PutRecord(freeLocalRecord(t, manager, keyA, 1, "a1")); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.PutRecord(canonicalTestRecord(t, manager, keyB, 1, "b1")); err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(prefixA); err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(prefixB); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	expectedPrefixes, err := normalizeWalletSubscriptionPrefixes([]string{prefixA, prefixB})
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.GetDKVSSubscriptionStatus()
	if err != nil || !subscriptionReadyForPrefixes(state, expectedPrefixes) {
		t.Fatalf("initial subscription state=%+v err=%v", state, err)
	}
	remote.mu.Lock()
	if remote.statusCalls != 0 || remote.snapshotCalls != 2 {
		t.Fatalf("initial status calls=%d snapshot calls=%d", remote.statusCalls, remote.snapshotCalls)
	}
	remote.mu.Unlock()
	store := &dkvsStore{manager: manager.dkvs, client: client}
	value, err := store.Get(keyA)
	if err != nil || value.Seq != 1 || string(value.Value) != "a1" {
		t.Fatalf("initial local value=%+v err=%v", value, err)
	}

	if _, err := seed.PutRecord(freeLocalRecord(t, manager, keyA, 2, "a2")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	value, err = store.Get(keyA)
	if err != nil || value.Seq != 2 || string(value.Value) != "a2" {
		t.Fatalf("delta value=%+v err=%v", value, err)
	}
	remote.mu.Lock()
	if remote.statusCalls != 1 || remote.snapshotCalls != 3 {
		t.Fatalf("poll status calls=%d snapshot calls=%d", remote.statusCalls, remote.snapshotCalls)
	}
	remote.mu.Unlock()

	remote.mu.Lock()
	remote.endpointID = "offline-simulated-endpoint"
	remote.mu.Unlock()
	value, err = store.Get(keyA)
	if err != nil || value.Seq != 2 {
		t.Fatalf("offline local read=%+v err=%v", value, err)
	}

	prefixes, err := manager.ListSubscribedDKVSPrefixes()
	if err != nil || len(prefixes) != 2 {
		t.Fatalf("prefixes=%v err=%v", prefixes, err)
	}
}

func TestDKVSFinalLocalPutAdvancesTokenWithoutSnapshotDownload(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "wallet/local-put")
	prefix, _, err := dkvsManagedPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	store, err := manager.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Put(dkvsValueMutation{
		Key: key, Value: []byte("local-v1"), Owner: manager.wallet,
		Signature: dkvsSignatureAccount,
	})
	if err != nil || value.Seq != 1 {
		t.Fatalf("local put value=%+v err=%v", value, err)
	}
	if err := store.WaitReady(key); err != nil {
		t.Fatal(err)
	}
	local, err := store.Get(key)
	if err != nil || local.Seq != 1 || string(local.Value) != "local-v1" {
		t.Fatalf("local replica value=%+v err=%v", local, err)
	}
	if err := store.SyncCurrent(key); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	statusCalls, snapshotCalls := remote.statusCalls, remote.snapshotCalls
	remote.mu.Unlock()
	if statusCalls != 1 || snapshotCalls != 1 {
		t.Fatalf("local PUT triggered snapshot: status=%d snapshot=%d", statusCalls, snapshotCalls)
	}
	state, err := manager.GetDKVSSubscriptionStatus()
	if err != nil || state.Generations[prefix] != 1 {
		t.Fatalf("subscription state=%+v err=%v", state, err)
	}

	seed := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	if _, err := seed.PutRecord(canonicalTestRecord(t, manager, key, 2, "remote-v2")); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncCurrent(key); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	statusCalls, snapshotCalls = remote.statusCalls, remote.snapshotCalls
	remote.mu.Unlock()
	if statusCalls != 2 || snapshotCalls != 2 {
		t.Fatalf("remote change was not synchronized selectively: status=%d snapshot=%d",
			statusCalls, snapshotCalls)
	}
	current, err := store.Get(key)
	if err != nil || current.Seq != 2 || string(current.Value) != "remote-v2" {
		t.Fatalf("current replica value=%+v err=%v", current, err)
	}
}

func TestDKVSFinalResponseLossReplaysOutboxBeforePrefixStatus(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "wallet/response-loss")
	prefix, _, err := dkvsManagedPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	store, err := manager.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.postCommitErr = &net.OpError{Op: "read", Net: "tcp", Err: errors.New("response lost")}
	remote.mu.Unlock()
	if _, err := store.Put(dkvsValueMutation{
		Key: key, Value: []byte("committed"), Owner: manager.wallet,
		Signature: dkvsSignatureAccount,
	}); err == nil {
		t.Fatal("response loss did not surface as a network error")
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	local, err := store.Get(key)
	if err != nil || local.Seq != 1 || string(local.Value) != "committed" {
		t.Fatalf("replayed local value=%+v err=%v", local, err)
	}
	remote.mu.Lock()
	statusCalls, snapshotCalls := remote.statusCalls, remote.snapshotCalls
	remote.mu.Unlock()
	if statusCalls != 1 || snapshotCalls != 1 {
		t.Fatalf("response-loss replay triggered snapshot: status=%d snapshot=%d",
			statusCalls, snapshotCalls)
	}
	entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(store.client.replicaNamespace)
	if err != nil || len(entries) != 0 {
		t.Fatalf("replayed outbox=%+v err=%v", entries, err)
	}
}

func TestDKVSFinalNewEmptyPrefixStillGetsSnapshotAndBecomesReady(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	seededKey := accountTestKey(t, manager, "seeded/value")
	seededPrefix, _, err := dkvsManagedPathForKey(seededKey)
	if err != nil {
		t.Fatal(err)
	}
	emptyKey := accountTestKey(t, manager, "empty/value")
	emptyPrefix, _, err := dkvsManagedPathForKey(emptyKey)
	if err != nil {
		t.Fatal(err)
	}
	seed := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	if _, err := seed.PutRecord(canonicalTestRecord(t, manager, seededKey, 1, "one")); err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(seededPrefix); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(emptyPrefix); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	expectedPrefixes, err := normalizeWalletSubscriptionPrefixes([]string{emptyPrefix, seededPrefix})
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.GetDKVSSubscriptionStatus()
	if err != nil || !subscriptionReadyForPrefixes(state, expectedPrefixes) {
		t.Fatalf("subscription state=%+v err=%v", state, err)
	}
	remote.mu.Lock()
	snapshotCalls := remote.snapshotCalls
	remote.mu.Unlock()
	if snapshotCalls != 2 {
		t.Fatalf("snapshot calls=%d, want initial seeded plus newly registered empty prefix", snapshotCalls)
	}
}

func TestDKVSFinalEndpointSwitchForcesEveryManagedPrefixSnapshot(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	firstKey := accountTestKey(t, manager, "first/value")
	secondKey := accountTestKey(t, manager, "second/value")
	firstPrefix, _, err := dkvsManagedPathForKey(firstKey)
	if err != nil {
		t.Fatal(err)
	}
	secondPrefix, _, err := dkvsManagedPathForKey(secondKey)
	if err != nil {
		t.Fatal(err)
	}
	seed := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	if _, err := seed.PutRecord(canonicalTestRecord(t, manager, firstKey, 1, "one")); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.PutRecord(canonicalTestRecord(t, manager, secondKey, 1, "two")); err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(firstPrefix); err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(secondPrefix); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.endpointID = "replacement-endpoint"
	remote.mu.Unlock()
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	snapshotCalls := remote.snapshotCalls
	remote.mu.Unlock()
	if snapshotCalls != 4 {
		t.Fatalf("snapshot calls=%d, want two initial plus two after endpoint switch", snapshotCalls)
	}
	expectedPrefixes, err := normalizeWalletSubscriptionPrefixes([]string{firstPrefix, secondPrefix})
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.GetDKVSSubscriptionStatus()
	if err != nil || state.EndpointID != "replacement-endpoint" ||
		!subscriptionReadyForPrefixes(state, expectedPrefixes) {
		t.Fatalf("switched subscription state=%+v err=%v", state, err)
	}
}

func TestDKVSFinalUnsubscribedGetDoesNotRegisterPrefix(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "direct/value")
	seed := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	if _, err := seed.PutRecord(freeLocalRecord(t, manager, key, 1, "direct")); err != nil {
		t.Fatal(err)
	}
	store, err := manager.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(key)
	if err != nil || string(value.Value) != "direct" {
		t.Fatalf("direct read=%+v err=%v", value, err)
	}
	prefixes, err := manager.ListSubscribedDKVSPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 0 {
		t.Fatalf("ordinary Get implicitly subscribed prefixes: %v", prefixes)
	}
	remote.mu.Lock()
	statusCalls, snapshotCalls := remote.statusCalls, remote.snapshotCalls
	remote.mu.Unlock()
	if statusCalls != 0 || snapshotCalls != 0 {
		t.Fatalf("unsubscribed Get started managed sync: status=%d snapshot=%d", statusCalls, snapshotCalls)
	}
}

func TestDKVSFinalUnmanagedReadUsesOneMinuteCache(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "direct/cache")
	seed := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	if _, err := seed.PutRecord(canonicalTestRecord(t, manager, key, 1, "one")); err != nil {
		t.Fatal(err)
	}
	store, err := manager.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Get(key)
	if err != nil || string(first.Value) != "one" {
		t.Fatalf("first read=%+v err=%v", first, err)
	}
	if _, err := seed.PutRecord(canonicalTestRecord(t, manager, key, 2, "two")); err != nil {
		t.Fatal(err)
	}
	cached, err := store.Get(key)
	if err != nil || cached.Seq != 1 || string(cached.Value) != "one" {
		t.Fatalf("cached read=%+v err=%v", cached, err)
	}
	cacheKey := store.client.unmanagedReadCacheNamespace() + "\x00" + key
	manager.dkvs.readCacheMu.Lock()
	entry := manager.dkvs.readCache[cacheKey]
	entry.fetchedAt = time.Now().Add(-2 * dkvsUnmanagedReadCacheTTL)
	manager.dkvs.readCache[cacheKey] = entry
	manager.dkvs.readCacheMu.Unlock()
	current, err := store.Get(key)
	if err != nil || current.Seq != 2 || string(current.Value) != "two" {
		t.Fatalf("refreshed read=%+v err=%v", current, err)
	}
}

func TestDKVSFinalUnmanagedReadCacheIsEndpointScoped(t *testing.T) {
	first := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", newRGB11MemoryDKVSHTTP())
	second := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", newRGB11MemoryDKVSHTTP())
	first.replicaNamespace = "testnet:satsnet"
	second.replicaNamespace = first.replicaNamespace
	first.rememberEndpointID("endpoint-a")
	second.rememberEndpointID("endpoint-b")
	if first.unmanagedReadCacheNamespace() == second.unmanagedReadCacheNamespace() {
		t.Fatal("unmanaged read cache namespace is shared across endpoints")
	}
}

func TestDKVSFinalCASConflictRefreshesRebuildsAndRetries(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "direct/rebase")
	seed := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	if _, err := seed.PutRecord(canonicalTestRecord(t, manager, key, 1, "one")); err != nil {
		t.Fatal(err)
	}
	store, err := manager.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	builds := 0
	values, err := store.Update([]string{key}, func(current map[string]*dkvsValue,
		next map[string]uint64) ([]dkvsValueMutation, error) {
		builds++
		if builds == 1 {
			if _, putErr := seed.PutRecord(canonicalTestRecord(t, manager, key, 2, "concurrent")); putErr != nil {
				return nil, putErr
			}
		}
		return []dkvsValueMutation{{
			Key: key, Value: []byte(fmt.Sprintf("rebuilt-%d-%d", builds, next[key])),
			Owner: manager.wallet, Signature: dkvsSignatureAccount,
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if builds != 2 || len(values) != 1 || values[0].Seq != 3 || string(values[0].Value) != "rebuilt-2-3" {
		t.Fatalf("builds=%d values=%+v", builds, values)
	}
	client, err := manager.dkvs.primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(client.replicaNamespace)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected CAS outbox entries=%+v err=%v", entries, err)
	}
}

func TestDKVSFinalExplicitRefreshForcesCurrentSnapshot(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "wallet/refresh-current")
	prefix, _, err := dkvsManagedPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	seed := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	if _, err := seed.PutRecord(canonicalTestRecord(t, manager, key, 1, "one")); err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(prefix); err != nil {
		t.Fatal(err)
	}
	store, err := manager.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Refresh(key); err != nil {
		t.Fatal(err)
	}
	first, err := store.Get(key)
	if err != nil || first.Seq != 1 || string(first.Value) != "one" {
		t.Fatalf("initial refresh value=%+v err=%v", first, err)
	}
	if _, err := seed.PutRecord(canonicalTestRecord(t, manager, key, 2, "two")); err != nil {
		t.Fatal(err)
	}
	stale, err := store.Get(key)
	if err != nil || stale.Seq != 1 {
		t.Fatalf("replica unexpectedly changed before explicit refresh: value=%+v err=%v", stale, err)
	}
	if err := store.Refresh(key); err != nil {
		t.Fatal(err)
	}
	current, err := store.Get(key)
	if err != nil || current.Seq != 2 || string(current.Value) != "two" {
		t.Fatalf("forced refresh value=%+v err=%v", current, err)
	}
}

func TestDKVSFinalMailboxReadDoesNotPersistManagedPrefix(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	recipientPubKey := manager.wallet.GetPubKey().SerializeCompressed()
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mailboxID := dkvsindexer.AccountID(recipientPubKey)
	senderID := dkvsindexer.AccountID(senderPriv.PubKey().SerializeCompressed())
	key, err := dkvsindexer.MailMsgKey(mailboxID, senderID, "0")
	if err != nil {
		t.Fatal(err)
	}
	remote.seedInternalMailboxRecord(&swire.DKVSRecord{
		Version: dkvsindexer.Version, Key: key, Value: []byte("message-manager-inner"),
		Seq: 1, IssueHeight: 1, TTL: 100,
	})
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	records, total, err := client.SubscribeMailbox(mailboxID)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(records) != 1 {
		t.Fatalf("mailbox records=%d total=%d", len(records), total)
	}
	prefixes, err := manager.ListSubscribedDKVSPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 0 {
		t.Fatalf("mailbox read persisted managed prefixes=%v", prefixes)
	}
}

func TestDKVSFinalManagedMailboxPollsPrefixGeneration(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	recipientPubKey := manager.wallet.GetPubKey().SerializeCompressed()
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mailboxID := dkvsindexer.AccountID(recipientPubKey)
	senderID := dkvsindexer.AccountID(senderPriv.PubKey().SerializeCompressed())
	key, err := dkvsindexer.MailMsgKey(mailboxID, senderID, "managed-0")
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := mailboxSubscriptionTarget(mailboxID)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	remote.seedInternalMailboxRecord(&swire.DKVSRecord{
		Version: dkvsindexer.Version, Key: key, Value: []byte("message-manager-inner"),
		Seq: 1, IssueHeight: 1, TTL: 100,
	})
	remote.mu.Lock()
	remote.generations[prefix]++
	remote.mu.Unlock()
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	// A mailbox kind is a query prefix over the registered mailbox snapshot,
	// not an independently managed PathMeta. Reading the child must therefore
	// use the parent mailbox generation that was synchronized above.
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	records, total, err := client.ReadMailboxMessages(mailboxID, 0, 10)
	if err != nil || total != 1 || len(records) != 1 || records[0].Key != key {
		t.Fatalf("managed mailbox child read records=%+v total=%d err=%v", records, total, err)
	}
	store, err := manager.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	values, err := store.ListMailboxVerified(mailboxID, dkvsindexer.RecordVerificationOptions{})
	if err != nil || len(values) != 1 || values[0].Key != key {
		t.Fatalf("managed mailbox values=%+v err=%v", values, err)
	}
	remote.mu.Lock()
	initialSnapshots := remote.snapshotCalls
	remote.mu.Unlock()
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	defer remote.mu.Unlock()
	if remote.snapshotCalls != initialSnapshots || remote.statusCalls == 0 {
		t.Fatalf("unchanged mailbox status calls=%d snapshot calls=%d initial=%d",
			remote.statusCalls, remote.snapshotCalls, initialSnapshots)
	}
}

func TestDKVSFinalAccountActivationRegistersReceiveAndMailboxPrefixes(t *testing.T) {
	manager, _ := finalDKVSTestManager(t)
	if err := manager.refreshDKVSRegistrations(); err != nil {
		t.Fatal(err)
	}
	accountID, err := dkvsAccountID(manager.wallet)
	if err != nil {
		t.Fatal(err)
	}
	mailboxPrefix, err := mailboxSubscriptionTarget(accountID)
	if err != nil {
		t.Fatal(err)
	}
	mappingKey, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, manager.wallet.GetAddress())
	if err != nil {
		t.Fatal(err)
	}
	mappingPrefix, _, err := dkvsManagedPathForKey(mappingKey)
	if err != nil {
		t.Fatal(err)
	}
	prefixes, err := manager.ListSubscribedDKVSPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{mailboxPrefix, mappingPrefix} {
		found := false
		for _, prefix := range prefixes {
			if prefix == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("activation prefix %s missing from %v", want, prefixes)
		}
	}
	manager.dkvs.mu.Lock()
	_, scheduled := manager.dkvs.jobs[dkvsAccountServiceJobPrefix+accountID]
	manager.dkvs.mu.Unlock()
	if !scheduled {
		t.Fatal("account receive/mailbox activation job was not scheduled")
	}

	manager.SwitchAccount(1)
	prefixes, err = manager.ListSubscribedDKVSPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	if manager.rootDKVSAccountID() != accountID {
		t.Fatalf("account switch changed root DKVS identity: got=%s want=%s",
			manager.rootDKVSAccountID(), accountID)
	}
	foundRootMailbox := false
	for _, prefix := range prefixes {
		foundRootMailbox = foundRootMailbox || prefix == mailboxPrefix
	}
	if !foundRootMailbox {
		t.Fatalf("account switch removed root DKVS mailbox %s", mailboxPrefix)
	}
}

func TestDKVSFinalOutboxUsesUniqueRequestIDAndPinsFreeLocalEndpoint(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	key := accountTestKey(t, manager, "wallet/outbox")
	prefix, _, err := dkvsManagedPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(prefix); err != nil {
		t.Fatal(err)
	}
	if err := manager.dkvs.forceCurrentPrefixes(client, []string{prefix}); err != nil {
		t.Fatal(err)
	}

	gate := make(chan struct{})
	remote.mu.Lock()
	remote.postGate = gate
	remote.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, putErr := client.PutRecord(freeLocalRecord(t, manager, key, 1, "queued"))
		done <- putErr
	}()
	store := newDKVSReplicaStore(manager.db)
	deadline := time.Now().Add(time.Second)
	var entry *DKVSBatchOutboxEntry
	for time.Now().Before(deadline) {
		entries, loadErr := store.LoadOutbox(client.replicaNamespace)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if len(entries) == 1 {
			entry = entries[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if entry == nil {
		t.Fatal("outbox entry was not persisted before network submission")
	}
	if entry.RequestID == "" || entry.RequestID == key || entry.EndpointID != "test-endpoint" {
		t.Fatalf("outbox identity=%+v", entry)
	}
	if _, err := manager.db.Read(dkvsOutboxKey(client.replicaNamespace, entry.RequestID)); err != nil {
		t.Fatalf("RequestID outbox key missing: %v", err)
	}

	remote.mu.Lock()
	remote.endpointID = "different-endpoint"
	remote.mu.Unlock()
	close(gate)
	if err := <-done; err == nil || !IsDKVSErrorCode(err, dkvsindexer.ErrorCodeLocalOnlyEndpointMismatch) {
		t.Fatalf("endpoint mismatch err=%v", err)
	}
	entries, err := store.LoadOutbox(client.replicaNamespace)
	if err != nil || len(entries) != 1 || entries[0].State != DKVSOutboxConflict {
		t.Fatalf("conflict outbox=%+v err=%v", entries, err)
	}
}

func TestDKVSFinalEndpointSwitchBlockedByActiveFreeLocal(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	key := accountTestKey(t, manager, "wallet/local-switch")
	prefix, _, err := dkvsManagedPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(prefix); err != nil {
		t.Fatal(err)
	}
	if err := manager.dkvs.forceCurrentPrefixes(client, []string{prefix}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PutRecord(freeLocalRecord(t, manager, key, 1, "local")); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.endpointID = "replacement-endpoint"
	remote.mu.Unlock()
	if _, err := manager.syncDKVSOnce(); !errors.Is(err, dkvsindexer.ErrLocalOnlyEndpointMismatch) {
		t.Fatalf("endpoint switch with FREE_LOCAL err=%v", err)
	}
}

func TestDKVSFinalUnsubscribePrunesSubscriptionReplica(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	key := accountTestKey(t, manager, "wallet/unsubscribe")
	prefix, _, err := dkvsManagedPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	seed := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	if _, err := seed.PutRecord(canonicalTestRecord(t, manager, key, 1, "value")); err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(prefix); err != nil {
		t.Fatal(err)
	}
	if err := manager.dkvs.forceCurrentPrefixes(client, []string{prefix}); err != nil {
		t.Fatal(err)
	}
	store := newDKVSReplicaStore(manager.db)
	if _, err := store.LoadSubscriptionRecord(client.replicaNamespace, key); err != nil {
		t.Fatal(err)
	}
	if err := manager.UnsubscribeDKVSPrefix(prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadSubscriptionRecord(client.replicaNamespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("unsubscribed replica record err=%v", err)
	}
}

func TestDKVSFinalArchitectureGuard(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	versionedProductionFile := regexp.MustCompile(`_v[0-9]+\.go$`)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		if strings.HasPrefix(text, "//go:build ignore") {
			t.Fatalf("DKVS package retains a build-ignore placeholder: %s", name)
		}
		if !strings.HasSuffix(name, "_test.go") && versionedProductionFile.MatchString(name) {
			t.Fatalf("DKVS implementation file has version suffix: %s", name)
		}
		if strings.HasSuffix(name, "_test.go") || !strings.Contains(name, "dkvs") {
			continue
		}
		for _, forbidden := range []string{
			"PathWritePrecondition", "EndpointPathState", "EndpointGeneration",
			"GetClientConfigV1", "PutRecordBatchCASV1", "WatchPath(",
			"/v3/dkvs/pathmeta", "/v3/dkvs/sync/path", "/v3/dkvs/watch/path",
			"/v3/dkvs/sync/directory", "/v3/dkvs/watch/directory",
			"/v3/dkvs/subscriptions/snapshot", "/v3/dkvs/subscriptions/watch",
			"dkvs-path-state-v1:", "dkvs-replica-confirmed-v1:", "dkvs-replica-root-v1:",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains forbidden final-DKVS token %q", name, forbidden)
			}
		}
	}
}
