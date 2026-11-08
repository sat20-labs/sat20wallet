package account

import (
	"bytes"
	bip39 "github.com/tyler-smith/go-bip39"
	"strings"
	"testing"
)

func privateBackupFixture(t *testing.T) Backup {
	t.Helper()
	mnemonic, err := bip39.NewMnemonic(make([]byte, 16))
	if err != nil {
		t.Fatal("fixture generation failed")
	}
	scalar := make([]byte, 32)
	scalar[31] = 1
	return Backup{Version: BackupVersion, Wallets: []WalletBackup{
		{Name: "Root", Type: WalletMnemonic, Secret: []byte(mnemonic), AccountCount: 1, SubAccounts: []SubAccount{{Index: 0, Name: "Root account"}}},
		{Name: "Imported key", Type: WalletPrivateKey, Secret: scalar, AccountCount: 1, SubAccounts: []SubAccount{{Index: 0, Name: "Key account"}}},
	}}
}

func TestPrivateBackupRoundTrip(t *testing.T) {
	source := privateBackupFixture(t)
	normalized, err := NormalizeBackup(source)
	if err != nil {
		t.Fatal("typed backup rejected")
	}
	secret := bytes.Repeat([]byte{7}, 32)
	locator := Locator{Version: Version, AccountID: strings.Repeat("a", 64), PackageID: strings.Repeat("b", 32), RecoveryMode: RecoveryMode2Of2}
	sealed, err := encryptBackup(secret, locator, normalized, nil)
	if err != nil {
		t.Fatal("backup encryption failed")
	}
	restored, err := decryptBackup(secret, locator, sealed)
	if err != nil || len(restored.Wallets) != 2 || restored.Wallets[1].Type != WalletPrivateKey || !bytes.Equal(restored.Wallets[1].Secret, source.Wallets[1].Secret) {
		t.Fatal("typed backup material did not round-trip")
	}
	state := ManagedState{Version: ManagedStateVersion, RootFingerprint: strings.Repeat("a", 64), Wallets: []ManagedWallet{
		{Fingerprint: strings.Repeat("a", 64), Name: source.Wallets[0].Name, Type: WalletMnemonic, Secret: source.Wallets[0].Secret, AccountCount: 1, SubAccounts: source.Wallets[0].SubAccounts},
		{Fingerprint: strings.Repeat("b", 64), Name: source.Wallets[1].Name, Type: WalletPrivateKey, Secret: source.Wallets[1].Secret, AccountCount: 1, SubAccounts: source.Wallets[1].SubAccounts},
	}}
	envelope, err := SealManagedState(secret, locator.AccountID, state, nil)
	if err != nil {
		t.Fatal("typed state encryption failed")
	}
	opened, err := OpenManagedState(secret, locator.AccountID, envelope)
	if err != nil {
		t.Fatal("typed state decryption failed")
	}
	full, err := BackupFromManagedState(opened)
	if err != nil || len(full.Wallets) != 2 || full.Wallets[1].Type != WalletPrivateKey || !bytes.Equal(full.Wallets[1].Secret, source.Wallets[1].Secret) {
		t.Fatal("managed state omitted private wallet material")
	}
	zero(normalized.Wallets[1].Secret)
	if source.Wallets[1].Secret[31] != 1 {
		t.Fatal("normalization aliases source material")
	}
	for _, version := range []uint32{BackupVersion - 1, BackupVersion + 1} {
		invalid := privateBackupFixture(t)
		invalid.Version = version
		if _, err := NormalizeBackup(invalid); err == nil {
			t.Fatal("unsupported backup version accepted")
		}
	}
}

func TestPrivateBackupRejectsInvalid(t *testing.T) {
	for _, mutate := range []func(*WalletBackup){
		func(w *WalletBackup) { w.Type = WalletType(255) },
		func(w *WalletBackup) { w.Secret = make([]byte, 32) },
		func(w *WalletBackup) { w.Secret = bytes.Repeat([]byte{255}, 32) },
		func(w *WalletBackup) { w.Secret = w.Secret[:31] },
		func(w *WalletBackup) { w.AccountCount = 2; w.SubAccounts = append(w.SubAccounts, SubAccount{Index: 1}) },
		func(w *WalletBackup) { w.SubAccounts[0].Index = 1 },
		func(w *WalletBackup) { w.SPEnabled = []uint32{0} },
	} {
		value := privateBackupFixture(t)
		mutate(&value.Wallets[1])
		if _, err := NormalizeBackup(value); err == nil {
			t.Fatal("invalid private wallet accepted")
		}
	}
}
