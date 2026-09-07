package wallet

import (
	"errors"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

var errInjectedBatchRead = errors.New("injected batch read failure")

type batchReadFailDB struct{ indexer.KVDB }

func (*batchReadFailDB) BatchRead([]byte, bool, func([]byte, []byte) error) error {
	return errInjectedBatchRead
}

func TestDeleteAllKeysWithPrefixPropagatesScanFailure(t *testing.T) {
	database := newMemoryKVDB()
	key := []byte("prefix-key")
	if err := database.Write(key, []byte("value")); err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteAllKeysWithPrefix(&batchReadFailDB{KVDB: database}, []byte("prefix-")); !errors.Is(err, errInjectedBatchRead) {
		t.Fatalf("scan error = %v", err)
	}
	if _, err := database.Read(key); err != nil {
		t.Fatalf("scan failure deleted existing data: %v", err)
	}
}

func TestLoadAllReservationsPropagatesScanFailure(t *testing.T) {
	_, err := LoadAllResvFromDB(&batchReadFailDB{KVDB: newMemoryKVDB()}, nil)
	if !errors.Is(err, errInjectedBatchRead) {
		t.Fatalf("reservation scan error = %v", err)
	}
}

func TestLoadAllReservationsIgnoresUpperLayerTypes(t *testing.T) {
	database := newMemoryKVDB()
	if err := database.Write([]byte(GetResvKey("deploycontract", 7)), []byte("owned by transcend")); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadAllResvFromDB(database, nil)
	if err != nil {
		t.Fatalf("upper-layer reservation rejected: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("wallet SDK loaded upper-layer reservations: %v", loaded)
	}
}

func TestLoadAllReservationsRejectsMalformedCurrentRecord(t *testing.T) {
	database := newMemoryKVDB()
	if err := database.Write([]byte(GetResvKey(RESV_TYPE_LOCALACTION, 8)), []byte("not gob data")); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadAllResvFromDB(database, nil); err == nil {
		t.Fatal("malformed current reservation was accepted")
	}
}

type legacyLocalActionV202507 struct {
	ReservationBase
	Action      string
	ActionParam []byte
}

func TestLoadAllReservationsIgnoresKnownLegacyLocalActionShape(t *testing.T) {
	database := newMemoryKVDB()
	legacy := &legacyLocalActionV202507{
		ReservationBase: NewReservationBase(9, true, RS_CLOSED, nil),
		Action:          "ascend",
		ActionParam:     []byte("legacy-script"),
	}
	encoded, err := EncodeToBytes(legacy)
	if err != nil {
		t.Fatal(err)
	}
	key := []byte(GetResvKey(RESV_TYPE_LOCALACTION, 9))
	if err := database.Write(key, encoded); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadAllResvFromDB(database, nil)
	if err != nil {
		t.Fatalf("known legacy localaction shape rejected: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("legacy reservation was loaded: %v", loaded)
	}
	persisted, err := database.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	if string(persisted) != string(encoded) {
		t.Fatal("legacy reservation was rewritten")
	}
}

func TestContractInvokeHistoryWritesAreAtomic(t *testing.T) {
	database := newMemoryKVDB()
	item := &InvokeItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{Version: 1, Id: 7},
		InUtxo:                "input:0",
	}
	failing := &passwordChangeFailFlushDB{KVDB: database}
	if err := SaveContractInvokeHistoryItem(failing, "contract", item); err == nil {
		t.Fatal("history save unexpectedly survived an atomic flush failure")
	}
	for _, key := range []string{
		GetContractInvokeHistoryKey("contract", item.GetKey()),
		GetContractInvokeHistoryKey2("contract", item.GetInvokeUtxo()),
	} {
		if _, err := database.Read([]byte(key)); !errors.Is(err, indexer.ErrKeyNotFound) {
			t.Fatalf("failed history save persisted %s: %v", key, err)
		}
	}

	if err := SaveContractInvokeHistoryItem(database, "contract", item); err != nil {
		t.Fatal(err)
	}
	if err := backupContractInvokeHistoryItem(failing, "contract", item); err == nil {
		t.Fatal("history backup unexpectedly survived an atomic flush failure")
	}
	for _, key := range []string{
		GetContractInvokeHistoryKey("contract", item.GetKey()),
		GetContractInvokeHistoryKey2("contract", item.GetInvokeUtxo()),
	} {
		if _, err := database.Read([]byte(key)); err != nil {
			t.Fatalf("failed history backup removed %s: %v", key, err)
		}
	}
	if _, err := database.Read([]byte(GetContractInvokeHistoryBackupKey("contract", item.GetKey()))); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("failed history backup persisted backup record: %v", err)
	}
}
