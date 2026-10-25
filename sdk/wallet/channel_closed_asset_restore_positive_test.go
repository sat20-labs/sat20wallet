package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// The existing memory batch stages Put/Delete and publishes all keys under
// one memoryKVDB mutex in Flush. Advertise that genuine atomic capability only
// for this accepted-child fixture; do not change any production DB behavior.
type closedAssetAtomicMemoryDB struct{ *memoryKVDB }

func (d *closedAssetAtomicMemoryDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return d.memoryKVDB.NewWriteBatch()
}

type closedAssetContinuationIndexer struct {
	*closedAssetPipelineIndexer
	anchor        string
	confirmations int
}

func (c *closedAssetContinuationIndexer) IsTxConfirmed(id string) bool {
	c.confirmations++
	return id == c.anchor
}

type closedAssetContinuationPeer struct {
	*closedAssetPipelinePeer
	results int
}

func (c *closedAssetContinuationPeer) SendActionResultNfty(common.Wallet, int64, string, int, string) error {
	c.results++
	return nil
}

// A submitted monitor child is a fixture here. Production C1 binding, public
// rejection, private existing-child handoff, normal monitor, durable exact-child
// verification and C1 completion are exercised without replacing their methods.
func TestClosedAssetRestoreManualRejectionPreservesSubmittedChildProgress(t *testing.T) {
	manager, channel, asset, output, basePeer, baseRPC := closedAssetPipelineFixture(t, true)
	manager.db = &closedAssetAtomicMemoryDB{memoryKVDB: manager.db.(*memoryKVDB)}
	owner, id, err := manager.prepareClosedChannelAssetRestore(basePeer.captured, channel.ChannelId, asset.String(), output.OutPointStr)
	if err != nil || id != 0 {
		t.Fatalf("prepare exact monitor fixture: id=%d err=%v", id, err)
	}
	amount := indexer.NewDefaultDecimal(100)
	resv := &SplicingReservation{SplicingDataInDB: SplicingDataInDB{
		ReservationBase: NewReservationBase(manager.GenerateNewResvId(), true, RS_INIT, basePeer.captured),
		ChannelId:       channel.ChannelId, AssetName: asset, Amt: amount.Clone(), SplicingAmt: amount.Clone(), SplicingValue: output.Value(),
	}, SplicingInputs: []*TxOutput{output.Clone()}, NewCapacity: channel.Capacity, NewLocalBalance: amount.Clone(), NewRemoteBalance: indexer.NewDefaultDecimal(0)}
	resv.Channel, resv.OldChannel = channel, channel.Clone()
	resv.InReq = &wwire.SplicingInRequest{MsgHeader: wwire.NewMsgHeader(), ChannelId: channel.ChannelId, CommitHeight: channel.CommitHeight, AssetName: asset.String(), Amt: amount.String(), Utxos: []string{output.OutPointStr}}
	raw, err := json.Marshal(resv.InReq)
	if err != nil {
		t.Fatal(err)
	}
	resv.ReqSig, err = basePeer.captured.SignMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.persistClosedChannelAssetRestore(owner, resv, "request_prepared"); err != nil {
		t.Fatal(err)
	}
	if err := manager.persistClosedChannelAssetRestore(owner, resv, "child_accepted"); err != nil {
		t.Fatal(err)
	}
	anchor := swire.NewMsgTx(2)
	anchor.AddTxOut(swire.NewTxOut(output.Value(), swire.TxAssets{{Name: asset.AssetName, Amount: *amount.Clone(), BindingSat: 1}}, channel.GetChannelPkScript()))
	resv.AnchorTx, resv.Status = anchor, RS_SPLICINGIN_ANCHOR_BROADCASTED
	channel.SetCommitLocalValue(asset, amount)
	if err := channel.AddFundingOutput(output, asset); err != nil {
		t.Fatal(err)
	}
	channel.AddUtxo_SatsNet(sindexer.GenerateTxOutput(anchor, 0))
	channel.UpdateTime = resv.Id
	if err := manager.SaveChannelToDB(channel); err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveWalletReservation(resv); err != nil {
		t.Fatal(err)
	}
	manager.AddResv(resv)
	peer := &closedAssetContinuationPeer{closedAssetPipelinePeer: basePeer}
	rpc := &closedAssetContinuationIndexer{closedAssetPipelineIndexer: baseRPC, anchor: anchor.TxID()}
	channel.PeerRPC = peer
	manager.serverNode.SetRPCClient(peer)
	manager.l2IndexerClient.SetMaster(rpc)
	before, err := manager.db.Read([]byte(GetResvKey(RESV_TYPE_LOCALACTION, owner.Id)))
	if err != nil {
		t.Fatal(err)
	}
	channel.Mutex.Lock()
	_, _, _, err = manager.FunderInitExpandingProcess(channel, &asset.AssetName, output.OutPointStr, "", nil)
	channel.Mutex.Unlock()
	if err == nil || peer.requests != 0 {
		t.Fatalf("manual overlap was not rejected before another request: err=%v requests=%d", err, peer.requests)
	}
	after, err := manager.db.Read([]byte(GetResvKey(RESV_TYPE_LOCALACTION, owner.Id)))
	if err != nil || string(before) != string(after) {
		t.Fatal("manual rejection altered the pending owner or original request")
	}
	got, err := closedAssetRestoreAPI(t, manager).startClosedChannelAssetRestoreForWallet(context.Background(), basePeer.captured, channel.ChannelId, asset.String(), output.OutPointStr)
	if err != nil || got != resv.Id || peer.requests != 0 {
		t.Fatalf("original private handoff lost its child: got=%d want=%d err=%v requests=%d", got, resv.Id, err, peer.requests)
	}
	manager.HandleChannelReservationStatus(false)
	if _, ok := manager.GetSplicingReservation(resv.Id); ok {
		t.Fatal("existing monitor did not finish its confirmed original child")
	}
	if err := closedAssetRestoreAPI(t, manager).waitClosedChannelAssetRestoreForWallet(context.Background(), basePeer.captured, channel.ChannelId, asset.String(), output.OutPointStr, resv.Id); err != nil {
		t.Fatalf("original child did not remain durably completed: %v", err)
	}
	if err := manager.completeClosedChannelAssetRestore(owner, resv.Id); err != nil {
		t.Fatalf("original owner could not continue to completion: %v", err)
	}
	owner.RLock()
	intent, err := loadClosedChannelIntent(owner)
	owner.RUnlock()
	if err != nil || intent.AssetRestore == nil || intent.AssetRestore.Step != "completed" || intent.AssetRestore.ChildID != resv.Id || rpc.confirmations < 2 || peer.results != 1 || peer.requests != 0 {
		t.Fatalf("wrong original child progression: err=%v confirmations=%d results=%d requests=%d", err, rpc.confirmations, peer.results, peer.requests)
	}
}

func TestClosedAssetRestoreNoOwnerKeepsExplicitSignedPipelineBoundary(t *testing.T) {
	manager, channel, asset, output, peer, rpc := closedAssetPipelineFixture(t, true)
	manager.DelResvWithId(peer.owner.Id)
	if err := DeleteReservation(manager.db, RESV_TYPE_LOCALACTION, peer.owner.Id); err != nil {
		t.Fatal(err)
	}
	channel.Mutex.Lock()
	_, _, _, err := manager.FunderInitExpandingProcess(channel, &asset.AssetName, output.OutPointStr, "", nil)
	channel.Mutex.Unlock()
	if !errors.Is(err, errClosedAssetPipelineObserved) || peer.requests != 1 || rpc.walletQueries != 0 || rpc.broadcasts != 0 {
		t.Fatalf("old explicit no-owner pipeline changed: err=%v requests=%d walletQueries=%d broadcasts=%d", err, peer.requests, rpc.walletQueries, rpc.broadcasts)
	}
	raw, err := json.Marshal(peer.request)
	if err != nil || !VerifyMessage(peer.captured.GetPaymentPubKey(), raw, peer.requestSig) {
		t.Fatal("old explicit no-owner request was not signed by its channel account")
	}
}
