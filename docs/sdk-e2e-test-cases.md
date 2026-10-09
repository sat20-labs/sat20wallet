# SDK 层 E2E 用例清单与评估

### 后续修复与复验状态（2026-10-09）

用户在后续任务中授权检查另一 session 并解决剩余问题。原六项 SDK 失败加错误守卫已一次定向 **7/7 通过**，安全查询 Promise 与 SNS 提前校验均已实施；新增 PWA 16 项最新逐项证据全部通过，Node 最终 5/5、独立 CSV 强退 3/3 通过。此前“待审核/未实施/not-run”段落保留为该次运行历史。本轮未重新执行 SDK29/PWA90 全量，功能覆盖缺口仍保留。

详见 [整合与复验记录](./rgb11-mainline-integration-2026-10-08.md)。证据目录 `/private/tmp/sat20-e2e-followup-20261009/`，汇总 `final-followup-summary.json`；原 5 秒断言、真实签名、精确资产与无广播反例保持，staged 未改。

整理日期：2026-10-09

本文盘点 `sat20wallet/sdk` 中跨 SDK、钱包状态与本地 SatoshiNet 测试环境的端到端用例。这里的“已覆盖”表示仓库中有相应用例，不代表最近一次运行通过。PWA 页面验收见 [PWA E2E 用例清单](./pwa-e2e-test-cases.md)。

### 测试代码补充快照（2026-10-09，运行前）

按用户“先完成测试代码，不需要跑”的要求，本轮已把直接 WASM 清单从 14 项扩展到 **29 项**，每项都有执行函数；复用现有浏览器、节点与受控 L1。新增私钥与监控钱包、V2 选币、L2 owner 锁、带资产 PSBT/订单、批发、垃圾发送、合约与发行/RGB 错误边界。PWA Tools、Mint、解质押和独立强退组同时补了实际 WASM 调用，见下文 SDK-X 映射。

编码完成时，本轮新增和增强断言全部 `not-run`，仅做 JS 语法、Go 格式和 diff 静态检查。用户随后授权定向执行，实际结果见下一节；补充前通过记录不能外推为当前 29 项通过。生产实现未修改。发行组另补真实整数 ORDX/BRC20 和 DID 所有权/重复拒绝，RGB 组另补 carrier 垃圾发送保护。监控钱包 TODO、RGB 地址/proxy 成功专项、有精度/限制发行、Runes/推荐人和真实 revoked commitment punish 等剩余边界继续公开登记；本轮不声称全 196 个导出的成功路径已补齐。

### 新增用例定向验证（用户后续授权）

用户随后要求编码完成后仅运行新增例子。复用既有 `--wallet-cases` 和 Go helper/临时 overlay，由同一 Luna max agent 串行执行：SDK 18 项（15 项新 W15～W29、新 W02/W12 断言及浏览器错误守卫）→ PWA 16 项（新增/增强断言及必要前置）→ 独立强退 3 步。首个非零组结束即停止后续，生产问题先分析方案再由用户审核。本计划不运行 SDK 29/PWA 90 全量、账户、发布态、RGB 恢复或 Guardian；运行结果另行记账，不能复用上述运行前 `not-run` 快照作为最终 verdict。

首轮 SDK 18 项实际为 **7 pass / 11 fail / 0 not-run，Go exit 1**；PWA 16 项及强退 3 步停止，均 `not-run`。证据目录 `/private/tmp/sat20wallet-e2e-20261009/`，逐项结果 `added-cases-sdk.verdicts.tsv`，完整日志 `added-cases-sdk.log`。运行前后 2039 项输入仅两份 Markdown 文档变化，测试和生产编译输入没有变化；本组自有进程已清理。

其中 W15/W16/W21/W24/W28/W29 的首个失败属于测试调用约定或预期错误，已修正：错误密码在冷解锁时验证；托管导入先进行权威根发现；asset info 返回 unsigned hex；合约名为 `exchange.tc`；L2 RPC 的 BTC 数值不能直接作为聪；垃圾发送显式输入转走完整选中值并另选白聪支付手续费。修正保留独立签名、资产、锁、费用和确认到账断言。仅复验这 6 项及 W13 守卫，结果为 **6 pass / 1 fail，Go exit 1**；输入前后 2039 项一致，SHA256 `9c81cb2832524ae01ab4cc96e2759d74c355fd97cbae56092acc6097d7835a12`。证据前缀 `corrected-cases-sdk`。

随后仅 W15 和 W13 再定向执行，实际 **1 pass / 1 fail，Go exit 1**（顶层 44.09 秒、包 46.100 秒），证据前缀 `private-key-cold-sdk`。W15 的目录快照移到 reload 前后，**已经解锁的私钥钱包仍被 getWalletCatalog 拒绝**，页面为 `/#/`；这不是仅冷锁读取顺序问题。根因和方案已增加到下一表。冷解锁/删除的后续断言尚未到达。

本次 before 与 root 收尾 after 的 2039 项输入完全一致，SHA256 `c41a52eb6071a709df037aa5cc540db45046290391c65caf7ba959ebc432ce66`，自有节点已退出。运行结束并完成 after 后，W15 尚未到达的冷锁断言改为错误密码后钱包地址仍不可读取；目录元数据本身不应被测试定义为必须解锁。此最后一处测试调整仅静态检查，不重复运行已确认会在前置目录查询失败的 case。

六项生产修复前，合并各次定向运行的逐项最新结果，已选择的 18 项为 **12 pass / 6 fail**；这是各次定向证据合并，并非当前源码一次全 18 项复跑。W22 的正向追加/finalize 已放在重复拒绝之前，重复输入拒绝断言保留。修复后的实际结果见下一节。新增 PWA 与独立强退仍 `not-run`，整体验收未通过。

合并逐项账本为 `added-cases-sdk.latest-verdicts.tsv`；最后两项的 root 收尾证据为 `private-key-cold-sdk.root-execution.txt`、`private-key-cold-sdk.root-verdicts.tsv` 和 `private-key-cold-sdk.root-inputs-after.sha256`，均位于上述证据目录。

### 已定位问题与六项批准修复（2026-10-09）

用户已审核同意以下六项最小修复。本轮已在现有入口实施，改动留在工作区；修改前六文件备份为 `approved-six-preedit.tar.gz`，完整 index 快照核对一致，未提交或暂存。原失败证据和原方案保留如下，实施不等于验证通过。

| 对应 case | 运行证据与根因 | 最小修复方案 |
| --- | --- | --- |
| W15 | 私钥钱包导入、独立签名、改名和切换成功后，`getWalletCatalog` 报 `the first mnemonic wallet must be unlocked`；`GetWalletCatalogSnapshot` 把无助记词根的合法 standalone 钱包当成目录错误。 | 目录查询先返回正常钱包元数据；未启用托管账户、且首个钱包为私钥时不计算助记词 root account ID。保留已启用托管账户的根校验，不自动启用托管或允许混用。 |
| W02 | `registerCallback()` 缺参返回 `nil`，缺少统一错误对象；`sdk/wasm/main.go` 的 `registerCallbacks` 也未校验函数类型。非函数分支尚未到达。 | 注册前检查恰好一个函数参数，错误统一返回 `{code,msg,data}`，校验失败保留已注册 callback。 |
| W18 | 空监控地址被接受；`sdk/wallet/interface.go:CreateMonitorWallet` 直接切换钱包并保存选中状态。后续错误地址/错误网络分支尚未到达。 | 在任何 scope/钱包状态变更前解码地址，显式核对当前网络和可生成脚本，错误立即返回。保留现有不持久化边界。 |
| W22 | 同一签名订单传入两次，merge 仍成功；`sdk/wallet/psbt.go` 直接追加输入，未检查重复 outpoint。addInputs 的相同缺口为静态发现，尚未运行到该断言。 | merge 和 addInputs 用局部集合检查原输入及待追加输入，重复立即拒绝；正常 split/merge、资产 metadata 和签名保持不变。 |
| W23 | 实际广播 `b92a9a7d…32485f` 只有钱包找零输出，未支付收款人。现有金额正数校验已存在；L1 batch 构造缺少 `n > 0`，零批次数可花费手续费。WASM `.Int()` 还会截断小数批次数。 | SDK L1/L2 batch 入口拒绝非正 `n`，WASM 转整数前拒绝非有限、小数或超出整数范围的值；复用原金额/精度检查和 V2 长度检查，避免重复机制。原日志未记录每次调用参数，零批次数归因由唯一找零输出和当前调用顺序/源码核对得出。 |
| W25 | `deployTickerOrdx('SDKBAD','-1','0',1,'1')` 实际签名广播 commit/reveal；ORDX 仅检查绑定数和整除。当前 SDK 始终显式编码 max，而 L1 `indexer/handle.go` 拒绝显式负 max。 | 签名前按 L1 Indexer 规则拒绝显式负 max/负 lim，保留绑定/整除限制。Indexer 容许 0；不凭本测试擅自增加 `> 0` 或 `lim <= max` 约束。省略 max 表示无上限与显式负 max 不同。同组 DID 名称缺口当时仅为静态发现；本轮实际证据和待审方案见下文。 |

已实施范围：`wallet_catalog.go` 私钥目录不依赖助记词 root；`interface.go` 监控地址/网络/脚本校验在状态变更前；`psbt.go` merge/addInputs 检查原及新增重复 outpoint，addInputs 完成预检查才追加；`interface_send.go` L1/L2 非正批次数拒绝；`wasm/main.go` callback 函数/错误对象及批次数有限、整数、int32 范围校验；`ordx.go` 显式负 max/lim 拒绝。已有托管根校验、金额/精度与 V2 长度校验保留；原 PSBT 无关格式不做整理。

仅复验 W02/W15/W18/W22/W23/W25 和 W13 守卫，第一段实际 **4 pass / 1 fail / 2 not-run，Go exit 1**，证据前缀 `approved-six-sdk`，顶层 449.59 秒、包 451.654 秒。W02、W15、W18、W22 全部通过，包括私钥钱包冷解锁/删除以及 PSBT 正向追加/finalize 和重复拒绝。

W23 的 27 次批量错误请求及每次目录/广播计数/mempool 不变断言均走过，随后 `safetySnapshot('missing-channel', …)` 超时 30 秒；其后六个安全操作和 W25/守卫尚未到达。批次数包含 `2^31`、NaN、正负 Infinity，金额和 V2 长度边界保留。原批次数缺口已经到达修复后的拒绝断言，但整个 W23 仍为 fail。

运行前后 2039 项输入完全一致，SHA256 `3ea0992b1d27f75329f97b145827dc1241719f04fb7f553d01bcc1f95e5bf569`，root 独立 after 同样一致。失败后测试诊断的 `page.evaluate` 无期限等待阻塞页面，主 agent 指示只向已核验的本轮 Node runner 发送 SIGTERM（Node exit 143），Go 完成收尾；自有节点/浏览器已退出。日志、逐项 TSV、执行说明和节点存档分别为 `approved-six-sdk.log`、`.verdicts.tsv`、`.execution.txt`、`nodes/approved-six-sdk/`。没有启动 PWA。

收尾 after 完成后，仅给现有测试诊断加 2 秒等待上限，保留原 fail；JS 语法/diff 静态检查通过。随后只补跑原七项中未到达的 W25 和 W13 守卫，实际 **1 pass / 1 fail / 0 not-run，Go exit 1**，顶层 58.94 秒、包 61.482 秒，证据前缀 `approved-six-remaining-sdk`。W25 到达 DID 后失败，W13 仅证明这一剩余两项组无未处理浏览器错误；W23 阻塞组未到达错误守卫。before/after 2039 项完全一致，SHA256 `6aa624fdf580981901e61ad2966aa860d2cc47372b21ba150c5cd1b8c7795a35`，root 独立 after 同样一致；相对上一 after 只有该诊断脚本变化。自有进程已收尾，完整 staged index 与修改前仍一致。

这七项在上述两次真实运行中的逐项最新结果为 **5 pass / 2 fail**；全部已选择新增 SDK 18 项合并历史定向证据后为 **16 pass / 2 fail**，均非一次全量运行。合并账本 `added-cases-sdk.latest-verdicts.tsv`、本轮 `approved-six-sdk.root-verdicts.tsv` 和 `.root-summary.json`。PWA 16 项与强退 3 步继续 `not-run`；不重跑此前通过项或 SDK29/PWA90 全量。

**该次结束时待审核的安全查询修复（后续已实施）：** `safetySnapshot` 同步 WASM 回调经 `CommitmentExport → FindChannel → LoadChannelInDB → jsDB.Read` 等待 IndexedDB `oncomplete`；回调未返回使浏览器事件循环无法处理数据库完成事件。`commitmentExport`、`forceClosePlan`、`punishStatus`、`punishBuild` 静态发现同样会读数据库或等待通道锁。建议这五个 WASM 包装复用现有 `createAsyncJsHandler`/Promise，通道参数回退、查询、JSON 编码均在异步任务内执行，保持响应字段和安全校验；同步更新 `pwa/types/wasm_exec.d.ts` 的五个返回类型为 Promise。PWA 的 `utils/stp.ts` 和页面现已按异步请求/await 调用；直接调用 WASM 的外部用户也须 await。无需新框架、后台任务或状态。该次结束时生产尚未修改；本轮后续已实施并复验，见文首。

**该次结束时待审核的 DID 校验修复（后续已实施）：** W25 的显式负 ORDX/BRC20、ORDX 精度及缺失 BRC20/Runes 请求已拒绝，且每次目录/广播计数/mempool 不变断言均通过。随后 `inscribeName('invalid/name','1')` 返回成功并实际提交两笔不同的 SNS 铭刻交易：commit `52196f232a4fe06f2472fdd9b91d28b9965c75d6be24fd009b91355905aa0433`，reveal `07042e415f9ef3ee283d38f4385d5507b42ee457308d24e5c0e12522ab6d438f`。root 独立解析原始交易验证 txid、reveal 对 commit 的引用和 witness 中的 `invalid/name`，保存在 `approved-six-remaining-sdk.root-broadcasts.json`。这些广播属于 DID，不属于已拒绝的 ORDX 请求；最后推荐人断言尚未到达。

根因是 `wallet/ordx.go:InscribeName` 仅转小写/trim 后直接选币、签名、广播，缺少名称规则；L1 Indexer `indexer/handle.go:handleSnsName` 已使用 `common.IsValidSNSName`，因此非法名称不会成为有效 SNS 注册。最小方案：把现有名称规范化移到 SDK 入口最前面，复用已导入的 `indexer/common.IsValidSNSName`，非法时在读取钱包/选币/锁/签名前返回错误；保留正常名字的现有流程。该次结束时此修复尚未授权、未实施；后续任务已授权、实施并复验，见文首。

两处生产方案批准后，先仅复验 W23/W25/W13；通过后继续尚未运行的 PWA 新增 16 项和强退 3 步，任一组失败则停止后续并分析，不扩展全量门禁。

### 最近已运行快照（2026-10-09，补充代码前）

| 组 | 最新结果 | 判断 |
| --- | --- | --- |
| SDK 冷恢复原生定向回归 | 20 个顶层测试及全部子项 pass，Go exit 0 | 已签 Splicing 8 阶段、Expand、身份/交易/所有权保护通过。 |
| SDK WASM | 14/14 pass，npm/Go exit 0 | 包含新数据库回退状态查询；196 导出的完整行为缺口仍见 SDK-X01～X09。 |
| 真实浏览器开通/注资重开 | 2/2 pass，Go exit 0 | 原 reservation、交易、确认和资金断言通过。 |
| Deposit 修复定向链 | 5/5 pass，Go exit 0 | 目标资产 Deposit 和四个必要前置通过；原预定入账、链上数量、SDK 余额与 PWA 显示断言通过。 |
| 修复前原始完整 PWA 钱包 | 61 pass、1 fail、20 not-run，npm/Go exit 1 | Deposit 测试误读原生 Decimal JSON；该失败现已定向复验通过，完整组未重新验收。 |
| 后续发布态/账户/CoreModules/Guardian | 当前修复快照 not-run | 用户要求停止全量复跑；历史通过不替代本次结果。 |

Deposit 单文件测试修复已获用户同意并应用，目标及必要前置定向复验通过。本阶段按用户要求收尾，不再复跑完整 82 项和后续功能组；本轮结果不代表完整发布门禁通过。生产 SDK 未再修改。详见文末记录及 PWA 文档。

## 范围与分层

- 主要入口位于 `sdk/e2e`，覆盖真实临时 Bootstrap/Core/Miner、SatoshiNet Indexer、SDK 签名和交易流程；L1 由受控测试 Indexer 提供输入与确认证据。
- `sdk/wallet` 中的 `core_modules_e2e_test.go` 是 SDK 核心模块业务场景，由 `TestSDKCoreModulesE2E` 驱动；其中含原生 SDK 用例，也会启动 PWA 浏览器检查。因此本文登记其 SDK 侧结果，浏览器验收细节另列在 PWA 文档。
- 纯单元测试、仅编译测试、测试运行时目录/锁等基础设施检查，不作为产品 E2E 业务 case 计数。
- `TestPOSPWAL1IndexerSharedBRC20AndRunes` 验证受控 L1 Indexer 对真实签名交易的处理能力；它本身不经过钱包选币、WASM 和 PWA 提交流程，不能单独证明钱包业务 E2E 已覆盖。

## 用例分组

| 用例组 | 主要入口 | 当前覆盖的业务 case |
| --- | --- | --- |
| 账户目录与密钥 | `TestRealSatoshiNetAccountManagementLifecycleAndConcurrentDevices`、`TestSDKAccountBackupKeyRecreatedFromRoot` | 创建/导入根钱包，子账户和目录同步，多设备并发更新，备份密钥从根密钥重建，账户身份和服务信息签名。 |
| 账户恢复、安全与持久化 | `TestSDKAccountReviewLifecycle`、`TestSDKAccountReviewRestoreValidation`、`TestSDKAccountReviewAuthorization`、`TestSDKAccountReviewMultiDevice`、`TestSDKAccountReviewPasswordRestart`、`TestSDKAccountFailureRegressions`、`TestSDKAccountPWAOfflineRestart`、`TestSDKAccountPWACommitResponseLossRestart` | 2-of-2/2-of-3 恢复、错误凭据只读拒绝、授权一次性消费、密码改后重启、并发设备合并、提交后响应丢失重启、离线恢复、失败写入不改变原目录。PWA 页面交互步骤见 PWA 文档。 |
| Guardian 与付费托管 | `TestSDKAccountGuardianIdentitySurvivesAccountRecovery`、`TestSDKAccountGuardianPaidPWABatchReview`、`TestSDKAccountAutopayPreparedTransactionColdRetry`、`TestRealSatoshiNetAccountManagementAutopaySync` | Guardian 更换设备后身份保持、原好友分片可恢复；付费存储确认和取消；AUTOPAY 预签交易在中断或回执丢失后续传；付费状态同步且不重复付款。 |
| DKVS 本地 SDK 与模块 | `TestSDKDKVSModuleLocal`、`TestSDKDKVSModulePaid`、`TestSDKDKVSModuleReplica` | 创建/读写/删除、批量原子性、幂等重试、并发 CAS、限额失败原子性、取消、续期、到期、付费降级保护、付费删除回执重放、快照/增量与磁盘重开。 |
| DKVS 网络、来源与安全边界 | `TestSDKDKVSActiveState`、`TestSDKDKVSActiveNetwork`、`TestSDKDKVSCurrentPayloadRejectsDeletionHistory`、`TestSDKDKVSBoundRPCHTTP`、`TestSDKDKVSBoundWalletRPC`、`TestSDKDKVSWriteContextSecurity`、`TestSDKDKVSLaunchBoundReplay`、`TestSDKDKVSLaunchReviewHTTP`、`TestSDKDKVSLaunchReviewNetwork`、`TestSDKDKVSReleaseReviewState`、`TestSDKDKVSReleaseReviewPeer` | 在线订阅和离线重连、删除后不复活、同步游标和端点绑定、非授权节点拒绝写入、签名/高度/序列校验、发布记录重放防护、替代 peer 恢复和释放时的并发状态边界。其他 DKVS 用例见 `sdk/e2e/dkvs*_e2e_test.go`。 |
| RGB 资产与恢复 | `TestSDKCoreModulesE2E`（调用 `TestSDKCoreModulesConnectedE2E`）及 `sdk/wallet/core_modules_e2e_*.go` | 多钱包/子账户备份，RGB NIA/IFA/UDA 发行，invoice 与直连传输，接收验证、ACK、广播、carrier/proof/lock、资产守恒、DKVS 恢复、恢复失败后的同库重试，以及 RGB11 名称和 Transcend 注册描述符。 |
| RGB11 注册表 | `TestRGB11RegistrySDKDKVSE2E`、`TestRGB11RegistryReviewE2EFullSnapshotAndReopen`、`TestRGB11RegistryReviewE2ERejectMutableRegistryShape` | HTTP 往返与快照恢复、SDK 资产类型兼容、非可信响应拒绝、权限策略失败关闭、快照/重建后读取和可变注册表形状拒绝。 |
| 合约 SDK 与资金安全 | `TestSDKSmartContractsAdmission`、`TestSDKSmartContractsFunding`、`TestSDKSmartContractReleaseAssetValidation`、`TestSDKSmartContractReleaseEscrow`、`TestSDKSmartContractDeployFailureSettlement` | 入参与资产元数据校验、carrier/binding sat 保留、资金不足时拒绝或退款、Result/执行托管、失败部署结算，以及不得把失败调用伪装为成功支付。 |
| 模板合约 | `TestSDKSmartContractsTemplates`、`TestRealSatoshiNetTemplateLimitOrder`、`TestRealSatoshiNetTemplateAMMMultiLiquidity`、`TestRealSatoshiNetTemplateExchangeGasForSatoshiAndClose`、`TestTemplateAutopayPaysAndCloses` | 限价单部分成交/退款/关闭、AMM 加池与 LP 权限、Exchange 最小输出保护、AUTOPAY 付款/取消/退款、资产矩阵和精确收款。 |
| EVM 与 Solidity | `TestSDKSmartContractsEVM`、`TestSDKSmartContractsOutOfGas`、`TestRealSatoshiNetEVMSignedAssetVault`、`TestRealSatoshiNetEVMAssetApps`、`TestRealSatoshiNetEVMStandardApps`、`TestRealSatoshiNetEVMAMMDefaultInvoke`、`TestRealSatoshiNetEVMAMMGasAsset`、`TestRealSatoshiNetEVMInternalERC20` | SDK 部署、调用、独立查询、revert/out-of-gas 回滚、资产存取与转账、权限拒绝、默认调用资产守恒、模板 EVM 应用和节点重启后继续调用。 |
| Agent 合约与消息 | `TestRealSatoshiNetAgentPredictionTenBettors`、`TestRealSatoshiNetAgentPredictionPayoutByShare`、`TestRealSatoshiNetMessageTopicSDKToCore` | Agent 预测合约多人下注与按份额结算；SDK 到 Core 的消息 topic 往返。真实模型审查/oracle 接入是否覆盖，须以测试实际依赖为准，不能由假模型响应推断。 |
| L1 交易夹具与运行环境 | `TestPOSPWAL1IndexerPreservesRealSignedTransactionAndAssetFlow`、`TestPOSPWAL1IndexerSharedBRC20AndRunes`、`TestSatoshiNetRuntimeNodeLifecycle`、`TestSatoshiNetRuntimeLockInheritedByNode` | 真实签名交易、UTXO/绑定资产流和受控确认；测试节点生命周期及运行锁。夹具和运行环境用例是业务 E2E 的依赖，不等同于钱包 UI 成功路径。 |

## 运行入口

在 SDK 目录执行针对性组别；以下示例按 Go 测试名筛选，不会运行 PWA 完整验收：

```bash
cd /Users/yingfeng/github/sat20wallet/sdk
go test ./e2e -run '^(TestSDKDKVSModuleLocal|TestSDKDKVSModulePaid|TestSDKSmartContractsTemplates)$' -v -count=1
```

`TestSDKCoreModulesE2E` 使用真实临时节点并包含 WASM/PWA 检查，执行预算明显更长；完整账户浏览器门禁和 PWA 钱包门禁以 [PWA E2E 用例清单](./pwa-e2e-test-cases.md) 中的 npm 入口为准。运行前应确认完整 SAT20 本地工作空间、Go/Node/浏览器依赖和测试运行锁状态。

## 最近运行记录与判断

- `pwa/scripts/verify/WALLET_ACCEPTANCE.md` 记录的最近一次完整 SDK E2E 运行在 2026-10-08 以 `go test -p=1 -parallel=1 ./e2e ... -timeout=100m` 结束，结果为失败并触发总超时；构建和运行期间相关源码仍有并发变更，因此不能据此认定当前工作树全部通过或把失败归因于最后显示的测试名称。
- 该记录中，Guardian 原生恢复的两个子场景曾返回 `key not found`，之后实现和测试均有变更，仍需对同一源码快照重新运行确认。
- 2026-10-08 已记录 Go 编译和 JS 语法检查通过的局部结果；它们不等于 SDK 业务 E2E 全部通过。
- 首次整理为静态盘点；本轮已增补直接 WASM 门禁并发起执行，完成前不认定当前快照通过。

## 增补与精简建议

1. **先把测试代码补齐并静态核对，再按用户指定 case 执行。** 本轮只运行新增/增强项与必要前置；遇到产品失败保留证据，审批修复后只复验对应项。正式发布前仍需当前源码的完整准出结果，历史通过不能代替。
2. **补协议边界时复用现有入口。** BRC20/Runes 的 Indexer 夹具用例不替代 SDK 钱包交易构造、签名和余额变化。若 SDK 层需要单独约束这些协议，优先复用现有 `TestPOSPWAL1Indexer...` 与钱包交易构造路径，不另建服务或运行框架；PWA 端到端成功路径由 PWA 文档跟踪。
3. **不要因 case 数多而删除 DKVS/恢复失败边界。** CAS、删除不复活、授权重放、提交回执丢失、离线副本等场景约束不同状态转换，不能简单合并成一个“同步成功”用例。可以在执行计划中按快测/真实节点长测分层，保留原有断言。
4. **精简重复的执行入口，不精简唯一业务断言。** 2026-10-08 已移除 9 个重复定向浏览器入口；后续优先沿用现有测试名和过滤能力，不再为同一 case 增加并行 wrapper。相同账户场景在 SDK 与 PWA 两层只有在明确验证不同边界时才分别保留。

## 维护约定

新增或删除用例时，同步更新本文件的入口、覆盖边界和最近验证状态；报告必须区分 `pass`、`fail`、`not-run`、编译通过与静态登记。用例预期应来自执行前已知的输入、费用和钱包权益，不应从实际到账倒推预期。

## WASM 功能门禁（本轮增补）

生产导出合同共 **196 个函数、三个命名空间**：`sat20wallet_wasm`、`sat20account_wasm`、`sat20wallet_operation_log`。静态 allowlist 和 SDK-W01 的浏览器检查约束导出合同。**导出存在不等于业务功能已经通过**。以下完整映射明确已有行为证据与缺口，不用原生 Go API 验收代替 JS 包装的验收。

| 功能族 | 接口数 | 全部生产导出 | 行为证据和边界 |
| --- | ---: | --- | --- |
| 账户恢复与存储授权 | 23 | `abortSession`、`acceptGuardianSetup`、`autopayStatus`、`cancelStorageAuthorization`、`checkGuardianSetup`、`commitRecovery`、`confirmStorage`、`consumeGuardianResponse`、`createGuardianRequest`、`createGuardianResponse`、`createRecovery`、`fundAutopay`、`getStorageOptions`、`guardianIdentity`、`loadRecovery`、`preflight`、`previewRecovery`、`recoverKnowledge`、`rehearse`、`resumeStorageAuthorization`、`reusePaidStorage`、`setUserShare`、`status` | 账户浏览器 66 项、Guardian/付费专项；授权、错误凭据、取消、重放、离线恢复、多设备。 |
| 操作日志 | 5 | `beginOperationLog`、`deleteAllOperationLogs`、`getOperationLog`、`getOperationLogs`、`updateOperationLog` | SDK-W09 JSON 往返、重载、删除；PWA 验证真实业务日志。 |
| 钱包、身份和生命周期 | 30 | `changePassword`、`createMonitorWallet`、`createWallet`、`deleteWallet`、`ensureAccount`、`getAllWallets`、`getChannelAddrByPeerPubkey`、`getMnemonice`、`getNodePubKey`、`getVersion`、`getWalletAddress`、`getWalletCatalog`、`getWalletPubkey`、`importWallet`、`importWalletWithPrivKey`、`init`、`isWalletExist`、`recoverAccountManagementFromCurrentWallet`、`recoverAccountManagementFromRootMnemonic`、`registerCallback`、`release`、`switchAccount`、`switchChain`、`switchWallet`、`unlockWallet`、`updateAccountMetadata`、`updateWalletName`、`validateBitcoinAddress`、`validateMnemonic`、`validateSatsNetAddress` | SDK-W01～05、W10～12 及钱包页面。SDK-W15～20 已补私钥/监控地址/L2 锁与 V2 查询；独立强退组补真实 callback。逐项当前结果见 W 表，新增强退组仍 not-run。 |
| 签名、PSBT 和订单 | 19 | `addInputsToPsbt_SatsNet`、`addOutputsToPsbt_SatsNet`、`buildBatchSellOrder_SatsNet`、`extractTxFromPsbt`、`extractTxFromPsbt_SatsNet`、`extractUnsignedTxFromPsbt`、`extractUnsignedTxFromPsbt_SatsNet`、`finalizeSellOrder_SatsNet`、`getCommitTxAssetInfo`、`getTxAssetInfoFromPsbt`、`getTxAssetInfoFromPsbt_SatsNet`、`mergeBatchSignedPsbt_SatsNet`、`signData`、`signMessage`、`signPsbt`、`signPsbt_SatsNet`、`signPsbts`、`signPsbts_SatsNet`、`splitBatchSignedPsbt_SatsNet` | SDK-W06～07 独立验 L1 单/批签名、PWA DApp 签名及实际广播。SDK-W21/W22 已补 L2 带资产 PSBT 和订单辅助包装，独立原生验签；W21 定向 pass、W22 重复输入拒绝失败。 |
| UTXO 和资产查询 | 20 | `getAllLockedUtxo`、`getAllLockedUtxo_SatsNet`、`getAssetAmount`、`getAssetAmount_SatsNet`、`getAssetSummary`、`getTickerInfo`、`getUtxosWithAsset`、`getUtxosWithAssetV2`、`getUtxosWithAssetV2_SatsNet`、`getUtxosWithAsset_SatsNet`、`isUtxoLocked`、`isUtxoLocked_SatsNet`、`lockUtxo`、`lockUtxoForOwner`、`lockUtxoForOwner_SatsNet`、`lockUtxo_SatsNet`、`unlockUtxo`、`unlockUtxoForOwner`、`unlockUtxoForOwner_SatsNet`、`unlockUtxo_SatsNet` | SDK-W08 owner 锁和跨 origin 拒绝；PWA 余额、选币、锁和重载。SDK-W19/W20 已补 L2 owner 锁及 V2 查询，定向 pass。 |
| RGB11 | 23 | `acceptRGB11Consignment`、`broadcastRGB11OutOfBand`、`cancelExpiredRGB11Transfer`、`cancelRGB11OutOfBandTransfer`、`createRGB11Invoice`、`deliverAndBroadcastRGB11AddressTransfer`、`deliverAndBroadcastRGB11ProxyTransfer`、`enableRGB11AddressReceive`、`exportRGB11Contract`、`fetchRGB11ProxyAck`、`getRGB11AddressCarrierWarning`、`getRGB11State`、`importRGB11Contract`、`importRGB11ContractFile`、`issueRGB11Asset`、`prepareRGB11AddressTransfer`、`prepareRGB11Consignment`、`prepareRGB11Transfer`、`receiveRGB11ProxyConsignment`、`refreshRGB11State`、`resolveRGB11AddressEndpoint`、`resumeRGB11PreparedTransfer`、`syncRGB11AddressMailbox` | 钱包 11 项、恢复 9 项浏览器组及原生 CoreModules；地址/proxy 成功仍缺 PWA 入口。 |
| 通道与安全退出 | 31 | `allReservations`、`batchUnlockFromChannel`、`batchUnlockFromChannelV2`、`closeChannel`、`commitmentExport`、`expandAll_SatsNet`、`expandAsset`、`expandChannel`、`expandChannel_SatsNet`、`forceClosePlan`、`getAllChannels`、`getChannel`、`getChannelStatus`、`getCurrentChannel`、`lockToChannel`、`lockToChannelWithExpand`、`openChannel`、`previewOpenChannel`、`punishBroadcast`、`punishBuild`、`punishStatus`、`rebuildChannel`、`reopenChannel`、`reservationStatus`、`restoreChannel`、`resumeLockWithExpandFromL1Tx`、`safetySnapshot`、`splicingIn`、`splicingOut`、`sweepBuild`、`unlockFromChannel` | 25 项资金/POS 组及 SDK/Transcend 原生场景。独立强退/CSV 回收已写代码，not-run；真实 punish 广播仍未接入浏览器组。 |
| 发送与跨层资金 | 8 | `batchSendAssets`、`batchSendAssetsV2_SatsNet`、`batchSendAssets_SatsNet`、`deposit`、`sendAssets`、`sendAssets_SatsNet`、`sendGarbage`、`withdraw` | 真实 WASM/PWA L1/L2 BTC/ORDX、BRC20、Runes、批量资金流和取消；夹具单测不能替代钱包发送。 |
| 合约 | 22 | `buildUnifiedContractContent`、`deployContract_Remote`、`deployUnifiedContract`、`estimateDeployUnifiedContract`、`getAddressStatusInContract`、`getAllAddressInContract`、`getContractInvokeHistoryByAddressInServer`、`getContractInvokeHistoryInServer`、`getDeployedContractAnalytics`、`getDeployedContractStatus`、`getDeployedContractsInServer`、`getFeeForDeployContract`、`getFeeForInvokeContract`、`getFeeForInvokeUnifiedContract`、`getParamForInvokeContract`、`getParamForInvokeUnifiedContract`、`getSupportedContracts`、`invokeContractV2`、`invokeContractV2_SatsNet`、`invokeContract_SatsNet`、`invokeUnifiedContract`、`queryContract` | Tools 现为 8 项，新增 direct WASM 查询/estimate/analytics 对账和非法请求；新增部分 not-run。 |
| 发行、DID 和推荐人 | 10 | `DeployRunes_Remote`、`bindReferrerForServer`、`deployTickerBrc20`、`deployTickerOrdx`、`getAllRegisteredReferrerName`、`inscribeName`、`mintAssetBrc20`、`mintAssetOrdx`、`mintAssetRunes`、`registerAsReferrer` | Mint/DID 现为 9 项，新增真实整数 ORDX/BRC20 部署/铸造、DID 所有权/重复拒绝、确认索引与重载；完整协议发行仍有剩余边界。 |
| 挖矿和节点角色 | 5 | `getBTCLuckyMiningStatus`、`minerUnstake`、`stakeToBeMiner`、`startBTCLuckyMining`、`stopBTCLuckyMining` | Mining 2 项、节点现为 5 项；父 Core 拒绝与独立 Miner/Core 解质押已写代码，not-run。 |

### 29 项直接 WASM case

入口 `TestSDKWASMInterfacesE2E` 共用既有真实 Bootstrap/Core/Miner、受控 L1 和 Chromium，直接调用原始导出检查响应、参数和独立密码学证据。每个 case 使用独立 IndexedDB；PSBT/订单只验签，最后三个发送 case 实际签名、广播和确认。L2 PSBT 使用 Go 原生资产解码及脚本 VM 验签；BTC 签名使用独立密码库校验。

| 编号 | 必跑 case | 状态 |
| --- | --- | --- |
| SDK-W01 | production namespaces match the complete export contract without debug secrets | pass（批准修复后完整复验） |
| SDK-W02 | missing arguments and wrong types return errors without terminating the runtime | pass：六项批准修复后定向复验，callback 缺参/非函数均拒绝 |
| SDK-W03 | mnemonic import and address validation preserve the independent fixture identity | pass（批准修复后完整复验） |
| SDK-W04 | wallet creation exports a valid phrase and password authentication survives reload | pass（批准修复后完整复验） |
| SDK-W05 | subaccount metadata and selection survive reload without changing root identity | pass（批准修复后完整复验） |
| SDK-W06 | data and Bitcoin message signatures independently verify the exact payload | pass（批准修复后完整复验） |
| SDK-W07 | PSBT and batch signatures preserve outputs and independently verify Taproot witnesses | pass（批准修复后完整复验） |
| SDK-W08 | UTXO owner locks persist and reject another origin before exact-owner unlock | pass（批准修复后完整复验） |
| SDK-W09 | operation log JSON roundtrip persists and deletion preserves wallet identity | pass（批准修复后完整复验） |
| SDK-W10 | invalid password mnemonic PSBT and root deletion preserve the catalog | pass（批准修复后完整复验） |
| SDK-W11 | negative fractional and overflowing account indexes are rejected without selection changes | pass（批准修复后完整边界复验） |
| SDK-W12 | release rejects stale calls and reinitialization recovers the same persisted wallet | pass：新增重复 init/错误配置恢复定向执行 |
| SDK-W13 | all direct interface cases finish without unhandled browser errors | pass：剩余 W25/守卫两项组；W23 阻塞组未到达守卫，全 29 项未运行 |
| SDK-W14 | unknown channel status returns from database fallback without blocking later calls | pass（第二次批准修复后 14/14 完整复验） |
| SDK-W15 | 私钥钱包独立派生/验签、改名、切换、冷重启、删除 | pass：六项批准修复后，冷解锁/删除及目录断言全部通过 |
| SDK-W16 | 托管助记词账户拒绝混入私钥钱包，目录不变 | pass：权威发现后托管导入，混用拒绝 |
| SDK-W17 | 合法监控地址可读，不能签名或导出秘密 | pass；当前不支持持久化 |
| SDK-W18 | 非法/错误网络监控地址拒绝且保留选中钱包 | pass：六项批准修复后，错误地址/网络均拒绝且身份不变 |
| SDK-W19 | L1/L2 V2 选币、余额与独立 Indexer 对账，超额拒绝 | pass |
| SDK-W20 | L2 owner 锁重载保持，origin/账户/fingerprint 错误拒绝 | pass |
| SDK-W21 | L2 带资产 PSBT 单/批签名、提取、独立 VM 验签，缺签/篡改拒绝 | pass：修正 unsigned hex 预期，原签名/篡改保护断言通过 |
| SDK-W22 | 订单 split/merge、addInputs/addOutputs、双钱包 finalize，重复输入拒绝 | pass：六项批准修复后，签名/资产 metadata/finalize 和重复拒绝通过 |
| SDK-W23 | 非法批次数量/金额及未知通道安全操作不广播 | fail：27 次批量错误请求拒绝且状态不变；随后 safetySnapshot 未知通道同步回调超时 |
| SDK-W24 | 合约内容与原生协议编码一致，错误请求不改目录/资金 | pass：修正合约名，原编码/错误请求断言通过 |
| SDK-W25 | 发行精度/金额、DID、推荐人错误参数不签名/广播 | fail：负 ORDX max 等前置拒绝通过；非法 DID 实际广播 commit/reveal，推荐人尚未到达 |
| SDK-W26 | 错误 RGB consignment/请求/续跑不改 proof、锁与目录 | pass；成功传输另由已有 RGB 组覆盖 |
| SDK-W27 | L1 真实批发、逐输出金额、返回 fee 与输入输出差一致、确认到账 | pass |
| SDK-W28 | L2 真实多金额批发、签名/确认、固定费与逐输出到账 | pass：原生聪值/固定费/确认到账通过 |
| SDK-W29 | 垃圾发送拒绝锁定输入，完整选中值到账、白聪 fee 输入保护 | pass：完整选中值、独立白聪 fee 输入、精确余额守恒通过 |

执行：

```bash
cd /Users/yingfeng/github/sat20wallet/pwa
npm run verify:sdk-wasm-e2e
```

验收要求：当前清单必须 **29/29 pass**，无 fail/not-run；本轮未运行，历史 14/14 不能代替。禁止私钥/调试导出；错误凭据/非法入参不得改变目录和选中身份；签名独立验证，不以非空返回算通过；负数、小数、uint32 溢出的账户索引必须拒绝。`getMnemonice` 现有错误密码约定是返回空助记词，测试同时检查不会泄露秘密和改变目录。私钥钱包成功 case 与托管根账户隔离；监控钱包未完成的能力保留失败断言，生产修复仍须用户审核。

本轮一个 **GPT 6 Luna / max** subagent 串行执行 SDK、PWA，因共用运行锁与节点构建目录无法安全并行。源码 SHA256、原始输出和逐项 verdict：`/private/tmp/sat20wallet-e2e-20261009/`。生产问题由主 agent 分析，经用户审核修复方案后实施和复验。上述缺口继续保留，不能宣称全部 196 个函数行为已经覆盖。

### 本轮已确认的运行结果与修复复验

- 2026-10-09 `TestSDKWASMInterfacesE2E` 首轮 9 pass / 4 fail / 0 not-run；三项失败为新增测试对派生索引、UTXO 金额/类型和旧 API 空助记词返回约定的误用，已只修测试。
- 修正后完整复跑 **12 pass / 1 fail / 0 not-run**，耗时 Go 包 194.909 秒，退出码 1；前三项误报均通过。
- 唯一产品失败：`switchAccount(1.25)` 返回成功并将小数截为账户 1；`-1` 已拒绝，本次失败后没有执行后续 uint32 溢出/MAX_SAFE 子断言。
- 根因：`sdk/wasm/main.go` 的 switchAccount 直接 `.Int()` 再 `uint32`，没有 ensureAccount/updateAccountMetadata 已有的原值与转换值相等检查。getWalletAddress/getWalletPubkey 静态存在同类转换。
- 最小方案：这三个接口复用既有无符号整数检查，保持合法整数行为；不增加持久状态或抽象。用户已审核同意，三个接口的校验已实施；原提案保留在 `/private/tmp/sat20wallet-e2e-20261009/proposed-wasm-account-index.patch`。
- SDK-W11 已补三个接口对负数、小数、uint32 溢出、MAX_SAFE_INTEGER、NaN、正负 Infinity 的拒绝与目录不变断言，以及合法 0/1 索引的切换和独立公钥/地址验证。JS 语法和 diff 检查通过；实际 WASM 的完整边界复验已 pass。
- 复跑原始日志：`/private/tmp/sat20wallet-e2e-20261009/sdk-wasm-rerun.log`；初次结果同目录 `sdk-wasm.log`。同一 Luna max agent 按 SDK、发布态、钱包 82 项、账户 66 项、RGB 和 Guardian 顺序继续；更新后的运行使用 `*-approved.log`，任一组失败后停止后续组并由主 agent 分析，当前整体尚不能准出。
- 批准修复后 SDK 完整复验 **13 pass / 0 fail / 0 not-run，退出码 0**，Go 包 387.762 秒。日志 `sdk-wasm-approved.log`；1764 个 SDK/PWA 文件前后快照一致，清单 SHA256 `b8f518edce388b2f6b629b69c6e544d837596485bea6abd3fa62426830336720`。这份清单没有覆盖本地 replace 的其他仓库；后续功能组扩展到 indexer、satoshinet、transcend 的实际源码及构建输入，分别保留范围。
- 后续钱包 82 项真实 WASM/PWA 功能组 **40 pass / 9 fail / 33 not-run，退出码 1**；前后 2038 文件输入清单一致。已确认注资 reservation 与旧 PWA 显示状态不一致；两个 stake case 尚未满足 H=32 前置条件。该轮后续账户/CoreModules/Guardian 未执行，不能把直接 WASM 13 项通过等同于全部接口行为准出。细项及证据见 PWA 文档。
- 六项导入启动等待失败修正后，RGB 11、Tools 6、Mint 6、Mining 2 共 **25/25 pass，退出码 0**，Go 用例 763.876 秒；前后源码与临时 overlay 输入清单一致，日志 `pwa-startup-focused.log`。这是定向复验，完整钱包 82 项、账户/CoreModules/Guardian 仍待继续；未将历史失败改写为完整组通过。
- 注资显示的两文件 PWA 修复已获用户同意并原样实施；同一 Luna max agent 先定向复验开通/注资/重载，再串行复跑 SDK WASM、完整钱包、发布态、账户/CoreModules/Guardian，逐组保留新的输入快照与 verdict。新运行完成前继续保留上述历史结果与公开接口行为缺口。
- 显示修复后两项定向复验为 **1 pass / 1 fail，Go 退出码 1**：注资已广播、重开前 pending 显示通过，关闭页面重开后 Channel 页显示 Open。SDK 冷解锁缺少 Splicing runtime 恢复，idle READY 初始化因 pending 保护跳过该通道，reservation.Channel 为 nil；现有查询和 monitor 均无法使用该 pending 通道。拟在 `db.go` / `interface.go` 补与现有恢复路径一致的分支，用户审核前未修改 SDK。
- 随后的只读诊断在提交阶段碰到独立的 Core READY 时序问题：客户端状态已为 16，但 Core 对注资请求返回 can't find channel，同秒稍后才完成 READY 安装；L1 pending 交易数 180 秒仍为 0，reservation 列表也为空，没有走到重开。已在既有诊断 case 增加 Core 只读 READY 前置等待；下一次为开通 1 pass、注资 1 中断/未完成，renderer 卡住且 8 个本地源输入在运行期间外部改变，不能作为当前业务归因证据。新有界诊断正在复跑。日志、快照和严格 verdict 在 PWA 文档登记；后续全量组保持未执行。
- 静态确认 `sdk/wasm/main.go` 的 getChannelStatus 同步回调在 FindChannel 数据库回退时等待 IndexedDB，阻塞回调所需 JS 事件循环。拟按相邻 getChannel/getCurrentChannel 使用既有异步包装，仍待用户批准；修复方案的生产范围因此为 db.go、interface.go、wasm/main.go 三文件。SDK-X07 补充必须验该导出在 runtime 未安装/未知通道时仍能返回且后续接口可响应，不只验证内存命中的成功路径。
- 最终有界浏览器诊断 **1 pass / 1 fail / 0 not-run，Go 退出码 1，266.269 秒**，输入前后一致。重开后 SDK 明确返回 channel is nil，但同一钱包/子账户的 0x201 reservation 与 SplicingTx/AnchorTx 均保留，证实冷解锁 runtime 缺失。日志 `pwa-splicing-bounded-diagnostic.log`；完整快照和节点证据见 PWA 文档。新生产修复仍待用户审核，未开始后续全量组。
- 上述诊断和快照收尾之后，在现有 `sdk-wasm-e2e.mjs` 增加 SDK-W14：独立导入钱包，以合法但不存在的 2-of-2 P2WSH 通道地址触发数据库回退，30 秒内必须返回既有 CS_UNKNOWN=0；再查版本、钱包地址和目录，证明 runtime 可继续响应。保留缺参/错误类型拒绝断言，不靠未解锁或非法地址跳过 DB。JS 语法与 diff 检查通过，浏览器业务尚未执行；新清单为 14 项。

### 完整接口行为的补充验收 case：代码映射与剩余边界

以下逐项关联可执行代码；代码完成与运行通过分别记录。尚缺真实产品入口或协议夹具的部分继续保留，不能用参数错误测试或原生 API 成功代替完整 WASM 成功路径。

| 编号 | 接口/场景 | 成功与边界断言 | 前置/当前限制 |
| --- | --- | --- | --- |
| SDK-X01 | importWalletWithPrivKey、switchWallet、deleteWallet | 独立公钥库从测试私钥推导地址；签名验真、错误 key/密码拒绝，冷重启保留身份；已托管助记词账户拒绝添加私钥钱包且目录不变。 | SDK-W15/W16 代码完成：独立私钥地址/签名、切换改名删除、冷重启、混用拒绝。调用约定已修正，W16 pass；W15 已解锁私钥目录 fail，冷解锁/删除尚未到达。 |
| SDK-X02 | createMonitorWallet | 合法地址可读、无助记词/签名能力；非法地址失败不改变原钱包；重启行为与公开支持承诺一致。 | SDK-W17/W18 代码完成；原生 CreateMonitorWallet 仍为 TODO，不保存且未校验地址。新边界断言保留，未实施生产修复。 |
| SDK-X03 | registerCallback、init/release | 真实账户/通道变更触发回调，回调内容匹配实际状态；release 后旧 manager 不再派发；重复 init/错误配置可恢复且不并存实例。 | SDK-W12 补重复 init/错误配置；独立 CSV 组在真实确认前 release，检查旧回调不派发，重建后捕获 force-close/sweep 的真实 txid。SDK-W02 补 callback 缺参/非函数拒绝；W12 定向 pass、W02 缺参 fail，非函数分支和强退组尚未运行。当前 registerCallback 缺参返回 nil、未校验函数类型，保留失败断言，未改生产实现。 |
| SDK-X04 | SatsNet PSBT/订单辅助导出 | 已知输入/资产/费用的 L2 PSBT 单/批签名；extract、addInputs/addOutputs、split/merge、finalize 往返保留原交易和资产；错误 PSBT、缺签名、重复输入、未知资产拒绝。 | SDK-W21/W22 代码完成：Go 独立资产解码/VM 验签、未签名提取拒绝、篡改 metadata 和重复输入拒绝、双钱包 finalize。不广播订单；W21 定向 pass，W22 merge 重复拒绝 fail，后续订单步骤尚未运行。 |
| SDK-X05 | V2 UTXO、L2 owner 锁、sendGarbage/batchSend | 查询结果与独立 node/indexer 对账；origin/账户 owner 不匹配不可解锁；零/负数/小数批次和超额拒绝，锁定或 RGB carrier 不可被垃圾选币花掉；合法批发到账与费用守恒。 | SDK-W19/W20/W23/W27～29 代码完成：选币/锁/非法批次、真实 L1/L2 批发与明确输入垃圾发送；RGB 第 1 项增强明确指定真实 carrier 时拒绝垃圾发送、余额/锁/广播/unspent 保持。W19/W20/W27 pass，W23 fail，W28/W29 定向 pass；PWA carrier 增强 not-run。 |
| SDK-X06 | 合约 estimate/params/list/status/analytics/history/query | Tools 实际部署/调用后查询同一已确认合约；参数和费用可解释，列表/历史包含实际 txid；未知模板/合约/地址、错误资产和 revert 明确失败且无资金变化。 | SDK-W24 + Tools 第 7/8 项代码完成：已部署 Exchange 的 estimate/params/fee/list/info/state/history，以及实际 Core STP 合约的 status/analytics/地址/历史对账和错误请求不广播。W24 定向 pass；PWA Tools 新增项 not-run。 |
| SDK-X07 | reservation/resume/reopen/rebuild/restore、getChannelStatus、punish/force-close/sweep | 已签 pending Splicing 冷解锁恢复相同 reservation/通道/交易，完成后通道仍可读；未知/未安装通道的状态查询有界返回且后续 WASM 调用可响应；重载与丢失回执不重复扣款/广播；不安全强退计划拒绝；真实 commitment/CSV/回收最终权益与预定费用一致。 | 既有冷恢复及未知通道断言保留；SDK-W23 补未知安全目标拒绝，TestSDKWalletPWAForceCloseCSV 已写真实 commitment、冷重开、CSV 早期拒绝/恰好成熟的预检边界与最终权益/费用/回调/重载对账。真实 revoked commitment punish 广播未接入本浏览器组，不能当已覆盖。 |
| SDK-X08 | mint/deployTicker/DeployRunes/inscribeName、referrer | 合法实际提交、确认、索引与供应/费用一致；错误精度、尾额、已占用 DID、无所有权推荐人不得签名/广播。 | SDK-W25 写错误边界；Mint 第 7～9 项写 ORDX/BRC20 部署→真实签名 commit/reveal→确认→权限→铸造→供应/余额→重载，以及 DID 实际铭刻/确认所有权/重复拒绝。复用共享铭文解析，只支持开放整数发行、ORDX N=1 和四字符 BRC20；有精度/限制发行、Runes/推荐人成功专项未接齐。直接 InscribeName 未做名称合法性校验，W25 保留拒绝断言；生产修复须审核。 |
| SDK-X09 | RGB11 address/proxy/pending-resume、minerUnstake | 地址/proxy 使用实际 DKVS/传输/ACK，刷新恢复同一请求、不重复广播，取消回收预留；合法解质押经链确认且金额/角色一致。 | SDK-W26 写 RGB 错误请求/续跑保持状态；Node 第 3～5 项写真实父 Core 拒绝、独立 Miner 解质押及独立 Core 解质押。已有原生 RGB 地址/Proxy/冷续跑代码保留；PWA 正常地址/Proxy 入口与相应 WASM 成功专项仍缺。 |

上述 SDK-X 系列完成前，本文不宣称全 196 个接口的功能和边界已经验收。当前准出命令只能约束已接入的必跑 case，完整功能准出还必须关闭这些公开缺口。

### 2026-10-09：SDK 恢复修复已批准实施，串行复验中

前述“待审核/未实施”为诊断当时状态。用户现已批准三文件方案：`sdk/wallet/db.go`、`sdk/wallet/interface.go` 恢复已签 pending Splicing 的运行引用；`sdk/wasm/main.go` 将状态查询复用既有 Promise 包装。没有新增持久状态或公开接口。`gofmt`、`git diff --check` 通过，尚不等于业务通过。

- 先补现有原生夹具的失败回归，由同一 GPT 6 Luna max agent 运行：in/out 共 8 个持久阶段全部因冷解锁缺失通道而失败，实际 Go 退出码 1，6.683 秒；日志 `sdk-splicing-native-before-fix.log`，2271 输入前后清单 SHA256 `a20e69c2` 前缀一致。
- 已补边界回归：旧 generation、非 READY、钱包/子账户/payment key/通道地址错误、reservation id/phase 不匹配、损坏通道 hash、所需交易缺失、恢复 Anchor 身份错误及另一 pending 操作冲突均拒绝且不改数据库；现有 busy runtime 保持、协商未签阶段跳过、普通 Expand 与 RecoverAscended 可恢复。原生夹具只证明引用/字节保持，真实签名/广播/资金由浏览器独立验收。
- 执行顺序：原生定向回归 → PWA 类型检查 → 原开通/注资重开两项 → SDK WASM 14 → 完整钱包 82 → 发布态 8 → 账户 66 → CoreModules（含 RGB 恢复 9）→ Guardian 2。单 agent 串行，逐组取编译输入前后快照，任一组失败停止后续并由主 agent 分析。
- 当前新快照业务结果尚未完成，整体未满足准出；历史 pass/fail/not-run 和 SDK-X 缺口保留。
- 修复后原生第一轮实际退出码 1，**编译失败、业务 not-run**：新增冲突夹具的状态常量类型未显式转换。2271 输入前后一致（SHA256 `58a49b85` 前缀），日志 `sdk-splicing-approved-native.log`。已修测试转换；空 PreTx 的校验改为加载后内存测试，因为 gob 无法持久化切片中的 nil 交易指针；拒绝及数据库不变断言保留。第二轮重新执行完整原生组，不能将该编译失败算作恢复功能失败。
- 第二轮原生 Go 退出码 1，33.491 秒：冷解锁 8 个阶段全部 pass；三个错误身份子项尚未进入恢复断言，因修改测试字段后未重算静态 root，被 fixture 写入校验拒绝。其他所选恢复/保护项通过。2271 输入前后一致（SHA256 `cc90cd37` 前缀），日志 `sdk-splicing-approved-r2-native.log`。只修测试 root 准备，生产校验不变，第三轮重跑；PWA 后续组仍 not-run。
- 第三轮原生 **20/20 顶层测试 pass，Go 退出码 0，20.381 秒**；包含 8 个冷恢复阶段、两种 Expand、16 个持久错误边界及空前置交易内存校验，所有子项通过。日志 `sdk-splicing-approved-r3-native.log`、对应 verdict TSV，2271 编译输入前后一致（SHA256 `734e6cc6` 前缀）。原生结果只覆盖恢复/保护，不替代 WASM 或真实浏览器业务。
- 本次 PWA 类型检查 `npx vue-tsc --noEmit` 实际退出码 0；2038 输入前后一致（SHA256 `f94a8048` 前缀），日志 `pwa-typecheck-splicing-approved-r3.log`。进入开通/注资重开的实际浏览器复验。
- 真实浏览器开通＋BTC 注资重开 **2 pass / 0 fail / 0 not-run，Go 退出码 0，132.358 秒**；注资重开后恢复同一操作，L1 txid `e12b2b4dac9604274fdd2f8cdf83bcbf41b1c3355590a0d63844808067b3fcbe`，原 Anchor `fe2426bc67bb89554614bc381983fd584104347fdc19a1cd0844d7f7fd34cc01` 确认于高度 13，原资金/签名/重开断言全部通过。日志 `pwa-splicing-focused-approved-r3.log`。这是定向复验，完整 SDK/PWA 门禁继续执行。
- 定向浏览器组前后 2038 源清单一致（SHA256 `f94a8048` 前缀）；含三个临时 overlay 输入的 2041 清单也一致（`eafbe5ca` 前缀）。节点日志单独保存在 `nodes/pwa-splicing-focused-approved-r3/`，本轮自建进程已退出、共享锁释放。随后启动当前 SDK WASM 14 项，尚未形成新 verdict。
- 本次 SDK WASM **14 pass / 0 fail / 0 not-run，npm/Go 退出码 0，192.806 秒**。新 SDK-W14 为 pass（8446ms），库回退返回 CS_UNKNOWN 后版本/地址/目录调用正常；原 13 项同时重新通过。日志/逐项 TSV/progress/run JSON 前缀 `sdk-wasm-splicing-approved-r3`，节点证据 `nodes/sdk-wasm-splicing-approved-r3/`；2038 输入前后 SHA256 `f94a8048ac65297188c43a9ba9783c31a505f3c016f027e72f8309b0b264ad3e`。本组已清理并释放锁，下一组原始完整 PWA 82 项，不使用定向 overlay。
- 原始完整 PWA 组已出现新失败：前四项 POS 通过，第五项 asset Deposit 在读取 Anchor 数量时报 `expected an exact integer amount`，依赖项停止、独立功能组继续。只读链证据显示实际 Amount 为 `{Precision:0,Value:"600"}`，与预定 600 相同；失败来自测试把原生 Decimal 对象交给平面字符串整数解析器，钱包/UI 增量断言尚未执行。单文件测试修复方案 `proposed-pwa-deposit-amount.md` 和补丁 `.patch` 已准备并向用户请求审核，未应用；当前运行结束后才允许更改输入。生产 SDK 无新修改，后续发布/账户/CoreModules/Guardian 暂停在本组之后。
- 完整 PWA 组现已结束：**61 pass / 1 fail / 20 not-run，npm/Go 退出码 1，1198.088 秒**；唯一失败是上述测试 Decimal 读取。独立功能组全部通过，JS 无未处理异常 case 通过；Go 在 Node 非零处停止，随后五个节点 postchecks 未执行。2038 输入前后 SHA256 `f94a8048ac65297188c43a9ba9783c31a505f3c016f027e72f8309b0b264ad3e`，日志/逐项表/节点证据 `pwa-functional-splicing-approved-r3`；自建进程退出、共享锁释放。未应用待批补丁，后续组 not-run，整体验收未通过。

### Deposit 测试读取修复已批准（2026-10-09，定向复验通过）

用户已同意 `proposed-pwa-deposit-amount.patch`。在上轮证据保存、进程退出和共享锁释放后，精确应用到现有 `pwa/scripts/verify/pos-pwa-e2e.mjs`：仅要求 Anchor Amount.Precision=0、Value 是字符串，并沿用 asInteger/BigInt。入账固定预期 600、交易/费用/脚本/签名/确认/UI/重载/节点检查保持原断言；没有生产代码改动或新框架。JS 语法、`git diff --check` 通过。

批准时曾计划由同一 GPT 6 Luna max subagent 从原始 PWA 82 项重新开始，并继续后续组；该计划已被用户后来的定向测试要求取消。最终只完成下述五项 Deposit 链路验证，逐项结果与证据已保存。

- 一次性脚本读取边界检查 **7/7 pass，Node exit 0**：真实 Anchor 600、超过 Number 安全范围的字符串保持精确；非零精度、非字符串、负数、小数、缺失值均拒绝。日志 `pwa-deposit-amount-reader-approved.log`，该检查不计入 82 个浏览器业务 case。
- 新完整 PWA 已启动，日志 `pwa-functional-deposit-approved.log`。2038 输入前清单 SHA256 `1bcb70b2` 前缀；与上一轮 `f94a8048` 清单逐文件比较，仅批准的 `pos-pwa-e2e.mjs` 变化（当前脚本 SHA256 `0532ec8c71feac981d158049f07264aba910e155beed45632f8a5a3a5e135acd`），SDK 生产及直接 WASM 组执行路径未变。
- 用户随后明确收窄为“只测试对应 case，不要全部测试，快点收尾”。已取消完整 82 及后续发布/账户/CoreModules/Guardian 的复跑计划，当前全量按用户要求收尾，不把中止算作新产品失败。仅以既有筛选/临时 Go overlay 运行 Deposit 目标及四个必要前置（开通、BTC 注资、激活、资产注资），业务断言不变；其余 77 项不执行。最终结论只适用于该修复路径，完整准出仍保留未完成状态。
- 收窄中止的全量运行已保存证据：开通/BTC注资/激活 3 pass，资产注资运行中断，目标 Deposit 未开始；Node 收到 TERM 后返回 143，npm exit 1 为用户中止，不新增产品 fail。日志与节点/进度/清单证据 `pwa-functional-deposit-approved.scope-stop.*`；2038 源前后 `1bcb70b2` 前缀一致，自建进程退出、共享锁释放后启动五步定向链。
- 最终定向链 **5 pass / 0 fail / 0 not-run，Go exit 0**，Go 顶层测试 321.93 秒、包用时 324.182 秒。逐项 TSV：开通 24192ms、BTC 注资重开 21241ms、激活 74737ms、资产注资 12300ms、目标 Deposit 20388ms。原 Anchor `34743db8102b3fa0ba67b9daab04ff8142250689defe182f03760d0cf91a5a7e` 在高度 35 确认，预定 ORDX 入账 600 的链上数量、SDK 资产/聪余额、PWA store 与页面显示均通过。证据前缀 `pwa-deposit-focused-approved`。
- 边界：重载验收属于前置开通/BTC 注资两项，目标 Deposit 没有独立的入账后重载步骤；Go `control.failures` 为空检查已通过，筛选模式跳过 activation/rotation/substitution/restart 四项全量结束检查。本阶段只关闭已批准修复的定向验证，其余 77 项及后续组未复跑，SDK-X 功能缺口保持登记。
- 2038 输入清单前后一致，SHA256 `1bcb70b2c04d5517c8fb28a9adf7f72f2d9a40ca0cfb1bf1fdcf86dacfe2ebc6`；含三个临时筛选输入的 2041 清单也一致，SHA256 `0da3f857738f99cdab6ea8d8fbdfea68ad20e3f45539244b367468efb731a163`。本轮 Go/浏览器 runner/Bootstrap/Core/Miner 均退出，共享锁无持有者；节点 stdout、逐项 TSV、progress、run JSON 和快照已保存。
