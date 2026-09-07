package wallet

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

type channelSyncAcceptanceClient struct{ IndexerRPCClient }

func (*channelSyncAcceptanceClient) TestRawTx_Bitcoin([]string) error { return nil }

// Only indexer acceptance is stubbed; peer decoding, merkle checks, commitment
// signing/verification, persistence and runtime installation use production code.
func newSignedChannelSyncState(t *testing.T, client *channelHeartbeatTestClient) (*Manager, *Channel) {
	t.Helper()
	manager := newChannelHeartbeatTestManager(t, client)
	manager.wallet.SetSubAccount(2) // Payment key differs from the wallet node ID.
	manager.db = newMemoryKVDB()
	manager.status = newDefaultStatus()
	manager.status.CurrentWallet = manager.wallet.GetId()
	manager.status.CurrentAccount = manager.wallet.GetSubAccount()
	manager.walletInfoMap = map[int64]*WalletInfo{
		manager.wallet.GetId(): {WalletInDB: WalletInDB{Id: manager.wallet.GetId(), Accounts: 3}, Wallet: manager.wallet},
	}
	manager.ResetResvMaps()
	serverWallet := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", GetChainParam())
	manager.serverNode = NewNode(client, "test", SERVER_NODE, serverWallet.GetPaymentPubKey(), serverWallet.GetNodePubKey())
	manager.l1IndexerClient = NewIndexerRPCClientMgr()
	manager.l1IndexerClient.SetMaster(&channelSyncAcceptanceClient{})
	channelID, err := manager.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}
	data := NewChannelInDB()
	data.ChannelId, data.Address = channelID, channelID
	data.IsInitiator, data.Status, data.CommitHeight = true, CS_READY, 2
	data.LocalWalletId = manager.wallet.GetId()
	data.LocalChanCfg.WalletId = manager.wallet.GetSubAccount()
	data.PeerNodeId = serverWallet.GetNodePubKey().SerializeCompressed()
	data.LocalChanCfg.PaymentKey = manager.wallet.GetPaymentPubKey()
	data.LocalChanCfg.RevocationBasePoint = manager.wallet.GetRevocationBaseKey()
	data.RemoteChanCfg.PaymentKey = serverWallet.GetPaymentPubKey()
	data.RemoteChanCfg.RevocationBasePoint = serverWallet.GetRevocationBaseKey()
	data.RedeemScript, _, err = GetP2WSHscript(data.LocalChanCfg.PaymentKey.SerializeCompressed(), data.RemoteChanCfg.PaymentKey.SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	funding := safetyTestTx(81)
	funding.TxOut[0].Value = 100_000
	funding.TxOut[0].PkScript, err = GetP2WSHpkScript(data.RedeemScript)
	if err != nil {
		t.Fatal(err)
	}
	data.ChanPoint = indexer.GenerateTxOutput(funding, 0)
	data.LocalCommitment, data.RemoteCommitment = NewChannelCommitment(), NewChannelCommitment()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(data.ChanPoint.OutPoint(), nil, nil))
	tx.AddTxOut(wire.NewTxOut(90_000, funding.TxOut[0].PkScript))
	data.LocalCommitment.CommitTx = tx
	data.LocalCommitment.CommitSig, err = PartialSignTxWithWallet(serverWallet, tx, data.GetCommitmentPrefetchor(),
		data.RedeemScript, false, manager.wallet.GetPaymentPubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	data.RemoteCommitment.CommitTx = tx.Copy()
	data.StaticMerkleRoot = data.CalcStaticMerkleRoot()
	channel := &Channel{ChannelInDB: *data, manager: manager, localWallet: manager.wallet.Clone()}
	if err := manager.SaveChannelToDB(channel); err != nil {
		t.Fatal(err)
	}
	if err := manager.EnableChannel(channel); err != nil {
		t.Fatal(err)
	}

	peer := channel.ChannelInDB
	peer.CommitHeight++
	// Flip to the server perspective, including its unrelated local wallet ID.
	if err := restorePeerChannelPerspective(&peer, serverWallet, manager.wallet.GetNodePubKey().SerializeCompressed()); err != nil {
		t.Fatal(err)
	}
	peer.LocalWalletId = serverWallet.GetId()
	peer.StaticMerkleRoot = peer.CalcStaticMerkleRoot()
	if err := peer.PrepareForSave(); err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeToBytes(&peer)
	if err != nil {
		t.Fatal(err)
	}
	client.syncResponse = &wwire.ActionSyncResp{ChannelData: raw}
	client.response = &wwire.PingResp{PingResponse: &wwire.PingResponse{
		NextAction: wwire.STP_ACTION_SYNC, ActionParam: "restore", CommitHeight: peer.CommitHeight,
	}}
	return manager, channel
}

func TestChannelSyncKeepsOriginalWalletAfterSelectionChange(t *testing.T) {
	for _, operation := range []string{"sync", "heartbeat"} {
		for _, selection := range []string{"account", "wallet"} {
			t.Run(operation+"/"+selection, func(t *testing.T) {
				client := &channelHeartbeatTestClient{syncStarted: make(chan struct{}), syncRelease: make(chan struct{})}
				manager, original := newSignedChannelSyncState(t, client)
				originalWallet := manager.wallet.Clone()
				done := make(chan error, 1)
				go func() {
					if operation == "heartbeat" {
						manager.runChannelHeartbeatTick()
						done <- nil
					} else {
						done <- manager.SyncChannel("restore", client)
					}
				}()
				release := func() {
					select {
					case <-client.syncRelease:
					default:
						close(client.syncRelease)
					}
				}
				t.Cleanup(release)
				select {
				case <-client.syncStarted:
				case <-time.After(5 * time.Second):
					t.Fatal("sync request did not start")
				}
				if selection == "account" {
					manager.SwitchAccount(1)
				} else {
					other := NewInternalWalletWithMnemonic(channelHeartbeatTestMnemonic, "other wallet", GetChainParam())
					manager.mutex.Lock()
					manager.walletInfoMap[other.GetId()] = &WalletInfo{WalletInDB: WalletInDB{Id: other.GetId(), Accounts: 1}, Wallet: other}
					manager.mutex.Unlock()
					if err := manager.SwitchWallet(other.GetId(), ""); err != nil {
						t.Fatal(err)
					}
				}
				selected := manager.wallet.Clone()
				release()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("sync did not finish")
				}
				restored := manager.GetChannel(original.ChannelId)
				if restored != original || restored.CommitHeight != 3 ||
					restored.LocalWallet().GetWalletId() != originalWallet.GetWalletId() ||
					!restored.LocalWallet().GetPaymentPubKey().IsEqual(originalWallet.GetPaymentPubKey()) {
					t.Fatal("sync did not update the original channel with its original signer")
				}
				stored, err := manager.LoadChannelInDB(original.ChannelId)
				if err != nil || stored.CommitHeight != 3 || stored.LocalWalletId != originalWallet.GetId() ||
					stored.LocalChanCfg.WalletId != originalWallet.GetSubAccount() {
					t.Fatalf("original channel not persisted correctly: %v", err)
				}
				if manager.wallet.GetWalletId() != selected.GetWalletId() ||
					!manager.wallet.GetPaymentPubKey().IsEqual(selected.GetPaymentPubKey()) || manager.GetCurrentChannel() != nil ||
					manager.status.CurrentWallet != selected.GetId() || manager.status.CurrentAccount != selected.GetSubAccount() {
					t.Fatal("sync changed the newly selected wallet or installed its channel")
				}
				if !bytes.Equal(client.lastSyncRequest.PubKey, originalWallet.GetPaymentPubKey().SerializeCompressed()) {
					t.Fatal("sync signed for the newly selected wallet")
				}
			})
		}
	}
}

func TestChannelSyncRejectsMismatchedWalletAfterSelectionChange(t *testing.T) {
	client := &channelHeartbeatTestClient{}
	manager, original := newSignedChannelSyncState(t, client)
	manager.SwitchAccount(1)
	var peer ChannelInDB
	if err := DecodeFromBytes(client.syncResponse.ChannelData, &peer); err != nil {
		t.Fatal(err)
	}
	// Keep the original channel ID but substitute the newly selected account key.
	peer.RemoteChanCfg.PaymentKey = manager.wallet.GetPaymentPubKey()
	peer.StaticMerkleRoot = peer.CalcStaticMerkleRoot()
	if err := peer.PrepareForSave(); err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeToBytes(&peer)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.rebuildChannelFromPeerChanInfoForWallet(raw, original.LocalWallet())
	if err == nil || !strings.Contains(err.Error(), "local payment key does not match") {
		t.Fatalf("mismatched wallet error = %v", err)
	}
	if stored, err := manager.LoadChannelInDB(original.ChannelId); err != nil ||
		stored.CommitHeight != 2 || original.CommitHeight != 2 {
		t.Fatalf("mismatched response changed local channel: %v", err)
	}
}

func TestChannelSyncRejectsCanceledResponse(t *testing.T) {
	client := &channelHeartbeatTestClient{syncStarted: make(chan struct{}), syncRelease: make(chan struct{})}
	manager, original := newSignedChannelSyncState(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.syncChannelForWallet(ctx, "restore", client, original.LocalWallet()) }()
	<-client.syncStarted
	cancel()
	close(client.syncRelease)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled sync error = %v", err)
	}
	stored, err := manager.LoadChannelInDB(original.ChannelId)
	if err != nil || stored.CommitHeight != 2 || original.CommitHeight != 2 {
		t.Fatalf("canceled response changed local channel: %v", err)
	}
}
