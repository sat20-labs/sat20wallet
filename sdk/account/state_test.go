package account

import (
	"bytes"
	"strings"
	"testing"
)

func managedStateFixture() ManagedState {
	root := strings.Repeat("1", 64)
	return ManagedState{
		Version: ManagedStateVersion, RootFingerprint: root, Revision: 4,
		Wallets: []ManagedWallet{
			{
				Fingerprint: strings.Repeat("2", 64), Revision: 3, Name: "Second",
				Mnemonic:     "legal winner thank year wave sausage worth useful legal winner thank yellow",
				AccountCount: 1, SubAccounts: []SubAccount{{Index: 0, Name: "Account 1"}},
			},
			{
				Fingerprint: root, Revision: 4, Name: "Root",
				Mnemonic:     "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
				AccountCount: 2, SubAccounts: []SubAccount{
					{Index: 1, Name: "Account 2"},
					{Index: 0, Name: "Account 1"},
				},
			},
		},
	}
}

func TestManagedStateCodecIsDeterministicAndRootFirst(t *testing.T) {
	first, err := encodeManagedState(managedStateFixture())
	if err != nil {
		t.Fatal(err)
	}
	second, err := encodeManagedState(managedStateFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("managed state encoding is not deterministic")
	}
	decoded, err := decodeManagedState(first)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Wallets[0].Fingerprint != decoded.RootFingerprint {
		t.Fatal("root wallet is not first")
	}
	if decoded.Wallets[0].SubAccounts[0].Index != 0 ||
		decoded.Wallets[0].SubAccounts[1].Index != 1 {
		t.Fatal("subaccounts are not ordered by index")
	}
	backup, err := BackupFromManagedState(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Wallets[0].Name != "Root" {
		t.Fatalf("recovery would not import root first: %q", backup.Wallets[0].Name)
	}
}

func TestManagedStateEnvelopeRejectsTampering(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, accountSecretSize)
	accountID := strings.Repeat("a", 64)
	envelope, err := SealManagedState(secret, accountID, managedStateFixture(), bytes.NewReader(bytes.Repeat([]byte{3}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenManagedState(secret, accountID, envelope); err != nil {
		t.Fatal(err)
	}
	envelope[len(envelope)-1] ^= 1
	if _, err := OpenManagedState(secret, accountID, envelope); err == nil {
		t.Fatal("tampered managed state was accepted")
	}
}

func TestManagedStateRejectsDeletedRoot(t *testing.T) {
	state := managedStateFixture()
	state.Wallets[1].Deleted = true
	if _, err := encodeManagedState(state); err == nil {
		t.Fatal("managed state accepted a deleted root wallet")
	}
}
