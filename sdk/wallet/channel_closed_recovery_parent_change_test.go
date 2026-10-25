package wallet

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/txscript"
	indexer "github.com/sat20-labs/indexer/common"
)

var errClosedChoicesProbeBeforeOwner = errors.New("ordinary probe stopped before any owner write")

type closedChoicesOwnerWriteProbe struct {
	indexer.KVDB
	attempts int
}

func (d *closedChoicesOwnerWriteProbe) Write(key, value []byte) error {
	if bytes.HasPrefix(key, []byte(GetDBKeyPrefix()+DB_KEY_RESV+RESV_TYPE_LOCALACTION+"-")) {
		d.attempts++
		return errClosedChoicesProbeBeforeOwner
	}
	return d.KVDB.Write(key, value)
}
func assertClosedChoicesSignedFixture(t *testing.T, f *singleUTXOExecutionFixture) {
	t.Helper()
	if !f.rpc.IsTxConfirmed(f.rpc.parent.TxID()) || !f.stored.IsInitiator || f.stored.LocalWalletId != f.manager.wallet.GetId() {
		t.Fatal("fixture is not the same account's confirmed close")
	}
	txs, err := closedWithdrawalPackage(f.rpc.parent, f.stored.RemoteCommitment)
	if err != nil {
		t.Fatal(err)
	}
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, f.rpc.parent)
	for _, tx := range txs[1:] {
		if err := VerifySignedTx(tx, fetcher); err != nil {
			t.Fatal(err)
		}
		AddTxOutputsToPrevFetcher(fetcher, tx)
	}
}
func callReviewedClosedWithdrawal(m *Manager, channelID, parentID string) (int64, error) {
	if api, ok := interface{}(m).(interface {
		WithdrawClosedChannelExact(string, string) (int64, error)
	}); ok {
		return api.WithdrawClosedChannelExact(channelID, parentID)
	}
	// The baseline has only the existing C2 entry. This exercises the exact
	// missing binding as a real state-changing request, not an API-name oracle.
	return m.WithdrawClosedChannel(channelID)
}
func TestSTPClosedRecoveryReviewedParentChangedBeforeConfirm(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	installSingleUTXOLocalOther(t, f)
	assertClosedChoicesSignedFixture(t, f)
	reviewedParent := f.rpc.parent.TxID()
	// The replacement is independently complete, signed, confirmed and belongs
	// to the same channel/account. A malformed successor cannot hide the bug.
	newer := f.rpc.parent.Copy()
	newer.LockTime++
	f.rpc.parent = newer
	f.stored.RemoteCommitment.CommitTx = newer
	raw, _ := EncodeMsgTx(newer)
	f.rpc.raw[newer.TxID()] = raw
	installSingleUTXOLocalOther(t, f)
	assertClosedChoicesSignedFixture(t, f)
	probe := &closedChoicesOwnerWriteProbe{KVDB: f.manager.db}
	f.manager.db = probe
	_, err := callReviewedClosedWithdrawal(f.manager, f.channelID, reviewedParent)
	if err == nil {
		t.Fatal("old reviewed parent was accepted for a newer valid close")
	}
	if probe.attempts != 0 || len(f.manager.GetLocalActionReservations()) != 0 || len(f.manager.GetClosingReservations()) != 0 || *f.signatures != 0 || len(f.rpc.broadcasts) != 0 {
		t.Fatalf("stale reviewed parent reached owner creation: attempts=%d err=%v", probe.attempts, err)
	}
	if errors.Is(err, errClosedChoicesProbeBeforeOwner) || strings.Contains(err.Error(), "probe stopped") {
		t.Fatal("parent guard was not the rejection boundary")
	}
}
func TestSTPClosedRecoveryCurrentReviewedParentRoutesToExistingC2(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	installSingleUTXOLocalOther(t, f)
	assertClosedChoicesSignedFixture(t, f)
	probe := &closedChoicesOwnerWriteProbe{KVDB: f.manager.db}
	f.manager.db = probe
	_, err := callReviewedClosedWithdrawal(f.manager, f.channelID, f.rpc.parent.TxID())
	if !errors.Is(err, errClosedChoicesProbeBeforeOwner) || probe.attempts != 1 {
		t.Fatalf("valid explicit choice must reach the unchanged C2 owner boundary: attempts=%d err=%v", probe.attempts, err)
	}
	if len(f.manager.GetLocalActionReservations()) != 0 || *f.signatures != 0 || len(f.rpc.broadcasts) != 0 {
		t.Fatal("write-failure positive control executed beyond its boundary")
	}
}
