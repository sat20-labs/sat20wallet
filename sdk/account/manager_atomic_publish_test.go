package account

import (
	"context"
	"testing"
)

type atomicPublishRepository struct {
	packageWrites int
}

func (r *atomicPublishRepository) SaveRecoveryPackage(_ context.Context, _ RecoveryPackage) error {
	r.packageWrites++
	return nil
}
func (*atomicPublishRepository) LoadRecoveryPackage(context.Context, Locator) (*RecoveryPackage, error) {
	return nil, ErrInvalidRecoveryPackage
}

func TestManagerPublishUsesAtomicRepositoryCapability(t *testing.T) {
	repository := &atomicPublishRepository{}
	manager := NewManager(repository)
	questions := []QuestionAnswer{
		{Question: KnowledgeQuestion{ID: "a", Prompt: "a"}, Answer: "answer one", Confirmation: "answer one"},
		{Question: KnowledgeQuestion{ID: "b", Prompt: "b"}, Answer: "answer two", Confirmation: "answer two"},
		{Question: KnowledgeQuestion{ID: "c", Prompt: "c"}, Answer: "answer three", Confirmation: "answer three"},
	}
	value, err := manager.CreateRecoveryPackage(CreateOptions{
		AccountID: "0000000000000000000000000000000000000000000000000000000000000000",
		Backup: Backup{Version: Version, Wallets: []WalletBackup{{
			Name: "wallet", Mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
			AccountCount: 1, SubAccounts: []SubAccount{{Index: 0, DID: "did"}},
		}}},
		RecoveryMode: RecoveryMode2Of2,
		Questions:    questions,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Publish(context.Background(), *value); err != nil {
		t.Fatal(err)
	}
	if repository.packageWrites != 1 {
		t.Fatalf("packageWrites=%d", repository.packageWrites)
	}
}
