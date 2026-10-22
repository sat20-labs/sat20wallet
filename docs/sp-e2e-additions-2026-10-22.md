# SP SDK / PWA E2E 增量验证

日期：2026-10-22。记录用户批准的六组测试、首页 SP 收款入口最小修复，以及独立账户备份的 SP 启用标记补全。

## 首页入口修复后结果

用户批准后，仅修改生产组件 `pwa/components/wallet/ReceiveQRCode.vue`：Bitcoin 普通收款弹层增加 `Private · SP` 按钮，点击关闭弹层并进入已有 `/wallet/receive` 页面。页面原有 Ordinary BTC / Private · SP 切换、启用和扫描逻辑继续复用；普通收款二维码和复制逻辑保留。没有新增页面、接口或持久状态。

| 验证 | 结果 | 耗时 / 退出码 |
| --- | --- | --- |
| PWA `npm run compile` | PASS | 50 秒 / 0 |
| `TestSPSilentPayBrowserE2E` | 9 PASS / 0 FAIL / 0 NOT_RUN | 包耗时 191.661 秒 / 0 |

首页原失败用例现同时验证：普通 BTC 二维码可见且剪贴板为实际普通地址；SatoshiNet 保留普通收款且不显示 SP 入口；回到 Bitcoin 后通过真实按钮进入已有收款页面、关闭旧弹层、启用 SP，钱包/子账户/网络保持一致且 SP 地址与 SDK 派生地址一致。其余 8 个 SP 浏览器用例均通过。

证据位于 `/private/tmp/sat20-sp-e2e-validation-20261022`：`home-entry-typecheck.log`、`home-entry-fixed.jsonl`、`home-entry-fixed-summary.json`、`home-entry-fixed/sp-pwa.jsonl` 和同目录 Core 日志。SDK 生产代码未变，复用下文已通过的两组 SDK 结果，未重复执行。

类型检查、脚本语法、`git diff --check` 均通过。此次修改未暂存、未提交；原有两个 WASM 文件的哈希保持不变。

## 测试范围

1. SDK：正常 Manager → 真实 Bitcoin Core 广播确认 → 生产 BaseIndexer/SP HTTP → Manager 扫描、精确余额和幂等刷新；使用两输入和普通找零。
2. SDK：正常初始化、内置 Provider、磁盘数据库、密码认证、关闭重开；另一子账户启用后不能继承余额；目标网络解锁后不能读取旧网络 checkpoint，切回原网络后余额仍在。
3. PWA：从首页收款入口访问 SP；独立验证页面启用、二维码、剪贴板及刷新恢复。
4. PWA：通过页面确认发送、重复点击只广播一次、Core 确认及接收方手动刷新。
5. PWA：延迟真实 HTTP 响应时切子账户、锁定；隔离 regtest 拒绝切换到公开网络。
6. PWA：连接中断保留已核实余额、重试、无效地址拒绝及取消不广播。

PWA 共 9 个独立叶子，复用 `account-management-e2e.mjs`。SDK 和 Core 用例复用原有 SP helper。普通 BTC 的测试读取适配只根据同一个生产 Base DB 与真实 Core 交易字节提供数据；SP、广播、签名和钱包业务不被替换。浏览器加载临时编译的当前源码 WASM，保留生产完整性校验和 IndexedDB。

## 首次补测执行状态（入口修复前）

| 最终验证 | 实际叶子 | PASS | FAIL | NOT_RUN | 包耗时 / 退出码 |
| --- | ---: | ---: | ---: | ---: | --- |
| SDK 生命周期（`wallet-final`） | 1 | 1 | 0 | 0 | 6.696 秒 / 0 |
| SDK Core 收发（`manager-v3`） | 1 | 1 | 0 | 0 | 63.883 秒 / 0 |
| PWA（`browser-v4`） | 9 | 8 | 1 | 0 | 221.995 秒 / 1 |

浏览器通过项：

1. 页面启用、精确地址二维码、真实剪贴板、刷新页面后相同子账户与地址。
2. 页面确认发送 2000 sats、重复确认仅一次真实广播；Core 确认后接收方 5000 → 7000 sats；重复刷新仍为 7000，记录包含实际交易 ID。
3. 扫描迟到响应不能覆盖新选中的子账户。
4. 延迟扫描期间隔离 regtest 拒绝切换到公开网络。
5. 扫描期间锁定隐藏私密收款 UI，重新认证后可扫描。
6. 网络中断保留已确认资金，重试后恢复。
7. 合法 Bech32 字符篡改导致的错误 SP 校验和被拒绝，未广播。
8. 确认页取消未广播。

本阶段唯一失败项是下述 SP-01。已接入 SilentPay workflow，失败断言保留启用；随后入口修复的通过结果见本文件开头。

- 测试夹具修正：110 个成熟资金区块改为 11 批、每批 10 块，独立 30 秒 HTTP client；普通请求保留 helper 的 5 秒时限，测试总时限未改。二维码测试从错误固定 H 纠错等级改为对精确地址的完整合法编码矩阵比对；发送等待首页资产加载、广播成功及本次扫描完成，保留金额、交易 ID 和单次广播断言。
- 初次浏览器启动因沙箱限制（SIGABRT / EPERM）未执行任何叶子；随后在所需权限下完成验证。中间 `browser-v2`、`browser-v3` 的测试识别/同步失败已由 `browser-v4` 复验覆盖，原日志保留。
- 证据根目录：`/private/tmp/sat20-sp-e2e-validation-20261022`。日志和 fixture 为私密材料，不复制钱包密码、助记词或数据库到本文件。
- 最终证据：`wallet-final.jsonl`、`wallet-final-summary.json`、`manager-v3.jsonl`、`manager-v3-summary.json`、`browser-v4.jsonl`、`browser-v4-summary.json`、`browser-v4/sp-pwa.jsonl` 及同目录 Core 日志。

## 生产问题

### SP-01：钱包首页收款入口未接入 SP（已修复并复验）

- 复现：导入并解锁 Bitcoin 钱包，在首页总余额区点击 Receive。
- 期望：真实收款入口能访问 Private · SP 及启用私密收款。
- 修复前实际：只有普通 Receive Address 弹层；等待 30 秒仍无 Private · SP 控件。
- 根因：`pwa/components/asset/BalanceSummary.vue:330` 设置 `showReceiveDialog`，第 55 行渲染普通 `ReceiveQRCode.vue`；SP 控件仅位于独立路由 `/wallet/receive` 的 `Receive.vue`，该入口未连接它。
- 已落实的最小方案：在 Bitcoin 收款弹层中增加进入已有 SP 页面的入口，复用现有页面。普通收款流程保留，SatoshiNet 不显示该入口。
- 证据：修复前 `browser-v4/sp-pwa.jsonl:2` 为 FAIL；修复后 `home-entry-fixed/sp-pwa.jsonl` 全部 9 项 PASS。

本次生产改动仅涉及上述收款弹层组件。

## 独立账户备份补全 SP 启用标记

原来 SP 启用标记通过账户托管 Provider 备份，但独立 `account.Backup` 仅导出助记词和普通子账户元数据。用户要求直接加入备份结构后，已将 `WalletBackup.SPEnabled []uint32` 接入导出及两个独立恢复入口，JSON 字段为 `sp_enabled`，只保存已启用的子账户编号。规范化时排序并拒绝重复或越界编号；精简 root bootstrap 备份时，仅保留账户 0 对应的启用标记。

`RestoreAccountBackup` 和 `RestoreAccountBackupWithResult` 均还原启用标记并持久化到 `WalletInDB`。测试通过独立备份传输到全新数据库、使用新密码恢复，核对账户 0/2 的原 SP 地址及账户 1 保持未启用；不传源设备数据库或托管 Provider 数据。恢复启用标记不会生成虚假的已扫描余额，收款视图仍需重新扫描。

| 验证 | 结果 |
| --- | --- |
| 修复前独立备份恢复回归 | 两个恢复子测试均因 SP 标记丢失失败（预期 RED） |
| 修复后钱包 SP、备份及托管恢复相关回归 | 36 PASS / 0 FAIL / 0 SKIP；两个原失败恢复路径均通过 |
| `sdk/account/...` 全部测试 | 38 PASS / 0 FAIL / 0 SKIP，包含新增标记校验、JSON/加密往返及 root 精简测试 |
| JS/WASM 编译 | PASS；产物仅写临时目录 |

证据目录：`/private/tmp/sat20-sp-backup-validation-20261022`。原始失败与最终结果分别保存在 `red.jsonl`、`green-wallet.jsonl`、`green-account.jsonl` 及对应摘要中，WASM 编译日志为 `wasm-build.log`。现有账户托管 Provider 和其远端格式保持原有实现；没有新增同步机制。修改未暂存、未提交。

## 真实 DKVS 独立设备恢复复验

执行 `TestSDKAccountSilentPayRemoteBackupIndependentRecovery_NotUI`，使用现有真实本地 SatoshiNet bootstrap/core/miner 与生产 DKVS HTTP/签名/CAS/账户恢复路径。账户使用临时 FREE_LOCAL 存储，不配置付费存储或 AUTOPAY。源设备确认启用 SP 的托管备份已上传后关闭；新设备使用独立数据库，仅以原 root 助记词和新本地密码恢复。

最终结果：**1 PASS / 0 FAIL / 0 SKIP / 0 NOT_RUN**，退出码 0，Go 包耗时 17.593 秒（shell 30 秒）。确认：

- 原三个子账户完整恢复；账户 0 的 SP 启用标记为 true，账户 1/2 保持 false。
- 恢复后的 SP 收款地址与源设备地址完全一致；原普通收款地址同样保持一致。
- 账户 ID、root 指纹及托管数据修订号与上传后状态一致；远端记录签名、FREE_LOCAL fee proof 和 TTL 断言均通过。

首轮在准备阶段的稳定快照检查失败，尚未进入新设备恢复。根因为测试将 `ManagedDataEnvelope`（内部加密数据）与完整 DKB1 blob 直接比较。仅修正测试读取：调用现有 `DecodeDKVSBlobValue` 后比较 `Data`，其余修订号、签名、启用标记和地址断言保留；随后同一测试复验通过。本次未修改生产代码。

证据根目录：`/private/tmp/sat20-sp-dkvs-recovery-validation-20261022`。首轮失败保存在 `run.jsonl` / `run-summary.json`，9 份节点日志另存于 `first-run-node-logs`；最终通过记录位于 `fixed-test/run.jsonl` / `fixed-test/run-summary.json` / `fixed-test/metadata.txt`。最终测试文件 SHA-256：`e1a636507334c5176a586ba0ecee07221a939ddc0dc92fb896d43f99c865e3ca`，运行前后相同。目录 0700、日志 0600，未暂存或提交。

本项验证 SDK 账户元数据跨设备恢复，不包含 PWA 恢复 UI、SP 实际资金重扫或付费/公网 DKVS 持久性验收。

## 验证边界

本轮不验证未实现的统一 BTC 余额/普通流水、通用 SP UTXO 选币及后台续扫。隔离 regtest 拒绝公开网络切换的测试，不等于允许主网/测试网切换时的迟到响应测试。没有远端服务重置、共享数据修改或 Git 提交。
