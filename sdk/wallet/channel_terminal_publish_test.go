package wallet

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
)

var errTerminalSave = errors.New("injected terminal channel save failure")

type terminalSaveFaultDB struct {
	indexer.KVDB
	key  string
	fail bool
	hits int
}

func (p *terminalSaveFaultDB) Write(key, value []byte) error {
	if p.fail && string(key) == p.key {
		p.hits++
		return errTerminalSave
	}
	return p.KVDB.Write(key, value)
}

type terminalBackupFunc func(*Channel, []byte) error

func (f terminalBackupFunc) BackupChannel(channel *Channel, raw []byte) error {
	return f(channel, raw)
}

func newTerminalPubFixture(t *testing.T) (*Manager, *Channel, *terminalSaveFaultDB) {
	t.Helper()
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("open terminal publication database")
	}
	t.Cleanup(func() { _ = database.Close() })
	w := NewInternalWalletWithMnemonic(channelHeartbeatTestMnemonic, "", GetChainParam())
	if w == nil {
		t.Fatal("create terminal publication wallet")
	}
	stored := savePendingFundingFixture(t, newMemoryKVDB(), w, "terminal-publication", 901)
	stored.Status = CS_READY
	fault := &terminalSaveFaultDB{KVDB: database, key: GetChannelKey(stored.ChannelId)}
	manager := &Manager{db: fault, wallet: w, channelMap: make(map[string]*Channel), nodeMap: make(map[string]string)}
	manager.resetResvMapsLocked()
	channel := &Channel{ChannelInDB: *stored, manager: manager, localWallet: w}
	if err := manager.SaveChannelInDB(&channel.ChannelInDB); err != nil {
		t.Fatal(err)
	}
	if err := manager.EnableChannel(channel); err != nil {
		t.Fatal(err)
	}
	return manager, channel, fault
}

func TestTerminalPublication(t *testing.T) {
	t.Run("write-failure", func(t *testing.T) {
		manager, channel, fault := newTerminalPubFixture(t)
		generation, err := captureChannelSafetyGeneration(channel)
		if err != nil {
			t.Fatal(err)
		}
		before, err := fault.Read([]byte(fault.key))
		if err != nil {
			t.Fatal(err)
		}
		backups := 0
		manager.SetChannelBackupHandler(terminalBackupFunc(func(*Channel, []byte) error { backups++; return nil }))
		fault.fail = true
		err = manager.persistTerminalTransition(channel, generation, CS_CLOSED_FORCELY)
		if !errors.Is(err, errTerminalSave) || fault.hits != 1 || backups != 0 {
			t.Fatalf("terminal save failure boundary: err=%v hits=%d backups=%d", err, fault.hits, backups)
		}
		after, readErr := fault.Read([]byte(fault.key))
		if readErr != nil || !bytes.Equal(before, after) || channel.Status != CS_READY || manager.GetChannel(channel.ChannelId) != channel || manager.GetActiveChannelWithId(channel.ChannelId) != channel {
			t.Fatal("failed local save published terminal state or removed the previous runtime")
		}
	})

	t.Run("replacement-generation", func(t *testing.T) {
		manager, channel, fault := newTerminalPubFixture(t)
		generation, err := captureChannelSafetyGeneration(channel)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := EncodeToBytes(&channel.ChannelInDB)
		if err != nil {
			t.Fatal(err)
		}
		var next ChannelInDB
		if err := DecodeFromBytes(raw, &next); err != nil {
			t.Fatal(err)
		}
		next.CommitHeight++
		replacement := &Channel{ChannelInDB: next, manager: manager, localWallet: channel.localWallet}
		if err := manager.SaveChannelInDB(&replacement.ChannelInDB); err != nil {
			t.Fatal(err)
		}
		if err := manager.EnableChannel(replacement); err != nil {
			t.Fatal(err)
		}
		before, err := fault.Read([]byte(fault.key))
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.persistTerminalTransition(channel, generation, CS_CLOSED_FORCELY); err == nil {
			t.Fatal("stale terminal publication accepted a replacement runtime")
		}
		after, readErr := fault.Read([]byte(fault.key))
		if readErr != nil || !bytes.Equal(before, after) || manager.GetChannel(channel.ChannelId) != replacement || replacement.Status != CS_READY || channel.Status != CS_READY {
			t.Fatal("stale terminal publication overwrote or removed the current generation")
		}
	})

	t.Run("backup-after-removal", func(t *testing.T) {
		manager, channel, _ := newTerminalPubFixture(t)
		generation, err := captureChannelSafetyGeneration(channel)
		if err != nil {
			t.Fatal(err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		finishBackup := func() { once.Do(func() { close(release) }) }
		defer finishBackup()
		manager.SetChannelBackupHandler(terminalBackupFunc(func(*Channel, []byte) error {
			close(entered)
			<-release
			return errors.New("optional backup is unavailable after local close")
		}))
		done := make(chan error, 1)
		go func() { done <- manager.persistTerminalTransition(channel, generation, CS_CLOSED_FORCELY) }()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("terminal publication did not reach optional backup")
		}
		stored, err := manager.LoadChannelInDB(channel.ChannelId)
		if err != nil || stored.Status != CS_CLOSED_FORCELY || manager.GetChannel(channel.ChannelId) != nil || manager.GetActiveChannelWithId(channel.ChannelId) != nil || manager.GetChannelByPeerWallet(channel.GetRemoteAddress()) != nil {
			t.Fatal("optional backup began before durable terminal and active-index removal")
		}
		if !channel.Mutex.TryLock() {
			t.Fatal("optional backup holds the channel commit lock")
		}
		channel.Mutex.Unlock()
		finishBackup()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("optional backup failure changed local close success: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("terminal publication did not return after backup release")
		}
	})
}
