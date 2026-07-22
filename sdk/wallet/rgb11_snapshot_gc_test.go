package wallet

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

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
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())});
		!errors.Is(err, ErrDKVSRecordNotFound) {
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
