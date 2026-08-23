package wallet

import (
	"bytes"
	"strings"
	"testing"

	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
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
	if err == nil || !strings.Contains(err.Error(), "funding is pending") {
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
		syncStarted:  make(chan struct{}),
		syncRelease:  make(chan struct{}),
		syncResponse: &wwire.ActionSyncResp{ChannelData: []byte("must not be decoded")},
	}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.resetResvMapsLocked()
	channelID, err := manager.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- manager.SyncChannel("test pending closing race", client)
	}()
	<-client.syncStarted
	manager.AddResv(&ClosingReservation{ClosingDataInDB: ClosingDataInDB{
		ReservationBase: NewReservationBase(704, true, RS_INIT, manager.wallet),
		ChannelId:       channelID,
	}})
	close(client.syncRelease)

	err = <-done
	if err == nil || !strings.Contains(err.Error(), "closing is pending") {
		t.Fatalf("SyncChannel error=%v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.syncCalls != 1 {
		t.Fatalf("peer sync calls=%d, want 1", client.syncCalls)
	}
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
