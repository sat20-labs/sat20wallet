package wallet

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// The pre-1.1 state/blob profile diagnostic and its remote wrapper repair
// were retired: neither describes the independent indexed module protocol.

func retryDiagnosticDKVSWrite(write func() (*swire.DKVSRecord, error)) (*swire.DKVSRecord, error) {
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		record, err := write()
		if err == nil {
			return record, nil
		}
		lastErr = err
		if !IsDKVSErrorCode(err, dkvsindexer.ErrorCodeWriteConflict) {
			return nil, err
		}
		time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
	}
	return nil, lastErr
}

// TestDiagnosticGuardianPackageMatch checks whether an isolated PWA profile
// owns the guardian key for recovery packages without exposing that key or any
// recovered share.
func TestDiagnosticGuardianPackageMatch(t *testing.T) {
	snapshotPath := os.Getenv("SAT20_ACCOUNT_DIAG_SNAPSHOT")
	accountID := os.Getenv("SAT20_ACCOUNT_DIAG_ACCOUNT_ID")
	if snapshotPath == "" || len(accountID) != 64 {
		t.Skip("guardian diagnostic inputs are not set")
	}
	defer func() {
		if err := os.Remove(snapshotPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove sensitive diagnostic snapshot: %v", err)
		}
	}()
	encoded, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Origin   string            `json:"origin"`
		Password string            `json:"password"`
		Values   map[string]string `json:"values"`
	}
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	zeroBytes(encoded)
	if snapshot.Origin != "http://localhost:5173" || snapshot.Password == "" {
		t.Fatal("unexpected guardian snapshot")
	}
	password := snapshot.Password
	snapshot.Password = ""
	database := newMemoryKVDB()
	for key, value := range snapshot.Values {
		raw, decodeErr := base64.StdEncoding.DecodeString(value)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if err := database.Write([]byte(key), raw); err != nil {
			t.Fatal(err)
		}
		zeroBytes(raw)
		delete(snapshot.Values, key)
	}
	manager := &Manager{db: database, cfg: &common.Config{
		Env: "prd", Chain: "testnet",
		IndexerL2: &common.Indexer{Scheme: "https", Host: "apiprd.ordx.market", Proxy: "satsnet/testnet"},
	}}
	manager.walletInfoMap, err = diagnosticLoadWallets(database)
	if err != nil {
		t.Fatal(err)
	}
	guardianKey, keyErr := manager.LoadAccountGuardianPrivateKey(password)
	defer zeroBytes(guardianKey)
	client := NewSatsNetDKVSClient("https", "apiprd.ordx.market", "satsnet/testnet", nil)
	records, _, err := client.ListRecords("/personal/"+accountID+"/account/recovery", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, record := range records {
		if record == nil {
			continue
		}
		pkg, decodeErr := account.DecodeRecoveryPackageStorage(record.Value)
		if decodeErr != nil || pkg.Manifest.Guardian == nil {
			continue
		}
		match := false
		if keyErr == nil && len(guardianKey) == 32 {
			encodedCapsule, capsuleErr := manager.LoadAccountGuardianCapsule(
				AccountIndexerLocation{Scheme: "https", Host: "apiprd.ordx.market", Proxy: "satsnet/testnet"},
				pkg.Manifest.Guardian.MailboxID, pkg.Envelope.Locator.PackageID,
				pkg.Manifest.Guardian.ShareID)
			if capsuleErr == nil {
				var capsule account.GuardianShareCapsule
				if json.Unmarshal(encodedCapsule, &capsule) == nil {
					share, decryptErr := account.DecryptGuardianShare(capsule, guardianKey)
					match = decryptErr == nil && share.PackageID == pkg.Envelope.Locator.PackageID
				}
			}
		}
		if match {
			matched++
		}
		t.Logf("guardian package: package=%s local_match=%t", diagnosticShort(pkg.Envelope.Locator.PackageID), match)
	}
	t.Logf("guardian key: available=%t package_count=%d matched=%d",
		keyErr == nil && len(guardianKey) == 32, len(records), matched)
}

func diagnosticExactRemoteRecord(client *SatsNetDKVSClient, prefix, key string) (*swire.DKVSRecord, error) {
	records, _, err := client.ListRecords(prefix, 0, 100)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record != nil && record.Key == key {
			return record, nil
		}
	}
	return nil, nil
}

func diagnosticLoadWallets(database *memoryKVDB) (map[int64]*WalletInfo, error) {
	wallets := make(map[int64]*WalletInfo)
	err := database.BatchRead([]byte(DB_KEY_WALLET), false, func(_, value []byte) error {
		var stored WalletInDB
		if err := DecodeFromBytes(value, &stored); err != nil {
			return err
		}
		wallets[stored.Id] = &WalletInfo{WalletInDB: stored}
		return nil
	})
	return wallets, err
}

func diagnosticKeyClass(key string) string {
	switch {
	case strings.HasSuffix(key, accountManagementProfileDBKey):
		return "account-profile"
	case strings.HasPrefix(key, DB_KEY_WALLET):
		return "encrypted-wallet"
	case strings.HasPrefix(key, string(dkvsOutboxPrefix)):
		return "dkvs-outbox"
	default:
		return "selected"
	}
}

func diagnosticAccountID(value string) string {
	if len(value) < 12 {
		return "invalid"
	}
	return value[:6] + "..." + value[len(value)-6:]
}

func diagnosticShort(value string) string {
	if len(value) < 10 {
		return "invalid"
	}
	return value[:5] + "..." + value[len(value)-5:]
}

func diagnosticRecordSeq(record *swire.DKVSRecord) uint64 {
	if record == nil {
		return 0
	}
	return record.Seq
}

func diagnosticRecordHeight(record *swire.DKVSRecord) uint64 {
	if record == nil {
		return 0
	}
	return record.IssueHeight
}

func diagnosticRecordTTL(record *swire.DKVSRecord) uint64 {
	if record == nil {
		return 0
	}
	return record.TTL
}

func diagnosticRecordHash(record *swire.DKVSRecord) string {
	if record == nil {
		return ""
	}
	return dkvsindexer.RecordHash(record).String()
}
