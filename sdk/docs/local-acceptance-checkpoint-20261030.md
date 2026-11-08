# 本地验收检查点（2026-10-30）

本页记录这轮本地验收终态与未关闭项，**不是通过收据**。原 Mac 的 `sat20wallet` 是唯一源码工作目录；没有提交、暂存、推送、部署或读取真实钱包数据库。

## 已有确定结果

- `./wallet` 完整 v5 exit 0：2131 唯一叶子，2103 pass、28 skip、0 fail；对应输入、日志与 inventory 在 `sdk/data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/`。两项账户修复关联 race 3/3 pass。后来共享 XAP 依赖已有变动，这些历史结果不自动成为最终候选的同版通过。
- 账户 PWA fake-L1 浏览器 v3 exit 1：66 必跑、54 pass、12 fail、0 not-run。11 项在受管写资格未建立前尝试后续写，另 1 项是后台备份完成后 UI 脏提示未及时消失；先前的“11 fail + 1 running”是 Go 输出拆行造成的解析错误。原始归类和证据见 `sdk/docs/pwa-write-readiness-review-20261030.md`；其中统一等待在线写资格的生产建议已撤回，SDK/PWA 生产逻辑在本轮保持原状。
- 用户随后明确未激活且无备份的新账户可直接做本地业务；先前“一律等 DKVS 在线资格”的 UI 方案已撤回。源码分界、被误挡的本地写和仍需严格在线的已激活/远端写见 `sdk/docs/account-preactivation-local-write-review-20261030.md`。前述 wallet-basic/mint 两处测试等待仅属历史定向通过，不能当作正确的新账户验收。
- transcend `TestAll` exit 1：wallet 组质押后首开通道触发 `channel history exists without local recovery material`，后续 channel/contract 组未运行；日志与输入在 transcend `data/sdk-acceptance-20261030/`，见 `docs/testall-first-open-stake-20261030.md`。通道恢复/选币只读取证审批受阻，且质押资金规则待用户决定；本线程不重查受阻路径、不修改生产 guard。
- 历史升级：SDK `legacy_pre_native` 四个 Gob fixture 的生成提交可读取且 7 个源码哈希匹配；transcend 的 0.1.4 前迁移提交 `9c86df0c` 可读取，旧完成合约确实保存在 reservation 中。但当前测试用新编码器模拟旧布局，旧依赖 replace 未固定到当时版本，无真实旧合约字节样本。发布矩阵与缺口见 `sdk/docs/release-upgrade-gate-20261030.md`。

## 钱包 PWA 浏览器命令已结束

`go test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=120m -run '^TestSDKWalletPWAConnectedBrowser$' ./e2e`，原 SDK 目录、隔离临时 Core/Miner/Bootstrap 与 fake L1、当次 WASM；原 exec session `56267` 已结束。完整日志及输入、退出码、源码指纹在 `sdk/data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/local-wallet-pwa-browser-v1-20261030.*`。exit **1**，Go 包耗时 2653.351 秒，日志 SHA-256 `9d136303e7f821fc75ea46548b87b663e9225bb23e72c19d40186169fd7d1302`；13 组全部运行并闭合，7 pass、6 fail，无 Go 日志中的 `panic:`。各组运行器 `caseSummary` 合计 104 required、78 pass、5 fail、21 not-run；`rgb-nia-workflow` 的 8 个业务 case pass，但组级页面错误检查失败，剩余运行时检查未运行，因此 6 个失败组不能等同于仅 5 个失败 case。

通过组为 `l1-protocols`、`dapp`、`rgb-ifa-issue`、`rgb-uda-issue`、`rgb-uda-transfer`、`tools`、`mining`。失败组：`pos` 19/1/7、`wallet-basic` 14/1/1、`rgb-nia-workflow` 8/0/1、`biometric` 2/1/2、`mint` 0/1/9、`node` 4/1/1（顺序为 pass/fail/not-run）。

随后仅修改两处**测试夹具**：`wallet-basic-e2e.mjs` 在直接 `addAccount` 前、`wallet-mint-e2e.mjs` 在直接 `refreshRGB11State` 前等候现有 `accountSDK.status().dkvs_write_allowed`，不绕过 SDK 门槛、不删除业务断言。两文件 `node --check`、限定 `git diff --check` 均通过。选取原两个失败 case 的同一 fake-L1 门禁定向复跑 exit 0，两个隔离组各 `required=2,pass=2,fail=0,not_run=0`，总耗时 123.097 秒，无 Go 日志 `panic:`；日志 SHA-256 `82502a654d1dc5026c82c680196b758208aa998024d75be976625cd05923ebf2`，命令/选择器/源码输入/exit 在同目录 `local-wallet-pwa-fixture-targeted-v1-20261030.*`。**这只证明两个目标 case 的前置修正，不把原完整 v1 改称通过，也不验证真实 UI 同步中禁写。**

失败事实：POS 公开 BTC 提款点击后 180 秒没有观察到待确认 L1 交易，后续 7 case 未运行；基础钱包在测试直接调用 `addAccount` 时写会话尚未同步；NIA 确认收款期间后台刷新报告 `publish RGB11 candidate projection: account management snapshot changed`，组级页面错误检查失败；biometric 密码变更后旧 PRF 绑定仍在（WebAuthn 功能按用户安排延期）；mint 组首项在测试直接 `refreshRGB11State` 时被未同步写门槛拒绝；node 组的独立 Core 安全退出在广播后读取本地 reservation 时得到 undefined，浏览器随后报告 `Go program has already exited`。这些是日志事实，未修改断言或生产行为。POS 及 node 的底层根因未定，不用延长超时或忽略错误冒充修复。

## 下一安全步骤与边界

三组有界诊断已完成：同一 fake-L1 runner 仅选 POS 前 19 项、RGB NIA 前 8 项、Node 的 Core stake/unstake，共 29 个指定业务 case；命令仍为上述 `go test`，通过 `SAT20_PWA_E2E_CASES` 限定。exit 1，包耗时 987.76 秒，完整日志 SHA-256 `2661876937b8e1dca6dcff35cac1c65e786e4c24ba51383f2fbdd875bbb7349a`，输入/结果/摘要在同目录 `local-wallet-pwa-three-diagnostics-v1-20261030.*`。Go 组级 `pos`、`rgb-nia-workflow` fail，`node` pass，无 unfinished；运行器共 29 pass、1 fail、2 not-run（POS 18/1/1，RGB 8/0/1，Node 3/0/0）。这些是定向结果，不替代完整门禁。

POS 点击确实调用 `manager.withdraw`，返回 `tx not found: c2f24bca30cb07eaf1842d1b0ee1f93140659d037d0887d66a240f2bac3b1735`，fake-L1 未出现新 pending/confirmed L1 tx。报错字面来自测试 L1 indexer 的 `/btc/rawtx` 或 `/btc/tx/simpleinfo` 未找到交易（`sdk/e2e/pos_pwa_l1_test.go:942-955`）；尚未查明请求为何引用该 txid。此前通道恢复/选币取证受阻，本轮停在这个界限，不借其他路径查。RGB 确认收款业务 8 项通过，但页面错误 `apply RGB11 refresh candidate: publish RGB11 candidate projection: account management snapshot changed` 再次复现；前后 sender/receiver 账户状态均 `READY`、写资格 true、`state_seq=2`、`managed_data_revision=1`。SDK 在原子投影发布前发现本地 RGB engine/projection 基线变化即拒绝覆盖（`sdk/wallet/rgb11_transfer_reservation.go:620-634`），尚未定位竞争写者，不能删校验。Node Core stake/unstake 及运行时收尾 3/3 通过，先前 WASM 退出本次未复现，也未证明已修好。

按失败类别做定向诊断，不重复整个 44 分钟浏览器批次；两项直接 store/SDK 测试的前置已定向通过，UI 生产门禁须等已提交的最小方案获审核并保留 UI 负例。POS 应先记录 UI 提交结果/SDK 错误与 L1 snapshot，node 应先记录 WASM 退出前的返回包/运行时错误，NIA 应区分账户 generation 正常竞争与真正不可恢复失败；三者目前没有足够证据改生产规则。旧版合约升级需先固定旧依赖并生成不含用户秘密的真实旧字节 fixture。TestAll 质押/通道规则及被拒只读取证继续等待明确确认；不绕路重查。其他 XAP 任务已修改共享依赖，最终发布候选必须重记四仓源码与依赖哈希。
