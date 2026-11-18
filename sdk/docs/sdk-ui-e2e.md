# SDK 业务接口收拢与真实 E2E

状态（2026-11-15）：用户接回审批后确定的 Guardian 暂停、私钥钱包禁止账户激活、删除后同密钥作为新钱包重导入均已落地。最新模块回归与 SDK 默认全仓门禁 `20261115T050807Z-91581` 通过：128/128 根、428 实际叶 PASS，7 个既有多设备并发编辑叶 SKIP，Core 15 组全部执行通过。固定 runner 输出的 429 多算了一个带跨级子测试的父根，此处按真实叶更正。按用户最新要求不重复 TestAll。

受影响 PWA 自动化已完成：WASM 29/29；账户运行时/使用场景 38 项及两条真实 regtest 页面旅程分批通过；wallet-basic 与 RGB 共 19 个去重名称通过；Core 冷导入重试与 AUTOPAY 页面通过，三个费率的实际签名付款交易均由 Go 读取节点原始交易核对；生产发布 8/8。关闭 Guardian 后遗留的裸 template 已移除，激活、演练、取消旧会话和冷恢复实际通过。自身 AUTOPAY 测试改为先建立自己的付费备份，并等待页面正常读取结束再打开报价，不恢复已关闭的 Guardian 逻辑。证据前缀为 `/private/tmp/sdk-account-simplify/review-*`。真实 regtest 的独立可见 UI 已通过私钥冷解锁及中英文激活拒绝、双因素备份/演练、新设备取消/冷恢复、删除后全新重导入（含 RGB 合约与发票清理及重启）；付费升级、份额实际下载与激活、一次续费及冷启动解锁已通过，两笔各 10,000 SGAS 的付款均有 Core 确认。恢复份额的“可选”提示和两个未翻译错误已获批修复，TwoFactor 定向及中英文实际页面均通过。

当前实现以本地及时持久化为基础，移除了 RGB 内存“全量核对完成”门槛及备份容量预演，没有采用“准备/确认每次先全钱包刷新”的早期方案。 已删除无生产调用的旧账户整包 AMD/ADE codec；当前仍使用独立模块 AMOD/AMOE，内存条目容器不承载版本或序号。账户管理独占 DKVS 同步和消息投递；保留重启初次同步、前缀订阅与远端导入互斥。下方 2026-11-14 记录保留为历史证据，不代表当前失败状态。

实际 UI 发现的两处解锁问题已单独获批修正：解锁后使用 SDK 已核验目录，删除重复的账户根查询；密码及生物识别成功导航使用 replace。新增私钥真实表单冷解锁及激活拒绝页面用例，并在原自动锁屏用例验证真实解锁后返回。最新 `TestPWAAccountViews` 3/3、`TestPWAUnlockNavigation` 1/1、生物识别 4/4、发布检查 8/8 均通过。此批不改 SDK 生产代码，不重复 SDK 全仓或 TestAll。

恢复退出导航也已单独获批：清理 SDK 会话后，若上一页为既定返回页则 back，否则 replace；不改变恢复规则。原 TwoFactor 旅程增加“演练完成 → 账户页返回 → 钱包页”断言，最新定向回归通过（45.251 秒，退出 0）；发布检查 8/8 通过（94.14 秒，退出 0）。对应证据 `review-pwa-return-1.jsonl`、`review-pwa-release-4.log`。同一付费账户的 Finish/Cancel 均实际返回账户页，再 Back 到 Settings；普通新设备直达 `/restore-account`（history.back=null），使用同一付费包预览后取消，回到 Welcome 且目录仍为空。未提交恢复或新增付款。三条退出路径均通过。

当前验收边界：7 个既有多设备并发编辑叶仍为 SKIP；已有旧 RGB 证明的设备离线跨过整个删除并重导入过程，仍可能在远端合并时冲突，本轮未增加自动清空或新的版本机制。普通删除并重导入、冷启动及新设备恢复已经覆盖。USDT 等待接入配置。

## 边界

- Go 定义以 `wallet/ui_types.go` 为准：RGB 13 个业务方法、账户 18 个、操作记录 2 个。
- Guardian 暂停：五个 Guardian 业务方法返回 `guardian recovery is disabled`，2of3 创建和加载同样拒绝；PWA 已移除 Guardian 操作。底层加密/分片实现保持，暂不设计新的付费托管。账户自己的双因素备份、付费存储和续费继续保留。
- 独立私钥钱包保留普通钱包业务能力；账户根必须是助记词钱包，初始化/激活不得把私钥钱包当账户根。
- 原通道管理和通道合约 API 保留。本轮没有实现此前草案中的 `ChannelAPI`。
- RGB 原生协议方法仍服务原生 SDK 调用方；新增 Go 业务方法使用 `RGB` 名称，WASM 使用审核过的 `RGB11` 名称。例如 `Manager.PrepareRGBTransfer` 对应 `prepareRGB11Transfer`。
- WASM 只解析参数、调用 SDK、序列化结果。PWA 只提交用户输入、展示结果、复制/下载 SDK 生成的文件。
- 证明、收款回执匹配、Direct 投递与 ACK、金额换算、付款报价、恢复会话及恢复材料验证由 SDK 处理。
- `RGBTransferView` 不增加 PWA 未使用的 `Kind` 字段；不通过历史日志猜测增发类型。资产本身的 `RGBAssetView.Kind` 保留。
- 新业务的操作记录只开放读取和清理已结束历史。用户本轮明确保留三个 PWA L1 执行入口，因此原 `beginOperationLog` / `updateOperationLog` 仅作为这三个旧调用的例外保留；其他页面不调用这两个写入口。删除历史不删除转账、收款或付款状态。原生业务入口记录命令结果；日志失败不覆盖业务返回值。

## 真实 E2E 入口

Go：`e2e/sdk_ui_e2e_test.go: TestSDKUIFlows`。
浏览器用例：`../pwa/scripts/verify/sdk-ui-e2e.mjs`。
复用：`runSDKPWARealRegtestBrowser`、现有 Playwright 浏览器设备、真实 Bitcoin 控制器和真实 WASM 构建。没有新增模拟索引器、协议状态替身或测试框架。

准备现有 regtest 环境的配置后，在 SDK 目录执行：

```sh
go test ./e2e -run '^TestSDK(UIFlows|AccountFlows|PaidAccountFlow|AccountReview|PartialRestore)$' -count=1 -timeout=35m -json
```

五个入口共用 `SAT20_ACCOUNT_UI_CONFIG`（真实 SDK regtest 配置）、`SAT20_ACCOUNT_UI_FIXTURE`（已准备的私密身份及资金）和 `SAT20_REAL_REGTEST_RUNTIME`（证据目录）。缺少配置直接报错；不会重建节点或将缺少环境视为通过。Bitcoin 控制器使用现有 `SAT20_REAL_BITCOIN_CLI`、`SAT20_REAL_BITCOIN_DATADIR` 和 `SAT20_REAL_BITCOIN_RPC_PORT`。

要求：真实 Bitcoin Core 与 L1 索引器；一个 bootstrap 和一个 Core，钱包通过 Core 服务工作；两个节点同链尖、Core 高度至少 32，WASM 按 32 高度激活 POSv2；真实 AUTOPAY 已部署并有运行余额；原资金钱包有足够 BTC、L2 普通聪和 SGAS。环境缺失须先修环境，不伪造节点或索引器响应。

各完整流程使用预先准备的独立身份及浏览器存储，同一流程最多同时打开两个原钱包和一个恢复设备；恢复前先关闭原 writer。失败回归复用原状态，不重新铸造或替换失败交易；H37/H41 不发起新提款。

## 用例与完整流程

| 用例 | 用户流程 | 关键业务断言 | 新接口覆盖 |
| --- | --- | --- | --- |
| RGB 手工转账、增发、取消与重启 | 发行 IFA → 导出/导入合约 → 创建 invoice → 准备转账 → 导出证明 → 收款验证并生成回执 → 原 ID 确认广播 → 实际出块 → 接受证明 → 增发 → 取消另一笔待办 → 重启 | 错误回执不广播；收款从 0 到 10.25，再到 12.75；发送余额 89.75；取消释放可用余额；重启后余额和原交易 ID 保留；清理日志不删除未完成业务 | RGB 全部 13 个方法；两个操作记录方法 |
| Direct 离线收款 | 双方配置恢复 → 发行/导入资产 → 接收方离线 → 投递 → 暂停发送方网络 → 接收方保存证明与 ACK → 再离线 → 原 ID 广播 → 通过第二笔真实 RGB 手工自收花费同交易的 RGB 找零 → 收款钱包重启上线 | 普通 Direct 仍等 ACK；真实索引器证实另一输出已花费、自己的输出仍在；余额恰增 10.25；刷新时正常出块不标记异常 | Direct 分支、账户 2of2 激活和同步 |
| 付费双因素备份、演练与续费 | 预检 → 选择/取消存储 → 真实付费存储 → 知识答案和用户份额激活 → AUTOPAY 续费 → 演练 → 新设备恢复并重启 | 等待中的存储付款沿用同一 txid；错误报价不接受；演练不能 Commit；冷恢复相同身份和账户清单，并能查询自己的付费存储 | 账户现行双因素路径，包括 FundAutopay |
| Guardian 关闭 | 调用五个 Guardian 方法及 2of3 创建 | 明确拒绝，不消费有效存储授权；同一授权仍可创建并取消 2of2 包 | 暂停功能的真实 WASM 拒绝路径 |

Direct 用例通过控制两个独立浏览器的在线状态，使收款钱包在广播前离线；不修改 SDK 状态机。检查其他输出是否花费，使用真实 `/v3/utxo/info/:outpoint`，不要求索引器返回花费交易。

## 2026-11-14 历史验证与当时剩余工作

已完成：SDK 原生构建；生产 WASM 构建；PWA 类型检查与私有 regtest 完整打包（包含 WASM 完整性检查）；新 E2E 可编译；JavaScript 语法检查；旧日志迁移函数体静态比对。DID 铭刻在原交易流程前增加已批准的双来源检查。未运行单元测试、TestAll 或暂停中的 PWA UI 验收。

本轮补充决定已落实：
- 备份风险由 `GetAccountView().BackupRisk` 返回，维持原优先级；PWA 只选择是否显示删除钱包的提示。
- DID 新增 `CheckDIDName(name string) (string, error)`，检查 L1 索引器及本网络、当前钱包/子账户的既有铭刻日志。PWA 检查按钮和 `InscribeName` 共用该判断；只有明确的名称不存在响应才能继续。
- 广播签名交易、广播 PSBT、构造 L1 卖单保留原 PWA 执行及日志调用，不新增 SDK 执行入口。

账户数据归属：页面输入、步骤和展示状态留在 PWA；恢复验证、解密和提交由 SDK 账户管理处理。`account_activation_resume.go` 与 `account_restore.go` 沿用既有 profile、钱包数据及导入进度 DB 持久化；未提交的明文恢复材料只在 SDK 内存中短暂持有，取消、提交完成或关闭 Manager 时清理。WASM 只转发参数和结果。

## 2026-11-14 历史：账户恢复专项新接口 E2E

用户于 2026-11-14 明确批准删除旧恢复会话测试及模拟数据，保留 SDK 当前业务临时状态。现已删除 `wasm/account_management_session_test.go`、PWA 的 `scripts/diagnostic/account-storage-session-resume.test.mjs`，以及导出检查中依赖已删除 WASM 私有函数的测试。共用 Promise 等待函数移到原通道测试文件，通道测试行为不变；导出白名单对齐已批准的新接口。

独立 Go 入口：`TestSDKAccountFlows`（`e2e/account_ui_e2e_test.go`）；付费扩展入口为 `TestSDKPaidAccountFlow`，使用独立的已供资身份。用当前源码构建 WASM，复用现有浏览器 runner；每条 case 是完整业务流程，两个子测试独立报告。账户专项使用三个隔离测试身份，不调用 RGB 转账、通道或 TestAll。

| 子测试 | 完整流程与关键断言 |
| --- | --- |
| TwoFactorRecovery | 导入钱包 → 存储选择/取消 → 创建并取消备份 → 2/2 备份激活 → 关闭原设备 → 新设备加载恢复码、预览、取消 → 重新预览后刷新 → 重新加载并提交 → 重启解锁；取消和刷新后的旧会话不能提交，未预览不能提交，已提交不能重复提交；恢复后的根账户、钱包指纹和子账户清单一致；账户命名空间仅包含批准的 18 个方法。 |
| GuardianRecovery | 临时存储 → 监护人联系人、设置回执及最终恢复码 → 监护人回复验证 → 激活并等到 `backed_up` → 只读演练拒绝提交 → 新设备通过监护人恢复 → 重启核对根账户与清单。 |
| GuardianPaidRecovery（独立付费入口） | 真实存储付款 → 监护人建立联系和回执 → 最终恢复码 → 监护人回复验证与激活 → 续费 → 只读演练 → 关闭原设备 → 新设备通过监护人恢复 → 重启核对；错误报价拒绝、演练不能提交，恢复身份及账户清单一致。 |

运行配置由 `SAT20_ACCOUNT_UI_CONFIG`（真实 regtest SDK 配置）和 `SAT20_ACCOUNT_UI_FIXTURE`（权限 0600 的三身份 fixture）传入；`SAT20_REAL_REGTEST_RUNTIME` 保存证据。付费 case 要求真实 AUTOPAY 和已供资的付费测试身份。准备不足是环境阻塞，不能算通过。fixture 不应包含现有 PWA 活跃身份。

```sh
go test ./e2e -run '^TestSDKAccountFlows$' -count=1 -timeout=35m -json
```

当前执行事实：
- `account-run-5`：TwoFactorRecovery PASS，确认 `backed_up` 后，取消、刷新、重新加载、冷恢复和重启核对完整通过。
- 同批的付费扩展在 `ConfirmStorage` 返回 `invalid dkvs fee proof`；未达到监护人设置或续费，不能算通过。只读实查节点返回 `details.exists=false`，查询的原供资地址 SGAS=0，确认为付费环境前置缺失；没有修改 SDK，也没有自动部署合约或转移资金。
- `account-run-6`：仅运行 GuardianRecovery，PASS，Go 退出码 0，业务用例耗时 43.42 秒。通过临时存储完成监护人设置、激活、只读演练、新设备恢复及重启核对；包耗时 733.657 秒包含共享 E2E 锁排队。
- 账户恢复专项共两条完整流程通过；这是生产 WASM、真实节点与浏览器存储的 SDK E2E，不代表暂停中的 PWA 页面交互验收完成。RGB 扩展未运行；付费存储和 FundAutopay 随后通过验证，见下节。
- 前几轮发现并修正的是测试问题：旧 runner 强求双 peer/H1-H2 通道记录；取消设置误断言 Storage 必须为 null；冷恢复未等待独立的 `backed_up` 状态。未修改生产业务代码。

测试不会直接构造 SDK 私有会话，也不伪造恢复份额、数据库或节点响应。原失败记录与 DB 保留，未运行老模块单元测试或 TestAll。


## 2026-11-14：补齐 AUTOPAY 后的付费 E2E

用户授权仅补齐原独立 P07 regtest 的 AUTOPAY 合约和测试资金，完成付费存储及续费 E2E。保持 bootstrap + Core 网络、H32 激活配置及原节点数据库；未修改生产业务代码，未运行 TestAll，也未恢复 PWA 页面交互验收。

准备工作复用现有真实 L1 SGAS 部署/铸造、通道转入/解锁、`DeployUnifiedContract` 和合约充值帮助函数。固定 ordinary-1 为 AUTOPAY 部署者；付费账户和监护人使用独立测试身份，不覆盖已通过恢复专项的账户。临时准备代码通过 Go overlay 编译，仅增加测试入口，未替换生产源文件或节点响应。

实际准备预算：首次存储报价 10,000 SGAS；付费账户共 20,300 SGAS，覆盖首次付款、一次续费及两次调用费用；另有 10,000 L2 普通聪用于交易费用。AUTOPAY 运行余额充值 2,400 SGAS，可覆盖预算内的 12 次结算。部署者本轮 SGAS 总目标为 27,900。以上为准备完成时的余额，不是用例执行后的剩余余额。

- 合约：`tc1qyq3fjn6t9cpgrynzeyljssgw0u92enachzv0s3xykukn586axlj04g52mtcu`。
- SGAS 供资交易：`939efc83ef71747c0b4f263efb289d961565a2f5e5f1667dd283f2d5e3b713a1`。
- L2 交易费用供资：`5064fd8fd9a55186316baafc87c615dca5e85d8420ed7d740fe63e2a40fd019c`。

执行结果：

| 阶段 | 结果 | 实际覆盖 |
| --- | --- | --- |
| `TestAccountPaidEnvironment` | PASS，退出码 0，包耗时 38.379 秒 | 真实资产准备、跨层供资、默认 AUTOPAY 部署、运行余额与付费账户资金核对 |
| `TestSDKPaidAccountFlow/GuardianPaidRecovery` | PASS，退出码 0，叶子耗时 47.39 秒 | 付费存储、监护人设置与激活、错误续费报价拒绝、真实续费并等待 ready、只读演练拒绝提交、新设备恢复及重启核对 |

首次准备在 STP 尚未监听时提前连接而失败；仅给临时准备程序补上已有 STP 就绪检查后继续，保留已准备的资金、数据库和测试身份，没有清理数据来消除失败。此前 `invalid dkvs fee proof` 的环境缺口已由真实部署和供资解决，没有修改 SDK 校验逻辑。

证据目录：`/private/tmp/sdk-ui-consolidation/paid-run-2/`，包含 `prepare-summary.json`、`prepare.jsonl`、`prepare-exit`、`summary.json`、`tests.jsonl` 和 `exit-code`。准备完成时的公开状态在 `/private/tmp/sdk-ui-consolidation/paid-env/paid-ready.json`。私密 fixture、RPC 凭据及恢复材料仅留在本地权限受限文件中。

复测入口为 `go test ./e2e -run '^TestSDKPaidAccountFlow$' -count=1 -timeout=35m -json`，需要配置已部署 AUTOPAY 的真实环境与独立已供资测试身份；不得将已完成恢复的身份重新作为空白账户导入来重跑整条用例。


## 2026-11-14：新接口全场景执行

| 用例 | 首次全组 | 已确认事实 / 剩余阻塞 |
| --- | --- | --- |
| TwoFactorRecovery | PASS | 多钱包及子账户、2/2、取消、错误输入、离线修改、冷恢复、重启完整通过 |
| GuardianRecovery | PASS | 临时存储、监护人设置、演练、用户份额 + 监护人恢复完整通过 |
| GuardianPaidRecovery | PASS | 真实首次付费、续费、错误报价、知识答案 + 监护人恢复完整通过 |
| DIDHistory | PASS | 铭刻、日志判重、索引器确认、清历史后判重完整通过 |
| RGBManual | FAIL | 原 `key not found` 已由批准修复解决；原 ID 确认随后停在 `sign-carrier-not-ready`，尚未调用广播后端 |
| RGBBlindUDA | FAIL | 原 armored 导入报错已由批准修复解决；恢复钱包直接准备转账报 `insufficient RGB11 balance`；后续 blind/UDA 尚未完成 |
| RGBDirectOffline | FAIL | 已定位为测试未等待重新联网的账户同步；原状态在 SDK 返回可写后刷新成功。已补等待条件，完整旅程尚待回归，未改 Direct 业务 |
| RGBProxy | FAIL | 发送方重启后继续原转账失败；正在用原 sender/receiver、原代理端口定向定位 |

首次全组 `/private/tmp/sdk-ui-complete/run-1/`：资金准备 PASS（67.68 秒）；Go 包 345.088 秒，退出码 1，8 个叶子去重后 4 PASS / 4 FAIL / 0 SKIP。接口调用不能等同于旅程通过。

批准修复后的原现场 `/private/tmp/sdk-ui-complete/regression-2/`：生产 WASM 构建通过；两个叶子后续均失败，包 84.039 秒。调用证据确认 `prepareRGB11Consignment` 已返回成功且回执非空、原文本合约 `importRGB11Contract` 已返回成功，错误证明/错误合约仍被拒绝。原状态首次回归 `regression-1` 因测试误用显示 ticker 定位资产而中止；已改为原转账的 asset ID，未修改生产业务。

当时提出的三个新 RGB 入口增加全量状态核对方案未采用；用户随后要求回到本地状态及时落库和重启仅检查自有 UTXO，当前已按此原则完成重构。定向观察类诊断不计入 PASS。未运行 TestAll、老模块单元测试或暂停中的 PWA 页面验收。


后续定向诊断（不计 E2E PASS）：`diagnostic-5` 以仅增加错误日志的临时 Go overlay 复用原 Direct 钱包状态；刚上线三次刷新均未获得账户写入许可，等 `GetAccountView().Modules` 明确返回 RGB 可写后，原 `RefreshRGBState` 返回成功。日志记录 `grant-success`，确认不是 RGB 证明或 UTXO 验证失败。正常 E2E 已补“等待恢复账户同步”条件，未放宽余额或广播断言。

`diagnostic-3` 的原代理现场未能验证广播：重启临时字节转运服务后丢失了原 relay 对象，钱包因原状态为 relayed/broadcast-attempted 不会重新上传；这属于诊断环境缺失，不能当成 SDK 广播根因。已从原 sender 待办提取同一份 SDK 证明，留待原 receiver 真实重建 ACK，并为后续失败保存 relay 对象；没有伪造 ACK。手工原现场已独立确认 `sign-carrier-not-ready`。批量 invoice 场景已加入 RGBManual，尚未执行到该阶段。
