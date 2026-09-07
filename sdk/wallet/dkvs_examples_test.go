package wallet

import (
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func ExampleSatsNetDKVSClient_offlineMessage() {
	recipientPriv := exampleDKVSPriv(3)
	senderPriv := exampleDKVSPriv(4)
	recipientPubKey := recipientPriv.PubKey().SerializeCompressed()
	const msgID int64 = 1786600000123
	remote := newRGB11MemoryDKVSHTTP()
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", remote)

	_, err := client.SendOfflineMessage(exampleDKVSWallet(senderPriv), recipientPubKey, msgID,
		[]byte("message-ciphertext"), nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 100, IssueHeight: 1})

	// AccountBound mailbox writes now require Manager.SendOfflineMessage so the
	// CoreNode can enforce SenderMsgID, billing, ACK and durable retry. Keep the
	// low-level client example explicit about that boundary.
	fmt.Println(errors.Is(err, ErrDKVSMessageManagerRequired))
	// Output: true
}

func ExampleSatsNetDKVSClient_serviceAuthenticity() {
	priv := exampleDKVSPriv(5)
	remote := newRGB11MemoryDKVSHTTP()
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", remote)

	_, _ = client.PublishServiceAuthenticity(exampleDKVSWallet(priv), "wallet", "pwa", "1.0.0",
		"sha256:abcd", "https://example.invalid/wallet", nil,
		dkvsindexer.RecordOptions{Seq: 1, TTL: 100, IssueHeight: 1})
	authenticity, _, _ := client.GetServiceAuthenticity("wallet", "pwa", "1.0.0")

	fmt.Println(authenticity.ServiceName, authenticity.AppID, authenticity.Release, authenticity.ArtifactHash)
	// Output: wallet pwa 1.0.0 sha256:abcd
}

func ExampleSatsNetDKVSClient_ResolveNameRecord() {
	priv := exampleDKVSPriv(6)
	key, _ := dkvsindexer.NameKey("alice-name")
	record, _ := NewDKVSSignedRecord(exampleDKVSWallet(priv), key, []byte("profile"),
		dkvsindexer.RecordOptions{Seq: 1, TTL: 100, IssueHeight: 1})
	remote := newRGB11MemoryDKVSHTTP()
	remote.mu.Lock()
	remote.records[key] = cloneRGB11DKVSRecord(record)
	remote.mu.Unlock()
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", remote)

	resolution, _ := client.ResolveNameRecord("alice-name")

	fmt.Println(resolution.NameID == "alice-name", resolution.Record.Key == key)
	// Output: true true
}

func exampleDKVSPriv(seed byte) *btcec.PrivateKey {
	key := make([]byte, 32)
	key[31] = seed
	priv, _ := btcec.PrivKeyFromBytes(key)
	return priv
}

func exampleDKVSWallet(priv *btcec.PrivateKey) *InternalWallet {
	w, _, err := NewInternalWalletWithPrivKey(priv.Serialize(), GetChainParam())
	if err != nil {
		panic(err)
	}
	return w
}
