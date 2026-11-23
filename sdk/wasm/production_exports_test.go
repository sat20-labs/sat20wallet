package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const productionWASMExportAllowlist = `
DeployRunes_Remote abortRecovery acceptGuardianSetup acceptRGB11Consignment activateRecovery activateRootAccount
addInputsToPsbt_SatsNet addOutputsToPsbt_SatsNet batchSendAssets batchSendAssetsV2_SatsNet batchSendAssets_SatsNet
batchUnlockFromChannel batchUnlockFromChannelV2 beginOperationLog bindReferrerForServer buildBatchSellOrder_SatsNet
buildUnifiedContractContent cancelRGB11Transfer cancelStorageSetup changePassword checkDIDName
claimBTCLuckyReward closeChannel commitRecovery commitmentExport confirmClosedChannelSingleUTXOTopup
confirmGuardianSetup confirmRGB11Transfer confirmStorage createGuardianRequest createGuardianResponse
createMonitorWallet createRGB11Invoice createRecovery createWallet deleteOperationHistory
deleteWallet deployContract_Remote deployTickerBrc20 deployTickerOrdx deployUnifiedContract
deposit enableSilentPay ensureAccount estimateDeployUnifiedContract expandAll_SatsNet
expandAsset expandChannel expandChannel_SatsNet exportPublicRGB11Contract exportRGB11Contract
exportRGB11Transfer extractTxFromPsbt extractTxFromPsbt_SatsNet extractUnsignedTxFromPsbt extractUnsignedTxFromPsbt_SatsNet
finalizeSellOrder_SatsNet forceClosePlan fundAutopay getAccountView getAddressStatusInContract
getAllAddressInContract getAllChannels getAllLockedUtxo getAllLockedUtxo_SatsNet getAllRegisteredReferrerName
getAllWallets getAssetAmount getAssetAmount_SatsNet getAssetSummary getBTCLuckyMiningStatus
getBTCLuckyRewards getChannel getChannelAddrByPeerPubkey getChannelStatus getCommitTxAssetInfo
getContractInvokeHistoryByAddressInServer getContractInvokeHistoryInServer getCurrentChannel getDeployedContractAnalytics getDeployedContractStatus
getDeployedContractsInServer getFeeForDeployContract getFeeForInvokeContract getFeeForInvokeUnifiedContract getGuardianContact
getMnemonice getNodePubKey getOperationLogs getParamForInvokeContract getParamForInvokeUnifiedContract
getRGB11View getSilentPayAccount getSilentPayBalance getStorageOptions getSupportedContracts
getTickerInfo getTxAssetInfoFromPsbt getTxAssetInfoFromPsbt_SatsNet getUtxosWithAsset getUtxosWithAssetV2
getUtxosWithAssetV2_SatsNet getUtxosWithAsset_SatsNet getVersion getWalletAddress getWalletCatalog
getWalletPubkey importRGB11Contract importWallet importWalletWithPrivKey init
inscribeName invokeContractV2 invokeContractV2_SatsNet invokeContract_SatsNet invokeUnifiedContract
isUtxoLocked isUtxoLocked_SatsNet isWalletExist issueRGB11Asset loadRecovery
lockToChannel lockToChannelWithExpand lockUtxo lockUtxoForOwner lockUtxoForOwner_SatsNet
lockUtxo_SatsNet mergeBatchSignedPsbt_SatsNet minerUnstake mintAssetBrc20 mintAssetOrdx
mintAssetRunes openChannel preflightRecovery preparePwa038 prepareRGB11Consignment prepareRGB11Inflation
prepareRGB11Transfer previewClosedChannelRecovery previewOpenChannel previewRecovery punishBroadcast
punishBuild punishStatus queryContract quoteClosedChannelSingleUTXOTopup rebuildChannel
recoverAccountManagementFromCurrentWallet recoverAccountManagementFromRootMnemonic refreshAccount refreshRGB11State registerAsReferrer
registerCallback release reopenChannel reservationStatus restoreChannel
resumeLockWithExpandFromL1Tx safetySnapshot sendAssets sendAssets_SatsNet sendGarbage
sendSilentPayment signData signMessage signPsbt signPsbt_SatsNet
signPsbts signPsbts_SatsNet splicingIn splicingOut splitBatchSignedPsbt_SatsNet
stakeToBeMiner startBTCLuckyMining stopBTCLuckyMining sweepBuild switchAccount
switchChain switchWallet syncSilentPayLive unlockFromChannel unlockUtxo
unlockUtxoForOwner unlockUtxoForOwner_SatsNet unlockUtxo_SatsNet unlockWallet updateAccountMetadata
updateOperationLog updateWalletName upgradePwa038Password validateBitcoinAddress validateMnemonic validateSatsNetAddress
verifyRGB11PublicContract withdraw withdrawClosedChannelExact
`

var productionExportPattern = regexp.MustCompile(`obj\.Set\("([^"]+)"`)

func TestProductionWASMExportsMatchAllowlist(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	actual := make(map[string]struct{})
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range productionExportPattern.FindAllSubmatch(source, -1) {
			name := string(match[1])
			if _, duplicate := actual[name]; duplicate {
				t.Fatalf("duplicate production WASM export %q", name)
			}
			actual[name] = struct{}{}
		}
	}

	want := make(map[string]struct{})
	for _, name := range strings.Fields(productionWASMExportAllowlist) {
		want[name] = struct{}{}
	}
	if missing, extra := setDifference(want, actual), setDifference(actual, want); len(missing) != 0 || len(extra) != 0 {
		t.Fatalf("production WASM export mismatch\nmissing: %v\nextra: %v", missing, extra)
	}
}

func TestProductionWASMExcludesDebugAndChannelPrivateKeyExports(t *testing.T) {
	forbidden := []string{
		"dbTest", "batchDbTest", "getCommitRootKey", "getCommitSecret",
		"deriveRevocationPrivKey", "getRevocationBaseKey",
	}
	for _, name := range forbidden {
		if strings.Contains(productionWASMExportAllowlist, name) {
			t.Fatalf("forbidden production WASM export %q is allowlisted", name)
		}
	}
}

func setDifference(left, right map[string]struct{}) []string {
	result := make([]string, 0)
	for value := range left {
		if _, ok := right[value]; !ok {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
