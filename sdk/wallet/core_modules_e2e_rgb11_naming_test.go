package wallet

import (
	"context"
	"strings"
	"testing"
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
	coreAssert(t, renamed.CanonicalName == "" && !renamed.Verified, "local rename was promoted to SatoshiNet canonical name")
	balanceAfter, err := manager.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "RGB11 naming balance after rename", err)
	coreAssert(t, balanceAfter.Cmp(balanceBefore) == 0, "SDK local rename changed RGB11 balance")

	descriptor, err := manager.BuildRGB11TranscendRegistrationDescriptor(issued.ContractID)
	coreRequire(t, "build RGB11 transcend registration descriptor", err)
	coreAssert(t, descriptor.ContractID == issued.ContractID, "descriptor contract ID mismatch")
	coreAssert(t, descriptor.BaseTicker == "E2EN", "descriptor must retain original base ticker")
	coreAssert(t, descriptor.GenesisAddress != "" && descriptor.GenesisOutpoint != "", "descriptor missing genesis facts")

	contract := NewTranscendContract()
	contract.GetContractBase().AssetName = issued.AssetName
	contract.RGB11Registration = descriptor
	encoded, err := contract.Encode()
	coreRequire(t, "encode RGB11 transcend contract", err)
	decoded := NewTranscendContract()
	coreRequire(t, "decode RGB11 transcend contract", decoded.Decode(encoded))
	coreRequire(t, "validate decoded RGB11 transcend contract", decoded.CheckContent())
	coreAssert(t, decoded.RGB11Registration != nil, "RGB11 registration descriptor lost in contract encoding")
	coreAssert(t, *decoded.RGB11Registration == *descriptor, "RGB11 registration descriptor changed during encode/decode")

	parsed, err := ContractContentUnMarsh(TEMPLATE_CONTRACT_TRANSCEND, contract.Content())
	coreRequire(t, "round-trip RGB11 transcend JSON contract", err)
	coreAssert(t, parsed.GetAssetName().String() == issued.AssetName.String(), "transcend JSON changed RGB11 asset identity")
	coreCheckpoint(t, "02_rgb11_local_name_and_descriptor", manager, cfg, chain, material)
}
