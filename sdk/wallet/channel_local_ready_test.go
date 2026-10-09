package wallet

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
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

func TestUnlockWalletRestoresPendingSplicingRuntime(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	for _, status := range []ResvStatus{
		RS_SPLICINGIN_STARTED, RS_SPLICINGIN_BROADCASTED, RS_SPLICINGIN_CONFIRMED, RS_SPLICINGIN_ANCHOR_BROADCASTED,
		RS_SPLICINGOUT_STARTED, RS_SPLICINGOUT_ANCHOR_BROADCASTED, RS_SPLICINGOUT_ANCHOR_CONFIRMED, RS_SPLICINGOUT_BROADCASTED,
	} {
		t.Run(fmt.Sprintf("phase%x", status), func(t *testing.T) {
			manager, stored, resv := pendingSplicingRuntimeFixture(t, status)
			channelBefore, _ := manager.db.Read([]byte(GetChannelKey(stored.ChannelId)))
			reservationBefore, _ := manager.db.Read([]byte(GetResvKey(RESV_TYPE_SPLICING, resv.Id)))
			manager.wallet = nil
			manager.clearAccountManagementSession()
			manager.initResvMap()
			if err := manager.initDB(); err != nil {
				t.Fatal(err)
			}
			if manager.GetCurrentChannel() != nil || manager.GetSplicingReservations()[resv.Id].Channel != nil {
				t.Fatal("locked initialization installed a splicing runtime")
			}
			if _, err := manager.UnlockWallet("password"); err != nil {
				t.Fatal(err)
			}
			runtime := manager.GetCurrentChannel()
			restored := manager.GetSplicingReservations()[resv.Id]
			if runtime == nil || restored == nil || restored.Channel != runtime {
				t.Fatal("cold unlock did not restore the pending splicing channel")
			}
			if runtime.ChannelId != stored.ChannelId || runtime.UpdateTime != resv.Id ||
				runtime.manager != manager || runtime.PeerRPC != manager.serverNode.client ||
				runtime.LocalWallet().GetWalletId() != resv.WalletId ||
				restored.LocalWallet().GetWalletId() != resv.WalletId || restored.Status != status {
				t.Fatal("splicing recovery changed ownership, phase or runtime references")
			}
			channelAfter, _ := manager.db.Read([]byte(GetChannelKey(stored.ChannelId)))
			reservationAfter, _ := manager.db.Read([]byte(GetResvKey(RESV_TYPE_SPLICING, resv.Id)))
			if !bytes.Equal(channelBefore, channelAfter) || !bytes.Equal(reservationBefore, reservationAfter) {
				t.Fatal("splicing hydration rewrote channel or reservation transactions")
			}
			runtime.ResvId = 777
			if _, err := manager.UnlockWallet("password"); err != nil {
				t.Fatal(err)
			}
			if manager.GetCurrentChannel() != runtime || runtime.ResvId != 777 ||
				manager.GetSplicingReservations()[resv.Id] != restored {
				t.Fatal("UI reauthentication replaced an active splicing runtime")
			}
			manager.DelResvWithId(resv.Id)
			if manager.GetCurrentChannel() != runtime {
				t.Fatal("channel disappeared after the completed reservation was removed")
			}
		})
	}
}

func pendingSplicingRuntimeFixture(t *testing.T, status ResvStatus) (*Manager, *ChannelInDB, *SplicingReservation) {
	t.Helper()
	manager, stored := localReadyTestFixture(t, 1)
	stored.UpdateTime = 701
	if err := manager.SaveChannelInDB(stored); err != nil {
		t.Fatal(err)
	}
	// Native fixtures check runtime hydration and byte preservation only. The
	// real browser/three-node test independently verifies signed transactions.
	anchor := swire.NewMsgTx(1)
	anchor.AddTxIn(swire.NewTxIn(&swire.OutPoint{}, []byte{0x51}, nil))
	anchor.AddTxOut(swire.NewTxOut(1000, nil, []byte{0x51}))
	resv := &SplicingReservation{SplicingDataInDB: SplicingDataInDB{
		ReservationBase:    NewReservationBase(701, true, status, manager.wallet),
		ChannelId:          stored.ChannelId,
		OldChanPoint:       stored.ChanPoint.OutPointStr,
		NeedSendSplicingTx: true,
		SplicingTx:         minimalPersistedCommitmentTx(11),
		AnchorTx:           anchor,
	}}
	if err := manager.SaveWalletReservation(resv); err != nil {
		t.Fatal(err)
	}
	manager.addResv(resv)
	return manager, stored, resv
}

func TestSplicingRuntimeRecoveryPreservesExistingRuntime(t *testing.T) {
	manager, stored, resv := pendingSplicingRuntimeFixture(t, RS_SPLICINGIN_BROADCASTED)
	runtime := NewChannel(stored, manager)
	runtime.ResvId = 777
	if err := manager.EnableChannel(runtime); err != nil {
		t.Fatal(err)
	}
	if err := manager.rehydratePendingSplicingRuntime(); err != nil {
		t.Fatal(err)
	}
	if manager.GetChannel(stored.ChannelId) != runtime || resv.Channel != runtime ||
		manager.GetSplicingReservations()[resv.Id] != resv || runtime.ResvId != 777 {
		t.Fatal("splicing recovery replaced an existing operation runtime")
	}
}

func TestSplicingRuntimeRecoverySupportsExpand(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		t.Run(fmt.Sprintf("alreadyAscended%v", recovered), func(t *testing.T) {
			manager, stored, resv := pendingSplicingRuntimeFixture(t, RS_SPLICINGIN_STARTED)
			resv.NeedSendSplicingTx = false
			resv.SplicingTx = nil
			if recovered {
				resv.RecoverAscended = true
				resv.RecoveredAnchorTxId = strings.Repeat("a", 64)
				resv.RecoveredAnchorOutpoint = resv.RecoveredAnchorTxId + ":0"
				resv.AnchorTx = nil
			}
			if err := manager.SaveWalletReservation(resv); err != nil {
				t.Fatal(err)
			}
			before, _ := manager.db.Read([]byte(GetResvKey(RESV_TYPE_SPLICING, resv.Id)))
			if err := manager.rehydratePendingSplicingRuntime(); err != nil {
				t.Fatal(err)
			}
			runtime := manager.GetChannel(stored.ChannelId)
			restored := manager.GetSplicingReservations()[resv.Id]
			if runtime == nil || restored.Channel != runtime || restored.RecoverAscended != recovered ||
				restored.SplicingTx != nil || restored.NeedSendSplicingTx {
				t.Fatal("valid Expand recovery lost its persisted transaction mode")
			}
			after, _ := manager.db.Read([]byte(GetResvKey(RESV_TYPE_SPLICING, resv.Id)))
			if !bytes.Equal(before, after) {
				t.Fatal("Expand recovery rewrote its reservation")
			}
		})
	}
}

func TestSplicingRuntimeRecoverySkipsUnsignedNegotiation(t *testing.T) {
	manager, stored, resv := pendingSplicingRuntimeFixture(t, RS_INIT)
	if err := manager.rehydratePendingSplicingRuntime(); err != nil {
		t.Fatal(err)
	}
	if manager.GetChannel(stored.ChannelId) != nil || resv.Channel != nil ||
		manager.GetSplicingReservations()[resv.Id] != resv {
		t.Fatal("unsigned negotiation was installed as a ready splicing runtime")
	}
}

func TestSplicingRuntimeRecoveryRejectsInvalidPersistedState(t *testing.T) {
	other := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", GetChainParam())
	tests := []struct {
		name   string
		mutate func(*ChannelInDB, *SplicingReservation)
		raw    func(*testing.T, *Manager, *ChannelInDB, *SplicingReservation)
	}{
		{name: "oldGeneration", mutate: func(c *ChannelInDB, _ *SplicingReservation) { c.UpdateTime++ }},
		{name: "notReady", mutate: func(c *ChannelInDB, _ *SplicingReservation) { c.Status = CS_CLOSED }},
		{name: "wrongChannelWallet", mutate: func(c *ChannelInDB, _ *SplicingReservation) { c.LocalWalletId++ }},
		{name: "wrongChannelAccount", mutate: func(c *ChannelInDB, _ *SplicingReservation) { c.LocalChanCfg.WalletId++ }},
		{name: "unavailableReservationWallet", mutate: func(_ *ChannelInDB, r *SplicingReservation) { r.WalletId.Id++ }},
		{name: "wrongReservationAccount", mutate: func(_ *ChannelInDB, r *SplicingReservation) { r.WalletId.SubAccountId++ }},
		{name: "wrongLocalPaymentKey", mutate: func(c *ChannelInDB, _ *SplicingReservation) { c.LocalChanCfg.PaymentKey = other.GetPaymentPubKey() }},
		{name: "wrongChannelAddress", mutate: func(c *ChannelInDB, _ *SplicingReservation) { c.RemoteChanCfg.PaymentKey = other.GetPaymentPubKey() }},
		{name: "missingSplicingTransaction", mutate: func(_ *ChannelInDB, r *SplicingReservation) { r.SplicingTx = nil }},
		{name: "missingAnchorTransaction", mutate: func(_ *ChannelInDB, r *SplicingReservation) { r.AnchorTx = nil }},
		{name: "invalidRecoveredAnchor", mutate: func(_ *ChannelInDB, r *SplicingReservation) {
			r.NeedSendSplicingTx, r.RecoverAscended = false, true
			r.RecoveredAnchorTxId, r.RecoveredAnchorOutpoint = strings.Repeat("a", 64), strings.Repeat("a", 64)+":1"
		}},
		{name: "recoveredAnchorRequiresNoBroadcast", mutate: func(_ *ChannelInDB, r *SplicingReservation) {
			r.RecoverAscended = true
			r.RecoveredAnchorTxId, r.RecoveredAnchorOutpoint = strings.Repeat("a", 64), strings.Repeat("a", 64)+":0"
		}},
		{name: "persistedReservationIdMismatch", raw: func(t *testing.T, m *Manager, _ *ChannelInDB, r *SplicingReservation) {
			persisted := r.SplicingDataInDB
			persisted.Id++
			data, err := EncodeToBytes(&persisted)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.db.Write([]byte(GetResvKey(RESV_TYPE_SPLICING, r.Id)), data); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "persistedPhaseMismatch", raw: func(t *testing.T, m *Manager, _ *ChannelInDB, r *SplicingReservation) {
			persisted := r.SplicingDataInDB
			persisted.Status = RS_SPLICINGIN_CONFIRMED
			data, err := EncodeToBytes(&persisted)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.db.Write([]byte(GetResvKey(RESV_TYPE_SPLICING, r.Id)), data); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "corruptChannelHash", raw: func(t *testing.T, m *Manager, c *ChannelInDB, _ *SplicingReservation) {
			c.ChannelHash = []byte("invalid hash")
			data, err := EncodeToBytes(c)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.db.Write([]byte(GetChannelKey(c.ChannelId)), data); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "conflictingOperation", raw: func(_ *testing.T, m *Manager, c *ChannelInDB, _ *SplicingReservation) {
			m.addResv(&FundingReservation{FundingDataInDB: FundingDataInDB{
				ReservationBase: NewReservationBase(702, true, ResvStatus(RS_FUNDING_BROADCASTED), m.wallet), ChannelId: c.ChannelId,
			}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, stored, resv := pendingSplicingRuntimeFixture(t, RS_SPLICINGIN_BROADCASTED)
			if test.mutate != nil {
				test.mutate(stored, resv)
			}
			// Keep the synthetic record internally hashed so identity rejection
			// is exercised during recovery, rather than by the fixture writer.
			stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
			if err := manager.SaveChannelInDB(stored); err != nil {
				t.Fatal(err)
			}
			if err := manager.SaveWalletReservation(resv); err != nil {
				t.Fatal(err)
			}
			if test.raw != nil {
				test.raw(t, manager, stored, resv)
			}
			channelBefore, _ := manager.db.Read([]byte(GetChannelKey(stored.ChannelId)))
			reservationBefore, _ := manager.db.Read([]byte(GetResvKey(RESV_TYPE_SPLICING, resv.Id)))
			if err := manager.rehydratePendingSplicingRuntime(); err == nil {
				t.Fatal("invalid persisted splicing state was accepted")
			}
			if manager.GetChannel(stored.ChannelId) != nil || resv.Channel != nil ||
				manager.GetSplicingReservations()[resv.Id] != resv {
				t.Fatal("rejected recovery installed or replaced an operation runtime")
			}
			channelAfter, _ := manager.db.Read([]byte(GetChannelKey(stored.ChannelId)))
			reservationAfter, _ := manager.db.Read([]byte(GetResvKey(RESV_TYPE_SPLICING, resv.Id)))
			if !bytes.Equal(channelBefore, channelAfter) || !bytes.Equal(reservationBefore, reservationAfter) {
				t.Fatal("rejected recovery changed persisted evidence")
			}
		})
	}
}

func TestSplicingRuntimeRecoveryRejectsMissingPrerequisite(t *testing.T) {
	manager, stored, resv := pendingSplicingRuntimeFixture(t, RS_SPLICINGIN_BROADCASTED)
	channelBefore, _ := manager.db.Read([]byte(GetChannelKey(stored.ChannelId)))
	reservationBefore, _ := manager.db.Read([]byte(GetResvKey(RESV_TYPE_SPLICING, resv.Id)))
	loaded, err := LoadReservation(manager.db, manager, RESV_TYPE_SPLICING, resv.Id)
	if err != nil {
		t.Fatal(err)
	}
	// gob cannot persist a nil pointer inside a transaction slice. Exercise
	// the runtime validator directly without fabricating an invalid encoding.
	loaded.(*SplicingReservation).PreTxs = []*wire.MsgTx{nil}
	if _, err := manager.loadPendingSplicingChannel(loaded.(*SplicingReservation)); err == nil {
		t.Fatal("missing prerequisite transaction was accepted")
	}
	if manager.GetChannel(stored.ChannelId) != nil || resv.Channel != nil {
		t.Fatal("prerequisite validation installed a runtime")
	}
	channelAfter, _ := manager.db.Read([]byte(GetChannelKey(stored.ChannelId)))
	reservationAfter, _ := manager.db.Read([]byte(GetResvKey(RESV_TYPE_SPLICING, resv.Id)))
	if !bytes.Equal(channelBefore, channelAfter) || !bytes.Equal(reservationBefore, reservationAfter) {
		t.Fatal("prerequisite validation changed persisted evidence")
	}
}
