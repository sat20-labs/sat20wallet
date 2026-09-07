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
DeployRunes_Remote abortSession acceptGuardianSetup acceptRGB11Consignment autopayStatus
addInputsToPsbt_SatsNet addOutputsToPsbt_SatsNet allReservations batchSendAssets
batchSendAssetsV2_SatsNet batchSendAssets_SatsNet batchUnlockFromChannel
batchUnlockFromChannelV2 beginOperationLog bindReferrerForServer broadcastRGB11OutOfBand
buildBatchSellOrder_SatsNet buildUnifiedContractContent cancelExpiredRGB11Transfer
cancelRGB11OutOfBandTransfer changePassword checkGuardianSetup closeChannel commitRecovery
commitmentExport confirmStorage consumeGuardianResponse createGuardianRequest
createGuardianResponse createMonitorWallet createRGB11Invoice createRecovery createWallet
deleteAllOperationLogs deleteWallet deliverAndBroadcastRGB11AddressTransfer
deliverAndBroadcastRGB11ProxyTransfer deployContract_Remote deployTickerBrc20 deployTickerOrdx
deployUnifiedContract deposit enableRGB11AddressReceive ensureAccount estimateDeployUnifiedContract
expandAll_SatsNet expandAsset expandChannel expandChannel_SatsNet extractTxFromPsbt
extractTxFromPsbt_SatsNet extractUnsignedTxFromPsbt extractUnsignedTxFromPsbt_SatsNet
fetchRGB11ProxyAck finalizeSellOrder_SatsNet forceClosePlan fundAutopay getAddressStatusInContract
getAllAddressInContract getAllChannels getAllLockedUtxo getAllLockedUtxo_SatsNet
getAllRegisteredReferrerName getAllWallets getAssetAmount getAssetAmount_SatsNet
getAssetSummary getBTCLuckyMiningStatus getChannel getChannelAddrByPeerPubkey getChannelStatus
getCommitTxAssetInfo getContractInvokeHistoryByAddressInServer getContractInvokeHistoryInServer
getCurrentChannel getDeployedContractAnalytics getDeployedContractStatus getDeployedContractsInServer
getFeeForDeployContract getFeeForInvokeContract getFeeForInvokeUnifiedContract getMnemonice
getNodePubKey getOperationLog getOperationLogs getParamForInvokeContract
getParamForInvokeUnifiedContract getRGB11AddressCarrierWarning getRGB11State getStorageOptions
getSupportedContracts getTickerInfo getTxAssetInfoFromPsbt getTxAssetInfoFromPsbt_SatsNet
getUtxosWithAsset getUtxosWithAssetV2 getUtxosWithAssetV2_SatsNet getUtxosWithAsset_SatsNet
getVersion getWalletAddress getWalletCatalog getWalletPubkey guardianIdentity importRGB11Contract
importRGB11ContractFile importWallet importWalletWithPrivKey init inscribeName invokeContractV2
invokeContractV2_SatsNet invokeContract_SatsNet invokeUnifiedContract isUtxoLocked
isUtxoLocked_SatsNet isWalletExist issueRGB11Asset loadRecovery lockToChannel
lockToChannelWithExpand lockUtxo lockUtxoForOwner lockUtxoForOwner_SatsNet lockUtxo_SatsNet mergeBatchSignedPsbt_SatsNet
minerUnstake mintAssetBrc20 mintAssetOrdx mintAssetRunes openChannel preflight
prepareRGB11AddressTransfer prepareRGB11Consignment prepareRGB11Transfer previewOpenChannel
previewRecovery punishBroadcast punishBuild punishStatus queryContract rebuildChannel
receiveRGB11ProxyConsignment recoverAccountManagementFromCurrentWallet
recoverAccountManagementFromRootMnemonic recoverKnowledge refreshRGB11State registerAsReferrer
registerCallback rehearse release reopenChannel reservationStatus resolveRGB11AddressEndpoint
restoreChannel resumeLockWithExpandFromL1Tx resumeRGB11PreparedTransfer safetySnapshot sendAssets
sendAssets_SatsNet sendGarbage setUserShare signData signMessage signPsbt signPsbt_SatsNet
signPsbts signPsbts_SatsNet splicingIn splicingOut splitBatchSignedPsbt_SatsNet
stakeToBeMiner startBTCLuckyMining status stopBTCLuckyMining sweepBuild switchAccount
switchChain switchWallet syncRGB11AddressMailbox unlockFromChannel unlockUtxo unlockUtxoForOwner
unlockUtxoForOwner_SatsNet unlockUtxo_SatsNet unlockWallet updateAccountMetadata updateOperationLog updateWalletName
validateBitcoinAddress validateMnemonic validateSatsNetAddress withdraw
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

func TestAccountRecoveryUsesActiveWalletNetwork(t *testing.T) {
	source, err := os.ReadFile("account_management.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "func accountExpectedNetwork() string {\n\treturn _mgr.GetChain()\n}") {
		t.Fatal("account recovery network must come from the active wallet manager")
	}
	if strings.Contains(text, "Network: walletsdk.GetChainParam_SatsNet().Name") {
		t.Fatal("account recovery locator must not use the SatoshiNet chain parameter as its account network")
	}
}

func TestRootMnemonicRecoveryDerivesIdentityWithoutStoragePassword(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "func recoverAccountManagementFromRootMnemonic(")
	if start < 0 {
		t.Fatal("root mnemonic recovery export is missing")
	}
	end := strings.Index(text[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("root mnemonic recovery export boundary is missing")
	}
	recovery := text[start : start+1+end]
	if !strings.Contains(recovery, `_mgr.ValidateMnemonic(mnemonic, "")`) {
		t.Fatal("root identity must be derived with the same empty BIP39 passphrase as wallet import")
	}
	if strings.Contains(recovery, `_mgr.ValidateMnemonic(mnemonic, password)`) {
		t.Fatal("local storage password must not be used as a BIP39 passphrase")
	}
}

func TestORDXSingleSendUsesV3Builder(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "name.Protocol == indexer.PROTOCOL_NAME_ORDX") ||
		!strings.Contains(text, "_mgr.SendAssetsV3(destAddress, assetName, amt, feeRate64, nil)") {
		t.Fatal("WASM ORDX single send must use the established V3 builder")
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
