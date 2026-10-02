package wallet

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
)

func TestRGB11TranscendRegistrationDescriptorRoundTrip(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	address, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{7}, 20), &chaincfg.TestNet3Params)
	if err != nil {
		t.Fatal(err)
	}
	contractID := fmt.Sprintf("%064x", 77)
	desc := &rgb11wallet.TranscendRegistrationDescriptor{
		ContractID:      contractID,
		BaseTicker:      "USDT",
		GenesisOutpoint: fmt.Sprintf("%064x:0", 88),
		GenesisAddress:  address.EncodeAddress(),
	}
	contract := NewTranscendContract()
	contract.GetContractBase().AssetName = indexer.AssetName{Protocol: rgb11wallet.Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: contractID}
	contract.RGB11Registration = desc
	if err := contract.CheckContent(); err != nil {
		t.Fatal(err)
	}
	encoded, err := contract.Encode()
	if err != nil {
		t.Fatal(err)
	}

	decoded := NewTranscendContract()
	if err := decoded.Decode(encoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.CheckContent(); err != nil {
		t.Fatal(err)
	}
	if decoded.RGB11Registration == nil || *decoded.RGB11Registration != *desc {
		t.Fatalf("descriptor mismatch: %+v", decoded.RGB11Registration)
	}
	if decoded.GetAssetName().String() != contract.GetAssetName().String() {
		t.Fatalf("asset changed: %s != %s", decoded.GetAssetName(), contract.GetAssetName())
	}

	content := contract.Content()
	parsed, err := ContractContentUnMarsh(TEMPLATE_CONTRACT_TRANSCEND, content)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := parsed.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatal("JSON contract round-trip changed signed contract content")
	}
}

func TestRGB11TranscendDeployPayloadFitsSatoshiNetLimit(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	address, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{9}, 20), &chaincfg.TestNet3Params)
	if err != nil {
		t.Fatal(err)
	}
	contractID := fmt.Sprintf("%064x", 99)
	contract := NewTranscendContract()
	contract.GetContractBase().AssetName = indexer.AssetName{
		Protocol: rgb11wallet.Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: contractID,
	}
	contract.RGB11Registration = &rgb11wallet.TranscendRegistrationDescriptor{
		ContractID: contractID, BaseTicker: "USDT",
		GenesisOutpoint: fmt.Sprintf("%064x:0", 100),
		GenesisAddress:  address.EncodeAddress(),
	}
	content, err := contract.Encode()
	if err != nil {
		t.Fatal(err)
	}
	path := contract.GetContractName()
	// DER signatures are normally 70-72 bytes. Use the upper bound so this
	// test protects the OP_RETURN budget rather than a lucky short signature.
	sig := bytes.Repeat([]byte{1}, 72)
	signed, err := stxscript.NewScriptBuilder().
		AddData([]byte(path)).
		AddData(content).
		AddInt64(123456789).
		AddData(sig).
		AddData(sig).
		Script()
	if err != nil {
		t.Fatal(err)
	}
	if len(signed) > sindexer.MAX_PAYLOAD_LEN {
		t.Fatalf("RGB11 transcend deploy payload=%d exceeds SatoshiNet limit=%d", len(signed), sindexer.MAX_PAYLOAD_LEN)
	}
	if _, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DEPLOYCONTRACT, signed); err != nil {
		t.Fatalf("RGB11 transcend deploy cannot be wrapped as SatoshiNet OP_RETURN: %v", err)
	}
}
