package wallet

import (
	"fmt"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func TestRevocationKeyDerivation(t *testing.T) {
	wallet1, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}

	wallet2, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}

	// First, we'll generate a commitment point, and a commitment secret.
	// These will be used to derive the ultimate revocation keys.
	commitSecret, commitPoint := wallet1.GetCommitRootKey(wallet2.GetNodePubKey().SerializeCompressed())

	// With the commitment secrets generated, we'll now create the base
	// keys we'll use to derive the revocation key from.
	// basePriv, basePub := wallet1.GetRevocationBaseKey()
	basePub := wallet1.GetRevocationBaseKey()

	// With the point and key obtained, we can now derive the revocation
	// key itself.
	revocationPub := utils.DeriveRevocationPubkey(basePub, commitPoint)

	// The revocation public key derived from the original public key, and
	// the one derived from the private key should be identical.
	// revocationPriv := utils.DeriveRevocationPrivKey(basePriv, commitSecret)
	revocationPriv := wallet1.DeriveRevocationPrivKey(commitSecret)
	if !revocationPub.IsEqual(revocationPriv.PubKey()) {
		t.Fatalf("derived public keys don't match!")
	}
}


func TestRevocationKeyDerivationV2(t *testing.T) {
	wallet11, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}
	wallet12, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}

	wallet21, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}
	wallet22, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}

	{
		// First, we'll generate a commitment point, and a commitment secret.
		// These will be used to derive the ultimate revocation keys.
		commitSecret, commitPoint := wallet12.GetCommitRootKey(wallet21.GetNodePubKey().SerializeCompressed())

		// With the commitment secrets generated, we'll now create the base
		// keys we'll use to derive the revocation key from.
		// basePriv, basePub := wallet1.GetRevocationBaseKey()
		basePub := wallet12.GetRevocationBaseKey()

		// With the point and key obtained, we can now derive the revocation
		// key itself.
		revocationPub := utils.DeriveRevocationPubkey(basePub, commitPoint)

		// The revocation public key derived from the original public key, and
		// the one derived from the private key should be identical.
		// revocationPriv := utils.DeriveRevocationPrivKey(basePriv, commitSecret)
		revocationPriv := wallet12.DeriveRevocationPrivKey(commitSecret)
		if !revocationPub.IsEqual(revocationPriv.PubKey()) {
			t.Fatalf("derived public keys don't match!")
		}
	}

	{
		// First, we'll generate a commitment point, and a commitment secret.
		// These will be used to derive the ultimate revocation keys.
		commitSecret, commitPoint := wallet22.GetCommitRootKey(wallet11.GetNodePubKey().SerializeCompressed())

		// With the commitment secrets generated, we'll now create the base
		// keys we'll use to derive the revocation key from.
		// basePriv, basePub := wallet1.GetRevocationBaseKey()
		basePub := wallet22.GetRevocationBaseKey()

		// With the point and key obtained, we can now derive the revocation
		// key itself.
		revocationPub := utils.DeriveRevocationPubkey(basePub, commitPoint)

		// The revocation public key derived from the original public key, and
		// the one derived from the private key should be identical.
		// revocationPriv := utils.DeriveRevocationPrivKey(basePriv, commitSecret)
		revocationPriv := wallet22.DeriveRevocationPrivKey(commitSecret)
		if !revocationPub.IsEqual(revocationPriv.PubKey()) {
			t.Fatalf("derived public keys don't match!")
		}
	}
}

func TestTweakKeyDerivation(t *testing.T) {
	t.Parallel()

	wallet1, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}

	wallet2, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}

	// First, we'll generate a commitment point, and a commitment secret.
	// These will be used to derive the ultimate revocation keys.
	_, commitPoint := wallet1.GetCommitRootKey(wallet2.GetNodePubKey().SerializeCompressed())

	// With the commitment secrets generated, we'll now create the base
	// keys we'll use to derive the revocation key from.
	basePriv, basePub := wallet1.getRevocationBaseKey(0)

	// With the base key create, we'll now create a commitment point, and
	// from that derive the bytes we'll used to tweak the base public key.
	commitTweak := utils.SingleTweakBytes(commitPoint, basePub)

	// Next, we'll modify the public key. When we apply the same operation
	// to the private key we should get a key that matches.
	tweakedPub := utils.TweakPubKey(basePub, commitPoint)

	// Finally, attempt to re-generate the private key that matches the
	// tweaked public key. The derived key should match exactly.
	derivedPriv := utils.TweakPrivKey(basePriv, commitTweak)
	if !derivedPriv.PubKey().IsEqual(tweakedPub) {
		t.Fatalf("pub keys don't match")
	}
}

func TestImportWallet(t *testing.T) {

	wallet := NewInternalWalletWithMnemonic("force plate fold brown kiss sample weapon useful earth useful shop priority", "", GetChainParam())
	//wallet := NewWalletWithMnemonic("", "", GetChainParam())

	if wallet == nil {
		t.Fail()
	}

	fmt.Printf("%s\n", wallet.GetAddress())
}
