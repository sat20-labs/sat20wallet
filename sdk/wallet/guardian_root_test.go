//go:build guardian

package wallet

import (
	"testing"
)

func TestAccountGuardianIdentityIsRootBoundNotSelectionBound(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager, _, _, _, _ := newLocalActivationFixture(t)
	rootID := manager.GetCurrentWalletId()
	childID, err := manager.ImportWallet(
		"legal winner thank year wave sausage worth useful legal winner thank yellow",
		"password",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SwitchWallet(rootID, "password"); err != nil {
		t.Fatal(err)
	}
	rootIdentity, err := manager.GetOrCreateAccountGuardianIdentity("password")
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.SwitchWallet(childID, "password"); err != nil {
		t.Fatal(err)
	}
	childSelectedIdentity, err := manager.GetOrCreateAccountGuardianIdentity("password")
	if err != nil {
		t.Fatal(err)
	}
	if childSelectedIdentity.MailboxID != rootIdentity.MailboxID ||
		childSelectedIdentity.PublicKey != rootIdentity.PublicKey {
		t.Fatalf("guardian identity followed current wallet: root=%+v selected=%+v",
			rootIdentity, childSelectedIdentity)
	}
	if manager.status.CurrentWallet != childID {
		t.Fatal("guardian identity lookup changed current wallet selection")
	}
}
