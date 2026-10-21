# SP SDK / PWA E2E 增量验证

日期：2026-10-22。记录用户批准的六组测试，以及随后批准的首页 SP 收款入口最小修复。

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

## 验证边界

本轮不验证未实现的统一 BTC 余额/普通流水、通用 SP UTXO 选币及后台续扫。隔离 regtest 拒绝公开网络切换的测试，不等于允许主网/测试网切换时的迟到响应测试。没有远端服务重置、共享数据修改或 Git 提交。
