package wallet

import (
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// A local mined-evidence fixture: no transaction is sent. Its normal two-party
// scripts are signed by their own fixture wallets, not by a withheld/old key.
type closedWithdrawalMinedFixtureClient struct {
	IndexerRPCClient
	raw         map[string]string
	unconfirmed map[string]bool
	broadcasts  int
}

func (p *closedWithdrawalMinedFixtureClient) IsTxConfirmed(id string) bool {
	return p.raw[id] != "" && !p.unconfirmed[id]
}
func (p *closedWithdrawalMinedFixtureClient) GetRawTx(id string) (string, error) {
	if s := p.raw[id]; s != "" {
		return s, nil
	}
	return "", fmt.Errorf("transaction not present in local mined fixture")
}
func (p *closedWithdrawalMinedFixtureClient) GetTxHeight(string) (int, error) { return 10, nil }
func (p *closedWithdrawalMinedFixtureClient) BroadCastTx(*wire.MsgTx) (string, error) {
	p.broadcasts++
	return "", fmt.Errorf("unexpected broadcast in already-mined fixture")
}
func (p *closedWithdrawalMinedFixtureClient) BroadCastTxs([]*wire.MsgTx) error {
	p.broadcasts++
	return fmt.Errorf("unexpected broadcast in already-mined fixture")
}

func closedWithdrawalBoundsFixture(t *testing.T, missingCommit bool) (*Manager, *Channel, string, *closedWithdrawalMinedFixtureClient) {
	t.Helper()
	manager, current := newSignedChannelSyncState(t, &channelHeartbeatTestClient{})
	// The consumer requires a real atomic database. The original sync fixture
	// uses memoryKVDB, which deliberately lacks atomic write support.
	path := t.TempDir()
	disk := NewKVDB(path)
	if disk == nil {
		t.Fatal("open isolated atomic withdrawal fixture DB")
	}
	if err := manager.db.BatchRead(nil, false, func(key, value []byte) error { return disk.Write(key, value) }); err != nil {
		disk.Close()
		t.Fatal(err)
	}
	manager.db = disk
	t.Cleanup(func() { manager.db.Close() })
	server := NewInternalWalletWithMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", GetChainParam())
	parent := current.RemoteCommitment.CommitTx.Copy()
	fullSign := func(tx *wire.MsgTx, fetcher txscript.PrevOutputFetcher) {
		t.Helper()
		peer, err := PartialSignTxWithWallet(server, tx, fetcher, current.RedeemScript, false, manager.wallet.GetPaymentPubKey().SerializeCompressed())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := FinalSignTxWithWallet(manager.wallet, tx, fetcher, current.RedeemScript, false, server.GetPaymentPubKey().SerializeCompressed(), peer); err != nil {
			t.Fatal(err)
		}
		if err := VerifySignedTx(tx, fetcher); err != nil {
			t.Fatal(err)
		}
	}
	fullSign(parent, current.GetCommitmentPrefetchor())
	other := wire.NewMsgTx(2)
	point := wire.OutPoint{Hash: parent.TxHash(), Index: 0}
	other.AddTxIn(wire.NewTxIn(&point, nil, nil))
	script, err := GetP2TRpkScript(manager.wallet.GetPaymentPubKey())
	if err != nil {
		t.Fatal(err)
	}
	other.AddTxOut(wire.NewTxOut(parent.TxOut[0].Value-1000, script))
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, parent)
	fullSign(other, fetcher)
	current.RemoteCommitment.CommitTx = parent.Copy()
	current.RemoteCommitment.Others = []*CommitmentTx{{CommitTx: other.Copy()}}
	if missingCommit {
		current.RemoteCommitment.Others = []*CommitmentTx{{}}
	}
	if err := manager.SaveChannelToDB(current); err != nil {
		t.Fatal(err)
	}
	rawParent, _ := EncodeMsgTx(parent)
	rawOther, _ := EncodeMsgTx(other)
	client := &closedWithdrawalMinedFixtureClient{raw: map[string]string{parent.TxID(): rawParent, other.TxID(): rawOther}, unconfirmed: map[string]bool{}}
	manager.l1IndexerClient.SetMaster(client)
	if manager.GetChannel(current.ChannelId) != current {
		t.Fatal("fixture did not preserve its actual cached runtime pointer")
	}
	return manager, current, path, client
}

func TestClosedWithdrawalRetiresCurrentCachedRuntimePointer(t *testing.T) {
	manager, current, _, _ := closedWithdrawalBoundsFixture(t, false)
	id, err := manager.WithdrawClosedChannel(current.ChannelId)
	if err != nil || id <= 0 {
		t.Fatalf("complete already-mined ordinary withdrawal: id=%d err=%v", id, err)
	}
	if got := manager.GetChannel(current.ChannelId); got != nil {
		t.Fatal("completed withdrawal left the current cached channel installed")
	}
	stored, err := manager.LoadChannelInDB(current.ChannelId)
	if err != nil || stored.Status != CS_CLOSED_FORCELY {
		t.Fatalf("completed withdrawal durable status: %v", err)
	}
}

func TestClosedWithdrawalRejectsMissingOtherCommitWithoutCompleting(t *testing.T) {
	manager, current, _, _ := closedWithdrawalBoundsFixture(t, true)
	id, err := manager.WithdrawClosedChannel(current.ChannelId)
	if err == nil {
		t.Fatalf("missing signed Other CommitTx was silently treated as completed parent-only withdrawal: id=%d", id)
	}
	if manager.GetChannel(current.ChannelId) != current {
		t.Fatal("missing dependency retired the current channel")
	}
	if len(manager.GetClosingReservations()) != 0 || len(manager.GetLocalActionReservations()) != 0 {
		t.Fatal("missing dependency created a durable withdrawal task instead of refusing preparation")
	}
}

// Reopen the actual isolated DB and hydrate only persisted reservation/channel
// runtime. This is a restart boundary control, not a full PWA/browser startup.
func TestClosedWithdrawalClosingMonitorResumesExactBytesAfterDatabaseReopen(t *testing.T) {
	manager, current, path, client := closedWithdrawalBoundsFixture(t, false)
	otherID := current.RemoteCommitment.Others[0].CommitTx.TxID()
	client.unconfirmed[otherID] = true
	id, err := manager.WithdrawClosedChannel(current.ChannelId)
	if err != nil || id <= 0 {
		t.Fatalf("ordinary confirmed-parent pending child: id=%d err=%v", id, err)
	}
	before, err := manager.db.Read([]byte(GetResvKey(RESV_TYPE_CLOSE, id)))
	if err != nil {
		t.Fatal(err)
	}
	var original ClosingDataInDB
	if err := DecodeFromBytes(before, &original); err != nil {
		t.Fatal(err)
	}
	if original.Status == RS_CLOSED {
		t.Fatal("unconfirmed Other was prematurely completed")
	}
	if err := manager.db.Close(); err != nil {
		t.Fatal(err)
	}
	manager.db = NewKVDB(path)
	if manager.db == nil {
		t.Fatal("reopen isolated withdrawal DB")
	}
	manager.ResetResvMaps()
	manager.channelMap = map[string]*Channel{}
	manager.nodeMap = map[string]string{}
	reservations, err := LoadAllResvFromDB(manager.db, manager)
	if err != nil {
		t.Fatal(err)
	}
	for _, resv := range reservations {
		if err := resv.InitLocalWallet(manager); err != nil {
			t.Fatal(err)
		}
		manager.AddResv(resv)
	}
	manager.rehydratePendingClosingRuntime()
	if err := manager.rehydrateClosedChannelIntents(); err != nil {
		t.Fatal(err)
	}
	pending, ok := manager.GetClosingReservation(id)
	if !ok || pending == nil || pending.Channel == nil || !equalSTPBitcoinChain(pending.PreTxs, original.PreTxs) {
		t.Fatal("restart lost the exact durable signed package or closing runtime")
	}
	client.unconfirmed[otherID] = false
	manager.HandleChannelReservationStatus(true)
	after, err := manager.db.Read([]byte(GetResvKey(RESV_TYPE_CLOSE, id)))
	if err != nil {
		t.Fatal(err)
	}
	var completed ClosingDataInDB
	if err := DecodeFromBytes(after, &completed); err != nil {
		t.Fatal(err)
	}
	if completed.Status != RS_CLOSED || !equalSTPBitcoinChain(completed.PreTxs, original.PreTxs) {
		t.Fatal("closing monitor failed to complete the original exact bytes after restart")
	}
	if client.broadcasts != 0 {
		t.Fatal("visible pending package was needlessly rebroadcast")
	}
	if manager.GetChannel(current.ChannelId) != nil {
		t.Fatal("restart installed a spendable old channel")
	}
}
