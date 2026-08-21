package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type mailboxSyncHTTP struct {
	remote *rgb11MemoryDKVSHTTP

	mu              sync.Mutex
	syncFilters     [][]dkvsindexer.Subscription
	watchFilters    [][]dkvsindexer.Subscription
	pathWatchCall   int
	injected        []*swire.DKVSRecord
	bestHeight      int64
	bestHeightErr   error
	bestHeightCalls int
}

func (h *mailboxSyncHTTP) SendGetRequest(url *URL) ([]byte, error) {
	if strings.HasSuffix(url.Path, "/btc/block/bestblockheight") {
		h.mu.Lock()
		h.bestHeightCalls++
		height, err := h.bestHeight, h.bestHeightErr
		h.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if height == 0 {
			height = 2
		}
		return rgb11DKVSResponse(0, "ok", height, 0)
	}
	return h.remote.SendGetRequest(url)
}

func (h *mailboxSyncHTTP) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	switch {
	case strings.HasSuffix(url.Path, "/v3/dkvs/sync"):
		var request DKVSSyncRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.syncFilters = append(h.syncFilters,
			append([]dkvsindexer.Subscription(nil), request.Filters...))
		injected := append([]*swire.DKVSRecord(nil), h.injected...)
		h.mu.Unlock()
		if len(injected) != 0 {
			root, err := dkvsindexer.DirectoryRootFromRecords(injected, 0)
			if err != nil {
				return nil, err
			}
			return rgb11DKVSResponse(0, "ok", &DKVSSyncPage{
				Records: injected, Done: true, Root: root.String(),
			}, 0)
		}
	case strings.HasSuffix(url.Path, "/v3/dkvs/watch"):
		var request DKVSWatchRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.watchFilters = append(h.watchFilters,
			append([]dkvsindexer.Subscription(nil), request.Filters...))
		h.mu.Unlock()
		return rgb11DKVSResponse(0, "ok", &DKVSWatchResult{
			Changed: false, Root: request.Root,
		}, 0)
	}
	return h.remote.SendPostRequest(url, body)
}

func (h *mailboxSyncHTTP) SendDKVSV1Get(path string, query map[string]string) ([]byte, error) {
	return h.remote.SendDKVSV1Get(path, query)
}

func (h *mailboxSyncHTTP) SendDKVSV1Post(path string, body []byte) ([]byte, error) {
	if path == "/v3/dkvs/watch/path" {
		h.mu.Lock()
		h.pathWatchCall++
		h.mu.Unlock()
	}
	return h.remote.SendDKVSV1Post(path, body)
}

func mailboxTestRecord(t *testing.T, owner *Manager, mailboxID, senderID, messageID string,
	value []byte) *swire.DKVSRecord {

	t.Helper()
	key, err := dkvsindexer.MailMsgKey(mailboxID, senderID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDKVSAccountSignedRecord(owner.wallet, key, value,
		dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 1, TTL: testRGB11FreeLocalTTL})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestDKVSListMailboxVerifiedFiltersTombstones(t *testing.T) {
	receiverPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	receiver := newRGB11MultiDeviceManager(t, receiverPriv, 1091)
	sender := newRGB11MultiDeviceManager(t, senderPriv, 1092)
	remote := newRGB11MemoryDKVSHTTP()
	tracked := &mailboxSyncHTTP{remote: remote}
	configureRGB11DKVSTestManager(receiver, tracked)

	mailboxID, err := dkvsAccountID(receiver.wallet)
	if err != nil {
		t.Fatal(err)
	}
	senderID, err := dkvsAccountID(sender.wallet)
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.MailMsgKey(mailboxID, senderID, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	tombstone, err := NewDKVSAccountSignedRecord(receiver.wallet, key, nil,
		dkvsindexer.RecordOptions{
			Seq:         2,
			IssueHeight: 1,
			TTL:         testRGB11FreeLocalTTL,
			Flags:       dkvsindexer.FlagTombstone,
		})
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.records[tombstone.Key] = cloneRGB11DKVSRecord(tombstone)
	remote.mu.Unlock()

	store, err := receiver.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	values, err := store.ListMailboxVerified(mailboxID,
		dkvsindexer.RecordVerificationOptions{Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatalf("mailbox returned tombstone as active value: %+v", values)
	}
}

func TestDKVSListMailboxVerifiedRejectsSenderSignedTombstone(t *testing.T) {
	receiverPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	receiver := newRGB11MultiDeviceManager(t, receiverPriv, 1093)
	sender := newRGB11MultiDeviceManager(t, senderPriv, 1094)
	remote := newRGB11MemoryDKVSHTTP()
	tracked := &mailboxSyncHTTP{remote: remote}
	configureRGB11DKVSTestManager(receiver, tracked)

	mailboxID, err := dkvsAccountID(receiver.wallet)
	if err != nil {
		t.Fatal(err)
	}
	senderID, err := dkvsAccountID(sender.wallet)
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.MailMsgKey(mailboxID, senderID, strings.Repeat("f", 64))
	if err != nil {
		t.Fatal(err)
	}
	// The sender owns the original msg record but does not own the receiver's
	// mailbox deletion authority. Mail tombstones are signed by mailbox owner.
	forgedDelete, err := NewDKVSAccountSignedRecord(sender.wallet, key, nil,
		dkvsindexer.RecordOptions{
			Seq:         2,
			IssueHeight: 1,
			TTL:         testRGB11FreeLocalTTL,
			Flags:       dkvsindexer.FlagTombstone,
		})
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.records[forgedDelete.Key] = cloneRGB11DKVSRecord(forgedDelete)
	remote.mu.Unlock()

	store, err := receiver.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ListMailboxVerified(mailboxID,
		dkvsindexer.RecordVerificationOptions{Height: 1})
	if !errors.Is(err, dkvsindexer.ErrInvalidSignature) {
		t.Fatalf("sender-signed mailbox tombstone err=%v, want invalid signature", err)
	}
}

func TestDKVSListMailboxVerifiedSyncsAggregateScope(t *testing.T) {
	receiverPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderAPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderBPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	receiver := newRGB11MultiDeviceManager(t, receiverPriv, 1101)
	senderA := newRGB11MultiDeviceManager(t, senderAPriv, 1102)
	senderB := newRGB11MultiDeviceManager(t, senderBPriv, 1103)
	remote := newRGB11MemoryDKVSHTTP()
	tracked := &mailboxSyncHTTP{remote: remote}
	configureRGB11DKVSTestManager(receiver, tracked)

	mailboxID, err := dkvsAccountID(receiver.wallet)
	if err != nil {
		t.Fatal(err)
	}
	senderAID, err := dkvsAccountID(senderA.wallet)
	if err != nil {
		t.Fatal(err)
	}
	senderBID, err := dkvsAccountID(senderB.wallet)
	if err != nil {
		t.Fatal(err)
	}
	messageA := strings.Repeat("a", 64)
	messageB := strings.Repeat("b", 64)
	recordA := mailboxTestRecord(t, senderA, mailboxID, senderAID, messageA, []byte("delivery-a"))
	recordB := mailboxTestRecord(t, senderB, mailboxID, senderBID, messageB, []byte("delivery-b"))
	shareKey, err := dkvsindexer.MailShareKey(mailboxID, "package", "share")
	if err != nil {
		t.Fatal(err)
	}
	share, err := NewDKVSAccountSignedRecord(receiver.wallet, shareKey, []byte("share"),
		dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 1, TTL: testRGB11FreeLocalTTL})
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.records[recordA.Key] = cloneRGB11DKVSRecord(recordA)
	remote.records[recordB.Key] = cloneRGB11DKVSRecord(recordB)
	remote.records[share.Key] = cloneRGB11DKVSRecord(share)
	remote.mu.Unlock()

	mailboxResult, err := receiver.SyncConfiguredRGB11AddressMailbox(
		context.Background(), dkvsindexer.RecordVerificationOptions{Height: 1},
		RGB11AddressDeliveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if mailboxResult.Scanned != 2 {
		t.Fatalf("mailbox scanned=%d, want only 2 msg records", mailboxResult.Scanned)
	}

	store, err := receiver.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	values, err := store.ListMailboxVerified(mailboxID,
		dkvsindexer.RecordVerificationOptions{Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	gotKeys := make(map[string]struct{}, len(values))
	for _, value := range values {
		gotKeys[value.Key] = struct{}{}
	}
	if len(values) != 2 {
		t.Fatalf("mailbox values=%+v", values)
	}
	if _, ok := gotKeys[recordA.Key]; !ok {
		t.Fatalf("mailbox is missing sender A record: %+v", values)
	}
	if _, ok := gotKeys[recordB.Key]; !ok {
		t.Fatalf("mailbox is missing sender B record: %+v", values)
	}

	target := "/mail/" + mailboxID
	filters := []dkvsindexer.Subscription{{
		Type: dkvsindexer.SubscriptionMailbox, Target: target,
	}}
	scope := dkvsReplicaScope(store.client.replicaNamespace, filters)
	confirmed, err := newDKVSReplicaStore(receiver.db).loadConfirmed(scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmed) != 3 {
		t.Fatalf("aggregate replica records=%d, want 3", len(confirmed))
	}

	tracked.mu.Lock()
	if tracked.bestHeightCalls == 0 {
		t.Fatalf("mailbox sync did not query endpoint bestheight")
	}
	if len(tracked.syncFilters) != 1 || len(tracked.syncFilters[0]) != 1 ||
		tracked.syncFilters[0][0] != filters[0] {
		t.Fatalf("sync filters=%+v", tracked.syncFilters)
	}
	tracked.mu.Unlock()
	for _, record := range confirmed {
		if strings.HasPrefix(record.Key, "/tmp/") {
			t.Fatalf("mailbox sync included tmp record %s", record.Key)
		}
	}

	root, err := newDKVSReplicaStore(receiver.db).loadRoot(scope)
	if err != nil {
		t.Fatal(err)
	}
	if !receiver.ensureDKVSManager().watch([]dkvsDirectoryState{{
		Prefix: target, Root: root, Scope: scope, Filters: filters,
	}}, make(chan struct{})) {
		t.Fatal("mailbox watch stopped unexpectedly")
	}
	tracked.mu.Lock()
	defer tracked.mu.Unlock()
	if len(tracked.watchFilters) != 1 || len(tracked.watchFilters[0]) != 1 ||
		tracked.watchFilters[0][0] != filters[0] {
		t.Fatalf("watch filters=%+v", tracked.watchFilters)
	}
	if tracked.pathWatchCall != 0 {
		t.Fatalf("mailbox watch used WatchPath %d times", tracked.pathWatchCall)
	}
}

func TestDKVSListMailboxVerifiedManagerRebuildResyncsRemote(t *testing.T) {
	receiverPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	receiver := newRGB11MultiDeviceManager(t, receiverPriv, 1111)
	sender := newRGB11MultiDeviceManager(t, senderPriv, 1112)
	remote := newRGB11MemoryDKVSHTTP()
	tracked := &mailboxSyncHTTP{remote: remote}
	configureRGB11DKVSTestManager(receiver, tracked)
	mailboxID, _ := dkvsAccountID(receiver.wallet)
	senderID, _ := dkvsAccountID(sender.wallet)
	record := mailboxTestRecord(t, sender, mailboxID, senderID, strings.Repeat("c", 64), []byte("delivery"))
	remote.mu.Lock()
	remote.records[record.Key] = cloneRGB11DKVSRecord(record)
	remote.mu.Unlock()

	firstStore, err := receiver.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstStore.ListMailboxVerified(mailboxID,
		dkvsindexer.RecordVerificationOptions{Height: 1}); err != nil {
		t.Fatal(err)
	}
	oldManager := receiver.dkvs
	receiver.dkvs = newDKVSManager(receiver)
	t.Cleanup(func() {
		releaseDKVSManagerRuntime(oldManager)
		releaseDKVSManagerRuntime(receiver.dkvs)
	})
	secondStore, err := receiver.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	values, err := secondStore.ListMailboxVerified(mailboxID,
		dkvsindexer.RecordVerificationOptions{Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Key != record.Key {
		t.Fatalf("rebuilt mailbox values=%+v", values)
	}
	tracked.mu.Lock()
	defer tracked.mu.Unlock()
	if len(tracked.syncFilters) != 2 {
		t.Fatalf("manager rebuild sync calls=%d, want 2", len(tracked.syncFilters))
	}
}

func TestDKVSListMailboxVerifiedFailsClosedWhenBestHeightUnavailable(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	receiver := newRGB11MultiDeviceManager(t, priv, 1131)
	tracked := &mailboxSyncHTTP{
		remote:        newRGB11MemoryDKVSHTTP(),
		bestHeightErr: errors.New("bestheight unavailable"),
	}
	configureRGB11DKVSTestManager(receiver, tracked)
	mailboxID, err := dkvsAccountID(receiver.wallet)
	if err != nil {
		t.Fatal(err)
	}
	store, err := receiver.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListMailboxVerified(mailboxID,
		dkvsindexer.RecordVerificationOptions{Height: 1}); err == nil ||
		!strings.Contains(err.Error(), "bestheight unavailable") {
		t.Fatalf("bestheight failure was not returned: %v", err)
	}

	tracked.mu.Lock()
	defer tracked.mu.Unlock()
	if len(tracked.syncFilters) != 0 {
		t.Fatalf("mailbox sync applied records after bestheight failure: %d", len(tracked.syncFilters))
	}
}

func TestDKVSListMailboxVerifiedRejectsOutsideRecordAtomically(t *testing.T) {
	receiverPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	otherPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	receiver := newRGB11MultiDeviceManager(t, receiverPriv, 1121)
	other := newRGB11MultiDeviceManager(t, otherPriv, 1122)
	sender := newRGB11MultiDeviceManager(t, senderPriv, 1123)
	remote := newRGB11MemoryDKVSHTTP()
	tracked := &mailboxSyncHTTP{remote: remote}
	configureRGB11DKVSTestManager(receiver, tracked)
	mailboxID, _ := dkvsAccountID(receiver.wallet)
	otherID, _ := dkvsAccountID(other.wallet)
	senderID, _ := dkvsAccountID(sender.wallet)
	tracked.injected = []*swire.DKVSRecord{
		mailboxTestRecord(t, sender, otherID, senderID, strings.Repeat("d", 64), []byte("outside")),
	}

	store, err := receiver.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ListMailboxVerified(mailboxID,
		dkvsindexer.RecordVerificationOptions{Height: 1})
	if !errors.Is(err, dkvsindexer.ErrInvalidKey) {
		t.Fatalf("outside mailbox record err=%v", err)
	}
	filters := []dkvsindexer.Subscription{{
		Type: dkvsindexer.SubscriptionMailbox, Target: "/mail/" + mailboxID,
	}}
	scope := dkvsReplicaScope(store.client.replicaNamespace, filters)
	confirmed, loadErr := newDKVSReplicaStore(receiver.db).loadConfirmed(scope)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(confirmed) != 0 || receiver.ensureDKVSManager().scopeReady(scope) {
		t.Fatalf("failed mailbox sync published records=%d ready=%v",
			len(confirmed), receiver.ensureDKVSManager().scopeReady(scope))
	}
}
