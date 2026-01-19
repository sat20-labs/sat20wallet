package wallet

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
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

func TestRevocationKeyDerivation_SubWallet(t *testing.T) {
	wallet1, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}
	wallet1.SetSubAccount(2)

	wallet2, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}
	wallet1.SetSubAccount(3)

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

// 支付的钱包跟通道的私钥完全分离，不需要同一套种子生成
func TestRevocationKeyDerivationV2(t *testing.T) {
	paymentWallet1, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}
	channelRevKey1, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}

	paymentWallet2, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}
	channelRevKey2, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatalf("NewWallet failed %v", err)
	}

	// 一方检查
	{
		// First, we'll generate a commitment point, and a commitment secret.
		// These will be used to derive the ultimate revocation keys.
		commitSecret, commitPoint := channelRevKey1.GetCommitRootKey(paymentWallet2.GetNodePubKey().SerializeCompressed())

		// With the commitment secrets generated, we'll now create the base
		// keys we'll use to derive the revocation key from.
		// basePriv, basePub := wallet1.GetRevocationBaseKey()
		basePub := channelRevKey1.GetRevocationBaseKey()

		// With the point and key obtained, we can now derive the revocation
		// key itself.
		revocationPub := utils.DeriveRevocationPubkey(basePub, commitPoint)

		// The revocation public key derived from the original public key, and
		// the one derived from the private key should be identical.
		// revocationPriv := utils.DeriveRevocationPrivKey(basePriv, commitSecret)
		revocationPriv := channelRevKey1.DeriveRevocationPrivKey(commitSecret)
		if !revocationPub.IsEqual(revocationPriv.PubKey()) {
			t.Fatalf("derived public keys don't match!")
		}
	}

	// 另一方检查
	{
		// First, we'll generate a commitment point, and a commitment secret.
		// These will be used to derive the ultimate revocation keys.
		commitSecret, commitPoint := channelRevKey2.GetCommitRootKey(paymentWallet1.GetNodePubKey().SerializeCompressed())

		// With the commitment secrets generated, we'll now create the base
		// keys we'll use to derive the revocation key from.
		// basePriv, basePub := wallet1.GetRevocationBaseKey()
		basePub := channelRevKey2.GetRevocationBaseKey()

		// With the point and key obtained, we can now derive the revocation
		// key itself.
		revocationPub := utils.DeriveRevocationPubkey(basePub, commitPoint)

		// The revocation public key derived from the original public key, and
		// the one derived from the private key should be identical.
		// revocationPriv := utils.DeriveRevocationPrivKey(basePriv, commitSecret)
		revocationPriv := channelRevKey2.DeriveRevocationPrivKey(commitSecret)
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

	mnemonic := "force plate fold brown kiss sample weapon useful earth useful shop priority"

	wallet := NewInternalWalletWithMnemonic(mnemonic, "", GetChainParam())
	//wallet := NewWalletWithMnemonic("", "", GetChainParam())

	if wallet == nil {
		t.Fail()
	}

	fmt.Printf("%s\n", wallet.GetAddress())

	sig, err := wallet.SignMessage([]byte(mnemonic))
	if err != nil {
		t.Fatal(err)
	}

	privateKey := wallet.getPaymentPrivKey().Serialize()
	fmt.Printf("private key: %s", hex.EncodeToString(privateKey))

	wallet2, _, err := NewInternalWalletWithPrivKey(privateKey, GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%s\n", wallet2.GetAddress())

	sig2, err := wallet2.SignMessage([]byte(mnemonic))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(sig, sig2) {
		t.Fatal()
	}

}

func TestNewWallet(t *testing.T) {

	//wallet := NewInternalWalletWithMnemonic("force plate fold brown kiss sample weapon useful earth useful shop priority", "", GetChainParam())
	wallet, mn, err := NewInteralWallet(&chaincfg.MainNetParams)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%s\n", mn)

	if wallet == nil {
		t.Fail()
	}

	fmt.Printf("%s\n", wallet.GetAddress())
	fmt.Printf("%s\n", hex.EncodeToString(wallet.GetPubKey().SerializeCompressed()))
}

// helper to generate a random secp256k1 private key and its serialized bytes
func genPrivKeyBytes(t *testing.T) ([]byte, *secp256k1.PrivateKey) {
	t.Helper()
	privBytes := make([]byte, 32)
	_, err := rand.Read(privBytes)
	if err != nil {
		t.Fatalf("rand.Read failed: %v", err)
	}
	// PrivKeyFromBytes will normalize the bytes into a valid key
	priv := secp256k1.PrivKeyFromBytes(privBytes)
	if priv == nil {
		t.Fatalf("failed to construct privkey from bytes")
	}
	// use the serialized 32-byte form (big endian)
	return priv.Serialize(), priv
}

func TestDeriveSharedSecretSymmetric(t *testing.T) {
	_, a := genPrivKeyBytes(t)
	_, b := genPrivKeyBytes(t)

	pubA := a.PubKey().SerializeCompressed()
	pubB := b.PubKey().SerializeCompressed()

	sec1, err := deriveSharedSecret(a, pubB)
	if err != nil {
		t.Fatalf("deriveSharedSecret(a, pubB) error: %v", err)
	}
	sec2, err := deriveSharedSecret(b, pubA)
	if err != nil {
		t.Fatalf("deriveSharedSecret(b, pubA) error: %v", err)
	}
	if !bytes.Equal(sec1, sec2) {
		t.Fatalf("shared secrets differ:\nA->B: %x\nB->A: %x", sec1, sec2)
	}
}

func TestEncryptToDecryptRoundtrip(t *testing.T) {
	// generate keys
	privABytes, a := genPrivKeyBytes(t)
	privBBytes, b := genPrivKeyBytes(t)

	// create wallet instances using existing constructor
	wA, _, err := NewInternalWalletWithPrivKey(privABytes, &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("NewPrivateKeyWallet A error: %v", err)
	}
	wB, _, err := NewInternalWalletWithPrivKey(privBBytes, &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("NewPrivateKeyWallet B error: %v", err)
	}

	pubA := a.PubKey().SerializeCompressed()
	pubB := b.PubKey().SerializeCompressed()

	plaintext := []byte("hello from A to B")

	ct, err := wA.EncryptTo(pubB, plaintext)
	if err != nil {
		t.Fatalf("EncryptTo error: %v", err)
	}
	if len(ct) == 0 {
		t.Fatal("ciphertext empty")
	}

	pt, err := wB.Decrypt(ct, pubA)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Fatalf("decrypted plaintext mismatch: got %q want %q", pt, plaintext)
	}
}

func TestDecryptErrors(t *testing.T) {
	w, _, err := NewInteralWallet(&chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("NewPrivateKeyWallet error: %v", err)
	}

	// empty data
	_, err = w.Decrypt(nil, []byte{1, 2, 3})
	if err == nil {
		t.Fatalf("expected error for empty data")
	}

	// malformed ciphertext (too short nonce)
	_, err = w.Decrypt([]byte{0, 1, 2}, []byte{1, 2, 3})
	if err == nil {
		t.Fatalf("expected error for too short ciphertext")
	}

	// attempt decrypt with wrong pubkey (should fail with auth error)
	// prepare a valid ciphertext using a matching pair, then try to decrypt with wrong pubkey
	_, otherPriv := genPrivKeyBytes(t)
	pubOther := otherPriv.PubKey().SerializeCompressed()

	// build matching pair
	_, a := genPrivKeyBytes(t)
	w1, _, err := NewInternalWalletWithPrivKey(a.Serialize(), &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("NewPrivateKeyWallet w1 error: %v", err)
	}
	_, bPriv := genPrivKeyBytes(t)
	pubB := bPriv.PubKey().SerializeCompressed()

	msg := []byte("msg")
	ct, err := w1.EncryptTo(pubB, msg)
	if err != nil {
		t.Fatalf("EncryptTo error: %v", err)
	}

	// decrypt with a wallet that does not have the corresponding private key and wrong pub (should fail)
	_, err = w.Decrypt(ct, pubOther)
	if err == nil {
		t.Fatalf("expected auth error when decrypting with wrong shared secret")
	}
}
