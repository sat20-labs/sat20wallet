package wallet

import (
	"context"
	"errors"
	"net"
	"testing"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func captureDKVSOutboxPanic(fn func()) (recovered any) {
	defer func() { recovered = recover() }()
	fn()
	return nil
}

func testDKVSOutboxEntry(t *testing.T) (*Manager, *SatsNetDKVSClient,
	*dkvsReplicaStore, *DKVSBatchOutboxEntry) {
	t.Helper()
	manager, _ := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	key := accountTestKey(t, manager, "wallet/fail-fast")
	mutation := dkvsindexer.CASMutation{
		Record:       freeLocalRecord(t, manager, key, 1, "value"),
		Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true},
	}
	entry, err := newDKVSBatchOutboxEntryFinal(client.replicaNamespace,
		[]dkvsindexer.CASMutation{mutation}, "test-endpoint", dkvsOutboxOrigin{})
	if err != nil {
		t.Fatal(err)
	}
	store := newDKVSReplicaStore(manager.db)
	if err := store.QueueOutbox(entry); err != nil {
		t.Fatal(err)
	}
	return manager, client, store, entry
}

func TestDKVSOutboxRetriesTransientServiceFailures(t *testing.T) {
	networkErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	if got := classifyDKVSOutboxError(networkErr); got != dkvsOutboxTransient {
		t.Fatalf("network error class=%d", got)
	}
	if got := classifyDKVSOutboxError(context.DeadlineExceeded); got != dkvsOutboxTransient {
		t.Fatalf("deadline error class=%d", got)
	}
	if got := classifyDKVSOutboxError(dkvsindexer.ErrWriteConflict); got != dkvsOutboxConflict {
		t.Fatalf("conflict error class=%d", got)
	}
	for _, status := range []int{408, 425, 429, 500, 502, 503, 504} {
		if got := classifyDKVSOutboxError(&HTTPResponseError{StatusCode: status}); got != dkvsOutboxTransient {
			t.Fatalf("HTTP %d error class=%d", status, got)
		}
	}
	for _, err := range []error{
		errors.New("unknown failure"),
		dkvsindexer.ErrInvalidRecord,
		dkvsindexer.ErrStorageModeDowngrade,
		&HTTPResponseError{StatusCode: 400, Body: []byte("invalid request")},
	} {
		if got := classifyDKVSOutboxError(err); got != dkvsOutboxPermanent {
			t.Fatalf("permanent error %v class=%d", err, got)
		}
	}
}

func TestAccountAutopayExpiryPausesOutboxUntilFunding(t *testing.T) {
	submissionErr := dkvsindexer.ErrInvalidFeeProof
	status := &AccountAutopayFundingStatus{
		Required: true, NeedsFunding: true, CanFund: true,
		Reason: AccountAutopayReasonPaymentExpired, Message: "expired",
	}
	err := accountAutopaySubmissionDecision(submissionErr, status, nil)
	if !errors.Is(err, ErrAccountAutopayFundingRequired) {
		t.Fatalf("expired AUTOPAY decision=%v", err)
	}
	if !isDKVSNonRetryableSyncError(err) {
		t.Fatal("expired AUTOPAY outbox would retry without waiting for funding")
	}

	status.Ready = true
	status.NeedsFunding = false
	status.CanFund = false
	if err := accountAutopaySubmissionDecision(submissionErr, status, nil); !errors.Is(err, submissionErr) {
		t.Fatalf("healthy AUTOPAY masked permanent record error: %v", err)
	}

	networkErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	if err := accountAutopaySubmissionDecision(submissionErr, nil, networkErr); !errors.Is(err, networkErr) {
		t.Fatalf("AUTOPAY status network failure decision=%v", err)
	}
}

func TestDKVSOutboxPermanentFailurePanicsWithoutCreatingTerminal(t *testing.T) {
	_, _, store, entry := testDKVSOutboxEntry(t)
	if recovered := captureDKVSOutboxPanic(func() {
		_ = markDKVSOutboxSubmissionFailure(store, entry,
			dkvsindexer.ErrStorageModeDowngrade)
	}); recovered == nil {
		t.Fatal("permanent DKVS outbox failure did not panic")
	}
	entries, err := store.LoadOutbox(entry.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].State == DKVSOutboxTerminal {
		t.Fatalf("permanent failure created terminal outbox: %+v", entries)
	}
}

func TestDKVSOutboxNetworkFailureRemainsPending(t *testing.T) {
	_, _, store, entry := testDKVSOutboxEntry(t)
	networkErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	if err := markDKVSOutboxSubmissionFailure(store, entry, networkErr); err != nil {
		t.Fatal(err)
	}
	entries, err := store.LoadOutbox(entry.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].State != DKVSOutboxPending {
		t.Fatalf("network failure outbox=%+v", entries)
	}
}

func TestDKVSLegacyTerminalOutboxPanicsImmediately(t *testing.T) {
	manager, client, store, entry := testDKVSOutboxEntry(t)
	if err := store.UpdateOutboxState(entry, DKVSOutboxTerminal,
		dkvsindexer.ErrStorageModeDowngrade); err != nil {
		t.Fatal(err)
	}
	if recovered := captureDKVSOutboxPanic(func() {
		_, _ = manager.flushDKVSBatchOutbox(client, store)
	}); recovered == nil {
		t.Fatal("legacy terminal outbox did not panic")
	}
}

func TestDKVSOutboxHTTPFailureRemainsPending(t *testing.T) {
	_, _, store, entry := testDKVSOutboxEntry(t)
	for _, status := range []int{429, 500, 502, 503, 504} {
		if err := markDKVSOutboxSubmissionFailure(store, entry, &HTTPResponseError{StatusCode: status}); err != nil {
			t.Fatal(err)
		}
		entries, err := store.LoadOutbox(entry.Namespace)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].State != DKVSOutboxPending || entries[0].RequestID != entry.RequestID {
			t.Fatalf("HTTP %d lost retry state: %+v", status, entries)
		}
	}
}
