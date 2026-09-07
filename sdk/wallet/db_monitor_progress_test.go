package wallet

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
)

type monitorStatusTestDB struct {
	indexer.KVDB
	write func([]byte, []byte) error
}

func (d *monitorStatusTestDB) Write(key, value []byte) error { return d.write(key, value) }

func TestSaveBlockMonitorProgressPublishesOnlyAfterWrite(t *testing.T) {
	for _, l1 := range []bool{true, false} {
		t.Run(fmt.Sprintf("L1=%t", l1), func(t *testing.T) {
			database := newMemoryKVDB()
			status := newDefaultStatus()
			status.SyncHeightL1, status.SyncHeightL2 = 100, 100
			status.BlockHashMapL1, status.BlockHashMapL2 = map[int]string{1: "old", 100: "hash-100"}, map[int]string{1: "old", 100: "hash-100"}
			manager := &Manager{status: status, db: database}
			if err := manager.SaveStatus(); err != nil {
				t.Fatal(err)
			}
			original, _ := database.Read([]byte(DB_KEY_STATUS))
			writeErr := errors.New("injected status write failure")
			fail := true
			manager.db = &monitorStatusTestDB{KVDB: database, write: func(key, value []byte) error {
				// The production method owns status.Lock here; neither field may
				// be published yet, regardless of the DB result.
				if status.SyncHeightL1 != 100 || status.SyncHeightL2 != 100 || len(status.BlockHashMapL1) != 2 || len(status.BlockHashMapL2) != 2 {
					t.Fatal("checkpoint was published before its write completed")
				}
				if fail {
					return writeErr
				}
				return database.Write(key, value)
			}}
			if err := manager.SaveBlockMonitorProgress(l1, 101, "hash-101", 12); !errors.Is(err, writeErr) {
				t.Fatalf("write error = %v", err)
			}
			persisted, _ := database.Read([]byte(DB_KEY_STATUS))
			if string(persisted) != string(original) {
				t.Fatal("failed write changed persisted status")
			}
			fail = false
			if err := manager.SaveBlockMonitorProgress(l1, 101, "hash-101", 12); err != nil {
				t.Fatal(err)
			}
			for _, actual := range []*Status{status, LoadStatusFromDB(database)} {
				height, hashes, other := actual.SyncHeightL2, actual.BlockHashMapL2, actual.SyncHeightL1
				if l1 {
					height, hashes, other = actual.SyncHeightL1, actual.BlockHashMapL1, actual.SyncHeightL2
				}
				if height != 101 || hashes[101] != "hash-101" || hashes[1] != "" || other != 100 {
					t.Fatalf("invalid checkpoint: height=%d hashes=%v other=%d", height, hashes, other)
				}
			}
			if manager.status != status {
				t.Fatal("replaced shared status pointer")
			}
		})
	}
}

func TestSaveBlockMonitorProgressSerializesWithStatusSave(t *testing.T) {
	database := newMemoryKVDB()
	entered, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	first.Store(true)
	manager := &Manager{status: newDefaultStatus()}
	manager.status.SyncHeightL1 = 100
	manager.db = &monitorStatusTestDB{KVDB: database, write: func(key, value []byte) error {
		if first.CompareAndSwap(true, false) {
			close(entered)
			<-release
		}
		return database.Write(key, value)
	}}
	saved, advanced := make(chan error, 1), make(chan error, 1)
	go func() { saved <- manager.SaveStatus() }()
	<-entered
	go func() { advanced <- manager.SaveBlockMonitorProgress(true, 101, "hash-101", 12) }()
	select {
	case err := <-advanced:
		close(release)
		<-saved
		t.Fatalf("checkpoint overtook an unfinished status save: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for _, done := range []chan error{saved, advanced} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("status writers did not finish")
		}
	}
	status := LoadStatusFromDB(database)
	if status.SyncHeightL1 != 101 || status.BlockHashMapL1[101] != "hash-101" {
		t.Fatal("older status save overwrote the monitor checkpoint")
	}
}
