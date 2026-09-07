package wallet

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type topicSDKMessageClient struct {
	*rgb11MessageNodeClient
	mu        sync.Mutex
	topics    map[string]swire.TopicServiceSnapshot
	published []*swire.TopicPublishMessage
}

func newTopicSDKMessageClient(remote *rgb11MemoryDKVSHTTP) *topicSDKMessageClient {
	return &topicSDKMessageClient{
		rgb11MessageNodeClient: newRGB11MessageNodeClient(remote),
		topics:                 make(map[string]swire.TopicServiceSnapshot),
	}
}

func (c *topicSDKMessageClient) SendMessageServiceReq(req *swire.MessageServiceRequest) (*swire.MessageServiceResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil message request")
	}
	switch req.Action {
	case swire.MessageServiceActionBindAccount, swire.MessageServiceActionNextMessage, swire.MessageServiceActionSendDirect:
		return c.rgb11MessageNodeClient.SendMessageServiceReq(req)
	case swire.MessageServiceActionCreateTopic:
		var create swire.TopicCreateRequest
		if swire.DecodeTopicJSON(req.Payload, &create) != nil || create.Meta.ServiceCoreNode != c.CoreNodeID() ||
			!c.accountBound(create.Meta.OwnerAccount) {
			return nil, fmt.Errorf("invalid topic create")
		}
		c.mu.Lock()
		existing, ok := c.topics[create.Meta.TopicName]
		if ok && (existing.Meta.OwnerAccount != create.Meta.OwnerAccount || existing.Meta.ServiceCoreNode != create.Meta.ServiceCoreNode) {
			c.mu.Unlock()
			return nil, fmt.Errorf("topic conflict")
		}
		if !ok {
			c.topics[create.Meta.TopicName] = swire.TopicServiceSnapshot{
				Meta:    create.Meta,
				State:   swire.TopicState{Status: "ACTIVE", KeySeq: 1, MemberCount: 1},
				Members: []swire.TopicMember{{AccountID: create.Meta.OwnerAccount, Status: "ACTIVE", Role: "OWNER", JoinedSeq: 1}},
			}
		}
		c.mu.Unlock()
		return &swire.MessageServiceResponse{}, nil
	case swire.MessageServiceActionTopicState:
		if err := verifyMessageServiceTestQuery(req); err != nil {
			return nil, err
		}
		c.mu.Lock()
		snapshot, ok := c.topics[req.TopicName]
		c.mu.Unlock()
		if !ok || !c.accountBound(req.AccountID) {
			return nil, fmt.Errorf("topic state unavailable")
		}
		payload, err := swire.EncodeTopicJSON(&snapshot)
		if err != nil {
			return nil, err
		}
		return &swire.MessageServiceResponse{Payload: payload}, nil
	case swire.MessageServiceActionTopicPublish:
		publish, err := swire.DeserializeTopicPublishMessage(req.Payload)
		if err != nil || req.TargetCoreNode != c.CoreNodeID() || !c.accountBound(publish.SenderAccount) {
			return nil, fmt.Errorf("invalid topic publish")
		}
		c.mu.Lock()
		snapshot, ok := c.topics[publish.TopicName]
		c.mu.Unlock()
		if !ok || snapshot.State.KeySeq != publish.KeySeq {
			return nil, fmt.Errorf("topic key mismatch")
		}
		c.state.mu.Lock()
		expected := c.state.next[publish.SenderAccount]
		if publish.SenderMsgID != expected {
			c.state.mu.Unlock()
			return nil, fmt.Errorf("unexpected sender message id")
		}
		c.state.next[publish.SenderAccount]++
		c.state.mu.Unlock()
		copyPublish := *publish
		copyPublish.Ciphertext = append([]byte(nil), publish.Ciphertext...)
		copyPublish.SenderSignature = append([]byte(nil), publish.SenderSignature...)
		c.mu.Lock()
		c.published = append(c.published, &copyPublish)
		c.mu.Unlock()
		return &swire.MessageServiceResponse{}, nil
	default:
		return nil, fmt.Errorf("unsupported topic test action %s", req.Action)
	}
}

func TestMessageTopicSDKCreateAndPublishUsesSharedSenderSequence(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create topic SDK wallet")
	}
	database := indexerdb.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("create topic SDK database")
	}
	t.Cleanup(func() { _ = database.Close() })
	remote := newRGB11MemoryDKVSHTTP()
	client := newTopicSDKMessageClient(remote)
	manager := &Manager{
		db: database, wallet: wallet,
		walletInfoMap: map[int64]*WalletInfo{wallet.GetId(): {
			WalletInDB: WalletInDB{Id: wallet.GetId(), Accounts: 1, Type: WALLET_TYPE_MNEMONIC}, Wallet: wallet,
		}},
		cfg: &sdkcommon.Config{Env: "test", Chain: "testnet", IndexerL2: &sdkcommon.Indexer{
			Scheme: "http", Host: "dkvs.test", Proxy: "testnet",
		}},
		http: remote,
	}
	manager.serverNode = NewNode(client, "topic.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := manager.CreateMessageTopic("developers", "Developers", 16)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State.KeySeq != 1 || snapshot.State.MemberCount != 1 || snapshot.Meta.OwnerAccount != accountID ||
		snapshot.Meta.ServiceCoreNode != client.CoreNodeID() {
		t.Fatalf("created topic=%+v", snapshot)
	}
	cryptoManager, err := NewTopicCryptoManager(manager, wallet)
	if err != nil {
		t.Fatal(err)
	}
	initialKey, err := cryptoManager.LoadTopicKey("developers", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Create is idempotent and retains the exact local epoch-1 key.
	if _, err := manager.CreateMessageTopic("developers", "Developers", 16); err != nil {
		t.Fatal(err)
	}
	initialKeyAgain, err := cryptoManager.LoadTopicKey("developers", 1)
	if err != nil || !bytes.Equal(initialKey, initialKeyAgain) {
		t.Fatalf("create retry changed initial key err=%v", err)
	}

	publish, err := manager.PublishMessageTopic("app-1", "developers", client.CoreNodeID(), []byte("hello topic"))
	if err != nil {
		t.Fatal(err)
	}
	if publish.SenderMsgID != 0 || publish.SenderAccount != accountID || publish.KeySeq != 1 {
		t.Fatalf("publish=%+v", publish)
	}
	plaintext, err := cryptoManager.DecryptPublish(publish)
	if err != nil || string(plaintext) != "hello topic" {
		t.Fatalf("decrypt=%q err=%v", plaintext, err)
	}
	client.state.mu.Lock()
	next := client.state.next[accountID]
	client.state.mu.Unlock()
	if next != 1 {
		t.Fatalf("topic publish next sender id=%d want=1", next)
	}
	if !client.accountBound(accountID) {
		t.Fatal("topic SDK did not bind account to current CoreNode")
	}
	bindingKey, _ := dkvsindexer.AccountMappingKey(GetChainParam().Name, manager.wallet.GetAddress())
	remote.mu.Lock()
	binding := remote.records[bindingKey]
	remote.mu.Unlock()
	if binding == nil {
		t.Fatal("canonical account CoreNode binding missing")
	}
}
