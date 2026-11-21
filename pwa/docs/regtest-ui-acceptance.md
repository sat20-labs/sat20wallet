# 办公 Mac 全新 regtest：PWA 钱包 UI 验收

正式 UI 路线只有一条：**在办公 Mac 上从空数据启动本轮隔离的 regtest，在同一台 Mac 上用实际 Chromium 验收 PWA**。103 只跑核心库 Go 原生测试，不参与 UI。无需旧节点、旧数据、旧浏览器、旧 attach、远程转发或现有 Vite。

本文命令已按当前脚本静态核对；本文修订本身没有启动服务或运行用例。

## 1. 候选与隔离

1. 在办公 Mac 冻结一个本轮候选工作区。按 runner 要求保留相邻依赖布局，记录 Wallet/PWA、SDK、L1 Indexer、聪网及本模块实际使用的 RGB11/Transcend 源码 HEAD、未提交差异、Go replace 路径、Go/Node/浏览器版本。不得从另一任务整树覆盖。历史 PWA 的两处测试前置调整只有在涉及文件释放、前像与当前候选核对后才带入；不得为通过而放宽真实备份或产品写入门禁。
2. 准备与实际测试并行；只等**当前模块**所依赖的 SDK/Indexer 等相关自动化和写入资源。先放行 L1 BTC/ORDX，不等待无关的 L2、RGB11 或 103 全量 Go；L2、RGB11 各按自己的依赖放行。每组一名执行者。核心 Go 与 PWA 不共用可写工作区、链数据、钱包、浏览器 profile 或端口。
3. 使用已安装的 Core 31.0、配套 bitcoin-cli、支持 HTTP Esplora 的 Blockstream Electrs、Go、Node、Chromium/Playwright 和相邻 Indexer 源码。必要时把已批准的可执行文件绝对路径传为 `SAT20_REAL_BITCOIND`、`SAT20_REAL_BITCOIN_CLI`、`SAT20_REAL_ELECTRS` 或 `GO`。缺工具先报缺项；如另获安装授权，参考[仓内工具脚本](../scripts/verify/install-real-regtest-tools.sh)。
4. 用私有空运行目录（权限 0700）。不加载旧任务的环境文件、datadir、钱包、端点、浏览器存储或凭证。wrapper 会为每组生成隔离链和浏览器状态，并清除继承的 attach/旧 UI URL，不手配它们。只用 loopback。同机 wrapper UI 默认 5189，聪网默认端口基数 30250（使用偏移 0–4、10–14）；Core/Indexer/Electrs 端口由本轮 wrapper 管理。运行前确认选中端口未被任何现有服务占用；有冲突就给本轮选空闲的 `SAT20_PWA_E2E_PORT`、`SAT20_REAL_SATOSHINET_PORT_BASE`，不能停止、重配或接管别人的服务。
5. 钱包用 wrapper 提供的仓内 regtest 合成夹具，在本轮新链和新浏览器上下文导入/创建。结构参见 `sdk/e2e/testdata/regtest/wallets.json`：角色 bootstrap、core、ordinary-1、ordinary-2；角色、公钥和实际节点须对应。不得复用有现实资金的身份。任何实际私有夹具只能存于本轮 0600 私有文件，不回显助记词、恢复材料、口令和 RPC cookie，不进仓库或报告。账户未绑定时核本地状态；已要求真实备份的浏览器用例必须等待实际绑定/备份完成。原生 Go 场景的 local-only 放行不能用来绕开 PWA 的用例前提。

## 2. 从空数据执行，先 L1

在冻结后的 `sat20wallet` 根目录运行下面命令，每条分别检查退出状态，失败即停在该组：

```sh
node pwa/scripts/verify/workspace-e2e.mjs --plan --only pwa-canonical \
  --tests TestSDKPWARealRegtestBitcoinFundsSendBrowser
node pwa/scripts/verify/workspace-e2e.mjs --check --only pwa-canonical \
  --tests TestSDKPWARealRegtestBitcoinFundsSendBrowser
node pwa/scripts/verify/workspace-e2e.mjs --only pwa-canonical \
  --tests TestSDKPWARealRegtestBitcoinFundsSendBrowser
```

`--plan` 只列组、实际入口和有序前置；`--check` 只检查本地工具，不证明节点、账户、PWA 或交易已通过。第三条才会创建本组 Bitcoin Core、L1 Indexer、Electrs，按 Go 真实准备阶段开本机聪网测试节点和准备资产，构建/启动本轮 PWA，运行实际 Chromium 操作，并进行真实 regtest 交易。虽然 L1 的通用准备会用聪网夹具，此命令并未选择聪网普通汇款业务。见 [wrapper 源码](../scripts/verify/workspace-e2e.mjs)。不通过直跑裸 Go、接旧站或 mock RPC 来补做所缺浏览器步骤。

本轮必须核对浏览器实际加载的是冻结候选：记录构建时 SDK/本地依赖指纹、候选 PWA 版本、实际 WASM/JS 的 SHA-256、浏览器加载同一制品的证据和实际来源；可对照 [浏览器 runner](../scripts/verify/account-management-e2e.mjs) 的生成完整性清单。不能以陈旧的 `dist`、旧 Vite、缓存或只查磁盘文件代替浏览器实际加载。验收若需要单独制品预构建，只在隔离候选 `pwa/` 用 `npm run build:regtest`；该构建会写生成物，不在正式工作区覆盖 `public`、生成文件或 `dist`。预构建不代替本轮 UI 启动或完整性回读。

### L1 通过标准

同一个根用例下核对四个实际浏览器业务叶用例，不把根 PASS、准备步骤 PASS 或 skip 记成四项通过：

| 顺序 | 实际 UI 场景 | 必须核对的结果 |
| --- | --- | --- |
| 1 | 普通 BTC 发送 | 实际确认与广播、唯一 TxID、收件 BTC 和余额变化、找零及网费。 |
| 2 | 普通 ORDX 发送 | 同一 TxID 在 Core 和真实 Indexer 可追溯；资产标识、数量及收件/找零正确，不误花受保护 UTXO。 |
| 3 | BTC 双输出 | 同一笔 Tx 中有两个预期收件输出，分别核对金额和两个收件余额；不要把找零算作第三个收件输出。 |
| 4 | ORDX 双输出 | 同一笔 Tx 中两个预期的收件资产输出，核对地址、资产身份和数量；找零、网费和余额守恒。 |

每项从真实 UI 选择网络、账户、资产、地址和额度并实际提交，随后核对区块高度/确认或本用例要求的最终状态、输出位置及余额，不把广播提示或 mempool 单独算作最终通过。保留可用的 UI 截图/日志和节点/Indexer 读回；每次提交只计一次 TxID。

## 3. L1 过后，分模块继续

- **聪网普通汇款**：相关 SDK/聪网测试和资源就绪后，重复上面的 `--plan`、`--check`、正式命令，只将精确 selector 换成 `TestSDKPWARealRegtestSatoshiNetPlainSendBrowser`，仍用 `--only pwa-canonical`。这是新组、新本机链，不承接 L1 数据。实际从真实 Anchor 准备有余额的测试账户，经 UI 发送既有用例的 2,000 sats；核唯一 TxID、出块/确认、双方余额、收件输出和网费。
- **账户与 RGB11**：在对应自动化放行后，按 `--only pwa-canonical --tests TestSDKPWARealRegtestFixedAccountManagementBrowser` 或 `--only pwa-canonical --tests TestSDKPWARealRegtestRGBBrowser` 分组执行；跨层另选 `--only pwa-rgb-cross-layer --tests TestSDKPWARealRegtestRGBCrossLayerBrowser`。各组先看计划里的真实准备与依赖步骤。账户冷恢复要独立浏览器上下文、核相同 root/恢复包和恢复后的余额与历史；RGB11 记录实际合约 ID、proof、TxID/确认、数量及收件状态。其它资产/通道/合约只跑本轮受影响并有真实用例的模块。
- **RGB11 外部钱包互操作**。使用官方 `rgb-cmd 0.11.2-rc.2`，在同一条本地 Regtest 链上先验 NIA 双向收发。记录实际执行的 PWA UI、CLI、回执、确认和余额。此项单独执行、单独出结果。 [第三方互操作验收指引](rgb11-third-party-interoperability.md#当前验收基线)

## 4. 证据、失败与恢复

- 每组记录 Run ID、实际工作区与指纹、工具与制品版本、私有运行目录、监听 PID/端口、链网身份、所选根及前置和真实叶用例。在 wrapper 打印的运行目录查 `summary.json` 与分步日志，核实际退出码、UI 证据、TxID/区块/输出、资产与余额。敏感值不写报告；证据目录 0700、含运行数据的文件 0600。
- 第一个失败停相关后续写入。先区分工具/端口、候选制品、链与索引同步、账户/资产前置、UI/SDK 产品失败。出现超时先查原交易和余额，不点击第二次发送；没确认原动作结果不得重试。保留最小现场，给出失败入口、最后成功步骤、错误和失败证据。生产修复依法按既定审批先给复现、根因和修复方案；只复跑受影响模块。
- 失败现场只用于定位，不人为把半轮状态改成通过。修复放行后从新 Run ID、新空数据按原 selector 重跑。结束时让 wrapper 管理本轮自建进程；人工清理由本轮 owner 凭运行目录/PID 仅处置本轮对象，检查端口释放，不删除原测试数据、不重启其它任务。非预期长期保留先说明，避免堆叠历史归档。
