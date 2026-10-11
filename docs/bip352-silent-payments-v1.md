# SAT20 Wallet · BIP352 Silent Payments V1

日期：2026-10-11
状态：实现设计，作为 `feat/bip352-per-account-sp-wallet-20261011` PR 的验收依据。

## 目标及范围

让 PWA 中每个 **已有的助记词子账户** 独立启用 BIP352 v0 静默支付（SP），与普通 BTC 共享账户资金视图及交易历史。SP 不创建新的 Wallet / Account，不适用于 L2、RGB11 或 STP 通道。

- 每个 `(wallet fingerprint, account index, L1 network)` 独立派生 Scan / Spend Key。
- **不共享 Root Scan Key**；即使属于同一个 Root Account，不同子账户的公开 SP 地址也不能因为重复的 Scan Key 而直接关联。
- 账户管理的 `SPEnabled` 是权威启用状态，跟随账户目录的本地原子修改、加密托管同步和新设备恢复；不存可派生的私钥或公开 SP 地址。
- SP 一经公开/启用，不提供会导致停止扫描的「关闭并遗忘」操作；界面可以隐藏收款入口，但恢复始终能够发现历史收款。
- SDK 同一个任务下载 Indexer 的公共数据、调度多个已启用的子账户；**每个账户各有独立检查点和匹配结果**。
- Indexer 仅保存公共候选记录，不获得 scan/spend private keys、用户身份、账户 ID、Label 或收款结果。

## 标准密钥及地址

严格遵循 BIP352 约定（BIP32 必须 hardened 的层级以 `'` 标记）：

```text
scan  m/352'/coin_type'/account'/1'/0
spend m/352'/coin_type'/account'/0'/0
```

`coin_type` 主网 = 0，测试网络 = 1；子账户索引为 `account`。每个独立助记词子钱包的主密钥独立，因此即使 `account=0` 也不复用密钥。根助记词、BIP39 passphrase 及网络必须一致；不使用当前选择状态或设备随机数作为派生因子。

公开地址 `sp1q...` / `tsp1q...`（BIP352 v0 Bech32m，66-byte compressed scan+spend pubkeys）。SP 发送端/接收端必须通过规范的校验向量。

## 启用和恢复

1. 用户在选定子账户的 BTC 收款页面点「启用隐私收款」。SDK 校验子钱包类型、解锁状态、账户索引及网络；派生公钥和 SP 地址。
2. **只在账户管理本地原子状态落盘后**报告启用成功。开启后该子账户参与统一扫描；一旦启用，不因隐藏入口、切换账户或退出 PWA 而撤销已用地址的历史识别义务。
3. 托管账户将 `SPEnabled` 纳入现有加密账户目录与远端合并规则；新设备恢复同一子钱包助记词与账户索引后可重新派生两把钥匙。不能仅把前端 `localStorage` flag 当成事实来源。
4. 未开启账户不应尝试匹配 SP，也不应错误继承其他账户的扫描检查点。
5. 恢复可用余额优先用 `/v1/sp/live-snapshot` + `mode=live`；独立后台 Archive 全历史扫描补齐交易历史。增量扫描从每账户**已完整提交**的高度继续；所有失败、部分分页或 30 秒超时均不提升检查点。
6. 每个匹配输出记录完整的 `txid:vout`、`k`、签名所需 tweak/派生路径、确认高度和输出类型，采用已有 SDK 本地安全存储；扫描私钥绝不出现在 HTTP 请求中。
7. 在 PWA/WebAssembly 任务中遵守浏览器生命周期：关闭/锁屏可暂停，恢复时从持久检查点继续。页面只消费 SDK 的查询进度和账户数据。**扫描未完成时不得显示确定的零 SP 余额。**

## 数据与余额

Indexer Base/SP 提供：

- `GET /v1/sp/status`
- `GET /v1/sp/live-snapshot`
- `GET /v1/sp/scan-data?mode=live|archive`（默认 CBOR，可选 JSON）
- `POST /v1/sp/txids-by-position`
- `POST /v1/sp/outpoints/query`（固定快照 UTXO 状态）
- 已有 `POST /v3/bitcoin/rawtx/batch` 获取真实输出金额、验证交易身份。

SDK 必须验证 network/schema/coverage/start 与固定 Base anchor；校验 SP 候选 `T`、输出 key、vout 和位图，不能从服务器直接接受“属于我的钱包”结论。只在扫描本地匹配后，通过真实链上输出确认金额，不从公共 SP 索引器猜测余额。资金和交易历史最终使用已有 BTC 账户消费读模型（包括普通账户 UTXO 与 SP UTXO）。

BIP352 同一 Scan Key 的标签共享发送方的输出序号 `k`；V1 暂不向普通用户暴露 Labels/收款单，保留 BIP352 预留的 Label 0 语义及将来恢复能力。

## 发送与签名

SP 只接受原生 L1 BTC。发送方在**最终选币之后**才计算地址输出：

- 校验接收方 SP 地址的版本、HRP、两个公钥。
- 筛选可安全花费、签名私钥明确可访问的 BIP352 合格输入；排除携带其他资产、未知输入类型及不支持的 Taproot script path。
- `a` 是每个合格输入私钥之和（P2TR 使用对应最终输出 key 的偶 Y 规范私钥），`A` 为公钥和；`input_hash` 绑定最小 outpoint 与 `A`。
- 逐 Scan Key 分组、按 BIP352 排序并从 `k=0` 派生 P2TR 一次性公钥；发送前核对 TxOut 与签名输入没有再被选币修改。
- RBF 或手续费调整若改变任意输入，必须重新计算 SP 输出；绝不让普通 `btcutil.DecodeAddress` 处理 `sp1...` 作为 Bitcoin 普通地址。
- 真正签名花费 SP 收款 UTXO 必须使用对应的派生 key 加 tweak（含 Taproot 奇偶性），不能错误复用原普通账户 BIP86 key。

## PWA

BTC 收款页展示两个可选收款方式：普通地址、隐私收款地址；后者仅对已启用账户显示。发送页识别 `sp1...`/`tsp1...` 并转入 SDK SP 交易构造，不误导用户普通地址软件一定支持。两类收款共享现有 BTC 余额/流水；SP 同步提供“可用余额恢复 / 历史补齐”独立状态，不把尚未扫描状态显示为余额 0。

## E2E 验收（默认运行，不加 build tag）

以 SDK 现有虚拟链/节点夹具（真实交易、脚本签名、模拟 L1 数据）为入口：

1. 主网与测试网路径/地址 golden vector；不同助记词、不同账户的 Scan/Spend Key 不得相同；助记词恢复、改密后不变。
2. SP 收款启用状态本地重启、账户切换、DKVS 托管恢复、远端合并保持正确，私钥/观察钱包拒绝启用；旧账户不会因 UI 隐藏而停止扫描。
3. 构造已签名 BTC->SP 付款，通过 Indexer 公共 T 恢复真正的 `txid:vout`、金额和 SP 私钥；用真实 Bitcoin 脚本引擎证明收款私钥可以花费。
4. 同一交易多个 SP 输出、普通与 SP 混合输出、先花 k=0 再恢复 k=1、已全部花费、不同账户独立匹配、错误 Scan Key 不误匹配。
5. Live 优先恢复、Archive 历史补齐、分页游标/快照失效、失败不推进账户检查点；关闭重启和切换账户恢复。
6. 发送端在最终选币、fee/RBF 更新后的交易输出始终匹配接收端；不签出携带其他资产的 UTXO。
7. PWA 的 WASM 出口、账户启用入口和收款/发送界面，通过已有 SDK 端到端虚拟网络测试环境验收。

明确不承诺：V1 的 mempool 未确认收款、Labels 收款单、独立后台守护进程、原始 P2PK/裸多签 prevout 的完整支持（Indexer 文档已注释该边界）、跨主网实时重建实测、真实设备永久后台运行。

## 上线门槛

无构建特殊 tag：`go test ./...`、Go WASM 编译、PWA 类型检查、默认 E2E 应通过；后续单独验收真实 bitcoind regtest、Indexer CheckSelf、PWA 浏览器恢复/关页续扫、真实主网性能。Indexer 若相关接口尚未发布，PWA 必须表现为“不支持/未准备好”，不能将扫描结果默认为成功。
