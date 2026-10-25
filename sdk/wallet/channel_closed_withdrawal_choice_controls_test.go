package wallet

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

type closedWithdrawalUnknownOpen struct {
	NodeRPCClient
	calls int
}

func (p *closedWithdrawalUnknownOpen) SendOpenChannelReq(info *FundingReservation) error {
	p.calls++
	if info == nil || info.Req == nil || len(info.ReqSig) == 0 {
		return fmt.Errorf("ordinary fixture has no signed Open request")
	}
	return fmt.Errorf("ordinary transport response unavailable")
}

func TestClosedWithdrawalRejectsActualUncertainReopenChoice(t *testing.T) {
	manager, current, _, client := closedWithdrawalBoundsFixture(t, false)
	delete(client.raw, current.RemoteCommitment.Others[0].CommitTx.TxID())
	manager.DisableChannel(current)
	current.Status = CS_CLOSED_FORCELY
	current.ClosingTx = current.RemoteCommitment.CommitTx.Copy()
	if err := manager.SaveChannelToDB(current); err != nil {
		t.Fatal(err)
	}
	peer := &closedWithdrawalUnknownOpen{NodeRPCClient: manager.serverNode.client}
	manager.serverNode.SetRPCClient(peer)
	manager.refreshTimeL1, manager.feeRateL1 = time.Now().Unix(), 1
	retained := indexer.GenerateTxOutput(current.RemoteCommitment.CommitTx, 0)
	if _, err := manager.FunderInitReOpenProcess(retained.Value(), retained, "reopen", false, true, nil, ""); err == nil || peer.calls != 1 {
		t.Fatalf("real signed Open unknown boundary not reached: calls=%d err=%v", peer.calls, err)
	}
	if _, err := manager.WithdrawClosedChannel(current.ChannelId); err == nil {
		t.Fatal("uncertain reopen was allowed to race old Other withdrawal")
	}
	if len(manager.GetClosingReservations()) != 0 || client.broadcasts != 0 {
		t.Fatal("uncertain reopen published a withdrawal")
	}
	for _, item := range manager.GetLocalActionReservations() {
		owner, ok := item.(*LocalActionPerformData)
		if !ok {
			continue
		}
		owner.RLock()
		intent, err := loadClosedChannelIntent(owner)
		owner.RUnlock()
		if err != nil {
			t.Fatal(err)
		}
		if intent.Choice != "reopen" || intent.Request == nil || len(intent.RequestSignature) == 0 {
			t.Fatal("refused withdrawal discarded or changed the exact reopen intent")
		}
	}
}

func TestClosedWithdrawalAtomicFailurePrecedesBroadcastAndCanRetry(t *testing.T) {
	manager, current, _, client := closedWithdrawalBoundsFixture(t, false)
	original, err := manager.db.Read([]byte(GetChannelKey(current.ChannelId)))
	if err != nil {
		t.Fatal(err)
	}
	failed := &nativeSettlementFaultDB{KVDB: manager.db, failFlush: true}
	manager.db = failed
	if _, err := manager.WithdrawClosedChannel(current.ChannelId); !errors.Is(err, errNativeSettlementWrite) {
		t.Fatalf("atomic flush failure not propagated: %v", err)
	}
	after, err := manager.db.Read([]byte(GetChannelKey(current.ChannelId)))
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("failed atomic withdrawal changed its channel")
	}
	if manager.GetChannel(current.ChannelId) != current || len(manager.GetClosingReservations()) != 0 || client.broadcasts != 0 {
		t.Fatal("failed persistence retired/broadcast/published a closing child")
	}
	for _, item := range manager.GetLocalActionReservations() {
		if item.GetStatus() == closedIntentWithdrawn {
			t.Fatal("failed persistence completed withdrawal")
		}
	}
	failed.failFlush = false
	if id, err := manager.WithdrawClosedChannel(current.ChannelId); err != nil || id <= 0 {
		t.Fatalf("safe same-choice retry after local persistence recovery: id=%d err=%v", id, err)
	}
	if len(manager.GetLocalActionReservations()) != 1 {
		t.Fatal("local persistence retry created duplicate recovery owners")
	}
}

func TestClosedWithdrawalCompletedParentDoesNotBlockNewConfirmedParent(t *testing.T) {
	manager, current, _, client := closedWithdrawalBoundsFixture(t, false)
	firstID, err := manager.WithdrawClosedChannel(current.ChannelId)
	if err != nil {
		t.Fatal(err)
	}
	firstRaw, err := manager.db.Read([]byte(GetResvKey(RESV_TYPE_CLOSE, firstID)))
	if err != nil {
		t.Fatal(err)
	}
	// A separate normal already-mined fixture funding generation, with the same
	// payment pair but a new exact parent. This tests the namespace boundary,
	// not a replacement for the real Reopen handshake integration.
	funding := safetyTestTx(93)
	funding.TxOut[0].Value = 100000
	funding.TxOut[0].PkScript = current.GetChannelPkScript()
	current.ChanPoint = indexer.GenerateTxOutput(funding, 0)
	parent := wire.NewMsgTx(2)
	parent.AddTxIn(current.ChanPoint.TxIn())
	parent.AddTxOut(wire.NewTxOut(90000, current.GetChannelPkScript()))
	server := NewInternalWalletWithMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", GetChainParam())
	sign := func(tx *wire.MsgTx, fetcher txscript.PrevOutputFetcher) {
		t.Helper()
		sig, err := PartialSignTxWithWallet(server, tx, fetcher, current.RedeemScript, false, manager.wallet.GetPaymentPubKey().SerializeCompressed())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := FinalSignTxWithWallet(manager.wallet, tx, fetcher, current.RedeemScript, false, server.GetPaymentPubKey().SerializeCompressed(), sig); err != nil {
			t.Fatal(err)
		}
		if err := VerifySignedTx(tx, fetcher); err != nil {
			t.Fatal(err)
		}
	}
	sign(parent, current.GetCommitmentPrefetchor())
	other := wire.NewMsgTx(2)
	other.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: parent.TxHash()}, nil, nil))
	script, err := GetP2TRpkScript(manager.wallet.GetPaymentPubKey())
	if err != nil {
		t.Fatal(err)
	}
	other.AddTxOut(wire.NewTxOut(89000, script))
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, parent)
	sign(other, fetcher)
	current.RemoteCommitment = NewChannelCommitment()
	current.RemoteCommitment.CommitTx = parent
	current.RemoteCommitment.Others = []*CommitmentTx{{CommitTx: other}}
	current.CommitHeight++
	current.Status = CS_READY
	current.ClosingTx = nil
	if err := manager.SaveChannelToDB(current); err != nil {
		t.Fatal(err)
	}
	if err := manager.EnableChannel(current); err != nil {
		t.Fatal(err)
	}
	client.raw[parent.TxID()], _ = EncodeMsgTx(parent)
	client.raw[other.TxID()], _ = EncodeMsgTx(other)
	secondID, err := manager.WithdrawClosedChannel(current.ChannelId)
	if err != nil || secondID <= 0 || secondID == firstID {
		t.Fatalf("new confirmed parent blocked by old terminal choice: first=%d second=%d err=%v", firstID, secondID, err)
	}
	after, err := manager.db.Read([]byte(GetResvKey(RESV_TYPE_CLOSE, firstID)))
	if err != nil || !bytes.Equal(after, firstRaw) {
		t.Fatal("new parent mutated the old completed exact signed package")
	}
	if len(manager.GetLocalActionReservations()) != 2 {
		t.Fatal("new parent did not receive a distinct bounded recovery identity")
	}
}
