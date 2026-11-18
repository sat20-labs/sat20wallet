//go:build guardian

package wallet

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

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
