package wallet

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func localReadyTestFixture(t *testing.T, height int) (*Manager, *ChannelInDB) {
	t.Helper()
	manager := newAccountManagementAutoTestManager(t)
	manager.initResvMap()
	if _, _, err := manager.CreateWallet("password"); err != nil {
		t.Fatal(err)
	}
	wallet := manager.wallet.(*InternalWallet)
	manager.serverNode = NewNode(&channelHeartbeatTestClient{}, "test", SERVER_NODE,
		wallet.GetPaymentPubKey(), wallet.GetNodePubKey())
	id, err := manager.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}
	stored := savePendingFundingFixture(t, manager.db.(*memoryKVDB), wallet, id, 601)
	if err := DeleteReservation(manager.db, RESV_TYPE_OPEN, 601); err != nil {
		t.Fatal(err)
	}
	stored.Status = CS_READY
	stored.CommitHeight = height
	stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
	if err := manager.SaveChannelInDB(stored); err != nil {
		t.Fatal(err)
	}
	return manager, stored
}

type readyChannelCountingDB struct {
	indexer.KVDB
	channelScans int
}

func (d *readyChannelCountingDB) BatchRead(prefix []byte, reverse bool, read func(k, v []byte) error) error {
	if string(prefix) == GetDBKeyPrefix()+DB_KEY_CHANNEL {
		d.channelScans++
	}
	return d.KVDB.BatchRead(prefix, reverse, read)
}

func TestUnlockWalletInitializesLocalReadyChannels(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	for _, height := range []int{0, 1} {
		t.Run(fmt.Sprintf("commit%d", height), func(t *testing.T) {
			manager, stored := localReadyTestFixture(t, height)
			before, err := manager.db.Read([]byte(GetChannelKey(stored.ChannelId)))
			if err != nil {
				t.Fatal(err)
			}
			manager.wallet = nil
			manager.clearAccountManagementSession()
			// Replay fresh Manager initialization: only persisted wallet metadata,
			// no signing keys, runtime channels or unfinished reservations.
			manager.initResvMap()
			if err := manager.initDB(); err != nil {
				t.Fatal(err)
			}
			if manager.GetCurrentChannel() != nil || len(manager.channelMap) != 0 {
				t.Fatal("locked initialization installed a channel")
			}
			counted := &readyChannelCountingDB{KVDB: manager.db}
			manager.db = counted
			if _, err := manager.UnlockWallet("password"); err != nil {
				t.Fatal(err)
			}
			runtime := manager.GetCurrentChannel()
			if runtime == nil || runtime.ChannelId != stored.ChannelId || runtime.CommitHeight != height {
				t.Fatalf("READY runtime not restored: %+v", runtime)
			}
			if runtime.localWallet == nil || runtime.PeerRPC != manager.serverNode.client ||
				manager.nodeMap[getNodeMapKeyWithChannel(runtime)] != stored.ChannelId {
				t.Fatal("READY runtime wallet/peer/node references were not installed")
			}
			if runtime.LocalCommitment.CommitTx.TxHash() != stored.LocalCommitment.CommitTx.TxHash() ||
				runtime.RemoteCommitment.CommitTx.TxHash() != stored.RemoteCommitment.CommitTx.TxHash() {
				t.Fatal("local initialization changed commitment transactions")
			}
			peer := &Channel{ChannelInDB: *stored, manager: manager, localWallet: manager.wallet.Clone()}
			if err := manager.replaceChannelFromPeer(peer, manager.wallet.Clone()); err == nil ||
				!strings.Contains(err.Error(), "must be greater") {
				t.Fatalf("equal peer snapshot was not rejected: %v", err)
			}
			scansAfterInitialization := counted.channelScans
			if err := manager.initializeLocalReadyChannels(); err != nil || counted.channelScans != scansAfterInitialization {
				t.Fatal("completed READY initialization scanned the channel DB again")
			}
			// UI re-authentication must not scan/rebuild an existing runtime
			// which now has an unfinished operation.
			runtime.ResvId = 777
			if _, err := manager.UnlockWallet("password"); err != nil {
				t.Fatal(err)
			}
			if manager.GetCurrentChannel() != runtime || runtime.ResvId != 777 || counted.channelScans != scansAfterInitialization {
				t.Fatalf("ordinary unlock replaced/rescanned runtime: scans=%d", counted.channelScans)
			}
			after, err := manager.db.Read([]byte(GetChannelKey(stored.ChannelId)))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("local initialization changed persisted channel data")
			}
		})
	}
}

func TestLocalReadyInitializationPreservesPendingOwnership(t *testing.T) {
	manager, stored := localReadyTestFixture(t, 1)
	resv := &FundingReservation{FundingDataInDB: FundingDataInDB{
		ReservationBase: NewReservationBase(601, true, ResvStatus(RS_FUNDING_BROADCASTED), manager.wallet),
		ChannelId:       stored.ChannelId,
	}}
	manager.addResv(resv)
	if err := manager.initializeLocalReadyChannels(); err != nil {
		t.Fatal(err)
	}
	if manager.GetChannel(stored.ChannelId) != nil || resv.Channel != nil {
		t.Fatal("READY initialization took ownership from the pending reservation")
	}
	manager.rehydratePendingFundingRuntime()
	pending := manager.GetFundingReservations()[601].Channel
	if pending == nil {
		t.Fatal("pending reservation path did not restore its own channel")
	}
	if err := manager.initializeLocalReadyChannels(); err != nil {
		t.Fatal(err)
	}
	if manager.GetFundingReservations()[601].Channel != pending || manager.GetChannel(stored.ChannelId) != nil {
		t.Fatal("READY initialization replaced the pending runtime")
	}
}

func TestLocalReadyInitializationPreservesExistingBusyRuntime(t *testing.T) {
	manager, current := newChannelSyncTestState(t)
	if err := manager.SaveChannelToDB(current); err != nil {
		t.Fatal(err)
	}
	current.ResvId = 777
	if err := manager.initializeLocalReadyChannels(); err != nil {
		t.Fatal(err)
	}
	if manager.GetChannel(current.ChannelId) != current || current.ResvId != 777 {
		t.Fatal("first initialization replaced a runtime owned by an operation")
	}
}

func TestLocalReadyInitializationRejectsInvalidPersistedChannel(t *testing.T) {
	manager, stored := localReadyTestFixture(t, 1)
	stored.ChannelHash = []byte("invalid persisted channel hash")
	raw, err := EncodeToBytes(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.db.Write([]byte(GetChannelKey(stored.ChannelId)), raw); err != nil {
		t.Fatal(err)
	}
	if err := manager.initializeLocalReadyChannels(); err == nil {
		t.Fatal("invalid channel bypassed LoadAllChannelInDBFromDB validation")
	}
	if manager.GetChannel(stored.ChannelId) != nil || manager.localReadyChannelsInitialized {
		t.Fatal("invalid channel was installed or initialization marked complete")
	}
}
