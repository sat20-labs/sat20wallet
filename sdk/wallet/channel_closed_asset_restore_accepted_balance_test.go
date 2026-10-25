package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

type closedAssetIncorrectBalancePeer struct {
	*closedAssetPipelinePeer
	response                    *Decimal
	accepted                    *SplicingReservation
	nextRequests, cancellations int
}

func (p *closedAssetIncorrectBalancePeer) SendSplicingInReq(resv *SplicingReservation) error {
	if err := p.closedAssetPipelinePeer.SendSplicingInReq(resv); !errors.Is(err, errClosedAssetPipelineObserved) {
		return err
	}
	resv.Id = p.manager.GenerateNewResvId()
	resv.NewCapacity = resv.Channel.Capacity
	resv.NewLocalBalance = p.response.Clone()
	resv.NewRemoteBalance = indexer.NewDefaultDecimal(0)
	p.accepted = resv
	return nil
}
func (p *closedAssetIncorrectBalancePeer) SendSplicingInCommitSigReq(*SplicingReservation) error {
	p.nextRequests++
	return errors.New("incorrect balance reached next peer signature request")
}
func (p *closedAssetIncorrectBalancePeer) SendActionResultNfty(common.Wallet, int64, string, int, string) error {
	p.cancellations++
	return nil
}

// Removing only the new snapshot's nonnil gate must NOT replace the original
// Funder's quantity checks. A peer nil zero is legal only when the original
// expected balance is zero, not when it truncates the client's actual100.
func TestClosedAssetRestoreOriginalFunderRejectsNilOrTruncatedNonzeroBalance(t *testing.T) {
	for _, mode := range []string{"nil-instead-of100", "99-instead-of100"} {
		t.Run(mode, func(t *testing.T) {
			manager, channel, asset, output, basePeer, rpc := closedAssetPipelineFixture(t, true)
			manager.db = &closedAssetAtomicMemoryDB{memoryKVDB: manager.db.(*memoryKVDB)}
			peer := &closedAssetIncorrectBalancePeer{closedAssetPipelinePeer: basePeer}
			if mode == "99-instead-of100" {
				peer.response = indexer.NewDefaultDecimal(99)
			}
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
				t.Fatalf("original Funder quantity guard was bypassed: id=%d err=%v requests=%d durable=%t", id, err, peer.requests, peer.preDispatchDurable)
			}
			resv := peer.accepted
			if resv == nil || peer.nextRequests != 0 || peer.cancellations != 0 || rpc.walletQueries != 0 || rpc.broadcasts != 0 || restored != 0 || resv.RemoteCommitInfo.CommitTx != nil || len(resv.RemoteCommitInfo.CommitSig) != 0 || resv.LocalRev != nil {
				t.Fatal("incorrect balance advanced signing/RPC, spent wallet funds, erased obligation, or reported restored")
			}
			stored, err := LoadReservation(manager.db, nil, RESV_TYPE_LOCALACTION, peer.owner.Id)
			if err != nil {
				t.Fatal(err)
			}
			owner, ok := stored.(*LocalActionPerformData)
			if !ok {
				t.Fatal("original recovery owner disappeared")
			}
			var intent closedChannelIntent
			raw, ok := owner.ActionParam.(string)
			if !ok || json.Unmarshal([]byte(raw), &intent) != nil || owner.Status != closedIntentReopened || intent.AssetsComplete || intent.AssetRestore == nil || intent.AssetRestore.Step != "child_accepted" || intent.AssetRestore.ChildID != id || intent.AssetRestore.Request == nil || len(intent.AssetRestore.RequestSignature) == 0 {
				t.Fatal("rejected peer balance cleared its exact accepted pending obligation")
			}
			if channel.ResvId != id {
				t.Fatal("rejected peer balance cleared its accepted child marker")
			}
			if _, err := manager.db.Read([]byte(GetResvKey(RESV_TYPE_SPLICING, id))); err != nil {
				t.Fatal("accepted child is not durable")
			}
			existing, err := closedAssetRestoreAPI(t, manager).startClosedChannelAssetRestoreForWallet(context.Background(), peer.captured, channel.ChannelId, asset.String(), output.OutPointStr)
			if err != nil || existing != id || peer.requests != 1 || peer.nextRequests != 0 || restored != 0 {
				t.Fatal("pending invalid response retried as a fresh peer obligation")
			}
		})
	}
}
