# RGB11 命名 PR 主线整合记录

## 2026-10-09：其他 session 修复核对与剩余失败复验

已核对任务“整理 SDK 和 PWA E2E 用例”的代码与逐项证据，保留其回调参数、纯私钥钱包目录、监控地址、重复 PSBT 输入、批量次数和 ORDX 负值校验六项修复。该任务的真实运行已通过原六项失败中的前四项；余下两项继续追到异步存储与 SNS 发行入口。本节是后续验证状态，下面的边界调整表和失败建议保留为当时的历史记录。

本轮最小修复：

- 五个会访问钱包存储的 WASM 安全接口复用现有 Promise 包装，释放 JavaScript 事件循环，让 IndexedDB 能完成；参数解析、返回字段、签名与安全检查不变，PWA 类型同步为 Promise。
- SNS/DID 铭刻在访问钱包与准备资金前，复用 indexer 的 SNS 名称校验；非法名称不再进入签名和广播。
- ORDX 发送冻结审核的 unit 复用表单已有显示名称，原有精确 `400 PWAPOS` 与 5 秒可见性断言不变。
- 当前承诺交易的资产查询遍历全部已保存 PrevTx 输出，并从已有非 BRC20 stub 集合解析普通 stub；保留 BRC20 资产映射与持久对象不变，不修改承诺交易或关闭交易。
- 公开 `GetTickerInfoV2` 的 L1 ticker 查询（RGB 除外）直接读取现有索引器，避免部署时持久缓存使确认及重载后的 `totalMinted` 永远为 0。内部交易元数据缓存与 RGB 本地已验证数据不变；不增加持久状态、后台刷新或兼容路径。ORDX/BRC20 回归先复现 0 而非 20，再验证连续读取当前供应、索引器失败不伪装为当前值、缓存未改写及 RGB 不访问 L1。
- 独立 Core 质押到 Bootstrap 后，解质押仍按默认 Core 服务节点构造 witness，误报 invalid channel。协同发送从已配置 server/Bootstrap peer 中推导实际通道，签名请求使用相同公钥对应 RPC；不切换全局 serverNode，不保存 peer 状态，不接受未配置 peer。回归先复现 Bootstrap 通道拒绝及未知 peer 被误发给默认节点，修复后核对两个正确 signer、未知 peer 零请求、当前服务节点不变，并覆盖现有 RGB 协同发送。
- ORDX 批量发送的资产找零可能用尾部白聪补齐到 330 聪。原实现仍比较切分前的 suffix，误报 `0 != 30` 或 `470 != 500`；现保留切分后的剩余输出，验证它无资产且价值等于输入减输出的实际余量，再据此选手续费。原选择器、dust 门槛、收费和签名流程不变。

关闭流程随后暴露一项测试计算错误：旧预期仅按 ORDX 数量计算绑定聪，遗漏 330 聪载体中 30 聪的白聪。解码实际关闭交易确认费用为 579 聪，与原费用公式一致。测试改为关闭前独立查询完整受控 UTXO 的价值，并新增输入集合完全一致、输入不重复及对端权益为零的检查；精确返回金额、资产数量、链上确认、L2 余额与页面门槛保留，生产关闭逻辑未改。

此前“0 金额批量发送被接受”的记录不准确：直接执行的反例证明已有金额拒绝，实际缺口是次数的零值、小数和溢出。另一项补充测试曾误把未知通道 `punishStatus` 的正常空列表要求为错误，已按现有查询语义改为精确空列表并核对无副作用，原安全接口负例未放宽。

| 验证组 | 本轮结果与范围 |
| --- | --- |
| SDK 原六项失败及浏览器错误守卫 | **7/7 通过**，一次定向运行，`sdk-seven-current.log` |
| PWA POS / Funds / Escape 与 DID | **27/27 通过**，一次定向运行，`pwa-funds-padding.log` |
| 另一任务的新增 PWA 16 项计划 | **16 项最新证据均通过**，分次运行，`pwa-added-sixteen-latest.json`；不记为一次 16 项全绿 |
| Node 质押与解质押完整组 | **5/5 通过**，最终生产源码，`pwa-node-peer-current.log` |
| 独立 CSV 强退及两个前置 | **3/3 通过**，新的临时通道、最终生产源码，`pwa-force-current.log` |
| 原生修复回归、RGB 协同签名与钱包 vet | 通过，`native-final-regressions.log`、`channel-peer-after.log`、`wallet-final-vet.log`；vet 保留既有 `-copylocks=false` 约定 |
| PWA 类型检查、WASM 导出检查及 JS/diff 检查 | 通过，原 5 秒显示门槛未放宽 |

16 项最新证据由 POS/Deposit 前置及 RGB carrier 6 项、Tools 2 项、ORDX 1 项、BRC20/DID 2 项和 Node 5 项组成。各组有重叠，不相加作为独立用例总数。SDK 另一任务原 18 项账本中的两项未关闭问题已由本轮对应 W23/W25 复验关闭，未重新执行一次完整 18 项。**本轮未重新执行 SDK WASM 29 项、PWA 钱包 90 项或整个发布门禁，不声称全量同一快照全绿。**

最后 Node 与独立 CSV 两组执行前后，2039 项工作空间输入清单完全一致，SHA256 `b7da1586c3030de7f4e300e6d50aa518f3aa023e5e7d8ec962b48888b1fc793d`。该清单不覆盖本地 rgb11 仓库及原生 wallet 测试；本轮修复源码、测试源码另存 `final-changed-inputs.json`。SDK 7 项与 PWA 27 项较早通过，后续新增 ticker/peer 修复有其独立原生与浏览器验证，不能把这些结果描述为全部来自同一源码快照。

新增 Tools 真实运行还暴露测试双重 JSON 编码、InvokeParam 字段大小写、info/state 返回值形状与 analytics 导出调用位置错误，按现有 PWA API 的对象入参、`action/param` 字段修正，分别按 query 类型读取现有返回形状；analytics 直接调用已公开 WASM 导出，不增加 PWA 适配 API，保留全部独立 REST 对账与无广播断言。修正后复现未知模板可得到有效估算；现估算/部署入口复用既有 SatoshiNet 模板目录提前拒绝未知 subtype。新增原生回归在修复前复现估算成功、部署越过校验访问未配置索引器；未知模板拒绝与已有统一模板、资金安全回归在修复后验证。

新增 SNS、后置 stub 与 ORDX dust 找零回归均有修复前失败证据，修复后通过；stub 同时覆盖 ORDX 和 BRC20，证明不修改持久对象。ORDX 四个找零场景核对精确资产分布、输入减输出等于手续费、手续费不含资产、真实签名有效及原 UTXO 不变。PWA 类型检查与 WASM 导出测试通过。未重新修改或执行 RGB 官方一致性库测试，其证据沿用本文件的独立官方验证记录。

BRC20 发行新增用例把地址持有量误当作可转账铭文 UTXO 数量。沿用已有 L1 资产用例的 `getAssetSummary`，仍精确核对铸造持有量 20、累计供应 20 与重载；同时检查未凭空生成转账载体。不修改 BRC20 生产余额语义。

新增发行用例的通用 txid 选择器在部署和铸造两张卡片同时显示回执时匹配两行，现精确限定在对应卡片；仍核对准确 reveal txid 与原 5 秒门槛。

Node 解质押的新增测试错误地查 `local_action` 类型，而生产日志专门使用 `miner_unstake`，漏掉成功记录后空等触发原有 5 分钟自动锁屏。修正日志筛选后成功操作约 16 秒完成，下一条断言又把实际 reservation 类型 `localaction` 写为 `local_action`。现按已有常量精确核对 `miner_unstake` 与 `localaction`；保留完成、reservation、链上角色移除、精确资产返回断言和生产自动锁屏。

独立 Node 筛选运行缺少完整钱包套件之前的 POS v2 激活步骤，质押 Anchor 真实落在 H 之前，原高度断言因此失败。Node 组现在仅在未到 H 时复用现有控制器 `/activate` 完成真实激活，不修改节点共识、Anchor 或原高度断言。

新增 PWA 16 项第一次启动未进入业务用例：另一任务的临时选择清单保留旧 Core 解质押 case 名称，当前登记名称是无 child miners 的独立 Core 解质押。已只修正外部选择名称，保留该启动失败，不修改断言或生产代码。

本轮日志、各次失败、最终逐项汇总及运行源码 SHA256 清单保存在 `/private/tmp/sat20-e2e-followup-20261009/`。其他任务证据在 `/private/tmp/sat20wallet-e2e-20261009/`。两仓库 staged 内容与本轮开始时备份逐字节相同；仅保留 worktree 修改，未提交或推送。

## 2026-10-09 RGB / DKVS 职责边界调整

用户批准将 RGB 业务移出 SatoshiNet。节点 DKVS 仅保存 `/contract` 下的不透明字节，保留权威签名、大小、永久记录、不可覆盖、快照不得删改和原子提交保护。RGB 编解码、Genesis/ContractID/ticker 校验、命名与完整集合 ordinal 校验移到 `sdk/wallet/rgb11/registry.go`。路径与紧凑 value 字节格式保持不变；SatoshiNet 删除直接 RGB11 依赖与专用业务查询接口。EVM 验证仍在节点自己的 evmsource 业务层。账户服务能力位的 RGB 含义也移到 SDK。

唯一注册业务端使用 `wallet.RGB11Registrar`，针对固定本地权威 backend 串行验证、分配与落盘，从当前记录推导序号。当前权威为配置中的 CoreNode；普通钱包不能登记。两个不同 ContractID 同时分配 ordinal 1 的失败测试已复现；加入串行保护后，混合 FT/NFT 的两个请求分配 1、2，失败写入不消耗序号，重试幂等，重启从同一数据库继续分配。provider 公钥必须来自已认证业务请求；实际桥接/Genesis 地址证据仍由未来 STP 调用方提供并验证。

保证边界随之改变：DKVS 同步验证签名和不可变字节，不解释 RGB、也不验证 ordinal。全局序号保证依赖唯一注册实例与唯一签名业务端，不能扩展为多个独立服务并发写。Transcend 生产 deposit/withdraw 尚未接入，不声称已完成桥接。

本轮证据目录：`/private/tmp/sat20-rgb-boundary-20261009/`。结果按本轮真实执行记录汇总，不把历史通过替代当前验收。

| 验证组 | 本轮结果 |
| --- | --- |
| SatoshiNet 完整 indexer/wire、全包编译、DKVS/RPC race 与 vet | 通过；默认生产包依赖图没有 RGB11 或 wallet SDK 包 |
| SDK RGB 原生全组、registry/registrar race、wallet vet | 通过；保留项目既有 `-copylocks=false` 约定 |
| Transcend 编译、PWA 名称 5 项与类型检查 | 通过 |
| PWA RGB 独立完整组 | 11/11 通过，原有 5 秒断言不变 |
| SDK 非浏览器全部 86 个入口 | 首轮 84 通过、2 失败；两项完整复测均通过，未记为单轮全绿 |
| PWA 钱包完整 82 项 | 67 通过、1 失败、14 未运行；其中 RGB 11/11、Mint 6/6、Tools 6/6、Mining 2/2、WebAuthn 4/4 通过 |
| PWA 账户完整入口及真实 IndexedDB WASM 门禁 | 66/66 通过，存储门禁通过 |
| Guardian 身份恢复及付费审核入口 | 身份恢复通过；付费审核 2/2 通过 |
| 生产发布浏览器门禁 | 8/8 通过，含真实生产构建、CSP、完整缓存、离线启动、版本更新及损坏 WASM 拒绝 |
| SDK Core RGB 完整入口 | 整组通过；浏览器 9/9 通过，无超时 |
| SDK WASM 全接口 | 29 项完整执行：23 通过、6 失败、0 未运行；RGB 无效输入保护、独立验签及真实 L1/L2 批量发送通过 |

非浏览器首轮 Topic 失败发生在 STP 服务尚未就绪时首次创建主题。测试现检查已有 `/health` 前置条件，完整业务复测通过。EVM 重复关闭的区块同步断言首次超时，原用例独立复测通过。首轮失败与复测日志分别保留。

钱包组唯一失败发生在 ORDX 发送广播前：确认框未在原有 5 秒内显示精确 `400 PWAPOS`；14 项依赖资金流程未运行。当前组合没有复现旧根恢复 5 秒失败，不能据此宣称启动时序问题已彻底解决。Core 首轮 RGB 临时恢复准备遇到安全 CAS 拒绝；测试复用已有同会话有限重试并核对拒绝后恢复仍未配置，没有改生产 CAS 或后续业务断言。WASM 首轮有直接调用不返回，已保留中断证据并给现有调用器增加 30 秒失败报告边界；接口业务断言不变。

Core 最终完整复验已通过，原生 44 条结果记录（含层级汇总）及浏览器 9/9 保存在 `core-modules-boundary-final.json` 和 `sdk-core-pwa-retry.log`。Core 实际用例耗时约 970 秒；此前共享节点锁排队另计，未混入其他运行结果。

WASM 最终 29 项完整执行保存在 `sdk-wasm-bounded.log` 与 `sdk-wasm-bounded-summary.json`。首轮中断及初步诊断 `wasm-core-triage.json` 仅作为历史证据；以下是最终复验的六个失败及最小处理建议，尚未修改相关生产语义：

| 最终失败 | 复现与处理建议 |
| --- | --- |
| 回调注册参数边界 | `registerCallback()` 返回 nil，且未检查参数必须为函数；补齐统一错误响应与函数类型检查 |
| 独立私钥钱包目录 | `getWalletCatalog` 要求首个助记词钱包解锁，阻断纯私钥钱包；先确定目录 API 是否支持这类钱包，再复用现有钱包目录而不新增账户状态 |
| 无效监控地址 | 空地址被 `createMonitorWallet` 接受；创建前用当前网络地址校验，拒绝后目录与选中钱包必须不变 |
| L2 PSBT 合并 | 相同已签名 PSBT 重复输入被接受；合并前拒绝重复 outpoint，保留原单次签名和正向合并流程 |
| 无效批量发送次数（首轮误记为金额） | 首轮误记为 0 金额接受；后续反例证明已有金额校验，实际缺口为次数的零值、小数与溢出。修复及真实广播/零广播复验见文首 |
| 无效发行参数 | ORDX `max=-1, limit=0` 返回成功；先按已确认协议区分“不限供应”的特殊值与非法范围，再补齐校验，不能直接把测试预设当成协议规则 |

完整钱包组的 ORDX 发送审核显示失败另列：资产表保留小写 ticker，表单显示名转为大写，冻结审核记录的 unit 却直接使用原 label；测试要求精确 `400 PWAPOS`。这是静态证据，失败时日志没有保存完整审核文本，尚需用现有场景捕获 `dl` 内容确认；建议统一使用现有显示名生成函数，保留金额、地址、链、网络、审核指纹及原有 5 秒断言。不得以此宣称后续 14 项资金流程已通过。

这些后续边界或协议取舍按用户工程原则先提出，不在本次 RGB/DKVS 职责调整中暗中修改。RGB/DKVS 已批准范围完成；全部计划的 SDK/PWA E2E 入口均已执行，但整体验收尚未全绿。

用户已有 staged 内容保持原样；本轮仅修改 worktree，未提交或推送。

## 边界调整前的验证结果（2026-10-09）

RGB11 类型定义已在 Registry 加载时解析一次，后续编解码复用解析结构；官方 NIA 原生解码基准约快 56 倍。库测试、race 和 vet 通过。SDK RGB 137 个顶层回归及 PWA RGB 完整 11 项通过，原有 5 秒显示门槛保持不变。

| 验证组 | 最新结果 |
| --- | --- |
| SDK 全部非浏览器 E2E | 86/86 通过：账户 23、DKVS 28、合约 28、夹具 7 |
| SDK Core、Guardian 身份恢复及付费审核浏览器入口 | 均通过 |
| PWA 账户完整入口及实际 IndexedDB WASM | 66/66 通过，存储门禁通过 |
| PWA 钱包：L1 资产、基础钱包、RGB、WebAuthn | 完整组合中分别 3/3、14/14、11/11、4/4 通过 |
| PWA 钱包：DApp、Tools、Mint、Mining 独立复验 | 分别 8/8、6/6、6/6、2/2 通过 |
| PWA Core/Miner 真实质押 | 2/2 通过，Miner 查询缓存污染修复已验证 |
| PWA 钱包完整组合 | 最近完整运行仍为 41 通过、13 失败、28 未运行；后续独立通过不替代全组验收 |

剩余阻塞是已有 RGB 备份账户的根恢复首次启动超出 5 秒，以及 splicing 的 PWA 状态仍依赖旧 channel.status，尚未按主线 reservation 显示进度。后者的最小方案已请求用户决策，未实施。所有运行日志、失败样本和通过汇总保存在 `/private/tmp/sat20-rgb-optimize-20261009/`。以下历史记录按其当时运行结果保留；本节为边界调整前的结果；本轮结论以文首边界调整记录为准。

## 官方一致性补充验证（2026-10-09）

用户批准的三个缺口已落地并通过。复用 `rgb11` 现有 Rust inspector、
`check_interop.mjs`、fixture 生成器、冻结向量、官方 rgb-lib CLI 和 SDK
`TestRGB11RegtestOfficialBidirectional`，未增加生产接口、持久状态或旧格式兼容。

| 验证组 | 本轮结果 |
| --- | --- |
| 官方 Rust 向量 / Go 差分向量 | 75 / 45 通过；canonical SHA256 不变 |
| 完整官方 Transfer validation | 44/44：10 valid、26 invalid、8 evidence_error；payload 字节一致 |
| SDK 原生 RGB 与 RGB 库回归 | 通过；最终 44 项固定 corpus 回归通过 |
| 真实本地 NIA / IFA / UDA 双向钱包互操作 | 3/3，通过实际签名、广播、确认、官方 Settled 和双方重启 |
| PWA RGB 完整业务组 | 11/11，0 失败、0 未运行；原有 5 秒断言不变 |

Rust 验证使用冻结官方 schema fixture 的 trusted type system。语义反例重新
生成 transition、bundle 与 Bitcoin commitment，并要求金额和 token 反例
到达官方 ScriptFailure；缺失证据单独记为 evidence_error。输入与逐项结果
在失败时也保留，保存后的 manifest 路径可直接重放。

真实链运行在本地临时 Bitcoin Core 28.1 与原生 Esplora，所有端口仅绑定
loopback。协议 oracle 是正式版 0.11.1 / Strict Types 与 Encoding 1.0.4；
钱包 oracle 是冻结 rgb-lib beta.7 / rc.11，不能据此宣称全部 RGB 版本或任意
AluVM 程序都已认证。NIA / IFA 重启后 Alice、SDK、Bob 的余额分别为
950、30、20；UDA 分别为 0、0、1，官方 assignment 为 NonFungible。
证明身份、原始 state data、seal 与 consignment hash 在 SDK 重启后保持一致。

初次运行保留了资金费率与未成熟 coinbase 的准备失败，以及既有测试入口
遗漏确认刷新、独立 witness 地址和 IFA 控制 allocation 的失败。修正仅在
临时环境与测试适配器。另一个已复现问题是 Esplora 对未知 txid 的 status
也返回 confirmed=false，旧适配器误报 mempool、导致跳过实际广播。现改为
检查完整 transaction info，新增存在性回归在修复前失败、修复后通过。
真实三项最终在同一版测试代码和各自的新钱包上完整通过。

PWA 的接收方重载约 2.745 秒、发送方恢复约 3.694 秒，UDA 后续重载约
2.867 和 2.789 秒；均保留 5 秒门槛。此处 11 项通过不替代前述完整钱包
82 项验收；原有组合恢复启动与 POS reservation 显示问题仍保留，未在本轮
放宽断言或实施尚未决定的生产设计。

日志、成功摘要和原始失败数据：`/private/tmp/sat20-rgb-conformance-20261009/`。
关键文件为 `status.json`、`final-source/differential-summary.json`、
`live-final-gate/<schema>/evidence/bidirectional-summary.json`、
`pwa-rgb-summary.json`。原生临时链服务已停止，数据保留。
修改保持未暂存；未提交或推送。

## 整合范围

- Wallet PR #10：`014002a4a85857d1b0c6b4bb3cfb2a06948661d3`。
- SatoshiNet PR #16：`1ee185f1c3e8ef604fa2c335d922b2db025a9212`。
- 整合基线：Wallet `29a4d4d`、SatoshiNet `cdd56e1`。
- 修改留在两个仓库的本地 main 工作区，没有提交、暂存或推送。

## 最终实现

主线 DKVS 的记录提交、ETag CAS、PathMeta、change index、认证快照与权限检查保留。PR 的旧 DKVS 写入与快照代码没有覆盖主线实现。

RGB 合约存储路径为 `/contract/rgb11/<contract_id>`，其中 ID 是完整 ContractID 的 64 位小写 hex。value 为版本 byte 1 + CompactSize 长度前缀的 provider_did、ticker + CompactSize ordinal + CompactSize 长度前缀的标准二进制 RGB 合约文件。完整 value 上限 1 MiB。合约类型和 ContractID 从 Genesis 推导并验证，不重复保存；ticker 必须一致。

EVM 源码路径统一为 `/contract/evm/source/<contract_address>`，保留主线源码编译、部署交易、签名及不可覆盖验证。旧 RGB 路径、33-byte value、旧 EVM blob 源码路径和 fingerprint 资产身份均不兼容。

同一 provider/ticker 的 FT/NFT 共用从 1 开始的连续 ordinal。RGB 记录永久且业务内容不可覆盖，不允许普通钱包 DKVS 写入或删除。RGB 集合快照不能遗漏已有合约，也不能通过旧记录排序绕过冲突检查。没有新增持久反查索引或序号表。

SDK 余额、证明、转账和快照使用完整 ContractID 资产 key。本地可变名称、默认 Genesis 地址后 12 位名称与可信注册名称分开。Primary DID helper 使用主线签名请求与写入序号/高度流程。本地改名复用已有备份流程；注册名称缓存保存签名记录，恢复时拒绝任意字符串伪造注册名称。

拒绝缺少 ContractID 的旧 ticker 元数据；收款状态携带精确 ReceiveRequestID，不再扫描发票匹配旧状态。快照恢复时从输入快照匹配 RequestID，而不是查询旧的本地引擎。查询合约列表使用当前钱包已有的验证记录，因此零余额导入在重启后仍可见，也不会显示其他钱包的内存缓存。冷启动改名从既有 ticker DB 读取元数据，并要求当前钱包存在已验证的合约引用。

## 初次整合与优化前验证记录（历史）

运行日志：`/private/tmp/sat20-rgb-integration-20261008/`。

- 针对性 RGB、Primary DID、EVM 回归：通过。
- 节点 DKVS、索引器 RPC、wire 完整包测试：通过。
- SDK 名称与注册记录回归：通过。
- PWA 名称展示测试 5 项、`verify:rgb11-l1`、`npm run compile`：通过。
- 钱包与节点相关包 go vet：通过（钱包保留现有 `-copylocks=false` 约定）。
- 完整 wallet 包首轮发现别名校验误作用于无别名恢复的 3 个回归，已修正。45 分钟预算的整包测试通过：wallet 1364.037 秒，子包均通过（`wallet-regression-final.log`）。零余额列表修复后的全部 RGB 钱包回归通过（1091.966 秒，`rgb-wallet-final-regression.log`）；后续旧格式拒绝、精确 RequestID、空钱包快照恢复、冷改名和收发回归通过（wallet 161.455 秒，RGB 子包 6.605 秒，`rgb-final-fixes.log`），最新 go vet 通过。
- `TestSDKCoreModulesE2E`：status 异步修复后的最新运行退出码 1（`sdk-core-after-status-fix.log`、脱敏报告 `core-modules-after-status-fix.json`）。原生部分 42 个通过记录；浏览器 9 项中 7 项通过、1 项失败、最后 1 项启动但被原有 18 分钟预算终止。失败仍是撤销故障后的 cold remote import 重试在原有 90 秒内不能解锁，错误 `account-managed data import is incomplete; restore the wallet before uploading`，state_seq 15、data_revision 8、诊断时 marker 为空。冷恢复用例总耗时 417.901 秒（含准备及善后），不代表解锁断言被延长。内部子进程 `signal: killed`，外层报告 `timed_out=false` 不能理解为浏览器没有预算中断。没有放宽解锁校验或清理恢复屏障。此前完整运行浏览器是 8 项通过、1 项失败（`sdk-core-complete.log`）；主线验收文档记有同类失败，但不足以证明本轮失败完全与本次修改无关。本次运行是该核心入口，未运行 SDK 的全部 E2E 入口。
- PWA RGB 资产管理完整 10 项场景尚未通过。最新 Long Tasks 和 CPU 诊断轮均是 4 项通过、1 项失败、5 项未运行（`pwa-rgb-event-loop-timing.log`、`pwa-rgb-startup-cpu-profile.log`，退出码 1）。第 5 项失败是接收方预验证 consignment 后重载，Bitcoin 页签未在原有 5 秒内出现。签名且不广播、标准合约导入不给余额、发票重载均已通过；确认广播/到账/供给守恒和取消流程仍被前序失败阻断。IFA/UDA 独立复验均通过。首轮第 3 项零余额合约重载丢失已修复并通过真实 PWA 重载复验。中间运行的发送/接收按钮 30 秒等待失败也保留日志，没有将这些失败计为通过。

关闭导入弹窗的诊断见 `pwa-rgb-runtime-diagnostic.log`：窗口进入 closed，退出动画在约 13.7 秒后才收到结束事件并移除；页面已在前台。这是执行时序证据，不代表 CSS 根因已证实。测试改用界面自带 Close 按钮，与发行窗口一致，保留 5 秒隐藏断言及所有身份、余额、交易、证明断言；按钮版重跑被更早的初始化页签断言阻断，尚未证明关闭等待已解决。另一次运行出现 `Go program has already exited` 和启动失败界面（`pwa-rgb-close-diagnostic-2.log`）。后续独立 UDA 运行已捕获原始堆栈，见下述根因修复。

重复定向 Go 入口由同工作区另一项已授权清理删除，全部业务场景保留在 `TestSDKWalletPWAConnectedBrowser`。本次复跑使用临时 Go overlay 筛选现有入口，未增加仓库内测试框架或恢复重复入口。共享 fake indexer 迁移后新增 BRC20 种子校验阻断过一次节点准备；已为既有 gas 资金夹具补齐首 sat 的铭文位置和金额，没有伪造查询返回或修改生产交易验证。overlay、日志和报告均保存在上述运行目录。

IFA/UDA 独立运行（`pwa-rgb-issuance-final.log`）最初是 IFA 通过、UDA 失败并暴露 WASM 死锁。status 异步修复后两项独立复验均通过（`pwa-rgb-issuance-after-status.log`，135.43 秒，退出码 0）。独立通过项不替代完整收发流程。
- ordinal 并发 race 检查：通过（macOS linker 有 LC_DYSYMTAB 警告，测试退出码 0）。

## E2E 暴露的 WASM 死锁修复

`pwa-rgb-issuance-final.log` 捕获 `fatal error: all goroutines are asleep - deadlock!`：同步 `accountStatus` 的 JS 回调等待 `GetAccountManagementStatus` 的钱包 RLock，而 RGB 自动备份持有钱包锁等待 IndexedDB 完成回调。JS 无法退出同步回调，数据库也无法完成。这里只将 `sdk/wasm/account_management.go` 的 status 导出改为已有 `createAsyncJsHandler` Promise 方式；PWA 包装本来就 await 方法返回值，JSON 字段、持久化、账户锁和备份流程保持原样。没有新增 worker、锁、状态或恢复兼容路径。`go test ./wasm -count=1` 通过；私有 WASM 已重新构建。`pwa-rgb-after-status-fix.log` 已通过发行、标准合约导入、发票重载和未广播签名 4 项；第 5 项在接收钱包重载后的 Bitcoin 页签 5 秒断言处失败，后 5 项未运行，尚未达到整组通过。IFA/UDA 独立复验均通过，未再捕获该死锁退出；SDK 核心入口复验结果见上文。

## 5 秒启动门槛

用户明确选择保留原有 5 秒页面显示断言，并另行定位启动性能。当前复验没有提高页面显示或关闭窗口的断言预算，也没有放宽余额、证明、签名、身份及广播条件。接收钱包重载诊断复用既有 WASM 调用 breadcrumbs，记录调用阶段时间、路由、页签挂载/可见性和弹窗状态；只用于区分解锁耗时、页面渲染和弹窗遮挡。首轮诊断日志为 `pwa-rgb-startup-timing.log`：NIA、导入和发票重载通过；发票重载到 Bitcoin 页签为 1569 毫秒，SDK 解锁 1159 毫秒、地址/公钥读取合计约 2 毫秒、目录读取 7 毫秒。未改读取路径，因为证据不支持将其视为主要瓶颈。该轮在发送按钮定位处失败（30 秒），接收方验证后的重载未执行，不能认为原有 5 秒问题已解决。IFA 独立复验重载为 1789 毫秒。当前夹具再验 `pwa-rgb-startup-timing-current.log` 为 2 项通过、1 项失败、7 项未运行，在点击 Receive 后 30 秒动作等待处失败，诊断时弹窗已出现。

Long Tasks 诊断轮 `pwa-rgb-event-loop-timing.log` 再次复现第 5 项的原有 5 秒超时：前 4 项通过、1 项失败、5 项未运行。接收页可见且无弹窗，停在 `#/unlock`，Bitcoin 页签未挂载。SDK 解锁 dispatch 到 returned 为 1177 毫秒，returned 后 26.8 毫秒开始 5847 毫秒主线程长任务；PWA 尚未记录 unlock 的 finished，地址/公钥读取尚未执行。证据确认主线程执行阻塞了剩余启动流程，不能归因于弹窗遮挡或重复地址读取。Long Tasks 与方法时序明细保存为 `rgb-startup-long-tasks.json`。未广播收款的后台同步再次执行完整 consignment 校验，是静态调用链中的候选来源；CPU 采样结论如下。

### CPU 采样结论与后续优化

`pwa-rgb-startup-cpu-profile.log` 再次在第 5 项复现原有 5 秒超时。健康发票重载约 2 秒；接收方预验证后的重载仍停在 `#/unlock`，SDK 已 returned、PWA 尚未 finished。失败采样为 `startup-1791477607585.cpuprofile`，对照健康采样 `startup-1791477573677.cpuprofile`。

私有 release WASM 被去除符号，因此另外离线构建同源码、同 POS 高度的符号版用于映射。两版均有 15273 个 WASM 函数，type/import/function/export sections 完全一致；采样函数映射保存为 `profile-function-names.json`，分析为 `startup-cpu-analysis.json`。解锁后采样热点是 `rgb11/strict_types` 递归解码、`consignment.DecodeArmor` / `EncodeArmor` 和反复 `encoding/json.Unmarshal` 解析类型描述；JSON Unmarshal 包含时间约 6.3 秒，类型递归解码包含时间约 5.8 秒，这些包含时间相互重叠，不能相加，也不能当作未采样运行的精确墙钟耗时。未开启 CPU 采样时独立 Long Tasks 已确认 5847 毫秒主线程阻塞。

静态路径与热点一致：解锁会唤起现有 RGB 链同步；`RefreshRGB11State` 对 `awaiting_broadcast` 收款调用 `acceptRGB11Consignment`，重新完整编解码。`rgb11/strict_types` 用 RawMessage 保存类型定义，`parseRef`、`restrictedString`、`refIsCharEnum`、`refEnumHasTag` 等递归路径重复 JSON 解析。当前 goroutine 仍在浏览器 WASM 主线程上执行，异步 Promise 无法将 CPU 工作移出主线程。

推荐下一步先在 `rgb11` 库优化类型解释：在既有 Registry 加载时将 JSON 类型定义解析一次，后续编解码复用解析结果；保持 canonical 编码和完整校验，先用原夹具比较冷/热耗时与 5 秒门槛。该方案增加 Registry 的内存类型结构与对应回归责任，涉及本次两个仓库以外的 RGB 引擎，需要按工程原则由用户决定后实施。暂不推荐 Worker，因为它需要新增消息接口、运行时和生命周期管理；也没有实现延后重放或跳过校验来制造通过。

一次性 Long Tasks / CDP CPU 采样代码已从仓库测试脚本移除，完整诊断脚本留在临时目录 `wallet-rgb-cpu-diagnostic.mjs` 供复现。保留已有 WASM breadcrumbs 的启动失败诊断和原有 5 秒断言。以下为用户随后批准的优化；上文测试记录属于优化前结果。

### 2026-10-09：类型定义解析一次

用户明确批准在 `rgb11` 库优化类型解释，并要求 RGB 原生与 PWA E2E 通过后继续全部 SDK/PWA E2E。`strict_types` 在 Registry 安装库或合约 TypeSystem 时解析 JSON 类型定义，包括 inline 引用。后续编解码读取不可变的类型结构。固定 RC11 库使用 `sync.Once` 初始化；各 Registry 仅复制注册表，合约动态注册仍保持实例隔离。命名和外部引用保持按既有规则解析，避免破坏前向引用。不增加合约结果缓存、持久状态、后台机制或旧格式兼容。

官方 NIA 解码基准（同机原生 Go，库加载在计时之外，2 秒采样）：优化前 `201480136 ns/op`、`15676612 B/op`、`217984 allocs/op`；优化后 `3571686 ns/op`、`653406 B/op`、`8209 allocs/op`。后续固定 Registry 创建从 `7298825 ns/op` 降到 `4747 ns/op`。这些是原生基准，浏览器 5 秒门槛仍须由真实 PWA E2E 验证。

RGB 库整包测试、strict_types race 和 vet 已通过。补充 SDK `go vet ./wallet ./wallet/rgb11` 返回 10 个 copylocks 告警（`sdk-vet.log`），涉及合约序列化、FundingReservation 和 ReservationBase；相关调用及类型文件与 HEAD 无差异，属于既有告警，不计为本次 RGB 修改通过的检查。新增回归确认固定定义共享而动态注册不泄漏、错误定义不发布半成品库，以及原有尾部数据、字符、UTF-8、长度、集合顺序和重复键校验仍有效。日志目录为 `/private/tmp/sat20-rgb-optimize-20261009/`。

钱包原生 RGB 复验还暴露通道发送丢失对端响应后的 witness 恢复问题：链刷新可先把状态改成 pending，广播的完成状态分支却直接返回；另一个恢复分支补齐了独立加载的 batch，却返回旧 first 记录中的交易。最小修复复用已有 witness 恢复，重新读取补齐的交易，仍验证原 txid 与双签名，不重新签名或广播。原失败用例修复后通过；新增已观察广播的回归直接验证返回交易的完整双签名。钱包原生 RGB 等 137 个顶层用例通过（`sdk-rgb-native-final.log`），最终 witness/广播相关 14 个回归通过（`sdk-rgb-witness-final.log`）。

优化后 PWA 首轮为 7 项通过、2 项失败、2 项未运行（`pwa-rgb-e2e.log`）。接收方预验证后重载在原有 5 秒断言内通过，实测 3425 毫秒；发送方恢复为 1859 毫秒。已确认收款、供给守恒与载体证明。取消用例假设已完成请求仍能从可恢复请求列表打开状态按钮，与 SDK 仅返回可恢复请求的行为不符；UDA 用例误把独立 witness 收款脚本当成钱包主地址脚本。测试改为核对空的新请求表单和保留的 settled 历史，以及实际 UI 创建的原生 receive request 的 witness_script；金额、双签名、广播次数、余额、锁与重载条件均保留。最终完整 11 个 RGB PWA 用例通过（`pwa-rgb-e2e-confirmation.log`、`rgb-pwa-pass-summary.json`），0 失败、0 未运行；原有 5 秒断言保持不变。取消终态精确核对 SDK 的 `rejected` 与 `user-rejected`，而不是并不存在的 `cancelled`。前一轮另有新 UDA 钱包导入的 5 秒失败，保留于 `pwa-rgb-e2e-final.log`，随后完整复验没有复现；已增加失败时的调用时序与页签诊断，不将一次通过解释成该首次导入时序故障的根因已证实。当前开始全量 SDK/PWA 分组 E2E。优化后 `TestSDKCoreModulesE2E` 整组通过（`sdk-core-rgb-e2e.log`、`core-modules-rgb-pass.json`）：原生结果 44 条全部 pass（包含层级汇总记录），浏览器 9/9 项通过，未超时。原先失败的冷设备导入重试约 152 秒（含准备），原有 90 秒重试断言保持不变；未完成通道、所有权证明、锁和不新增广播的断言均通过。

## 全量 SDK / PWA E2E 推进

RGB 整组通过后，已执行 `sdk/e2e` 的全部 86 个非浏览器顶层入口：账户 23/23、DKVS 28/28、合约 28/28、基础夹具 7/7 通过。分组日志及结果保存在 `/private/tmp/sat20-rgb-optimize-20261009/` 的 `sdk-account-all.log`、`sdk-dkvs-all.log`、`sdk-contracts-all.log`、`sdk-fixtures-all.log` 和对应 `*-all-summary.json`。另有 SDK Core、Guardian 身份恢复、Guardian 付费审核三个包含浏览器的入口通过。

账户完整浏览器入口当前要求 66 项。第一轮 54 项通过、1 项失败，余项因整套 30 分钟运行预算中断而未完成。失败的创建/导入目录读取故障用例使用了已激活远端备份的共享助记词，导入进入远端恢复路径，未触发用例预设的普通导入故障。测试改用同用例前一子场景实际创建的新钱包助记词，保留一次提交、一次故障、精确新增钱包数及重载一致性断言；该用例独立复验通过。后续 12 项定向复验为 8 通过、4 失败，4 项均缺少正常入口预先准备的恢复材料，尚未执行业务断言，不能记为通过。接下来使用正常入口和完整材料复验；整套运行资源预算设为 45 分钟，单项页面 5 秒门槛及其他验收断言保持不变。

钱包完整浏览器入口第一轮 82 项中 22 通过、14 失败、46 未运行。RGB 11/11、WebAuthn 4/4、Mining 2/2 再次通过。导入用例中的 Recovery Phrase 标签同时匹配页签容器和文本框，已将 L1 资产、基本钱包、DApp、Tools 相关定位改为精确 textbox，待完整复验。Core stake 夹具未将真实候选公钥加入 STP 的配置准入列表，已补齐准入配置；签名资金交易和链上 Anchor 证明仍须实际通过。Miner 质押当时运行在 POS 前序失败后未跨越 v2 激活高度的网络，尚不能视为独立生产缺陷。Mint 的部分导入仍未通过 5 秒页面显示门槛，已补充脱敏的调用时序与路由诊断，未提高断言预算。

POS 最小复现中 opening 通过，splicing-in 显示失败。SDK 与 PWA 读取的 channel.status 均为 READY（16），主线已把扩容进度移入 reservation，旧 ChannelCard 仍依赖通道状态显示 Splicing in。拟复用现有刷新和 allReservations，按当前钱包及通道派生操作显示，不新增持久状态、后台任务或 SDK 接口；该生产 UI 修改正在等待用户决策，尚未实施。日志为 `pwa-pos-diagnostic.log`，原有 5 秒断言保留。

钱包第二轮完整复验（`pwa-wallet-second.log`、`wallet-pwa-second-summary.json`）为 41 通过、13 失败、28 未运行。L1 资产 3/3、基本钱包 14/14、RGB 11/11、WebAuthn 4/4 通过。DApp 前六项权限、撤销和消息签名通过，PSBT 用例误读 `OutPoint`，与真实 Indexer 的 JSON 字段 `Outpoint` 不一致，已修正并增加资金 outpoint 格式校验，待补验。Core 准入配置修复后两项节点质押均通过真实提交，但因 POS 前序流程中断而未在 v2 激活后验收 Anchor；计划用既有控制器实际出块跨越激活高度，再单独复验这两项，不改变链上验证。Tools 三个独立入口、Mint 五项及 Mining 第一项在导入后的原有 5 秒页面显示门槛失败；后一 Mining 项和 Tools 依赖项未执行。

未采样的 Mint breadcrumbs 显示根助记词远端恢复调用耗时约 4.4–4.7 秒，加上此前助记词验证及页面挂载超过 5 秒；另一例在超时时仍未返回。随后使用相同 RGB 备份准备与原有 Mint 用例采样：RGB 11 项再次通过，Mint 6 项在诊断轮均失败，不能将采样轮作为正常验收结果。实际私有 release WASM 与符号版的 type/import/function/export sections 一致，均为 15278 个定义函数，符号映射可对应本轮代码。6 个 CPU 样本中已映射的 scrypt self 时间约 0.58–1.03 秒，RGB strict_types self 时间约 0.03–0.11 秒，另有曲线、JSON、本地持久化与运行时开销；包含时间相互重叠，不能相加或据此认定一个独立根因。未降低 KDF 参数、跳过恢复校验或删除数据。采样文件、分析及脚本保存为 `mint-startup-*.cpuprofile`、`mint-startup-cpu-analysis.json`、`wallet-mint-cpu-diagnostic.mjs`；仓库中的临时采样代码已移除，保留原有 5 秒断言与脱敏失败诊断。

正常账户完整入口最终通过：66/66、0 失败、0 未运行，Go 入口耗时 2250.20 秒（约 37.5 分钟），完整的实际 IndexedDB WASM 存储门禁也通过。日志为 `pwa-account-dapp-node.log`，独立汇总为 `account-pwa-full-pass-summary.json`。前述定向入口缺少材料的四项均在正常入口的真实材料上通过，包括响应丢失后的恢复幂等性、SDK 数据重载、Guardian 付费确认的记录数与根身份、离开恢复页后的会话与临时材料清除。仅增加整套执行资源预算到 45 分钟，页面 5 秒、余额、证明、密码、签名、广播与单项恢复等待条件保持不变。

### 独立功能复验与新增问题

Mint / Mining 使用既有入口的正常新设备和资金夹具独立复验，8/8 通过（Mint 6/6、Mining 2/2；`pwa-mint-mining-functional.log`、`mint-mining-pwa-pass-summary.json`），根入口耗时 198.67 秒。包括 ORDX/BRC20/Runes/DID 参数、无效 ticker、部分与余量金额、取消后不签名不保留资金、不增加广播，以及真实 WASM worker 的独立 PoW 证明、停止与重载配置。这是功能复验，不替代全量组合中同一账户已有 RGB 备份的 5 秒恢复门槛；组合失败证据仍保留。

DApp 的原有 PSBT 地址断言暴露审批界面没有输出地址。`TxDetailSection` 用现有 bitcoinjs 从 SDK 的脚本显示地址或非地址脚本。Bitcoin 完成签名的 PSBT 在 SDK 资产切分信息中可能保留输入脚本，因此 `SignPsbt` 从 SDK 已返回的实际 `txHex` 读取输出脚本，并核对输出数量和金额，解析异常仍禁用签名。未修改 SDK 资产分配或交易签名规则，新增完成签名 PSBT 的输出复核与取消用例。PWA 类型检查通过（`pwa-compile-psbt.log`）。复验还确认功能用例在 10 秒内连续发起超过 8 次审批触发现有限速；用例在签名/广播组边界等待原限速窗口，生产限速不变。随后无效 PSBT 错误展示、禁止签名和取消检查通过；签名返回已 finalize，测试再 finalize 失败，已改为要求真实 finalScriptWitness 并保留后续独立 Schnorr 验签、原交易输出、零广播和精确一次支付校验，完整复验仍进行中。

Tools 的初始空合约列表是用例遗漏现有 Load 操作，已补齐真实加载。后续 Exchange 部署暴露数字零阈值被 `value || ''` 变成空字符串，已改为 `??` 保留零；真实部署和 canonical Result 执行已通过。历史断言将状态 JSON 与历史视图同时计数，已限定到 Query History 视图，并保留唯一视图和独立链上历史检查。EVM 调用确认按钮在手机视口之外且无法滚动，已给本页交易审核弹窗增加高度上限和内部滚动。Solidity 编译和实际 EVM 部署在第二轮通过；修复后完整 Tools 仍未通过，不能据局部结果记为全组完成。

`pwa-tools-mint-mining-third.log` 的 EVM 用例受到保存源码触发的 Vite 热更新干扰，该部分不作为验收结论；第四轮使用固定源码重新运行，Exchange 已到真实执行和历史读取，但被历史定位阻断，另有 EVM 审核弹窗和 Agent 钱包首次导入的 5 秒失败。日志全部保留，没有加长断言或强制点击。节点质押独立复验通过既有控制器的实际签名心跳和出块跨越 POS v2 高度，再执行原有 Anchor、服务节点、资金 outpoint、金额和刷新后状态检查；不伪造链高或成员记录。

### 后续复验（2026-10-09）

DApp 完整独立复验 8/8 通过，包括真实 PSBT 输出审核、无效输入禁签、取消无广播、独立 Schnorr 验签和精确一次广播。与节点组共同运行的日志为 `pwa-dapp-node-fourth.log`，其中 Core 质押也通过全部真实资金、Anchor、索引和页面状态检查；Miner 在真实 Anchor、服务节点、资金 outpoint、通道地址通过后，因节点返回空 AssetName 而失败，共 9 通过、1 失败。通过明细保存在 `pwa-dapp-node-fourth-pass-evidence.json`。

固定源码的 Tools 6 项复验（`pwa-tools-final.log`）为 4 通过、1 失败、1 未运行：Exchange 部署与精确供给、EVM 编译部署、EVM 状态调用与历史、Agent 不安全来源禁签均通过。Faucet 精确结算 990,000 GAS、实际取消、资金与 Result 消费关系、双方余额及库存通过后，历史断言仍要求默认调用的资金交易 ID；主线索引仅按显式合约 op 构建历史，普通资金默认调用没有 invoke op，实际历史记录的是成功的 Result。断言已改为要求实际 Result ID、正确合约与成功状态，保留资金/Result 同块消费关系和所有金额检查，正在重跑。模板关闭依赖此前 Faucet，尚未在该轮执行。

Tools 最终 6/6 通过、0 失败、0 未运行（`pwa-tools-history-result.log`、`tools-pwa-pass-summary.json`）。Exchange/Faucet/关闭均通过精确数量、Result 消费关系和余额检查，EVM 编译部署及状态调用、Agent 不安全来源禁签也通过。PWA 最终类型检查通过（`pwa-compile-final.log`）。

Miner 空名称已定位到 `/v3/ascend` 的既有处理器：它清空响应对象的 Assets，而公开 GetAscendData 返回 RPC 索引缓存中的原对象。SDK 查询 Anchor 因此污染 RPC 缓存，后续 MinerInfo 将其当成普通聪。公开查询已用现有 cloneAscendData 返回深拷贝，保持 API 响应及持久化格式不变。回归修复前精确复现名称清空、金额变为普通聪数量（`miner-ascend-cache-red.log`）；修复后与两个既有质押索引回归全部通过（`miner-ascend-cache-green.log`），base 整包测试通过（`node-base-query-regression.log`，18.742 秒）。真实 Core/Miner PWA 最终 2/2 通过（`pwa-node-ascend-query-fix.log`、`node-pwa-pass-summary.json`，根入口 337.965 秒；Core 22.478 秒、Miner 29.045 秒），原有精确资产、金额、资金签名、Anchor、服务节点、通道、重载记录、余额扣减和页面状态检查全部保留。

钱包非 POS 功能组的 56 个业务用例已有通过证据，分布于完整运行及上述独立运行；不能将不同夹具、不同运行的结果拼成完整 82 项通过。完整组合的恢复启动门槛和 POS 前序流程仍待解决。Splicing 显示方案复用当前刷新中的 allReservations，按当前钱包/channel 派生 pending 文案，不新增持久状态、后台任务或 SDK 接口。依照工作空间 [AGENTS.md](/Users/yingfeng/github/AGENTS.md) 第 2 条“需要增加复杂性时，先说明具体问题或失败场景……由用户决定后再修改相关生产设计”，该新增跨模块读取方案已提交用户决策，尚未落地；其余不依赖该决定的功能复验已完成。

## PWA RGB 场景明细

以下均以优化后的完整 11 项运行 `pwa-rgb-e2e-confirmation.log` 为准；早期失败记录保留于上文。

| 场景 | 当前证据 |
| --- | --- |
| NIA 发行及确认载体保护 | 通过 |
| 标准合约导出、导入且不给接收方余额 | 通过 |
| witness invoice 重载保留 | 通过 |
| 构建签名且接收方验证前不广播 | 通过 |
| 接收方验证同一包且不给可花余额、重载保留 | 通过；页面重载 3920 毫秒 |
| 发送方恢复、核对摘要并广播实际交易 | 通过；页面重载 2531 毫秒 |
| 确认到账、供给守恒、持久载体证明 | 通过；页面重载 2873 毫秒 |
| 取消未广播转账并保留载体保护 | 通过；核对 rejected/user-rejected |
| IFA 发行、独立通胀权限、重载保留证明 | 通过 |
| UDA 不可分割资产发行 | 通过 |
| UDA 跨钱包转移、实际 witness 输出及重载证明 | 通过 |

## 范围边界

本次未实现 Transcend/STP 的 RGB deposit/withdraw；该注册入口仍供后续可信桥接流程调用。测试使用临时本地节点和私有 WASM 构建，未部署远端节点。公开目录中既有 WASM 改动保持原状，实际浏览器 E2E 使用本次代码构建的私有 WASM。

初次 SDK 与 PWA E2E 被 macOS 沙箱阻止创建浏览器 Mach 端口，浏览器用例未执行。已用最小启动检查确认正常本机权限下浏览器可运行，并用现有配置指定 Playwright 自带 headless shell 重跑，未修改生产逻辑或放宽断言。
