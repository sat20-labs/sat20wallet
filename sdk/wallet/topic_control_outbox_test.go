package wallet

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type topicControlOutboxTestClient struct {
	NodeRPCClient
	failFirst bool
	calls     int
	pub       *secp256k1.PublicKey
}

func (c *topicControlOutboxTestClient) SendMessageServiceReq(req *swire.MessageServiceRequest) (*swire.MessageServiceResponse, error) {
	if req == nil || req.Action != swire.MessageServiceActionTopicCommit {
		return nil, fmt.Errorf("unexpected message service request")
	}
	c.calls++
	if c.failFirst && c.calls == 1 {
		return nil, errors.New("simulated response loss")
	}
	return &swire.MessageServiceResponse{}, nil
}

func TestTopicControlOutboxRetainsKeyUntilConfirmedCommit(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create wallet")
	}
	database := indexerdb.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("create database")
	}
	t.Cleanup(func() { _ = database.Close() })
	seed := make([]byte, 32)
	seed[31] = 9
	corePub := secp256k1.PrivKeyFromBytes(seed).PubKey()
	client := &topicControlOutboxTestClient{failFirst: true, pub: corePub}
	manager := &Manager{db: database, wallet: wallet}
	manager.serverNode = NewNode(client, "topic.test", SERVER_NODE, corePub, corePub)
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		t.Fatal(err)
	}
	coreID := manager.serverNode.NodeId.SerializeCompressed()
	serviceCore := fmt.Sprintf("%x", coreID)
	newKey := bytes.Repeat([]byte{0x42}, topicKeySize)
	wrapped, err := wallet.EncryptToAccount(accountID, newKey)
	if err != nil {
		t.Fatal(err)
	}
	outbox := &topicControlOutboxRecord{
		Version: topicControlOutboxVersion, TopicName: "developers",
		ChangeType: swire.TopicMembershipRotate,
		Commit: swire.TopicMembershipCommit{
			TopicName: "developers", ServiceCoreNode: serviceCore, IssuerAccount: accountID,
			BaseKeySeq: 1, NewKeySeq: 2, ChangeType: swire.TopicMembershipRotate,
		},
		WrappedTopicKey: wrapped,
	}
	if err := manager.saveTopicControlOutbox(accountID, outbox); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.sendPersistedTopicControl(wallet, outbox); err == nil {
		t.Fatal("first simulated response loss unexpectedly succeeded")
	}
	if pending, err := manager.loadTopicControlOutbox(accountID, "developers"); err != nil || pending == nil {
		t.Fatalf("pending outbox=%#v err=%v", pending, err)
	}
	cryptoManager, err := NewTopicCryptoManager(manager, wallet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cryptoManager.LoadTopicKey("developers", 2); err == nil {
		t.Fatal("unconfirmed rotation promoted topic key")
	}

	if _, err := manager.sendPersistedTopicControl(wallet, outbox); err != nil {
		t.Fatal(err)
	}
	stored, err := cryptoManager.LoadTopicKey("developers", 2)
	if err != nil || !bytes.Equal(stored, newKey) {
		t.Fatalf("stored key=%x err=%v", stored, err)
	}
	if pending, err := manager.loadTopicControlOutbox(accountID, "developers"); err != nil || pending != nil {
		t.Fatalf("outbox retained after confirmation: %#v err=%v", pending, err)
	}
	if client.calls != 2 {
		t.Fatalf("service calls=%d want=2", client.calls)
	}
}
