package wallet

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// Retaining the pointer models a consumer paused immediately after sync.Map
// Load. The real public retry must retire that exact receipt permanently before
// its next RPC can re-enter the byte-identical created_inflight source.
func TestClosedFundingCreatedReturnedErrorRetiresLoadedReceiptBeforeExactReplay(t *testing.T) {
	m, old, client, database, _ := newClosedFundingReturnFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var exact []byte
	client.created = func(f *FundingReservation, call int) error {
		raw := closedFundingReturnJSON(t, f.FundingCreated)
		if call == 1 {
			exact = raw
			database.failWrite.Store(true)
		} else {
			if !bytes.Equal(exact, raw) {
				return errors.New("error replay replaced original FundingCreated")
			}
			if call != 2 {
				return errors.New("retired receipt released a live FundingCreated replay")
			}
			close(started)
			<-release
		}
		return errClosedFundingReturnRPC
	}
	if err := callClosedFundingReturn(m, old); !errors.Is(err, errClosedFundingReturnRPC) || !errors.Is(err, errClosedFundingReturnWrite) {
		t.Fatalf("fixture did not retain a returned error: %v", err)
	}
	owner, original := onlyClosedOpenOwner(t, m)
	value, ok := m.closedFundingCreatedReturns.Load(owner)
	if !ok {
		t.Fatal("known returned error was not retained")
	}
	stale := value.(*closedFundingCreatedReturn)
	database.failWrite.Store(false)
	m.serverNode.SetRPCClient(&closedFundingReturnPublicClient{client})
	m.bInited, m.actionMonitorRunning = true, true
	done := make(chan error, 1)
	go func() { _, err := m.ReopenChannel(false); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("real public error retry did not enter its second FundingCreated")
	}
	_, live := onlyClosedOpenOwner(t, m)
	if live.Step != "created_inflight" || live.source != original.source || client.openCalls.Load() != 1 || client.createdCalls.Load() != 2 {
		t.Fatal("fixture did not enter a live replay with byte-identical original source")
	}
	if _, ok := m.closedFundingCreatedReturns.Load(owner); ok {
		t.Fatal("successfully persisted error receipt still occupies the live owner slot")
	}
	if stale.busy.CompareAndSwap(false, true) {
		t.Error("retired returned-error receipt can be reclaimed by a delayed Load consumer while exact replay is live")
	}
	if _, err := m.ReopenChannel(false); err == nil || !strings.Contains(err.Error(), "created_inflight") {
		t.Fatalf("concurrent public call did not remain pending behind exact live replay: %v", err)
	}
	_, after := onlyClosedOpenOwner(t, m)
	if after.source != live.source || client.openCalls.Load() != 1 || client.createdCalls.Load() != 2 {
		t.Fatal("delayed error consumer rolled back owner or released live RPC")
	}
	once.Do(func() { close(release) })
	if err := <-done; !errors.Is(err, errClosedFundingReturnRPC) {
		t.Fatal(err)
	}
}

func TestClosedFundingCreatedReturnedErrorConcurrentConsumersKeepOneLiveReplay(t *testing.T) {
	m, old, client, database, _ := newClosedFundingReturnFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	client.created = func(_ *FundingReservation, call int) error {
		if call == 1 {
			database.failWrite.Store(true)
		} else {
			if call != 2 {
				return errors.New("concurrent returned-error consumer started an extra live replay")
			}
			close(started)
			<-release
		}
		return errClosedFundingReturnRPC
	}
	if err := callClosedFundingReturn(m, old); !errors.Is(err, errClosedFundingReturnWrite) {
		t.Fatal(err)
	}
	_, original := onlyClosedOpenOwner(t, m)
	database.failWrite.Store(false)
	m.serverNode.SetRPCClient(&closedFundingReturnPublicClient{client})
	m.bInited, m.actionMonitorRunning = true, true
	begin, done := make(chan struct{}), make(chan error, 7)
	for i := 0; i < 7; i++ {
		go func() { <-begin; _, err := m.ReopenChannel(false); done <- err }()
	}
	close(begin)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent returned-error consumers did not start exact retry")
	}
	for i := 0; i < 6; i++ {
		select {
		case err := <-done:
			if err == nil {
				t.Error("duplicate consumer reported success while exact replay is live")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("duplicate error consumer did not stay independently pending")
		}
	}
	_, live := onlyClosedOpenOwner(t, m)
	if live.Step != "created_inflight" || live.source != original.source || client.openCalls.Load() != 1 || client.createdCalls.Load() != 2 {
		t.Fatalf("error consumers released exact live replay: step=%s Open=%d Created=%d", live.Step, client.openCalls.Load(), client.createdCalls.Load())
	}
	once.Do(func() { close(release) })
	if err := <-done; !errors.Is(err, errClosedFundingReturnRPC) {
		t.Fatal(err)
	}
}
