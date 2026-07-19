package rgb11wallet

import (
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
)

func TestEngineSnapshotRestoresPendingInvoiceAcrossLocalScopes(t *testing.T) {
	sourceDB := indexerdb.NewKVDB(t.TempDir())
	defer sourceDB.Close()
	source := NewEngineStore(sourceDB)
	if err := source.SetScope("wallet-1-account-3"); err != nil {
		t.Fatal(err)
	}
	engine, err := corewallet.NewEngine(source)
	if err != nil {
		t.Fatal(err)
	}
	request, err := engine.CreateReceive(corewallet.ReceiveParams{
		Network: invoicing.BitcoinTestnet3, RecipientID: "recipient",
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := source.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}

	targetDB := indexerdb.NewKVDB(t.TempDir())
	defer targetDB.Close()
	target := NewEngineStore(targetDB)
	if err := target.SetScope("wallet-99-account-3"); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportSnapshot(records); err != nil {
		t.Fatal(err)
	}
	restoredEngine, err := corewallet.NewEngine(target)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restoredEngine.LoadReceive(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Invoice != request.Invoice || restored.Seal != request.Seal || restored.RelayKey != request.RelayKey || restored.AckKey != request.AckKey {
		t.Fatalf("restored request differs: %#v", restored)
	}
}
