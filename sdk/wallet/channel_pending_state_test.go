package wallet

import (
	"bytes"
	"context"
	"errors"
	"testing"

	swire "github.com/sat20-labs/satoshinet/wire"
)

func pendingStateTestTx() *swire.MsgTx {
	tx := swire.NewMsgTx(2)
	tx.AddTxOut(swire.NewTxOut(100, nil, []byte{0x51}))
	return tx
}

func TestPendingL2OutputIsNotSpendableUntilEnabled(t *testing.T) {
	channel := NewChannelInDB()
	tx := pendingStateTestTx()
	channel.AddPendingUtxo_SatsNet(tx)

	if got := channel.GetValidOutput_SatsNet(); len(got) != 0 {
		t.Fatalf("pending output leaked into spendable set: %v", got)
	}
	if got := channel.GetAllOutput_SatsNet(); len(got) != 1 || got[0].OutPointStr != tx.TxID()+":0" {
		t.Fatalf("protocol outputs=%v, want pending %s:0", got, tx.TxID())
	}
	if got := channel.GetPendingOutput_SatsNet(); len(got) != 1 {
		t.Fatalf("pending outputs=%d, want 1", len(got))
	}

	channel.EnableUtxo_SatsNet(tx.TxID())
	if got := channel.GetValidOutput_SatsNet(); len(got) != 1 || got[0].OutPointStr != tx.TxID()+":0" {
		t.Fatalf("spendable outputs=%v, want %s:0", got, tx.TxID())
	}
	if got := channel.GetAllOutput_SatsNet(); len(got) != 1 || got[0].OutPointStr != tx.TxID()+":0" {
		t.Fatalf("enabled outputs=%v, want %s:0", got, tx.TxID())
	}
	if got := channel.GetPendingOutput_SatsNet(); len(got) != 0 {
		t.Fatalf("pending outputs=%d after confirmation, want 0", len(got))
	}
}

type channelMonitorTestIndexer struct {
	IndexerRPCClient
	err               error
	rawTxCalls        int
	acceptanceCalls   int
	confirmationCalls int
	broadcastCalls    int
}

func (c *channelMonitorTestIndexer) GetRawTx(string) (string, error) {
	c.rawTxCalls++
	return "", c.err
}

func (c *channelMonitorTestIndexer) TestRawTx_SatsNet([]string) error {
	c.acceptanceCalls++
	return c.err
}

func (c *channelMonitorTestIndexer) IsTxConfirmed(string) bool {
	c.confirmationCalls++
	return false
}

func (c *channelMonitorTestIndexer) BroadCastTx_SatsNet(*swire.MsgTx) (string, error) {
	c.broadcastCalls++
	return "", c.err
}

func TestTestnetL2MonitorDoesNotRepairReadyChannel(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"endpoint 500", errors.New("HTTP 500")},
		{"timeout", context.DeadlineExceeded},
		{"lagging endpoint missing transaction", errors.New("transaction not found")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := newMemoryKVDB()
			wallet := NewInternalWalletWithMnemonic(channelHeartbeatTestMnemonic, "", GetChainParam())
			stored := savePendingFundingFixture(t, database, wallet, "ready-monitor", 701)
			stored.Status = CS_READY
			anchor := pendingStateTestTx()
			stored.AddPendingUtxo_SatsNet(anchor)
			stored.EnableUtxo_SatsNet(anchor.TxID())
			if err := SaveChannelInDB(database, stored); err != nil {
				t.Fatal(err)
			}
			resv := &FundingReservation{FundingDataInDB: FundingDataInDB{
				ReservationBase: NewReservationBase(stored.FundingTime, true, RS_CLOSED, wallet),
				ChannelId:       stored.ChannelId,
				AnchorTx:        anchor,
			}}
			if err := SaveReservation(database, resv); err != nil {
				t.Fatal(err)
			}
			client := &channelMonitorTestIndexer{err: tc.err}
			manager := &Manager{
				db: database, wallet: wallet,
				l2IndexerClient: &IndexerRPCClientMgr{active: client},
			}
			manager.resetResvMapsLocked()
			channel := &Channel{ChannelInDB: *stored, manager: manager, localWallet: wallet}
			manager.channelMap = map[string]*Channel{stored.ChannelId: channel}
			// A completed opening reservation stays in DB, not in the active monitor map.
			before := make(map[string][]byte)
			for _, key := range []string{GetChannelKey(stored.ChannelId), GetResvKey(RESV_TYPE_OPEN, resv.Id)} {
				value, err := database.Read([]byte(key))
				if err != nil {
					t.Fatal(err)
				}
				before[key] = value
			}
			for tick := 0; tick < 3; tick++ {
				manager.HandleChannelReservationStatus(false)
			}
			if channel.Status != CS_READY || len(channel.GetPendingOutput_SatsNet()) != 0 || len(channel.GetValidOutput_SatsNet()) != 1 {
				t.Fatalf("normal monitor changed READY channel: status=%d", channel.Status)
			}
			if len(manager.GetFundingReservations()) != 0 {
				t.Fatal("normal monitor recreated a completed funding reservation")
			}
			if client.rawTxCalls != 0 || client.acceptanceCalls != 0 || client.confirmationCalls != 0 || client.broadcastCalls != 0 {
				t.Fatalf("normal monitor probed/replayed a completed opening anchor: %+v", client)
			}
			for key, value := range before {
				after, err := database.Read([]byte(key))
				if err != nil || !bytes.Equal(value, after) {
					t.Fatalf("normal monitor changed persisted state %s: %v", key, err)
				}
			}
		})
	}
}

func TestL2MonitorStillRetriesPendingOpeningAnchor(t *testing.T) {
	oldTesting := ENABLE_TESTING
	ENABLE_TESTING = false // Exercise the RPC retry, not the test broadcast shortcut.
	t.Cleanup(func() { ENABLE_TESTING = oldTesting })
	client := &channelMonitorTestIndexer{err: errors.New("retry later")}
	manager := &Manager{l2IndexerClient: &IndexerRPCClientMgr{active: client}}
	manager.resetResvMapsLocked()
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.Status = CS_ANCHOR_BROADCASTED
	resv := &FundingReservation{FundingDataInDB: FundingDataInDB{
		ReservationBase: NewReservationBase(702, true, ResvStatus(CS_ANCHOR_BROADCASTED), nil),
		AnchorTx:        pendingStateTestTx(),
	}, Channel: channel}
	manager.fundingChannelMap[resv.Id] = resv
	manager.HandleChannelReservationStatus(false)
	if client.confirmationCalls != 1 || client.broadcastCalls != 1 || client.rawTxCalls != 0 || client.acceptanceCalls != 0 {
		t.Fatalf("pending anchor did not use the normal confirmation/retry path: %+v", client)
	}
	if manager.fundingChannelMap[resv.Id] != resv || channel.Status != CS_ANCHOR_BROADCASTED {
		t.Fatal("pending anchor was dropped or downgraded after a retry error")
	}
}
