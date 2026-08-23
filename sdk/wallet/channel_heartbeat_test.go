package wallet

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

const channelHeartbeatTestMnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"

type channelHeartbeatTestClient struct {
	NodeRPCClient
	mu              sync.Mutex
	manager         *Manager
	response        *wwire.PingResp
	pingCalls       int
	syncCalls       int
	switchOnPing    bool
	pingStarted     chan struct{}
	blockPing       bool
	syncStarted     chan struct{}
	syncRelease     chan struct{}
	blockSync       bool
	syncResponse    *wwire.ActionSyncResp
	lastPingRequest *wwire.PingReq
}

func (c *channelHeartbeatTestClient) SendPingReq(req *wwire.PingReq) (*wwire.PingResp, error) {
	c.mu.Lock()
	c.pingCalls++
	c.lastPingRequest = req
	switchOnPing := c.switchOnPing
	c.mu.Unlock()
	if switchOnPing {
		c.manager.wallet.SetSubAccount(1)
	}
	return c.response, nil
}

func (c *channelHeartbeatTestClient) SendPingReqContext(ctx context.Context, req *wwire.PingReq) (*wwire.PingResp, error) {
	if c.pingStarted != nil {
		close(c.pingStarted)
	}
	if c.blockPing {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return c.SendPingReq(req)
}

func (c *channelHeartbeatTestClient) SendActionSyncReq(*wwire.ActionSyncReq) (*wwire.ActionSyncResp, error) {
	c.mu.Lock()
	c.syncCalls++
	syncStarted := c.syncStarted
	syncRelease := c.syncRelease
	syncResponse := c.syncResponse
	c.mu.Unlock()
	if syncStarted != nil {
		close(syncStarted)
	}
	if syncRelease != nil {
		<-syncRelease
	}
	if syncResponse != nil {
		return syncResponse, nil
	}
	return nil, errors.New("stop after proving action sync was requested")
}

func (c *channelHeartbeatTestClient) SendActionSyncReqContext(ctx context.Context, req *wwire.ActionSyncReq) (*wwire.ActionSyncResp, error) {
	if c.blockSync {
		c.mu.Lock()
		c.syncCalls++
		syncStarted := c.syncStarted
		c.mu.Unlock()
		if syncStarted != nil {
			close(syncStarted)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return c.SendActionSyncReq(req)
}

func newChannelHeartbeatTestManager(t *testing.T, client *channelHeartbeatTestClient) *Manager {
	t.Helper()
	localWallet := NewInternalWalletWithMnemonic(channelHeartbeatTestMnemonic, "", GetChainParam())
	if localWallet == nil {
		t.Fatal("create test wallet")
	}
	serverKeyBytes, err := hex.DecodeString("0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e")
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := secp256k1.ParsePubKey(serverKeyBytes)
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		cfg:               &common.Config{Mode: "client"},
		wallet:            localWallet,
		serverNode:        NewNode(client, "test", SERVER_NODE, serverKey, serverKey),
		channelMap:        make(map[string]*Channel),
		nodeMap:           make(map[string]string),
		closingChannelMap: make(map[int64]*ClosingReservation),
	}
	client.manager = manager
	return manager
}

func TestChannelHeartbeatCodeOneRequestsExistingSync(t *testing.T) {
	client := &channelHeartbeatTestClient{response: &wwire.PingResp{
		BaseResp: wwire.BaseResp{Code: 1, Msg: "channel existing"},
		PingResponse: &wwire.PingResponse{
			NextAction:  wwire.STP_ACTION_SYNC,
			ActionParam: "restore",
		},
	}}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.runChannelHeartbeatTick()

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.pingCalls != 1 || client.syncCalls != 1 {
		t.Fatalf("ping/sync calls = %d/%d, want 1/1", client.pingCalls, client.syncCalls)
	}
	if client.lastPingRequest == nil || client.lastPingRequest.Channel != nil {
		t.Fatalf("missing local channel must ping with Channel=nil: %+v", client.lastPingRequest)
	}
}

func TestChannelHeartbeatSkipsPeerSyncWhileFundingIsPending(t *testing.T) {
	client := &channelHeartbeatTestClient{response: &wwire.PingResp{
		BaseResp: wwire.BaseResp{Code: 1, Msg: "channel existing"},
		PingResponse: &wwire.PingResponse{
			NextAction:  wwire.STP_ACTION_SYNC,
			ActionParam: "restore",
		},
	}}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.resetResvMapsLocked()
	channelID, err := manager.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}
	manager.AddResv(&FundingReservation{FundingDataInDB: FundingDataInDB{
		ReservationBase: NewReservationBase(701, true, ResvStatus(CS_FUNDING_BROADCASTED), manager.wallet),
		ChannelId:       channelID,
	}})

	manager.runChannelHeartbeatTick()

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.pingCalls != 1 || client.syncCalls != 0 {
		t.Fatalf("ping/sync calls = %d/%d, want 1/0 while funding is pending", client.pingCalls, client.syncCalls)
	}
}

func TestChannelHeartbeatClosingReservationScope(t *testing.T) {
	tests := []struct {
		name      string
		channelID func(string) string
		wallet    func(*Manager) common.Wallet
		status    ResvStatus
		wantSync  int
	}{
		{
			name:      "different channel does not block",
			channelID: func(string) string { return "another-channel" },
			wallet:    func(manager *Manager) common.Wallet { return manager.wallet },
			status:    RS_INIT,
			wantSync:  1,
		},
		{
			name:      "different wallet does not block",
			channelID: func(current string) string { return current },
			wallet: func(manager *Manager) common.Wallet {
				other := manager.wallet.Clone()
				other.SetSubAccount(1)
				return other
			},
			status:   RS_INIT,
			wantSync: 1,
		},
		{
			name:      "closed current reservation does not block",
			channelID: func(current string) string { return current },
			wallet:    func(manager *Manager) common.Wallet { return manager.wallet },
			status:    RS_CLOSED,
			wantSync:  1,
		},
		{
			name:      "active current reservation blocks",
			channelID: func(current string) string { return current },
			wallet:    func(manager *Manager) common.Wallet { return manager.wallet },
			status:    RS_INIT,
			wantSync:  0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &channelHeartbeatTestClient{response: &wwire.PingResp{
				BaseResp: wwire.BaseResp{Code: 1, Msg: "channel existing"},
				PingResponse: &wwire.PingResponse{
					NextAction:  wwire.STP_ACTION_SYNC,
					ActionParam: "restore",
				},
			}}
			manager := newChannelHeartbeatTestManager(t, client)
			manager.resetResvMapsLocked()
			currentChannelID, err := manager.GetChannelAddress()
			if err != nil {
				t.Fatal(err)
			}
			manager.AddResv(&ClosingReservation{ClosingDataInDB: ClosingDataInDB{
				ReservationBase: NewReservationBase(703, true, test.status, test.wallet(manager)),
				ChannelId:       test.channelID(currentChannelID),
			}})

			manager.runChannelHeartbeatTick()

			client.mu.Lock()
			defer client.mu.Unlock()
			if client.syncCalls != test.wantSync {
				t.Fatalf("sync calls=%d, want %d", client.syncCalls, test.wantSync)
			}
		})
	}
}

func TestChannelHeartbeatStopsWhenAccountChangesDuringPing(t *testing.T) {
	client := &channelHeartbeatTestClient{
		switchOnPing: true,
		response: &wwire.PingResp{
			BaseResp: wwire.BaseResp{Code: 1, Msg: "channel existing"},
			PingResponse: &wwire.PingResponse{
				NextAction:  wwire.STP_ACTION_SYNC,
				ActionParam: "restore",
			},
		},
	}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.runChannelHeartbeatTick()

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.pingCalls != 1 || client.syncCalls != 0 {
		t.Fatalf("ping/sync calls = %d/%d, want 1/0 after account switch", client.pingCalls, client.syncCalls)
	}
}

func TestChannelHeartbeatStartStop(t *testing.T) {
	client := &channelHeartbeatTestClient{response: &wwire.PingResp{
		BaseResp:     wwire.BaseResp{Code: 0, Msg: "ok"},
		PingResponse: &wwire.PingResponse{NextAction: "pong"},
	}}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.startChannelHeartbeat()
	manager.stopChannelHeartbeat()

	manager.channelHeartbeatMu.Lock()
	running := manager.channelHeartbeatRunning
	manager.channelHeartbeatMu.Unlock()
	if running {
		t.Fatal("channel heartbeat still running after stop")
	}
}

func TestChannelHeartbeatRejectsIdentityChangeDuringActionSync(t *testing.T) {
	client := &channelHeartbeatTestClient{
		syncStarted:  make(chan struct{}),
		syncRelease:  make(chan struct{}),
		syncResponse: &wwire.ActionSyncResp{ChannelData: []byte("must not be decoded")},
		response: &wwire.PingResp{
			BaseResp: wwire.BaseResp{Code: 1, Msg: "channel existing"},
			PingResponse: &wwire.PingResponse{
				NextAction:  wwire.STP_ACTION_SYNC,
				ActionParam: "restore",
			},
		},
	}
	manager := newChannelHeartbeatTestManager(t, client)
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.runChannelHeartbeatTick()
	}()
	<-client.syncStarted

	manager.channelIdentityMu.Lock()
	manager.wallet.SetSubAccount(1)
	manager.channelIdentityGeneration++
	manager.channelIdentityMu.Unlock()
	close(client.syncRelease)
	<-done

	if channel := manager.GetCurrentChannel(); channel != nil {
		t.Fatalf("stale identity restored channel %s", channel.ChannelId)
	}
}

func TestChannelHeartbeatStopCancelsInFlightPing(t *testing.T) {
	client := &channelHeartbeatTestClient{
		pingStarted: make(chan struct{}),
		blockPing:   true,
	}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.startChannelHeartbeat()
	<-client.pingStarted

	done := make(chan struct{})
	go func() {
		manager.stopChannelHeartbeat()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel the in-flight heartbeat ping")
	}
}

func TestChannelHeartbeatStopCancelsInFlightActionSync(t *testing.T) {
	client := &channelHeartbeatTestClient{
		syncStarted: make(chan struct{}),
		blockSync:   true,
		response: &wwire.PingResp{
			BaseResp: wwire.BaseResp{Code: 1, Msg: "channel existing"},
			PingResponse: &wwire.PingResponse{
				NextAction:  wwire.STP_ACTION_SYNC,
				ActionParam: "restore",
			},
		},
	}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.startChannelHeartbeat()
	<-client.syncStarted

	done := make(chan struct{})
	go func() {
		manager.stopChannelHeartbeat()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel the in-flight heartbeat action sync")
	}
}

func TestChannelHeartbeatSkipsMonitorWallet(t *testing.T) {
	client := &channelHeartbeatTestClient{response: &wwire.PingResp{
		BaseResp:     wwire.BaseResp{Code: 0, Msg: "ok"},
		PingResponse: &wwire.PingResponse{NextAction: "pong"},
	}}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.db = newMemoryKVDB()
	manager.status = newDefaultStatus()
	rgbManager, err := newRGB11Manager(manager, manager.db, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.rgbManager = rgbManager

	manager.startChannelHeartbeat()
	deadline := time.Now().Add(time.Second)
	for {
		client.mu.Lock()
		pingCalls := client.pingCalls
		client.mu.Unlock()
		if pingCalls == 1 {
			break
		}
		if time.Now().After(deadline) {
			manager.stopChannelHeartbeat()
			t.Fatal("initial mnemonic-wallet heartbeat did not run")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, err := manager.CreateMonitorWallet("tb1qmonitor"); err != nil {
		manager.stopChannelHeartbeat()
		t.Fatal(err)
	}
	if manager.channelIdentityGeneration != 1 {
		t.Fatalf("monitor wallet identity generation = %d, want 1", manager.channelIdentityGeneration)
	}
	time.Sleep(50 * time.Millisecond)
	manager.stopChannelHeartbeat()

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.pingCalls != 1 || client.syncCalls != 0 {
		t.Fatalf("ping/sync calls after switching to monitor wallet = %d/%d, want 1/0", client.pingCalls, client.syncCalls)
	}
}
