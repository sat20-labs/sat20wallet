package wallet

import (
	"context"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func coreRGB11NamingE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	manager *Manager, material *coreRecoveryMaterial) {

	issued, err := manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EN", Name: "E2E naming asset", Amounts: []uint64{321},
	})
	coreRequire(t, "issue RGB11 naming asset", err)
	coreCheckpoint(t, "01_rgb11_naming_issue", manager, cfg, chain, material)

	state, err := manager.GetRGB11State()
	coreRequire(t, "read RGB11 naming state", err)
	var before *RGB11TickerInfo
	for _, info := range state.TickerInfos {
		if info != nil && info.ContractID == issued.ContractID {
			before = info
			break
		}
	}
	coreAssert(t, before != nil, "issued RGB11 ticker missing from state")
	coreAssert(t, before.AssetKey == issued.AssetName.String(), "human-readable name leaked into immutable asset key")
	coreAssert(t, before.NamingStatus == "local-address", "issued RGB11 asset did not resolve genesis address")
	coreAssert(t, strings.HasPrefix(before.Ticker, "e2en@"), "default local RGB11 name is not ticker@address-suffix")

	balanceBefore, err := manager.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "RGB11 naming balance before rename", err)
	coreRequire(t, "rename unregistered RGB11 asset", manager.SetRGB11LocalAssetName(issued.ContractID, "my-local-rgb-name"))

	state, err = manager.GetRGB11State()
	coreRequire(t, "read renamed RGB11 state", err)
	var renamed *RGB11TickerInfo
	for _, info := range state.TickerInfos {
		if info != nil && info.ContractID == issued.ContractID {
			renamed = info
			break
		}
	}
	coreAssert(t, renamed != nil && renamed.Ticker == "my-local-rgb-name", "SDK local RGB11 rename not visible")
	coreAssert(t, renamed.AssetKey == issued.AssetName.String(), "SDK local rename changed immutable asset key")
	balanceAfter, err := manager.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "RGB11 naming balance after rename", err)
	coreAssert(t, balanceAfter.Cmp(balanceBefore) == 0, "SDK local rename changed RGB11 balance")

	// The same DID ownership fact is exposed to the Wallet L1 client and to the
	// real SatoshiNet nodes by the parent e2e harness.
	chain.setNameOwner("alice", manager.GetWallet().GetAddress())
	client, err := manager.ensureDKVSManager().primaryClient()
	coreRequire(t, "create DKVS client for primary DID", err)
	_, err = client.PutPrimaryDID(
		manager.GetWallet(), "alice", dkvsindexer.RecordOptions{},
		&DKVSAutopayOptions{
			AddressParams: GetChainParam_SatsNet(),
			PoolContract:  material.auth.Autopay.PoolContract,
		},
	)
	coreRequire(t, "write primary DID through real DKVS", err)

	primary, _, err := client.GetPrimaryDID(manager.GetWallet().GetPubKey().SerializeCompressed())
	coreRequire(t, "read primary DID through real DKVS", err)
	coreAssert(t, primary == "alice", "primary DID round-trip mismatch")

	registration, err := manager.RegisterRGB11AssetName(issued.ContractID)
	coreRequire(t, "register RGB11 canonical name in DKVS", err)
	coreAssert(t, registration.ContractID == issued.ContractID, "registered ContractID mismatch")
	coreAssert(t, registration.AssetName == "rgb11:f:e2en@alice", "unexpected first canonical RGB11 name")
	coreAssert(t, registration.Ordinal == 1, "unexpected first RGB11 ordinal")

	again, err := manager.RegisterRGB11AssetName(issued.ContractID)
	coreRequire(t, "repeat RGB11 canonical registration", err)
	coreAssert(t, *again == *registration, "repeat registration changed immutable mapping")

	state, err = manager.GetRGB11State()
	coreRequire(t, "read registered RGB11 state", err)
	var registered *RGB11TickerInfo
	for _, info := range state.TickerInfos {
		if info != nil && info.ContractID == issued.ContractID {
			registered = info
			break
		}
	}
	coreAssert(t, registered != nil, "registered RGB11 ticker missing")
	coreAssert(t, registered.Ticker == registration.AssetName, "registered name not used for presentation")
	coreAssert(t, registered.CanonicalName == registration.AssetName && registered.Verified,
		"registered DKVS name not marked canonical")
	coreAssert(t, manager.SetRGB11LocalAssetName(issued.ContractID, "must-not-change") != nil,
		"registered RGB11 name remained locally mutable")

	// Transcend content itself carries no naming descriptor. Later STP work only
	// needs to consume the already-registered canonical AssetName.
	contract := NewTranscendContract()
	contract.GetContractBase().AssetName = *mustRGB11CanonicalAssetName(t, registration.AssetName)
	encoded, err := contract.Encode()
	coreRequire(t, "encode RGB11 transcend contract with canonical name", err)
	decoded := NewTranscendContract()
	coreRequire(t, "decode RGB11 transcend contract with canonical name", decoded.Decode(encoded))
	coreRequire(t, "validate decoded RGB11 transcend contract", decoded.CheckContent())
	coreAssert(t, decoded.GetAssetName().String() == registration.AssetName,
		"transcend contract changed registered RGB11 asset name")

	coreCheckpoint(t, "02_rgb11_dkvs_registered_name", manager, cfg, chain, material)
}

func mustRGB11CanonicalAssetName(t *testing.T, value string) *indexer.AssetName {
	t.Helper()
	name := indexer.NewAssetNameFromString(value)
	coreAssert(t, name != nil, "invalid canonical RGB11 AssetName")
	return name
}
