package wallet

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func newTopicCryptoTestManager(t *testing.T, mnemonic string) (*Manager, *InternalWallet, *TopicCryptoManager) {
	t.Helper()
	wallet := NewInternalWalletWithMnemonic(mnemonic, "", &chaincfg.TestNet4Params)
	if wallet == nil {
		t.Fatal("create topic test wallet")
	}
	database := indexerdb.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("create topic test database")
	}
	t.Cleanup(func() { _ = database.Close() })
	manager := &Manager{db: database, wallet: wallet}
	cryptoManager, err := NewTopicCryptoManager(manager, wallet)
	if err != nil {
		t.Fatal(err)
	}
	return manager, wallet, cryptoManager
}

func TestTopicCryptoKeyPackagesMessagesAndHistory(t *testing.T) {
	_, ownerWallet, ownerCrypto := newTopicCryptoTestManager(t,
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire")
	_, memberWallet, memberCrypto := newTopicCryptoTestManager(t,
		"comfort very add tuition senior run eight snap burst appear exile dutch")
	ownerID, err := dkvsAccountID(ownerWallet)
	if err != nil {
		t.Fatal(err)
	}
	memberID, err := dkvsAccountID(memberWallet)
	if err != nil {
		t.Fatal(err)
	}

	key1 := bytes.Repeat([]byte{0x11}, topicKeySize)
	if err := ownerCrypto.StoreTopicKey("developers", 1, key1); err != nil {
		t.Fatal(err)
	}
	packages, err := ownerCrypto.CreateKeyPackages("developers", 1, key1, []string{ownerID, memberID})
	if err != nil {
		t.Fatal(err)
	}
	var memberPackage swire.TopicKeyPackageRecipient
	for _, item := range packages {
		if item.Recipient == memberID {
			memberPackage = item
		}
	}
	if memberPackage.Recipient == "" {
		t.Fatal("member key package missing")
	}
	if err := memberCrypto.AcceptKeyPackage(&swire.TopicKeyFanoutMessage{
		TopicName: "developers", KeySeq: 1, IssuerAccount: ownerID,
		Recipients: []swire.TopicKeyPackageRecipient{memberPackage},
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := memberCrypto.LoadTopicKey("developers", 1); err != nil || !bytes.Equal(got, key1) {
		t.Fatalf("member key1=%x err=%v", got, err)
	}

	publish1, err := ownerCrypto.EncryptPublish("developers", 1, 7, "00000000000000010000000000000001", []byte("first topic message"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := memberCrypto.DecryptPublish(publish1)
	if err != nil || string(plaintext) != "first topic message" {
		t.Fatalf("decrypt=%q err=%v", plaintext, err)
	}

	key2 := bytes.Repeat([]byte{0x22}, topicKeySize)
	packages2, err := ownerCrypto.CreateKeyPackages("developers", 2, key2, []string{ownerID, memberID})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range packages2 {
		if item.Recipient != memberID {
			continue
		}
		if err := memberCrypto.AcceptKeyPackage(&swire.TopicKeyFanoutMessage{
			TopicName: "developers", KeySeq: 2, IssuerAccount: ownerID,
			Recipients: []swire.TopicKeyPackageRecipient{item},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if current, err := memberCrypto.CurrentTopicKeySeq("developers"); err != nil || current != 2 {
		t.Fatalf("current seq=%d err=%v", current, err)
	}
	// Historical keys remain available while old mailbox messages may still
	// reference them.
	plaintext, err = memberCrypto.DecryptPublish(publish1)
	if err != nil || string(plaintext) != "first topic message" {
		t.Fatalf("historical decrypt=%q err=%v", plaintext, err)
	}

	tampered := *publish1
	tampered.Ciphertext = append([]byte(nil), publish1.Ciphertext...)
	tampered.Ciphertext[len(tampered.Ciphertext)-1] ^= 1
	if _, err := memberCrypto.DecryptPublish(&tampered); err == nil {
		t.Fatal("tampered topic ciphertext was accepted")
	}

	badPackage := memberPackage
	badPackage.EncryptedTopicKey = append([]byte(nil), badPackage.EncryptedTopicKey...)
	badPackage.EncryptedTopicKey[len(badPackage.EncryptedTopicKey)-1] ^= 1
	if err := memberCrypto.AcceptKeyPackage(&swire.TopicKeyFanoutMessage{
		TopicName: "developers", KeySeq: 1, IssuerAccount: ownerID,
		Recipients: []swire.TopicKeyPackageRecipient{badPackage},
	}); err == nil {
		t.Fatal("tampered topic key package was accepted")
	}
}
