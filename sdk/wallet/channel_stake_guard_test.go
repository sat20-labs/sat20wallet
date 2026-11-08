package wallet

import (
	"errors"
	"strings"
	"testing"

	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
)

type stakeGuardRPC struct {
	IndexerRPCClient
	staked bool
	info   *swire.MinerInfo
	err    error
}

func (r *stakeGuardRPC) IsMinerNode([]byte) (bool, error) {
	return r.staked, r.err
}

func (r *stakeGuardRPC) GetMinerInfo([]byte) (*swire.MinerInfo, error) {
	return r.info, r.err
}

func TestStakeAddressGate(t *testing.T) {
	manager := newChannelHeartbeatTestManager(t, &channelHeartbeatTestClient{})
	rpc := &stakeGuardRPC{}
	manager.l2IndexerClient = &IndexerRPCClientMgr{active: rpc}
	pubkey := manager.wallet.GetPaymentPubKey().SerializeCompressed()
	const address = "stake-channel"

	if err := manager.RejectStakedAddr(pubkey, address); err != nil {
		t.Fatalf("unstaked address rejected: %v", err)
	}
	rpc.err = errors.New("indexer unavailable")
	if err := manager.RejectStakedAddr(pubkey, address); err == nil || !strings.Contains(err.Error(), "indexer unavailable") {
		t.Fatalf("indexer failure was accepted: %v", err)
	}
	rpc.err = nil
	rpc.staked = true
	rpc.info = &swire.MinerInfo{MinerInfo: &sindexer.MinerInfo{ChannelAddr: address}}
	if err := manager.RejectStakedAddr(pubkey, address); err == nil || !strings.Contains(err.Error(), "used for stake") {
		t.Fatalf("staked address was accepted: %v", err)
	}
	if err := manager.RejectStakedAddr(pubkey, "other-channel"); err != nil {
		t.Fatalf("other address rejected: %v", err)
	}
	rpc.info = &swire.MinerInfo{MinerInfo: &sindexer.MinerInfo{AscendUtxo: "stake-output"}}
	if err := manager.RejectStakedAddr(pubkey, address); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unknown stake address was accepted: %v", err)
	}
	rpc.info = &swire.MinerInfo{MinerInfo: &sindexer.MinerInfo{AssetAmt: "0"}}
	if err := manager.RejectStakedAddr(pubkey, address); err != nil {
		t.Fatalf("configured unstaked core rejected: %v", err)
	}
}

func TestChannelStakeGate(t *testing.T) {
	manager := newChannelHeartbeatTestManager(t, &channelHeartbeatTestClient{})
	const address = "open-channel"
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.Address, channel.ChannelId, channel.Status = address, address, CS_READY
	manager.channelMap[address] = channel
	if err := manager.RejectChannelStake(address); err == nil || !strings.Contains(err.Error(), "active channel") {
		t.Fatalf("active channel allowed stake: %v", err)
	}
	if err := manager.RejectChannelStake("other-channel"); err != nil {
		t.Fatalf("other address rejected: %v", err)
	}
	channel.Status = CS_CLOSED
	if err := manager.RejectChannelStake(address); err != nil {
		t.Fatalf("closed channel blocked stake: %v", err)
	}

	opening := &Channel{ChannelInDB: *NewChannelInDB()}
	opening.Address = address
	manager.fundingChannelMap = map[int64]*FundingReservation{
		1: {FundingDataInDB: FundingDataInDB{ReservationBase: NewReservationBase(1, true, RS_INIT, manager.wallet)}, Channel: opening},
	}
	if err := manager.RejectChannelStake(address); err == nil || !strings.Contains(err.Error(), "opening channel") {
		t.Fatalf("opening reservation allowed stake: %v", err)
	}
	manager.fundingChannelMap[1].Status = RS_CLOSED
	if err := manager.RejectChannelStake(address); err != nil {
		t.Fatalf("closed reservation blocked stake: %v", err)
	}
}
