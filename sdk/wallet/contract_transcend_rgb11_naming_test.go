package wallet

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11TranscendRegistrationDescriptorRoundTrip(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	address, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{7}, 20), &chaincfg.TestNet3Params)
	if err != nil { t.Fatal(err) }
	contractID := fmt.Sprintf("%064x", 77)
	desc := &rgb11wallet.TranscendRegistrationDescriptor{
		ContractID: contractID,
		BaseTicker: "USDT",
		GenesisOutpoint: fmt.Sprintf("%064x:0", 88),
		GenesisAddress: address.EncodeAddress(),
	}
	contract := NewTranscendContract()
	contract.GetContractBase().AssetName = indexer.AssetName{Protocol: rgb11wallet.Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: contractID}
	contract.RGB11Registration = desc
	if err := contract.CheckContent(); err != nil { t.Fatal(err) }
	encoded, err := contract.Encode()
	if err != nil { t.Fatal(err) }

	decoded := NewTranscendContract()
	if err := decoded.Decode(encoded); err != nil { t.Fatal(err) }
	if err := decoded.CheckContent(); err != nil { t.Fatal(err) }
	if decoded.RGB11Registration == nil || *decoded.RGB11Registration != *desc {
		t.Fatalf("descriptor mismatch: %+v", decoded.RGB11Registration)
	}
	if decoded.GetAssetName().String() != contract.GetAssetName().String() {
		t.Fatalf("asset changed: %s != %s", decoded.GetAssetName(), contract.GetAssetName())
	}

	content := contract.Content()
	parsed, err := ContractContentUnMarsh(TEMPLATE_CONTRACT_TRANSCEND, content)
	if err != nil { t.Fatal(err) }
	reencoded, err := parsed.Encode()
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(encoded, reencoded) {
		t.Fatal("JSON contract round-trip changed signed contract content")
	}
}
