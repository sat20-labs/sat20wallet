package account

import (
	"crypto/rand"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBackupSilentPayRoundTrip(t *testing.T) {
	backup := testBackup()
	backup.Wallets[0].SPEnabled = []uint32{1, 0}
	normalized, err := NormalizeBackup(backup)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{0, 1}
	if !reflect.DeepEqual(normalized.Wallets[0].SPEnabled, want) ||
		!reflect.DeepEqual(backup.Wallets[0].SPEnabled, []uint32{1, 0}) {
		t.Fatal("SP backup must have canonical indexes without mutating its input")
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Backup
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Wallets[0].SPEnabled, want) {
		t.Fatal("standalone backup encoding lost SP accounts")
	}
	locator := Locator{Version: Version, AccountID: strings.Repeat("a", 64), PackageID: strings.Repeat("b", 32), RecoveryMode: RecoveryMode2Of2}
	encrypted, err := encryptBackup(testAccountSecret(), locator, normalized, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := decryptBackup(testAccountSecret(), locator, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recovered.Wallets[0].SPEnabled, want) {
		t.Fatal("encrypted backup recovery lost SP accounts")
	}
}

func TestBackupSilentPayRejectsInvalidAccounts(t *testing.T) {
	for _, item := range []struct {
		name    string
		indexes []uint32
	}{
		{"duplicate", []uint32{1, 1}},
		{"outside-account-count", []uint32{2}},
		{"overflow-index", []uint32{^uint32(0)}},
	} {
		t.Run(item.name, func(t *testing.T) {
			backup := testBackup()
			backup.Wallets[0].SPEnabled = item.indexes
			if _, err := NormalizeBackup(backup); err == nil {
				t.Fatal("backup accepted an invalid SP account scope")
			}
		})
	}
}

func TestRootBootstrapBackupKeepsOnlyRootSilentPayFlag(t *testing.T) {
	for _, item := range []struct {
		name string
		all  []uint32
		root []uint32
	}{
		{"root-and-child", []uint32{0, 1}, []uint32{0}},
		{"child-only", []uint32{1}, nil},
		{"ordinary-only", nil, nil},
	} {
		t.Run(item.name, func(t *testing.T) {
			backup := testBackup()
			backup.Wallets[0].SPEnabled = item.all
			root, err := RootBootstrapBackup(backup)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(root.Wallets[0].SPEnabled, item.root) {
				t.Fatal("root bootstrap kept an SP flag for a removed child account")
			}
			if _, err := NormalizeBackup(root); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(backup.Wallets[0].SPEnabled, item.all) {
				t.Fatal("root bootstrap mutated the source backup")
			}
		})
	}
}
