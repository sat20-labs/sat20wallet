package account

import (
	"bytes"
	"strings"
	"testing"
)

func TestRecoveryCapsuleStore(t *testing.T) {
	private, public, err := GenerateGuardianKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer zero(private)
	pkg, err := NewManager(nil).CreateRecoveryPackage(CreateOptions{
		AccountID: strings.Repeat("a", 64), Backup: testBackup(), RecoveryMode: RecoveryMode2Of3,
		Questions: testQuestions(), GuardianMailboxID: strings.Repeat("b", 64), GuardianPublicKey: public,
	}, testAccountSecret())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeRecoveryPackageStorage(*pkg)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EncodeRecoveryPackageStorage(*pkg)
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatal("recovery package encoding must remain deterministic", err)
	}
	stored, err := DecodeRecoveryPackageStorage(encoded)
	if err != nil || stored.GuardianCapsule == nil || *stored.GuardianCapsule != *pkg.GuardianCapsule || *stored.Manifest.Guardian != *pkg.Manifest.Guardian {
		t.Fatal("stored recovery package lost the encrypted capsule or delivery reference", err)
	}
	if stored.UserShare.Data != "" || stored.UserShare.PackageID != "" {
		t.Fatal("storage exposed the user's recovery share")
	}
	guardian, err := DecryptGuardianShare(*stored.GuardianCapsule, private)
	if err != nil {
		t.Fatal(err)
	}
	backup, secret, err := RecoverAccount(stored.Envelope, pkg.UserShare, guardian)
	if err != nil || !bytes.Equal(secret, testAccountSecret()) || len(backup.Wallets) != 1 {
		t.Fatal("recovery without the Guardian mailbox failed", err)
	}
	defer zero(secret)
	guardianBytes, err := EncodeGuardianCapsuleStorage(*stored.GuardianCapsule)
	if err != nil || guardianBytes[len(guardianStorageMagic)] != 1 {
		t.Fatal("the independent Guardian storage protocol changed", err)
	}
}

func TestRecoveryCapsuleBindings(t *testing.T) {
	_, public, err := GenerateGuardianKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := NewManager(nil).CreateRecoveryPackage(CreateOptions{
		AccountID: strings.Repeat("a", 64), Backup: testBackup(), RecoveryMode: RecoveryMode2Of3,
		Questions: testQuestions(), GuardianMailboxID: strings.Repeat("b", 64), GuardianPublicKey: public,
	}, testAccountSecret())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"package", "share", "hash", "missing"} {
		t.Run(name, func(t *testing.T) {
			changed := *pkg
			capsule, reference := *pkg.GuardianCapsule, *pkg.Manifest.Guardian
			changed.GuardianCapsule, changed.Manifest.Guardian = &capsule, &reference
			switch name {
			case "package":
				capsule.PackageID = strings.Repeat("1", 32)
			case "share":
				capsule.ShareID = strings.Repeat("2", 16)
			case "hash":
				reference.CapsuleHash = strings.Repeat("3", 64)
			case "missing":
				changed.GuardianCapsule = nil
			}
			if _, err := EncodeRecoveryPackageStorage(changed); err == nil {
				t.Fatal("mismatched Guardian recovery material was accepted")
			}
		})
	}
}
