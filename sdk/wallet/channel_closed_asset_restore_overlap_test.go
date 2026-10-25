package wallet

import (
	"context"
	"errors"
	"testing"
)

// Reproduce a real signed first-request boundary with a lost/unknown response.
// A subsequent explicit public expansion must not authorize another request
// around the new automatic owner's pending obligation.
func TestClosedAssetRestoreUnknownRequestBlocksExplicitExpansionOverlap(t *testing.T) {
	manager, channel, asset, output, peer, rpc := closedAssetPipelineFixture(t, true)
	_, err := closedAssetRestoreAPI(t, manager).startClosedChannelAssetRestoreForWallet(context.Background(), peer.captured, channel.ChannelId, asset.String(), output.OutPointStr)
	if !errors.Is(err, errClosedAssetPipelineObserved) || peer.requests != 1 || !peer.preDispatchDurable {
		t.Fatalf("did not establish unknown exact signed obligation: err=%v requests=%d durable=%t", err, peer.requests, peer.preDispatchDurable)
	}
	channel.Mutex.Lock()
	_, _, _, err = manager.FunderInitExpandingProcess(channel, &asset.AssetName, output.OutPointStr, "", nil)
	channel.Mutex.Unlock()
	if err == nil || errors.Is(err, errClosedAssetPipelineObserved) || peer.requests != 1 || rpc.walletQueries != 0 || rpc.broadcasts != 0 {
		t.Fatalf("explicit expansion bypassed pending automatic obligation: err=%v requests=%d walletQueries=%d broadcasts=%d", err, peer.requests, rpc.walletQueries, rpc.broadcasts)
	}
}
