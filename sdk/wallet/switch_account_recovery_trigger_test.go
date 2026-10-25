package wallet

import (
	"bytes"
	"testing"

	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

// Exercise the ordinary SDK entry point, not a replacement account-switch
// implementation. Pause the heartbeat consumer by installing its real bounded
// wake queue without starting a goroutine, so coalescing is deterministic.
// Draining that queue then uses the production heartbeat tick and RPC fixture.
func TestSwitchAccountSameAccountRecoveryWakeIsCoalescedAndIdentityScoped(t *testing.T) {
	for _, running := range []bool{false, true} {
		name := "stopped heartbeat stays stopped"
		if running {
			name = "running heartbeat queues one current-account check"
		}
		t.Run(name, func(t *testing.T) {
			client := &channelHeartbeatTestClient{response: &wwire.PingResp{
				BaseResp: wwire.BaseResp{Code: 1, Msg: "channel existing"},
				PingResponse: &wwire.PingResponse{
					NextAction: wwire.STP_ACTION_SYNC, ActionParam: "restore",
				},
			}}
			manager := newChannelHeartbeatTestManager(t, client)
			manager.db = newMemoryKVDB()
			manager.wallet.SetSubAccount(2)
			manager.status = newDefaultStatus()
			manager.status.CurrentWallet = manager.wallet.GetId()
			manager.status.CurrentAccount = manager.wallet.GetSubAccount()
			catalogWallet := manager.wallet.Clone()
			catalogWallet.SetSubAccount(0)
			manager.walletInfoMap = map[int64]*WalletInfo{
				catalogWallet.GetId(): {
					WalletInDB: WalletInDB{Id: catalogWallet.GetId(), Accounts: 4},
					Wallet:     catalogWallet,
				},
			}
			rgbManager, err := newRGB11Manager(manager, manager.db, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			manager.rgbManager = rgbManager
			if err := rgbManager.selectRGB11Scope(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { rgbManager.scopeStates.stopReconciliations() })

			selected := manager.wallet
			selectedID := selected.GetWalletId()
			selectedKey := append([]byte(nil), selected.GetPaymentPubKey().SerializeCompressed()...)
			if bytes.Equal(selectedKey, catalogWallet.GetPaymentPubKey().SerializeCompressed()) {
				t.Fatal("fixture must distinguish the selected subaccount from the root account")
			}
			wake := make(chan struct{}, 1)
			manager.channelHeartbeatRunning = running
			manager.channelHeartbeatWake = wake

			for attempt := 0; attempt < 3; attempt++ {
				if err := manager.SwitchAccount(selectedID.SubAccountId); err != nil {
					t.Fatalf("same-account selection %d: %v", attempt+1, err)
				}
			}
			if manager.wallet != selected || manager.wallet.GetWalletId() != selectedID ||
				manager.status.CurrentWallet != selectedID.Id ||
				manager.status.CurrentAccount != selectedID.SubAccountId ||
				catalogWallet.GetSubAccount() != 0 || len(manager.walletInfoMap) != 1 {
				t.Fatal("same-account recovery trigger changed the selected wallet or account catalog")
			}
			wantWake := 0
			if running {
				wantWake = 1
			}
			if manager.channelHeartbeatRunning != running || len(wake) != wantWake {
				t.Fatalf("heartbeat running=%v queued=%d, want %v/%d",
					manager.channelHeartbeatRunning, len(wake), running, wantWake)
			}
			if client.pingCalls != 0 || client.syncCalls != 0 {
				t.Fatal("account selection performed RPC work instead of queuing the existing heartbeat")
			}
			if !running {
				return
			}

			<-wake
			manager.runChannelHeartbeatTick()
			if client.pingCalls != 1 || client.syncCalls != 1 || len(wake) != 0 {
				t.Fatalf("one coalesced wake produced ping/sync/queued=%d/%d/%d, want 1/1/0",
					client.pingCalls, client.syncCalls, len(wake))
			}
			if client.lastPingRequest == nil || client.lastSyncRequest == nil ||
				!bytes.Equal(client.lastPingRequest.PubKey, selectedKey) ||
				!bytes.Equal(client.lastSyncRequest.PubKey, selectedKey) {
				t.Fatal("queued recovery did not remain scoped to the selected subaccount")
			}
			if manager.wallet != selected || manager.wallet.GetWalletId() != selectedID {
				t.Fatal("channel check changed wallet selection")
			}
		})
	}
}
