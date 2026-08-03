package account

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRecoveryPackageStorageCodecIsCompactDeterministic(t *testing.T) {
	manager := NewManager(nil)
	pkg, err := manager.CreateRecoveryPackage(CreateOptions{
		AccountID:    "0000000000000000000000000000000000000000000000000000000000000000",
		Backup:       testBackup(),
		RecoveryMode: RecoveryMode2Of2,
		Questions:    testQuestions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := EncodeRecoveryPackageStorage(*pkg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeRecoveryPackageStorage(*pkg)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(struct {
		Envelope  Envelope
		Manifest  Manifest
		Capsule   DKVSShareCapsule
		Questions KnowledgeRecoveryBundle
	}{pkg.Envelope, pkg.Manifest, pkg.DKVSShareCapsule, pkg.KnowledgeBundle})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || len(first) >= len(legacy) || bytes.HasPrefix(first, []byte("{")) {
		t.Fatalf("compact=%d legacy=%d deterministic=%v", len(first), len(legacy), bytes.Equal(first, second))
	}
	decoded, err := DecodeRecoveryPackageStorage(first)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Envelope != pkg.Envelope || decoded.Manifest != pkg.Manifest ||
		decoded.DKVSShareCapsule != pkg.DKVSShareCapsule ||
		!equalKnowledgeRecoveryBundle(decoded.KnowledgeBundle, pkg.KnowledgeBundle) {
		t.Fatal("compact recovery package changed after round trip")
	}
	corrupt := append(append([]byte(nil), first...), 1)
	if _, err := DecodeRecoveryPackageStorage(corrupt); err == nil {
		t.Fatal("corrupt recovery package was accepted")
	}
}

func TestRootBootstrapBackupContainsOnlyRootAccount(t *testing.T) {
	backup := testBackup()
	backup.Wallets = append(backup.Wallets, WalletBackup{
		Name: "second", Mnemonic: backup.Wallets[0].Mnemonic,
		AccountCount: 1, SubAccounts: []SubAccount{{Index: 0, Name: "Second", DID: "did:second"}},
	})
	root, err := RootBootstrapBackup(backup)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Wallets) != 1 || root.Wallets[0].AccountCount != 1 ||
		len(root.Wallets[0].SubAccounts) != 1 || root.Wallets[0].SubAccounts[0].Index != 0 {
		t.Fatalf("root bootstrap=%+v", root)
	}
}

func TestGuardianCapsuleStorageCodecRoundTrip(t *testing.T) {
	_, publicKey, err := GenerateGuardianKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(nil)
	pkg, err := manager.CreateRecoveryPackage(CreateOptions{
		AccountID: strings.Repeat("a", 64), Backup: testBackup(),
		RecoveryMode: RecoveryMode2Of3, Questions: testQuestions(),
		GuardianMailboxID: strings.Repeat("b", 64), GuardianPublicKey: publicKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeGuardianCapsuleStorage(*pkg.GuardianCapsule)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeGuardianCapsuleStorage(encoded)
	if err != nil || *decoded != *pkg.GuardianCapsule {
		t.Fatalf("guardian capsule=%+v err=%v", decoded, err)
	}
}

func equalKnowledgeRecoveryBundle(left, right KnowledgeRecoveryBundle) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}
