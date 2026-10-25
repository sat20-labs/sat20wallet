package wallet

import (
	"bytes"
	indexer "github.com/sat20-labs/indexer/common"
	"testing"
)

type singleUTXOParentChangesBeforeOwner struct {
	indexer.KVDB
	fixture *singleUTXOExecutionFixture
	reads   int
	changed bool
}

func (d *singleUTXOParentChangesBeforeOwner) Read(key []byte) ([]byte, error) {
	if bytes.Equal(key, []byte(GetChannelKey(d.fixture.channelID))) {
		d.reads++
		if d.reads == 2 {
			f := d.fixture
			next := f.rpc.parent.Copy()
			next.LockTime++
			f.rpc.parent = next
			f.stored.RemoteCommitment.CommitTx = next
			raw, err := EncodeMsgTx(next)
			if err != nil {
				return nil, err
			}
			f.rpc.raw[next.TxID()] = raw
			stored, err := EncodeToBytes(f.stored)
			if err != nil {
				return nil, err
			}
			if err = d.KVDB.Write(key, stored); err != nil {
				return nil, err
			}
			d.changed = true
		}
	}
	return d.KVDB.Read(key)
}
func TestSTPSingleUTXOTopupParentChangedAtOwnerSnapshotCannotChooseOrSign(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	quote := f.quote(t)
	probe := &singleUTXOParentChangesBeforeOwner{KVDB: f.manager.db, fixture: f}
	f.manager.db = probe
	if _, err := singleUTXOTopupAPIForTest(t, f.manager).ConfirmClosedChannelSingleUTXOTopup(quote); err == nil {
		t.Fatal("changed reviewed parent was accepted between revalidation and owner snapshot")
	}
	if !probe.changed || probe.reads != 2 {
		t.Fatalf("fixture did not reach the actual owner stored-snapshot boundary: reads=%d changed=%v", probe.reads, probe.changed)
	}
	if *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 || len(f.manager.utxoLockerL1.GetLockedUtxoList()) != 0 {
		t.Fatal("changed owner snapshot created a recovery choice, signature, locks or funding")
	}
}
