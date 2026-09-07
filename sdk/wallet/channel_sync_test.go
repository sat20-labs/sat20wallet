package wallet

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func TestSyncChannelRejectsPeerRecoveryWhileFundingIsPending(t *testing.T) {
	client := &channelHeartbeatTestClient{}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.resetResvMapsLocked()
	channelID, err := manager.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}
	manager.AddResv(&FundingReservation{FundingDataInDB: FundingDataInDB{
		ReservationBase: NewReservationBase(702, true, ResvStatus(CS_FUNDING_BROADCASTED), manager.wallet),
		ChannelId:       channelID,
	}})

	err = manager.SyncChannel("test pending funding gate", client)
	if err == nil || !strings.Contains(err.Error(), "open is already in progress") {
		t.Fatalf("SyncChannel error=%v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.syncCalls != 0 {
		t.Fatalf("peer sync calls=%d, want 0", client.syncCalls)
	}
}

func TestSyncChannelRejectsClosingThatStartsDuringPeerRequest(t *testing.T) {
	client := &channelHeartbeatTestClient{
		syncStarted: make(chan struct{}),
		syncRelease: make(chan struct{}),
	}
	manager, original := newSignedChannelSyncState(t, client)

	done := make(chan error, 1)
	go func() {
		done <- manager.SyncChannel("test pending closing race", client)
	}()
	<-client.syncStarted
	manager.SwitchAccount(1)
	manager.AddResv(&ClosingReservation{ClosingDataInDB: ClosingDataInDB{
		ReservationBase: NewReservationBase(704, true, RS_INIT, original.LocalWallet()),
		ChannelId:       original.ChannelId,
	}})
	close(client.syncRelease)

	err := <-done
	if err == nil || !strings.Contains(err.Error(), "close is already in progress") {
		t.Fatalf("SyncChannel error=%v", err)
	}
	if stored, err := manager.LoadChannelInDB(original.ChannelId); err != nil ||
		stored.CommitHeight != 2 || original.CommitHeight != 2 {
		t.Fatalf("rejected sync changed original channel: %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.syncCalls != 1 {
		t.Fatalf("peer sync calls=%d, want 1", client.syncCalls)
	}
}

func newChannelSyncTestState(t *testing.T) (*Manager, *Channel) {
	t.Helper()
	client := &channelHeartbeatTestClient{}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.db = newMemoryKVDB()
	manager.ResetResvMaps()
	channelID, err := manager.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}
	channelData := NewChannelInDB()
	channelData.ChannelId = channelID
	channelData.Address = channelID
	channelData.Status = CS_READY
	channelData.CommitHeight = 2
	channelData.LocalWalletId = manager.wallet.GetId()
	channelData.LocalChanCfg.PaymentKey = manager.wallet.GetPaymentPubKey()
	channelData.LocalChanCfg.RevocationBasePoint = manager.wallet.GetRevocationBaseKey()
	channelData.RemoteChanCfg.PaymentKey = manager.serverNode.Pubkey
	channelData.RemoteChanCfg.RevocationBasePoint = manager.serverNode.Pubkey
	channelData.LocalCommitment = NewChannelCommitment()
	channelData.RemoteCommitment = NewChannelCommitment()
	channelData.LocalCommitment.CommitTx = minimalPersistedCommitmentTx(1)
	channelData.RemoteCommitment.CommitTx = minimalPersistedCommitmentTx(2)
	channelData.StaticMerkleRoot = channelData.CalcStaticMerkleRoot()
	channel := &Channel{
		ChannelInDB: *channelData,
		manager:     manager,
		localWallet: manager.wallet.Clone(),
	}
	if err := manager.EnableChannel(channel); err != nil {
		t.Fatal(err)
	}
	return manager, channel
}

type channelBackupTestFunc func(*Channel, []byte) error

func (f channelBackupTestFunc) BackupChannel(c *Channel, data []byte) error {
	return f(c, data)
}

type channelWriteFailDB struct {
	indexer.KVDB
	err error
}

func (db *channelWriteFailDB) Write(_, _ []byte) error { return db.err }

func TestChannelBackupFailureDoesNotFailLocalCommit(t *testing.T) {
	for _, operation := range []string{"save", "peer sync"} {
		t.Run(operation, func(t *testing.T) {
			manager, current := newChannelSyncTestState(t)
			next := &Channel{ChannelInDB: current.ChannelInDB, manager: manager, localWallet: manager.wallet.Clone()}
			next.CommitHeight++
			calls := 0
			manager.SetChannelBackupHandler(channelBackupTestFunc(func(c *Channel, data []byte) error {
				calls++
				stored, err := manager.LoadChannelInDB(c.ChannelId)
				if err != nil || stored.CommitHeight != next.CommitHeight {
					t.Fatalf("backup ran before local commit: %v", err)
				}
				var snapshot ChannelInDB
				if err := DecodeFromBytes(data, &snapshot); err != nil || snapshot.CommitHeight != next.CommitHeight {
					t.Fatalf("backup did not receive committed snapshot: %v", err)
				}
				return errors.New("injected backup failure")
			}))
			var err error
			if operation == "save" {
				err = manager.SaveChannelToDB(next)
			} else {
				err = manager.replaceChannelFromPeer(next, manager.wallet.Clone())
			}
			if err != nil || calls != 1 {
				t.Fatalf("backup changed successful %s result: calls=%d err=%v", operation, calls, err)
			}
			if operation == "peer sync" {
				if manager.GetChannel(current.ChannelId) != current || current.CommitHeight != next.CommitHeight {
					t.Fatal("backup failure prevented runtime channel installation")
				}
				// The height rule is unchanged; the first sync must report success
				// rather than induce an unnecessary retry at the same height.
				if err := manager.replaceChannelFromPeer(next, manager.wallet.Clone()); err == nil || !strings.Contains(err.Error(), "must be greater") {
					t.Fatalf("equal-height sync was not rejected: %v", err)
				}
				if calls != 1 {
					t.Fatal("rejected sync triggered backup")
				}
			}
		})
	}
}

func TestChannelLocalWriteFailureStillFailsBeforeBackup(t *testing.T) {
	for _, operation := range []string{"save", "peer sync"} {
		t.Run(operation, func(t *testing.T) {
			manager, current := newChannelSyncTestState(t)
			if err := manager.SaveChannelToDB(current); err != nil {
				t.Fatal(err)
			}
			before, err := manager.db.Read([]byte(GetChannelKey(current.ChannelId)))
			if err != nil {
				t.Fatal(err)
			}
			manager.SetChannelBackupHandler(channelBackupTestFunc(func(*Channel, []byte) error {
				t.Fatal("failed local write triggered backup")
				return nil
			}))
			failure := errors.New("injected local write failure")
			manager.db = &channelWriteFailDB{KVDB: manager.db, err: failure}
			next := &Channel{ChannelInDB: current.ChannelInDB, manager: manager, localWallet: manager.wallet.Clone()}
			next.CommitHeight++
			if operation == "save" {
				err = manager.SaveChannelToDB(next)
			} else {
				err = manager.replaceChannelFromPeer(next, manager.wallet.Clone())
			}
			if !errors.Is(err, failure) {
				t.Fatalf("local write failure was swallowed: %v", err)
			}
			after, err := manager.db.Read([]byte(GetChannelKey(current.ChannelId)))
			if err != nil || !bytes.Equal(before, after) || current.CommitHeight != 2 || manager.GetChannel(current.ChannelId) != current {
				t.Fatalf("failed write changed persisted or runtime channel: %v", err)
			}
		})
	}
}

func TestPeerSnapshotRejectsLifecycleThenUpdatesConvergedChannel(t *testing.T) {
	manager, current := newChannelSyncTestState(t)
	replacement := &Channel{ChannelInDB: current.ChannelInDB, manager: manager, localWallet: manager.wallet.Clone()}
	replacement.StaticMerkleRoot = replacement.CalcStaticMerkleRoot()
	replacement.CommitHeight = current.CommitHeight + 1
	reservation := &SplicingReservation{SplicingDataInDB: SplicingDataInDB{
		ReservationBase: NewReservationBase(710, true, RS_SPLICINGIN_BROADCASTED, manager.wallet),
		ChannelId:       current.ChannelId,
	}, RevocationInfo: RevocationInfo{Channel: current}}
	manager.AddResv(reservation)

	err := manager.replaceChannelFromPeer(replacement, manager.wallet.Clone())
	if err == nil || !strings.Contains(err.Error(), "splicing is already in progress") {
		t.Fatalf("replace during expand lifecycle error=%v", err)
	}
	if got := manager.GetChannel(current.ChannelId); got != current {
		t.Fatalf("current channel changed during lifecycle: got=%p want=%p", got, current)
	}
	if reservation.Channel != current {
		t.Fatal("rejected snapshot changed the reservation channel pointer")
	}

	reservation.Status = RS_CONFIRMED
	if err := manager.replaceChannelFromPeer(replacement, manager.wallet.Clone()); err != nil {
		t.Fatalf("replace after lifecycle convergence: %v", err)
	}
	if got := manager.GetChannel(current.ChannelId); got != current || got.CommitHeight != replacement.CommitHeight {
		t.Fatalf("sync did not update the existing runtime: got=%p want=%p height=%d", got, current, current.CommitHeight)
	}
	if reservation.Channel != current {
		t.Fatalf("reservation channel=%p, want current map channel=%p", reservation.Channel, current)
	}
}

func TestPeerSnapshotRequiresHigherCommitHeight(t *testing.T) {
	for _, tc := range []struct {
		name       string
		peerHeight int
		prepare    func(*testing.T, *Manager, *Channel)
		wantError  string
	}{
		{name: "lower", peerHeight: 1, wantError: "must be greater"},
		{name: "equal", peerHeight: 2, wantError: "must be greater"},
		{name: "higher", peerHeight: 3},
		{name: "local advanced while requesting", peerHeight: 3,
			prepare: func(t *testing.T, m *Manager, c *Channel) {
				c.CommitHeight = 3
				if err := m.SaveChannelToDB(c); err != nil {
					t.Fatal(err)
				}
			}, wantError: "must be greater"},
		{name: "persisted equal", peerHeight: 2,
			prepare: func(_ *testing.T, m *Manager, c *Channel) { m.DisableChannel(c) }, wantError: "must be greater"},
		{name: "persisted higher", peerHeight: 3,
			prepare: func(_ *testing.T, m *Manager, c *Channel) { m.DisableChannel(c) }},
		{name: "no local channel", peerHeight: 0,
			prepare: func(t *testing.T, m *Manager, c *Channel) {
				if err := m.CleanChannelData(c.ChannelId); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "no local channel rejects negative height", peerHeight: -1,
			prepare: func(t *testing.T, m *Manager, c *Channel) {
				if err := m.CleanChannelData(c.ChannelId); err != nil {
					t.Fatal(err)
				}
			}, wantError: "must be greater"},
		{name: "busy mutex", peerHeight: 3,
			prepare: func(t *testing.T, _ *Manager, c *Channel) { c.Mutex.Lock(); t.Cleanup(c.Mutex.Unlock) }, wantError: "busy"},
		{name: "negotiation not in reservation map", peerHeight: 3,
			prepare: func(_ *testing.T, _ *Manager, c *Channel) { c.ResvId = 711 }, wantError: "not idle"},
		{name: "closing", peerHeight: 3,
			prepare: func(_ *testing.T, _ *Manager, c *Channel) { c.Status = CS_CLOSING_STARTED }, wantError: "not idle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, current := newChannelSyncTestState(t)
			if err := manager.SaveChannelToDB(current); err != nil {
				t.Fatal(err)
			}
			peer := &Channel{ChannelInDB: current.ChannelInDB, manager: manager, localWallet: manager.wallet.Clone()}
			peer.CommitHeight = tc.peerHeight
			peer.StaticMerkleRoot = peer.CalcStaticMerkleRoot()
			if tc.prepare != nil {
				tc.prepare(t, manager, current)
			}
			before, _ := manager.db.Read([]byte(GetChannelKey(current.ChannelId)))
			beforeRuntime := manager.GetChannel(current.ChannelId)
			err := manager.replaceChannelFromPeer(peer, manager.wallet.Clone())
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("sync error=%v, want %q", err, tc.wantError)
				}
				after, _ := manager.db.Read([]byte(GetChannelKey(current.ChannelId)))
				if !bytes.Equal(before, after) || manager.GetChannel(current.ChannelId) != beforeRuntime {
					t.Fatal("rejected sync changed persisted or runtime channel")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			stored, err := manager.LoadChannelInDB(current.ChannelId)
			if err != nil {
				t.Fatal(err)
			}
			if stored.CommitHeight != tc.peerHeight || manager.GetChannel(current.ChannelId).CommitHeight != tc.peerHeight {
				t.Fatalf("accepted sync did not persist/install height %d", tc.peerHeight)
			}
		})
	}
	t.Run("ordinary save and reopen are not sync", func(t *testing.T) {
		manager, current := newChannelSyncTestState(t)
		if err := manager.SaveChannelToDB(current); err != nil {
			t.Fatal(err)
		}
		if err := manager.SaveChannelToDB(current); err != nil {
			t.Fatal(err)
		}
		if err := manager.CleanChannelData(current.ChannelId); err != nil {
			t.Fatal(err)
		}
		reopened := &Channel{ChannelInDB: current.ChannelInDB, manager: manager, localWallet: manager.wallet.Clone()}
		reopened.CommitHeight = 0
		reopened.StaticMerkleRoot = reopened.CalcStaticMerkleRoot()
		if err := manager.SaveChannelToDB(reopened); err != nil {
			t.Fatal(err)
		}
		if err := manager.EnableChannel(reopened); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRestoredPeerChannelUsesWalletAndServerNodeIDs(t *testing.T) {
	walletValue := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if walletValue == nil {
		t.Fatal("create wallet")
	}
	walletValue.SetSubAccount(2)
	if bytes.Equal(walletValue.GetNodePubKey().SerializeCompressed(), walletValue.GetPaymentPubKey().SerializeCompressed()) {
		t.Fatal("test requires account payment key to differ from wallet node id")
	}

	serverWallet := walletValue.Clone()
	serverWallet.SetSubAccount(3)
	serverNodeID := serverWallet.GetPaymentPubKey().SerializeCompressed()

	channel := NewChannelInDB()
	channel.ChannelId = "node-id-perspective"
	channel.PeerNodeId = walletValue.GetNodePubKey().SerializeCompressed()
	channel.LocalChanCfg.PaymentKey = serverWallet.GetPaymentPubKey()
	channel.RemoteChanCfg.PaymentKey = walletValue.GetPaymentPubKey()
	channel.IsInitiator = true
	channel.TotalSatSent = 11
	channel.TotalSatReceived = 22
	channel.LocalCommitment = NewChannelCommitment()
	channel.RemoteCommitment = NewChannelCommitment()

	localConfigKey := channel.RemoteChanCfg.PaymentKey
	remoteConfigKey := channel.LocalChanCfg.PaymentKey
	localCommitment := channel.RemoteCommitment
	remoteCommitment := channel.LocalCommitment
	if err := restorePeerChannelPerspective(channel, walletValue, serverNodeID); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(channel.PeerNodeId, serverNodeID) {
		t.Fatalf("peer node id = %x, want server node id %x", channel.PeerNodeId, serverNodeID)
	}
	if channel.IsInitiator || channel.TotalSatSent != 22 || channel.TotalSatReceived != 11 {
		t.Fatalf("perspective was not flipped: initiator=%v sent=%d received=%d",
			channel.IsInitiator, channel.TotalSatSent, channel.TotalSatReceived)
	}
	if channel.LocalChanCfg.PaymentKey != localConfigKey || channel.RemoteChanCfg.PaymentKey != remoteConfigKey ||
		channel.LocalCommitment != localCommitment || channel.RemoteCommitment != remoteCommitment {
		t.Fatal("channel configs or commitments were not flipped")
	}

	invalid := NewChannelInDB()
	invalid.PeerNodeId = walletValue.GetPaymentPubKey().SerializeCompressed()
	if err := restorePeerChannelPerspective(invalid, walletValue, serverNodeID); err == nil {
		t.Fatal("account payment key was accepted as wallet node id")
	}
}
