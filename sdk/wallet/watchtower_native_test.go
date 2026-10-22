package wallet

import (
	"bytes"
	"errors"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func TestNativeWatchtowerProofAndRetrySurviveRestart(t *testing.T) {
	f := newNativeRecoveryFixture(t)
	commit, punish := f.channel.RemoteCommitment.CommitTx, minimalPersistedCommitmentTx(91)
	// Invalid imported proof material is deliberate: a journal can be intact
	// while its canonical chain receipt is not yet usable. The real importer
	// must reject it and retain the complete recovery package for diagnosis.
	proof := []byte("unavailable native canonical receipt")
	record := &nativePunishRecord{Version: 1, ChannelID: f.channel.ChannelId, CommitTxID: commit.TxID(),
		Transactions: []*wire.MsgTx{punish}, Proofs: map[string]map[string][]byte{punish.TxID(): {"rgb11": proof}}}
	if err := saveNativePunishRecord(f.db, record); err != nil {
		t.Fatal(err)
	}
	tower := newWatchTower(f.manager, false)
	if err := tower.markPunishPending(commit.TxID()); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db.KVDB = NewKVDB(f.directory)
	tower = newWatchTower(f.manager, false)
	f.manager.watchTower = tower
	if !tower.HasCommitTx(commit.TxID()) || !tower.HasBroadcastedCommitTx(commit.TxID()) {
		t.Fatal("watchtower lost durable native punishment or retry intent")
	}
	loaded, err := loadNativePunishRecord(f.db, f.channel.ChannelId, commit.TxID())
	if err != nil || loaded == nil || !bytes.Equal(loaded.Proofs[punish.TxID()]["rgb11"], proof) ||
		!equalSTPBitcoinChain(record.Transactions, loaded.Transactions) {
		t.Fatalf("native package changed across reopen: %v", err)
	}
	if err := f.manager.completePunishBroadcastState(f.channel.ChannelId, commit.TxID()); err == nil {
		t.Fatal("mempool-only native punishment was cleaned up")
	}
	f.rpc.confirmed[punish.TxID()] = true
	f.rpc.raw[punish.TxID()], err = EncodeMsgTx(punish)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.completePunishBroadcastState(f.channel.ChannelId, commit.TxID()); err == nil {
		t.Fatal("invalid native Current receipt was accepted after Bitcoin confirmation")
	}
	if !tower.HasCommitTx(commit.TxID()) || !tower.HasBroadcastedCommitTx(commit.TxID()) {
		t.Fatal("failed native import removed the recovery task")
	}
	if _, err := loadNativePunishRecord(f.db, f.channel.ChannelId, commit.TxID()); err != nil {
		t.Fatal(err)
	}
	mutated := *record
	mutated.Proofs = map[string]map[string][]byte{punish.TxID(): {"rgb11": []byte("replacement")}}
	if err := saveNativePunishRecord(f.db, &mutated); err == nil {
		t.Fatal("a different proof overwrote the native punishment package")
	}
	loaded, err = loadNativePunishRecord(f.db, f.channel.ChannelId, commit.TxID())
	if err != nil || !bytes.Equal(loaded.Proofs[punish.TxID()]["rgb11"], proof) {
		t.Fatal("rejected replacement changed durable evidence")
	}
}

func TestNativeWatchtowerCleanupIsAtomic(t *testing.T) {
	f := newNativeRecoveryFixture(t)
	commit, punish := f.channel.RemoteCommitment.CommitTx, minimalPersistedCommitmentTx(92)
	record := &nativePunishRecord{Version: 1, ChannelID: f.channel.ChannelId, CommitTxID: commit.TxID(),
		Transactions: []*wire.MsgTx{punish}, Proofs: map[string]map[string][]byte{punish.TxID(): {"rgb11": []byte("receipt")}}}
	if err := saveNativePunishRecord(f.db, record); err != nil {
		t.Fatal(err)
	}
	if err := saveBroadcastedCommitTx(f.db, commit.TxID()); err != nil {
		t.Fatal(err)
	}
	point := commit.TxIn[0].PreviousOutPoint.String()
	if err := saveUtxoCommitTxIdMap(f.db, f.channel.ChannelId, point, map[string]bool{commit.TxID(): true}); err != nil {
		t.Fatal(err)
	}
	keys := []string{GetPunishTxKey(f.channel.ChannelId, commit.TxID()), GetBroadcastedCommitKey(commit.TxID()), GetUtxoToCommitTxIdMapKey(f.channel.ChannelId, point)}
	for _, failAt := range []int{1, 2, 3, 0} {
		f.db.operations, f.db.failOperation, f.db.failFlush = 0, failAt, failAt == 0
		if _, _, err := deleteAllWatchtowerDataWithPending(f.db, f.channel.ChannelId, commit.TxID()); !errors.Is(err, errNativeSettlementWrite) {
			t.Fatalf("native cleanup failure was not propagated: %v", err)
		}
		for _, key := range keys {
			if _, err := f.db.Read([]byte(key)); err != nil {
				t.Fatalf("failed cleanup removed %s: %v", key, err)
			}
		}
	}
	f.db.operations, f.db.failOperation, f.db.failFlush = 0, 0, false
	if _, _, err := deleteAllWatchtowerDataWithPending(f.db, f.channel.ChannelId, commit.TxID()); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if _, err := f.db.Read([]byte(key)); !errors.Is(err, indexer.ErrKeyNotFound) {
			t.Fatalf("cleanup retained %s: %v", key, err)
		}
	}
}
