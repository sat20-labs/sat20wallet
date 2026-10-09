# PWA 钱包基本功能验收

## 当前代码清单（2026-10-09）

钱包功能组现为 **90 项**（原 82 项加 Tools 2、真实 Mint/DID 3、Node 3）；独立强退/CSV 另有 **1 项**及两个既有前置，已接入 `verify:release-gate`。SDK 直接 WASM 为 **29 项**。编码结束时新增/增强代码全部未运行，历史记录保留原计数，不能外推为当前通过结果。用户随后授权仅运行新增例子，范围和结果见 [PWA cases](../../../docs/pwa-e2e-test-cases.md) 与 [SDK cases](../../../docs/sdk-e2e-test-cases.md)；编码阶段没有执行测试、编译、浏览器或服务。

## 运行入口

从 PWA 目录运行账户、恢复、RGB 持久化和本次新增的钱包功能验收：

```bash
cd /Users/yingfeng/github/sat20wallet/pwa
npm run verify:wallet-e2e
```

仅执行新增的基本钱包功能组：

```bash
npm run verify:wallet-e2e:functional
```

它们调用 SDK 现有的 Go E2E，而不是另外启动一套假钱包服务。对应的 Go 入口是：

| Go 测试 | 作用 |
| --- | --- |
| `TestSDKWalletPWAConnectedBrowser` | 当前钱包基本功能、资金/POS、工具、DApp、RGB 业务、挖矿和质押 |
| `TestSDKAccountPWAConnectedBrowser` | 账户目录、子钱包、账户安全、2of2/2of3、Guardian、恢复、密码、离线和失败边界 |
| `TestSDKCoreModulesE2E` | SDK 核心模块、真实 RGB provider、非空数据备份和 PWA 恢复 |
| `TestSDKAccountGuardianIdentitySurvivesAccountRecovery` | Guardian 自身通过根助记词或恢复材料换机，身份不变且旧好友分片仍可用于两种 Guardian 恢复组合 |
| `TestSDKAccountGuardianPaidPWABatchReview` | 自身 temporary 的 Guardian 续费 paid 托管，及原 AUTOPAY 交易在广播前退出／回执丢失后的续传 |

2026-10-08 已移除 9 个重复的定向浏览器回归入口（钱包组 6 个、账户组 3 个）。它们选择的业务用例全部保留在上述完整入口中，业务断言与必跑清单不变。Guardian 自身换机、付费托管和原 AUTOPAY 交易续传已纳入 `verify:wallet-e2e` 最终入口；登记不表示已执行或通过。

普通 `go test ./e2e` 也会发现这些测试。没有额外 build tag，没有默认跳过浏览器、POS 或 RGB 的开关。
依赖缺失时应明确失败。所需环境仍是现有 SDK 工作空间：同级的 `indexer`、`satoshinet`、`transcend`、
当前引用的 `rgb11`、匹配各 `go.mod` 的 Go，以及已安装依赖的 PWA、Node 和 Chromium/Chrome/Edge。
`SAT20_BROWSER_EXECUTABLE` 可指定本地浏览器可执行文件。

不要将 `scripts/live` 或需要连接现有 CDP 会话的旧验证脚本作为本套测试入口。

## 真实执行边界

- 真实 PWA 页面、Vue 状态、SDK WASM、签名与密码学、浏览器 IndexedDB、RGB provider。
- SDK 临时目录中的真实 Bootstrap、Core、Miner 进程，以及真实 STP、合约和 SatoshiNet Indexer。
- 只有 L1 Indexer 受测试控制：种子 UTXO/资产、交易接收、UTXO 变更、确认、区块和 Bitcoin 证据接口。
- L1 广播必须先解析真实交易并验证签名、输入消耗、价值守恒；绑定资产根据实际 sat 偏移流动。
  L1 预检不产生广播，重复广播不重复记账。
- 公共入金使用真实部署且已激活的 Transcend 合约；不能以直接写入 L2 余额或假合约状态替代。
- L2 资金来自合法 Anchor 和正常签名交易；测试控制器只推进网络、确认假 L1 和读取证据。
- DApp 端是随机 loopback 端口上的普通客户端，只发送生产 postMessage 请求和接收钱包回复。
  它不实现钱包、STP 或 RPC；授权必须在钱包页面完成。
- 浏览器阻止外部网络请求。PWA 的资产 API 与 WASM 使用同一批临时 Indexer；HTTP 转发保留真实响应。
- 使用当次 SDK 编译的临时 WASM 时，加载器仍验证实际字节的 MIME、大小、SHA-256 和 release ID。
  不关闭完整性校验，不修改发布用的 `public/wallet.wasm`。

浏览器每个独立功能组使用新的本地数据库。账户、RGB、资金等有前后依赖的流程保留同一组内的真实状态。
节点质押组最后执行，因为成功质押确实会改变候选节点集合，后面不再断言原三节点的固定轮换顺序。

## 功能与业务证据

下表是测试覆盖清单，不是某次运行的通过报告。只有日志中的实际 `pass` 才表示该次测试通过。

| 功能入口 | 基本操作 | 业务结果和证据 | 测试位置 |
| --- | --- | --- | --- |
| 创建钱包 | 密码、确认密码、展示助记词、保存后进入钱包 | 真实 SDK 目录只有一个钱包；刷新重新认证后地址和公钥相同 | `wallet-basic-e2e.mjs` |
| 导入钱包 | 页面输入助记词与新密码 | SDK 身份等于测试种子身份，确实读到受控 L1 资金 | `wallet-basic-e2e.mjs` |
| 钱包/子钱包管理 | 新建、选择、改名、删除和根钱包保护 | SDK 目录、页面身份、冷启动和独立设备恢复一致 | 现有账户 E2E |
| 账户管理与恢复 | 存储选择、2of2、2of3、Guardian、预览、提交、取消 | 真实恢复会话、远端 DKVS、独立 Guardian、备份和重新认证 | 现有账户及 CoreModules E2E |
| 收款 | 收款弹窗、QR、复制地址、独立收款页 | 核对实际 QR 画布编码的地址；剪贴板等于当前钱包地址 | `wallet-basic-e2e.mjs` |
| 公钥 | 展示公钥、计算服务节点通道地址 | 公钥与当前 SDK 一致；通道地址与实际 SDK 计算结果一致 | `wallet-basic-e2e.mjs` |
| 助记词 | 错误密码拒绝、正确密码查看、离开后重新进入 | 验证前不显示，显示内容匹配当前钱包，离开后清空 | `wallet-basic-e2e.mjs` |
| 密码 | 修改、拒绝旧密码、新密码冷启动解锁 | SDK 与 UI 持久数据均一致 | 现有账户 E2E |
| 生物识别 | 创建 PRF 凭据、刷新后生物识别解锁、删除后密码解锁 | 浏览器实际虚拟认证器、加密绑定持久化、断言计数增加和同一 SDK 钱包；不替换 navigator.credentials | `wallet-biometric-e2e.mjs` |
| 自动锁定 | 修改偏好、真实一分钟无操作、再次解锁 | 偏好持久化；计时器锁定签名能力；密码认证恢复同一身份 | `wallet-basic-e2e.mjs` |
| 隐藏余额 | 隐藏、刷新、显示 | 偏好持久化且展示变化不改变钱包身份 | `wallet-basic-e2e.mjs` |
| 语言 | 英文/中文切换和刷新 | 语言持久化，网络及钱包身份保持一致 | `wallet-basic-e2e.mjs` |
| 网络选择 | 错误密码、取消 | 不切换账户或网络、不丢失目录 | 现有账户 E2E；成功 mainnet 切换见边界表 |
| UTXO 管理 | 手动锁定 Bitcoin UTXO、刷新、解锁 | 真实 SDK 锁表随操作变化且跨刷新保留 | `wallet-basic-e2e.mjs` |
| 协议数据签名 | 完整消息预览、取消、确认 | 使用独立 ECDSA 校验确认签名绑定当前公钥及精确 payload | `wallet-basic-e2e.mjs` |
| 操作日志 | 查看真实操作、打开详情、清空 | 日志存在、不含密码/助记词，清空后钱包仍在 | `wallet-basic-e2e.mjs` |
| Bitcoin Send | BTC、ORDX 转账；BTC 高级多输出 | 真实签名原始交易、正确收款脚本、精确资产增量、确认和费用 | `pos-pwa-e2e.mjs` |
| BRC20 / Runes Send | 已有 transfer 铭文发送、从余额创建 transfer 后发送、带精度 Rune 部分发送 | 真实页面提交；实际铭文/Runestone、引用关系、独立预期余额、费用、取消及冷重载 | `wallet-l1-assets-e2e.mjs` |
| ORDX 高级拆分 | 从资产行进入 Advanced 并提交两个输出 | 应能正常启用；核对两个真实资产输出、确认费率与实际费用 | `pos-pwa-e2e.mjs` |
| SatoshiNet Send | BTC、ORDX 转账 | 正常签名、真实节点确认、收款方余额增量及发送方扣款 | `pos-pwa-e2e.mjs` |
| 私有通道开通 | 预览、确认、L1 等待时刷新 | 实际 L1 funding、合法 Anchor、PWA 状态恢复和预签 outpoint | `pos-pwa-e2e.mjs` |
| 私有通道扩容 | BTC、ORDX splicing-in，等待时关闭/重开页面 | 预签引用、资产数量、Anchor 身份和两端索引一致 | `pos-pwa-e2e.mjs` |
| 公共通道入金 | BTC、ORDX deposit | 真实 Transcend 处理、正确用户 Anchor 输出、L2 余额增加 | `pos-pwa-e2e.mjs` |
| 通道资产转换 | BTC、ORDX unlock → lock | 通道和外部 L2 余额按真实交易变化；往返回到预期状态 | `pos-pwa-e2e.mjs` |
| 私有通道出金 | BTC、ORDX splicing-out | 实际 L2 deanchor、L1 退款交易、确认后 Bitcoin 余额增加 | `pos-pwa-e2e.mjs` |
| 公共通道出金 | BTC、ORDX withdraw | 真实合约处理、L2 扣款与 L1 退款证据 | `pos-pwa-e2e.mjs` |
| 通道安全 | 当前 commitment 展示、合作关闭取消、强退确认取消 | 展示真实输入/输出、安全快照；取消不转移资产 | `pos-pwa-e2e.mjs` |
| 合作关闭 | 确认关闭、等待退款、重新显示 Open | 退款消耗正确 funding；BTC/ORDX 返回 L1；外部 L2 余额保留 | `pos-pwa-e2e.mjs` |
| POS v2 激活 | 已运行链在固定 H 激活，激活前排空入金 | H−1/H/H+1、正常 Core 重启、既有通道和资金不变 | `pos-pwa-e2e.mjs` / Go 控制器 |
| POS 出块与替补 | 正常轮换、Miner 下线、Core 替补、Miner 恢复 | 真正的生产者、获批签名、slot 奖励脚本及节点 canonical tip | `pos-pwa-e2e.mjs` / Go 控制器 |
| Core / Miner 质押 | 两个独立钱包分别确认质押，等待时刷新 | 正确 L1 资产/通道输出、真实 Anchor、节点角色/父节点/数量和 UI 索引 | `wallet-node-e2e.mjs` |
| DApp 连接 | 未授权拒绝、连接取消、明确授权、读取账户信息 | 真实跨 origin/source 消息、能力授权和 SDK 身份 | `wallet-integrations-e2e.mjs` |
| DApp 签名与撤权 | 无能力拒绝、签名取消、确认、断开 | 独立验证带 origin/network/nonce 的消息签名；撤权后拒绝访问 | `wallet-integrations-e2e.mjs` |
| DApp PSBT 与广播 | Bitcoin PSBT 预览、取消、签名，独立授权广播 | 独立验证 Taproot 签名、精确输出、500 sats 费用；签名不广播，坏签名拒绝，同字节重播不重复支付 | `wallet-integrations-e2e.mjs` |
| RGB 发行 | NIA、IFA、UDA | 真实 provider 生成合约、供应量/通胀权、载体和锁定 | `wallet-rgb-e2e.mjs` |
| RGB 合约文件 | 导出标准文件、另一设备导入 | 真正下载/导入；导入元数据不制造收款方余额 | `wallet-rgb-e2e.mjs` |
| RGB 收发 | OOB invoice、准备、收方验证、签名、恢复、广播、接收 | 真实包摘要、原始 witness tx、L1 确认、供应守恒及持久化证明 | `wallet-rgb-e2e.mjs` |
| RGB 取消 | 未广播 transfer 取消 | 释放 reservation，保留原资产载体保护，不广播 | `wallet-rgb-e2e.mjs` |
| UDA 转移 | 独立设备 invoice、接收方校验、发送方确认、广播和确认 | `rgb11:o:` 同一合约；错误摘要拒绝；原载体消耗，发送方 0／接收方 1，证明、锁与冷重载 | `wallet-rgb-e2e.mjs` |
| 模板合约 | Exchange 部署、供给 SGAS、查询状态/历史、关闭 | 真实签名 Work 及 canonical Result；库存进入合约、关闭归还部署者 | `wallet-tools-e2e.mjs` |
| Faucet | 支付确认取消、确认兑换 | 真实合约向收款脚本输出 GAS，实际余额增量与 Result 对齐 | `wallet-tools-e2e.mjs` |
| EVM 合约 | 浏览器编译当前 SDK probe、部署、生成 calldata、调用 | 真实 solc 字节、链上 Result 成功、counter 从 0 到 1、页面状态查询和历史 | `wallet-tools-e2e.mjs` |
| Agent 合约 | 配置预测参数、拒绝非法数据源 | 实际表单和 SDK 校验在签署前拒绝，未产生部署交易/扣款；真实模型成功流程见边界表 | `wallet-tools-e2e.mjs` |
| L1 协议部署 | ORDX/BRC20/Runes 参数检查、规范化、预览、取消 | 逐项核对额度、精度、self-mint、binding sat、网络/地址/费率，取消后无交易/预留/扣款 | `wallet-mint-e2e.mjs` |
| L1 协议铸造 | ORDX/BRC20/Runes 不存在的 ticker 检查 | 真实 SDK 读取假 L1 的已知状态，未取得铸造资格时不能进入签署；不宣称已完成 mint | `wallet-mint-e2e.mjs` |
| DID 铸造 | 非法名称拒绝、可用名称预览、取消、重复检查 | 使用真实 Indexer 的缺名返回契约，取消不登记 pending、不预留或花费；不宣称已完成注册 | `wallet-mint-e2e.mjs` |
| BTC Lucky Mining | 配置、启动、实际 worker 求解、提交、停止、刷新 | 假 L1 独立重算 BTC header/hash/target 验证工作量；停止后不再请求 job | `wallet-mining-e2e.mjs` |
| 推荐人 | 无 owned name 的注册保护、绑定取消、未知名字拒绝 | 不广播注册，不扣 L2 资金，不产生假绑定 | `wallet-basic-e2e.mjs` |

## 必须明确的覆盖边界

以下不能从当前基本门禁的成功推断为已经完成验收，也不能用 `skip`、预期失败或假 SDK 回包变成绿色：

| 功能 | 本套环境的边界 |
| --- | --- |
| 已安装 PWA、生产 Service Worker 升级与离线冷启动 | 当前复用 Vite DEV 入口。DEV 不注册生产 SW；刷新/重开和节点离线不等同于完整安装包升级验收 |
| 成功 mainnet 网络切换 | 本夹具只有隔离 testnet 节点。验证认证/取消，不连接公开主网或用 testnet 响应冒充 mainnet |
| 完整强退与 CSV 后清扫 | 当前检查真实安全快照和确认取消；完整强退需要单独通道及延迟高度推进，合作关闭已是资金回收路径 |
| L1 协议 commit/reveal 与全量索引语义 | 首版 Mint/DID 停在资格检查、确认预览和取消，尚未覆盖提交后的 commit/reveal。假 L1 现有签名、确认与绑定 sat 流能力也不能替代 ORDX/BRC20/Runes/DID 发行解析或据此宣称新资产到账 |
| 推荐人成功注册/绑定/奖励 | 真实注册有服务节点白名单和已持有名字前提；首版只覆盖前提/取消/拒绝，不伪造白名单签名或推荐人收益 |
| 预测 Agent 的模型审查与 oracle 结果 | 真实模型服务不是 L1 Indexer；本套不得复用 SDK 里假 OpenAI-compatible 服务作为成功证明。参数和签署保护不等于 Agent 业务完成 |
| BTC 全网算力、真实中奖和收益分成 | 仅验证本地真实 worker 和假 L1 job/提交；不使用公网算力接口，不把低难度测试工作量当成真实 Bitcoin 网络收益 |
| 所有协议、所有模板与所有参数组合 | 基本验收选代表业务路径；协议特有转账、所有模板分支和参数矩阵仍需对应专项 SDK/PWA 测试 |

## 2026-10-08 功能验收补充评估与新增用例

共享 fake indexer 增加协议能力，并不等于已经覆盖 SDK WASM 或 PWA 的对应业务。
`TestPOSPWAL1IndexerSharedBRC20AndRunes` 使用独立构造和签名的 Bitcoin 交易验证 HTTP 夹具，
没有经过 SDK 钱包选币、WASM、PWA 确认与提交。该用例也不在 `verify:wallet-e2e` 的名称过滤范围内。
下列 6 条已实现并加入现有完整门禁的必跑清单，不代表运行通过。

新增 **6 条**，复用现有资金、DApp、RGB 组与共享 fake，不增加 Go 浏览器入口：

| 建议 case | 当前缺口 | 必须验证的业务结果 |
| --- | --- | --- |
| BRC20 使用已有 transfer 铭文发送 | 现有浏览器 Send 只覆盖 BTC/ORDX；HTTP 夹具测试不能替代钱包发送 | 从资产卡选择并确认发送；真实签名与收款脚本、精确余额、费用、已消费铭文不可再转移；冷重载一致 |
| BRC20 无现成 transfer 铭文时发送 | SDK 会构造 transfer 的 commit/reveal，再发送，当前夹具回归只使用预置可转移载体 | 从已有余额创建真实 transfer；核对 inscription payload、交易引用、接收金额及发送方扣款；必要步骤失败不得制造到账或重复花费 |
| Runes 部分发送与找零 | 当前只有原生 HTTP 的整数分配验证 | 使用非零 divisibility 与带 spacer 的名称；确认数量与真实 Runestone、收款/找零原子量一致；精度非法或取消不广播 |
| DApp PSBT 签名 | 此前 DApp 组只覆盖连接、消息签名、撤权 | 独立构造 Bitcoin PSBT，页面授权后核验实际 Taproot 签名和精确输入/输出；签名不广播；无权限、取消、畸形 PSBT 拒绝。SatoshiNet PSBT 的成功分支仍未纳入本次 6 条 |
| DApp 原交易广播 | 当前没有交易广播的用户授权成功路径 | 单独取得广播权限并逐次确认；广播签名 case 的同一交易；独立金额预期、TXID、确认及收款一致；取消不广播、重播不重复记账 |
| UDA 实际转移 | 此前 UDA 用例验证发行、`rgb11:o:` 名称和余额 1；NIA 收发不能证明 NFT 转移 | 通过现有 invoice 收发把唯一 UDA 转给另一设备；发送方 0、接收方 1、合约不变、实际载体/证明及锁正确、冷重载一致；数量错误的接收摘要不能取得广播资格 |

另有以下使用路径需要保留为待验收，不能凭原生测试或预览取消关闭：

| 使用路径 | 已有证据与补充边界 |
| --- | --- |
| RGB 地址发送、ACK 与重开后继续 | `TestSDKCoreModulesE2E` 已覆盖原生真实节点直传、ACK 后广播、资产守恒及恢复；浏览器尚未覆盖地址模式和 pending 任务重开。应补正常完成、等待 ACK 重开两条。当前 `enableRGB11AddressReceive` 在 PWA 只有包装与验证脚本调用，未找到正常页面启用入口；需先明确用户收款入口，不能测试中直接启用后声称完整页面流程已通过。无 ACK/拒绝 ACK 不得广播，续跑应使用同一 transfer/TXID |
| L1 铸造/部署成功 | 现有 Mint/DID 停在资格检查、预览、取消，部分/尾额 Mint 也未真正提交。优先选一条合法部分/尾额 Mint 和一条 BRC20 commit/reveal 正常路径，核对实际 payload、确认及索引结果。共享 fake 仍是 Transcend 原有协议子集；现有 `PWAMINT` 权限响应是固定夹具，不是发行索引证明，不能手工补 ticker/到账冒充成功 |
| RGB proxy 收发 | 原生 HTTP/provider 测试覆盖 transport/ACK，浏览器当前只覆盖 OOB。应使用现有 proxy 测试能力或明确的本地服务，验证真实页面交付、ACK、接收与冷重载；L1 fake 不能充当 proxy。复用方式确认前不新增独立服务框架 |
| 通道强退后取回资金 | 现有页面只覆盖安全快照、确认和取消；合作关闭不能替代强退。需要独立通道完成真实 commitment 广播、CSV 等待和最终资金回收，预期来自操作前权益与费用；放在独立验收组，不能挤入已有资金组而改变后续状态 |

Guardian 已纳入最终清单。当前账户浏览器脚本已使用用户从页面复制的最终恢复码与用户分片，
关闭原设备，再在新 context 完成 knowledge+Guardian、share+Guardian 恢复；无需再增加等价重复用例。
Guardian 自身换机与 temporary 自身备份下的 paid 托管续费，仍按上方已登记的专门入口验收。

新增独立业务使用各自的页面、钱包身份和初始余额，复用同一节点及控制器；
同一业务内的准备→签名→广播→确认保留真实依赖，前序失败后后续标记 `not-run`。
不得依靠失败 case 的成功收尾恢复下一独立 case 所需的余额或合约状态。
最终断言使用提交前已知数量、精度、协议费用和钱包权益，不能从实际到账反推全部预期。

当前钱包组静态登记共 **82 条**：原 76 条加本次 6 条（L1 协议 3、DApp 2、UDA 转移 1）。
新的 BRC20/Runes、DApp、UDA 分别使用独立资金身份；UDA 使用独立 context，并且原 RGB 组失败不会阻止其开始。
新增用例复用现有 `--wallet-cases` 精确选择、`not-run` 记录和同一 Go 浏览器入口。
本轮已通过 JavaScript 语法检查及 SDK E2E 包编译，没有修改生产代码。
业务用例尚未执行：准备启动时，共享运行锁由另一轮 `TestSDKCoreModulesE2E` 持有，因而没有启动第二轮浏览器运行等待该锁。
下方既有运行记录保留其历史清单和分母，不计入本次 6 条的验证结果。

## 失败判定与排查

每个 case 输出 `running`、`pass` 或 `fail`。有依赖的前序失败后，后续必需 case 会明确输出 `not-run`。
独立功能组继续执行；Tools 的模板/EVM/Agent、DApp/RGB/WebAuthn、各 Mint 协议和两种质押也各自保留失败并继续。最终摘要包含 required/pass/fail/not_run，任何失败或未执行必需项使进程非零退出。
不得把缺少浏览器、WASM 编译失败、节点启动失败、合约未就绪或 Indexer 不支持某接口当成成功。

截至 **2026-10-08 04:30 UTC** 的源码核对，钱包功能入口登记 **75 个必需检查**：资金/POS/通道 25、
基础钱包 14、DApp 6、RGB 10、WebAuthn 4、Tools 6、Mint/DID 5、挖矿 2、节点质押 2，
以及无未处理浏览器错误 1。共享 WebAuthn 组在本次运行后增加了密码修改与重新注册验证，因此钱包组由原来的 74 项增至 75 项。

同一时点，账户入口为 **63 项**（59 条账户检查加共享的 4 条 WebAuthn），RGB 恢复入口为 **9 项**。
三个入口合计 **147 个浏览器执行项**，按用例名称跨入口去重为 **143 项**；Go/WASM 存储测试及 SDK 自身用例另行执行。
本轮实际执行的是更早的清单：账户 57、RGB 恢复 9、新增钱包 74，共 140 项。下面运行记录保留该历史分母。
这些数量描述已登记用例，不表示已运行或通过。

页面失败会输出当时的 URL、heading、alert；Go 一侧保留节点日志位置以及控制动作错误。
链上结果需要组合检查：实际 txid/raw bytes、确认、资金脚本/数量、Anchor funding outpoint、各节点 AIDX 和钱包状态。
节点 `/bestheight` 只是 API 暴露的链 tip，不是独立数据库 flush 高度；真正的 Anchor 索引就绪由逐节点 AIDX 记录验证。

早期静态核对发现的资产行 Advanced 与 RGB UDA 余额问题继续保留为普通必需回归用例。
2026-10-08 再次核对源码：Advanced 与费率判断已统一接受 `bitcoin`/`l1`；资产列表的 `n` 过滤已限定到 ORDX。
UDA 的 NFT 资产类型应为 `rgb11:o:`，当前浏览器发行用例也要求该类型与真实可用余额 1。
测试仍需验证用户可拆分并得到正确输出、UDA 页面显示真实余额，不能把禁用或零余额当作通过。
这些源码修正不代表本次已重新执行浏览器回归；下方保留旧次运行记录。

新增组覆盖的是正常运行和可控激活，不扩展历史 Anchor 跨 H 迁移或深度 reorg 的假设。

## 2026-10-08 实际运行记录

### 当前状态

本轮已恢复任务并执行两次 SDK E2E。测试实现与局部修正已保存；**整体验收尚未通过，本轮构建登记的新增 74 项未执行；随后扩展到 75 项的当前钱包组同样尚待复跑。**

首次运行 `20261008T102839-1552-e2e` 在编译阶段失败：`pos_pwa_l1_test.go` 的两个断言将
`INVALID_ID` 传给接口参数时默认推断为 `int`，常量溢出。两处均已改为显式 `uint64(indexercommon.INVALID_ID)`。
第二轮已成功编译并真正执行 SDK、WASM 与浏览器测试，因此这两处编译问题已有实际验证。

第二次运行的记录：

| 项目 | 实际值 |
| --- | --- |
| run ID | `20261008T103030-1810-e2e` |
| 开始时间 | 2026-10-08 10:30:30 +08:00 |
| 结束时间 | 2026-10-08 12:11:01 +08:00 |
| 进程结果 | `FAILED`，exit code 1 |
| supervisor 耗时 | 6031 秒 |
| Go 包内耗时 | 6002.316 秒；全局 100 分钟 timeout |
| 实际命令 | `/usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS /usr/local/go/bin/go test -p=1 -parallel=1 ./e2e -timeout=6000000ms -count=1` |

原始记录保存在：

- `/Users/yingfeng/mcp/local_access/logs/test-runs/sat20wallet-sdk-test/20261008T103030-1810-e2e.status`
- `/Users/yingfeng/mcp/local_access/logs/test-runs/sat20wallet-sdk-test/20261008T103030-1810-e2e.log`
- `/Users/yingfeng/mcp/local_access/logs/test-runs/sat20wallet-sdk-test/20261008T103030-1810-e2e.tail`
- `/Users/yingfeng/github/sat20wallet/sdk/review-evidence/core-modules-e2e-latest.json`：本轮读取的版本 SHA-256 为
  `de37231752012a8f9298566cd8927bfcaf367347df6c8f85194617292a0e2631`；后续运行可能覆盖该 latest 文件。

### 浏览器实际结果

以下按日志中的唯一 case 去重，只将明确输出 `pass` 的检查算作通过。行数不包含 Go 父测试、子测试、
WASM 存储检查或日志重复输出。分母是本轮构建时的登记数量，不能拿随后新增的账户用例改写历史结果。

| 功能组 | 本轮登记 | pass | fail | 已开始、无完成结果 | 未开始 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 现有账户 PWA | 57 | 32 | 1 | 1 | 23 |
| 现有 RGB 恢复 PWA | 9 | 3 | 2 | 0 | 4 |
| 新增钱包基本功能 | 74 | 0 | 0 | 0 | 74 |
| 合计 | 140 | 35 | 3 | 1 | 101 |

共有 102 项未取得完成结果。新增 `TestSDKWalletPWAConnectedBrowser` 未被运行到；不能将其记录为通过或失败。

### 需要复跑确认的失败

1. 账户 DID 持久化用例在初始账户同步的 `settled()` 等待中超过 90 秒。此时尚未配置恢复，
   已完成的 DID 本地保存、重载和显示检查本身成功；不能将此错误解释为“恢复 DID 失败”。下一次应结合新增的同步诊断字段判断原因。
2. RGB cold remote import 在撤销注入故障后，90 秒内仍停在 `remote-apply / provider-import`，
   marker 的 `state_revision=14`、`data_revision=7`。页面保持锁定并报告托管数据导入未完成。
   本轮 native 的 RemoteApply/Rebase provider 恢复用例通过，但这不能替代浏览器重载路径的失败。
3. RGB recovery UI 故障用例未在 30 秒内观察到预期 alert。本轮构建后，PWA 的钱包身份持久化实现和该测试均有更新：
   当前身份由 SDK 负责，旧的冗余 IndexedDB 身份快照写入已被移除，最新用例改为验证即使旧写入故障存在也能正常进入解锁。
   保留了该最新测试改动；旧构建的失败需要通过新构建复验。
4. 原生 Guardian 身份恢复的 root-mnemonic 与 recovery-material 两个子测试返回 `key not found`。
   该测试二进制构建后，Guardian 实现及其测试已被更新为从恢复的 root 派生身份；旧日志不能证明最新代码仍有同一问题。

账户浏览器入口在 30 分钟总预算后被终止；最后一个“remote deletion … unrelated changes”用例只有
`running`，没有明确断言失败。RGB 浏览器子进程的 `signal: killed` 对应
`sdk/wallet/core_modules_e2e_browser_test.go` 的 18 分钟上下文，Core 外层报告 `timed_out: false`。
缺少逐用例耗时证据时，不能将被终止的用例定性为死锁，也不能仅靠延长预算宣布修复。

整包最终在 100 分钟触发 Go testing alarm。当时 `TestRealSatoshiNetEVMInternalERC20` 刚运行 7 秒，
仍在启动节点并等待 Core RPC，没有执行到 ERC20 合约操作。账户和 Core 两个入口已耗时约 57 分钟；
不能将整包超时归因于最后出现名称的 EVM 用例。

本轮执行期间 SDK/PWA 源码仍在并发更新，native 二进制及不同阶段构建的 WASM 不代表最终同一份源码快照。
所有上述生产行为判断都应以一致的新构建复验为准。

### 已落实的局部测试修正

| 文件 | 修改目的 |
| --- | --- |
| `sdk/e2e/pos_pwa_l1_test.go` | 对两个 INVALID_ID 断言显式转换为 uint64，修复常量溢出；第二轮已实际编译通过 |
| `pwa/scripts/verify/account-management-e2e.mjs` | 每个 running/pass/fail 事件增加开始时间和耗时，用于定位时间预算消耗 |
| `pwa/scripts/verify/account-management-usage-e2e.mjs` | 账户同步失败时输出最后一次正常轮询的状态字段；cold RGB peer 无论成功失败都恢复原名称，并保留原始失败 |
| `sdk/wallet/core_modules_e2e_test.go` | Guardian 重配置前执行正常账户同步，避免前一个浏览器子测试失败跳过同步后制造级联失败 |

同步失败诊断只记录 pending/dirty、版本和最后同步错误等字段；失败后不额外调用 SDK。
cold retry 的 90 秒断言、必需用例和退出码仍保持原意，没有清除恢复 marker 或将失败标为预期成功。
前一个 PWA 成功路径的同步与名称断言也保留。

写入前发现并发更新后，已将这些局部改动合并到最新文件，没有覆盖新的账户用例或 RGB 快照验证逻辑。
本轮没有修改生产文件。最终 JavaScript 改动通过 `node --check`，写入后已核对远端内容。
新增的 Go 同步修正和这些 JavaScript 行为仍待新一轮编译、浏览器执行；静态语法通过不代表功能验收通过。

### 下一次运行前提和入口

当前已确认主测试进程结束，尚未确认超时前启动的节点及共享 runtime lock 是否释放。
`sat20wallet-test-process-status` 只查原 supervisor 及其后代；原 supervisor 已结束后的空进程树不能证明没有遗留节点。
现有 `sat20wallet-test-cleanup` 的 nohup 路径只处理仍为 RUNNING 且 supervisor 身份匹配的运行，
不适用于本轮已 FAILED 的状态。Go 全局 timeout 也不保证执行节点的 `t.Cleanup`；节点会继承 runtime flock。

先在本机只读检查共享锁持有者及其命令身份；如确认存在本次运行遗留的虚拟节点，只清理对应进程，再确认锁释放。
不能删除锁文件、改写运行状态或绕过锁。此步骤需要能识别锁持有进程的执行能力；目前暴露的 MCP 状态/清理 profile 不提供该能力。

随后先使用已存在的定向入口：

```bash
cd /Users/yingfeng/github/sat20wallet/pwa
npm run verify:wallet-e2e:functional
```

该入口仍通过 SDK `./e2e` 和相同虚拟网络执行，只选择新增钱包测试，预算 55 分钟并启用 `-v`。
通过后再运行 `npm run verify:wallet-e2e` 复验三个 PWA 入口；根据逐用例耗时证据评估预算，
不将未经诊断的 timeout 仅靠扩大阈值处理。

当前 MCP 已暴露的 `sat20wallet-sdk-test-launch / e2e` 执行整个 SDK E2E，
`sat20wallet-focused-test` 作用于 `./wallet`，均不是上述新增钱包专用入口。
应由本机执行现有 npm 命令，或由 MCP 操作员暴露对应固定入口后再调用；
本轮没有修改 MCP 的白名单、执行脚本或测试发现机制来绕过此限制。
