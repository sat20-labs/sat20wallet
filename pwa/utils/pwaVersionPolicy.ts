import { ref } from 'vue'
import { VersionPolicy } from './versionPolicy'
const local = typeof __SAT20_APP_VERSION__ === 'string' ? __SAT20_APP_VERSION__ : '0.0.0'
const build = typeof __SAT20_BUILD_ID__ === 'string' ? __SAT20_BUILD_ID__ : ''
export const versionUrl = import.meta.env.VITE_SAT20_VERSION_URL || `${import.meta.env.BASE_URL}version.json`
export const policyRevision = ref(0)
export const versionPolicy = new VersionPolicy(local, build, () => { policyRevision.value++ })
const cacheKey = `sat20-version-policy:v1:${import.meta.env.MODE}:${versionUrl}:${local}:${build}`
try { const stored=localStorage.getItem(cacheKey); if(stored)versionPolicy.accept(JSON.parse(stored)) } catch { /* Unknown keeps existing capability. */ }
export const acceptVersionPolicy = (info: unknown) => {
  if(!versionPolicy.accept(info))return false
  try {localStorage.setItem(cacheKey,JSON.stringify(versionPolicy.remote))} catch { /* Keep known restriction in memory. */ }
  return true
}
const readOnlyWalletMethods = new Set(["allReservations", "commitmentExport", "estimateDeployUnifiedContract", "forceClosePlan", "getAddressStatusInContract", "getAllAddressInContract", "getAllChannels", "getAllLockedUtxo", "getAllLockedUtxo_SatsNet", "getAllRegisteredReferrerName", "getAllWallets", "getAssetAmount", "getAssetAmount_SatsNet", "getAssetSummary", "getBTCLuckyMiningStatus", "getChain", "getChannel", "getChannelAddrByPeerPubkey", "getChannelStatus", "getCommitTxAssetInfo", "getContractInvokeHistoryByAddressInServer", "getContractInvokeHistoryInServer", "getCurrentChannel", "getDeployedContractStatus", "getDeployedContractsInServer", "getFeeForDeployContract", "getFeeForInvokeContract", "getFeeForInvokeUnifiedContract", "getMnemonice", "getNodePubKey", "getParamForInvokeContract", "getParamForInvokeUnifiedContract", "getPaymentPubKey", "getPublicKey", "getRGB11AddressCarrierWarning", "getRGB11State", "getSupportedContracts", "getTickerInfo", "getTxAssetInfoFromPsbt", "getTxAssetInfoFromPsbt_SatsNet", "getUtxos", "getUtxosWithAsset", "getUtxosWithAssetV2", "getUtxosWithAssetV2_SatsNet", "getUtxosWithAsset_SatsNet", "getUtxos_SatsNet", "getVersion", "getWallet", "getWalletAddress", "getWalletCatalog", "getWalletPubkey", "hello", "init", "isWalletExist", "isWalletExisting", "previewOpenChannel", "punishStatus", "queryContract", "registerCallback", "release", "reservationStatus", "resolveRGB11AddressEndpoint", "resumeRGB11PreparedTransfer", "safetySnapshot", "start", "switchAccount", "switchChain", "switchWallet", "unlockWallet", "validateBitcoinAddress", "validateMnemonic", "validateSatsNetAddress"])
const readOnlyAccountMethods = new Set(['status','getStorageOptions','autopayStatus'])
export const beginVersionDispatch = (method: string, account = false) => versionPolicy.begin((account ? readOnlyAccountMethods : readOnlyWalletMethods).has(method))

// Preflight only; the facade must still recheck immediately before dispatch.
export const assertWalletWriteAllowed = () => versionPolicy.assertAllowed(false)
