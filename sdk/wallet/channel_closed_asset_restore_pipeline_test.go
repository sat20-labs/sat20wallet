package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

var errClosedAssetPipelineObserved = errors.New("stop after actual existing-stub signed request")

type closedAssetPipelineIndexer struct {
	IndexerRPCClient
	channelID, walletAddress  string
	outputs                   map[string]*TxOutput
	stubs                     []*TxOutput
	walletQueries, broadcasts int
	badStub                   bool
}

func (c *closedAssetPipelineIndexer) GetSyncHeight() int { return 1 }
func (c *closedAssetPipelineIndexer) GetFeeRate() int64  { return 1 }
func (c *closedAssetPipelineIndexer) GetAscendData(string) (*sindexer.AscendData, error) {
	return nil, errors.New("not found")
}
func (c *closedAssetPipelineIndexer) GetExistingUtxos(points []string) ([]string, error) {
	return append([]string(nil), points...), nil
}
func (c *closedAssetPipelineIndexer) GetTxOutput(point string) (*TxOutput, error) {
	output := c.outputs[point]
	if output == nil {
		return nil, fmt.Errorf("unknown output %s", point)
	}
	copy := output.Clone()
	if c.badStub && len(c.stubs) > 0 && point == c.stubs[0].OutPointStr {
		copy.OutValue.Value++
	}
	return copy, nil
}
func (c *closedAssetPipelineIndexer) GetUtxoListWithTicker(address string, asset *swire.AssetName) []*indexerwire.TxOutputInfo {
	if address == c.walletAddress {
		c.walletQueries++
		return nil
	}
	if address != c.channelID || !indexer.IsPlainAsset(asset) {
		return nil
	}
	result := make([]*indexerwire.TxOutputInfo, 0, len(c.stubs))
	for _, output := range c.stubs {
		result = append(result, output.ToAssetsInUtxo())
	}
	return result
}
func (c *closedAssetPipelineIndexer) BroadCastTx(*wire.MsgTx) (string, error) {
	c.broadcasts++
	return "", errors.New("must not broadcast")
}
func (c *closedAssetPipelineIndexer) BroadCastTxs([]*wire.MsgTx) error {
	c.broadcasts++
	return errors.New("must not broadcast")
}
func (c *closedAssetPipelineIndexer) BroadCastTx_SatsNet(*swire.MsgTx) (string, error) {
	c.broadcasts++
	return "", errors.New("must not broadcast")
}
func (c *closedAssetPipelineIndexer) BroadCastTxs_SatsNet([]*swire.MsgTx) error {
	c.broadcasts++
	return errors.New("must not broadcast")
}

type closedAssetPipelinePeer struct {
	NodeRPCClient
	manager            *Manager
	owner              *LocalActionPerformData
	captured           common.Wallet
	requests           int
	request            *wwire.SplicingInRequest
	requestSig         []byte
	preDispatchDurable bool
	switchAtRequest    bool
}

func (c *closedAssetPipelinePeer) GetDeployedContractsReq() ([]string, error) { return nil, nil }
func (c *closedAssetPipelinePeer) SendSplicingInReq(resv *SplicingReservation) error {
	c.requests++
	c.request, c.requestSig = resv.InReq, append([]byte(nil), resv.ReqSig...)
	stored, err := LoadReservation(c.manager.db, nil, RESV_TYPE_LOCALACTION, c.owner.Id)
	if err == nil {
		owner, ok := stored.(*LocalActionPerformData)
		if ok {
			var payload struct {
				AssetRestore *struct {
					AssetName, ExactUTXO, Step string
					Request                    *wwire.SplicingInRequest
					RequestSignature           []byte
				}
			}
			if raw, ok := owner.ActionParam.(string); ok && json.Unmarshal([]byte(raw), &payload) == nil && payload.AssetRestore != nil {
				pending := payload.AssetRestore
				actual, _ := json.Marshal(pending.Request)
				want, _ := json.Marshal(resv.InReq)
				c.preDispatchDurable = pending.Step == "request_prepared" && pending.AssetName == resv.AssetName.String() &&
					len(resv.InReq.Utxos) == 1 && pending.ExactUTXO == resv.InReq.Utxos[0] && bytes.Equal(actual, want) && bytes.Equal(pending.RequestSignature, resv.ReqSig)
			}
		}
	}
	if c.switchAtRequest {
		c.manager.channelIdentityMu.Lock()
		c.manager.mutex.Lock()
		c.manager.wallet.SetSubAccount(1)
		c.manager.mutex.Unlock()
		c.manager.channelIdentityMu.Unlock()
	}
	return errClosedAssetPipelineObserved
}

func closedAssetPipelineFixture(t *testing.T, enoughStubs bool) (*Manager, *Channel, *AssetName, *TxOutput, *closedAssetPipelinePeer, *closedAssetPipelineIndexer) {
	t.Helper()
	manager, channel, asset, output := closedAssetRestoreFixture(t)
	captured := channel.LocalWallet().Clone()
	owner, intent, err := manager.ensureClosedChannelIntent(captured, channel.ChannelId, channel.RemoteCommitment.CommitTx.TxID(), "reopen", closedChannelIntentInitial{AssetExpectations: []closedChannelAssetExpectation{}})
	if err != nil {
		t.Fatal(err)
	}
	intent.Step = "reopened"
	intent.Request = &wwire.OpenChannelRequest{FundingKey: captured.GetPaymentPubKey().SerializeCompressed(), Outpoints: []string{channel.ChanPoint.OutPointStr}}
	owner.ActionResvs = []*SubActionInfo{{ActionType: RESV_TYPE_OPEN, ResvId: 8501}}
	if err := manager.persistClosedChannelIntent(owner, intent, closedIntentReopened); err != nil {
		t.Fatal(err)
	}
	channel.FundingTime = 8501
	channel.StaticMerkleRoot = channel.CalcStaticMerkleRoot()
	if err := manager.SaveChannelToDB(channel); err != nil {
		t.Fatal(err)
	}
	peer := &closedAssetPipelinePeer{manager: manager, owner: owner, captured: captured}
	channel.PeerRPC = peer
	manager.serverNode.SetRPCClient(peer)
	rpc := &closedAssetPipelineIndexer{channelID: channel.ChannelId, walletAddress: captured.GetAddress(), outputs: map[string]*TxOutput{output.OutPointStr: output}}
	if enoughStubs {
		for i := 0; i < 2; i++ {
			stub := channel.ChanPoint.Clone()
			stub.OutPointStr = fmt.Sprintf("%064x:0", 8510+i)
			stub.OutValue.Value, stub.Assets, stub.Offsets = 330, nil, nil
			rpc.stubs = append(rpc.stubs, stub)
			rpc.outputs[stub.OutPointStr] = stub
		}
	}
	manager.l1IndexerClient = NewIndexerRPCClientMgr()
	manager.l1IndexerClient.SetMaster(rpc)
	manager.l2IndexerClient = NewIndexerRPCClientMgr()
	manager.l2IndexerClient.SetMaster(rpc)
	manager.utxoLockerL1 = NewUtxoLocker(manager.db, rpc, GetChainParam().Name)
	manager.tickerInfoMap = map[string]*indexer.TickerInfo{asset.String(): {AssetName: asset.AssetName, N: 1, Divisibility: 0}}
	return manager, channel, asset, output, peer, rpc
}

func TestClosedAssetRestoreExistingStubsReachOriginalSignedPipeline(t *testing.T) {
	manager, channel, asset, output, peer, rpc := closedAssetPipelineFixture(t, true)
	peer.switchAtRequest = true
	id, err := closedAssetRestoreAPI(t, manager).startClosedChannelAssetRestoreForWallet(context.Background(), peer.captured, channel.ChannelId, asset.String(), output.OutPointStr)
	if !errors.Is(err, errClosedAssetPipelineObserved) || id != 0 || peer.requests != 1 || !peer.preDispatchDurable {
		t.Fatalf("existing-stub original pipeline boundary: id=%d err=%v requests=%d preDispatchDurable=%t", id, err, peer.requests, peer.preDispatchDurable)
	}
	if len(peer.request.Utxos) != 1 || peer.request.Utxos[0] != output.OutPointStr || len(peer.request.Fees) != 2 || peer.request.NeedSendSplicingTx {
		t.Fatalf("not one exact existing-source expansion: %+v", peer.request)
	}
	raw, _ := json.Marshal(peer.request)
	if !VerifyMessage(peer.captured.GetPaymentPubKey(), raw, peer.requestSig) {
		t.Fatal("request was not signed by captured account")
	}
	if manager.wallet.GetSubAccount() != 1 || rpc.walletQueries != 0 || rpc.broadcasts != 0 {
		t.Fatal("recovery spent wallet funds or replaced newer selected account")
	}
	owner, err := LoadReservation(manager.db, nil, RESV_TYPE_LOCALACTION, peer.owner.Id)
	if err != nil || owner.GetStatus() != closedIntentReopened {
		t.Fatal("unknown initial response erased or rewound READY owner")
	}
}

func TestClosedAssetRestoreMissingStubsNeverFallsBackToWallet(t *testing.T) {
	manager, channel, asset, output, peer, rpc := closedAssetPipelineFixture(t, false)
	id, err := closedAssetRestoreAPI(t, manager).startClosedChannelAssetRestoreForWallet(context.Background(), peer.captured, channel.ChannelId, asset.String(), output.OutPointStr)
	if err == nil || !strings.Contains(err.Error(), "approval") || id != 0 || peer.requests != 0 || rpc.walletQueries != 0 || rpc.broadcasts != 0 {
		t.Fatalf("missing stubs caused automatic spending: id=%d err=%v requests=%d walletQueries=%d broadcasts=%d", id, err, peer.requests, rpc.walletQueries, rpc.broadcasts)
	}
}

func TestClosedAssetRestoreRevalidatesSelectedExistingStub(t *testing.T) {
	manager, channel, asset, output, peer, rpc := closedAssetPipelineFixture(t, true)
	rpc.badStub = true
	_, err := closedAssetRestoreAPI(t, manager).startClosedChannelAssetRestoreForWallet(context.Background(), peer.captured, channel.ChannelId, asset.String(), output.OutPointStr)
	if err == nil || peer.requests != 0 || rpc.walletQueries != 0 || rpc.broadcasts != 0 {
		t.Fatalf("changed stub was signed or spent: err=%v", err)
	}
}

type closedAssetSecretBoundary interface {
	receiveNewCommitmentForWallet(*RevocationInfo, *wwire.CommitSigInfo, []*wire.MsgTx, common.Wallet) error
	revokeCurrentCommitmentForWallet(*RevocationInfo, common.Wallet) (*wwire.RevokeAndAck, error)
}

type closedAssetSecretProbe struct {
	common.Wallet
	reads int
}

func (w *closedAssetSecretProbe) GetCommitSecret([]byte, uint32) *secp256k1.PrivateKey {
	w.reads++
	return nil
}

func TestClosedAssetRestoreOrdinaryCommitmentSecretUsesCapturedAccount(t *testing.T) {
	manager, channel, _, _ := closedAssetRestoreFixture(t)
	api, ok := any(manager).(closedAssetSecretBoundary)
	if !ok {
		t.Fatal("missing private captured-wallet commitment/secret boundary")
	}
	captured := channel.LocalWallet().Clone()
	manager.wallet.SetSubAccount(1)
	probe := &closedAssetSecretProbe{Wallet: captured}
	if err := api.receiveNewCommitmentForWallet(&RevocationInfo{Channel: channel}, &wwire.CommitSigInfo{}, nil, probe); err == nil || probe.reads != 1 {
		t.Fatalf("ordinary commitment secret not captured: reads=%d err=%v", probe.reads, err)
	}
	rev, err := api.revokeCurrentCommitmentForWallet(&RevocationInfo{Channel: channel}, captured)
	want := captured.GetCommitSecret(channel.PeerNodeId, uint32(channel.CommitHeight))
	if err != nil || rev == nil || !bytes.Equal(rev.Revocation[:], want.Serialize()) {
		t.Fatalf("ordinary revocation secret not captured: %v", err)
	}
	if manager.wallet.GetSubAccount() != 1 {
		t.Fatal("secret helper changed selected account")
	}
}
