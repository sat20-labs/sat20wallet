# SDK / RGB11 统一验收阶段记录（2026-10-28）

本记录对应四仓未提交共享工作树。输入指纹见 `../data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/input-manifest-v9.json`（SHA-256 `749b43b6a1d66aaf1f41a3a4fd173791567e4769c594c7507f987c310555fc2c`）。旧输入清单和全部失败日志保留，不把分批通过当成全量通过。SDK 的 `prepareManagedWrite` 临时变动已撤回；生产写会话和离线门槛没有因此放宽。

已取得真实退出码的结果：

| 批次 | 结果 | 收据 |
| --- | --- | --- |
| SDK wallet 八组定向普通测试 | 28 个叶子通过，exit 0 | `sdk-wallet-targeted-v8-20261028.result.json` |
| SDK viewer/ACK/marker race | 16 个叶子通过，exit 0 | `sdk-viewer-ack-race-v8-20261028.result.json` |
| SDK RGB 冷恢复/IFA race | 12 个叶子通过、双资产 scope 用例发现 1 处 `_chain` 全局变量竞争，exit 1 | `sdk-rgb-recovery-race-v8-20261028.result.json` |
| SatoshiNet RGB 公共技术登记 | 2 个叶子通过，exit 0 | `satoshinet-rgb-registration-v8-20261028.result.json` |
| transcend 代表场景（SDK 门槛保持原样，夹具先 Start 再 Bind） | 通过，exit 0 | `transcend-rgb-bind-fixture-v15-20261028.result.json` |
| transcend swap/DAO/嵌套 launchpool | 三条通过，exit 0 | `transcend-rgb-three-paths-v16-20261028.result.json` |
| transcend 六场景原选择器 | 六个 RGB 子场景通过；额外独立顶层 launchpool 激活快照竞争失败，命令 exit 1 | `transcend-rgb-contract-six-v17-20261028.result.json` |

上述收据和 JSON 日志均位于 `../data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/`，记录原命令、耗时、Go 退出码、日志哈希和部分源码哈希。v17 使用 `go test -json -count=1 -timeout=12m ./stp -run '^(TestContractRGB11|TestContractRGB11LaunchPool)$'`，耗时 204.406 秒。六个嵌套场景是 transcend、AMM、swap、DAO、faucet、launchpool；顶层 launchpool 是同一选择器额外执行的第七个叶子。

绑定初始失败来自共享测试夹具的 `StopDKVSSync → Initialize → Bind → Start` 顺序。已将 Start 移到 Bind 前，代表场景在原 SDK 写门槛下通过。公共登记后续失败由测试夹具在部署前缺少技术登记引起；现用原 Core 业务路由的 fake L2 对公开合同、签名权限和读回进行验证，transcend 服务端复用 SDK 已配置的 DKVS client。swap/DAO 曾与夹具挖块重叠，现仅在发行准备窗口使用既有 `pauseAndDrain/resume`，仍保留真实 tip/UTXO 快照保护。launchpool 准备结果的 hex ticker 在公开导出前须转换为规范 `rgb:...` ContractID。

未闭合：顶层 launchpool 在 v17 的账户激活报 `account management snapshot changed`，嵌套入口同轮通过。夹具在激活前停止 worker 的两次试探分别于 repository 写和激活时触发同步门槛，均已撤回。最新夹具在 Bind 后增加一次显式 `SyncAccountManagementState`，但其单场景 v20 工具调用于 12.8 秒被中止，只有 `run` 日志，没有测试退出码或结果收据；精确进程查询受沙箱拒绝，此修改仍未验收。不得把 v20 当作通过。另有 `_chain` race、完整 `TestRunAll_contract`、SDK account/rgb11 更广回归、整组 wallet race、WASM/PWA/市场构建和浏览器 E2E 未完成。短正式名的协议键变更仍独立等待定案，未计入本轮通过。

所有命令使用原仓目录与默认 Go 缓存，测试在临时 DB 和 fake L1/L2 上运行；未提交、推送、上传、部署或访问真实钱包 DB。其他会话的 dirty 仍保留。恢复测试前应先确认 v20 中止进程的状态，且要使用 v9 输入清单或更新后的新清单核对四仓源文件漂移。

## 后续定向验收（Mac 本地 2026-10-29）

以上 v9/v17 状态是历史记录，不能代替后续源码的验收。v11 输入下，SDK `./account ./wallet/rgb11` 共 139 个叶子通过；transcend 六个 RGB 子场景、独立顶层 launchpool 和完整 `TestRunAll_contract` 均取得 exit 0。完整合约聚合耗时约 1168 秒、17 个叶子通过，收据为 `transcend-all-contract-v11-approved-20261028.result.json`。SDK WASM 编译、PWA TypeScript/Vite 编译及市场 TypeScript 检查也分别通过；PWA Vite 使用既有 WASM，未重新生成版本和完整性元数据，不能视为发布包验收。

SDK `./wallet` 完整包 v9 仍为 exit 1：218 个叶子通过、24 个失败、2 个跳过，并由 nil-DB 状态路径的 panic 中止；用例发现清单中另有 998 个顶层测试未开始。nil-DB 守卫的单项回归随后经正常审批路径真实通过。旧日志和 `sdk-wallet-full-v9-20261028.summary.json` 保留；尚未以最新源码重跑完整包。

本轮将几组旧测试接到真实签名 Core 绑定及已验证 state/blob 基线，保留原生产写门槛；定向通过的收据包括 `sdk-managed-session-three-v12-20261028.result.json`、`sdk-account-selection-session-v13-20261028.result.json`、`sdk-reconfiguration-session-v18-20261029.result.json`。同键并发创建钱包现在验证显式冲突、两端本地/远端数据不丢，顺序创建另测成功，两项通过，见 `sdk-wallet-creation-concurrency-v20-20261029.result.json`。RGB 写入故障注入已移至真实接收请求与 dirty generation 的原子 batch；注入失败后 profile 和 RGB 记录均回滚、锁释放，单项通过，见 `sdk-rgb-atomic-failure-v23-20261029.result.json`。这些是局部验收，不构成完整 wallet 通过。

`TestAccountTTLCommittedLostACKColdRetry` 仍失败，诊断收据为 `sdk-ttl-lost-ack-profile-v16-20261029.result.json`。原三记录请求在 ACK 丢失前已写入远端；冷启动以**相同 RequestID** 重放，远端成功响应。随后出现新 RequestID 的单条 account-mapping binding 请求，远端也成功响应。最终本地返回 `dkvs write conflict`，当时 outbox 已空，profile 的 state/data 与远端逐项匹配。冲突不能归因于原请求被服务端拒绝；具体本地返回点尚待定位，不得放宽 conflict 断言或猜改 ACK 协议。

补充只读策略诊断见 `sdk-ttl-lost-ack-baseline-v27-20261029.result.json`：在首次冲突后，本地 profile 的 `RecordTTL=144`，远端已提交的 state/data 记录策略均与该 profile 不匹配，但内容 hash/envelope 匹配，`requireAccountManagedBaselineLocked` 返回 nil。初次重放与新的 account-mapping 请求仍均返回成功。这是租期策略协调差异的确证，尚非冲突返回点的最终证明；生产 ACK 逻辑未改。

`TestAccountActiveMailboxReadRejectsChangedCatalog` 仍未闭合。探索性夹具尝试曾发现：原测试的 config 阻塞可能被后台请求先消耗，且在真实已订阅缓存下，active import 未必经 config 网络路径。探索改动已撤回，失败日志与收据保留为 `sdk-active-mailbox-baseline-v26-20261029.*`；后续须在实际 active-import 读取点设置门禁，确保编辑发生于捕获快照之后，并维持在线写会话。其余 paid-activation、容量、remote-import/rebase 旧失败仍未回归。

仍需在当前四仓输入重新冻结后，完成完整 `./wallet`、相关 `-race`、公共登记及浏览器 fake-L1 E2E。此前 race 中 `_chain` 全局变量竞争尚未闭合；不要以分组通过代替整批 race。短正式名的链上身份接线属于独立待定范围，不计入本轮通过。


## MCP 亲自继续验收（2026-10-29 UTC）

执行机器是用户 Mac，经本地文件 MCP 操作；未创建或委派其他 agent。四仓 HEAD 和 v27 的七项文件指纹曾核对一致，不代表其他 dirty 文件无变动。

- TTL 丢 ACK：原失败经 MCP 真实复现，见 `ttl-mcp-red-20261029.json`。仅测试探针显示下一次同步成功（`ttl-mcp-next-sync-diagnostic-20261029.json`），探针已撤回，原断言保留。因此旧“TTL 策略差异就是根因”的推断不成立。
- 新确定性回归 `TestAccountOwnACKDuringSnapshotCaptureDoesNotConflict`：空 provider 门禁在捕获本地快照后暂停导出，再启动后台重放原请求。没有 TTL 变化、没有第三端写入；自己的 ACK 把 generation 2 推进为 3、pending 1 清为 0。旧实现仍在前台返回冲突；下一次新快照同步成功且远端三键 hash 未变。真实红例见 `own-ack-snapshot-mcp-red-20261029.json`。
- 用户在本轮批准最小生产修复：`syncAccountManagementStateMode` 在冲突分类前检查已捕获快照版本，变化时复用既有有界重试。没有修改 ACK 协议、TTL 策略或真实冲突拒绝规则。上述确定性回归与原 TTL 冷重试均 exit 0，收据分别为对应 Test 名 + `-mcp-green-20261029.json`。相关 race 和更广回归仍待执行。
- 付费激活测试只修 fixture：CAS/BindingCAS 使用真实已同步基线和同一 endpoint；首次 WaitReady 明确拒绝业务写且 profile/generation 不变；选择测试检查 SwitchAccount 返回值和实际切换。两组定向通过。随后四个付费激活测试合并 `-race` 真实 exit 0（66.999 秒），完整 JSON 日志 `paid-activation-mcp-race-20261029.jsonl`，SHA-256 `09ab988b3ed937c3462b3f8dd1975913f35c4111d8b8a641a21ec9ae9f0e243c`。
- 此前一次直接 MCP race 调用未取回结果，明确不计通过；只读进程检查确认无匹配 launcher/test binary 后才开始上述带持久日志的批次。没有停止其他进程。
- 本轮新增脚本只做精确测试进程只读查询、固定回归命令与日志保存；未提交或推送。完整 wallet、其他 fixture、_chain race、PWA 浏览器验收仍未完成。


### MCP 扩展回归进度（17:35 UTC）
- 自身 ACK 修复扩大 race：首轮暴露测试在后台读取时替换 manager.db；改为 fixture 初始化前安装故障包装。v2 整批 exit 0、139.789 秒，包含真实并发冲突拒绝、顺序创建、ACK N 后新修改保留、TTL/no-op 与新增确定性快照场景。日志 `own-ack-mcp-race-v2-20261029.jsonl`，exit 文件为 0。MCP 长调用曾超时，但原进程继续写日志；未重复启动，读取落盘终态确认。
- 容量 fixture 改用真实 NIA 发行、相同账户范围、签名公共登记和 fake Bitcoin 证据，保留全部容量及本地 proof/远端不变断言，定向 exit 0。
- 旧自动合并恢复测试改为 clean target 的热/冷中断续传与多 provider 续传；真实并发冲突仍有单独通过测试覆盖。五类 marker 篡改继续全部拒绝，ciphertext 注入改在当前 clean import 实际恢复的 profile envelope，避免对不存在 replay bytes 越界。定向通过。
- 首次 MCP 完整 wallet v1 跑到 DKVS 测试阶段，194.591 秒 exit 1，又暴露旧手工构造 dkvsManager 未初始化 jobDue 导致 panic，以及剩余 fixture 问题。完整原日志 `wallet-full-mcp-v1-20261029.jsonl` 保留，未计通过。
- 密码、冷解锁、guardian 测试先通过真实 Sync 建立在线基线；初次绑定尚未完成时本地写应及时拒绝，不得阻塞；存储授权选择测试补成功切换断言。延迟 job 测试改用生产 constructor，未改生产门禁。7 项定向批次 `session-fixtures-mcp-v1-20261029.jsonl` exit 0。
- mailbox 并发测试门禁从无关 config HTTP 改到实际本地 mailbox 读取（capture 之后），用同步好的 fixture 验证目录变化仍拒绝旧快照，定向通过。
- RGB freshness fixture 对网络失联/远端更新先实际接收同步，再验证业务拒绝；profile/endpoint 改变注入真实读取阶段。不要求每个 RGB 业务都额外访问备份网络。四场景定向通过。公共依赖测试补自己的 testnet 上下文。
- 整体仍未完成：继续冻结新输入重跑完整 wallet，补相关 race/_chain，最后 PWA fake-L1 浏览器验收及 review；不提交。


### 后半段覆盖与依赖阻塞（17:53 UTC）
- 完整 wallet v2：344.231 秒 exit 1。已越过之前 DKVS job nil-map panic，仍在 legacy channel 读取处受全局网络配置污染而 panic，后续覆盖未完成。详见 `wallet-full-mcp-v2-summary-20261029.json`；不能把聚合 pass 事件数视为唯一叶子数。
- Legacy Gob 单独运行 exit 0；固定旧生成器使用 mainnet bootstrap，而前序 NewManager 将 indexer.CHAIN 留为 testnet。只在 fixture 读取时设置生成器的 mainnet/非 testing 环境，并清理恢复，不改旧二进制或生产通道逻辑。与 RGB/身份批次合跑后两个 legacy 叶子通过。
- RGB 局部提交测试遗漏公开注册前置：补真实发行合约的签名公共登记及恢复端测试 authority。连续刷新中本地提交、两种原子失败/重启、压缩拒绝后恢复均在 `rgb-local-mcp-v1-20261029.jsonl` 通过；两个 ACK 门禁场景仍在等待 publication 处超时。原因是测试只等 5 秒，而生产合并窗口为 10 秒。已改为策略常量 quiet + 5 秒边界，尚未复测。
- 两个身份快照测试补真实同步基线并检查 SwitchAccount 返回，定向批次通过。RGB 双 scope 测试将 peer 构造移动到 target worker 启动前，避免 NewManager 重写进程全局链参数与运行中 worker 读竞争，此调整尚未 race 复测。
- 仍待逐项核定：旧 DKVS outbox 冲突删除预期与保留冲突意图的新设计不符；独立 key pending 被错误当作全局离线；重启/URL alias 时先探测再离线缓存的状态断言过时；generation/endpoint 拒绝的具体错误和状态断言；这些不能简单放宽，须分别保留保护性断言并复跑。
- 新阻塞：`rgb-local-mcp-v2-20261029` 在构建前退出，Go 提示更新 go.mod。SDK go.mod/go.sum 无变更；本地 indexer 新增 `github.com/sat20-labs/xaplib v0.0.0` 和它自己的 `replace ../xaplib`，提高 cbor 等版本。SDK 主模块不会继承该 replace。xaplib 当前依赖 gnark-crypto v0.19.0，而 SDK 原图为 v0.18.1，需检查实际模块图影响。
- 已请求用户批准仅 SDK 依赖对齐，未执行 tidy、未改变 SDK go.mod/go.sum、未回退 indexer。等待确认期间没有新测试运行。代码/日志均在用户 Mac 原项目；没有新 agent、提交、push、外部源码上传或生产服务重启。


### 依赖阻塞解除及旧断言校正（2026-10-30 07:55 UTC+8）
- 其他 XAP 工作将 indexer 的依赖改为无第三方依赖的 xaplib/chainindex 子模块，恢复 indexer 原第三方版本。亲自重新读取用户 Mac：indexer/go.mod SHA256 43f9e92a5df665faf653044f6583727feaca5c82a5a2671a738a4b6b4de2da1f；SDK go.mod 仍为 7bda44b699d4e0758da54773c72cec8e2429b4e7f17821f8364e5b9f0f4b4ea3。本轮未改 SDK 依赖，之前依赖对齐提案暂不执行。
- 原 RGB ACK 单例实跑 exit 0；整个 RGB 本地提交/恢复、身份快照和 legacy fixture 批次 rgb-local-mcp-v3-20261030.jsonl exit 0，54.617 秒。两个 quiet 窗口场景已通过，不再只是待复测。
- 五个测试文件只校正已批准设计下的旧断言，未改生产规则：冲突 outbox 保留原签名与 mutation，仍能收到远端后续更新；多目录同步计入一致性 metadata 检查；倒退 generation 拒绝并保持基线；不批准的 endpoint 撤销在线资格但不换本地数据；显式 CAS 重建成功仍保留原冲突请求。
- 冷启动/URL 切换先尝试同步，失败后本地缓存可读且不得写；后续缓存读不重复联网。已过期并在首次读取时清理的记录不得被延迟 Watch 复活。恢复中的未决 key 禁止新意图，而已在线验证的独立 key 可写；实际提交链路离线则禁止账户改名/密码修改并保持 profile 原值。
- 上述九项顶层测试批次 dkvs-boundaries-mcp-v2-20261030.jsonl exit 0，8.364 秒；原保护性断言保留或增强。全部仍是 SDK 进程内测试，不等于 PWA 浏览器通过。准备继续完整 wallet v3 与剩余 race。


### e2e 主模块依赖仍需最小对齐（07:58 UTC+8）
- 前述恢复仅对 wallet 包成立，不能外推 e2e：只读预编译 `go test -mod=readonly -run '^$' ./e2e` 实际 exit 1，报 indexer/base/xap_raw.go 缺少 xaplib/chainindex 模块 go.sum 条目。完整日志 `e2e-build-mcp-v1-20261030.log`；没有运行任何 e2e 业务。
- 子模块 go.mod 只有 module/go 两行，SHA256 ed67ef7e89eee87c49876fd16dfa2b106abb862f257f5c254756054b19677815，无第三方依赖。已向用户说明并请求将 SDK 依赖方案缩小为同级 chainindex 子模块映射，不升级密码学库。尚未获得此项答复，SDK go.mod/go.sum diff 仍空。
- 完整 wallet v3 已开始并持续写日志；MCP 请求超时不代表测试停止，未重复启动。其他不受影响的 wallet 与 RGB race 可继续完成。


### wallet v3 部分覆盖（08:13 UTC+8；09:08 更正终止原因）
- wallet-full-mcp-v3-20261030.jsonl / exit 真实终态：exit 1，740.424 秒，已启动的968个顶层项目均有终态，但随后确认在 R1 receipt expansion 发生 nil-pointer panic，未覆盖完整清单。已启动部分的1758个唯一叶子：1684 pass、59 fail、15 skip。叶子计算排除所有多层父节点，不能把父级 fail 再计入。完整统计已落 wallet-full-mcp-v3-summary-20261030.json。
- 此次越过 legacy panic 后首次覆盖后半段，暴露剩余账户/RGB fixture 或旧同步设计断言；尚未判定所有失败根因，不得声明整体通过。
- 后续定向：warm unlock 的后台 mailbox 用已在线同步的同一 client/endpoint 建立真实 config 门禁；public refresh 用真实 RGB 发行/公开登记/导入恢复基线，并在 Bitcoin GetTip 证据读取点阻塞。四个密码正确/错误场景定向 exit 0，原本地验证延迟与身份不变断言保留。queued 同账户选择场景尚待复跑。
- remote_action_rehydrate_test 原测试忽略 SwitchAccount(2) 的失败，实际保存 account 0 signer，却期望重启得到 account 2。新增真实账户管理同步前置并检查 SwitchAccount 错误及实际子账户。正向重启 signer 用例定向 exit 0；mismatch 用例待复跑。
- RGB scope/race 批次已启动（rgb-scope-mcp-race-v1-20261030），结果尚未取得。新增变更仍仅测试 fixture；生产修改仍限用户批准的快照版本检查。e2e 的 chainindex 最小依赖调整继续等待用户确认。


### 分组闭合（08:28 UTC+8）
- RGB scope + 本地提交恢复批次 -race 真实 exit 0，203.116 秒，无 race report，包含 TestRGB11AccountRecoveryTwoAssetScopesAndScopeBinding；原 _chain 构造时序竞争在此覆盖下已闭合。日志 rgb-scope-mcp-race-v1-20261030.jsonl。
- wallet-late-fixtures-mcp-v2-20261030：16 项顶层选择，45.860 秒 exit 0。包含 warm unlock 真实 Bitcoin/邮件读取门禁、同账户切换与解锁、正/反 signer 重启、关闭 reservation、4 类 atomic fault wrapper、账户冷解锁与首次 quiet-window 备份、并发账户 bundle 冲突保护。
- 对数据库包装器显式转发真实 NewAtomicWriteBatch，并保留原故障 batch 的 Put/Flush 注入；未以普通 batch 冒充原子事务。原故障必须触发且磁盘/内存原值不变的断言继续执行。
- 账户并发字段/不同 provider 编辑旧自动合并测试改为显式冲突：重复同步均拒绝，逐设备的持久目录/provider 数据、远端 state/data 字节不变，pending/dirty 不清除；不再要求单个备份 key 内自动字段合并。对应函数新名 TestAccountSubaccountConcurrentFieldsRequireReconciliation、TestReviewAccountConcurrentBundlesRequireReconciliation。
- 公共 generic send fixture 补真实发行资产的签名共享登记与接收端 authority，不影响单独 missing-registration 负例。rgb-registration-fixtures-mcp-v1-20261030 四项顶层选择 exit 0，31.290 秒，包含未签名过期取消、取消与本地操作并发、混合 ACK/NACK 四场景、冲突候选重启前脏标记恢复。
- rgb-async-fixtures-mcp-v1-20261030 八项顶层选择 exit 0，11.318 秒。备份网络等待不阻塞本地读；业务回调等待不锁 UI/只读接口；一次本地业务只推进一代并保留 dirty，随后明确 Sync 才清除；创建 invoice 不再等待后台备份 CAS，备份失败应保留本地接收请求、dirty 和原冲突/未确认 outbox，远端 blob 不变。
- network-lock audit 的两处旧未同步 fixture 已改为真实在线基线、公共 refresh 在实际 Bitcoin 证据点探测；扩大批次 v2 尚待运行。全 wallet v4 尚未跑，其他旧 baseline/active restore/private cancel 等失败仍待逐项核定，不计为通过。


### 冷恢复 generation=0 真实生产缺陷（08:45 UTC+8，待用户批准）
- TestRGB11R1WitnessBatchIndependentRequests 的 prepare=true/false 两场景，补齐“源端先完成后台备份，目标端再恢复”前置后，均在恢复后首次 Sync 失败，远端已提交且 ACK 已落盘。
- 定向诊断两例一致：本地 ManagedDataGeneration=0，StateSeq=3，ManagedDataRevision=2，pending=0，dirty=false，secret 已加载；outbox 恰一条 acknowledged，OriginDomain=account-managed，OriginGeneration=0，Namespace 相同。不是第三端并发写入或网络失败。
- 源码根因：account_management_state.go restoreAccountManagementStateOperation 的 profile 构造（约1015行）漏设 ManagedDataGeneration；同文件新建/激活两条构造已明确设为1。account_managed_ack.go finishAccountManagedACKLocked 正确拒绝 OriginGeneration==0，因此本端不能完成自己的 ACK。
- 已请求最小生产修复：冷恢复 profile 构造初始化 ManagedDataGeneration=1，与其余新 profile 路径一致；保留 ACK 校验，不做旧格式兼容。尚未修改生产代码，等待答复。
- 最近其他回归：rgb-async-fixtures-mcp-v4-20261030 整批 exit0（31.262秒），包括显式同步后再业务、真实离线禁止 mutation、ACK 前本地完整持久恢复材料和 dirty marker、metadata 检查次数、真实网络无锁审计。
- rgb-peer-backup-mcp-v1-20261030：过期 blind peer 清理和 zero→one receipt 扩展已通过，仅 witness 双请求恢复两场景因上述生产缺陷失败。rgb-final-boundaries-mcp-v1-20261030：两个 private cancel 和 atomic marker failure 通过；cold restored sender 的 snapshot-changed 及 CAS 冲突后旧自动重试预期仍待核定。


### 已闭合的分组与 v4 复跑（08:50 UTC+8）
- rgb-final-boundaries-mcp-v2-20261030 五项整批 exit0，12.973秒。冷恢复 settled sender 证据用例原先由测试本身同时启动后台 reconciliation 和显式 private Refresh，导致乐观快照失效；此材料恢复测试现在只由其显式步骤推进自己的临时 runtime，完整 settled 导出、接收方验证及余额40断言通过。未改生产 retry 或 snapshot guard。
- CAS 拒绝用例保留原 conflicting outbox，重复同步继续拒绝、原 request/mutations/authorization 不变，不再要求静默重新授权。atomic marker fault 发生在同一本地事务，断言 profile 与 RGB snapshot 同时不变，再执行成功重试及独立备份；未改生产事务代码。
- 新增 account_restore_generation_test.go 验证冷恢复建立有效本地 generation。初版诊断测试的 Sync 无需重写，实际红点仅为 generation仍0；收据 cold-restore-generation-red-mcp-20261030.json 保留，不能称它复现了 ACK 失败。最终测试名 TestAccountColdRestoreInitializesLocalGeneration，ACK 失败的完整行为证据仍为 R1 双请求两个场景（源已备份、目标 acknowledged outbox originGeneration=0）。
- 全 wallet v4 已启动，固定日志 wallet-full-mcp-v4-20261030.jsonl、exit、wallet-sources 与 dependencies 指纹。已知 generation=0 生产缺陷尚未修复，预计该测试及 R1 两场景仍失败；本次不跳过它们，继续检查整包是否存在额外回归。
- 当前仍待用户确认两项：SDK 接入同级 xaplib/chainindex 子模块以恢复 e2e 编译；冷恢复 profile 初始化 ManagedDataGeneration=1。均未执行。PWA 浏览器、最终完整 review 和提交仍未完成。


### v3 统计更正与 v4 完整清单核对（09:18 UTC+8）
- 前次依据已启动测试均有终态，错误判断 v3 无 panic/遍历完毕。已直接搜索完整 JSON 日志确认 TestRGB11R1ReceiptExpansionPeerSync 的 nil-pointer panic/SIGSEGV；已向用户更正。以后终态检查同时包含 panic、包终态及独立 test inventory，不能只看 run/pass/fail 的闭合。
- v4 实际无 panic，941.754 秒 exit1，1131个顶层项目（1128个 Test 加3个 Example），2131唯一叶子：2087 pass、16 fail、28 skip。已用 `go test -mod=readonly -list '^Test' ./wallet` 对照，1128个 Test 无遗漏；清单收据 wallet-full-mcp-v4-inventory-20261030.json 的“removed_or_renamed”三项实际是额外 Example，不是删改测试。
- v4 包尾新增可见失败仍有旧 fixture：4个账户业务操作未先同步；3个 atomic DB wrapper 未转发真实能力；2个 public snapshot 错误字符串仍要求旧 backup-baseline 前缀；4个由临时 fixture 自身后台 reconciliation 与显式 Refresh 竞争的快照变化。已做最小测试前置/包装修正，保留正确密码、根钱包删除保护、子账户实际身份、原子写次数、受损资产不可用、总余额/磁盘不变及真实网络无锁断言；wallet-tail-fixtures-mcp-v1-20261030 已开始，尚未取得结果。
- 其余3个叶子是同一已定位生产缺陷：cold restore generation=0 的新回归，以及 R1 witness prepare 两场景。仍未获批准修改初始化。
- SDK account/...、wallet/dkvs/...、wallet/rgb11/... 全部 -race 分组命令 exit0，日志 account-rgb-dkvs-packages-race-mcp-v1-20261030.jsonl。这不是完整 wallet -race 或 PWA 浏览器通过。


### 当前检查点（09:24 UTC+8）
- v4 inventory 已重新包含 Test 和 Example：expected=1131、started=1131、missing=[]、removed_or_renamed=[]。原只列 Test 而显示3个 Example为多余项的统计描述已被此精确核对替代。
- wallet-tail-fixtures-mcp-v1-20261030 整批 exit0，45.090秒（日志 SHA256 36d4cd5131b146db7f13a10721c4a1e451a09754da1b712b3d9998e7f911d02b）。v4 中除 generation=0 三个叶子以外的13个失败均在此定向组闭合；不宣称新整包通过。相同11个顶层用例（含受控公共 resume/refresh 竞争控制）-race v2 正在执行。
- account/rgb11/dkvs 五个包（包含 account/fuzzy 与 internal/shamir）-race 均真实 pass，无race报告；日志 SHA256 c2b403ab22b7c18b3d66ae2f0aff787d8620365621a3d5a04a344ddd7472b04e。
- PWA 测试源码预检查发现 usage 场景仍名为“offline catalog edits survive reload and converge with another device”，且实际期待断网改名/增加子账户/删除钱包、多端自动合并；与已确定离线只读和显式冲突原则不符。只读记录此缺口，尚未改 PWA 测试，更未把未执行浏览器用例计为通过。
- 当前没有提交、stage、push、上传源码或修改底层 DKVS。生产变更仍只有已批准的自身 ACK 旧快照检查。等待两项明确答复：冷恢复 generation 初始化；e2e 的 chainindex 主模块依赖映射。


### 用户批准两项最小修改并实施（09:28–09:37 UTC+8）
- 用户 Sentinel_8b26bbe05f6c8191898c1ae962ef5947 明确同意：冷恢复 generation 初始化及 SDK chainindex 主模块映射。已在 restoreAccountManagementStateOperation 的 profile 构造增加 ManagedDataGeneration:1；未降低 ACK 约束，未加兼容分支。
- SDK go.mod 只增加 github.com/sat20-labs/xaplib/chainindex v0.0.0 的 indirect require 与 ../../xaplib/chainindex replace。go.sum 无diff，其他第三方版本无变化。e2e-build-mcp-v2-20261030.log：`go test -mod=readonly -run '^$' ./e2e` exit0（只编译，不计浏览器通过）。
- 新冷恢复 generation 测试定向通过。rgb-peer-backup-mcp-v2 中 ACK 已能收尾，但目标端按链上证据规范化并发布后，旧端继续写前未同步，造成真正的两端基线冲突。fixture 改为严格交接：源完成备份→目标恢复并同步→返回源端前源先采用目标确认基线，不自动合并，不继续使用旧基线写入。
- rgb-peer-backup-mcp-v3-20261030 五项顶层选择真实exit0，29.944秒，包含先前失败的两种 witness 双请求恢复、新 generation 回归和自身 ACK 快照回归。生产修复与测试业务交接前置分别记录，不将后者冒称为额外生产修复。
- wallet-tail-fixtures-mcp-race-v2-20261030 整批exit0，346.186秒，无race报告；其输入不包含后批准的 generation 初始化，后续需复核该生产行关联回归。
- wallet-full-mcp-v5 已启动，日志/exit 使用独立v5文件，新增记录 indexer/satoshinet/transcend/chainindex 的 Go 源文件hash；没有覆盖旧收据。
- PWA offline usage 用例已改为等待真实网络失败、拒绝改名/加子账户/删除钱包、验证目录及 pending 不变、离线重载仍可读、重连后采用另一端正常更新。必跑清单名称同步更新，尚未执行浏览器或语法检查，不计通过。


### MCP 最终检查点及本地 Codex 接手（09:57 UTC+8）
- 完整 wallet v5 真实 exit0，1095.065秒。1131顶层项目、2131唯一叶子：2103 pass、0 fail、28 skip；无panic，无unfinished。独立 inventory expected=started=1131，missing=[]，无改名遗漏。日志 wallet-full-mcp-v5-20261030.jsonl 与 exit/summary/inventory 均已落盘。
- 28项 skip 保持真实原状且不计通过，包含诊断、live opt-in 和由 e2e 外层启动的 adapter。不能将 wallet 整包通过外推为未跑的浏览器、live 或外层 E2E 通过。
- PWA 两个 account runner 的 node --check 和限定 git diff --check 已通过；未启动浏览器。原项目发布用 WASM 未覆盖。
- 用户要求额度恢复后改回本地 Codex，且以后经常双向切换。已建立 sdk/docs/local-execution-switch.md，明确单一源码修改者、同一 Mac 原目录、docs/data 检查点、日志与exit终态、既有批准继承及本地/远程权限分离。
- 原 Mac yingfengdeMacBook-Pro.local 已再确认 connected/attached/authorized；原工程线程为 01a17883-8700-76c6-94fa-60ef1b25002c。主线程 MCP 此后停止源码写入，拟由原线程单独继续 PWA fake-L1 验收和最终review，不新建代码副本，不重复已完成v5。

### 本地 Codex 接手后的定向验证（10:06 UTC+8）
- 原本地线程已核对根 AGENTS、切换记录、当前 git：HEAD `fde00ec629f689839264bb328d0a08574d622ed0`，暂存区为空；保留现有全部 dirty。`account_management_state.go` 及两项关联回归源码的 SHA 与 wallet v5 固定输入一致，v5 `.exit` 实际为 0，没有重跑完整 `./wallet`。
- 关联 `-race` 首次命令在默认 Go 缓存读入阶段被沙箱拒绝，尚未执行测试；失败日志 `local-switch-account-fixes-race-v1-20261030.jsonl`，exit 1。经相同目录、相同默认缓存的正常审批后，第二次命令 `go test -mod=readonly -race -json -p=1 -parallel=1 -count=1 -timeout=15m -run '^(TestAccountOwnACKDuringSnapshotCaptureDoesNotConflict|TestAccountColdRestoreInitializesLocalGeneration|TestAccountTTLCommittedLostACKColdRetry)$' ./wallet` 退出 0，3 项均 pass，无 race 报告。日志 `local-switch-account-fixes-race-v2-20261030.jsonl`，SHA-256 `d1e9439593fbb4125c0e406dc6a25f758dd244a52de95337daa10115cac9a1f2`。
- `TestSDKAccountPWAConnectedBrowser` 已按 `pwa/package.json` 既有门禁启动，命令 `go test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=55m -run '^TestSDKAccountPWAConnectedBrowser$' ./e2e`；日志 `local-account-pwa-browser-v1-20261030.jsonl`，退出码尚待运行结束。临时测试 fixture 及 WASM 由 E2E 自行生成，浏览器结果未计通过。

### 首次账户浏览器 E2E 与测试前置修正（10:12 UTC+8）
- 首次账户浏览器 E2E `local-account-pwa-browser-v1-20261030.jsonl` exit 1，240.32 秒。嵌套 WASM/真实 IndexedDB 存储回归通过，首个钱包创建/身份场景通过；紧接的 `IndexedDB profile failure leaves updateWalletName unchanged` 未到达故障注入的 profile 写入 (`injected=false`)，故后续必跑场景标 `not-run`。没有把这次结果称作生产回滚缺陷。日志 SHA-256 `d6a323ddf629b1fc22d36582058518a0a4cc996564587ddf15878f868ef4ab91`。
- 源码路径：`wallet/account_management_state.go` 的 `prepareAccountMutationLocked` 在任何 profile 持久化前检查在线 DKVS 写资格；原浏览器测试在首次创建后立即注入本地 DB 故障，未先证实该资格。测试只增加对真实 `status.dkvs_write_allowed` 的有界等待，并在注入未触发时报告观察到的错误；原“注入后失败、磁盘/实时目录不变”的安全断言保留。`pwa/scripts/verify/account-management-e2e.mjs` 的 `node --check` 和限定 diff check 通过。v2 同一 E2E 已启动，结果待终态。

### 账户浏览器 E2E v2 的下一处前置（10:20 UTC+8）
- v2 `local-account-pwa-browser-v2-20261030.jsonl` exit 1；首个注错点已通过，累计30个浏览器场景pass、1 fail、35 not-run。失败场景 `locked remote deletion followed by offline unlock refreshes PWA identity from local SDK` 在一开始的 `createWallet` 收到 `DKVS managed path is not synchronized`，尚未进入远端删除断言。日志 SHA-256 `4cb837023984e8cc6a901dead63021eb31f0d7f8bb4b69cacc2b0682d37bcf56`。
- 紧邻前一场景故意把节点 API 断开并离线重载，最后仅移除了 Playwright 断线路由；这不会立即恢复 SDK 已撤销的在线写资格。测试在该场景结束处新增对真实 `status.dkvs_write_allowed` 的有界等待，成功才允许后续创建钱包；仍要求离线时只读，未加固定睡眠或生产门禁例外。`node --check` 和限定 diff check 已通过，v3 待运行终态。

### 本地账户浏览器 v3 与发布升级范围（10:50 UTC+8）
- v3 已结束，`local-account-pwa-browser-v3-20261030.jsonl` exit 1，1755.67 秒。运行器 `caseSummary` 为 66 必跑、54 pass、12 fail、0 not-run；Go 包终态 fail，无 panic 记录。日志 SHA-256 `b0cace6420fb8064c60a290916a29b9f570cc286e0276553a7e7967c9a8e75ca`。先前单行解析误记成 11 fail/1 running：后台状态刷新用例的 fail JSON 被 Go 输出拆行，但原运行器明确计为 fail。12 项集中在 usage 后半段，常见 `DKVS managed path is not synchronized`；后台状态刷新还有独立的提示未消失问题。基础账户场景前 31 项已通过，不能用它们替代 usage 全部验收。
- `account-management-usage-e2e.mjs` 的 `fresh()` 创建钱包后立即返回；SDK 写入前要求真实在线写资格。PWA 账户设置页显示 `dkvs_write_allowed`，但创建子账户等入口尚未据此禁用或解释同步中状态。12 项失败的逐项分组、UI 最小方案及后台状态独立诊断见 `sdk/docs/pwa-write-readiness-review-20261030.md`。测试 fixture 可以等待真实前置资格；真实用户页面还需验证写入口门禁与友好错误，不能靠测试等待掩盖。生产修复遵守根 `AGENTS.md` 先给根因与最小方案审核。
- 发布门禁及历史通道/通道合约升级覆盖矩阵记录在 `sdk/docs/release-upgrade-gate-20261030.md`。新 RGB、DKVS、账户管理不添加旧格式兼容；已有通道/通道合约的旧数据升级需独立证据。`transcend/stp.TestAll` 在隔离临时数据库 exit 1，77.281 秒，停于 `runAll_wallet` 的 `TestCoreNode` 质押后首次开通道；通道组与合约组未执行。日志与输入在 `transcend/data/sdk-acceptance-20261030/transcend-testall-v1-20261030.*`，未把编译或启动计作通过。

### 互斥候选的本地续验（2026-10-30）
- 用户明确允许 SDK/PWA/transcend 本地验收后，原目录、默认 Go 缓存的 `go test -mod=readonly -run '^$' ./stp` 复核获准并 exit 0；这只是编译。最新 SDK 的 `TestStakeAddressGate`、`TestChannelStakeGate` 定向 2/2 pass。
- 隔离 `transcend/stp.TestAll` 最新轮 exit 1，先在 `TestSendRGB11` 的恢复激活报 `account management snapshot changed`，尚未进入 `TestCoreNode`。未重试整套。为定向运行已批准的 `TestCoreNode`，仅将其测试数据库切到 `t.TempDir()`，避免触碰固定共享目录；异址新通道已达 READY，但原测试仍在后续合约部署步骤得到 `only bootstrap node can handle this requirement`。当前完整 `TestCoreNode` 未通过；失败后的后台发块未停，仅中断本轮自己的测试子进程。
- 上述失败分别属于账户快照边界和旧 TestCoreNode 部署步骤与异址正例的冲突；没有据此改账户生产规则或删业务断言。详见 `transcend/data/sdk-acceptance-20261030/stake-guard-acceptance-20261030.json` 与对应完整日志。`TestStakeToBeMiner` 和完整 transcend `TestAll` 最新通过证据仍缺，不能进入发布通过结论。

### 本地 checkpoint 最小修复与账户 PWA v7（2026-10-30 10:39 UTC）
- 用户确认 RGB L1 monitor checkpoint 只需设备本地保存后，`rgb11/store.go:261-271` 的 `SaveLocalMetadata` 改为 scoped 单键原子本地写，不再误触发备份 dirty/generation；真正 RGB 恢复材料保留原 reservation 原子批次。红例 `rgb-local-meta-red-v1-20261030.jsonl` 证明旧实现仅 metadata 变化也使激活快照失效；绿例 `rgb-local-meta-green-v2-20261030.jsonl` 与 `rgb-local-meta-race-v1-20261030.jsonl` 均 exit 0，覆盖本地游标不增代际、真实材料仍拒绝旧快照、scope/重启/写失败。此为定向 SDK 验证，不替代 wallet 整包重跑。
- 个人 DKVS 的账户/RGB 内容及实际恢复读取方见 `sdk/docs/account-rgb-backup-scope-20261030.md`。该清单不授权删本地运行数据或裁掉未知恢复材料。
- 账户浏览器组 v7 使用当前临时 Core/Miner/Bootstrap、当次钱包 WASM、真实 IndexedDB 和受控 fake-L1。`account-pwa-group-gate-v7-20261030.jsonl` 的 Go exit 1，2007.342 秒；66 个页面 case 全有终态：63 pass、3 fail、0 unfinished。失败为恢复后清空 DID 仍显示 `bob.btc`、旧离线修改/远端删除场景在 SDK 写资格处拒绝、已批准离线只读用例将省略的零值 `pending_changes` 与数字 0 直接比较。完整名称、输入 SHA 和日志 SHA 见同前缀 `.receipt.json`；六组源码/依赖 pre3 与 post3 指纹完全相等，无 `router copy.go`，不能把失败归因于运行中源码漂移。
- v7 后只对第三项的两处断言补 `?? 0`，保持“无 pending”的语义；`node --check` 与限定 diff check 通过，**没有**复跑浏览器。前两项未修改，需按既定测试 case 沟通范围及产品写资格要求决定最小调整。SDK/PWA、transcend 其他验收门禁仍未全绿，不进入发布通过结论。

### 已批准的四项测试修正，待定向浏览器验收（2026-10-30）
- 用户批准恢复后 DID 清空先取得首次同步写资格、remote deletion 的离线拒写与重连远端采纳、`pending_changes` 省略零值按 0、POS heartbeat 真实 funding fixture。DID 与 remote deletion 仍保留重载/冷恢复、无本地脏数据、远端删除及身份断言；新 remote case 名称已同步必跑清单和 `TestPWAAccountRecovery`。
- POS 仅 heartbeat 改为原 `FundAddress` 生成有 raw tx 的 mature funding；质押 seed、提现金额、fee 与确认断言未改。当前源码 SHA-256：`pwa/scripts/verify/account-management-e2e.mjs` `870de0a3b3b3022b8b3fd64ee830852b51677e309161cd163f9d7df39a6cf19b`；`pwa/scripts/verify/account-management-usage-e2e.mjs` `712f99608a409781a6f93b316021ce50dbf94976568e357281da713910f068f8`；`sdk/e2e/pos_pwa_browser_e2e_test.go` `5cd3400b4f9772707f9b4ebba37c9a7f0868191fcecaa72ce0ac8251d8b591b1`；`sdk/e2e/account_management_browser_e2e_test.go` `8a5b8382ce3df8219b3811b5ee96210f86003871780a8d91f33e9976a42f177d`。两个 JS `node --check`、Go `gofmt -d` 和限定 `git diff --check` 均 exit 0；本轮未启动浏览器、Go 测试或构建，不将静态检查计作通过。

### POS heartbeat BRC20 offset 与账户远端夹具修正（2026-10-30）
- 上节 heartbeat 真实 funding 首次定向运行在浏览器启动前被 fake indexer 拒绝：`seed BRC20 requires one inscription offset`。已给 `FundAddress` 的 heartbeat asset 恢复原 `seedAnchor` 的首 sat `Offsets=[0,1)` 和 `OffsetToAmts` 金额；其他 stake seed 不变。`sdk/e2e/pos_pwa_browser_e2e_test.go` 当前 SHA-256 `a2294bb551290abc6ce1331eaeaccaf6ff38ff3f1b8e3f30fbb5dbd404be4feb`，尚未复跑。
- 用户同意仅对远端同步/恢复测试共用夹具做真实激活和基线。`reviewAccountDevices` 与 `managedImportTestSource` 原在 `InitializeAccountManagement` 后立即 `SyncAccountManagementState`，而未激活的 `RecoveryConfigured=false` 被该 API 明确拒绝。现复用 `rereviewRecoveryPackage` 和 `ActivateAccountManagement`，配置同一真实 CoreNode 身份，再做原同步；本地-only case 未改。`reviewAccountDevices` 新增激活秘密使用独立变量并在 helper 返回时清零，避免与后续第二设备所用秘密重声明。首次定向复跑 `TestAccountCatalogCommitFailureIsAtomic` 的 12 个叶子全部通过；`TestManagedDataImportFailureCannotOverwriteServer` 进入真实激活后发现 `_chain` 为空，root wrapper 拒绝空网络。`managedImportTestSource` 现仅在调用方未设网络时临时使用 `testnet`，由 `t.Cleanup` 恢复原值。两文件 SHA-256 分别为 `44c3e97bddad394f54852437e9b57f5f8354e0efedc2071c57767ea69a59787d` 与 `ef708cc76a5470c3d3a04709a0c45d7213bd60d611d7b6903301f45534adc2fa`。`gofmt -d`、限定 `git diff --check` 静态通过；后一夹具修后的结果待 Luna 复跑。wallet 全包 v1 的 82 个失败叶子中，45 个顶层、77 个叶子含相同 DKVS 未同步签名，其余 5 个叶子不能混判；不能按公共 helper 覆盖范围推算已通过数量。

### POS 其余预置 Anchor 资金完整登记（2026-10-30）
- POS v3 定向组真实 exit 1，19 个浏览器 case 通过、1 失败、7 未运行。public BTC withdrawal 的提交调用因 `tx not found: dbf521d201acc75061199724abb4a3916bffa601fbf42514e81c11e60e5f33ba` 失败；该 ID 是旧 `core-stake` 合成 funding outpoint。它发生在提现前的 UTXO 来源分类读取父交易，不能据此断言生产提现已花费质押资金。
- 同一已批准的真实 serialized funding 夹具修正现覆盖 `core-stake`、`miner-stake` 和既有 heartbeat。前两者在节点启动前经 `FundAddress` 登记真实 raw tx 和相应 outpoint，保留原资产、数量、binding=1、BRC20 inscription offset、P2WSH 脚本及 Anchor 签名/确认断言。旧合成 outpoint 无其他独立引用。`sdk/e2e/pos_pwa_browser_e2e_test.go` 新 SHA-256：`6111ba9345975a344bb4fb72c24f4350a027c28248c97afee86c77bfe92cdb79`；gofmt 与限定 diff check 通过。此新候选**尚未执行浏览器测试**，不得计作通过。

### 远端账户测试夹具真实激活补齐（2026-10-30）
- 已批准的远端同步/恢复夹具修正扩至 `establishAccountManagedTestWriteSession`：未配置且无待完成激活的会话先创建真实恢复包并激活；已配置会话不重复激活，待完成激活不伪装成新账户。没有改动基础 `newAccountManagementAutoTestManager` 的 local-only 默认。原 `123456` 钱包用例向共用 helper 显式传入其真实密码；缺失测试网络时仅在此用例生命周期内建立 testnet。
- `account_management_state_sync_test.go` 的首次临时发布、unchanged sync、网络等待本地修改三处，以及 Guardian 冷恢复、双设备密钥用例，首次远端 Sync 前使用签名 Core 身份与真实激活。原 TTL、marker、网络等待和密码/密钥断言未删。`activation_ordering_diagnostic_test.go` 的手工旧 ACK 场景不属于机械夹具修正，本次未改。
- 五个文件 SHA-256：`account_managed_test_session_test.go` `519ee0e8f8fd3ae11e2f0b5460895c6cd00ddf76f932d57d766591aaad9571fe`；`account_management_auto_test.go` `4e9d84cf6ee9f4409a2523552fb6320a03ab949ce5550ca4299d93369cf6fd82`；`account_management_state_sync_test.go` `ee1527d459421ea654d6a518e9ed2dfd1197c80b55b2b7b297186642a3df728c`；`wallet_key_derivation_test.go` `5b65fafcca931c170d47fdc6f5148ee80822a89c0441272b8a12d3e319c2b064`；`account_guardian_autopay_review_test.go` `39bcd0f8a100a57840fd5ea8eebb05b6f7a05525978ae2c4098ca52678db2541`。gofmt、tracked 文件限定 diff check 通过；**尚未编译或运行测试**，待定向验证。

### RGB NIA 快照竞争 debug 探针与账户夹具收口（2026-10-30）
- NIA v2 先前 8 个业务页面 case 通过，但组级 pageerror 仍是 RGB 全快照比较失败。后续日志探针仅在该比较拒绝时输出匿名 manager 编号、wallet/account/build 身份一致性布尔值、engine receive 与 projection proof/transfer/output 等记录的新增/删除/值变化数量；Accept 与 refresh 的 begin/committed 也输出同一匿名 manager 编号及来源。无 key、value、地址、seal 或账户标识内容。没有改变快照比较、锁、事务、重试或返回值。`rgb11_transfer_reservation.go` SHA-256 `aecbad03e04c93a1a75794b0568d99c2a30dcd1231a893d7ee7575b67f7de64d`；`rgb11_manager.go` SHA-256 `9a673e4e62efe470371484cb8c60b2fb2161f8c5b2f15dfbf759a71ae4a93236`。PWA 账户 runner 可用 `SAT20_PWA_DIAGNOSTIC=debug` 设置 WASM logrus Debug 级别；旧 NIA v2 和运行中的 TestAll v4 二进制均不包含本次日志代码。
- 另在已批准的测试前置范围内，三个密码/本地选择/Guardian 身份用例移除未激活时的旧强制远端 Sync，原密码、身份、持久化断言保留。marker-read 故障用例改由真实激活并同步的 manager 注入 marker Read 错误，仍要求该错误传播并检查远端记录不变。`account_password_binding_regression_test.go` SHA-256 `bfa25eda4ec41823b13ef2f930f64cbdd3487fb99487fb17cc97d5d364ad21a0`；`account_managed_data_recovery_test.go` SHA-256 `256facc6c5b4f431990ab2c6b930bbabdf742529df43d65f710b7d4eb206e8d0`。gofmt 与限定 diff check 通过，**新候选尚未编译、未运行测试**。`TestAccountStorageBindingDoesNotHoldLocalScope` 的旧业务断言、手工旧 ACK 用例均未改。
- NIA 复跑所用账户 PWA runner 的 console 白名单已增加仅在 `SAT20_PWA_DIAGNOSTIC=debug` 下转发 `RGB snapshot operation:` 与 `RGB snapshot rejected:` 两类现有探针；不打印所有浏览器消息、不改变用例断言。`pwa/scripts/verify/account-management-e2e.mjs` SHA-256 `ba046357e2144e3c0eef61bb1bd64c6d7252aae6dca4b1271649c2bf73ea75eb`；`node --check` 和限定 `git diff --check` 均 exit 0，浏览器未由本线程执行。

### 账户同步/本地身份用例前置对齐（2026-10-30）
- `TestWalletIdentitySnapshotSurvivesAccountSwitch` 只验证本地签名身份，不再要求未激活远端同步；`TestAccountManagementSyncSnapshotCarriesRootSigner` 在远端 Sync 前经真实激活夹具建立基线，原 root signer 断言保留。
- `prepareRemoteActionAccountSelection` 改为先恢复包激活、后检查当前 subscription 并同步；冷重启和签名身份失配的原断言保持。`TestAccountColdUnlockResumesPersistedMetadata` 通过同一真实激活基线先发布初始状态，再制造本地 pending 编辑；保留冷启动任务、权威远端内容与 Core 绑定断言。`TestAccountFirstWalletSchedulesInitialBackup` 旧语义本次未动。
- SHA-256：`identity_snapshot_test.go` `05482b4aa2790eec0d6575778e1364bbbf01faaab22f1031af7368ea8e08090a`；`remote_action_rehydrate_test.go` `6281bf897795cd3191a1273603e99370b35ddfe12afbab0107c05beb6d106a40`；`review_account_merge_regression_test.go` `44b388979748f0bd284b72a9674caaeb12490f1d0fa2c989ab57151c6b89c31d`。gofmt 和限定 diff check 通过；**新候选未编译或执行测试**，现有 Luna 二进制仍是旧输入。
- `TestUnlockWalletIsAtomicOnPasswordFailure` 同属纯本地冷解锁安全用例，已移除未激活时的旧远端 Sync 及仅随之等待的后台任务；两钱包、错误密码、冷进程和凭据清零断言保留。`wallet_lock_test.go` SHA-256 `03b4f8ac20a3f23d1baad4916ef3052a23a10db4ac33aad2b91f9206dd7a3de1`，gofmt 与限定 diff check 通过；待 Luna 随最新夹具组执行，不计通过。

### 用户批准的发布测试语义对齐（2026-10-30）
- PWA `wallet-basic-e2e.mjs`、`wallet-mint-e2e.mjs` 的写入前等待现在分别接受明确 local-only，或已激活且 DKVS 写资格就绪；activation-pending 不放行。仍执行原 addAccount/refresh 及资产/资金断言。两文件 SHA-256 分别为 `7c770e249e894a18d8eaba2e3978ca9ba7bf17ce847f158cb79dfd44e8a96aa1`、`ba9a5deb797916ef18ce2050572ad0382cc41d228192ca02780129a61715d63d`；两个 `node --check` 和限定 diff check 通过。
- `transcend/stp/manager_test.go` 的 sats 用例在 deposit 前等待两端真实 Bitcoin active，检查 L2 ready 错误；可停止的 sender 在正常路径及 `t.Fatal` 清理前均停下。该文件 SHA-256 `98e68a16002c2ffc3d72b0ce8cd105949e921b573aad16e75a20fb84d3040239`，gofmt 与限定 diff check 通过；已交独立测试执行者，未由本线程运行。其他 transcend 测试未改。
- SDK 四项旧预期现按未激活 local-only 原则验证：服务绑定等待期间本地 EnsureAccount 及时成功；首次钱包未排远端备份且激活后才发布；RGB 公共 mailbox 无恢复配置时可本地执行且快照不变；过期 RGB 公开取消完成 tombstone/释放锁并保持幂等。`TestDKVSFinalAccountActivationRegistersReceiveAndMailboxPrefixes` 单例改用真实 mnemonic、恢复包激活与同步，不改变通用 final fixture。五个文件 SHA-256：`account_password_binding_regression_test.go` `6d7b4a1adf01d55a415ceccd9a9e4e3bd4282a1df45a9da665baadfcd0f43acc`；`review_account_merge_regression_test.go` `1d9989b8bd5cc68b5abcf405f1c272c07dd23f125521c2f98a8c6c5253a8a867`；`rgb11_background_mailbox_regression_test.go` `2fa0c41fd3fa1f843052f134a08f9d80d572237b0a53fc844e40638bf14dfd38`；`rgb11_expired_cancel_test.go` `4ea11376a537c1fb95d0832bbb2655782d7c3516dc65553ee1df460f2d928160`；`dkvs_subscription_protocol_test.go` `30676a1e823b41f9a43084366479da06034f7e94386271664adc22a75f759b47`。
- `TestAccountActivationACKCaptureOrdering` 改为真实已激活账户重新生成恢复包并尝试重配置，保留真实签名的 state/data ACK、捕获前成功/捕获后冲突分支及旧 profile/已建立 wrapper 不被失败重配置覆盖。`activation_ordering_diagnostic_test.go` SHA-256 `5537b1baec5b74098bc6c62c2bfccece31919853b6a860467579cdf1d3f0bce9`。上述 SDK 测试只完成 gofmt 和限定 diff check，**未编译或执行**；若该 case 暴露需生产规则修改，先停该项评审，不以放宽断言修绿。NIA 生产重试方案未获批准，未实施。


### 本轮定向验收收据与待跑清单（2026-10-30 12:40 UTC）

本节更新下列 selectors 的实际终态；不代表 SDK 全包、transcend `TestAll` 或所有 PWA 组通过。验收日志根目录为 `../data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/`（相对于本文件）；最终运行前缀保存完整 `.jsonl`、`.exit`、输入前后指纹、`.summary.json`、`.evidence.json` 与 `.sha256`，旧候选/setup-blocked 尝试保留原日志和 exit，不覆盖。跨仓日志保存在 `../../../transcend/data/sdk-acceptance-20261030/`。

#### 产物索引与终态

- `TestContractRGB11`：exit 0，6 个子场景通过；219.036 秒。transcend 日志 `../../../transcend/data/sdk-acceptance-20261030/transcend-contract-rgb11-run-v1-20261030.jsonl`，SHA-256 `665573e67c1ee11403a02b3345c687cabeacf827a1623fda5440500b950c93de`。
- `TestPWAAccountRecovery`：exit 0，3 个恢复 case 通过。SDK 收据前缀 `pwa-account-recovery-v1-20261030`，日志 SHA-256 `e460f81c037bb9d4c53ea02227d044ac7466ac1e061b2d40051cc1e3cd1257b4`。
- POS 历史尝试必须区分：v1 在浏览器前因 heartbeat BRC20 缺 inscription offset 失败；v2 是 Go build cache 权限阻塞，case 未运行；v3 旧 POS hash `a229…` 下 19 pass/1 fail/7 not-run，失败为提现输入父交易 `tx not found`；最终 v4 hash `6111ba9345975a344bb4fb72c24f4350a027c28248c97afee86c77bfe92cdb79` exit 0、27/27 browser case 通过，无 pageerror/panic，输入前后稳定。v4 日志 SHA-256 `839386c9dbef70d5448e87ba2f51810a774c5504534a026f30cd10ceeb023eca`。
- NIA 非诊断 v1 在执行期间跨过 POS 测试输入更新，不能作为固定候选结论；v2 使用最终 hash 且输入稳定，exit 1：8 个业务 case 通过，末尾 `Wallet PWA: all feature groups finish without unhandled browser errors` 为 not-run；`apply RGB11 refresh candidate` 出现 `publish RGB11 candidate projection: account management snapshot changed`，组级 pageerror 断言失败，无 panic。日志 SHA-256 `f8d01f3acafa0013774c12ef864f96bb928a11cf00ba79ab05f8da043d2c470d`。
- NIA debug 单次诊断：`SAT20_PWA_DIAGNOSTIC=debug` 已由 JS allowlist 转发两类 RGB debug。8 个业务 case 通过，末尾全组运行时 guard 为 not-run；pageerror 断言失败，无 panic，探针/候选 hash 前后稳定。JSONL SHA-256 `618e3b7bba8d3a89e7a73c5d84b7c44f04b32792e33b275187a70669dc5b5545`；去敏差异见 `pwa-rgb-nia-diagnostic-v1-20261030.evidence.json`。
- 七个非 RGB groups 单次串行运行 exit 1，输入 hash 稳定、无 panic/pageerror：`l1-protocols` PASS 4/4；`wallet-basic` FAIL 14 pass/1 fail/1 not-run；`dapp` PASS 9/9；`tools` PASS 9/9；`mint` FAIL 1 fail/9 not-run；`mining` PASS 3/3；`node` PASS 6/6。两处首错均为期望 `ready`、实际 `READY:pending`。完整逐组状态见 `pwa-non-rgb-wallet-groups-v1-20261030.evidence.json`；JSONL SHA-256 `7389c2716449a5615aab4b5b28bc132542e41d3c2aa7fc0d8e5949ced855473a`。

#### NIA 探针差异摘要

- runner 保存了 RGB debug 输出；原始 JSONL 有 72 条 operation、8 条 rejected 行，按完整输出去重后分别为 31 个 operation 和 4 个 rejected snapshot。manager 已匿名为 M1/M2。
- 去重 operation 来源/阶段：M1 accept begin/committed 各 1；M1 refresh begin 14、committed 11；M2 refresh begin/committed 各 2。rejected 事件的 `wallet_same`、`account_same`、`build_same` 全为 true；实现没有显式输出 `scope_same`，此处仅按 wallet/account 两项推导为 true。
- 最接近 pageerror 的 rejected snapshot：engine record 无差异；projection `pending` 编辑 1 项、`proof` 编辑 2 项。四个匿名、按 record 类别拆分的完整 add/remove/edit 计数在同前缀 `.evidence.json`；没有保留原 record key、value、地址或密钥。

#### 临时进程清理

本轮测试进程均已到 Go 终态；POS v4、NIA v2、NIA debug 分别有 PWA phase cleanup 标记，七组运行有 7 个 cleanup 标记；账户 runner 的 `finally` 调用 `runnerResources.close`，Go `command.Run` 已返回。对本轮 12 个失败 case 目录做的限定只读 `pgrep` exit 1、无匹配进程。失败 case 数据目录按 runner 规则保留作证据；没有删除目录、终止进程或触碰其他 session。

#### 等待批准后的精确待跑项

1. `wallet-basic` 与 `mint` 当前失败点分别在 `wallet-basic-e2e.mjs:530`、`wallet-mint-e2e.mjs:164` 等待 `state.dkvs_write_allowed`；两处夹具是 local-only 钱包，而 `account_management_state.go:270` 仅在 `RecoveryConfigured` 时开放该写资格。两项最小测试等待条件调整已提交用户审批，尚未获批；本轮没有修改或重跑。
2. NIA production snapshot 修复仍等待用户审批。获批后，仅重跑 NIA 定向诊断一次：`SAT20_PWA_DIAGNOSTIC=debug go test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=55m -run '^TestSDKWalletPWAConnectedBrowser$/^rgb-nia-workflow$' ./e2e`。
3. wallet-basic 与 mint 调整获批并落盘后，分别定向复验：`go test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=90m -run '^TestSDKWalletPWAConnectedBrowser$/^wallet-basic$' ./e2e`；然后同命令改为 `^mint$`。
4. `biometric`、`rgb-ifa-issue`、`rgb-uda-issue`、`rgb-uda-transfer` 依 root 指示延期；POS 与账户恢复已绿，不列入待重跑。上述批准前不启动任何复验。

因此当前验收状态仍为 **未全绿**：NIA pageerror、wallet-basic、mint 尚未闭合；定向通过项不替代全仓/全组验收。本轮未运行 SDK `./wallet` 整包或 transcend `TestAll`。


### 用户批准的 wallet-basic/mint 定向复验（2026-10-30）

本节更新前文“等待批准后的精确待跑项”中 wallet-basic/mint 的状态。测试脚本仅作已批准的 PWA readiness 前置调整；本轮只运行这两个既有浏览器组，各一次、串行。未扩展到 PWA 无法触发的人工并发情景，也未运行其他组。两脚本 `node --check` 均通过。

- `wallet-basic`：`go test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=55m -run '^TestSDKWalletPWAConnectedBrowser$/^wallet-basic$' ./e2e`；exit 0，16/16 browser cases pass，0 fail/skip/not-run，0 pageerror/panic。PWA cleanup marker 1 个；前后 1,022 项源码/依赖输入 hash 稳定。JSONL SHA-256 `2f7dde0f0e328d05a0d2f68c18b3dc54e68f8ee437aac2ad0cafff729b769a58`。收据前缀 `pwa-wallet-basic-v1-20261030`。
- `mint`：`go test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=55m -run '^TestSDKWalletPWAConnectedBrowser$/^mint$' ./e2e`；go test exit 0，10/10 browser cases pass，0 fail/skip/not-run，0 pageerror/panic；PWA cleanup marker 1 个。JSONL SHA-256 `6b165b2b62bfeda549c886ff8326aa45b2add1763f057020927f6694fc697de0`。**该执行不计为固定候选通过**：运行期间 3 个 wallet production Go 输入 hash 发生变化，分别为 `wallet/rgb11_transfer_reservation.go` 13:27:11 UTC、`wallet/rgb11_store_sync.go` 13:27:17 UTC、`wallet/rgb11_manager.go` 13:28:53 UTC；对应前后 hash 和 1,022 文件清单见 `pwa-mint-v1-20261030.input.before.json`、`.input.after.json`、`.evidence.json`。未自动重跑。
- 两次完整 JSONL、exit、前后 hash、逐 case 状态、cleanup/pageerror/panic 证据和校验清单均在 `../data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/`，各以 `pwa-wallet-basic-v1-20261030`、`pwa-mint-v1-20261030` 为前缀。对应 `.sha256` 清单已逐项验证。

当前状态：wallet-basic 在稳定候选上通过；mint 的一次执行 10/10 通过，但源码在运行中变化，需待生产输入冻结后再决定是否安排一次固定候选复验。NIA 快照 pageerror 的生产方案仍待批准；biometric 与 RGB IFA/UDA 仍延期。不要用上述局部结果宣称 SDK 全包或所有 PWA 组全绿。

- 清理核查：basic 临时节点 PID `16291,16305,16313`；mint 临时节点 PID `16946,16952,16998`。两组各有 PWA phase cleanup 标记；结束后对这六个测试创建的 PID 作定向 `ps -p ...`，exit 1、无匹配。mint 结束后定向 `pgrep` 也无仍运行的 `TestSDKWalletPWAConnectedBrowser`；没有终止任何进程。

### 双 scope RGB 冷恢复 valid 分支预期对齐（2026-10-30 14:05 UTC）

用户批准将 `TestRGB11AccountRecoveryTwoAssetScopesAndScopeBinding/valid` 的旧“Restore 后尚未 StartDKVSSync 必须拒写”前置改为核验真实账户 state/data 基线。`LoadAccountManagementStateForRecovery` 在读取恢复值前已经调用 `store.Refresh(stateKey, dataKey)` 并认证资料；Restore 后公开状态必须为 Active、RecoveryConfigured、非 LocalOnly/ActivationPending，且 `StateSeq`、`ManagedDataRevision` 非零、`DKVSWriteAllowed=true`。后续仍启动正常后台同步并原样执行两个非空资产 scope 的余额、签名广播、凭证及再次使用；两个错 scope 负例的拒绝、零广播、零残留断言未改。仅测试源码变化，生产文件未改。

`wallet/rgb11_account_scope_recovery_test.go` 修改前 SHA-256 `f4c637c0b4e70c58529b5fa842e056c7f6e08ca4ec92f4d70832daeb6b3b833d`，修改后 `aad08bf08ee469d871a463ef0bd1e8dcf36086cde9c00aeea0044a881b6aff13`；gofmt 和限定 `git diff --check` 已通过。**尚未运行本次修改。**定向入口：`go test -mod=readonly -count=1 -run '^TestRGB11AccountRecoveryTwoAssetScopesAndScopeBinding$/^valid$' ./wallet`（工作目录 `sdk`）；随后同顶层入口不带 `/^valid$` 复核两个身份负例。

### 冻结 unified RGB 候选的 PWA 定向验收（2026-10-30 14:05–14:13 UTC）

本节更新前文 NIA 快照、mint 源码漂移及 IFA/UDA 延期记录；每项只运行一次，没有重跑已通过的账户或 POS 组。

- `rgb-nia-workflow`：`SAT20_PWA_DIAGNOSTIC=debug go test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=55m -run '^TestSDKWalletPWAConnectedBrowser$/^rgb-nia-workflow$' ./e2e`，exit 0，9/9 browser cases pass，0 pageerror/panic。实际 `wallet.wasm` SHA-256 `d20226fd4b910229bbdb5841d01e5c376a6394079f888c8f7891fdaba2cb27a5`；height-32 overlay 下 326 包、2,348 项有效依赖前后 hash `0516a63e3fbc46dd847ee06fffd7bcd62cbb23c33d95dfdefc4baa74a8d32d5b` 相同。去敏 debug 证据保存在 `pwa-rgb-nia-unified-v1-20261030.evidence.json`。
- `mint`：`go test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=30m -run '^TestSDKWalletPWAConnectedBrowser$/^mint$' ./e2e`，exit 0，10/10 pass，0 pageerror/panic。实际 WASM SHA 与 NIA 相同；height-32 overlay 有效依赖 manifest 前后均为 `e02c25854fd7908c2f4bdcfec32c23b35ebf2e6c59021c8e377eb2f5186dae21`，211 项 runner/PWA 输入稳定。该次是固定候选通过；先前源码在运行中变化的 Mint 结果不计作候选通过。
- IFA/UDA 三个既有 PWA 组按单一 `go test` 串行运行：`go test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=90m -run '^TestSDKWalletPWAConnectedBrowser$/^(rgb-ifa-issue|rgb-uda-issue|rgb-uda-transfer)$' ./e2e`，exit 0；`rgb-ifa-issue`、`rgb-uda-issue`、`rgb-uda-transfer` 三个真实浏览器场景均 PASS，0 fail/skip/not-run/pageerror/panic，各组各有 cleanup marker。运行前候选源码 hash 与冻结 production hash 一致；326 包、2,338 个 Go/embed 编译文件及 2,348 项依赖输入在运行内无文件修改，运行前后的有效 manifest hash 均为 `76f357818580026ed371aa469b8bf0aedeecc12b0f0db12f706d13780fdfbe37`，runner/PWA 211 项输入前后均为 `e55c072f0aa837dccd8cc3f38789b1041edaace4d75c8d8502add37fee1477da`。run-specific overlay 的源文件与规范 replay 稳定；overlay JSON 仅含本轮临时目录路径。
- 三组运行的 preflight 有一项收据时序限制：本轮临时目录在首个组结束时已清理，run-specific overlay/dependency manifest 后续才完成读取。完整 pre-run 依赖参考采用紧邻的稳定 Mint 运行，hash 为 `e02c2585…6dae21`；当前有效依赖清单与该参考的所有非 overlay 输入均相同，源码文件 mtime 在本轮执行时间内变化数为 0。当前三组生成的 `wallet.wasm` 二进制未能直接取到 hash，未因该收据缺口重跑。具体时间戳、实际 overlay 路径 hash 和比较依据见对应 `.input.before.json`、`.input.after.json`、`.evidence.json`。
- 收据前缀 `pwa-rgb-issue-groups-unified-v1-20261030`；完整 JSONL SHA-256 `8b67c6893cf170f9a36d9cdbc850c1efef7593ec8ef1018d523e27eaddb2ff7b`。退出码、overlay、go-list 与前后清单见 `../data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/`；summary/evidence 和逐项校验在同前缀 `.summary.json`、`.evidence.json`、`.sha256`。只运行了上述三个组；biometric、POS、账户全集、SDK `./wallet` 整包及 transcend `TestAll` 不属于本次结果。
