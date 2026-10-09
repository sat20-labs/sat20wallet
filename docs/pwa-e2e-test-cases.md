# PWA 层 E2E 用例清单与评估

### 后续修复与复验状态（2026-10-09）

后续任务已继续执行此前被 SDK 失败阻断的计划：新增 PWA 16 项按最新逐项证据全部通过（分次运行），Node 完整组一次 **5/5**，独立 CSV 强退与两个前置一次 **3/3**。另有原 PWA 资金/DID 27 项整组通过。修复实际的资产审核、stub 查询、ORDX dust 找零、公开 ticker 缓存与 Core 通道签名 peer 问题；新增脚本的入参/返回值、txid 选择器、BRC20 账本、激活前置与日志类型按既有接口修正，原 5 秒及资金/签名/确认断言保持。

此前“未执行/待审核”段落为历史状态；**本轮未重新执行 90 项完整钱包门禁**，分次通过不代替全量同一快照验收。详见 [整合与复验记录](./rgb11-mainline-integration-2026-10-08.md)，证据 `/private/tmp/sat20-e2e-followup-20261009/final-followup-summary.json`。未提交或推送，staged 保持原样。

整理日期：2026-10-09

本文整理 `sat20wallet/pwa` 的真实浏览器验收入口、已登记业务 case、当前覆盖边界和增删建议。SDK 原生 API、节点和协议模块用例见 [SDK E2E 用例清单](./sdk-e2e-test-cases.md)。

### 测试代码补充快照（2026-10-09，运行前）

按用户要求，本轮只补测试代码，不运行用例。钱包功能清单从 82 项扩展到 **90 项**：Tools 6→8、Mint 6→9、Node 2→5。新增真实 ORDX/BRC20 部署与铸造、DID 铭刻/所有权/重复拒绝、确认索引与重载、合约 WASM 查询对账与拒绝边界、父 Core 有 child 时拒绝解质押、独立 Miner/Core 实际解质押。既有 asset Deposit 补入账后的独立重载，核对余额保持且不重播交易；RGB 发行补垃圾发送不得消费已确认 carrier 的真实 WASM 断言。

另写 **1 项独立强退/CSV 用例**，通过 `TestSDKWalletPWAForceCloseCSV` 选择开通、BTC 注资重开两个必要前置和该用例，复用原资金脚本与节点夹具。它包含真实 commitment 广播/确认、冷重开、release 后旧 callback 不派发、新 manager 回调对账、早期 sweep 被相对高度拒绝、恰好满足 BIP68 区块边界时预检允许、monitor 成熟后签名回收、操作前权益减精确费用、最终余额及重载不重播。已接入正式 `verify:release-gate`，与合作关闭分开使用新的临时通道。

**本轮新增和增强断言全部 not-run。** JS 语法、Go 格式和 diff 静态检查不代表 E2E 通过；下文 82 项和 Deposit 5/5 等结果属于此前快照。生产代码未修改。RGB 地址/Proxy 正常页面入口、Runes/推荐人成功发行和真实 punish 浏览器演练，仍登记为剩余边界。当前发行夹具仅支持开放、整数 ORDX/BRC20，不提供完整生产 L1 协议验证保证。

### 新增用例定向验证（用户后续授权）

编码收尾后，用户要求只跑新增例子。当前执行计划为 SDK 18 项 → PWA 16 项 → 独立强退 3 步，首个非零组结束即停止后续。PWA 16 项包括新增 Tools 2/Mint 3/Node 3、增强的 Deposit/Carrier 各 1、四个 Deposit 业务前置及两个原 stake 前置；独立强退另用新通道执行两个必要前置和新 CSV case。复用原 helper/临时 Go overlay 与真实断言，不运行 90 项全量或其他发布/账户/RGB 恢复/Guardian 组。具体 pass/fail/not-run 和证据在执行后补记；生产问题仍先交用户审核修复方案。

首轮 SDK 定向组 **7 pass / 11 fail，Go exit 1**；测试约定修正后的 7 项为 **6 pass / 1 fail，Go exit 1**，再仅 W15/守卫执行为 **1 pass / 1 fail，Go exit 1**。六项修复前合并各次逐项最新证据，18 个已选择 SDK case 为 **12 pass / 6 fail**，不是一次全量复跑。用户同意六项最小修复后，第一段实际为 **4 pass / 1 fail / 2 not-run，Go exit 1**：W02/W15/W18/W22 通过；W23 批量边界拒绝后，未知通道 `safetySnapshot` 同步 WASM 回调等待 IndexedDB 导致超时。诊断无限等待已用现有脚本的 2 秒上限修正，仅补跑未开始的 W25/错误守卫为 **1 pass / 1 fail，Go exit 1**：负 ORDX 等前置拒绝通过，但非法 DID 名称仍提交 commit/reveal；守卫仅证明该两项组无未处理浏览器错误。七项两次定向运行的逐项最新结果为 **5 pass / 2 fail**，新增 SDK 18 项合并历史证据为 **16 pass / 2 fail**。两处新生产问题先给最小方案，待用户审核；因此本轮 PWA 16 项和独立强退 3 步仍为 **not-run**。根因、实施范围和状态见 [SDK 定向结果](./sdk-e2e-test-cases.md#已定位问题与六项批准修复2026-10-09)。PWA 的历史通过结果不能替代这些新增断言的运行结果。

### 最近已运行快照（2026-10-09，补充代码前）

**对应修复定向验证通过，完整发布准出未完成。** SDK WASM 14/14、原生恢复回归及开通/注资重开通过；Deposit 目标和四个必要前置 **5/5 pass，Go exit 0**。修复前原始钱包 82 项为 **61 pass / 1 fail / 20 not-run**；该 Deposit JSON 读取失败现已修正，原入账与 UI 断言通过。完整组、发布态、账户、CoreModules、Guardian 未在当前快照重新验收。

单文件测试修复已获用户同意并应用。按用户“只测试对应 case”的要求完成定向链后，本阶段收尾，不启动其他组。证据与增删建议见下文；定向结果只适用于对应修复路径。

## 运行入口与真实边界

从 PWA 目录运行当前发布准出门禁（SDK WASM、生产构建、钱包、独立强退、账户、CoreModules、Guardian 串行执行，任一组失败停止）：

```bash
cd /Users/yingfeng/github/sat20wallet/pwa
npm run verify:release-gate
```

原 `npm run verify:wallet-e2e` 保留为钱包/账户/CoreModules/Guardian 的组合入口，不包含新 SDK WASM 和生产构建专项，不能单独代替上述发布门禁。

只运行钱包功能组（包括下表 90 个钱包 case）：

```bash
npm run verify:wallet-e2e:functional
```

独立强退只选择必要前置与对应 case：

```bash
npm run verify:wallet-e2e:force-close
```

上述命令是后续执行入口，本轮没有启动。

完整门禁由 `sdk/e2e` 启动临时 Bootstrap/Core/Miner、真实 SatoshiNet Indexer、PWA Vite 服务和 Chromium；页面加载本次编译的 SDK WASM，使用真实钱包目录、密码学、签名、IndexedDB、RGB provider 和 RPC。L1 Indexer 是受控测试边界：它提供种子资产、交易接收、签名/输入/价值校验和确认推进，但不代表完整的生产 L1 协议索引服务。用例通过需看业务断言及逐项 verdict，不能把“页面能打开”、编译通过、预览或取消算成成功交易验收。

## 钱包功能组：90 个登记 case

`TestSDKWalletPWAConnectedBrowser` 运行下表。90 项均有执行代码，是当前登记数；补充前 82 项的历史结果不能替代当前清单。

| 功能组 | 数量 | 当前登记的业务范围 | 主要脚本 |
| --- | ---: | --- | --- |
| 资金 / POS / 通道 | 25 | BTC、ORDX 收发和高级多输出；私有/公共通道开通、入金、锁/解锁、扩容、出金、合作关闭；POS v2 激活、出块轮换、Miner 替补和恢复；安全快照与强退确认取消。 | `pos-pwa-e2e.mjs` |
| L1 资产发送 | 3 | 使用既有 BRC20 transfer 铭文发送、从余额创建 transfer 后发送、带精度和找零的 Runes 部分发送。断言交易、余额、费用、取消和重载结果。 | `wallet-l1-assets-e2e.mjs` |
| 钱包基本操作 | 14 | 创建/导入、收款地址与剪贴板、公钥、助记词认证、锁定 UTXO、签名预览、语言/安全偏好、自动锁定、操作日志和推荐人拒绝/取消边界。 | `wallet-basic-e2e.mjs` |
| DApp | 8 | 未授权拒绝、显式账户授权、消息签名/取消、撤权；独立 Bitcoin PSBT 签名；单独授权并广播同一签名交易、确认和防重播。 | `wallet-integrations-e2e.mjs` |
| RGB | 11 | NIA/IFA/UDA 发行、标准合约导出/导入、OOB invoice、发送签名、接收验证、恢复后广播、供应守恒、取消预留和 UDA 实际转移；新增明确指定 carrier 的垃圾发送拒绝，余额/锁/广播及 unspent 保持。 | `wallet-rgb-e2e.mjs` |
| WebAuthn | 4 | PRF 凭据创建和加密绑定、刷新后真实认证器解锁、改密后重新登记、删除后恢复密码解锁。 | `wallet-biometric-e2e.mjs` |
| Tools | 8 | 既有 Exchange/Faucet/EVM/Agent；新增真实新 Exchange 的 estimate/params/fee/list/info/state/history，以及 Core STP status/analytics/地址/历史对账；非法请求不广播。 | `wallet-tools-e2e.mjs` |
| Mint / DID | 9 | 既有参数审查/取消/精度/资格/DID；新增 ORDX/BRC20 ticker 真实部署与铸造、签名 commit/reveal、确认索引、供应/余额及重载；DID 确认所有权和重复名称拒绝。 | `wallet-mint-e2e.mjs` |
| Mining | 2 | WASM worker 对受控 L1 工作量独立求解/提交；停止后不再请求 job，刷新保留配置。 | `wallet-mining-e2e.mjs` |
| Core / Miner 质押 | 5 | 既有两项独立钱包实际质押；新增运行中的父 Core 有 child 时拒绝解质押、独立 Miner 解质押、独立 Core 解质押。成功路径直接调用真实 WASM local action 并核对签名交易的确认、角色退出及精确 stake 资产返还，尚不代表解质押页面流程。 | `wallet-node-e2e.mjs` |
| 未处理浏览器错误 | 1 | 完成功能组后无未处理浏览器异常。 | `account-management-e2e.mjs` |
| **合计** | **90** | 本轮新增 8 项；独立强退另计 1 项，并复用本表两个必要前置。 | `TestSDKWalletPWAConnectedBrowser` |

依赖前序准备的交易应保留在同一个业务 case 中；独立资产/资金用例使用独立钱包身份和预置余额。前序失败后依赖项记为 `not-run`，不得用失败后的余额补下一项。最终断言基于执行前已知金额、精度、费用和钱包权益。

## 账户管理与 RGB 恢复浏览器入口

完整门禁还运行以下独立入口，账户恢复与 90 项钱包功能分别报告：

| Go 入口 | 登记范围 | 主要 case 家族 | 状态口径 |
| --- | ---: | --- | --- |
| `TestSDKAccountPWAConnectedBrowser` | 66 项，其中含 4 项共享 WebAuthn | 首次创建/导入、存储授权、锁屏写入保护、2-of-2/2-of-3 与 Guardian、恢复预览/取消/提交、密码重置、离线重载、多设备合并、远端删除回退、IndexedDB 失败保持原状、提交响应丢失重试。 | 以当前脚本要求的 case 名和运行 verdict 为准；过去一轮的部分结果不是当前快照结论。 |
| `TestSDKCoreModulesE2E` 的 RGB 浏览器阶段 | 9 项 | 非空 RGB 所有权 proof/lock 恢复、远端导入重试、provider 持久化失败后同库恢复、AUTOPAY 费率和重载边界。原生 SDK 的 RGB 传输与恢复另见 SDK 文档。 | 浏览器用例和 native SDK 子用例分别报告，不互相代替。 |
| `TestSDKAccountGuardianIdentitySurvivesAccountRecovery` | 独立跨设备补充 | Guardian 用根助记词或恢复材料换机后保持同一身份，旧好友分片可用于两种恢复组合。 | 属于完整 npm 门禁，不计入 90 项。 |
| `TestSDKAccountGuardianPaidPWABatchReview` | 独立跨设备/付费补充 | Guardian 自身 temporary paid 托管续费，以及原 AUTOPAY 交易中断和丢失回执后的续传。 | 属于完整 npm 门禁，不计入 90 项。 |

账户脚本保留单项 JSON verdict；账户锁、同步失败、失败后不变、退出恢复会话清理等负向边界不能只以页面没有报错作为通过。完整 PWA 入口会并列执行这些专门组，不应把 SDK Go API 成功当成页面流程已经验收。

## 暂未覆盖或不能由本门禁推断的边界

| 路径 | 当前边界 | 评估建议 |
| --- | --- | --- |
| 安装版 PWA / Service Worker 更新 / 离线冷启动 | 功能门禁仍使用 Vite DEV；本轮新增 8 项生产构建、真实 SW、升级及离线冷启动用例。 | 必须实际通过新增门禁；两个当前源码真实构建的升级不代表所有历史版本迁移，移动系统安装交互仍需设备验收。 |
| L1 真实 Mint 和索引 | 开放整数 ORDX、N=1 及四字符整数 BRC20 已写成功流程；DID 从已确认真实铭文及当前资产输出投影所有权，不增加持久状态或注入成功余额。原 PWAMINT 固定权限仅用于既有审查 case。 | 当前新增代码 not-run。有精度/限制发行、Runes 远端部署及推荐人成功注册/绑定仍待补充。 |
| 通道强退及 CSV 后回收 | 独立测试代码已接入正式门禁；受控 L1 验 Bitcoin VM 与区块型 BIP68，推进连续空块；不覆盖时间型 BIP68 或真实 BTC 网络传播。 | 当前 not-run；实际通过后才能关闭该发布门禁。真实 revoked commitment punish 演练仍未接入浏览器组。 |
| RGB 地址模式 / pending 续跑 | SDK 原生用例已有直传、ACK 与恢复覆盖，PWA 尚未完成地址模式和等待 ACK 重载路径；目前没有确认正常页面启用入口。 | 先确定真实用户入口，再决定是否增加页面 case；不要在测试中直接开启内部开关并宣称用户路径通过。 |
| RGB Proxy 收发 | 页面门禁以 OOB 为主，native provider/HTTP 测试不等于 PWA proxy 收发。 | 只有明确 proxy 页面能力和可复用本地服务后再补页面 case，不为此单独造服务框架。 |
| 成功主网切换、真实模型/oracle、BTC 全网挖矿收益 | 当前环境隔离于 testnet，不能由测试网络或假服务推导主网/真实模型/收益结果。 | 不纳入默认自动门禁；若发布需要，应使用单独审批和受控验收计划。 |

## 最近运行状态

根据 `pwa/scripts/verify/WALLET_ACCEPTANCE.md` 中截至 2026-10-08 的记录：

- 当时钱包功能组静态登记为 82 项；这 82 项尚未在那次完整运行中执行。当时新增的 6 项已加入已有入口，但没有完成浏览器业务运行；本轮另补 8 项，现为 90 项。
- 最近一次全量 SDK E2E 运行失败并在 100 分钟总预算触发超时。该次使用更早清单：账户浏览器 57 项中 32 pass、1 fail、1 项已开始但无完成结果、23 未开始；RGB 恢复 9 项中 3 pass、2 fail、4 未开始；当时的钱包功能 74 项全部未开始。
- 后续代码、WASM 和 case 清单有更新；旧运行数字只作历史记录，不能外推为当前实现的通过率。
- 2026-10-08 有 JavaScript 语法检查和 SDK E2E 包编译通过的记录，但业务 case 尚待同一源码快照复跑。
- 首次整理未运行测试；本轮增补发布态门禁并发起分组执行，完成前不认定当前发布通过。

## 增加与精简建议

### 建议优先补齐

1. **下次正式发布验收执行完整门禁。** 新增的 BRC20、Runes、DApp PSBT/广播、UDA 转移用例已经进入必跑清单；发布时需要当前源码的一致运行结果。本阶段按用户要求仅完成对应修复的定向验证，不继续全量测试。
2. **保留新补 ORDX/BRC20/DID 成功断言，继续补 Runes 和推荐人。** 本轮已有真实提交、确认、索引与重载代码；有精度/受限发行、远端 Runes 部署与推荐人完整注册/绑定仍需等价独立证据。
3. **Service Worker 更新和通道强退使用独立验收组。** 两者已分别接入发布态和独立强退命令，均属于 release-gate；避免与合作关闭共用已结束的通道。
4. **RGB 地址/Proxy case 以产品入口为前提。** 先确认用户可操作的真实页面路径和现有本地服务，再决定补 case，避免测试专用开关替代产品流程。

### 可精简的执行重复

- 完整 npm 门禁中，4 条 WebAuthn case 同时属于钱包功能组与账户浏览器入口。建议保留同一套断言和单独可运行能力，但完整门禁只执行一次；如果账户组需要独立运行，再由账户专用命令包含它们。
- 2026-10-08 已合并重复的定向浏览器入口。后续继续复用现有 `--wallet-cases` / usage case 选择和 verdict，不为同一业务场景另建 Go runner 或重复脚本。
- 不建议合并具有不同资金状态或故障时序的 case：例如签名与广播、成功交易与取消、已准备交易与丢失回执、RGB 发送方与接收方验证。压缩入口可以，压缩断言会丢掉独立保证。
- 不建议把主网切换、真实 BTC 收益或真实 oracle 混入默认 CI；用独立验收替代依赖不稳定外部状态的伪 E2E。

## 维护约定

新增 case 时记录唯一名称、入口、独立初始状态、业务断言和外部依赖；删除 case 时说明它被哪个仍在运行的断言完整覆盖。每次验收更新 `pass`、`fail`、`not-run` 和测试源码快照，不用历史结果或编译结果代替当前通过状态。

## 发布准出门禁（本轮增补）

### 8 项发布态浏览器 case

新增 `TestPWAProductionReleaseE2E`，在临时副本执行普通完整生产构建（含 TypeScript 检查），加载本次源码编译的生产 WASM，**无测试 POS overlay**。验真实 dist、CSP、WASM 完整性加载器和 Service Worker；不降低 CSP，不覆盖工作树 public/generated/dist。阻断外部请求，实际交易另由真实节点功能门禁验证。

| 编号 | 必跑 case | 状态 |
| --- | --- | --- |
| PWA-R01 | current production WASM passes the ordinary typechecked release build | pass（批准 SDK 修复后发布态复验） |
| PWA-R02 | all emitted release assets match size hash MIME and release metadata | pass（批准 SDK 修复后发布态复验） |
| PWA-R03 | production onboarding boots with CSP and without development hooks | pass（批准 SDK 修复后发布态复验） |
| PWA-R04 | real service worker installs a complete verified release cache | pass（批准 SDK 修复后发布态复验） |
| PWA-R05 | offline cold page boots the same release and persisted local catalog | pass（批准 SDK 修复后发布态复验） |
| PWA-R06 | update preserves the running page then switches and retires the old cache after READY | pass（批准 SDK 修复后发布态复验） |
| PWA-R07 | corrupted WASM rejects startup and cannot activate an incomplete release | pass（批准 SDK 修复后发布态复验） |
| PWA-R08 | production online offline and update flows have no unhandled page errors | pass（批准 SDK 修复后发布态复验） |

检查实际缓存每项 hash/size，等待升级时旧页面不被抢占，新页面 READY 后才退休旧缓存，损坏 WASM 必须拒绝启动和激活不完整发布。升级使用当前源码的两次真实构建，不能代表历史版本的数据迁移；离线资金钱包解锁在账户组验证，此处使用空目录验证生产启动和本地持久数据。

```bash
cd /Users/yingfeng/github/sat20wallet/pwa
npm run verify:pwa-release-e2e
npm run verify:release-gate
```

完整准出顺序复用新增 SDK WASM、发布态及既有完整钱包/账户/RGB/Guardian 入口，任一非零退出拒绝发布。必须同时满足：

- WASM **29 项**、发布态 8 项、钱包 **90 项**、独立强退/CSV **1 项**（另需两个已有前置）、账户 66 项、RGB 恢复 9 项和两个 Guardian 专项的全部登记 case pass。
- 无 fail/not-run/运行未完成；无超时、未处理浏览器错误或节点验收失败。
- 保留源码 SHA256、临时构建和逐项结果；执行时源码改变不能用于当前快照准出。
- 发布范围若涉及有精度/受限发行、Runes/推荐人成功发行、真实 punish、RGB 地址/proxy 或解质押页面流程，必须追加对应专项成功验收。已补整数 ORDX/BRC20、DID、CSV 回收和 WASM 解质押代码，但当前未运行，不提供通过保证。

旧完整入口的总预算为 100m。新 release-gate 复用既有 Go 入口、分别给 WASM 30m、发布态 25m、钱包功能 55m、独立强退 30m、账户 55m、CoreModules 40m、Guardian 60m，串行执行并在任一组失败时停止；避免一个共享总超时让后面的组全部来不及开始。预算上限不是实际耗时，不因超时删 case 后认定准出。

### 增补和精简建议调整

1. 已补直接 WASM 边界和生产 SW/缓存/离线/升级，应纳入必跑。
2. 本轮已补整数 ORDX/BRC20、DID、CSV 回收、私钥/监控导入、L2 PSBT/订单、批发和解质押代码；继续登记有精度/限制发行、Runes/推荐人、真实 punish 和 RGB 地址/proxy 成功专项缺口。复用现有夹具，代码完成不代替运行证据。
3. 完整门禁的 WebAuthn 4 项可只执行一次，同时保留账户组单独运行能力；本轮先保留原清单，不以精简代替完整基线。
4. 原生 SDK 与 PWA 在分别检查接口返回/密码学、页面授权时可同时保留；重复 Go wrapper 可精简，非法参数、错误密码、取消、回执丢失和到账断言不可删。

### 本轮执行结果与待测项

- 2026-10-09 完整发布态验收最终 **8 pass / 0 fail / 0 not-run，退出码 0**；Go 用例 124.86 秒。
- 最终日志：`/private/tmp/sat20wallet-e2e-20261009/pwa-release-rerun2-escalated.log`；执行前后 980 个相关源文件快照相同，清单 SHA256 `1fbc04791cc329c899330ec0a825b777dd7b41e9275b225a54be01bdcb7005bb`。
- 诊断过程保留：首轮旧页面的 ready/active 观察对象错误（5 pass/1 fail/2 not-run）；第二轮测试没有等待首次 SW 激活（3 pass/1 fail/4 not-run），均只修测试等待/观察。一次默认沙箱中 Edge 启动 SIGABRT/EPERM（2 pass/6 not-run），经获准权限运行完整通过。没有为 SW 修改生产实现或放宽业务断言。
- **82 项钱包功能组已结束：40 pass / 9 fail / 33 not-run，退出码 1，Go 用例 1250.411 秒；66 项账户、9 项 RGB 恢复及两个 Guardian 专项本轮尚未运行**。SDK 小数索引的三个接口修复已经用户审核同意并实施，SDK 完整复验 13/13 pass；新 WASM 发布态门禁完整复验 8/8 pass。钱包组失败后停止后续组，由主 agent 分析，仍由同一个 Luna max agent 执行复验。
- npm release-gate 仅完成入口配置；完整串行组合尚未执行。新增分组预算配置发生在上述发布态业务运行之后，没有改变生产代码；不能把 8 项发布壳通过宣称为完整 PWA 准出。
- 批准 SDK 索引校验修复后，使用新 WASM 再跑发布态门禁，结果 **8 pass / 0 fail / 0 not-run，退出码 0**，Go 包 552.267 秒；日志 `/private/tmp/sat20wallet-e2e-20261009/pwa-release-approved.log`。此前通过记录继续作为历史证据保留。
- 该次宽范围 SDK/PWA 清单检测到外部变动：`sdk/wallet/rgb11_regtest_interop_test.go` 和 `sdk/wallet/rgb11_transfer_test.go`。两者不参与生产 WASM `go build` 或本组 `./e2e` 编译，发布态输入未受此变动影响；不能声明整个 SDK/PWA 目录在该次运行期间无变化，也未回退这些文件。后续 CoreModules 会编译 wallet 测试，因此在该组开始前重新记录快照。
- 钱包 82 项组执行前后 2038 文件清单一致，覆盖 SDK/PWA 及 indexer、satoshinet、transcend 的 Go 源、module files、节点/plugin 构建脚本与配置；排除 review-evidence、生成物、Git、node_modules。清单 SHA256 `ae88036ddf80abe449eaca07ee54129937f379b3ed3fcc53875f1c6c60e67c1b`。逐项结果 `/private/tmp/sat20wallet-e2e-20261009/pwa-functional-approved.verdicts.tsv`；完整日志 `pwa-functional-approved.log`；节点日志 `nodes/pwa-functional-approved/`，均在同一证据目录。

| 功能组 | pass | fail | not-run |
| --- | ---: | ---: | ---: |
| POS、Funds、Escape | 1 | 1 | 23 |
| L1 资产发送 | 3 | 0 | 0 |
| 基础钱包及运行错误检查 | 15 | 0 | 0 |
| DApp | 8 | 0 | 0 |
| RGB | 1 | 1 | 9 |
| WebAuthn | 4 | 0 | 0 |
| Tools | 3 | 2 | 1 |
| Mint | 3 | 3 | 0 |
| Mining | 2 | 0 | 0 |
| Node | 0 | 2 | 0 |
| 合计 | 40 | 9 | 33 |

### 钱包组新失败：注资进度显示与冷启动恢复（两次修复已批准实施，复验中）

- `POS PWA: opening confirms the signed Anchor after pending reload` 已 pass；紧接的 BTC splicing-in case fail。注资已真实广播，受控 L1 尚待本 case 确认；SDK 与页面的 `channel.status` 均为 16，页面没有出现必需的 `Splicing in`。后续依赖 POS/资金 case 尚未执行，独立功能组按原策略继续。
- 根因是状态合同不一致。`sdk/wallet/channel_status.go` 仅定义通道生命周期；注资进度由 `channel_resv_status.go` 的 `RS_SPLICINGIN_*`（0x200～0x203）和提款 `RS_SPLICINGOUT_*`（0x300～0x303）持久化到 reservation。`pwa/components/wallet/ChannelCard.vue` 仍按旧通道值 33/51 显示状态，因此无法表现当前 SDK 的 pending 操作。不能通过删掉 pending/reload 断言使该 case 通过。
- 最小草案仅改 PWA `store/channel.ts` 和 `ChannelCard.vue`：复用 `allReservations`，匹配当前 channelId 并从未完成 reservation 派生临时显示值；保留 SDK 通道状态，使用现有遮罩显示 pending。成本是一条现有接口读取和一个显示字段，无新持久状态、接口或后台任务。
- 用户于 2026-10-09 审核同意 `/private/tmp/sat20wallet-e2e-20261009/proposed-pwa-splicing-status.patch`；已原样应用两个 PWA 文件，实际 diff 的增删内容与批准补丁一致，`git diff --check` 通过。未完成 reservation 从当前 channelId 派生 `pendingSplicing` 临时显示值，READY 的注资/提款仍显示现有遮罩；确认完成后下一次读取自然清除该值。
- 同一个 Luna max subagent 已收到复验指令：现有 TypeScript 检查，开通＋BTC 注资及重载定向两项，SDK WASM 13 项、完整钱包 82 项、发布态 8 项、账户 66 项、CoreModules（含 RGB 恢复 9 项）、两个 Guardian 专项；串行执行，任一组失败则停后续组并交回主 agent 分析。新日志使用 `splicing-approved` 后缀，定向日志 `pwa-splicing-focused.log`。本节此时尚未认定上述新运行通过。
- 修复后的 `npx vue-tsc --noEmit` 已直接运行并取得编译器实际退出码 0，无诊断；日志 `pwa-typecheck-splicing-approved.log`。最初 tee 管道的退出码未计为编译器结果，已重跑确认。前后 2038 输入清单一致，SHA256 `fab156b54a9c1cf8994fcb3e99c5edce88b00db9ad9736023ee9459f6e9c821a`。
- 显示修复首次定向复验 **1 pass / 1 fail / 0 not-run，Go 退出码 1，259.001 秒**：开通 pass；BTC 注资已广播、重开前 `Splicing in` 断言通过，但关闭页面重开解锁后的同一断言失败，Channel 页显示 `Open`。日志 `pwa-splicing-focused.log`，节点日志 `nodes/pwa-splicing-focused/`；2038 输入前后 SHA256 `fab156b54a9c1cf8994fcb3e99c5edce88b00db9ad9736023ee9459f6e9c821a`，含三个临时 overlay 输入的 2041 清单 SHA256 `902a3ca6a96aa848a415eab96cc3d25ede18c3352bbc33a93425a958b605094c`。后续组未启动。
- SDK 静态根因：冷初始化只解码持久 Splicing reservation，其运行时 `Channel` 引用为 nil；冷解锁只恢复 funding、closing、local/remote action。idle READY 初始化因 pending 操作保护而跳过该通道，GetActiveChannelWithId 的 reservation 分支和 monitor 又都依赖非空 Channel，因而无法继续该操作。不能删除 pending 保护或重新开通来替代恢复。
- 增加只读 stage/状态/reservation/身份诊断后复跑，结果仍 **1 pass / 1 fail / 0 not-run，Go 退出码 1，348.828 秒**，但此次是独立的 `stage=submit` 失败：180 秒等待 `snapshot.l1.pending_txids.length==1`，实际为 0；另外诊断 `reservations=[]`，SDK/持久通道状态均为 16。Core 13:14:50 先对 `/splicingin/require` 返回 `can't find channel`，同秒随后才完成 `HandleChannelReady`。该次没有走到重开，不能用它证明或否定冷启动恢复。
- 该次日志 `pwa-splicing-diagnostic.log`、节点日志 `nodes/pwa-splicing-diagnostic/`；2038 输入前后 SHA256 `3b633e9bcf45ea920c158aaa94d75ad18b0cec889d213b2b35c0bc2b0d74def9`，2041 清单 SHA256 `5e35c759bd1b72e003f39bff1e4246e5815c00c45b044e39d746aab156ec135b`。进程已结束，共享锁已释放。
- 准出补充边界：客户端先 READY、Core 尚未 READY 时，页面应明确反馈临时拒绝，reservation/余额/L1 广播保持不变；两端就绪后用户通过相同 UI 再提交应成功。需要用真实服务状态验收该错误与重试路径；恢复诊断中的前置等待不替代这项边界，尚未认定通过，也不为测试新增生产自动重试。
- 为隔离重开恢复，在既有 case 中追加 Core 现有 `/info/channel/{id}` 的只读 READY 前置等待；保留上述两端就绪时序问题，未修改生产流程、重试资金操作或放宽原资金/签名/重载断言。此次 `pwa-splicing-reopen-diagnostic.log` 为 **开通 1 pass、注资 1 运行中断/未完成，Go 退出码 1，706.663 秒**。注资已广播但 renderer 持续占用 CPU，诊断未完整返回；按主 agent 指令停止本轮自建 Node runner 后正常完成节点清理。进度 `pwa-splicing-reopen-progress-55737.jsonl`，采样 `splicing-renderer-sample.txt`；本轮精确节点日志为 `nodes/pwa-splicing-reopen-diagnostic-final/`。
- 该运行前后另有 8 个外部源文件变更：indexer 的 `indexer/interface_kv.go`、`rpcserver/ordx/{handler_kv.go,model_kv.go,router.go}`、`share/base_indexer/interface.go`，SDK 的 `wallet/{restclient.go,restclientmgr.go}`，Transcend 的 `stp/dkvs.go`。已保留前/中止前/结束快照，未回退这些改动。输入不稳定且无最终诊断，该轮不作为当前业务通过或业务失败归因的证据。
- 另确认静态 WASM 包装缺陷：`getChannelStatus` 同步回调中调用 FindChannel，runtime 缺失时会读 IndexedDB 并等待回调；同步 Go `js.FuncOf` 又暂停 JS 事件循环，构成阻塞。相邻 getChannel/getCurrentChannel 已采用异步模式。诊断曾调用这个生产导出，随后 display 读取也没有独立 Node 超时，因而可能无法形成最终结果。仅依据采样不能把上一轮全部卡住归因到具体源码行。
- 新的有界诊断保留逐阶段日志与原业务断言，移除上述同步状态查询并为 display 添加 Node 10 秒时间边界，结果 **1 pass / 1 fail / 0 not-run，Go 退出码 1，266.269 秒**：开通 pass（25748ms）；注资重开 fail（17084ms）。阶段完整走到 reopened，SDK 返回 `getCurrentChannel: channel is nil`；reservation id 1791524574804323、状态 513（0x201）、同一 channelId，SplicingTx/AnchorTx 均存在；钱包 id 1791524545088000、子账户 0 与 reservation 相同，未锁定、地址保持 fixture 身份，Channel 面板显示 Open。确认持久记录/已签交易仍在，缺失的是 SDK 内存通道引用。
- 最终证据：`pwa-splicing-bounded-diagnostic.log`、`pwa-splicing-bounded-diagnostic.verdicts.tsv`、`pwa-splicing-bounded-progress-58561.jsonl`，精确节点日志 `nodes/pwa-splicing-bounded-diagnostic/`。2038 输入前后 SHA256 `ca159fad7c5ec8f809f80eaddadae068026854149ed19c1982bcade45969037f`；含三个 overlay 输入的 2041 清单前后 SHA256 `ab862e7aa5d52aa8c54cb1011c62527d633bbfeea27d64bb3b1fdaad7e394b10`。自建浏览器、三个节点与 runner 已退出、共享锁已释放；后续组未启动。
- SDK 最小修复方案待用户审核，详见 `/private/tmp/sat20wallet-e2e-20261009/proposed-sdk-splicing-rehydrate.md`：拟在 `sdk/wallet/db.go` 和 `interface.go` 恢复已签名 pending in/out 的钱包、通道和 peer 引用，校验身份与 generation，沿用现有 monitor；另仅将 `sdk/wasm/main.go` 的 getChannelStatus 改用现有异步包装，具体未应用补丁 `proposed-wasm-channel-status-async.patch`。总计三个生产文件，无新接口、持久状态或后台机制。当前均未实施，整体仍不满足准出。
- SDK 状态查询的独立直接用例 SDK-W14 已在最终诊断收尾后补入既有入口（合法未知通道 DB 回退、有界返回、后续调用和目录保持）；语法检查通过，业务 not-run。批准修复后 SDK 完整组须跑当前 14 项，不能沿用原 13/13 历史通过作新清单准出。
- 另外静态发现 `pwa/composables/channelStatus.ts` 仍保留旧的支付/注资通道状态枚举，且强制关闭/意外关闭为 256/512，而当前 SDK 是 -1/-2；SDK 的开通可恢复状态 5 也缺显示映射。保留为 SDK-X07 的额外显示边界，待真实状态复验及单独修复审核。

### 导入启动等待失败（已定向复验）

- NIA 首项在导入后的 `Bitcoin` 标签可见断言失败，使用了 Playwright 默认 5 秒等待；失败诊断时路由已经是 `#/wallet`，钱包目录读取完成，页面已有 Bitcoin/SatoshiNet 标签、无 alert。业务发行阶段尚未开始，此项不能记为 RGB 发行成功或发行产品失败。
- 同组独立 UDA 实际转移 case 已 pass（128578ms），包含真实发送、接收、proof/carrier 和重载检查。不能因此推断依赖 NIA 的其他 9 项也通过。
- 在完整组结束、快照和节点证据保存后，将 RGB `fresh`/`reload`、Tools/Mint/Mining/Node 的六处启动标签断言改为现有的 90 秒有界启动等待。保留全部身份、真实发行、供应、签名、广播、proof、carrier 和重载断言；JS 语法及 diff 检查通过。
- Tools 的 Solidity/EVM 部署、Agent 数据源拒绝，以及 Mint 三项也在同一导入后 5 秒标签断言处失败，业务阶段尚未开始；原完整组的六项 fail 继续保留为历史 verdict，新的定向运行分别记录复验结论。
- 同一个 Luna max agent 定向复验 RGB 11、Tools 6、Mint 6、Mining 2，共 **25 pass / 0 fail / 0 not-run，退出码 0，Go 用例 763.876 秒**。六项先前的启动失败均通过，包含真实 NIA 发行、Solidity 编译部署及 EVM 状态/历史验收。使用既有 helper 选项和临时 Go overlay；82 项必跑清单保持完整。定向通过不替代完整钱包门禁。
- 日志 `/private/tmp/sat20wallet-e2e-20261009/pwa-startup-focused.log`，逐项结果 `pwa-startup-focused.verdicts.tsv`，节点 stdout `nodes/pwa-startup-focused/`；前后 2038 文件源码清单一致，SHA256 `6695d637d565ac3e6e2e40a7b3290cab9098a5a3b79303067620bf5e39348d79`。另保留三个 overlay 输入的独立 hash，合并 2041 文件清单前后一致，SHA256 `060abed9c3fd20239b038eacdaa6a95cd050a78844a96f3dbf199006fbed07ea`。测试进程退出、临时节点清理完成。

### Node 组前置条件未满足（待 POS 修复后复验）

- Core、Miner 两项都已真实签名、广播、重载并确认 stake Anchor；失败断言要求 Anchor 高度至少为启用高度 H=32。
- 本次 Core 节点日志在高度 18 添加 Core、在高度 19 添加 Miner；前面的注资显示失败中止了 POS 组，尚未调用既有 `/activate`。因此未满足 POS-v2 验证前置条件，不能将两项直接归为质押产品缺陷，也不能记为通过。
- 保留两项 fail 与严格高度、索引、资产和余额断言；在注资修复后按原顺序重跑完整组验证。当前 25 项定向启动复验不包含这两项，不能用于 Node 准出。

### 2026-10-09：第二次批准后的复验

前述“待审核/未实施”为诊断当时状态。用户已同意 SDK 三文件修复，现已实施：冷解锁恢复已签 pending Splicing 的钱包/通道/peer 关联并验证身份与 generation；状态查询复用既有异步包装。PWA 已批准的显示修复保留。

- 原生新失败回归已先运行：8/8 持久阶段复现缺失通道，Go 退出码 1（6.683 秒）。新增保护回归和修复后复验正在由同一 GPT 6 Luna max subagent 串行执行；没有启动并行节点环境。
- 后续顺序为类型检查、开通/注资重开两项、SDK 14、完整钱包 82、发布态 8、账户 66、CoreModules、Guardian 2。完整钱包保持原 POS 顺序与结束后的节点检查，定向通过不替代完整门禁。
- 每组保存实际退出码、逐项 verdict、编译输入前后快照和精确节点日志。任一失败停止后续，新增生产修复仍须用户审核。
- 当前整体验收未通过；Core 两端 READY 时序边界、公开功能缺口与历史失败继续可见。
- 修复后原生第一轮停于新增测试状态常量类型错误，Go 退出码 1，业务未运行；未启动 PWA 或节点。测试夹具已修，第二轮将从原生完整组开始；证据 `sdk-splicing-approved-native.log`，2271 输入前后一致（SHA256 `58a49b85` 前缀）。
- 第二轮原生冷恢复 8 个阶段全部 pass，但三个错误身份夹具未重算测试静态 root，写入阶段失败，整体退出码 1（33.491 秒）。只修测试准备并重跑第三轮；尚未启动浏览器。日志 `sdk-splicing-approved-r2-native.log`，2271 输入前后一致（SHA256 `cc90cd37` 前缀）。
- 第三轮原生 **20/20 顶层测试及所有子项 pass，Go 退出码 0（20.381 秒）**；包含冷恢复、Expand、身份/generation/交易/冲突拒绝及现有运行对象保持。日志 `sdk-splicing-approved-r3-native.log`，2271 输入前后一致（SHA256 `734e6cc6` 前缀）。进入浏览器复验，整体准出仍待后续组。
- PWA 类型检查本轮实际退出码 0，日志 `pwa-typecheck-splicing-approved-r3.log`，2038 输入前后一致（SHA256 `f94a8048` 前缀）；没有诊断。进入开通/注资两项浏览器复验，成功后继续全量门禁。
- 开通＋BTC 注资重开定向复验 **2 pass / 0 fail / 0 not-run，Go 退出码 0（132.358 秒）**。注资重开后 Channel/reservation runtime 正常关联，同一 L1 txid `e12b2b4dac9604274fdd2f8cdf83bcbf41b1c3355590a0d63844808067b3fcbe`，Anchor `fe2426bc67bb89554614bc381983fd584104347fdc19a1cd0844d7f7fd34cc01` 确认于高度 13，原资金/交易/身份断言全部通过。日志 `pwa-splicing-focused-approved-r3.log`。仅证明这两个修复路径，不能替代完整 82 项及其节点检查。
- 定向浏览器前后 2038 清单一致（SHA256 `f94a8048` 前缀），含三个 overlay 的 2041 清单一致（`eafbe5ca` 前缀）。节点日志 `nodes/pwa-splicing-focused-approved-r3/`，逐项 TSV/progress/run JSON 与本轮日志同前缀；自建进程退出、共享锁释放。开始 SDK WASM 14 项。
- SDK WASM 当前 **14/14 pass，npm/Go 退出码 0（192.806 秒）**，含未知通道数据库回退后续调用及索引边界。证据 `sdk-wasm-splicing-approved-r3` 日志/TSV/progress/run JSON，节点 `nodes/sdk-wasm-splicing-approved-r3/`；2038 输入前后 SHA256 `f94a8048ac65297188c43a9ba9783c31a505f3c016f027e72f8309b0b264ad3e`，正常清理/锁释放。开始原始全量钱包 82 项及 POS 结束后检查；整体准出仍未完成。

### 完整组新失败：Deposit 的测试金额 JSON 读取（已批准修复，复验中）

- 本轮原始 82 项（日志 `pwa-functional-splicing-approved-r3.log`）前四个 POS 用例 pass：开通、BTC 注资重开、激活保持私有通道、v2 ORDX 注资。第五项 asset Deposit 失败于 `expected an exact integer amount`；后续 20 个 POS/资金/安全依赖项停止，独立功能组按原策略继续。当前运行中，尚无完整退出码。
- 本轮只读 `/snapshot` 取得失败项同一个 Anchor：txid `34743db8102b3fa0ba67b9daab04ff8142250689defe182f03760d0cf91a5a7e`，高度 35，钱包输出 600 sats 与 ORDX pwapos 600。其 Amount 是 `{Precision:0,Value:"600"}`，预定 Deposit=600。证据 `pwa-deposit-amount-read-only-evidence.json`。
- 控制器 Anchor 使用原生 `wire.TxAssets`/Decimal JSON，测试却将 Amount 对象交给只接受 string/number 的 asInteger。SDK 和节点 RPC 的 DisplayAsset 平面字符串格式不同，保持原读取。此次还未走到钱包余额/UI/重载断言，不能由 Anchor 数量正确推断整项通过。
- 最小修复只改 `pwa/scripts/verify/pos-pwa-e2e.mjs` 的 Anchor 数量求和：要求 Precision=0、Value 是字符串，再沿用 asInteger/BigInt。保留预定 600、资金/费用/脚本/签名/确认/UI/重载和原 POS/postchecks，不改生产代码、不新增框架。方案 `proposed-pwa-deposit-amount.md`、精确补丁 `.patch` 已提交审核，`git apply --check` 通过但尚未应用。
- 当前组结束并保存节点/verdict/post SHA、释放锁后才能应用已批准补丁并重新跑原始 82 项。任意后续生产修复仍须审核；发布态、账户、CoreModules、Guardian 后续组本轮尚未开始。
- 本轮现已收尾：**61 pass / 1 fail / 20 not-run，npm/Go 退出码 1，Go 包用时 1198.088 秒**。POS 4 pass/1 fail/20 not-run；Assets 3、钱包基本 14、DApp 8、RGB 11、WebAuthn 4、Tools 6、Mint 6、Mining 2、Core/Miner 2、JS 无未处理异常 1 均 pass。Node 质押这次满足 POS v2 前提，两项通过；生产 Deposit 增量仍需修正读数后验收。
- Go `require.NoError(command.Run())` 因 Node exit 1 直接停止，后面的 `control.failures`、`activationChecked`、`rotationChecked`、`substitutionChecked`、`restartChecked` 五项结束检查均 not-run，不能用 JS 无未处理异常代替。
- 完整证据前缀 `/private/tmp/sat20wallet-e2e-20261009/pwa-functional-splicing-approved-r3`：`.log`、`.verdicts.tsv`（全部 82 项）、`.postchecks.tsv`、`.run.json`、`.progress.jsonl` 和输入前后清单。节点 stdout 与 harness failure copies 分别存于 `nodes/pwa-functional-splicing-approved-r3/`。2038 输入前后 SHA256 `f94a8048ac65297188c43a9ba9783c31a505f3c016f027e72f8309b0b264ad3e`；自建节点/runner 已退出、共享锁无持有者，未删除锁文件。
- 后续发布态 8、账户 66、CoreModules（含 RGB 恢复 9）、Guardian 2 没有启动。本次 SDK 三文件修复已通过定向/直接 WASM 验证，但完整准出仍等待测试读取补丁审核及全量复验。暂不应用补丁，不宣称任务整体完成。

### Deposit 补丁批准后的新运行（2026-10-09）

- 用户已审核同意 `proposed-pwa-deposit-amount.patch`。在上轮收尾之后精确应用到现有 `pos-pwa-e2e.mjs`；不修改生产代码，校验零精度与字符串 Value 后才用既有 BigInt 整数读取。
- 原预定入账 600、资金/费用/签名/确认/输出脚本/UI/重载和原始 POS 顺序、五项 Go postchecks 均保留。JS 语法与 diff 检查通过，尚不代表 Deposit 业务通过。
- 批准时曾启动原始完整 82 项，并计划继续发布态、账户、CoreModules、Guardian；该计划随后被用户定向测试要求取消。SDK14 的生产实现和直接 WASM 组执行路径未变。
- 新证据与历史分别保存；最终定向结果见下文，本阶段已收尾，完整准出未完成。
- 脚本读数边界 **7/7 pass，Node exit 0**，日志 `pwa-deposit-amount-reader-approved.log`：真实600与大整数字符串精确保留，错误精度/类型/负数/小数/缺失Value拒绝。这不是额外浏览器业务case，不扩大82项统计。
- 原始完整组已启动，日志 `pwa-functional-deposit-approved.log`。2038 输入前 SHA256 `1bcb70b2` 前缀，与上一轮 `f94a8048` 逐文件对比仅 `pos-pwa-e2e.mjs` 被批准变更；无额外生产/PWA/SDK执行路径变化。共享锁启动前无holder。
- 用户随后收窄范围：**只测对应 case，不再跑全部测试**。取消完整 82 和后续发布态/账户/CoreModules/Guardian 复跑；当前完整组按要求收尾，其退出不新增记作产品失败。
- Deposit 对应定向链仅五项：原开通 → BTC 注资重开 → 激活保持通道 → 资产注资 → 目标 asset Deposit。前四是现有业务状态依赖，沿用原 case/Go helper/临时 overlay 筛选；其他 77 项及所有后续组不执行，不改断言、不插入成功余额。证据前缀 `pwa-deposit-focused-approved`，只对该修复路径下结论，不能当完整发布准出。
- 用户收窄中止的全量运行已收尾：3 pass（开通/BTC注资/激活）、资产注资运行中断、Deposit未开始；Node TERM 后 exit 143、npm exit 1，仅记用户中止。证据 `pwa-functional-deposit-approved.scope-stop.*`，2038 输入前后 `1bcb70b2` 前缀一致；本轮自建进程退出，共享锁无holder，锁文件保留。开始五步定向复验。
- 最终定向链 **5 pass / 0 fail / 0 not-run，Go exit 0**，Go 顶层测试 321.93 秒、包用时 324.182 秒。逐项 TSV：开通 24192ms、BTC 注资重开 21241ms、激活保持通道 74737ms、资产注资 12300ms、目标 Deposit 20388ms。没有执行其他 77 项或后续组。
- 目标 Deposit 的原预定 ORDX 600、钱包输出脚本、已确认 Anchor 数量、SDK 资产及总聪余额增量、PWA store 和页面资产/余额显示均通过；Anchor `34743db8102b3fa0ba67b9daab04ff8142250689defe182f03760d0cf91a5a7e` 于高度 35 确认。没有修改期望量或插入成功余额。
- 重载通过来自前置开通/BTC 注资两项；目标 Deposit case 不包含独立入账后重载。Go `control.failures` 空检查已通过，筛选模式跳过 activation/rotation/substitution/restart 四项全量结束检查；不能将五项定向通过记作完整门禁通过。
- 证据前缀 `/private/tmp/sat20wallet-e2e-20261009/pwa-deposit-focused-approved`。本阶段对应修复已验证，按用户最新范围收尾；保留未执行项和功能覆盖缺口供后续发布验收使用。
- 2038 输入前后 SHA256 `1bcb70b2c04d5517c8fb28a9adf7f72f2d9a40ca0cfb1bf1fdcf86dacfe2ebc6`，含临时筛选输入的 2041 清单前后 SHA256 `0da3f857738f99cdab6ea8d8fbdfea68ad20e3f45539244b367468efb731a163`，均一致。本轮 Go/runner/Bootstrap/Core/Miner 已退出，共享锁无持有者；已保存节点 stdout、逐项 TSV、progress、run JSON 和源码清单，未删除运行时缓存。
