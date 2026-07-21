package account

import (
	"strings"
	"testing"
)

func TestKnowledgeRecoveryToleratesSmallAnswerError(t *testing.T) {
	manager := NewManager(nil)
	pkg, err := manager.CreateRecoveryPackage(CreateOptions{
		AccountID:    strings.Repeat("e", 64),
		Backup:       testBackup(),
		RecoveryMode: RecoveryMode2Of2,
		Questions:    testQuestions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	share, err := RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, []AnswerAttempt{
		{QuestionID: "book-page", Answer: "月光落在安静的古桥上"},
		{QuestionID: "private-note", Answer: "yellow bicycle beside the winter river"},
	})
	if err != nil {
		t.Fatalf("small answer change should be recoverable: %v", err)
	}
	if share.Role != ShareRoleDKVS {
		t.Fatalf("unexpected role %s", share.Role)
	}
}

func TestKnowledgeRecoveryRejectsUnrelatedAnswers(t *testing.T) {
	manager := NewManager(nil)
	pkg, err := manager.CreateRecoveryPackage(CreateOptions{
		AccountID:    strings.Repeat("f", 64),
		Backup:       testBackup(),
		RecoveryMode: RecoveryMode2Of2,
		Questions:    testQuestions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, []AnswerAttempt{
		{QuestionID: "book-page", Answer: "这是一个完全不同且无法匹配的回答"},
		{QuestionID: "private-note", Answer: "another completely unrelated private answer"},
	}); err == nil {
		t.Fatal("unrelated answers recovered the DKVS share")
	}
}
