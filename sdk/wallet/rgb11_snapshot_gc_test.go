package wallet

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Continuous retention updates one stable snapshot key and its head in the
// same batch-CAS transaction.
func TestRGB11PaidBackupReplacesStableSnapshot(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 901)
	remote := newRGB11MemoryDKVSHTTP()
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	walletID, err := manager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}

	createRGB11MultiDeviceInvoice(t, manager, "snapshot-one")
	head1, _, err := manager.BackupRGB11WalletState(client, walletID, nil, dkvsindexer.RecordOptions{})
	if err != nil {
		t.Fatal(err)
	}
	createRGB11MultiDeviceInvoice(t, manager, "snapshot-two")
	head2, _, err := manager.BackupRGB11WalletState(client, walletID, head1, dkvsindexer.RecordOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if head2.Seq != 2 {
		t.Fatalf("head sequence=%d", head2.Seq)
	}

	pubKey := priv.PubKey().SerializeCompressed()
	if _, _, err := client.GetRGB11WalletSnapshot(pubKey, walletID, head1.OperationID,
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("old operation unexpectedly resolved: %v", err)
	}
	if _, _, err := client.GetRGB11WalletSnapshot(pubKey, walletID, head2.OperationID,
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}); err != nil {
		t.Fatalf("latest snapshot missing: %v", err)
	}

	accountID := dkvsindexer.AccountID(pubKey)
	wantKey, err := dkvsindexer.BlobKey(accountID, RGB11WalletSnapshotBlobKey(walletID))
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	defer remote.mu.Unlock()
	blobCount := 0
	for key := range remote.records {
		if strings.HasPrefix(key, "/blob/"+accountID+"/") {
			blobCount++
		}
	}
	if blobCount != 1 || remote.records[wantKey] == nil {
		t.Fatalf("blob count=%d stable snapshot=%v", blobCount, remote.records[wantKey] != nil)
	}
}

func TestRGB11BackupUpdatesWithinFixedRecordCapacity(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 903)
	remote := newRGB11MemoryDKVSHTTP()
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	walletID, err := manager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}

	createRGB11MultiDeviceInvoice(t, manager, "capacity-one")
	head1, _, err := manager.BackupRGB11WalletState(client, walletID, nil, dkvsindexer.RecordOptions{})
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	initialRecords := len(remote.records)
	remote.maxRecords = initialRecords
	remote.mu.Unlock()

	createRGB11MultiDeviceInvoice(t, manager, "capacity-two")
	head2, _, err := manager.BackupRGB11WalletState(client, walletID, head1, dkvsindexer.RecordOptions{})
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	finalRecords := len(remote.records)
	remote.mu.Unlock()
	if head2.Seq != head1.Seq+1 || finalRecords != initialRecords {
		t.Fatalf("head sequence=%d records=%d want records=%d", head2.Seq, finalRecords, initialRecords)
	}
}

func TestRGB11FreeLocalBackupUsesNodeTTL(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 902)
	remote := newRGB11MemoryDKVSHTTP()
	remote.freeLocal.MaxTTL = 90_000
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)

	walletID, err := manager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	createRGB11MultiDeviceInvoice(t, manager, "free-local")
	_, headRecord, err := manager.BackupRGB11WalletState(
		client, walletID, nil, dkvsindexer.RecordOptions{TTL: 365 * 24 * 60 * 60 * 1000},
	)
	if err != nil {
		t.Fatal(err)
	}
	if headRecord.TTL != remote.freeLocal.MaxTTL {
		t.Fatalf("head TTL=%d want=%d", headRecord.TTL, remote.freeLocal.MaxTTL)
	}
	proof, err := dkvsindexer.ParseFeeProof(headRecord.FeeProof)
	if err != nil || proof.Mode != dkvsindexer.FeeModeFreeLocal {
		t.Fatalf("head proof=%+v err=%v", proof, err)
	}
	if manager.rgbManager.dkvsBackupMode != "temporary" ||
		manager.rgbManager.dkvsBackupTTL != remote.freeLocal.MaxTTL {
		t.Fatalf("backup mode=%q ttl=%d", manager.rgbManager.dkvsBackupMode, manager.rgbManager.dkvsBackupTTL)
	}

	remote.mu.Lock()
	defer remote.mu.Unlock()
	for key, record := range remote.records {
		if record == nil || (!strings.Contains(key, "/blob/") && key != headRecord.Key) {
			continue
		}
		recordProof, parseErr := dkvsindexer.ParseFeeProof(record.FeeProof)
		if parseErr != nil || recordProof.Mode != dkvsindexer.FeeModeFreeLocal {
			t.Fatalf("record %s proof=%+v err=%v", key, recordProof, parseErr)
		}
	}
}
