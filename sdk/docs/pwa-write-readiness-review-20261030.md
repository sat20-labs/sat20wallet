# PWA 在线写资格：浏览器验收阻断与最小审核方案

**状态更新（2026-10-30）：本页“一律等待在线资格”的生产建议已因用户新澄清撤回，不能据此实施。** 未激活且无备份的新账户应允许本地业务；激活后才实行未同步只读。源码重新分类和待审方案见 `account-preactivation-local-write-review-20261030.md`。本页保留原始失败证据与当时假设，供追溯。

日期：2026-10-30。此页是根因分组和待审核的生产修改方案，**尚未修改 PWA/SDK 生产逻辑**。账户浏览器 v3 的完整日志、exit 和哈希见 `sdk/data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/local-account-pwa-browser-v3-20261030.*`。

## 真实终态

`TestSDKAccountPWAConnectedBrowser` 使用临时 Core/Miner/Bootstrap、fake L1、当次 WASM 和真实 IndexedDB，运行器 `caseSummary` 为 `required=66, pass=54, fail=12, not_run=0`，顶层 exit 1。早先的“11 fail + 1 running”是单行解析错误：后台状态用例的 JSON 失败事件跨两条 Go `Output` 记录，原运行器仍将其记为 fail。

11 个失败在取到可靠在线写资格之前进入后续受管写动作：

- 直接 store/SDK 用例：后台目录刷新与子账户切换、远端删除/旧本地编辑、浏览器密码事务、离线目录重连、共享 IndexedDB 子账户切换、共享 IndexedDB 钱包切换、共享 IndexedDB 密码更改、Guardian 付费确认。这 8 项在 `pwa/scripts/verify/account-management-usage-e2e.mjs:185-218` 的 `fresh()` 新钱包建立后，或在恢复后，立即进行下一次写；最初错误均为 `DKVS managed path is not synchronized`。负例的数据库故障/并发竞争还未触发，不应把这些错误当作安全断言已通过。
- 真实页面用例：地址 DID 正常保存、地址 DID 持久化故障，共 2 项。`NameSelect.vue` 的错误提示真实显示 `DKVS managed path is not synchronized`，因此期望的“DID 已保存”不存在；测试的写入故障/恢复断言尚未触发。
- 真实页面与测试夹具混合用例：创建/导入后目录读取失败，1 项。在 `manager` 路径，调用 `fresh()` 后立即提交第二次创建，观察到一次提交、零次目标目录读故障，说明没有到达注错点；不能判定创建后故障恢复已通过。

第 12 项是独立问题：`usage: account page refreshes background backup failure and success without navigation`。后台恢复后，测试读取的 SDK `status` 已满足 `pending_changes=0` 且 `managed_data_dirty=false`，页面仍显示“最新必要数据尚未备份”。`Index.vue:62-64` 由 `savedState.managed_data_dirty` 渲染此提示，`:663-696,833-834` 依赖页面可见性和 30 秒轮询更新。该测试在 `account-management-usage-e2e.mjs:903` 给“待同步变更：0”45 秒等待，但下一行针对脏提示消失使用 Playwright 默认 5 秒；页面刷新周期为 30 秒。日志中的失败恰在这条 5 秒断言，尚不能据此认定轮询永远不更新，也不能断言只改测试等待就会通过。先记录最后一次页面状态读取时间、可见性和 SDK 脏状态转换时间，再按实际刷新契约修正测试或页面；不能通过删掉风险提示让测试通过。

## 根因与最小生产方案（待用户审核）

SDK `wallet/account_management_state.go:1239,2810` 在受管写之前检查真实在线写资格，这是必须保留的资产/并发边界。PWA 的 `SubWalletManager.vue:81,203-224` 创建按钮仅按提交中禁用，失败只显示“账户创建失败”；`WalletManager.vue` 的创建/改名等入口和 `NameSelect.vue:15,51` 的 DID 保存入口也未根据 `accountSDK.status().dkvs_write_allowed` 表达同步中只读。账户设置页已有状态读取和 30 秒轮询，但不能代替其他写入口的门禁。首次创建钱包必须允许建立账户，后续受管写则需已验证基线。

建议复用现有 `accountSDK.status()`，只为受管写操作提供一个当前账户的只读 UI 状态：页面进入、账户/网络切换和可见性变化时读取；状态未知、同步中、离线或错误时禁用后续写按钮，给出对应简短原因；就绪后重新启用。状态返回时核对 `account_id`/`root_wallet_id` 与当前选择，丢弃切换前的迟到结果，避免旧账户的“可写”点亮新账户按钮。页面停留期间可复用现有可见页刷新机制，不新增远端请求队列或自动重试业务写。提交瞬间仍由 SDK 最终校验；若状态刚变或真实冲突发生，UI 保留原意图并显示明确错误，不将失败当成功。首次创建/恢复入口单独处理，不能用 `active=false` 一刀切禁止根钱包建立；DID 可在恢复配置前设置，但它仍是后续受管写，应按真实 `dkvs_write_allowed` 判断。覆盖创建子账户、DID 保存、钱包增删/改名、密码更改等已失败路径；切换查看与只读操作不被误禁。

最简单的仅测试处理是在每个直接 SDK 故障注入场景前等到 `dkvs_write_allowed=true`；它能使注错到达目标，但**不能验证真实页面是否防止用户过早写入**，故只可作为直接 SDK 场景的前置修正。UI 应补浏览器负例：同步进行中按钮禁用且状态可见；断线后只读、无本地变更；真实恢复到可写后按钮启用且再次提交成功。后台状态用例先增加仅测试诊断，记录页面可见性、刷新次数/上次状态与 SDK 当前状态，再修正确认的缓存刷新原因。全部保留 SDK 原有写门槛和业务断言。

根目录 `AGENTS.md` 要求生产修改先给根因和最小方案由用户审核。本页提供审核对象；目前只继续独立验收与只读取证。
