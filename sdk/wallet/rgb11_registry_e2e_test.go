package wallet

import (
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestRGB11RegistrySDKDKVSE2E(t *testing.T) {
	coreWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"",
		&chaincfg.TestNet4Params,
	)
	if coreWallet == nil || coreWallet.GetPubKey() == nil {
		t.Fatal("create CoreNode test wallet")
	}

	transport := newSatoshiNetDKVSTestTransport()
	transport.height = 100
	transport.indexer.SetSystemVerifier(dkvsindexer.StaticSystemVerifier{
		Keys: [][]byte{coreWallet.GetPubKey().SerializeCompressed()},
	})

	contractID := fmt.Sprintf("%064x", 401)
	key, err := dkvsindexer.RGB11RegistryKey("alice", "USD", 1)
	if err != nil {
		t.Fatal(err)
	}
	value, err := dkvsindexer.EncodeRGB11ContractID(contractID)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDKVSSignedRecord(
		coreWallet, key, value,
		dkvsindexer.RecordOptions{Seq: 1, IssueHeight: transport.height},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated, err := transport.indexer.PutInternalRGB11Registry(record); err != nil || !updated {
		t.Fatalf("server registration updated=%v err=%v", updated, err)
	}

	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", transport)
	registration, err := client.GetRGB11Registration("alice", "USD", contractID)
	if err != nil {
		t.Fatal(err)
	}
	if registration.ContractID != contractID ||
		registration.AssetName != "rgb11:f:usd@alice" ||
		registration.ProviderDID != "alice" ||
		registration.BaseTicker != "usd" ||
		registration.Ordinal != 1 {
		t.Fatalf("registration=%+v", registration)
	}

	secondID := fmt.Sprintf("%064x", 402)
	secondKey, err := dkvsindexer.RGB11RegistryKey("alice", "USD", 2)
	if err != nil {
		t.Fatal(err)
	}
	secondValue, err := dkvsindexer.EncodeRGB11ContractID(secondID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewDKVSSignedRecord(
		coreWallet, secondKey, secondValue,
		dkvsindexer.RecordOptions{Seq: 1, IssueHeight: transport.height},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated, err := transport.indexer.PutInternalRGB11Registry(second); err != nil || !updated {
		t.Fatalf("second registration updated=%v err=%v", updated, err)
	}
	registration, err = client.GetRGB11Registration("alice", "USD", secondID)
	if err != nil {
		t.Fatal(err)
	}
	if registration.AssetName != "rgb11:f:usd_2@alice" || registration.Ordinal != 2 {
		t.Fatalf("second registration=%+v", registration)
	}
}
