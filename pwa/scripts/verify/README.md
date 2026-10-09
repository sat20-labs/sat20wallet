# SAT20 PWA Verification Scripts

These scripts exercise the SAT20 PWA wallet against the test networks.

## 2026-10-09 测试代码更新

编码阶段按用户要求只补代码，未运行测试、构建、浏览器或节点服务。SDK 直接 WASM 清单为 **29 项**，钱包功能清单为 **90 项**，另有独立强退/CSV **1 项**及两个必要前置；账户、RGB 恢复、生产发布态和 Guardian 入口继续保留。新增代码与静态检查不能当作运行通过。用户随后授权仅运行新增/增强用例和必要前置，结果见两份 case 文档。

完整清单、剩余覆盖边界与历史运行快照分别见 [SDK E2E cases](../../../docs/sdk-e2e-test-cases.md) 和 [PWA E2E cases](../../../docs/pwa-e2e-test-cases.md)。正式发布入口为 `npm run verify:release-gate`，独立强退入口为 `npm run verify:wallet-e2e:force-close`；编码阶段未执行这些命令，后续定向验证也不启动完整发布门禁。

## 本地钱包基本功能验收

完整验收入口复用 SDK 的真实临时 Bootstrap、Core、Miner 和真实 SatoshiNet Indexer；只有 L1 Indexer 受控。
测试使用真实 PWA、当次编译的 SDK WASM、STP、合约、签名、RGB provider 和浏览器 IndexedDB。完整命令也包含 Guardian 自身换机恢复、temporary 账户的 paid 好友托管续费，以及原 AUTOPAY 交易续传。

L1 HTTP 夹具与 Transcend 的虚拟网络共用 SDK 的 `testutil/fakeindexer` 资产模型，复用 ORDX、BRC20 和 Runes 模拟能力；SDK HTTP 层继续校验真实签名并显式确认交易。模型与边界说明见 [共享 fake indexer](../../../sdk/testutil/fakeindexer/README.md)。迁移后只完成编译检查，尚未重新运行浏览器业务验收。

```bash
cd /Users/yingfeng/github/sat20wallet/pwa
npm run verify:wallet-e2e
# 仅新增的钱包功能、资金/POS、工具、DApp、RGB、挖矿和节点质押组
npm run verify:wallet-e2e:functional
```

这两条命令会自行启动 Vite 和隔离浏览器，不需要现有钱包、公开测试网络或 CDP 会话。
完整功能映射、交易与余额断言、运行依赖和未覆盖的专项边界见 [WALLET_ACCEPTANCE.md](./WALLET_ACCEPTANCE.md)。
缺少依赖、失败或必需用例未执行均返回非零退出码；没有默认跳过开关。

PWA 隔离节点默认使用 3 秒出块／预告间隔、1 秒检查间隔；生产节点配置不变。
需要尝试 1 秒时，可在上述命令前设置 `SATOSHINET_POS_MINER_INTERVAL=1`
和 `SATOSHINET_POS_PREWARNING_INTERVAL=1`。间隔调整不能省略交易确认、节点收敛或激活高度检查；
1 秒配置尚未完成这组浏览器回归验证。编译和浏览器启动耗时不受出块间隔影响。

## Prerequisites

The local account E2E starts its own server and browser; follow its dedicated section below.
For the other scripts, start the PWA dev server, then launch a Chromium-based browser with remote debugging:

```bash
npm run dev
```

Example browser command:

```bash
"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge" \
  --headless=new \
  --remote-debugging-port=9223 \
  --user-data-dir=/private/tmp/sat20-pwa-verify-profile \
  --no-first-run \
  --disable-extensions \
  --disable-gpu \
  about:blank
```

## L1 Orderbook

The L1 script uses the built-in test mnemonic and password by default. Override them with:

- `SAT20_TEST_MNEMONIC`
- `SAT20_TEST_PASSWORD`
- `SAT20_CDP_URL`

The seller, buyer, and order size can be selected without editing the script:

```bash
SAT20_L1_SELLER_ACCOUNT_INDEX=1 \
SAT20_L1_BUYER_ACCOUNT_INDEX=0 \
SAT20_L1_SELL_AMOUNT=1000 \
npm run verify:l1-orderbook
```

Defaults remain seller account `0`, buyer account `1`, and `1000` DOGCOIN.
The seller needs the requested DOGCOIN amount in a spendable asset UTXO. With
the default sell amount, the buyer needs one plain BTC UTXO of at least `4900`
sats. In general the minimum source value is `3900 + sell amount` sats because
the fixed unit price is one sat per DOGCOIN. The script broadcasts on the real
testnet by default; set `SAT20_DRY_RUN=1` only for a no-broadcast preflight.

## 本地账户管理 E2E

账户管理测试使用 SDK 现有的临时 SatoshiNet 夹具：真实 Bootstrap/CoreNode/miner、真实
WASM、真实 PWA，以及每台设备独立的 SDK 与 UI IndexedDB 数据库。只将 PWA 的端点配置指向临时节点，
不替换账户、密码学、存储或 RPC 实现。浏览器阻止访问外部地址。

前提是完整本地工作空间：`sat20wallet` 同级存在 `indexer`、`satoshinet`、`transcend` 和 SDK 当前引用的 `rgb11`，
保留各模块的本地 `replace`，并安装满足各 `go.mod` 的 Go，以及 Node/npm。

准备 PWA 依赖：

```bash
cd /Users/yingfeng/github/sat20wallet/pwa
npm ci
```

SDK 的三个浏览器入口均自动在测试临时目录编译当前版本的 WASM，并使用匹配 Go 版本的 `wasm_exec.js`。
真实加载器继续校验这些临时字节的 MIME、大小、SHA-256 和 release ID，无需先更新 `public/` 发布产物。

已安装 Microsoft Edge 或 Google Chrome 时直接使用其无头模式；其他系统可以使用
Playwright Chromium：

```bash
npx playwright install chromium
```

运行 SDK 账户门禁和浏览器账户门禁：

```bash
cd /Users/yingfeng/github/sat20wallet/sdk
# 一次运行全部 SDK 账户用例及 PWA 浏览器用例
go test ./e2e \
  -run 'Test(SDKAccount|RealSatoshiNetAccountManagement)' -v -count=1 -timeout=65m

# 只运行浏览器账户门禁
go test ./e2e \
  -run '^TestSDKAccountPWAConnectedBrowser$' -v -count=1 -timeout=35m
```

第二条命令自动启动并关闭 Vite、隔离浏览器和本地节点，不需要另开开发服务器或 CDP
端口。首次运行或 SDK 代码变动后会编译节点与插件，耗时明显长于复用缓存的运行。可通过
`SAT20_BROWSER_EXECUTABLE=/absolute/path/to/chromium` 指定浏览器。

普通 Go E2E 默认执行账户及 RGB 浏览器用例；缺少 Node/Chromium 等依赖时明确失败。浏览器用例逐项输出 JSON verdict，任意失败返回非零退出码。

浏览器门禁会先编译并在真实 Chromium IndexedDB 上运行 `wallet/lightnode` 的 Go WASM 存储回归，
随后验证真实节点业务。新增故障场景检查改名、创建/导入钱包、添加子账户和元数据修改遇到 profile 写入失败时，
API 返回错误且目录与持久数据不变，页面重启后仍保留原状态。

仅执行浏览器存储回归（复用同一 Playwright/Vite 入口，不需要节点）：

```bash
cd /Users/yingfeng/github/sat20wallet/sdk
storage_dir=$(mktemp -d)
GOOS=js GOARCH=wasm go test -c -o "$storage_dir/storage.test.wasm" ./wallet/lightnode
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$storage_dir/wasm_exec.js"
cd ../pwa
node scripts/verify/account-management-e2e.mjs --storage-wasm "$storage_dir/storage.test.wasm"
```

Node 的默认 `go_js_wasm_exec` 不提供 IndexedDB，不能用它替代上述真实浏览器验证。

当前浏览器门禁覆盖首次创建、根助记词自动发现与导入、账户预检、临时存储授权恢复/取消、2of2 设置与恢复、
子账户改名、根钱包删除保护、独立 Guardian 的 setup/receipt/请求/响应、2of3 恢复、
会话取消/重复提交、密码修改后刷新解锁、节点 API 不可达时刷新并用新密码解锁，以及网络切换密码错误/取消。
离线刷新验证 SDK IndexedDB 持久化的钱包目录、账户身份和恢复配置，以及独立的 UI IndexedDB 状态；确认 DKVS 副本的离线读取另由 SDK 回归覆盖。
恢复页面另外通过真实按钮完成恢复码、知识问题、用户分片、预览和新密码步骤。
新增浏览器场景也检查离线目录编辑与另一设备并发变更的合并、恢复提交后响应丢失，以及设置页面复用真实 AUTOPAY。
实际充值成功仍由 SDK 的真实节点 E2E 覆盖，浏览器检查付费确认取消和无充值的授权复用。

账户 review 回归另外覆盖锁屏阻止账户写入、日志等待期间锁屏使请求失效、远端删除当前钱包后
PWA 的选择/地址/公钥回退与重载一致、重复钱包名和超限子账户拒绝、恢复页面取消清理会话、
Manager release 清理预览，以及 2of3 的知识+用户、知识+Guardian、用户+Guardian 三种组合。
用户+Guardian 组合通过实际恢复页面按钮完成，不要求知识答案。

PWA 的账户写入按单实例使用约束验收。用户明确排除改密期间新增钱包，以及共享数据库中仍可写的旧 SDK Manager 在改密后写回旧凭据；对应历史复现保留，不要求新增多 Manager 条件提交机制。正常密码重新认证、冷启动解锁和已列出的跨页身份通知场景仍保留验证。

失败回归复用同一套 SDK/PWA 夹具，按应有行为断言；任何回归都以普通失败阻断门禁，
不使用 skip、预期失败或放宽断言。可以单独执行 SDK 的新增组：

```bash
cd /Users/yingfeng/github/sat20wallet/sdk
go test ./e2e -run '^TestSDKAccountFailureRegressions$' -v -count=1 -timeout=20m
```

该组每例使用独立账户和数据库，覆盖：删除遇到本地 profile 写入失败后不得发布远端墓碑；
目录容量准入必须计入实际发布的数据引用；私钥导入不得使已有助记词账户无法备份；
两设备各自新建默认命名的钱包必须同步收敛；旧 `SwitchAccount` 入口不得绕过恢复数量限制。
另有远端删除后的重新认证和同一数据库冷重启对照，要求目录和根钱包第 0 个账户保持一致。
不只检查本机内存，也通过真实 DKVS 读取和独立设备恢复检查结果。

浏览器另外覆盖无效恢复码/用户分片的拒绝，以及纠正输入后在原设备完成恢复；
最后执行锁屏期间远端删除当前钱包、节点 API 断线后解锁的回归，要求 PWA 从本机 SDK
重建根钱包第 0 个账户的目录、地址和公钥。此场景在锁屏期间让 SDK 继续接收真实远端删除。
断线前要求删除获远端确认、接收端采用该版本且没有待同步 overlay；另外检查旧钱包仍在目录中时，
解锁也必须采用 SDK 已选中的钱包身份。
离线边界只阻断节点 API，保留 PWA 资源服务器；不将其记为生产 Service Worker 的离线冷启动验证。

RGB11 数据备份和恢复使用现有 CoreModules 真实节点门禁：

```bash
cd /Users/yingfeng/github/sat20wallet/sdk
# 默认同时运行原生 RGB11 与真实 PWA 的非空恢复和失败边界
go test ./e2e -run '^TestSDKCoreModulesE2E$' -v -count=1 -timeout=35m
```

它验证真实 DKVS/AUTOPAY 中的备份，以及新设备恢复后的多钱包/子账户目录、NIA/UDA/IFA 资产、
余额、证明、任务、发票和锁定状态；未完成的交易按既有稳定备份策略验证，恢复不得意外广播。
Bitcoin UTXO 和确认数据由隔离夹具控制，测试不向公开网络广播。
新增 `RGB11_account_recovery_failure_boundaries` 使用已发行资产的非空、多钱包/子账户 RGB11 备份，
检查缺失/损坏的数据包必须拒绝且不安装钱包；RGB11 导入的一次性故障必须阻止部分数据上传，
故障消失后必须能在同一数据库重试，恢复全部余额、证明和锁定状态且不广播交易。
只在真实 RGB11 provider 的导入边界注入一次错误，成功路径仍调用原 provider，不用合成资产数据替代。
账户 CI 执行原生账户门禁、此 RGB11 门禁和浏览器门禁；浏览器使用 CI 从源码构建的 WASM。

`account-management-usage-e2e.mjs` 复用同一入口，补充以下业务断言：

- SDK 创建已提交、UI 快照写入失败：刷新重建目录，重新认证不重复创建钱包。
- 多钱包密码事务的第二次写入失败：所有钱包保留原密码，新密码不得解锁。
- 新账户初始同步完成后发布首次恢复包；绑定由账户入口完成，不依赖其他模块。
- 已有账户离线改名、增加子账户、删除钱包后刷新；联网合并另一设备独立编辑。
- 独立地址 DID 设置入口：修改和清空经 SDK 保存；冷重载及新设备恢复一致，恢复向导只展示已保存目录。
- DID profile 写入失败：页面报错，SDK 目录和冷重载结果不变。
- 另一设备确认删除同一钱包后，本地旧改名／新增子账户／DID 编辑不复活钱包、不阻断独立变更；刷新后选择、地址、公钥保持一致。
- UI 快照读取故障必须报错，内存和持久快照保持不变，刷新后仍可解锁。
- 冷启动 SDK status 读取故障：显示真实启动错误，保留原钱包选择；撤销故障后点击页面重试，正常解锁。
- 共享 IndexedDB 标签页锁屏，使另一页待派发请求失效；锁屏页不得写入授权。
- 共享标签页切换子账户，使另一页旧身份的待返回结果失效；刷新解锁后地址、公钥采用新账户。
- 共享标签页切换钱包时，另一页在 SDK 提交前重载重建；提交后解锁必须采用新钱包，SDK 与界面身份一致。
- 共享标签页修改密码后，旧 Manager 和刷新后的页面均不得接受旧密码。
- 实际 2of2 设置页面：密码错误、答案不一致、创建备份及演练完成。
- 实际 2of3 设置页及独立 Guardian 页面：无效 contact、setup、receipt、请求、响应及演练完成。
- 恢复已提交而调用方丢失响应：刷新采用完整目录，旧 session 不得再次提交。
- 恢复页面中 SDK 已提交而 UI 快照事务失败：只显示重新载入，恢复调用仅一次，新密码和目录保持正确。
- 重新打开设置后粘贴自身 Guardian 联系信息：UI 和高层 WASM 均拒绝；不同网络的联系信息也拒绝。
- 账户设置使用 200 条报价后接受 Guardian 付费请求：确认框按请求的 100 条报价，显示固定根身份，取消无广播。
- 付费设置页面拒绝不足记录数；取消确认不得留下付费授权。
- 离开已加载并解密的恢复页：清理会话，刷新不能继续旧预览。

恢复页面场景还检查 UI IndexedDB、localStorage、sessionStorage 和 SDK 操作日志中没有测试答案、分片和密码明文。
这些断言独立记录结果；失败用例仍使整个门禁返回非零，不跳过后续独立用例。

CoreModules 门禁默认额外运行 7 项浏览器场景：通过实际恢复页面恢复已发行的多钱包/子账户
NIA/UDA/IFA 资产、证明与锁；真实 RGB11 IndexedDB 写入失败后阻止目录修改，并在同一数据库刷新后通过解锁页的恢复入口重试；
导入失败后普通解锁必须拒绝，另一独立设备发布新版后仍能完成原恢复目标再同步新版；同一根助记词通过实际导入页在刷新后续跑；
正常远端应用提交后的 import 阶段标记写入失败并冷刷新后，撤销故障、同页再次解锁仍保留本机未完成通道，认证不得重写其持久记录或广播交易。此项使用同一设备的生产编码数据库快照，先验证健康冷启动可见通道，再注入故障，避免把另一设备的本地钱包 ID 当成本机 ID；
SDK 恢复提交后的 UI 快照写入失败只提供重载，使用新密码通过实际解锁页后，非空资产、证明与锁保持一致；
已付费根钱包通过设置页面复用真实 AUTOPAY，不再次充值。资产和证明以同一备份的原生 SDK 恢复结果为参照。
独立维护账户随后通过页面真实提交更高费率的 AUTOPAY 充值：日志写入失败不广播；提交后延迟合约查询，经过生产两分钟等待超时、刷新和再次确认，保留原 TXID、待确认日志不可清空且只广播一次；恢复查询后以合约就绪状态继续设置。

临时记录到期边界另外由 `TestSDKAccountPWATemporaryExpiry` 推进隔离节点的真实区块高度验证：
远端 state 仍存在但引用的 blob 到期时拒绝覆盖；state、blob、root wrapper 和恢复包全部到期后，
完整本地设备可重新发布状态，并经正常 SDK 配置重新生成恢复材料，再由独立设备恢复。
此项不代表已配置账户的 PWA 维护入口已经完成。
BTC HTTP 查询仅提供现有发行夹具的受控链事实，账户/DKVS/RGB11 成功响应没有被替换，禁止广播。

生产 Service Worker 的完全离线冷启动、真实 mainnet/testnet 双网络成功切换与回滚、浏览器实际首次充值成功、
临时记录到期仍需最终验收，不计入这些场景的通过范围。

## Wallet Basics

The wallet basics script is a no-broadcast verification path. It imports/unlocks the built-in test wallet, sets production environment plus testnet network, reloads the PWA, checks account/subaccount reads, queries L1/L2 assets and UTXOs through wallet helpers and indexer APIs, signs/extracts a local L1 PSBT without broadcasting it, and validates DApp bridge success/error envelopes.

```bash
npm run verify:wallet-basics
```

Covered bridge cases:

- direct `getAccounts`, `getPublicKey`, `getNetwork`, `getAssetAmount`
- expired request rejection
- duplicate request rejection
- request-origin mismatch rejection
- user rejection propagation for an approval action

## Generic L1/L2 Send

The generic send script uses account 0 as the source and account 1 as the
destination. Real testnet broadcast is the default: it sends 1 sat on
SatoshiNet and 600 sats on Bitcoin testnet4, then verifies both raw
transactions.

```bash
npm run verify:generic-send
```

Use `SAT20_BATCH_SEND=1` to create two outputs on each chain. Use
`SAT20_DRY_RUN=1` for a no-broadcast preflight. Already-broadcast transactions
can be rechecked without spending again:

```bash
SAT20_EXISTING_L1_TXID=<txid> \
SAT20_EXISTING_L2_TXID=<txid> \
npm run verify:generic-send
```

Use the read-only network round trip and optional endpoint timing trace when
diagnosing network-switch latency:

```bash
SAT20_NETWORK_SWITCH_ONLY=1 \
SAT20_TRACE_NETWORK_SWITCH=1 \
npm run verify:generic-send
```

## Prediction Deploy

The prediction deploy script uses the current unified agent request format.
Dry-run estimate does not broadcast:

```bash
SAT20_DRY_RUN=1 node scripts/verify/prediction-deploy-flow.mjs
```

Every run prints and includes a fixed deploy nonce in the request and result.
Set `SAT20_DEPLOY_NONCE` and `SAT20_PREDICTION_NOW` to reuse a known positive
JavaScript-safe nonce and identical prediction content for deterministic
contract-address lookup after an uncertain submission.

The real testnet path is the default: it submits the order, locks it, builds and broadcasts the low-value L1 buy, then verifies the result. Use an explicit dry-run switch when a no-write check is required:

```bash
SAT20_DRY_RUN=1 npm run verify:l1-orderbook
```

The legacy write flags are still accepted for compatibility, but are no longer required:

```bash
SAT20_ALLOW_ORDERBOOK_WRITE=1 npm run verify:l1-orderbook
```

To explicitly disable all broadcasts:

```bash
SAT20_DISABLE_BROADCAST=1 npm run verify:l1-orderbook
```

To avoid spending another buyer UTXO on a dummy split, reuse an already broadcast dummy split:

```bash
SAT20_EXISTING_DUMMY_TXID=<txid> \
SAT20_EXISTING_DUMMY_CHANGE=<change_sats> \
npm run verify:l1-orderbook
```

The existing dummy transaction must belong to the selected buyer account. Its
change must be at least `2200 + sell amount` sats (`3200` for the default order)
so the final buy can fund the seller output, replacement dummy outputs, and buy
fee.

Current known L1 result on `2026-05-25`:

- Standard L1 order PSBT build and PWA signing succeed.
- `SubmitBatchOrders`, `LockBulkOrder`, `UnlockBulkOrder`, and signed `CancelOrder` succeed.
- With `SAT20_DRY_RUN=1` (or `SAT20_DISABLE_BROADCAST=1`), the script does not submit or broadcast and reports the skipped write path.
- Full L1 buy broadcast succeeds through the standard `BulkBuyOrder` path.
- Successful test txs:
  - dummy split: `ddff5cb1069adba95fcd51819e99335f4a34a9d2d020b4579ef6cdd90fe6a05d`
  - buy tx: `ae646dfaaad688ea9dd25b206c4492ab7532f2afdd039136e0de580c244e963f`

Root cause of the earlier `SubmitBatchOrders` panic: the SatoshiNet order builder serializes an asset-aware TxOut. The old `ordx-marketplace` L1 service parses it as a normal Bitcoin PSBT and sees an empty unsigned tx output script, then panics in `LogPsbt` while converting `PkScript` to an address.

Root cause of the earlier `BulkBuyingThirdOrder` mismatch: that endpoint only routes Magisat-sourced orders. SAT20 L1 orderbook buys should construct the final Bitcoin transaction locally and submit it with `BulkBuyOrder`.

## L2 Contract Flow

The L2 script verifies SatoshiNet contract read paths and wallet-side contract helpers against the production environment plus testnet network.

The real testnet path, including a low-value contract invoke, is the default:

```bash
npm run verify:l2-contract
```

Explicit read-only/dry-run mode:

```bash
SAT20_DRY_RUN=1 npm run verify:l2-contract
```

Run against a selected contract kind:

```bash
SAT20_L2_INVOKE_KIND=amm-swap \
SAT20_L2_CONTRACT_URL=<amm-contract-url> \
npm run verify:l2-contract

SAT20_L2_INVOKE_KIND=launchpool-mint \
SAT20_L2_CONTRACT_URL=<launchpool-contract-url> \
SAT20_L2_INVOKE_AMOUNT=1 \
npm run verify:l2-contract
```

Useful overrides:

- `SAT20_L2_CONTRACT_URL`
- `SAT20_L2_INVOKE_KIND` defaults to `swap-v2`; supported low-value test kinds: `swap-v2`, `amm-swap`, `launchpool-mint`
- `SAT20_L2_INVOKE_AMOUNT` defaults to `1`
- `SAT20_L2_INVOKE_UNIT_PRICE` defaults to `1`
- `SAT20_L2_FEE_RATE` defaults to `1`
- `SAT20_STP_API` defaults to `https://apiprd.ordx.market/stp/testnet`
- `SAT20_SATSNET_INDEXER_API` defaults to `https://apiprd.ordx.market/satsnet/testnet`

The default real invoke uses a `swap.tc` contract buy action and pays `::` on SatoshiNet, so keep the amount at `1` unless intentionally testing a larger flow.

Current known L2 result on `2026-05-25`:

- Read-only checks passed for deployed contract list, target contract status, supported contract templates, invoke fee, wallet balances, wallet UTXO selection, and direct SatoshiNet indexer UTXO queries.
- Real low-value invoke succeeded on `tb1qw86hsm7etf4jcqqg556x94s6ska9z0239ahl0tslsuvr5t5kd0nq7vh40m_runes:f:BITCOIN•TESTNET_swap.tc`.
- Invoke tx: `945905188f63bbfe260f5af602578d478218e7baa7e9cba2308f85f71c8feb37`.
- Chain-side checks found the raw tx and contract history item with `InUtxo=945905188f63bbfe260f5af602578d478218e7baa7e9cba2308f85f71c8feb37:0`; the contract moved to block `3396`, `invokeCount=45`, and `TotalDealTx=21`.
- AMM low-value invoke succeeded on `tb1qw86hsm7etf4jcqqg556x94s6ska9z0239ahl0tslsuvr5t5kd0nq7vh40m_runes:f:BITCOIN•TESTNET_amm.tc`.
- AMM tx: `59dca74bc49451615c5ac35e5590b9044942b4eb7a1bfac1e9e9cecd983c09be`; contract history later showed `InUtxo=59dca74bc49451615c5ac35e5590b9044942b4eb7a1bfac1e9e9cecd983c09be:0`, `Done=1`, `OrderType=2`, and `TotalDealTx=147`.
- Launchpool low-value mint was accepted by `tb1qw86hsm7etf4jcqqg556x94s6ska9z0239ahl0tslsuvr5t5kd0nq7vh40m_runes:f:SFSFSFFKKK_launchpool.tc`.
- Launchpool tx: `c9ffdcff6774fd0a7fe1fe76fbeeeab8e58e3fdfe22c9927a5f00882d6f6d970`; contract history showed `InUtxo=c9ffdcff6774fd0a7fe1fe76fbeeeab8e58e3fdfe22c9927a5f00882d6f6d970:0`, `OrderType=8`, `OutAmt=1`, and `TotalMinted` moved from `200` to `201`. The item stayed `Done=0` during the short poll window, so this path may need a longer settlement check.
- Read-only matrix checks passed for `swap.tc`, `amm.tc`, `launchpool.tc`, `dao.tc`, and `transcend.tc`. `recycle.tc` status reads passed, but the placeholder `refund` fee probe returned `unsupport action refund`; use the actual recycle action before testing that path.

2026-10-06 的 `sdk/wallet/lightnode/account_storage_failure_test.go` 最初在 WASM/Node 环境复现旧后端故障，现改为真实浏览器 IndexedDB 回归。
原后端的 5 项失败证据保留。经用户确认，SDK 已统一采用 IndexedDB 原生事务，不增加旧存储兼容路径。
当前门禁先验证真实 IndexedDB 的 12 项存储边界回归，再执行原有 31 项及新增 15 项 PWA 场景（共 46 项业务断言）；
执行命令见上文，不能使用原有 25 项功能场景的历史通过记录替代新存储验收。
