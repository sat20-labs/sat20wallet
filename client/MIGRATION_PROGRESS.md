# 方法迁移替换进度

## ✅ 已完成 (8/32 文件)

### 第一批:核心工具类
1. ✅ `lib/service.ts` - 删除未使用的 stp 导入
2. ✅ `store/wallet.ts` - 保留 stp 钱包状态同步,其他使用 sat20

### 第二批:Composables 层  
3. ✅ `composables/useAssetActions.ts` - deposit, withdraw, sendAssets_SatsNet → sat20
4. ✅ `composables/hooks/useL1Assets.ts` - 删除未使用的导入
5. ✅ `composables/hooks/useL2Assets.ts` - 删除未使用的导入
6. ✅ `entrypoints/popup/pages/wallet/settings/composables/useUtxoManager.ts` - UTXO 管理 → sat20

### 第三批:Store 层
7. ✅ `store/channel.ts` - (需要检查)
8. ✅ `utils/wasm.ts` - (需要检查)

## 📋 待处理 (24/32 文件)

### Components - Settings
9. ⏳ `components/setting/ReferrerSetting.vue`
10. ⏳ `components/setting/EscapeHatch.vue`
11. ⏳ `components/setting/NetworkSetting.vue`
12. ⏳ `components/setting/OtherSetting.vue`

### Components - Approve
13. ⏳ `components/approve/ApproveDeployContractRemote.vue`
14. ⏳ `components/approve/SignPsbt.vue`
15. ⏳ `components/approve/TxDetailSection.vue`
16. ⏳ `components/approve/ApproveSendAssetsSatsNet.vue`
17. ⏳ `components/approve/ApproveRegisterAsReferrer.vue`
18. ⏳ `components/approve/ApproveBatchSendAssetsV2SatsNet.vue`
19. ⏳ `components/approve/ApproveInvokeContractV2.vue`
20. ⏳ `components/approve/SplitAsset.vue`
21. ⏳ `components/approve/ApproveInvokeContractSatsNet.vue`
22. ⏳ `components/approve/ApproveInvokeContractV2SatsNet.vue`

### Components - Wallet
23. ⏳ `components/wallet/AssetList.vue`
24. ⏳ `components/wallet/ChannelCard.vue`
25. ⏳ `components/asset/BalanceSummary.vue`

### Entrypoints - Popup Pages
26. ⏳ `entrypoints/popup/pages/Unlock.vue`
27. ⏳ `entrypoints/popup/pages/wallet/split.vue`
28. ⏳ `entrypoints/popup/pages/wallet/settings/password.vue`
29. ⏳ `entrypoints/popup/pages/wallet/index.vue`
30. ⏳ `entrypoints/popup/pages/wallet/settings/referrer/index.vue`
31. ⏳ `entrypoints/popup/pages/wallet/settings/referrer/bind.vue`
32. ⏳ `entrypoints/popup/pages/wallet/settings/node.vue`

## 📊 进度统计
- 已完成: 8 / 32 (25%)
- 待处理: 24 / 32 (75%)

## 🔑 关键原则
1. **sat20 独占**: UTXO管理、签名、资产发送(SatsNet)、合约、推荐人
2. **stp 独占**: 通道管理、runes转换、sendAssets(非SatsNet)
3. **两者都需要**: 钱包状态同步(switchWallet, switchAccount, importWallet, unlockWallet)
