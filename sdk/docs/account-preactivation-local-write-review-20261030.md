# 未激活账户的本地写入门槛：待审核修正

日期：2026-10-30。用户明确：**没有激活账户管理/DKVS、且没有备份的新账户应能正常进行本地业务；激活后才执行“未同步不得本地修改”。** 本页是源码核查和最小方案，未修改 SDK/PWA 生产代码，未将已激活账户的离线只读规则放宽。

## 已确认的分界

- 首次 `CreateWallet` 立刻把账户 profile 和钱包写入同一批次（`sdk/wallet/interface.go:215-247,377-434`）。该 profile 的 `RecoveryConfigured=false`、`StateSeq` 非零、`ManagedDataDirty=true`（`sdk/wallet/account_management_state.go:404-456`）；真正的激活在远端发布、读回之后才以 `RecoveryConfigured=true` 保存（同文件 `522-842`）。因此 `accountProfile != nil` **不等于用户已激活备份**。
- 导入根钱包仅在 root discovery 确认 `not_found` 后消费一次授权并建立此新 profile（`sdk/wallet/account_root_wrapper.go:868-908`、`sdk/wallet/interface.go:297-339`）。`not_found`、远端不可用和已有 root 必须继续区分；不能把网络失败当成“没有备份”。
- 目前 `requireAccountManagedWriteLocked` 只要 profile 非空就要求 DKVS client、在线路径和已确认基线（`sdk/wallet/account_management_state.go:2810-2828`）。账户目录写的 `prepareAccountMutationLocked`（`1239-1250`）、密码更改（`sdk/wallet/interface.go:483-493`）、RGB 恢复预检（`sdk/wallet/rgb11_managed_operation.go:98-110`）都经过它。RGB 公共操作还另外在同文件 `282-290` 对 `!RecoveryConfigured` 硬拒绝；RGB 原子投影发布在 `sdk/wallet/rgb11_local_commit.go:17-33` 又检查一次。因此新账户本地写会被当作已激活但未同步而拒绝。
- 首次钱包建立后已安排 `scheduleAccountManagedStateSync`（`sdk/wallet/interface.go:434`）；该 job 无激活判断，会先尝试远端 Core binding 再 `SyncAccountManagementState`（`sdk/wallet/account_management_state.go:1206-1216`）。这与“激活前没有 DKVS 备份”不一致。应保留本地 profile/必要 RGB 材料和 dirty/generation，停止激活前自动远端发布。
- `GetAccountManagementStatus` 对任何 profile 返回 `Active=true`，并用上述远端门槛计算 `DKVSWriteAllowed`（`sdk/wallet/account_management_state.go:239-270`）。PWA `AccountManagementSetting.vue:8-30` 因此在未激活时显示“管理”和“只读”；`pendingBackupRisk.ts:9-12` 先看 dirty，再看 `recovery_configured`。状态语义需要把“本地可做业务、尚无备份”与“已激活但同步中只读”分开，不能简单把 DKVS 远端写资格改名为本地写资格。
- 现有 `TestDKVSOfflineUnconfiguredProfileRejectsMetadata`（`sdk/wallet/dkvs_local_view_contract_test.go:422-453`）和 `TestRGB11PublicOperationsRequireRecoveryBeforeMutation` 的 unconfigured invoice 子项（`sdk/wallet/rgb11_api_sync_gate_test.go:94-115`）明确把新账户离线/未配置写入当作必须拒绝；这两条旧断言与最新用户规则直接冲突，应改为可验证本地持久化的正例。它们同文件针对**已配置账户**离线写与 Direct 远端写的拒绝子项继续保留。

## 已遇失败重新归类

账户浏览器 v3 的 12 个失败仍是事实，但旧文档提出“一律等 `dkvs_write_allowed` 再写”的生产方案已撤回。`account-management-usage-e2e.mjs:184-191` 的 `fresh()` 直接创建新钱包。其后的子账户/钱包增删改名、密码、DID、Guardian 预激活设置等测试若尚未调用 `setup`，应验证**无需 DKVS 在线写资格的本地成功**，不能加统一等待。`wallet-basic-e2e.mjs:526-532` 和 `wallet-mint-e2e.mjs:159-166` 已加的两处等待让定向用例通过，但可能遮蔽此错误；应在确认场景 `recovery_configured=false` 后撤回，并以新账户立即本地写入的负/正例替换。

不是所有 `DKVS managed path is not synchronized` 都属这一类。`account-management-usage-e2e.mjs:910-964` 的离线目录场景先恢复已激活资料，随后需要在线基线；它的离线只读断言仍应保留。Core account binding 是远端映射写入，`sdk/wallet/dkvs_core_binding_client.go:101-125` 必须有真实在线会话；旧六个合约 fixture 的 Start/Bind 顺序修正不因本原则失效。RGB Direct mailbox 等真实 DKVS 写也仍需在线资格。复原/激活中和远端 root 状态未知的账户不能误判为全新。

`account-management-usage-e2e.mjs:486-549` 的旧用例在 `setup` 激活后刻意让另一设备离线编辑并期待自动收敛；这段期望与当前“激活后离线只读、真实同 key 冲突不自动合并”原则冲突，需单独改写，不能将早期新账户写失败修好后继续接受该旧断言。

## 最小修正方向与安全边界

1. 本地业务门槛按已验证的状态分支：真正无 profile，或新账户已确认 root 不存在且 `RecoveryConfigured=false` 时允许本地原子写；保留必要恢复材料、dirty/generation。`RecoveryConfigured=true` 的账户继续要求在线已验证基线，离线只读、冲突显式失败。远端 DKVS/API 写不使用本地豁免。
2. 激活前的后台 account state/blob 发布不启动；激活流程把最新本地目录和 RGB 必要材料纳入首次公开发布。激活进行中和结果不确定时不能仅凭本地 `RecoveryConfigured=false` 放行并发写；现有 `accountOperationActive`/generation 复核及崩溃后远端已发布、本地未落盘的处理需作定点安全设计，不能把网络失败等同新账户。
3. PWA 用 `recovery_configured` 表示激活边界；未激活时显示“仅本地、尚无备份”，允许本地操作并在删除/换端时提示风险；激活后才根据 SDK 在线写资格禁用受管写入口。SDK 是最终校验者，真实冲突不得由 UI 自动合并。
4. 回归覆盖：新建及已确认 root 不存在的导入在无 DKVS 会话/离线时，目录、密码及有效 RGB 本地操作可完成且冷启后保持；激活首次快照包含这些变更；激活中/结果不确定不丢材料；已激活离线同样操作仍拒绝且零本地变更；Core binding/Direct mailbox 仍需在线；故障注入测试必须到达原注错点，不得用等待遮蔽新账户路径。

上述第 2 项的崩溃歧义可能需要额外持久状态。依根目录 `AGENTS.md`，先由用户审核复杂性取舍；当前没有实施此方案或重跑旧账户 PWA 全套。
