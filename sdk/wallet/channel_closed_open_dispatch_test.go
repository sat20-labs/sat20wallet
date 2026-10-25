package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

var errClosedOpenWrite = errors.New("ordinary test: owner write unavailable")
var errClosedOpenReply = errors.New("ordinary test: Open reply unavailable")

type closedOpenFaultDB struct {
	*memoryKVDB
	failWrite, failBatch atomic.Bool
}

func (d *closedOpenFaultDB) Write(key, value []byte) error {
	if d.failWrite.Load() {
		return errClosedOpenWrite
	}
	return d.memoryKVDB.Write(key, value)
}
func (d *closedOpenFaultDB) NewAtomicWriteBatch() indexer.WriteBatch {
	// This existing memory backend stages Put and atomically publishes Flush.
	return &closedOpenFaultBatch{WriteBatch: d.memoryKVDB.NewWriteBatch(), db: d}
}

type closedOpenFaultBatch struct {
	indexer.WriteBatch
	db *closedOpenFaultDB
}

func (b *closedOpenFaultBatch) Flush() error {
	if b.db.failBatch.Load() {
		return errClosedOpenWrite
	}
	return b.WriteBatch.Flush()
}

type closedOpenDispatchClient struct {
	NodeRPCClient
	calls atomic.Int32
	send  func(*FundingReservation, int) error
}

func (p *closedOpenDispatchClient) SendOpenChannelReq(f *FundingReservation) error {
	return p.send(f, int(p.calls.Add(1)))
}
func closedOpenDispatchFixture(t *testing.T) (*Manager, *Channel, *closedOpenDispatchClient, *closedOpenFaultDB) {
	t.Helper()
	peer := &channelHeartbeatTestClient{}
	manager, old := newSignedChannelSyncState(t, peer)
	manager.DisableChannel(old)
	old.Status = CS_CLOSED_FORCELY
	if err := manager.SaveChannelToDB(old); err != nil {
		t.Fatal(err)
	}
	database := &closedOpenFaultDB{memoryKVDB: manager.db.(*memoryKVDB)}
	manager.db = database
	manager.refreshTimeL1, manager.feeRateL1 = time.Now().Unix(), 1
	client := &closedOpenDispatchClient{NodeRPCClient: peer}
	manager.serverNode.SetRPCClient(client)
	return manager, old, client, database
}
func callClosedOpenDispatch(manager *Manager, old *Channel) error {
	_, err := manager.FunderInitReOpenProcess(old.ChanPoint.Value(), old.ChanPoint, "reopen", false, true, nil, "")
	return err
}
func onlyClosedOpenOwner(t *testing.T, manager *Manager) (*LocalActionPerformData, *closedChannelIntent) {
	t.Helper()
	for _, item := range manager.GetLocalActionReservations() {
		owner, ok := item.(*LocalActionPerformData)
		if ok && owner.Action == localActionClosedChannelRecovery {
			owner.RLock()
			intent, err := loadClosedChannelIntent(owner)
			owner.RUnlock()
			if err != nil {
				t.Fatal(err)
			}
			return owner, intent
		}
	}
	t.Fatal("missing retained closed owner")
	return nil, nil
}

func TestClosedOpenReturnedWriteFailureKeepsExactRetryLive(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprintf("accepted=%t", accepted), func(t *testing.T) {
			manager, old, client, database := closedOpenDispatchFixture(t)
			var firstRequest, firstSignature []byte
			client.send = func(f *FundingReservation, call int) error {
				raw, _ := json.Marshal(f.Req)
				if call == 1 {
					firstRequest, firstSignature = raw, append([]byte(nil), f.ReqSig...)
					if accepted {
						// Receipt boundary only: failed atomic save must stop
						// before any commitment/signature processing occurs.
						f.Id = 7001
						f.Accept = &wwire.AcceptChannel{Id: f.Id}
						database.failBatch.Store(true)
						return nil
					}
					database.failWrite.Store(true)
				} else if !bytes.Equal(raw, firstRequest) || !bytes.Equal(f.ReqSig, firstSignature) {
					return errors.New("DB retry rebuilt the original Open")
				}
				return errClosedOpenReply
			}
			err := callClosedOpenDispatch(manager, old)
			if !errors.Is(err, errClosedOpenWrite) {
				t.Fatalf("write error was hidden: %v", err)
			}
			owner, intent := onlyClosedOpenOwner(t, manager)
			if intent.Step != "open_inflight" || intent.Accept != nil || len(owner.ActionResvs) != 0 || len(manager.GetFundingReservations()) != 0 {
				t.Fatalf("returned RPC remains live or published unpersisted child: step=%s", intent.Step)
			}
			raw, err := database.Read([]byte(GetResvKey(RESV_TYPE_LOCALACTION, owner.Id)))
			if err != nil {
				t.Fatal(err)
			}
			var durable LocalActionPerformData
			if err := DecodeFromBytes(raw, &durable); err != nil {
				t.Fatal(err)
			}
			disk, err := loadClosedChannelIntent(&durable)
			if err != nil || disk.Step != "open_dispatching" || disk.Accept != nil {
				t.Fatalf("failed write changed durable pre-send record: %v", err)
			}
			database.failWrite.Store(false)
			database.failBatch.Store(false)
			if err := callClosedOpenDispatch(manager, old); !errors.Is(err, errClosedOpenReply) {
				t.Fatalf("same-process exact retry stayed blocked: %v", err)
			}
			next, _ := onlyClosedOpenOwner(t, manager)
			if client.calls.Load() != 2 || next.Id != owner.Id {
				t.Fatal("retry changed owner or failed to send exact request")
			}
		})
	}
}

func TestClosedOpenConcurrentCallsDoNotReleaseLiveReplay(t *testing.T) {
	manager, old, client, database := closedOpenDispatchFixture(t)
	started := make(chan int, 2)
	release := make(chan struct{}, 2)
	client.send = func(_ *FundingReservation, call int) error {
		started <- call
		<-release
		if call == 1 {
			database.failWrite.Store(true)
		}
		return errClosedOpenReply
	}
	done := make(chan error, 1)
	go func() { done <- callClosedOpenDispatch(manager, old) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first Open did not start")
	}
	if err := callClosedOpenDispatch(manager, old); err == nil || client.calls.Load() != 1 {
		t.Fatal("concurrent call dispatched a second Open")
	}
	release <- struct{}{}
	if err := <-done; !errors.Is(err, errClosedOpenWrite) {
		t.Fatalf("missing returned write error: %v", err)
	}
	database.failWrite.Store(false)
	go func() { done <- callClosedOpenDispatch(manager, old) }()
	select {
	case call := <-started:
		if call != 2 {
			t.Fatal("wrong replay")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("returned failed write left replay blocked")
	}
	_, intent := onlyClosedOpenOwner(t, manager)
	if intent.Step != "open_dispatching" {
		t.Fatal("old returned dispatcher cleared the current live replay")
	}
	if err := callClosedOpenDispatch(manager, old); err == nil || client.calls.Load() != 2 {
		t.Fatal("live replay was released by another call")
	}
	release <- struct{}{}
	if err := <-done; !errors.Is(err, errClosedOpenReply) {
		t.Fatal(err)
	}
}
