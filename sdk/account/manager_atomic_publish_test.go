package account

import (
	"context"
	"testing"
)

type atomicPublishRepository struct {
	packageWrites int
	itemWrites    int
}

func (r *atomicPublishRepository) SaveRecoveryPackage(_ context.Context, _ RecoveryPackage) error {
	r.packageWrites++
	return nil
}
func (r *atomicPublishRepository) SaveEnvelope(context.Context, Envelope) error {
	r.itemWrites++
	return nil
}
func (r *atomicPublishRepository) SaveDKVSShareCapsule(context.Context, Locator, DKVSShareCapsule) error {
	r.itemWrites++
	return nil
}
func (r *atomicPublishRepository) SaveKnowledgeBundle(context.Context, Locator, KnowledgeRecoveryBundle) error {
	r.itemWrites++
	return nil
}
func (r *atomicPublishRepository) SaveManifest(context.Context, Manifest) error {
	r.itemWrites++
	return nil
}
func (*atomicPublishRepository) LoadEnvelope(context.Context, Locator) (*Envelope, error) {
	return nil, ErrInvalidRecoveryPackage
}
func (*atomicPublishRepository) LoadDKVSShareCapsule(context.Context, Locator) (*DKVSShareCapsule, error) {
	return nil, ErrInvalidRecoveryPackage
}
func (*atomicPublishRepository) LoadKnowledgeBundle(context.Context, Locator) (*KnowledgeRecoveryBundle, error) {
	return nil, ErrInvalidRecoveryPackage
}
func (*atomicPublishRepository) LoadManifest(context.Context, Locator) (*Manifest, error) {
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
		Questions: questions,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Publish(context.Background(), *value); err != nil {
		t.Fatal(err)
	}
	if repository.packageWrites != 1 || repository.itemWrites != 0 {
		t.Fatalf("packageWrites=%d itemWrites=%d", repository.packageWrites, repository.itemWrites)
	}
}
