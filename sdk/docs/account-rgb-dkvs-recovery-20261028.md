# SDK 账户、RGB11 与 DKVS 恢复证据（2026-10-28）

本记录保存当前开发工作树的恢复材料与验收状态。基线为 sat20wallet `main` 的 `63e2f869d60f2394873e684b77ad72a83c721b1d`。归档在 `sdk/data/recovery-evidence-20261028-v1/`，大小 324,415 字节，SHA-256 为 `55c77a0acd2dc40bb5847da09023b038dafb41ad1f43e67e981237aff0d8f340`。包内有 61 个与本任务相关的 dirty 源文件及测试（含上轮全部 23 个候选）、`sdk/go.mod`、`sdk/go.sum`、当前相对 HEAD 的选定 tracked 补丁、清单和脱敏结果。清单另记全部 83 个 dirty/untracked 文件的哈希；22 个其他任务文件未打包。

归档不是完整可编译工作树：同一 `wallet` 包的其他 dirty 文件未收录；本地 `replace` 依赖 `satoshinet` 和 `indexer` 各有未提交变更，包仅记录其 HEAD 与状态，没有复制这些仓库。旧 `/private/tmp/dkvs-local-commit.v2a30xt1` 证据目录已消失，旧补丁字节、原始 JSONL 测试日志及其余 60 个文件的旧哈希无法重新核验。本轮没有运行测试、构建或读取真实钱包数据库。

## 已观察到的验收结果

以下是 2026-10-27 已读取的历史测试回执，不是本次新运行的结果：

- `./wallet/rgb11` 普通测试 101/101 叶子通过；race 测试 101/101 叶子通过。
- `./wallet` 选定恢复闭环普通测试 14/14 叶子通过。
- 相同选择器的整组 wallet race 在 180 秒累计上限超时：已有 9 个叶子通过，`TestRGB11RejectedCompactionSurvivesOlderACKAndRestart` 当时运行约 21 秒，堆栈位于 Pebble 提交路径；剩余两根用例后来在源码不变时分组执行，5/5 叶子通过。**这不等于一次整组 race 通过**，也不能排除组合运行的耗时或交互问题。
- 相关 86 个 wallet 根测试的发现阶段命中 86/86；普通测试在进程创建前被自动审批拒绝，race 未运行。不得把发现阶段算作测试通过。

本次选定测试入口的复现命令如下；这些命令只是记录，2026-10-28 未执行：

```sh
cd /Users/yingfeng/github/sat20wallet/sdk
go test -json ./wallet/rgb11 -count=1 -timeout=180s
go test -race -json ./wallet/rgb11 -count=1 -timeout=180s
tests='^(TestRGB11LocalCommitDuringContinuousDKVSRefresh|TestRGB11LocalRecoveryCommitIsAtomicAndSurvivesRestart|TestRGB11AccountRecoveryTwoAssetScopesAndScopeBinding|TestRGB11AcknowledgedBackupFailureCompletesLocallyBeforeNewCommit|TestRGB11AcknowledgedBackupBeforeReceiveAllowsLocalCommit|TestAccountManagedACKFinalizesAfterRemoteAdvance|TestRGB11RejectedCompactionSurvivesOlderACKAndRestart)$'
go test -json ./wallet -run "$tests" -count=1 -timeout=180s
go test -race -json ./wallet -run "$tests" -count=1 -timeout=180s
```

原 86 项选择器文件随旧临时目录丢失，不能用上述七根用例冒充其完整复现选择器。

## 尚未关闭的八项交付

1. ACK 原子收尾后，暂态本地 `LoadOutbox` 失败时，现有 worker 必须能在无新 watch 事件时重试写会话对账。
2. DKVS 写资格和 outbox 阻断范围须按真实依赖组隔离；账户 state/blob 继续同组，未决 ACK 与真实冲突仍需阻断相交请求。
3. 新备份发布需有低频、有界合并；已签 exact outbox、ACK 恢复和通知不能被延迟或改写。
4. RGB 公共合约引用的生产发布与恢复校验接线仍待闭环，不能把测试 adapter 当作已接入 Core。
5. 冷恢复后 IFA 需要验证实际后续控制操作，现有证明/锁断言不足以代表可继续增发。
6. OOB settled 后的 consignment 只读导出与接收方补交仍待业务闭环验证。
7. PWA 的 RGB 可读名称、DID 别名及账户同步状态、KV 查看与切端风险提示仍待按已定规则接线和验收；区块浏览器按用户要求延期。
8. 相关 86 项普通/race 回归及一次整组 wallet race 通过尚未取得；已分组通过的 race 不抵消整组超时。

离线只读、个人数据与公共合约引用分离、未备份本地数据不被远端旧值覆盖，以及 RGB/DKVS/账户管理不保留旧格式兼容，仍是后续验收边界。当前记录不表示这些剩余交付已完成。

## v2 源码恢复快照

为补足 v1 只有增量、缺少基线和本地依赖源码的问题，另存 [v2 源码快照](../data/recovery-evidence-20261028-v2/snapshot.tar.gz)、[清单](../data/recovery-evidence-20261028-v2/manifest.json)和[校验收据](../data/recovery-evidence-20261028-v2/receipt.json)。v1 原件保留不变。v2 大小为 6,193,398 字节，SHA-256 为 `01fac79789dd8d3d0bf7b92d001fad0d7a56d8bf4dcd7630d2885fa3388895d3`。

v2 将 `sat20wallet/sdk`、`satoshinet`、`indexer` 的安全源码、测试和构建描述分成各仓 HEAD 基线与当前工作树覆盖层：基线分别有 919、1,221、457 个文件；覆盖层分别有 73、19、12 个文件。sat20wallet 覆盖层中 23 个是本任务前轮候选，另 50 个是其他工作树源码输入；依赖仓库的覆盖层全部标记为依赖编译输入，不视为本任务修改。还包含 18 个 SDK 公开合约测试 fixture、18 个 SatoshiNet 公开测试 fixture，以及 Indexer `go:embed` 所需的 22 个静态文件。每项路径、来源、哈希和排除原因见清单。

已将归档解包到独立临时目录，逐项验证 2,703 个成员，并按“每仓 HEAD 基线、随后工作树覆盖层”的顺序恢复后验证 2,624 个唯一文件的哈希；恢复布局中 SDK 的 `../../satoshinet` 与 `../../indexer` 均解析到对应模块的 `go.mod`。这只证明**源码字节和本地路径可恢复**，没有运行 Go 构建或测试，也没有证明全部运行时测试 fixture 可用。真实钱包数据、凭据、密钥、日志、PID、可执行文件、缓存、旧格式 `.gob` 和大型区块二进制 fixture 均未收录；可能包含钱包材料的 `sdk/e2e/testdata/regtest/wallets.json` 按路径排除且未读取。外部 Go 模块下载及未收录的运行时 fixture 仍是实际编译和测试时需要另行验证的边界。

恢复时先在空目录解包，将每个 `repositories/<仓库>/baseline/` 复制到并列的 `workspace/<仓库>/`，再用同仓 `overlay/` 覆盖。`workspace/sat20wallet/sdk` 与 `workspace/satoshinet`、`workspace/indexer` 必须维持并列关系。恢复完成后先按清单比对哈希，再另行执行构建与测试；不能用本次解包校验代替验收。

## 10 月 28 日 RGB11 合约回归与后续源码批次

其他会话的通道修改后，`transcend/stp` 的 `TestRunAll_contract/rgb11` 六个场景均在公共准备阶段报 `native channel account bind core node: DKVS managed path is not synchronized`。共同入口为 `transcend/stp/contract_rgb11_test.go:30-55` → `rgb11_channel_integration_test.go:30-41` → `sdk/wallet/message_manager.go:166-175,222-227` → `dkvs_core_binding_client.go:100-129`。绑定写前调用 `dkvs_subscription_manager.go:1576-1589`，其中读会话已有而写会话尚未建立时会返回该错误。fixture 在初始化前停止后台 DKVS worker，初始化只登记新路径，具体状态竞争尚未完成专项定位。按用户最新优先级，先完成剩余代码，六场景最后统一测试；不得用等待或绕过同步门槛掩盖失败。

已使用当前 SDK 源码与 fake L1/L2 fixture，单独执行 `cd /Users/yingfeng/github/transcend && go test -json -count=1 -timeout=120s -run '^TestContractRGB11$/^transcend$' ./stp`。首次受默认 Go 缓存沙箱拒绝，测试体未启动，日志 `../sat20wallet/sdk/data/contract-rgb11-dkvs-20261028/single-cache-blocked.jsonl`；同一命令经正常权限复核实际运行，退出码 1，`TestContractRGB11/transcend` 在 1.57 秒复现相同绑定错误，日志 `single-red.jsonl`。该结果发生在本节下面的源码增量之前，不能代表最新树。

本轮新源码增量尚未跑测试：后台 worker 对 durable ACK 本地收尾失败、暂态本地 outbox 读取失败增加有界自动复核，损坏的 outbox 编码仍 fail-fast；outbox 冲突依精确 key 和账户 state/blob/root-wrapper 依赖组隔离，真实离线仍撤销在线写资格；账户备份复用现有 job map，以 10 秒静默窗、1 分钟最长期限合并，已签请求/ACK 清理不参加合并；新增 settled OOB recipient consignment 只读导出，恢复后从本地内容寻址对象读取并验证哈希，不改变 `ResumeRGB11PreparedTransfer` 状态门禁，另补“发送方先 settled、收件方未收到、发送方冷恢复后补交”的真实 SDK 闭环测试源码；清理账户导入 marker 的旧格式特判、RGB 邮箱隐式旧格式回退和未遮蔽公共注册记录的旧格式接受。已执行 `gofmt`、`git diff --check`，后者退出码 0；普通/race 测试以及六场景、完整 `TestRunAll_contract` 均未在新源码上运行。

待关闭：依赖组/定时器回归实测及其失败修复；RGB 公共注册生产发布与 Core 接线；IFA 控制权在冷恢复后的实际增发操作（当前 SDK 只有 genesis IFA 发行入口，未发现后续增发 API，需确定最小接口及交易流程）；OOB 收件方真实补交闭环；PWA；原 86 项和一次整组 wallet race；上述六场景与完整合约聚合。持久命令、日志哈希和新源码字节哈希见 `../data/contract-rgb11-dkvs-20261028/execution-evidence.json`，v1/v2 仅作历史恢复基线，不能覆盖当前代码。

## IFA、OOB 与公共注册的定点源码交接（只读核查，未测试）

- **IFA 控制权可恢复，但后续增发入口缺失。** SDK 的原生验证把非 4000 assignment 投影为 `Type="control"`（`wallet/rgb11/validation_native.go:360-370`）；回归源码确认 genesis 的 4000 与 4010 同载体被分别保留（`wallet/rgb11/validation_native_test.go:130-165`）。现 `IssueRGB11Asset` 的 `InflationAmounts` 只用于 genesis（`wallet/rgb11_manager.go:1517-1565`）。普通 `PrepareRGB11Transfer` 已有发票解析及资产、金额、网络匹配（`:3096-3193`, `:3516-3570`）、同载体全 assignment 保留及 seal reveal（`:3198-3234`, `:3282-3300`）、UTXO 控制/签名路径检查（`:3862-3905`）、PSBT/recipient 与 local consignment、原子 pending/reservation（`:3302-3505`），但选择器只将 4000 作为目标（`:3650-3669`），transition 固定 10000（`:3302-3305`）；接收匹配也只接纳 4000（`:2858-2895`）。依赖 `rgb11@v1.0.0` 的现有 IFA 测试使用 transition 8000、metadata 1000 剩余额度、global 2010 本次增发量、输入 4010、输出 4000+4010（模块缓存 `schemas/ifa_transition_test.go:35-56`）；校验器要求 4010 输入等于两种输出之和（`schemas/transition.go:238-255`）。因此可复用普通发送的后半段，但不能只把普通发送 API 改名或重用 genesis 发行；最小入口和目标选择/transition 参数仍待设计。
- **OOB settled 导出目前校验不足。** `ExportRGB11SettledConsignment` 现只核状态、方向、transport、witness 非空、对象哈希一致并解码 transfer armor（`wallet/rgb11_dkvs.go:1634-1679`）。RGB11 选 scope 时会清旧 scope 并校验钱包身份（`wallet/rgb11_manager.go:125-147`），但导出入口的 `synchronizedRGB11Manager` 只检查 manager 和远端导入 marker（`wallet/rgb11_api.go:76-84,370-378`），未重新验证当前解锁钱包与选定 scope。发票、RecipientID、RecipientVout、WitnessTxID 均持久保留（`wallet/rgb11/types.go:150-168`），但导出没有把这些与原收款人证明和 consignment witness 匹配。已有接收侧验证可参考 `wallet/rgb11_refresh_candidate.go:133-183,360-417`；`wallet/rgb11_manager.go:172-201` 可核本地 SignedTx 的 txid、输入与输出。settled 压缩只清重复的 recipient 字节，不清 SignedTx（`wallet/rgb11/store.go:720-764`）；当前恢复材料也保留 settled pending 的 witness（`wallet/rgb11/recovery.go:353-384`）。冷恢复后补交的测试源码在 `wallet/rgb11_backup_material_test.go:200-269`，当前源码批次未执行。进入生产前须补当前作用域及原收款人、tx/recipient 一致性校验；这不是仅有 hash 校验便已关闭。
- **公共技术登记与 DID 命名尚耦合，生产入口未接。** SDK `PublicRGB11Contract` 可提取不含私有 seal 的公共 genesis/schema 且保持 ContractID（`wallet/rgb11/public_contract.go:10-46`）；当前 `RGB11ContractValue` 同时持有 `ProviderDID` 和公共内容（`wallet/rgb11/registry.go:24-40`），`RGB11Registrar.Register` 在写入之前强制 Primary DID 与可签资格（`wallet/rgb11_registrar.go:43-66`）。SDK 生产源码仅见构造和测试调用该 registrar，未见 Core 服务实例化；普通 SDK 客户端只有已认证的读取入口（`wallet/rgb11_registry.go:28-68`）。SatoshiNet 已提供 authority contract 存储 `IndexerMgr.PutDKVSInternalContract`（`../../../satoshinet/indexer/indexer/dkvs_interface.go:58-65`）及普通读取路由 `GET /v3/dkvs/record`（`../../../satoshinet/indexer/rpcserver/indexer/router.go:60-66`）；其普通 wallet 写入口是 `POST /v3/dkvs/records/batch-cas`，而 authority contract 的普通 `PutLocal` 路径明确拒绝（`../../../satoshinet/indexer/indexer/dkvs/indexer.go:127-137`）。缺的是可信 CoreNode 服务的独立登记入口、身份/合约验证和调用内部 authority 写入的接线；不能由普通 SDK 钱包写替代。迟命名要求无 DID 的技术合约资料先可登记，发行人 DID 别名随后独立登记；现单个不可变 `RGB11ContractValue` 无法表达这个顺序。跨仓改动未执行，也未触真实网络。

## 当前 SDK 候选文件及 PWA 接线核查（只读源码核对，未测试）

v2 恢复包清单把下列 **23 个 SDK 文件**标为本任务前轮候选；这是历史字节快照，不能代表当前源码已通过测试：

```text
wallet/account_managed_ack.go
wallet/account_managed_data.go
wallet/account_managed_data_sync.go
wallet/account_management_state.go
wallet/dkvs_application_client.go
wallet/dkvs_final_boundary_test.go
wallet/dkvs_outbox_sync.go
wallet/dkvs_rgb_local_commit_test.go
wallet/dkvs_subscription_sync.go
wallet/rgb11/engine_store.go
wallet/rgb11/projection_read_atomic_test.go
wallet/rgb11/reservation_store.go
wallet/rgb11/store.go
wallet/rgb11_account_provider.go
wallet/rgb11_account_scope_recovery_test.go
wallet/rgb11_conflict_cas_test.go
wallet/rgb11_conflict_final_boundary_test.go
wallet/rgb11_dkvs.go
wallet/rgb11_local_commit.go
wallet/rgb11_managed_operation.go
wallet/rgb11_store_sync.go
wallet/rgb11_transfer_reservation.go
wallet/wallet_catalog.go
```

本日 `data/contract-rgb11-dkvs-20261028/execution-evidence.json` 记录的**最新未测增量**涉及 12 个文件：`wallet/dkvs_outbox_sync.go`、`wallet/dkvs_subscription_sync.go`、`wallet/dkvs_subscription_manager.go`、`wallet/dkvs_final_boundary_test.go`、`wallet/account_management_state.go`、`wallet/account_managed_data_sync.go`、`wallet/account_import_marker_diagnostic_test.go`、`wallet/rgb11_dkvs.go`、`wallet/rgb11_api.go`、`wallet/rgb11_backup_material_test.go`、`wallet/rgb11/registry.go`、`wallet/rgb11_public_privacy_test.go`。其中 6 个与前述 23 个重叠，已记录候选共 29 个不同 SDK 源文件/测试。另有本说明文档及 data 证据，不属于生产源码。当前 `git status` 尚有其他会话的大量 SDK/PWA dirty（包括通道代码）；不能按 dirty 状态把它们归为本任务，也不能用 v2 包覆盖当前树。

PWA 接线的初始缺口（下表为随后 PWA 源码增量之前的核查，增量状态见下一节）：

| 场景 | 已有链路 | 尚缺或必须校验 |
| --- | --- | --- |
| 账户同步状态 | SDK `GetAccountManagementStatus` 给出 `managed_data_dirty`、`pending_changes`、`dkvs_status`、`dkvs_write_allowed`、缓存时间/高度、待核对请求和错误（`wallet/account_management_state.go:127-153,239-272`）；WASM `account.status` 直接返回该结构（`wasm/account_management.go:1104-1122`）；PWA 类型及调用已存在（`pwa/utils/accountManagement.ts:69-91,137-193`）。紧凑设置页在只读时展示状态、缓存时点、待核对请求（`pwa/components/account/AccountManagementSetting.vue:17-36`）。 | 账户管理主页面虽展示 dirty、待同步数和错误（`pwa/entrypoints/popup/pages/wallet/settings/account-management/Index.vue:59-74`），但未展示 `dkvs_status`、写资格、缓存时点和待核对请求；它可复用现有 `status()`，已在可见时每 30 秒刷新（同文件 `:602-618,740-757`），无需新 SDK 状态接口。紧凑设置页只在 mounted 时读取一次（`:51-53`），需要检查其保持可见时的刷新语义。 |
| DKVS KV 查看 | SDK 底层有经管理缓存读取的 `GetVerifiedRecord`、`ListVerifiedRecords`（`wallet/dkvs_client_final.go:251-278,347-383`），账户 state 和 blob 的既有解码/解密及引用完整性验证分别见 `account_management_state.go:185-225`、`account_managed_data_sync.go:325-352`。 | 当前账户管理 WASM 仅导出 `status`、恢复等方法（`wasm/account_management.go:1119-1138`），PWA 账户管理页无 KV raw hex/安全解码查看器；缺一个限定当前账户已授权路径的**只读 SDK Manager→WASM 查看接口**，返回可核验记录的 raw hex 与已识别格式的安全解码结果。解密须沿当前解锁账户及现有完整性校验，不应由 PWA 任意 key 直接绕过管理缓存或显示私钥/恢复秘密。具体暴露字段尚未实现。 |
| 未备份 RGB 切换端风险 | 账户主页面对 dirty 已有警示（`Index.vue:61-64`）；SDK 的 `managed_data_dirty` 和 `pending_changes` 足以给源端展示尚未备份风险。 | 网络切换入口只调用 `setNetwork` 并重载（`pwa/components/setting/NetworkSetting.vue:76-80`）；store 切换前只要求密码，随后 release/init/recover（`pwa/store/wallet.ts:229-237,263-323`），未用 dirty/待同步状态提示。新设备 `RestoreAccount.vue:54-80` 仅展示已加载备份的钱包和子账户，无法从旧设备本地状态判断较新未备份 RGB，故源端需基于现有 `status()` 做风险提示，不能把新设备成功解密当作源端最新备份的证明。这里不需要新 SDK 状态字段，但需明确其他“切端”入口和 UX 验收。 |
| RGB 名称、proof 与交易 | `GetRGB11State` 提供余额、proof、transfer、`ticker_infos`（`wallet/rgb11_manager.go:1092-1209` → `wasm/main.go:5200-5215` → `pwa/utils/sat20.ts:339-342`）。PWA 用精确 `Protocol/Type/Ticker` 匹配 ticker info，`ticker` 优先作列表 label，保留 `contract_id`（`pwa/composables/hooks/useRgb11Assets.ts:12-52`），列表显示 label、正式名和完整 ContractID（`pwa/components/asset/L1AssetsTabs.vue:124-150`）；发送/收款分别以 `contract_id` 构造请求（`pwa/components/wallet/RGB11SendDialog.vue:227-235,353-360`、`RGB11InvoiceDialog.vue:162-165,317-325`）。 | 当前 SDK `AssetName.Ticker` 仍是完整 ContractID hex（`wallet/rgb11/types.go:55-74`），注册名只填展示元数据（`wallet/rgb11_manager.go:2222-2242`），`validateRGB11TickerInfoName` 甚至拒绝导入元数据中的 canonical name（`:1063-1082`）。PWA proof 过滤却优先以 `canonical_name` 和 proof 的真实 `AssetName` 精确比较（`L1AssetsTabs.vue:376-390`）；一旦已注册的人类名与现 hex key 不同，proof 会被错误隐藏。待已定短正式名/DID 别名在协议映射落地并验收后，PWA 应把**展示名**与 proof/余额的实际键分开；不能靠仅改 label 宣称协议名已接通。pending task/transfer 摘要仍直接显示原始 `Asset.Name.Ticker`（`L1AssetsTabs.vue:64-69,104-109`），也需走同一展示映射。两个名称输入指向同资产的 SDK 解析目前未见生产闭环，不能将 UI 别名等同于协议支持。 |

本节只核代码与现有接口，没有修改 PWA、没有运行 SDK/PWA/合约测试。IFA 实际后续增发 API、OOB 原收款人/tx/作用域校验、公共技术登记生产接线仍按上一节保持开放；尤其 `ExportRGB11SettledConsignment` 目前只有状态和对象哈希校验，**不能标为已完成 OOB 安全闭环**。区块浏览器依用户决定延期。

## PWA 现有状态接口接线增量（源码已改，等待统一测试）

本批仅改 PWA 源码：`components/account/AccountManagementSetting.vue`、`entrypoints/popup/pages/wallet/settings/account-management/Index.vue`、`components/asset/L1AssetsTabs.vue`、`composables/hooks/useRgb11Assets.ts`、`components/setting/NetworkSetting.vue`、`components/setting/DeleteWallet.vue`、`components/wallet/WalletManager.vue`、`components/wallet/SubWalletManager.vue`、`utils/pendingBackupRisk.ts`、`locales/zh.json`、`locales/en.json`。其中紧凑账户设置页原已有其他会话的只读状态增量，本批在其上追加展示与可见时刷新，未覆盖原增量；`utils/accountManagement.ts` 已有状态 DTO 的其他会话改动，本批未编辑。其余通道、WASM 二进制及其他 dirty 均保持原状。

- 账户管理主页面与紧凑页复用现有 `account.status()`，展示 SDK 实际返回的同步状态、写资格、待核对请求；仅 `dkvs_cache_at_ms` 存在时显示缓存核验时间，仅高度字段存在时显示高度。紧凑页增加可见时 30 秒刷新和读取失败提示，不把上次成功值伪装成当前值。没有新增 SDK API 或捏造远端时间。
- 源端切换网络、钱包、子账户及两处删除钱包入口，在动作发生前读取**本机** `managed_data_dirty`、`pending_changes`；普通 DKVS outbox 请求数不单独被解释为 RGB 备份未完成。未确认时明确告知另一设备只能依最后已确认备份恢复；状态读取失败时提示“无法核实”，由用户决定是否继续。删除时未配置备份也给提示。普通切换不删除本地资料；页面不声称能查询另一设备的未备份数据。独立删除组件只在 SDK 确认删除成功后刷新，不再全量清空 PWA 存储，避免取消、失败或仅删除单钱包时误清理其他资料。
- RGB 列表身份仍取 SDK 余额的真实 `asset_key/key/id`。`canonical_name`、ticker/DisplayName 只用于展示，不再被当作 proof 匹配键；待办和转移摘要也按相同资产键查现有可读标签。完整 ContractID 仍在详情可见；发送/收款请求仍按既有 `contract_id` 走 SDK，没有更改资产协议身份或擅自补 DID 别名解析。若 SDK 只给原始 ContractID、没有可信可读元数据，界面仍会显示原始 ID，这需要后端已定名称方案的实际接线。

统一测试时须覆盖：`READY`、`SYNCING`、`OFFLINE_READY`、`RESET_REQUIRED`、`ERROR` 与状态读取失败；有/无真实缓存时间和高度；本机未确认、已确认、未配置、未知四种风险在切换网络/钱包/子账户和两处删除入口的确认与取消；删除失败或只删除一个钱包时其他 PWA 存储不被清空；注册显示名与 proof 真实键不同时仍可见 proof，发送和收款仍用同一 ContractID，待办与转移显示正确。JSON 文件已作静态解析、tracked PWA 补丁 `git diff --check` 为 0；**未运行 PWA 构建、自动化测试、SDK 测试或真实钱包操作**。DKVS raw hex/安全解码查看器仍缺 SDK Manager→WASM 只读接口，待 Astra 设计；IFA、OOB、公共注册和短正式名协议接线也未被本批 PWA 改动关闭。

## 后续实施设计（Astra xhigh，未作为验收结果）

以下是已确定的实现边界，不能据此声称生产功能已通过验证。

- OOB settled 导出沿用 `ExportRGB11SettledConsignment`：固定当前解锁钱包/子账户作用域；以原 Invoice 核合同、数量、recipient；把收款 consignment 的 allocation、witness 与原 SignedTx、txid、vout/script 或 concealed seal 绑定；仅投影收款方所需元数据。只读、无重发、无远端同步，材料缺失时拒绝导出。
- DKVS viewer 新增仅限当前账户的只读 `InspectCurrentAccountDKVS({Cursor?,Limit?})`，WASM 为 `account.inspectDKVS`。raw hex 必须来自同一已验证缓存快照的原记录；安全解码仅展示允许字段；cursor 固定账户与快照，缓存不完整/过期和本地 dirty 明示。PWA 同页展示 raw 与解码两窗，不开放任意 namespace、prefix 或密文输入。
- IFA 使用 `PrepareRGB11Inflation` 从 Invoice 取合同、数量和收款人，消耗 4010 并以 8000 输出 4000 与剩余 4010；混合载体上的旧 4000 和其他控制额度在同一交易由 10000 守恒转回。沿用 prepared/sign/broadcast/journal/reservation 及现有恢复材料。满额 `R=0` 的 4010 occurrence/零值编码在实施前必须核清 schema，不猜测。
- 技术公共合约登记与发行人 DID 命名分离。SDK 可准备不要求 DID 的 codec/registrar，但真实 Core 写入路由及独立验证属于待确认的跨仓范围；公共引用不能验证读取时，私人备份 dirty 不得清除。当前没有生产登记闭环。

2026-10-28 本轮先落地 OOB 导出的作用域、原 Invoice/收款对象/交易绑定及返回字段投影源码候选；静态格式检查已执行，**未运行测试或构建**。该段实施状态仅指当前源码增量，统一回归尚待后续。

## 10 月 28 日后续源码候选（尚未统一测试）

- OOB settled 导出在 SDK 源码中追加当前钱包/子账户/fingerprint 前后核对、原 Invoice 合同/数量/recipient 绑定、收款 allocation 和 embedded witness 对原 SignedTx、收款 script/vout 或 blind seal 的核对，并将返回的 transfer state 投影为收件人必要字段。此处仅表示源码候选；正常补交、篡改拒绝、离线无写入和冷恢复闭环尚须执行。
- DKVS 查看器新增 `Manager.InspectCurrentAccountDKVS({Cursor,Limit})`，WASM `account.inspectDKVS` 和 PWA 账户页的 raw hex/安全解码双窗。入口只列当前解锁账户已验证的本地订阅缓存；游标绑定账户和缓存摘要；展示缓存时间/高度、覆盖范围、本地 dirty 和待确认数，并明确远端当前性未知。已识别账户 state/data 仅显示字段计数与引用信息，不输出解密后的秘密。缓存覆盖不全时不宣称“全部远端”。scope、跨页快照、脱敏与 UI 均未运行测试。
- IFA 新增 `PrepareRGB11Inflation` 的 SDK/WASM/PWA 调用入口，从一张 Invoice 取得合同、数量和收款人；候选实现以 4010 为控制输入、8000 输出新 4000 与剩余 4010，混合载体其余 assignment 在同一 bundle 的 10000 中守恒转回。构造后复用现有 PSBT、recipient/local consignments、pending/reservation、签名及广播路径。`rgb11_inflation_bundle_test.go` 是待执行的同载体双 transition 输入映射用例；**未执行，也没有宣称恢复后再次增发通过**。
- 本地依赖的 IFA schema 已定点核查：transition 8000 的 4010 输出 occurrence 为 `min=0,max=65535`；`schemas/transition.go:238-255` 同时要求 metadata 1000 为剩余额度，global 2010 为本次发行量，输入 4010 等于两者之和。因此满额 `R=0` 候选写 metadata 0 且省略零值 4010 输出。该结论来自 schema 读取，并非本轮 SDK 交易验收。

用户已明确公共技术资料登记的关键业务路径是**市场发起的 transcend 合约部署**，以及后续 splicing-in/expand；deposit 使用已部署合约，不作为首要登记入口。只读定位：`market_satsnet/app/transcend/create/page.tsx:66-101` 由 ticker 查询构造 `assetName` 后调用钱包适配器的 `deployContractRemote`（`lib/walletAdapter.ts:338-340`）；SDK `wallet/interface_contract_client.go:147-204` 在解析/编码合约后才进入付费和签名动作。splicing-in `wallet/channel_operations.go:533-558` 在 `PrepareSTPAssetInputs(...LockInputs:true)` 前有前置核验点；expand 同文件 `:720-751` 在证据准备和 `funderInitExpandingProcessWithRGBEvidence` 前有前置核验点。这只是给后续跨仓设计的调用链证据，本轮没有修改市场、Core 或 satoshinet，也没有接入生产登记。技术合约资料登记仍须独立于发行人 DID 命名资格。

统一测试阶段仍需覆盖：IFA 纯控制输入、混合载体全状态保全、额度不足/满额、再次增发及冷恢复后再次增发；OOB 原收款人/金额/合同/tx/vout/scope 篡改拒绝和只读；viewer 两窗同快照、游标/账户隔离、已验证原始 raw、脱敏、未知类型、缓存不完整。当前源码增量不得替代此前待跑 86 项、整组 race 及六个合约场景。

## 公共技术登记业务顺序修订（Astra 设计，未实施）

市场部署流程须先完成技术登记并验证公开读回，再部署 RGB transcend 合约。splicing-in/expand 校验既有登记；deposit 复用已部署合约，不启动登记。技术登记是“客户端提交公开资料 → Core 独立验证 → authority 写入 → 验证公开读回”；不要求发行人或 Primary DID。canonical 沿用已定名称规则，真实身份仍用完整 ContractID。

| 入口 | 前置校验 | 缺登记时 |
|---|---|---|
| 市场部署 transcend | 网络、完整 ContractID、公开合约资料与部署参数一致，保留现有部署条件 | 市场串联独立、幂等登记；Core 写入且公开读回验证成功后才提交部署 |
| splicing-in | 既有 transcend 合约、通道、本次 RGB 资产的网络和完整 ContractID 一致；登记与公共引用可验证获取，保留原业务检查 | 返回前置错误；通过独立入口登记，随后重新校验并重试，不在资产操作中隐藏补登 |
| expand | 对实际涉及的 RGB 资产做同样校验，不得用扩容替换资产身份 | 同 splicing-in；纯 BTC 扩容不增加 RGB 登记依赖 |

后续接口须区分：确认不存在的 `RGB11_PUBLIC_REGISTRATION_REQUIRED`、读取失败或资料不可验证的 `RGB11_PUBLIC_DATA_UNAVAILABLE`、登记/部署引用/请求身份不一致的 `RGB11_PUBLIC_IDENTITY_MISMATCH`。同 ContractID、同内容幂等成功；内容冲突明确失败，不覆盖。网络错误不能当作未登记。

Core 的部署、splicing-in、expand 入口必须各自核验登记与公共引用，并绑定请求、transcend 合约、通道的真实资产身份；客户端检查只用于提前反馈，不能以客户端 `registered` 标志或展示名称放行。登记服务负责公开合约与技术身份验证；业务入口继续负责原有权限、金额、通道状态与交易证明。DID 显示别名不成为前置条件。

登记成功而部署失败时保留公共登记，只恢复或重试部署；登记结果不确定时先按完整 ContractID 查询，未验证读回前不得部署；部署结果不确定时先核原交易和合约结果，避免重复部署。市场分别显示登记与部署结果，不把登记成功显示为整体成功。网络请求期间不持账户/RGB/通道业务锁；请求前捕获作用域、网络与资产身份，返回后在业务提交前重新校验，复用现有操作记录，不新增全局状态机。

deposit 仅引用已部署的 transcend 合约和其已验证资产绑定，核对网络与完整 ContractID；部署结果或公共引用缺失、冲突、不可验证时返回前置失败，引导部署/独立登记，不暗中登记或创建合约。部署和恢复记录必须保留可验证、可重新获取的公共引用；恢复时公共资料不可取得，相关资产不能标为可继续使用。有效登记可复用，DID 别名变化不改变身份。

以上是业务设计和只读调用链定位，**不是跨仓写入授权**。当前 SDK 的旧登记 codec/registrar 仍把技术资料与 DID/Ordinal 硬绑定（`wallet/rgb11/registry.go:33-40,77-135`、`wallet/rgb11_registrar.go:43-66`）；本轮未改其生产语义，亦未改市场/Core/satoshinet。市场实际调用在 `market_satsnet/app/transcend/create/page.tsx:66-101`；SDK 部署入口在 `wallet/interface_contract_client.go:138-204`，splicing-in 在 `wallet/channel_operations.go:511-579`，expand 在同文件 `:697-751`。未来 Core 服务端真实路由/验证层仍需另行实施，不能把本轮客户端 SDK 候选当作公共登记闭环。

## 技术登记 SDK 候选更新（2026-10-28 13:57 UTC，未执行测试）

上段“旧登记仍耦合”的描述是**更新前的历史状态**。当前 SDK 源码候选已将 `/contract/rgb11/<完整 ContractID hex>` 的技术记录改为 v2，只编码经投影的公共合约资料；`RGB11Registration` 的 ContractID、SchemaID、资产类型、base ticker 都从公开 genesis 推导。`RGB11Registrar.Register(content)` 不再要求 Primary DID、提交者签名资格或 ticker ordinal，按完整 ContractID 对同内容幂等、不同内容冲突。v1 直接拒绝，不双读、不迁移。DID 命名与 ordinal 不在技术记录中；本地别名仍是不可信的本地标签，不能充当发行人认证。这里**未修改**已约定的短正式资产名称规则；当前 SDK 的上链 `AssetName` 仍使用完整 ContractID 的既有实现，命名协议接线不能因这次拆分就视为完成。

SDK 只读前置：`wallet/interface_contract_client.go:152` 在 transcend 部署缴费前检查，`wallet/channel_operations.go:539,723,794` 及 `wallet/channel_expanding_client.go:307` 在 splicing-in、expand 锁资金/构造证据前检查；纯 BTC 不新增此检查。检查 `TickerInfo` 的 ContractID/SchemaID/ticker 与 authority 签名的公开记录一致。`wallet/rgb11_registry.go:43-68` 使用现有 `GET /v3/dkvs/record` **直接**读取公共路径，并再次验证签名、固定 authority、公钥权限、不可变 Seq/TTL、Key 与公开内容；它不要求账户私有 DKVS 已订阅该路径。未登记、读取不可用、身份不符分别返回不同错误。deposit 未增加隐式登记。

私人备份提交之前，`wallet/account_management_state.go:637,2606` 在本地锁外遍历 RGB 恢复包的合约对象，核其 `contract-object` 引用、实际私有合约投影与独立可读的公开记录内容完全一致；每份 receipt 也必须有对应合约引用。失败时不进入该次备份的 PUT/ACK 路径，dirty 不因此清除。公共资料不写入私有备份以替代证明；恢复包仍保留原本不可重建的私有材料。该检查是备份边界，不将普通 RGB 业务写改为等待 DKVS 网络。

**尚未形成生产闭环：**市场当前 `app/transcend/create/page.tsx:101,122` 只向钱包适配器传部署参数，`lib/walletAdapter.ts:338-340` → PWA `utils/stp.ts:515-524` → WASM `sdk/wasm/main.go:4080-4120` → SDK `wallet/interface_contract_client.go:138-210`。SDK 现在依赖本地已验证 `TickerInfo` 取得完整 ContractID；未导入该 RGB 合约的钱包不能仅凭短 assetName 完成前置校验。市场仍缺独立“提交公共合约→可信 Core 验证写入→公开读回”步骤及完整 ContractID/部署参数绑定。SatoshiNet 现有 `indexer/rpcserver/indexer/router.go:61` 仅提供公开记录 GET；`indexer/indexer/dkvs_interface.go:60` 的内部写入只存不解释的 authority 记录，不是生产登记 POST。`transcend/stp/action_remote.go:600`、`contract_deploy.go:435`、`handler.go:2714` 是服务端部署请求链；`handler.go:805` 同时处理 splicing-in 与 `NeedSendSplicingTx=false` 的 expand。这些服务端入口必须独立校验真实 ContractID/部署合约/通道绑定，SDK 客户端检查不能替代。上述跨仓文件**均未修改**，不把内部存储接口或测试适配器当成已上线服务。

本批回归**仅写入源码，尚未运行**：`TestRGB11RegistrarConcurrentTechnicalContracts`、`TestRGB11RegistrarRetryAndFailedCommit`、`TestRGB11RegistrySDKDKVSE2E`、`TestRGB11PublicRegistrationPreflightIdentityAndMissingRecord`、`TestRGB11BackupPreflightRejectsOmittedPublicContractReference` 等。`gofmt` 与 `git diff --check` 静态通过；没有编译、单测、race 或六个通道合约场景的最新树结果。OOB/viewer/IFA 的其余统一回归仍按前述清单待执行，IFA 恢复后实际再次增发与广播尚未有闭环验收。源码候选和本次哈希收据在 `data/contract-rgb11-dkvs-20261028/implementation-registry-20261028.json`；其他 session 的大量 dirty 保持原状。

补充回归源码：`wallet/account_dkvs_inspect_test.go` 现在用测试远端写入并同步真实已签记录，检查缓存 raw hex、hash、敏感明文隐藏、同快照分页和变更后游标拒绝；`wallet/rgb11_backup_material_test.go` 在冷恢复后通过生产 `PrepareRGB11Inflation`、广播、接收方验收及刷新验证 4010 可实际再增发，并在一个场景重复增发。上段“尚未有闭环验收”仍准确表示**未运行**，而非缺少测试源码。新增测试尚需统一执行，未声称通过。

统一验收时的起点命令（此刻**未执行**）：在 `sdk` 目录运行 `go test ./wallet/rgb11 ./wallet -run 'TestRGB11Registrar|TestRGB11RegistrySDKDKVSE2E|TestRGB11PublicRegistration|TestRGB11BackupPreflight|TestRGB11BackupMaterialsColdRestoreCanSpend|TestAccountDKVSInspection' -count=1`，通过后再跑相关账户/DKVS 定向与 `-race`；最后在 `transcend` 目录运行 `go test ./stp -run '^TestRunAll_contract$' -count=1`，包括用户报告的六个 RGB11 通道场景。此前 86 项未执行与整组 race 超时须重新取得真实结果，不能由源码或分组历史结果代替。

## SDK/PWA 静态收口（2026-10-28 14:14 UTC，未测试）

本节是当前源码状态，覆盖上文各历史时点的“仍缺”表述。SDK 的 `PrepareRGB11Inflation` 已经由 WASM `prepareRGB11Inflation` 和 PWA `walletManager.prepareRGB11Inflation` 暴露；PWA 尚无增发操作页面，不能称用户端已完成操作流程。`InspectCurrentAccountDKVS` 已经由 `account.inspectDKVS` 接至账户管理页双窗；只枚举当前解锁根账户的 `/personal/<AccountID>/`、`/blob/<AccountID>/`、`/mail/<AccountID>/` 与其根地址 mapping 的已验证缓存，同页返回原记录 hex/hash、允许字段解码、账户及钱包子作用域、缓存完整性与时点。未知类型为 raw-only，敏感明文只按已知关键词隐藏；**这不是对任意未知明文永不泄露秘密的证明**，需独立安全审查。缓存页不读远端，不能保证此刻为最新远端状态。

上文八项中的 1–3 现在已有源码候选：`dkvs_subscription_sync.go` 对暂态 `LoadOutbox` 失败用有界计时重试，`dkvs_outbox_sync.go`/`dkvs_subscription_manager.go` 按 exact key、账户 state/blob/root-wrapper 依赖组阻断相交请求，`account_management_state.go` 用 quiet/maximum 两界调度合并后台备份。这只说明实现入口存在，仍须统一回归证明成功 ACK、独立 key、失败重启与时限；上文历史“尚未关闭”清单不能直接划为通过。普通业务提交与 dirty/gen 原子本地写的恢复包测试源码在 `dkvs_rgb_local_commit_test.go`、`rgb11_backup_material_test.go`，最新树同样未运行。

OOB settled 证明 SDK 导出保留原发票、收款 allocation、witness 与签名交易的核验及当前账户作用域前后检查。此次补上 WASM `exportRGB11SettledConsignment`、PWA facade，以及转移列表中已结算 out-of-band 发送项的标准 `.rgbc` 下载；下载前后校验当前钱包身份。该列表目前只显示最近 8 条，较早转移的 UI 检索入口仍缺，但 SDK/WASM 可按 TransferID 导出。`wallet/rgb11_backup_material_test.go` 已写冷恢复后再次准备、签名广播 8000 增发及接收余额断言；`recovery.go` 的 proof/validation、合约对象、pending/witness、engine receive 记录根保存 4010 的 Opout、载体和控制所需材料，并拒绝缺依赖的恢复包。所有测试源码在最新树均未执行。

旧格式静态清查：RGB11 收件箱正常扫描此前已只接受 MessageManager 直接消息，本轮把残留的外层签名 consignment/ACK 公共读取回退也改为拒绝，并新增拒旧 ACK 的测试源码。PWA OOB 文本恢复与收款摘要只接受原始字节的精确对象哈希，不再接受旧版 trim 后的哈希；相应验证脚本已改为拒旧断言。账户/DKVS/RGB 恢复 codec 的版本校验仍为严格单版本；`account_root_wrapper` 的缺失发布是现版 state/blob 验证后的根包装维护，`account_managed_data_sync` 对写入中的零值 marker 补当前版本不是旧格式读取；二者未删。`dkvsSignatureLegacy` 名字对应当前通用外层签名记录，不能仅按变量名删除。证据适配器 `rgb11/naming_origin.go` 和 `rgb11_manager.go` 仍有对缺 raw-tx 测试提供器的窄回退；其是否应删除需在统一测试阶段核依赖能力，不能误当已接受旧备份格式。

**名称仍未按已定短正式名落地：**`rgb11/types.go` 的 `NewContractAssetKey` 仍将完整 ContractID hex 写入 `AssetName.Ticker`，`rgb11_manager.go` 的导入验证拒绝 `CanonicalName`，PWA 只是按现有 ticker 信息生成可读标签并以真实 AssetName 匹配 proof。此次不改链上资产键。技术登记的 SDK v2 与 DID 命名已拆开，但生产公共登记仍缺市场提交、Core 写入/验证、服务端部署及 splicing-in/expand 独立校验；SDK 客户端前置不能替代。跨仓授权正在单独等待，未写 `market_satsnet`、`satoshinet` 或 `transcend`。

本任务候选源码/测试以 `data/contract-rgb11-dkvs-20261028/static-closure-20261028/` 的清单、当前文件 SHA-256 和相对当前 HEAD 的选定补丁冻结。冻结时共享仓库 HEAD 已由前述 `63e2f86` 前进到 `fde00ec`（其他会话的 `update lucky mining` 提交）；旧 v1/v2 与上一收据仍指向各自历史基线，不作覆盖。本轮清单包含历史本任务候选与本轮增量，对此前与其他会话共用的路径另作标记；不能仅凭路径或整文件 diff 将每个旧改动归到本任务。BTC Lucky、WebAuthn、WASM 二进制及清单外其他会话 dirty 未改动或纳入选定补丁。没有运行构建、单测、race、PWA 验证脚本、真实钱包或网络服务；最新树验收状态仍是**未验证**。

## 静态安全复核后的 SDK 修补（2026-10-28，源码候选，未测试）

独立静态复核指出六项源码边界；这些是可达条件的推断，不是已复现的泄漏或资产损失。原 `static-closure-20261028` 冻结材料保持原样。本轮在其上新增下列候选，仍需统一构建、定向/race 与通道合约回归：

- viewer 的未知和无法确认安全类型的值默认隐藏 raw，只有成功解封并验证的账户 state、data 引用和 root wrapper 才放行原密文字节。原关键词扫描已移除；PWA 显示 `raw_hidden_reason`，不把隐藏原因误报为已检测到秘密。SDK 查看从同一 DB 固定读视图获取 state、active meta 和全部记录，大缓存分页不再用前后高度相等冒充原子快照；无固定读视图时拒绝查看。新测试源码覆盖普通英文、敏感关键词、非 UTF-8 未知值，以及扫描时底层缓存被改写。
- IFA 本地找零以严格 seal disclosure 匹配，容量完成判断也逐 seal 检查；同一 `txid:vout` 的 4010/4000 可以各自核验，仍拒绝同一个 `(outpoint, AssetName)` 的重复 Opout。构造器合并同载体同 assignment 的兼容 fungible 保全金额，溢出或非兼容同类状态拒绝；回归源码增加混合载体 prepare→广播→接收→刷新及旧 4000/剩余额度可用断言。未执行，不能称资产使用性已验证。
- 在线接收验证完成后，未决 outbox 仅限制相交的 key/账户 state-blob-wrapper 组。单请求可重试 HTTP 响应失败时 worker 可继续独立项；实际传输断线仍停整段写会话。ACK 持久化失败与删除 outbox 后授权读取失败均进入有界重试分类。新增未决三状态的组隔离、独立请求推进和删除后读取失败的回归源码。
- 公开 RGB 合约引用验证移到同步所有完成分支之前，包括 state-only、dirty/no-op 和远端导入，并在外部读取后复核身份/generation；恢复加载和导入前也要求公共资料可验证。失败不应把相关私有备份或恢复标为完成。技术登记的 Core/市场生产服务端仍未接通；该 SDK 门槛不能替代服务端登记。

尚需统一验收的精确边界：新 viewer 源码依赖生产 KVDB 的 `SnapshotDB.NewReadView`，测试内存 DB 使用独立快照适配器；需要实际编译和并发扫描回归。IFA 的同载体测试是源码，尚未运行。直接业务写之前的本地 ACK 收尾仍由现版状态机完成；上述公共引用检查覆盖显式恢复、同步发布/导入完成入口，尚需审查 ACK 后资料失联时的完成语义及所有间接调用。OOB 历史列表仍只载入最近 8 条，PWA 增发入口还需确认可完成签名/广播交互。本轮没有修改其他仓库、没有运行测试/构建、没有提交或上传。

随后 PWA 候选补上了这两处交互缺口：RGB11 转移监控初显 8 条，可每次加载更早 8 条，同一 `getRGB11State` 历史列表中的 settled OOB 项仍按 TransferID 走现有 SDK/WASM 证明导出；账户作用域切换时重置。具有已结算 4010 控制 proof 的 RGB 资产显示增发入口，复用发票模式的准备、收件摘要人工核对、标准 consignment 导出及现有签名广播流程，准备时使用 `prepareRGB11Inflation`。当前 UI 只是源码接线，尚无 PWA 构建、类型检查、真实钱包操作或用户确认结果；不能把增发或 OOB 补交标为已通过。

新候选快照保存于 `data/contract-rgb11-dkvs-20261028/static-review-fixes-20261028/`，保留先前 `static-closure-20261028` 原包。新包记录当前 HEAD `fde00ec629f689839264bb328d0a08574d622ed0`、106 个选定文件（87 tracked、19 untracked）、相对旧包变化的 19 个文件及 3 个排除的其他 dirty；`candidate.patch` 为选定路径相对 HEAD 的补丁，实际 SHA-256 以同目录 `receipt.json` 为准。manifest 从真实逐路径 Git 状态重新采集，修正旧冻结记录遗漏的 `account_import_marker_diagnostic_test.go` 状态；文件级候选中仍有先前其他会话共用的 dirty，不能把整文件差异归给本次修补。新包不是已编译或已验收结果。

## 跨仓技术登记接线候选（2026-10-28，仍未统一测试）

用户明确同意 `market_satsnet`、`satoshinet`、`transcend` 三处最小范围。市场创建页、SDK/PWA 公开合约导出及签名读回、SatoshiNet Core 独立校验写入、transcend 登记入口与部署/splicing-in/expand 前置检查现已写出源码；各仓记录见各自 `docs/rgb11-technical-registration-20261028.md`。技术登记不要求发行人 DID，不对 deposit 隐式补登，不改通用 DKVS 引擎、服务配置或真实数据。新增的 `VerifyRGB11PublicContract` 按公开内容和完整 ContractID 验证签名读回，不依赖部署前尚不存在的聪网 ticker 元数据。

SDK 六项静态修补的独立源码收据见 [rgb11-six-static-fixes-20261028.md](rgb11-six-static-fixes-20261028.md)。跨仓接线和六项修补均未构建或运行，旧历史测试通过数不构成本轮验收。`rgb11/types.go:58-69` 与 `satoshinet/anchortx/nativeproof/validate.go:90-94` 仍把完整 64 位 ContractID hex 写入协议 `AssetName.Ticker`；本轮技术登记未改变该键。已定 `rgb11:f:<ticker>@<12hex>` 短正式名与 DID 别名在协议身份层尚未落地，改键会影响 STP、nativeproof 与历史同资产解析，须先由 Astra 对具体跨仓映射作确认，不能仅改 UI label。
