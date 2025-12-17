# 方法迁移总结

## 已完成的工作

### 1. sat20.ts 新增方法

从 stp.ts 迁移到 sat20.ts 的方法:

#### 基础方法
- `init`
- `release`  
- `isWalletExist`
- `createWallet`
- `importWallet`
- `unlockWallet`
- `changePassword`
- `switchWallet`
- `switchAccount`
- `getMnemonice`
- `getAllWallets`
- `switchChain`
- `getChain`
- `getVersion`
- `registerCallback`

#### 地址和密钥
- `getWalletAddress`
- `getWalletPubkey`
- `getPaymentPubKey`
- `getPublicKey`
- `getCommitRootKey`
- `getCommitSecret`
- `deriveRevocationPrivKey`
- `getRevocationBaseKey`
- `getNodePubKey`
- `getChannelAddrByPeerPubkey`

#### 签名方法
- `signMessage`
- `signData`
- `signPsbt`
- `signPsbt_SatsNet`
- `extractTxFromPsbt`
- `extractTxFromPsbt_SatsNet`

#### 资产发送
- `sendUtxos_SatsNet`
- `sendAssets_SatsNet`
- `batchSendAssets`
- `batchSendAssets_SatsNet`
- `batchSendAssetsV2_SatsNet`

#### UTXO 管理
- `lockUtxo`
- `lockUtxo_SatsNet`
- `unlockUtxo`
- `unlockUtxo_SatsNet`
- `getAllLockedUtxo`
- `getAllLockedUtxo_SatsNet`
- `getUtxos`
- `getUtxos_SatsNet`
- `getUtxosWithAsset`
- `getUtxosWithAsset_SatsNet`
- `getUtxosWithAssetV2`
- `getUtxosWithAssetV2_SatsNet`
- `getAssetAmount`
- `getAssetAmount_SatsNet`

#### PSBT 相关
- `buildBatchSellOrder_SatsNet`
- `splitBatchSignedPsbt`
- `splitBatchSignedPsbt_SatsNet`
- `finalizeSellOrder_SatsNet`
- `mergeBatchSignedPsbt_SatsNet`
- `addInputsToPsbt`
- `addOutputsToPsbt`
- `addInputsToPsbt_SatsNet`
- `addOutputsToPsbt_SatsNet`
- `getTxAssetInfoFromPsbt`
- `getTxAssetInfoFromPsbt_SatsNet`

#### 其他
- `getTickerInfo`
- `deposit`
- `withdraw`

#### 合约方法
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

#### 推荐人方法
- `getAllRegisteredReferrerName`
- `registerAsReferrer`
- `bindReferrerForServer`

#### 新增方法 (TODO: 需要补充参数)
- `batchDbTest`
- `dbTest`
- `signPsbts`
- `signPsbts_SatsNet`
- `extractUnsignedTxFromPsbt`
- `extractUnsignedTxFromPsbt_SatsNet`
- `isUtxoLocked`
- `isUtxoLocked_SatsNet`

### 2. stp.ts 保留方法

只保留 STP 特有的方法:

#### 通道管理
- `closeChannel`
- `openChannel`
- `getAllChannels`
- `getCurrentChannel`
- `getChannel`
- `getChannelStatus`
- `lockToChannel`
- `unlockFromChannel`
- `splicingIn`
- `splicingOut`
- `getCommitTxAssetInfo`

#### STP 特有
- `isWalletExisting`
- `hello`
- `start`
- `release`
- `getWallet`
- `runesAmtV2ToV3`
- `runesAmtV3ToV2`
- `sendAssets` (非 SatsNet 版本)
- `deployContract_Local`

## 下一步工作

需要在项目中将已迁移方法的调用从 `stp` 替换为 `sat20`:

### 需要检查的文件列表

1. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/store/channel.ts`
2. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/store/wallet.ts`
3. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/setting/ReferrerSetting.vue`
4. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/setting/EscapeHatch.vue`
5. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/lib/service.ts`
6. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/entrypoints/popup/pages/Unlock.vue`
7. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/approve/ApproveDeployContractRemote.vue`
8. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/approve/SignPsbt.vue`
9. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/approve/TxDetailSection.vue`
10. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/approve/ApproveSendAssetsSatsNet.vue`
11. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/approve/ApproveRegisterAsReferrer.vue`
12. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/approve/ApproveBatchSendAssetsV2SatsNet.vue`
13. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/approve/ApproveInvokeContractV2.vue`
14. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/approve/SplitAsset.vue`
15. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/approve/ApproveInvokeContractSatsNet.vue`
16. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/setting/NetworkSetting.vue`
17. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/wallet/AssetList.vue`
18. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/wallet/ChannelCard.vue`
19. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/setting/OtherSetting.vue`
20. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/entrypoints/popup/pages/wallet/split.vue`
21. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/entrypoints/popup/pages/wallet/settings/password.vue`
22. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/approve/ApproveInvokeContractV2SatsNet.vue`
23. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/entrypoints/popup/pages/wallet/index.vue`
24. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/entrypoints/popup/pages/wallet/settings/referrer/index.vue`
25. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/entrypoints/popup/pages/wallet/settings/referrer/bind.vue`
26. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/components/asset/BalanceSummary.vue`
27. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/entrypoints/popup/pages/wallet/settings/node.vue`
28. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/entrypoints/popup/pages/wallet/settings/composables/useUtxoManager.ts`
29. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/composables/useAssetActions.ts`
30. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/composables/hooks/useL2Assets.ts`
31. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/composables/hooks/useL1Assets.ts`
32. `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/client/utils/wasm.ts`

### 替换策略

对于每个文件:
1. 检查使用的 stp 方法
2. 如果方法已迁移到 sat20,则:
   - 添加 `import sat20 from '@/utils/sat20'`
   - 将 `stp.methodName` 替换为 `sat20.methodName`
3. 如果方法仍在 stp 中,保持不变
4. 如果同时使用已迁移和未迁移的方法,同时导入两个模块
