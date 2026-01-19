# 方法迁移替换进度

## ✅ 已完成 (32/32 文件)

### 第一批:核心工具类
1. ✅ `lib/service.ts` - 删除未使用的 stp 导入
2. ✅ `store/wallet.ts` - 保留 stp 钱包状态同步,其他使用 sat20

### 第二批:Composables 层  
3. ✅ `composables/useAssetActions.ts` - deposit, withdraw, sendAssets_SatsNet → sat20
4. ✅ `composables/hooks/useL1Assets.ts` - 删除未使用的导入
5. ✅ `composables/hooks/useL2Assets.ts` - 删除未使用的导入
6. ✅ `entrypoints/popup/pages/wallet/settings/composables/useUtxoManager.ts` - UTXO 管理 → sat20

### 第三批:Store 层
7. ✅ `store/channel.ts` - 检查完毕,符合迁移原则
8. ✅ `utils/wasm.ts` - 检查完毕,符合同步初始化原则

### 第四批:组件层 (Settings)
9. ✅ `components/setting/ReferrerSetting.vue` - 已使用 sat20
10. ✅ `components/setting/EscapeHatch.vue` - 已保留 stp 通道管理
11. ✅ `components/setting/NetworkSetting.vue` - 已同步 release/init
12. ✅ `components/setting/OtherSetting.vue` - 已同步 release/init

### 第五批:组件层 (Approve)
13. ✅ `components/approve/ApproveDeployContractRemote.vue` - 已迁移至 sat20
14. ✅ `components/approve/SignPsbt.vue` - 已使用 sat20
15. ✅ `components/approve/TxDetailSection.vue` - 已检查,无需 stp
16. ✅ `components/approve/ApproveSendAssetsSatsNet.vue` - 已使用 sat20
17. ✅ `components/approve/ApproveRegisterAsReferrer.vue` - 已使用 sat20
18. ✅ `components/approve/ApproveBatchSendAssetsV2SatsNet.vue` - 已使用 sat20
19. ✅ `components/approve/ApproveInvokeContractV2.vue` - 已使用 sat20
20. ✅ `components/approve/SplitAsset.vue` - 已使用 sat20
21. ✅ `components/approve/ApproveInvokeContractSatsNet.vue` - 已使用 sat20
22. ✅ `components/approve/ApproveInvokeContractV2SatsNet.vue` - 已使用 sat20

### 第六批:组件层 (Wallet)
23. ✅ `components/wallet/AssetList.vue` - 已按原则分发方法 call
24. ✅ `components/wallet/ChannelCard.vue` - 已保留 stp 通道管理
25. ✅ `components/asset/BalanceSummary.vue` - 已使用 sat20

### 第七批:页面层 (Popup Pages)
26. ✅ `entrypoints/popup/pages/Unlock.vue` - 已使用 sat20
27. ✅ `entrypoints/popup/pages/wallet/split.vue` - 已迁移至 sat20
28. ✅ `entrypoints/popup/pages/wallet/settings/password.vue` - 已迁移至 sat20
29. ✅ `entrypoints/popup/pages/wallet/index.vue` - 已同步 callback 注册
30. ✅ `entrypoints/popup/pages/wallet/settings/referrer/index.vue` - 已迁移至 sat20
31. ✅ `entrypoints/popup/pages/wallet/settings/referrer/bind.vue` - 已迁移至 sat20
32. ✅ `entrypoints/popup/pages/wallet/settings/node.vue` - 已迁移至 sat20

## 📊 进度统计
- 已完成: 32 / 32 (100%)
- 待处理: 0 / 32 (0%)

## 🔑 关键原则
1. **sat20 独占**: UTXO管理、签名、资产发送(SatsNet)、合约、推荐人
2. **stp 独占**: 通道管理、runes转换、sendAssets(非SatsNet)、stakeToBeMiner
3. **两者都需要**: 钱包状态同步(switchWallet, switchAccount, importWallet, unlockWallet)
