package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
)

// Verify durable ownership and a repeated bounded heartbeat before testing
// the runtime marker. On the unfixed baseline only the final marker check is
// red; the existing bad-response Funder validation is never bypassed.
func TestClosedAssetRestoreBadBalanceWakeRetainsExactObligation(t *testing.T) {
	manager, channel, asset, output, basePeer, rpc := closedAssetPipelineFixture(t, true)
	manager.db = &closedAssetAtomicMemoryDB{memoryKVDB: manager.db.(*memoryKVDB)}
	peer := &closedAssetIncorrectBalancePeer{closedAssetPipelinePeer: basePeer, response: indexer.NewDefaultDecimal(99)}
	channel.PeerRPC = peer
	manager.serverNode.SetRPCClient(peer)
	restored := 0
	manager.RegisterCallback(func(event string, _ interface{}) {
		if event == MSG_CHANNEL_RESTORED {
			restored++
		}
	})
	id, err := closedAssetRestoreAPI(t, manager).startClosedChannelAssetRestoreForWallet(context.Background(), peer.captured, channel.ChannelId, asset.String(), output.OutPointStr)
	if err == nil || !strings.Contains(err.Error(), "new local balance incorrect") || id <= 0 || peer.requests != 1 || !peer.preDispatchDurable {
		t.Fatalf("original Funder did not reject bad amount before next RPC: id=%d err=%v requests=%d", id, err, peer.requests)
	}
	key := []byte(GetResvKey(RESV_TYPE_LOCALACTION, peer.owner.Id))
	before, err := manager.db.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := LoadReservation(manager.db, nil, RESV_TYPE_LOCALACTION, peer.owner.Id)
	if err != nil {
		t.Fatal(err)
	}
	durable, ok := owner.(*LocalActionPerformData)
	if !ok {
		t.Fatal("durable recovery owner disappeared")
	}
	raw, ok := durable.ActionParam.(string)
	var intent closedChannelIntent
	if !ok || json.Unmarshal([]byte(raw), &intent) != nil || durable.Status != closedIntentReopened || intent.AssetsComplete || intent.AssetRestore == nil || intent.AssetRestore.Step != "child_accepted" || intent.AssetRestore.ChildID != id || intent.AssetRestore.Request == nil || len(intent.AssetRestore.RequestSignature) == 0 {
		t.Fatal("bad peer response cleared durable exact owner/request/child")
	}
	if _, err := manager.db.Read([]byte(GetResvKey(RESV_TYPE_SPLICING, id))); err != nil {
		t.Fatal("accepted child disappeared from durable storage")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done, err := closedAssetDriverAPI(t, manager).driveClosedChannelAssetRestoreForWallet(ctx, peer.captured, peer, channel.ChannelId)
	if done || err == nil || peer.requests != 1 || peer.nextRequests != 0 || peer.cancellations != 0 || rpc.walletQueries != 0 || rpc.broadcasts != 0 || restored != 0 {
		t.Fatalf("repeated wake replaced/advanced invalid obligation: done=%t err=%v requests=%d next=%d", done, err, peer.requests, peer.nextRequests)
	}
	after, err := manager.db.Read(key)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("repeated wake replaced or erased durable exact owner")
	}
	if peer.accepted == nil || peer.accepted.RemoteCommitInfo.CommitTx != nil || len(peer.accepted.RemoteCommitInfo.CommitSig) != 0 || peer.accepted.LocalRev != nil {
		t.Fatal("rejected quantity reached commitment signing/revocation")
	}
	if channel.ResvId != id {
		t.Fatal("pending accepted obligation lost its runtime child marker")
	}
}
