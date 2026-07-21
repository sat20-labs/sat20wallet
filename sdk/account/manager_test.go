package account

import (
	"reflect"
	"strings"
	"testing"
)

func testBackup() Backup {
	return Backup{Version: Version, Wallets: []WalletBackup{{Name: "Primary", Mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", AccountCount: 2, SubAccounts: []SubAccount{{Index: 0, DID: "alice"}, {Index: 1, DID: "alice-work"}}}}}
}

func testQuestions() []QuestionAnswer {
	return []QuestionAnswer{
		{Question: KnowledgeQuestion{ID: "book-page", Prompt: "你最喜欢的一本书指定版本第十页最后十个字是什么？", IgnorePunctuation: true}, Answer: "月光落在安静的旧桥上", Confirmation: "月光落在安静的旧桥上"},
		{Question: KnowledgeQuestion{ID: "private-note", Prompt: "你长期保存的私人纸条中指定句子是什么？", IgnorePunctuation: true}, Answer: "yellow bicycle beside the winter river", Confirmation: "yellow bicycle beside the winter river"},
		{Question: KnowledgeQuestion{ID: "family-phrase", Prompt: "你与家人约定但从未公开的一段话是什么？", IgnorePunctuation: true}, Answer: "周日傍晚六点在老树下见", Confirmation: "周日傍晚六点在老树下见"},
	}
}

func TestCreateAndRecoverTwoOfThree(t *testing.T) {
	privateKey, publicKey, err := GenerateGuardianKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(nil)
	pkg, err := manager.CreateRecoveryPackage(CreateOptions{AccountID: strings.Repeat("a", 64), Backup: testBackup(), RecoveryMode: RecoveryMode2Of3, Questions: testQuestions(), GuardianMailboxID: strings.Repeat("b", 64), GuardianPublicKey: publicKey})
	if err != nil {
		t.Fatal(err)
	}
	dkvsShare, err := RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, []AnswerAttempt{{QuestionID: "book-page", Answer: "月光落在安静的旧桥上。"}, {QuestionID: "private-note", Answer: "yellow bicycle beside the winter river"}})
	if err != nil {
		t.Fatal(err)
	}
	guardianShare, err := DecryptGuardianShare(*pkg.GuardianCapsule, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	backup, secret, err := RecoverAccount(pkg.Envelope, dkvsShare, guardianShare)
	if err != nil {
		t.Fatal(err)
	}
	zero(secret)
	normalized, _ := NormalizeBackup(testBackup())
	if !reflect.DeepEqual(backup, normalized) {
		t.Fatalf("backup mismatch: %#v", backup)
	}
	if _, err := RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, []AnswerAttempt{{QuestionID: "book-page", Answer: "月光落在安静的旧桥上"}}); err == nil {
		t.Fatal("one question recovered DKVS share")
	}
}

func TestTwoOfTwoRequiresUserShare(t *testing.T) {
	manager := NewManager(nil)
	pkg, err := manager.CreateRecoveryPackage(CreateOptions{AccountID: strings.Repeat("c", 64), Backup: testBackup(), RecoveryMode: RecoveryMode2Of2, Questions: testQuestions()})
	if err != nil {
		t.Fatal(err)
	}
	dkvsShare, err := RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, []AnswerAttempt{{QuestionID: "book-page", Answer: "月光落在安静的旧桥上"}, {QuestionID: "family-phrase", Answer: "周日傍晚六点在老树下见"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, secret, err := RecoverAccount(pkg.Envelope, pkg.UserShare, dkvsShare); err != nil {
		t.Fatal(err)
	} else {
		zero(secret)
	}
}

func TestNewDeviceIsRecoveryRehearsal(t *testing.T) {
	manager := NewManager(nil)
	pkg, err := manager.CreateRecoveryPackage(CreateOptions{AccountID: strings.Repeat("d", 64), Backup: testBackup(), RecoveryMode: RecoveryMode2Of2, Questions: testQuestions()})
	if err != nil {
		t.Fatal(err)
	}
	dkvsShare, err := RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, []AnswerAttempt{{QuestionID: "book-page", Answer: "月光落在安静的旧桥上"}, {QuestionID: "private-note", Answer: "yellow bicycle beside the winter river"}})
	if err != nil {
		t.Fatal(err)
	}
	confirmed, persisted := false, false
	summary, err := RestoreOnNewDevice(NewDeviceRecoveryOptions{Envelope: pkg.Envelope, Shares: []RecoveryShare{pkg.UserShare, dkvsShare}, Confirm: func(summary RecoverySummary) (bool, error) { confirmed = true; return len(summary.Wallets) == 1, nil }, Persist: func(secret []byte, backup Backup) error { persisted = len(secret) == 32 && len(backup.Wallets) == 1; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed || !persisted || summary.PackageID != pkg.Envelope.Locator.PackageID {
		t.Fatal("rehearsal callbacks not completed")
	}
}
