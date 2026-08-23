package wallet

import (
	"strings"
	"testing"
	"time"
)

func TestChannelNegotiationTimeoutDoesNotApplyAfterPersistentState(t *testing.T) {
	now := time.Now().UnixMicro()
	id := now - 10*int64(time.Minute.Microseconds())
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ResvId = id
	resv := &FundingReservation{FundingDataInDB: FundingDataInDB{
		ReservationBase: NewReservationBase(id, true, RS_INIT, nil),
	}, Channel: channel}

	if !shouldTimeoutChannelNegotiation(now, resv, channel) {
		t.Fatal("stale pre-broadcast negotiation did not time out")
	}
	resv.Status = ResvStatus(CS_FUNDING_BROADCASTED)
	channel.Status = CS_FUNDING_BROADCASTED
	if shouldTimeoutChannelNegotiation(now, resv, channel) {
		t.Fatal("broadcast funding state was limited by the negotiation timeout")
	}
	resv.Status = RS_PAYMENT_BROADCASTED
	if shouldTimeoutChannelNegotiation(now, resv, channel) {
		t.Fatal("broadcast payment state was limited by the negotiation timeout")
	}

	closingChannel := &Channel{ChannelInDB: *NewChannelInDB()}
	closingChannel.Status = CS_CLOSING_DEANCHOR_BROADCASTED
	closingChannel.ResvId = id
	closing := &ClosingReservation{ClosingDataInDB: ClosingDataInDB{
		ReservationBase: NewReservationBase(id, true, RS_INIT, nil),
	}, Channel: closingChannel}
	if shouldTimeoutChannelNegotiation(now, closing, closingChannel) {
		t.Fatal("legacy persisted closing state was limited by the negotiation timeout")
	}
}

func TestClosingLifecycleIsVisibleAndBlocksNewFunding(t *testing.T) {
	client := &channelHeartbeatTestClient{}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.resetResvMapsLocked()
	manager.channelMap = make(map[string]*Channel)
	channelID, err := manager.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId = channelID
	channel.Status = CS_CLOSING_BROADCASTED
	closing := &ClosingReservation{ClosingDataInDB: ClosingDataInDB{
		ReservationBase: NewReservationBase(601, true, RS_INIT, manager.wallet),
		ChannelId:       channelID,
	}, Channel: channel}
	manager.AddResv(closing)

	if got := manager.GetCurrentChannel(); got != channel {
		t.Fatalf("current channel=%p, want closing channel=%p", got, channel)
	}
	if _, err := manager.PreviewOpenChannel(1, 100_000); err == nil || !strings.Contains(err.Error(), "close is already in progress") {
		t.Fatalf("preview error=%v", err)
	}
	if _, err := manager.FunderInitFundingProcess(1, 100_000, nil, "", ""); err == nil || !strings.Contains(err.Error(), "close is already in progress") {
		t.Fatalf("funding error=%v", err)
	}
}

func TestPendingSplicingBlocksCooperativeClose(t *testing.T) {
	client := &channelHeartbeatTestClient{}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.resetResvMapsLocked()
	channelID, err := manager.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId = channelID
	channel.Status = CS_READY
	manager.AddResv(&SplicingReservation{SplicingDataInDB: SplicingDataInDB{
		ReservationBase: NewReservationBase(602, true, RS_SPLICINGIN_BROADCASTED, manager.wallet),
		ChannelId:       channelID,
	}})

	closing := &ClosingReservation{ClosingDataInDB: ClosingDataInDB{
		ReservationBase: NewReservationBase(603, true, RS_INIT, manager.wallet),
		ChannelId:       channelID,
	}, Channel: channel}
	if err := manager.AllowClose(closing); err == nil || !strings.Contains(err.Error(), "splicing is already in progress") {
		t.Fatalf("AllowClose error=%v", err)
	}
}

func TestCompletedSplicingDoesNotBlockCooperativeClose(t *testing.T) {
	client := &channelHeartbeatTestClient{}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.resetResvMapsLocked()
	channelID, err := manager.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}
	manager.AddResv(&SplicingReservation{SplicingDataInDB: SplicingDataInDB{
		ReservationBase: NewReservationBase(604, true, RS_CONFIRMED, manager.wallet),
		ChannelId:       channelID,
	}})

	if err := manager.rejectUnfinishedChannelLifecycle(channelID); err != nil {
		t.Fatalf("completed splicing blocked channel lifecycle: %v", err)
	}
}

func TestPendingLifecycleGateIsScopedToWalletAndChannel(t *testing.T) {
	client := &channelHeartbeatTestClient{}
	manager := newChannelHeartbeatTestManager(t, client)
	manager.resetResvMapsLocked()
	channelID, err := manager.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}
	otherWallet := manager.wallet.Clone()
	otherWallet.SetSubAccount(manager.wallet.GetWalletId().SubAccountId + 1)
	manager.AddResv(&PaymentReservation{PaymentDataInDB: PaymentDataInDB{
		ReservationBase: NewReservationBase(605, true, RS_PAYMENT_BROADCASTED, otherWallet),
		ChannelId:       channelID,
	}})
	manager.AddResv(&PaymentReservation{PaymentDataInDB: PaymentDataInDB{
		ReservationBase: NewReservationBase(606, true, RS_PAYMENT_BROADCASTED, manager.wallet),
		ChannelId:       "another-channel",
	}})
	if err := manager.rejectUnfinishedChannelLifecycle(channelID); err != nil {
		t.Fatalf("unrelated reservation blocked channel lifecycle: %v", err)
	}

	manager.AddResv(&PaymentReservation{PaymentDataInDB: PaymentDataInDB{
		ReservationBase: NewReservationBase(607, true, RS_PAYMENT_BROADCASTED, manager.wallet),
		ChannelId:       channelID,
	}})
	if err := manager.rejectUnfinishedChannelLifecycle(channelID); err == nil || !strings.Contains(err.Error(), "payment is already in progress") {
		t.Fatalf("pending payment error=%v", err)
	}
	manager.DelResvWithId(607)

	manager.AddResv(&LocalActionPerformData{
		ReservationBase: NewReservationBase(608, true, RS_PERFORM_ACTION_TX_BROADCASTED, manager.wallet),
		Action:          LOCAL_ACTION_LOCK_WITH_EXPAND,
		ActionParam:     &LocalActionParam_Expand{ChannelId: channelID},
		ReqPubKey:       manager.wallet.GetPaymentPubKey().SerializeCompressed(),
	})
	if err := manager.rejectUnfinishedChannelLifecycle(channelID); err == nil || !strings.Contains(err.Error(), "lock-with-expand is already in progress") {
		t.Fatalf("pending lock-with-expand error=%v", err)
	}
}
