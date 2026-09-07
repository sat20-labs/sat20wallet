# SAT20 Wallet SDK 全量安全与正确性审查

更新时间：2026-08-23  
审查范围：`/Users/yingfeng/github/sat20wallet/sdk`  
关联模块：PWA/WASM、Transcend、SatoshiNet、Indexer、RGB11、DKVS  
文档状态：**审查完成，等待修复；本轮未修改生产代码**

---

## 1. 审查基线与约束

本次不是只审查 git diff，而是审查当前 `main` 上作为 PWA 钱包核心运行时的 SDK 全量关键路径，重点覆盖：

- Manager 生命周期、钱包创建/导入/解锁/切换；
- 私钥、助记词、密码和签名接口；
- L1/L2 网络、Indexer、STP、DKVS 客户端；
- UTXO locker、PSBT、资产和交易广播；
- 通道、watchtower、heartbeat、reservation；
- 账户管理、Shamir/guardian、managed-data；
- RGB11 状态、广播、恢复、链上 reconciliation；
- WASM 导出面和 PWA 可调用能力；
- 数据库格式、迁移、批量扫描与错误传播；
- 本地测试、真实网络测试隔离和发布构建。

审查时 `sat20wallet` 工作区只有两个生成的 WASM 文件发生变化：

```text
M client/public/wasm/sat20wallet.wasm
M pwa/public/wasm/sat20wallet.wasm
```

本次没有修改、stage、commit 或 push 任何生产代码。

严重级别：

- **P0**：存在跨网络签名、私钥/数据库暴露、资金安全或生产发布阻断风险；
- **P1**：高概率导致状态损坏、错误身份签名、恢复失败或服务不可用；
- **P2**：主要影响可维护性、诊断性、资源控制或未来安全边界。

---

## 2. 总体结论

SDK 在 DKVS、RGB11、通道恢复和账户管理方面已经形成较完整的测试与状态机基础，尤其是 DKVS 的 CAS/outbox/replica、heartbeat 身份冻结、RGB11 广播不可逆意图和账户 managed-data 原子导入，整体设计质量明显高于普通钱包 SDK。

但当前版本仍不适合作为新的生产 PWA 钱包发布基线，主要阻断项是：

1. WASM 生产导出面包含任意数据库读写和通道私密密钥接口；
2. SDK 自身的 `SwitchChain` 只改变全局链参数与地址，没有重建网络客户端和状态命名空间；
3. 解锁、账户/钱包切换和通用签名缺少统一的原子身份快照；
4. 首次导入 mnemonic 不自动启用账户管理，与已确认产品基线冲突；
5. 多处数据库扫描、远端查询和广播错误被降级为空结果或成功，存在 fail-open；
6. PSBT 在线签名仍过度依赖调用方提供的 prevout 元数据，且没有明确返回签名完整性；
7. 包级全局状态使多个 Manager、测试实例和网络上下文无法安全共存。

建议先关闭 WASM 暴露面与跨网络风险，再处理身份事务性、持久化错误传播和签名输入权威性，最后统一 API 和资源边界。

---

## 3. 风险摘要

| 编号 | 级别 | 问题 | 主要影响 |
| --- | --- | --- | --- |
| SDK-P0-01 | P0 | WASM 导出任意 DB 读写和通道私密密钥接口 | 同源脚本/XSS 可读取或篡改钱包状态，提取通道秘密 |
| SDK-P0-02 | P0 | `SwitchChain` 未重建 RPC/STP/DKVS/locker 上下文 | 跨网络查询、签名、广播和持久化污染 |
| SDK-P1-01 | P1 | 解锁非原子，失败后可能留下部分钱包已解密 | 锁定语义失效、错误状态继续运行 |
| SDK-P1-02 | P1 | 签名和后台任务缺少统一身份快照 | 并发切换时可能使用错误 wallet/account |
| SDK-P1-03 | P1 | 首次导入 mnemonic 不启用账户管理 | 导入用户没有默认恢复与 DKVS 托管能力 |
| SDK-P1-04 | P1 | DB 扫描/迁移/历史写入存在静默错误或非原子顺序 | 部分状态被当作完整状态，恢复和索引不一致 |
| SDK-P1-05 | P1 | 多个 REST/RPC 适配器 fail-open 或可 panic | 权限误放行、错误 fee、节点崩溃 |
| SDK-P1-06 | P1 | PSBT prevout 元数据和签名完成度边界不足 | 错误展示、无效签名、调用方误判已完整签名 |
| SDK-P1-07 | P1 | DKVS 写入在账户切换时存在 TOCTOU | 使用错误账户对路径签名或写入错误 scope |
| SDK-P1-08 | P1 | 公共密码语义为“PWA SHA-256 后再交给 SDK scrypt” | SDK/PWA/其他客户端口令不兼容 |
| SDK-P1-09 | P1 | 通道私密派生函数作为公共 SDK/WASM API | 上层误用或被恶意脚本提取惩罚密钥材料 |
| SDK-P2-01 | P2 | 包级 `_chain/_env/_mode/indexer.CHAIN` 等全局状态 | 多 Manager 数据竞争、测试污染、不可并发 |
| SDK-P2-02 | P2 | reservation ID 生成未原子保留 | 并发创建时潜在重复 ID |
| SDK-P2-03 | P2 | live repair/diagnostic 工具仍位于普通测试包 | 环境变量误设时可能访问真实测试网 |
| SDK-P2-04 | P2 | 部分日志输出原始 PSBT、签名和运行参数 | 浏览器控制台/日志泄露敏感业务数据 |

---

## 4. P0 发布阻断项

## SDK-P0-01：WASM 生产导出面允许任意数据库访问和通道秘密提取

涉及：

- `sdk/wasm/main.go`
- `dbTest`
- `batchDbTest`
- `getCommitRootKey`
- `getCommitSecret`
- `deriveRevocationPrivKey`
- 以及所有直接暴露到 `window`/Go WASM 全局对象的原始签名和助记词接口

### 现状

WASM 注册了可以直接读写钱包底层 KVDB 的调试函数。只要 JavaScript 与钱包处于同源执行环境，就可以绕过 Manager 的业务约束，读取或篡改：

- 加密 wallet record；
- status、当前 wallet/account；
- UTXO lock；
- channel/reservation；
- DKVS outbox/replica；
- RGB11 engine/projection；
- 账户管理 profile 和本地恢复状态。

WASM 同时公开了通道 commitment root、commit secret、revocation private key 等敏感派生能力。这些接口不是普通钱包功能，错误暴露会直接削弱 STP 的惩罚和旧状态保护模型。

PWA 当前缺少足以把同源脚本视为完全可信的隔离条件，因此任何 XSS、被替换的 service-worker/WASM、依赖注入或浏览器扩展同源脚本，都可能把上述调试接口变成完整钱包控制面。

### 修复要求

1. 从生产构建彻底删除 `dbTest`、`batchDbTest`；调试版本使用 build tag，例如 `wallet_debug_wasm`。
2. 将 commitment/revocation 私密派生 API移出公共 Manager/WASM；只允许 STP 内部窄接口使用。
3. 生产 WASM 导出采用显式 allowlist，禁止“为了调试顺手注册”的全局函数。
4. 增加构建测试：解析生成的 WASM/JS 导出清单，确保禁用接口不存在。
5. 对助记词、私钥、原始签名函数增加显式交互授权层，而不是由任意页面脚本直接调用。
6. 重新生成两个 WASM 文件，并在 release manifest 中记录源码 commit 与二进制 hash。

### 必增测试

- production build 不导出 DB 调试函数；
- production build 不导出 revocation private key；
- debug build 只能在显式 tag 下生成；
- PWA 锁定后所有私钥类接口返回 locked；
- release WASM hash 与 CI 生成物一致。

---

## SDK-P0-02：`SwitchChain` 不是完整的网络切换事务

涉及：

- `sdk/wallet/interface.go: SwitchChain`
- `sdk/wallet/manager.go`
- 包级 `_chain`、`indexer.CHAIN`
- L1/L2 Indexer client、STP peer、DKVS manager、UTXO locker、ticker/fee cache

### 现状

SDK 的 `SwitchChain` 主要执行：

- 修改当前链全局变量；
- 修改 status；
- 按新 chain params 重新派生 wallet 地址；
- 保存状态。

它没有同步重建或清理：

- `cfg.Chain` 与 Indexer proxy；
- L1/L2 RPC client；
- STP/bootstrap/core peer client；
- UTXO locker 的网络 scope；
- DKVS client、endpoint affinity、confirmed replica、verification height；
- ticker、fee、contract、RGB11 等链相关 cache；
- 后台 heartbeat、watchtower、action monitor 和 DKVS worker。

因此直接 SDK 调用者可能出现：

```text
地址已经切到 mainnet
但 RPC、DKVS 或 STP 仍指向 testnet
```

这会导致跨网络查询、错误 UTXO 选择、把签名交易广播到错误网络、用 testnet 高度验证 mainnet DKVS record，或把状态写入错误数据库前缀。

PWA 当前通过 `release -> 使用目标配置重新 init Manager` 规避了部分风险，但 SDK 和 Transcend 仍公开调用该方法，因此不能把 PWA 的规避当成 SDK 已安全。

### 修复方案

优先采用“不可原地切链”：

1. 将 SDK `SwitchChain` 标记 deprecated，并使其返回明确错误；
2. 统一要求调用方：
   ```text
   Stop/Lock old Manager
   -> 等待全部 worker 退出
   -> Close old Manager
   -> 用目标网络完整 Config 新建 Manager
   -> Unlock
   ```
3. 如果必须保留原地切链，需实现完整事务：停止所有 worker、冻结写入、重建所有客户端/locker/DKVS、清空 cache、切换 DB scope、重新加载状态，任一步失败则回滚。
4. 为 Manager 增加不可变 `NetworkContext`，业务代码不得读取包级 `_chain`。

### 必增测试

- testnet -> mainnet 后所有 L1/L2 URL、peer、DB prefix、DKVS endpoint、height 和地址一致；
- 切链过程中任何签名/广播请求被拒绝；
- 失败回滚后旧网络仍完整可用；
- 两个不同网络 Manager 同进程运行互不污染；
- Transcend 不再调用原地 `SwitchChain`。

---

## 5. P1 高优先级问题

## SDK-P1-01：钱包解锁不是全有或全无

涉及：

- `sdk/wallet/wallet_crypto.go`
- `unlockWallet`
- `UnlockWallet`

### 问题

解锁过程逐条解密 wallet 并直接写回 `walletInfoMap`。若后续某条 wallet 解密失败，前面已经成功的 wallet 仍留在内存中，函数返回 error 但 Manager 处于“部分解锁”状态。

同时，损坏的 mnemonic/private-key record 有些路径只记录日志并跳过；若当前选择项没有成功生成运行时 wallet，后续访问可能遇到 nil 或与 status 不一致的 wallet。

### 风险

- UI 认为钱包仍锁定，但部分私钥已经在 WASM/Go heap 中；
- 重试或后台 worker 可能继续使用部分解锁 wallet；
- 损坏一个钱包项可能使当前身份落到另一钱包或 nil；
- 错误恢复路径难以判断哪些秘密需要清零。

### 修复

- 在临时 map 中完成所有 wallet 的解密、格式校验、fingerprint 去重和当前身份解析；
- 全部成功后一次性替换运行时 catalog；
- 失败时清零临时 secret，并保证 live manager 完全不变；
- 增加显式 `Lock()`：停止私钥相关 worker、清空 runtime wallet、account secret、RGB11 scoped wallet 和 signer cache；
- 当前 wallet 缺失时 fail closed，不得自动选择其他 wallet。

---

## SDK-P1-02：签名与后台任务没有统一冻结 wallet/account 身份

涉及：

- `SwitchWallet`、`SwitchAccount`
- PSBT/交易/消息签名入口
- DKVS record 构造
- RGB11、watchtower、heartbeat、action monitor

通道 heartbeat 已有较好的 `identity + generation` 校验模式，但该模式没有统一应用到所有签名和写入入口。部分函数先读取 `manager.wallet`，随后执行远端查询或复杂构造，期间用户可以切换 wallet/account，最终动作可能使用旧/新身份混合状态。

### 修复


1. 开始时获取 common.Wallet
2. 传递该对象到需要的地方

或者：暂时不修改

---

## SDK-P1-03：首次导入 mnemonic 不自动激活账户管理

涉及：

- `sdk/wallet/account_management_auto_test.go`
- `TestImportFirstMnemonicWalletDoesNotInitializeAccountManagement`
- wallet import flow

测试当前明确把“首次 import 不初始化账户管理”作为预期，而已确认产品基线是：

> 创建或导入第一个 mnemonic wallet 时，账户管理立即存在；是否配置正式恢复材料是另一状态。

### 风险

导入用户会在没有明确提示的情况下缺少：

- root account identity；
- account secret；
- wallet/subaccount catalog；
- FREE_LOCAL managed-data 初始同步；
- RGB11 恢复 provider 的默认保护。

### 修复

- 创建和导入第一个 mnemonic 统一调用同一原子激活函数；
- 激活失败时 wallet import 不得只完成一半；
- `RecoveryConfigured=false` 仅表示未完成 Shamir/guardian 设置，不表示账户管理未启用；
- 更新现有反向测试。

---

## SDK-P1-04：数据库扫描错误和多记录更新缺少严格传播/原子性

涉及：

- `sdk/wallet/db.go`
- `DeleteAllKeysWithPrefix`
- reservation、contract history、reverse index、wallet/status migration

已确认模式包括：

- `BatchRead` 失败后返回空/部分结果；
- 部分 helper忽略 scan error；
- 某些历史 item 和反向索引分两次写入；
- backup/move 路径先删除旧记录，再写新记录；


### 风险

- 数据库 I/O 故障被解释为“没有数据”；
- 部分 reservation/channel/RGB 状态丢失后节点仍继续运行；
- 崩溃窗口产生单边索引；
- 迁移失败后旧数据已被删除，无法自动重试。

### 修复

- 所有 scan helper必须返回并传播 error；
- 多 key 业务更新使用同一 write batch；
- 迁移遵循 `read+validate -> batch write new -> version advance -> delete old`；
- 不允许存储错误触发 `panic` 或被当作空集合。

---

## SDK-P1-05：REST/RPC 适配层存在 fail-open 和边界 panic

涉及：

- `sdk/wallet/restclient.go`
- `AllowDeployTick`
- `GetFeeRate`
- `GetRawTxContext`
- `GetUtxoId`
- 广播错误分类

典型问题：

- 权限查询 transport/unmarshal 失败时返回允许；
- 假设返回 list 至少有一项；
- JSON `interface{}` 直接类型断言；
- nil output 继续解引用；
- 广播幂等性依赖人类可读错误字符串。

### 修复原则

- 权限、ticker、资产、fee、prevout 等安全输入全部 fail closed；
- API response 使用严格结构和范围校验；
- fee 不可用时禁止构造资金交易，而不是默认零值；
- 广播结果使用 typed error/code 和后续链上可见性确认；
- 所有 slice/index/type assertion 前显式检查。

---

## SDK-P1-06：PSBT 签名过度信任调用方 prevout，且没有明确签名完成状态

涉及：

- `sdk/wallet/wallet.go`
- `SignPsbt`
- `SignPsbt_SatsNet`
- prevout fetcher 构造和签名数据清理

### 问题

- `NonWitnessUtxo` 使用前需验证其 txid确实等于 input outpoint hash，并检查 vout；
- `WitnessUtxo` 的 value、script、assets 由 PSBT 提供，在线钱包不应无条件信任；
- 部分不支持或不属于当前 wallet 的 input 可能被跳过，但调用方不易区分“部分签名”和“完成签名”；
- SatoshiNet 资产 prevout 直接影响 sighash 和资产展示。

### 审核结果
不修复，没必要这里还要查询indexer

---

## SDK-P1-07：DKVS 写入与账户切换存在 TOCTOU

DKVS manager本身具备较好的 CAS、path lock、outbox 和 replica 设计，但上层 mutation 构造仍有路径直接读取当前 `owner.wallet`。如果远端 sync/fee/height 查询期间账户发生切换，可能用新的 signer签旧路径，或用旧 signer写入新 scope。

### 修复

- mutation必须携带 common.Wallet

---

## SDK-P1-08：PWA 与 SDK 的密码语义不一致

SDK 底层 wallet secret使用带独立 salt 的 scrypt/snacl 加密，这一点是合理的。问题在于 PWA 先把用户口令做 SHA-256 十六进制转换，再把该字符串作为 SDK password。

结果是：

- 同一用户输入通过 PWA 和直接 SDK 得到不同加密口令；
- CLI、PWA、移动端、WASM 的导入/解锁语义容易不兼容；
- 用户修改密码或迁移客户端时难以解释真实口令格式。

### 修复

在协议层只定义一个 password API：调用方传入原始 UTF-8 密码，SDK 内部执行唯一 KDF。若必须保留旧 PWA prehash 数据，需显式 versioned credential scheme并提供一次性迁移，不能静默猜测。

---

## SDK-P1-09：通道私密派生函数不应是公共钱包 API

`GetCommitRootKey`、`GetCommitSecret`、`DeriveRevocationPrivKey` 等接口应属于 STP 内部能力。公开到 SDK 和 WASM 后，上层任何业务模块都可获得本不需要暴露的通道密钥材料。

### 修复

- 移到 internal package或 capability对象；
- 只接受 channel/reservation绑定的调用上下文；
- 返回签名/交易结果，不返回原始 private key；
- 对所有调用记录审计，但日志不得包含 secret；
- 检查 PWA、Transcend 是否仍依赖原始 key返回，逐步替换。

---

## 6. P2 与工程治理问题

### SDK-P2-01：包级全局网络状态

`_mode`、`_env`、`_chain`、`indexer.CHAIN`、部分 fee/cache 使 Manager 不是独立对象。测试中经常需要保存并恢复全局值，已经证明该边界容易污染。

应把网络、模式、env、chain params、DB prefix生成器都收进不可变 Manager config；包级 getter只作为兼容 facade，最终删除。

### SDK-P2-02：reservation ID 未在生成时原子占用

`GenerateNewResvId` 只保证生成函数串行，ID 到真正插入 map/DB 之间仍存在窗口。应在同一 Manager 锁/DB batch中完成 `allocate + reserve + persist`，或使用持久化单调序列。

审核结果：不修改，没必要单调，不同即可。

### SDK-P2-03：真实测试网修复工具位于普通测试包

`TestManageTestnetAccountSeq2Repair` 已有严格 snapshot、target、hash 和 APPLY token保护，默认 dry-run并 skip；这比普通 live test安全。但它仍会在环境变量被设置时访问真实测试网，且代码体量很大。

建议迁移到：

- 独立 maintenance command；或
- `//go:build wallet_maintenance`；

普通 `go test ./...` 不应编译或包含生产账户特定常量和修复工作流。

### SDK-P2-04：敏感日志

删除原始 PSBT、signed PSBT、签名、助记词、完整 source package和高敏 reservation参数的日志。统一使用 txid、短 hash、wallet fingerprint尾部和结构化状态码。

---

## 7. 已确认的正向实现

以下设计应保留并作为其他模块修复的参考：

1. **DKVS Manager**：per-path lock、CAS/batch-CAS、durable outbox、endpoint affinity、confirmed replica、current-session readiness和 stop/cancel边界较完整。
2. **DKVS fail-closed**：watch变化后撤销 ready、同步成功后恢复，能够避免旧缓存直接写入。
3. **Heartbeat identity freeze**：已有测试覆盖账户切换、generation变化和 in-flight cancel，建议抽成全 SDK 通用模式。
4. **RGB11 广播不可逆意图**：广播不确定、持久化失败和链上 evidence恢复已有较系统测试。
5. **RGB11 recovery provider**：将不可重建 proof/未完成状态纳入 managed-data，而不备份可重建 projection，边界合理。
6. **账户 managed-data 导入**：先校验全部 provider，再事务性 apply/rollback，避免部分导入。
7. **密码修改**：已有重新加密钱包和 account secret、失败回滚测试。
8. **通道安全**：watchtower、sweep、funding/closing reservation rehydrate、身份变化取消已有较多防御测试。
9. **live test隔离**：主要真实网络测试使用 build tag或显式环境开关；普通 all profile清除 `SAT20WALLET_RUN_LIVE_NETWORK_TESTS`。

---

## 8. 与 SatoshiNet 整改计划的联动

参考：

```text
/Users/yingfeng/github/satoshinet/docs/satoshinet-review-remediation-plan.md
```

SDK 修复必须与以下协议变化同步：

### PoS V2/no-reorg

- SDK 不应再把 L2 常规 reorg作为长期生产常态；
- 仍需支持 PoS V2 激活前测试网回滚和历史同步；
- 合约激活后 combined state root与 finality certificate查询要进入交易确认状态；
- 测试网设置 PoS V2 高度前，先用旧版本统一回滚。

### 资产严格规则

- AssetName严格三段；
- amount > 0；
- BindingSat一致；
- 资产列表 canonical排序与数量/长度上限；
- SDK/wallet/PWA构造必须先符合未来共识，但在修改历史解码前先完成主网扫描。

### EVM source metadata

SDK/PWA不再直接把 source metadata写入 contract indexer。提交路径改为：

```text
/blob/evm/source/<contract_address>
```

节点端必须执行 exact init-code比较和准确上下文 runtime replay。SDK只负责提交 versioned source package和展示节点验证结果，不能自行声明 verified。

### DKVS snapshot authority

SDK mirror/path sync必须接受服务端授权 authority规则；不能把任意响应 peer视为可信完整快照来源。

---

## 9. 建议实施顺序

### 阶段 1：立即关闭暴露面

1. 删除生产 WASM DB调试导出；
2. 删除/内部化 revocation/commit private-key导出；
3. 增加真实 `Lock()` 并让 PWA auto-lock调用；
4. 禁用 SDK 原地 `SwitchChain`；
5. 删除敏感日志。

### 阶段 2：身份与持久化事务



### 阶段 3：签名与网络输入

1. 权威 prevout resolver；
2. PSBT 完整性结果；
3. REST/RPC strict decoding和 fail-closed；
4. typed broadcast error；
5. DKVS signer snapshot；
6. 密码 API version化与迁移。

### 阶段 4：协议联动

1. AssetName/BindingSat新规则；
2. PoS V2确认语义；
3. EVM source system Blob；
4. DKVS authority sync；
5. regenerate WASM并与 PWA集成测试。

---

## 10. 测试要求

### 当前验证状态

- `sat20wallet-sdk-test all` 已通过 supervisor启动，显式清除了 live-network开关；文档生成时仍在执行耗时较长的 `wallet` 包。
- 已完成并通过的子包：
  - `sdk/account`
  - `sdk/account/fuzzy`
  - `sdk/account/internal/shamir`
- `sat20wallet-sdk-build` 的 MCP请求超时，未取得可判定终态；不能据此认定 build失败。
- 最终结果应读取：
  ```text
  /Users/yingfeng/mcp/local_access/logs/test-runs/sat20wallet-sdk-test/latest.status
  ```

### 必增测试矩阵

- production WASM export allowlist；
- 真正锁定后私钥不可调用；
- unlock 任意中间失败不改变 live state；
- 两 Manager、两网络并发隔离；
- 禁止原地 SwitchChain；
- wallet/account切换与签名并发；
- first-import账户管理激活；
- DB scan故障、batch原子性和迁移崩溃恢复；
- REST malformed/empty/permission failure；
- PSBT forged WitnessUtxo、错误 NonWitness txid、重复 outpoint、部分签名；
- DKVS账户切换 TOCTOU；
- PWA旧密码 scheme迁移；
- SDK + PWA + Transcend串行 E2E。

---

## 11. 发布退出条件

在以下条件全部满足前，不建议发布新的生产 PWA钱包：

1. 生产 WASM不包含任意 DB接口和通道 private-key接口；
2. PWA锁定会清除 SDK运行时秘密并停止私钥 worker；
3. SDK不允许不完整原地切链；
4. 解锁与 wallet/account切换具有事务性；
5. first import自动建立账户管理；
6. DB scan和远端安全输入全部 fail closed；
7. PSBT必须完成权威 prevout校验并明确报告签名完成度；
8. 两网络/两 Manager并发测试通过；
9. 全量 unit、SDK E2E、PWA typecheck/bundle和 Transcend联动测试通过；
10. 生成 WASM与源码 commit/hash进入 release manifest。

---

## 12. 下一次 session 的执行入口

1. 读取本文档及 SatoshiNet整改计划；
2. 检查 `sat20wallet` git status，不覆盖当前生成 WASM changes；
3. 先确认 SDK全量测试最终状态；
4. 只在 changes/unstaged中修改；
5. 先落地 SDK-P0-01、SDK-P0-02和真实 Lock；
6. 每项修复增加针对性测试；
7. 不运行真实网络测试，除非用户明确要求；
8. 不 stage、不 commit、不 push，除非用户明确授权。
