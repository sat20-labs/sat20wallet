# 方法迁移总结 (Definitive Version)

根据最新要求，以下是 `sat20` (WalletManager) 支持的方法列表。任何未在此列表中的方法均属于 `stp` (SatsnetStp) 的范畴。

## 1. sat20.ts (WalletManager) 支持的方法列表

### 基础与核心
- `init`
- `release`
- `getVersion`
- `registerCallback`
- `isWalletExist`
- `createWallet`
- `importWallet`
- `importWalletWithPrivKey`
- `createMonitorWallet`
- `unlockWallet`
- `changePassword`
- `getAllWallets`
- `switchWallet`
- `switchAccount`
- `switchChain`
- `getChain`
- `getMnemonice`

### 地址与账户
- `getWalletAddress`
- `getWalletPubkey`
- `getChannelAddrByPeerPubkey`

### 签名与交易处理
- `signMessage`
- `signData`
- `signPsbt`
- `signPsbts`
- `signPsbt_SatsNet`
- `signPsbts_SatsNet`
- `extractTxFromPsbt`
- `extractTxFromPsbt_SatsNet`
- `extractUnsignedTxFromPsbt`
- `extractUnsignedTxFromPsbt_SatsNet`
- `getTxAssetInfoFromPsbt`
- `getTxAssetInfoFromPsbt_SatsNet`

### 资产发送
- `sendAssets`
- `sendAssets_SatsNet`
- `batchSendAssets`
- `batchSendAssets_SatsNet`
- `batchSendAssetsV2_SatsNet`
- `deposit`
- `withdraw`

### UTXO 管理
- `lockUtxo`
- `unlockUtxo`
- `isUtxoLocked`
- `getAllLockedUtxo`
- `lockUtxo_SatsNet`
- `unlockUtxo_SatsNet`
- `isUtxoLocked_SatsNet`
- `getAllLockedUtxo_SatsNet`
- `getUtxosWithAsset`
- `getUtxosWithAsset_SatsNet`
- `getUtxosWithAssetV2`
- `getUtxosWithAssetV2_SatsNet`
- `getAssetAmount`
- `getAssetAmount_SatsNet`

### PSBT 处理
- `buildBatchSellOrder_SatsNet`
- `finalizeSellOrder_SatsNet`
- `splitBatchSignedPsbt_SatsNet`
- `mergeBatchSignedPsbt_SatsNet`
- `addInputsToPsbt_SatsNet`
- `addOutputsToPsbt_SatsNet`

### 合约方法
- `getSupportedContracts`
- `getDeployedContractsInServer`
- `getDeployedContractStatus`
- `getFeeForDeployContract`
- `getFeeForInvokeContract`
- `getParamForInvokeContract`
- `invokeContract_SatsNet`
- `invokeContractV2_SatsNet`
- `invokeContractV2`
- `getContractInvokeHistoryInServer`
- `getContractInvokeHistoryByAddressInServer`
- `getAllAddressInContract`
- `getAddressStatusInContract`

### 推荐人方法
- `getAllRegisteredReferrerName`
- `registerAsReferrer`
- `bindReferrerForServer`

### 核心密钥派生 (L2)
- `getCommitSecret`
- `deriveRevocationPrivKey`
- `getRevocationBaseKey`
- `getNodePubKey`

### 调试与数据库
- `batchDbTest`
- `dbTest`
- `getTickerInfo`

---

## 2. stp.ts (SatsnetStp) 保留与独占方法

除上述方法外，涉及 **L1 通道管理** 和 **特定 Runes 转换** 的方法均由 `stp` 负责：

### 通道管理 (Exclusive)
- `openChannel`
- `closeChannel`
- `getAllChannels`
- `getCurrentChannel`
- `getChannel`
- `getChannelStatus`
- `lockToChannel`
- `unlockFromChannel`
- `splicingIn`
- `splicingOut`
- `getCommitTxAssetInfo`

### 合约部署 (Exclusive)
- `deployContract_Local`
- `deployContract_Remote`

### 节点质押 (Exclusive)
- `stakeToBeMiner`

### 其他与 Runes
- `runesAmtV2ToV3`
- `runesAmtV3ToV2`
- `isWalletExisting`
- `hello`
- `start`
- `getWallet`

### 兼容性方法 (重叠但需调用 stp)
- `splitBatchSignedPsbt` (L1 版本)
- `addInputsToPsbt` (L1 版本)
- `addOutputsToPsbt` (L1 版本)
- `sendUtxos_SatsNet` (如果需要)
