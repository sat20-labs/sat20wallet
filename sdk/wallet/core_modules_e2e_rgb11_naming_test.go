package wallet

import (
	"context"
	"strings"
	"testing"

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
	coreAssert(t, before.AssetKey == issued.AssetName.String(),
		"human-readable name leaked into immutable asset key")
	coreAssert(t, before.NamingStatus == "local-address",
		"issued RGB11 asset did not resolve genesis address")
	coreAssert(t, strings.HasPrefix(before.Ticker, "e2en@"),
		"default local RGB11 name is not ticker@address-suffix")

	balanceBefore, err := manager.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "RGB11 naming balance before rename", err)
	coreRequire(t, "rename unregistered RGB11 asset",
		manager.SetRGB11LocalAssetName(issued.ContractID, "my-local-rgb-name"))

	state, err = manager.GetRGB11State()
	coreRequire(t, "read renamed RGB11 state", err)
	var renamed *RGB11TickerInfo
	for _, info := range state.TickerInfos {
		if info != nil && info.ContractID == issued.ContractID {
			renamed = info
			break
		}
	}
	coreAssert(t, renamed != nil && renamed.Ticker == "my-local-rgb-name",
		"SDK local RGB11 rename not visible")
	coreAssert(t, renamed.AssetKey == issued.AssetName.String(),
		"SDK local rename changed immutable asset key")
	coreAssert(t, renamed.CanonicalName == "" && !renamed.Verified,
		"unregistered local name was marked canonical")
	balanceAfter, err := manager.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "RGB11 naming balance after rename", err)
	coreAssert(t, balanceAfter.Cmp(balanceBefore) == 0,
		"SDK local rename changed RGB11 balance")

	client, err := manager.ensureDKVSManager().primaryClient()
	coreRequire(t, "create DKVS client for primary DID", err)
	autopay := &DKVSAutopayOptions{
		AddressParams: GetChainParam_SatsNet(),
		PoolContract:  material.auth.Autopay.PoolContract,
	}

	_, err = client.PutPrimaryDID(
		manager.GetWallet(), "alice", dkvsindexer.RecordOptions{}, autopay,
	)
	coreRequire(t, "write primary DID through real DKVS", err)
	primary, _, err := client.GetPrimaryDID(manager.GetWallet().GetPubKey().SerializeCompressed())
	coreRequire(t, "read primary DID through real DKVS", err)
	coreAssert(t, primary == "alice", "primary DID round-trip mismatch")

	if _, err := client.PutPrimaryDID(
		manager.GetWallet(), "abcdefghijk", dkvsindexer.RecordOptions{}, autopay,
	); err == nil {
		t.Fatal("core-e2e: 11-character primary DID unexpectedly accepted")
	}
	if _, err := client.PutPrimaryDID(
		manager.GetWallet(), "notowned", dkvsindexer.RecordOptions{}, autopay,
	); err == nil {
		t.Fatal("core-e2e: unowned primary DID unexpectedly accepted")
	}

	_, err = client.PutPrimaryDID(
		manager.GetWallet(), "company", dkvsindexer.RecordOptions{}, autopay,
	)
	coreRequire(t, "replace primary DID through real DKVS", err)
	primary, _, err = client.GetPrimaryDID(manager.GetWallet().GetPubKey().SerializeCompressed())
	coreRequire(t, "read replaced primary DID", err)
	coreAssert(t, primary == "company", "primary DID replacement mismatch")

	coreRequire(t, "rename remains mutable before SatoshiNet registration",
		manager.SetRGB11LocalAssetName(issued.ContractID, "still-local-before-stp"))
	state, err = manager.GetRGB11State()
	coreRequire(t, "read RGB11 state after primary DID replacement", err)
	for _, info := range state.TickerInfos {
		if info != nil && info.ContractID == issued.ContractID {
			coreAssert(t, info.Ticker == "still-local-before-stp",
				"primary DID change incorrectly froze local RGB11 name")
			coreAssert(t, info.CanonicalName == "" && !info.Verified,
				"primary DID alone incorrectly created a canonical RGB11 registration")
		}
	}

	// Ordinary wallets cannot register /contract/rgb11 records. The selected
	// CoreNode registration service uses the SDK registrar after bridge
	// validation and persists signed opaque bytes through the contract store.
	coreCheckpoint(t, "02_rgb11_primary_did_and_local_name", manager, cfg, chain, material)
}
