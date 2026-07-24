package wallet

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Continuous AUTOPAY keeps one active recoverable snapshot generation instead
// of charging for every historical generation indefinitely.
func TestRGB11PaidBackupPrunesSupersededSnapshot(t *testing.T) {
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
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}); !errors.Is(err, ErrDKVSRecordNotFound) {
		t.Fatalf("superseded snapshot remained active: %v", err)
	}
	if _, _, err := client.GetRGB11WalletSnapshot(pubKey, walletID, head2.OperationID,
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}); err != nil {
		t.Fatalf("latest snapshot missing: %v", err)
	}

	remote.mu.Lock()
	defer remote.mu.Unlock()
	activeManifests := 0
	for key, record := range remote.records {
		if record == nil || !strings.HasSuffix(key, "/manifest") {
			continue
		}
		manifest, err := dkvsindexer.ParseBlobManifestValue(record.Value, dkvsindexer.DefaultBlobPolicy())
		if err == nil && string(manifest.Metadata) == string(rgb11WalletSnapshotMetadata(walletID)) {
			activeManifests++
		}
	}
	if activeManifests != 1 {
		t.Fatalf("active wallet snapshot manifests=%d", activeManifests)
	}
}

func TestRGB11BackupPrunesOrphansBeforeCapacityIsNeeded(t *testing.T) {
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
	orphanID := [32]byte{0x7f}
	if _, _, err := client.PutBlob(
		manager.wallet,
		hex.EncodeToString(orphanID[:]),
		[]byte("orphan"),
		rgb11WalletSnapshotMetadata(walletID),
		dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000},
	); err != nil {
		t.Fatal(err)
	}

	remote.mu.Lock()
	remote.maxRecords = len(remote.records)
	remote.mu.Unlock()

	createRGB11MultiDeviceInvoice(t, manager, "capacity-two")
	head2, _, err := manager.BackupRGB11WalletState(client, walletID, head1, dkvsindexer.RecordOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if head2.Seq != head1.Seq+1 {
		t.Fatalf("head sequence=%d want=%d", head2.Seq, head1.Seq+1)
	}
	if _, _, err := client.GetRGB11WalletSnapshot(
		priv.PubKey().SerializeCompressed(),
		walletID,
		orphanID,
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())},
	); !errors.Is(err, ErrDKVSRecordNotFound) {
		t.Fatalf("orphan snapshot remained active: %v", err)
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
