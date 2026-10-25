package wallet

import (
	"bytes"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

func closedFundingReturnFailedSuccess(t *testing.T) (*Manager, *Channel, *closedFundingReturnClient, *closedFundingReturnDB, *FundingReservation) {
	t.Helper()
	m, old, client, database, _ := newClosedFundingReturnFixture(t)
	var original *FundingReservation
	client.created = func(f *FundingReservation, call int) error {
		if call != 1 {
			return errors.New("unexpected second FundingCreated after a known success")
		}
		if err := closedFundingReturnSigned(client, f); err != nil {
			return err
		}
		original = f
		database.failBatch.Store(true)
		return nil
	}
	if err := callClosedFundingReturn(m, old); !errors.Is(err, errClosedFundingReturnWrite) {
		t.Fatalf("fixture did not reach known-success persistence failure: %v", err)
	}
	return m, old, client, database, original
}

func TestClosedFundingCreatedReturnedSnapshotIsIndependentAndRetainedUntilSave(t *testing.T) {
	m, old, client, database, mutable := closedFundingReturnFailedSuccess(t)
	owner, before := onlyClosedOpenOwner(t, m)
	signed := closedFundingReturnJSON(t, mutable.FundingSigned)
	// The fresh dispatch pointer was already published by created_prepared.
	// A returned receipt must not alias that monitor/transport-visible object.
	mutable.FundingSigned.CommitSig[0][0] ^= 1
	mutable.FundingCreated.CommitSig[0][0] ^= 1
	if err := callClosedFundingReturn(m, old); !errors.Is(err, errClosedFundingReturnWrite) {
		t.Fatalf("second local persistence failure did not retain returned result: %v", err)
	}
	if client.createdCalls.Load() != 1 || durableClosedFundingReturn(t, database, owner).source != before.source {
		t.Fatal("retry replayed RPC or published failed returned-result write")
	}
	database.failBatch.Store(false)
	if err := callClosedFundingReturn(m, old); err != nil {
		t.Fatalf("retained independent result did not resume after database recovered: %v", err)
	}
	_, after := onlyClosedOpenOwner(t, m)
	assertClosedFundingReturnIdentity(t, before, after)
	if !bytes.Equal(signed, closedFundingReturnJSON(t, after.FundingSigned)) || client.createdCalls.Load() != 1 || client.openCalls.Load() != 1 {
		t.Fatal("runtime receipt aliased a mutable funding object or started another lifecycle")
	}
}

func TestClosedFundingCreatedReturnedResultRequiresOriginalOwnerSourceAndExactChild(t *testing.T) {
	for _, change := range []string{"source", "child", "exact", "owner"} {
		t.Run(change, func(t *testing.T) {
			m, old, client, database, _ := closedFundingReturnFailedSuccess(t)
			owner, original := onlyClosedOpenOwner(t, m)
			database.failBatch.Store(false)
			switch change {
			case "source":
				changed, err := loadClosedChannelIntent(owner)
				if err != nil {
					t.Fatal(err)
				}
				changed.AssetsComplete = true
				if err := m.persistClosedChannelIntent(owner, changed, owner.Status); err != nil {
					t.Fatal(err)
				}
			case "child":
				changed, err := loadClosedChannelIntent(owner)
				if err != nil {
					t.Fatal(err)
				}
				changed.Accept.Id++
				if err := m.persistClosedChannelIntent(owner, changed, owner.Status); err != nil {
					t.Fatal(err)
				}
			case "exact":
				changed, err := loadClosedChannelIntent(owner)
				if err != nil {
					t.Fatal(err)
				}
				changed.FundingCreated.CommitSig[0][0] ^= 1
				if err := m.persistClosedChannelIntent(owner, changed, owner.Status); err != nil {
					t.Fatal(err)
				}
			case "owner":
				raw, err := EncodeToBytes(owner)
				if err != nil {
					t.Fatal(err)
				}
				var replacement LocalActionPerformData
				if err := DecodeFromBytes(raw, &replacement); err != nil {
					t.Fatal(err)
				}
				if err := replacement.InitLocalWallet(m); err != nil {
					t.Fatal(err)
				}
				m.AddResv(&replacement)
			}
			if err := callClosedFundingReturn(m, old); err == nil || client.createdCalls.Load() != 1 || client.openCalls.Load() != 1 {
				t.Fatalf("changed %s consumed a returned result or dispatched RPC: %v", change, err)
			}
			// Restore only the test mutation, not a new request, and prove the
			// rejected consumer did not destroy the original cached result.
			owner.Lock()
			owner.ActionParam = original.source
			owner.Unlock()
			if err := m.SaveWalletReservation(owner); err != nil {
				t.Fatal(err)
			}
			m.AddResv(owner)
			if err := callClosedFundingReturn(m, old); err != nil {
				t.Fatal(err)
			}
			_, after := onlyClosedOpenOwner(t, m)
			assertClosedFundingReturnIdentity(t, original, after)
			if client.createdCalls.Load() != 1 {
				t.Fatal("identity rejection lost the original result")
			}
		})
	}
}

func TestClosedFundingCreatedReturnedConcurrentConsumersNeverReplayRPC(t *testing.T) {
	m, old, client, database, _ := closedFundingReturnFailedSuccess(t)
	_, before := onlyClosedOpenOwner(t, m)
	database.failBatch.Store(false)
	entered, release := make(chan struct{}), make(chan struct{})
	var flushes atomic.Int32
	database.beforeFlush = func() {
		if flushes.Add(1) == 1 {
			close(entered)
			<-release
		}
	}
	done := make(chan error, 7)
	go func() { done <- callClosedFundingReturn(m, old) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("returned result was never claimed for local persistence")
	}
	for i := 0; i < 6; i++ {
		go func() { done <- callClosedFundingReturn(m, old) }()
	}
	close(release)
	for i := 0; i < 7; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent returned-result consumer did not finish")
		}
	}
	if err := callClosedFundingReturn(m, old); err != nil {
		t.Fatal(err)
	}
	_, after := onlyClosedOpenOwner(t, m)
	assertClosedFundingReturnIdentity(t, before, after)
	if after.Step != "funding_monitor" || client.createdCalls.Load() != 1 || client.openCalls.Load() != 1 || flushes.Load() != 2 {
		t.Fatalf("returned consumers replayed or duplicated result persistence: step=%s Open=%d Created=%d batches=%d", after.Step, client.openCalls.Load(), client.createdCalls.Load(), flushes.Load())
	}
}

// ReopenChannel is the user-facing recovery wrapper. Its normal read-only peer
// liveness/contract checks remain in place; only the transport responses are
// deterministic. No fresh-funding preparation is permitted after the original
// request and child were durably recorded.
type closedFundingReturnPublicClient struct{ *closedFundingReturnClient }

func (*closedFundingReturnPublicClient) SendActionResultNfty(common.Wallet, int64, string, int, string) error {
	return nil
}
func (*closedFundingReturnPublicClient) GetDeployedContractsReq() ([]string, error) { return nil, nil }

func TestClosedFundingCreatedReturnedSuccessPublicReopenResumesLocally(t *testing.T) {
	m, _, client, database, _ := closedFundingReturnFailedSuccess(t)
	owner, before := onlyClosedOpenOwner(t, m)
	m.serverNode.SetRPCClient(&closedFundingReturnPublicClient{client})
	m.bInited, m.actionMonitorRunning = true, true
	database.failBatch.Store(false)
	channelID, err := m.ReopenChannel(false)
	if err != nil || channelID != before.ChannelID {
		t.Fatalf("public Reopen did not consume returned result locally: id=%s err=%v", channelID, err)
	}
	next, after := onlyClosedOpenOwner(t, m)
	assertClosedFundingReturnIdentity(t, before, after)
	if next != owner || after.Step != "funding_monitor" || client.createdCalls.Load() != 1 || client.openCalls.Load() != 1 {
		t.Fatal("public Reopen replaced owner or replayed an already-successful RPC")
	}
}

func TestClosedFundingCreatedReturnedWrongResponseChildRemainsPending(t *testing.T) {
	m, old, client, database, _ := newClosedFundingReturnFixture(t)
	client.created = func(f *FundingReservation, _ int) error {
		if err := closedFundingReturnSigned(client, f); err != nil {
			return err
		}
		f.FundingSigned.Id++
		database.failBatch.Store(true)
		return nil
	}
	if err := callClosedFundingReturn(m, old); !errors.Is(err, errClosedFundingReturnWrite) {
		t.Fatalf("wrong-child response did not reach returned-result failed save: %v", err)
	}
	_, before := onlyClosedOpenOwner(t, m)
	database.failBatch.Store(false)
	if err := callClosedFundingReturn(m, old); err == nil {
		t.Fatal("different FundingSigned child was treated as the original returned result")
	}
	_, after := onlyClosedOpenOwner(t, m)
	if after.source != before.source || after.Step != "created_inflight" || client.createdCalls.Load() != 1 || client.openCalls.Load() != 1 {
		t.Fatal("wrong-child result changed the original owner or released unknown dispatch")
	}
}
