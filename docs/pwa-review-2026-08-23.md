# SAT20 PWA Wallet 全量安全与正确性审查

更新时间：2026-08-23  
审查范围：`/Users/yingfeng/github/sat20wallet/pwa`  
核心依赖：`sat20wallet/sdk/wasm`、浏览器 IndexedDB/localStorage、Service Worker、DApp bridge  
文档状态：**审查完成，等待修复；本轮未修改生产代码**

---

## 1. 审查范围

本次重点检查 PWA 作为最终用户自托管钱包的完整攻击面：

- WASM 初始化、释放、升级和错误传播；
- 钱包创建、导入、解锁、自动锁定、切换网络/钱包/账户；
- 密码、助记词、剪贴板与浏览器存储；
- DApp iframe、origin allowlist、request/approval bridge；
- PSBT、消息签名、UTXO lock/unlock；
- 交易、资产、合约和 EVM Solidity 工具；
- Pinia 状态、router guard、页面生命周期；
- Service Worker、离线缓存、CSP、source map；
- operation log与敏感日志；
- TypeScript typecheck、Vite bundle和生成 WASM一致性。

本次没有修改 PWA 源码。工作区中现有的 `pwa/public/wasm/sat20wallet.wasm` 变化是审查前已存在的生成物变化。

---

## 2. 总体结论

PWA 已具备较完整的页面结构、账户管理入口、DApp request nonce/origin检查、风险提示和 operation log包装。网络切换采取“释放旧 Manager，再用目标配置重新初始化”的做法，也比 SDK 当前原地 `SwitchChain` 安全。

但浏览器端安全边界仍存在数个必须在生产发布前解决的问题：

1. “锁定钱包”仅清空前端 password/UI 状态，WASM Manager中的私钥仍然解锁；
2. 生产 WASM 暴露任意 DB读写、助记词、原始签名和通道 private-key能力；
3. PSBT 解析或 prevout查询失败后仍可点击确认，属于签名 fail-open；
4. 已授权 DApp可无逐次确认执行 UTXO lock/unlock，可能解除通道/RGB/reservation保护；
5. 内置 DApp Market会自动永久授权 allowlist origin并主动发送地址/公钥，绕过 `requestAccounts`；
6. origin授权没有撤销、过期和方法级 capability，production allowlist仍含 localhost；
7. 缺少严格 CSP/WASM完整性，Service Worker长期 cache-first缓存高权限代码；
8. PWA内置 solc版本与节点规范编译器不一致，当前源码验证模型无法确保链上 bytecode一致；
9. JS `number` 仍用于可能超过 2^53 的 UTXO ID、satoshi、gas和资产数值；
10. 密码 prehash、助记词清理和控制台日志仍需统一治理。

当前版本应视为开发/测试钱包，不应在完成 P0 项前作为高价值生产自托管钱包发布。

---

## 3. 风险摘要

| 编号 | 级别 | 问题 | 主要影响 |
| --- | --- | --- | --- |
| PWA-P0-01 | P0 | 前端锁定不锁 SDK/WASM 私钥 | 自动锁定后同源脚本仍可签名、读取秘密 |
| PWA-P0-02 | P0 | 生产 WASM 含 DB/debug/private-key导出 | XSS或被替换资源可完全接管钱包 |
| PWA-P0-03 | P0 | PSBT解析/查询失败后仍允许签名 | 用户无法确认真实输入、资产、fee和输出 |
| PWA-P0-04 | P0 | DApp可无审批 lock/unlock UTXO | 可解除协议锁并诱导后续错误花费 |
| PWA-P0-05 | P0 | 内置 Market自动授权并主动披露账户 | 绕过账户授权流程，形成永久高权限 DApp |
| PWA-P1-01 | P1 | Origin权限无撤销/过期/方法 capability | 权限长期扩张，单一授权等价全功能授权 |
| PWA-P1-02 | P1 | CSP、WASM完整性和 Service Worker更新边界不足 | 高权限代码被注入/缓存后可长期驻留 |
| PWA-P1-03 | P1 | Wallet/network/account切换回滚不完整 | UI、SDK和持久化状态可能分叉 |
| PWA-P1-04 | P1 | 账户导入清理 mnemonic而不是严格拒绝 | 用户输入被静默改变，恢复身份可能错误 |
| PWA-P1-05 | P1 | Raw message/signData缺少协议域隔离 | DApp可诱导产生可跨协议复用的签名 |
| PWA-P1-06 | P1 | Solidity compiler与节点规范不一致 | 显示“已验证”但实际 bytecode不匹配 |
| PWA-P1-07 | P1 | 关键金额使用 JS `number` | 大值精度损失，错误 fee/资产/UTXO展示 |
| PWA-P1-08 | P1 | 密码在 Pinia中长期驻留且预先 SHA-256 | 锁定语义与其他 SDK客户端口令不一致 |
| PWA-P2-01 | P2 | Approval store单槽位，request集合无界 | 并发请求覆盖、内存增长和错误审批 |
| PWA-P2-02 | P2 | 敏感 PSBT/签名/编译结果写控制台 | 浏览器日志、远程调试和扩展可读取 |
| PWA-P2-03 | P2 | PWA typecheck/bundle缺少可追踪终态 | 发布无法证明前端构建通过 |

---

## 4. P0 发布阻断项

## PWA-P0-01：当前“锁定”只是 UI 锁，不是密钥锁

涉及：

- `pwa/store/wallet.ts`
- `lockWallet`
- 自动锁定计时器
- `pwa/entrypoints/popup/App.vue`
- SDK/WASM Manager生命周期

### 现状

`lockWallet()` 主要执行：

- 清空 Pinia 中的 password；
- 把 `locked` 等 UI 状态设为 true；
- 跳转到解锁页面。

它没有调用 SDK/WASM 的真实锁定或 release，也没有：

- 清空解密后的 wallet/private key；
- 清空 account secret；
- 停止 RGB11、heartbeat、watchtower、DKVS等可使用 signer的 worker；
- 清空 WASM全局 Manager；
- 使签名 API在 SDK层返回 locked。

因此自动锁定后，只要同源 JavaScript仍能访问 WASM全局接口，就可能继续调用签名、助记词或通道秘密接口。

### 修复要求

1. SDK增加真实 `Lock()`，由 PWA锁定动作调用；
2. Lock必须停止并等待所有私钥相关 worker退出；
3. 清空 runtime wallet、account secret、scoped RGB11 wallet、signer cache和临时签名数据；
4. WASM所有敏感导出先检查 Manager locked state；
5. 页面隐藏、系统休眠、超时、logout和 `pagehide` 使用同一锁定事务；
6. Lock失败时 UI不得宣称安全锁定，应进入错误状态并阻止继续使用；
7. 解锁必须重新从加密存储构造完整 runtime，而不是恢复旧引用。

### 必增测试

- auto-lock 后调用所有签名接口均失败；
- auto-lock 后 `getMnemonic`、commit/revocation secret均失败；
- in-flight签名/网络请求在锁定时取消；
- 刷新、后台恢复和 Service Worker消息不能绕过 lock；
- 锁定不损坏持久化数据。

---

## PWA-P0-02：WASM高权限导出与浏览器同源攻击面组合成完整接管

涉及：

- `pwa/main.ts`
- `pwa/utils/wasm.ts`
- `sdk/wasm/main.go`
- `window.sat20`/Go WASM globals
- `dbTest`、`batchDbTest`、助记词、raw sign、revocation key导出

PWA把 WASM能力注册为页面全局 API。生产 WASM又包含任意数据库访问和私密密钥派生函数。浏览器安全模型中，同源脚本一旦执行，就能调用这些 API；PWA没有额外进程、iframe origin或 capability隔离。

这意味着以下任一事件都可能导致完整钱包接管：

- XSS；
- 被污染的 npm依赖；
- 被替换的 Service Worker或 WASM；
- 错误允许的 iframe同源导航；
- 浏览器扩展/调试脚本访问页面上下文。

### 修复

- 生产 WASM只导出经过审核的最小 API；
- 删除 DB调试和 private-key返回接口；
- 不将 Manager对象或通用调用器挂在 `window`；
- 使用窄 message facade，按方法验证 lock、origin、capability和用户审批；
- CI对 WASM导出表做 denylist/allowlist检查；
- PWA必须有强 CSP和构建完整性策略。

该项与 SDK-P0-01必须一起完成，单独修改 PWA不足以消除风险。

---

## PWA-P0-03：PSBT审批在无法完整解析时 fail-open

涉及：

- `pwa/components/approve/SignPsbt.vue`
- `LayoutApprove.vue`
- PSBT解析、prevout查询和确认按钮状态

### 现状

当出现以下情况时，页面会显示不完整信息或错误提示，但确认按钮仍可能可用：

- PSBT解析失败；
- input/output信息为空；
- L1/L2 prevout查询失败；
- 资产信息无法解析；
- fee无法可靠计算；
- 重复 outpoint被 UI去重；
- signed PSBT未能确认全部预期 input。

这违反钱包审批的基本原则：

> 无法完整、权威解释交易时，不得签名。

重复 input/outpoint不应为了展示便利而去重，因为它本身是异常交易信号。原始 PSBT、解析结果和已签名 PSBT还被写入控制台。

### 修复要求

1. 使用 `confirmDisabled` 将解析状态绑定为强制门槛；
2. 任一 input/output/prevout/asset/fee未知时禁止确认；
3. 从 SDK获取权威 prevout和结构化签名计划，不在 Vue中拼装安全结论；
4. 拒绝重复 outpoint、重复 input、越界 vout和 network mismatch；
5. 明确显示：
   - 每个 input的来源、地址、资产、金额；
   - 每个 output的地址、资产、金额；
   - change识别依据；
   - BTC fee、资产 fee、gas；
   - 哪些 input会签、哪些不会签；
6. 用户批准的摘要生成 digest，并要求 SDK签名前重新验证同一摘要；
7. 默认禁止部分签名，除非页面明确是 multisig协作模式；
8. 删除原始 PSBT/签名日志。

### 必增测试

- malformed PSBT；
- 空 input/output；
- prevout查询失败；
- forged WitnessUtxo；
- NonWitness txid不匹配；
- 重复 outpoint；
- L1/L2 network mismatch；
- 资产/BindingSat无法解析；
- 超过 JS safe integer；
- 部分签名结果。

---

## PWA-P0-04：DApp无需逐次审批即可操作 UTXO锁

涉及：

- `pwa/composables/usePwaDappBridge.ts`
- DApp method路由
- L1/L2 `LOCK_UTXO`、`UNLOCK_UTXO`
- SDK UTXO locker/reservation

### 风险

一个已经获得连接权限的 DApp可以直接锁定或解锁 UTXO，而不经过专门审批页面。钱包底层的 UTXO锁不仅服务普通 DApp，也可能保护：

- STP channel funding/commitment；
- pending reservation；
- RGB11 carrier、change或待确认转移；
- contract invoke/result资金；
- watchtower/sweep等待状态。

若 DApp能解除不属于自己的锁，后续普通转账或 DApp交易可能把协议关键 UTXO花掉。

### 修复要求

1. locker记录必须包含：
   ```text
   owner_type
   owner_id
   purpose
   network
   wallet/account generation
   created_at
   ```
2. DApp只能释放自己创建、且标记为 DApp-owned的锁；
3. channel/RGB/reservation/system锁永远不能由 DApp API释放；
4. lock/unlock属于资金控制动作，至少需要方法级 capability，关键 unlock需逐次审批；
5. 页面显示 UTXO、资产、锁拥有者和后果；
6. origin撤销时清理其可清理的临时锁，但不能触碰协议锁。

---

## PWA-P0-05：内置 DApp Market绕过账户授权

涉及：

- `pwa/entrypoints/popup/pages/wallet/DappMarket.vue`
- `pwa/lib/authorized-origins.ts`
- iframe初始化与 `postMessage`

### 现状

Market加载 allowlist中的 iframe来源时会：

- 自动加入永久 authorized origins；
- 不展示 `requestAccounts`审批；
- 主动把当前 address和 pubkey发送给 DApp；
- iframe未配置严格 sandbox；
- 某些 URL解析失败路径可能使用 `targetOrigin="*"`；
- 默认 allowlist包含 localhost/127.0.0.1来源。

这使“被列入市场”事实上等价于永久账户访问授权，而不是“可以被用户打开”。开发机上任何监听允许端口的本地进程也可能获得钱包上下文。

### 修复要求

- Market catalog与账户授权彻底分离；
- 首次 `requestAccounts`必须由用户审批；
- 不主动发送账户信息；
- 删除 production localhost allowlist；
- iframe使用最小 sandbox，除非业务确需，不允许 top-navigation、same-origin或任意弹窗；
- 禁止 `targetOrigin="*"` 发送任何账户信息；
- catalog URL必须 HTTPS、canonical origin并经过签名 manifest验证；
- 用户可查看、撤销单个 origin权限。

---

## 5. P1 高优先级问题

## PWA-P1-01：Origin授权模型缺少生命周期和方法级权限

`authorized-origins.ts`主要提供 add/get/isAuthorized，没有：

- revoke；
- expiry；
- last-used；
- wallet/account/network scope；
- methods/capabilities；
- spending limit；
- session-only授权；
- schema corruption处理。

建议权限对象至少为：

```ts
interface DappGrant {
  origin: string
  createdAt: number
  expiresAt?: number
  network: string
  walletFingerprint: string
  accountIndex: number
  capabilities: string[]
  sessionOnly: boolean
}
```

切换 wallet/account/network后旧授权不得自动继承。敏感方法如签名、广播、UTXO unlock、contract deploy应独立授权并可要求逐次确认。

---

## PWA-P1-02：CSP、WASM完整性和 Service Worker更新不足

涉及：

- `pwa/index.html`
- `pwa/vite.config.ts`
- `pwa/utils/wasm.ts`
- `pwa/public/service-worker.js`

问题包括：

- repo内没有足够严格的 production CSP；
- `index.html`包含 inline script，阻碍移除 `'unsafe-inline'`；
- WASM fetch未严格检查 HTTP status、MIME和预期 hash；
- `go.run`退出和 init返回值没有完整上报；
- Service Worker对 HTML/JS/WASM采用 cache-first，可能长期运行旧或被污染的高权限代码；
- production source map开启会扩大源码与内部接口暴露。

### 修复建议

- CSP至少限制 `default-src 'self'`、`frame-src` allowlist、`connect-src`明确后端、禁止 object/base-uri；
- 移除 inline script，使用 nonce/hash；
- WASM/worker manifest包含 SHA-256，加载时验证；
- Service Worker使用版本化 precache + network-first HTML + 原子 activate；
- 新版本必须同时匹配 JS、WASM和数据库 schema；
- 默认关闭公开 source map，内部发布单独保存符号文件；
- 加入 service-worker kill switch与回滚版本策略。

---

## PWA-P1-03：网络/钱包/账户切换的失败回滚不完整

PWA网络切换整体采用 release/reinit是正确方向，但多个动作仍存在：

- 先更新 UI/localStorage，再调用 SDK；
- SDK失败只 warning；
- `deleteWallet`、`addAccount`后忽略 switch失败；
- 旧 Manager release失败后仍尝试启用新 Manager；
- callback/轮询可能使用旧 generation。

所有身份变化应采用状态机：

```text
IDLE -> QUIESCING -> PERSISTING -> REBUILDING -> VERIFYING -> READY
```

任一步失败恢复明确旧状态，期间禁止签名和 DApp请求。

---

## PWA-P1-04：Mnemonic导入会静默重写用户输入

当前部分输入清理会移除所有非字母数字字符。对助记词来说，应只做明确允许的 Unicode normalization、trim和空白折叠，不能把非法字符删除后继续导入。

否则用户输入错误可能被转换成另一组合法助记词，造成恢复到错误钱包。

要求：

- 保留词边界；
- 明确 BIP39 language；
- 校验词数、wordlist和 checksum；
- 非法字符直接报错；
- 显示 fingerprint/address让用户二次核对；
- 首次导入成功后自动建立账户管理。

---

## PWA-P1-05：Raw message签名缺少域隔离

`SignMessage.vue`仍允许 DApp调用 raw `signData`。如果签名只是对任意内容哈希后 ECDSA/Schnorr签名，恶意 DApp可以把用户看到的文本包装成另一协议的授权消息。

建议：

- DApp默认只允许 `signWalletMessage`等带固定 domain/network/origin的格式；
- raw signData移到高级模式并逐次显示十六进制 digest、origin和不可撤销警告；
- 禁止把 transaction/preimage/contract admin payload伪装成普通文本；
- 签名结构绑定：`SAT20 Wallet Message`、network、origin、timestamp、nonce。

---

## PWA-P1-06：PWA Solidity编译结果不能作为源码验证事实

涉及：

- `pwa/utils/solc-browser.ts`
- `pwa/entrypoints/popup/pages/wallet/Tools.vue`
- `package.json` 中 solc版本

PWA bundle使用的 solc版本与节点当前规范配置可能不同。页面即使从节点读取 `solcVersion=0.8.30`，实际浏览器 bundle也可能是 0.8.36。静态 `deployedBytecode.object`还不能处理 constructor immutable和准确链上上下文。

按照 SatoshiNet已确认设计：

```text
/blob/evm/source/<contract_address>
```

只有节点端完成以下验证后才能写入：

1. pinned solc exact compile；
2. `creation bytecode || constructor args == DeployTx.ContractContent`；
3. 使用准确 pre-state和 block context重放部署；
4. replay runtime bytecode与 committed code逐字节一致。

PWA只提交 source package，不得展示自己产生的“verified=true”。浏览器本地编译只能作为开发预检，并必须显示实际 compiler binary/version。

---

## PWA-P1-07：关键值使用 JS `number`

UTXO ID编码、satoshi总额、资产原子量、gas、BindingSat和部分 height可能超过 2^53。任何 `BigInt -> Number` 或 JSON number转换都会产生不可见精度损失。

统一规则：

- wire/API中大整数使用十进制字符串；
- UI内部使用 `bigint` 或 Decimal库；
- 禁止以 `number`参与交易构造；
- 只在经过范围检查后用于展示小值；
- operation log和审批摘要保留原始字符串。

---

## PWA-P1-08：密码和助记词生命周期

### 密码

解锁期间 password保存在响应式 Pinia state；浏览器 devtools、插件或错误日志更容易观察。PWA还先 SHA-256后交给 SDK，造成客户端语义不一致。

修复：

- 原始密码只在局部变量中短暂存在；
- SDK内部执行唯一 KDF；
- store只保留 `unlocked session token/generation`，不保留密码；
- 旧 prehash scheme使用显式版本迁移。

### 助记词

助记词查看页会将 mnemonic放入响应式状态并支持 clipboard。需要：

- 页面离开/unmount立即清空；
- 不允许clipboard；
- 禁止截图提示；
- 重新验证密码并增加尝试限速；
- 不输出控制台或 operation log；
- 不允许 iframe/DApp同时打开助记词页面。

---

## 6. P2 工程问题

### PWA-P2-01：审批请求管理不是队列

approval store使用单个当前请求。并发 DApp请求可能互相覆盖、让前一个收到错误结果，或造成用户将一个请求的 UI理解为另一个请求。

应使用有界队列，按 origin/request id保存状态；同一 origin限速；过期请求自动拒绝。`handledRequests`需要 TTL/LRU和最大数量。

另有 `BIND_REFERRER_FOR_SERVER`被标记为需要审批，但未发现对应审批组件，可能出现空白或悬挂页面。每个 approval-required method必须有完整 renderer，否则 fail closed。

### PWA-P2-02：控制台敏感日志

删除：

- raw PSBT；
- signed PSBT；
- signature；
- full parsed transaction；
- source/bytecode；
- password/mnemonic相关错误上下文。

生产 logger使用结构化事件和脱敏短 hash。

### PWA-P2-03：构建终态不可追踪

本次两次 `sat20wallet-pwa-typecheck` 和一次 `sat20wallet-pwa-bundle` 均在 MCP调用层超时，profile没有像 Go测试一样生成持久化 status/log，因此无法确认通过或失败。

需要把前端 build profile接入相同 supervisor：

```text
RUNNING/PASSED/FAILED
start/end/duration/exit_code
full log/tail
```

发布不能只依赖 MCP请求是否超时。

---

## 7. 已确认的正向实现

1. **网络切换方向正确**：PWA释放旧 Manager并按目标配置重新初始化，避免直接依赖不完整的 SDK `SwitchChain`。
2. **DApp request envelope**：已经检查 source、origin、request id、nonce和 expiry，基础防重放框架可继续增强。
3. **Operation log包装**：`pwaOperationLog.ts`倾向记录动作摘要而非原始参数/结果，符合“日志给人看”的原则。
4. **Agent risk policy**：对高风险 AI Agent动作增加 warning和第二次确认，是正确的上层防线。
5. **WASM生命周期有集中入口**：`utils/wasm.ts`和 release/reinit路径便于后续加入 generation、integrity和 cancel。
6. **账户管理 UI已有基础**：临时/付费存储、恢复配置和状态展示可在 SDK修复后继续使用。
7. **DApp origin canonicalization基础存在**：不是完全信任消息内自报 origin，而是结合 iframe/window source检查。

---

## 8. 与 SDK、SatoshiNet和Transcend的联动

### 与 SDK

PWA-P0-01/P0-02必须依赖 SDK真实 Lock和 WASM最小导出；仅在 Vue中隐藏按钮没有安全意义。

PSBT审批应由 SDK返回权威、可签名的结构化 plan，PWA只展示并把用户批准的 digest返回 SDK。

### 与 SatoshiNet

参考：

```text
/Users/yingfeng/github/satoshinet/docs/satoshinet-review-remediation-plan.md
```

- PoS V2后 L2 confirmation/finality UI应区分 V1与 V2；
- 合约激活后每块 combined root可用于最终确认显示；
- AssetName只允许严格三段；
- EVM source由节点验证后写 system Blob；
- DKVS snapshot source必须是授权 authority。

### 与 Transcend

PWA不能把 Transcend公开 reservation列表或未认证 ACK当作可信状态。通道/跨链操作 UI应验证：

- request/response签名；
- channel/reservation绑定；
- peer identity；
- 当前状态 generation；
- 服务端 operation log与本地 operation log关联。

---

## 9. 建议实施顺序

### 阶段 1：浏览器密钥边界

1. SDK真实 Lock；
2. 删除生产 WASM调试/密钥导出；
3. PWA auto-lock接入 SDK Lock；
4. password不进 Pinia；
5. 删除敏感日志。

### 阶段 2：DApp权限

1. Market不自动授权/推送账户；
2. capability grant + revoke + expiry；
3. 删除 production localhost；
4. UTXO锁 owner绑定；
5. 所有高风险方法逐次审批；
6. approval有界队列。

### 阶段 3：签名安全

1. SDK权威 PSBT plan；
2. PWA fail-closed展示；
3. 大整数改 string/bigint；
4. raw signData域隔离；
5. identity/network generation绑定。

### 阶段 4：供应链与合约

1. CSP；
2. WASM/worker hash；
3. Service Worker原子更新；
4. 关闭公开 source map；
5. Solidity改为节点验证工作流；
6. AssetName严格三段。

---

## 10. 测试要求

### 当前结果

- `sat20wallet-pwa-typecheck`：两次 MCP调用超时，**未取得终态**；不能宣称通过或失败。
- `sat20wallet-pwa-bundle`：MCP调用超时，**未取得终态**。
- 当前 profile没有持久化日志，需要先改进测试运行基础设施。

### 必增自动化

- 锁定后 WASM敏感 API全部拒绝；
- XSS模拟脚本无法访问 Manager/DB/private key；
- DApp首次 account请求审批；
- grant revoke/expiry/network/account isolation；
- Market iframe无自动授权；
- UTXO system lock不可被 DApp释放；
- PSBT所有失败场景 confirm disabled；
- duplicate outpoint不可隐藏；
- >2^53数值完整显示和提交；
- service-worker版本切换时 JS/WASM一致；
- WASM hash错误拒绝加载；
- CSP E2E；
- source package仅显示 node-verified状态；
- first-import账户管理；
- network/wallet/account切换并发。

---

## 11. 发布退出条件

1. 前端锁定等于 SDK密钥锁定；
2. production WASM导出面通过 allowlist审计；
3. PSBT无法完整解释时绝不签名；
4. DApp不能解锁非自己持有的 UTXO；
5. Market不自动授权或主动披露账户；
6. 权限可撤销、可过期、按方法和身份 scope隔离；
7. 强 CSP、WASM完整性和 Service Worker原子更新生效；
8. 所有金额使用无损类型；
9. Solidity verified只来自节点 exact compile/replay；
10. typecheck、bundle、SDK tests、PWA E2E均有持久化 PASSED终态；
11. JS/WASM/source commit/hash写入 release manifest。

---

## 12. 下一次 session 的执行入口

1. 读取本文档和 SDK审查文档；
2. 检查现有两个 WASM changes，不覆盖用户生成物；
3. 先实现 SDK Lock和生产导出 allowlist；
4. 再修改 PWA lock、DApp权限和 PSBT审批；
5. 所有修改只放 changes/unstaged；
6. 不运行真实网络动作；
7. 完成后串行执行 typecheck、bundle、SDK E2E和 PWA安全测试；
8. 不 stage、不 commit、不 push，除非用户明确授权。
