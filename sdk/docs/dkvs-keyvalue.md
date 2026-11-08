# DKVS Key / Value 清单

核对日期：2026-11-07。依据 `sat20wallet/sdk` 与 `satoshinet` 当前工作区生产代码，包含尚未提交的修改；这不是已发布版本的兼容承诺。测试夹具中的临时 key 不计入协议清单。

未发布的 DKVS、RGB 和账户格式统一为首版 1。账户状态使用 `AMS1` / `AME1` 及 `sat20-account-managed-state-v1` / `sat20-account-managed-state-aad-v1|` 密码学域；本地 profile key 为 `<B>account-management-profile-v1`。本次没有旧格式兼容或数据迁移，已有开发数据和恢复包不能假定可由新格式读取；通道业务版本、外部协议版本及业务修订号保持原样。

本文是持续维护的 key/value 登记表。新增、删除或改变 key、value 字段、编码、权限或存储范围时，必须在同一变更中更新本文。未找到生产写入方的定义明确标记为“通用/预留”或“残留定义”，不能据此认为 PWA 正在使用。

## 1. 读表约定与公共记录格式

- `A`、`S`、`R`：账户、发送方、接收方的 32 字节 x-only 公钥，小写十六进制共 64 字符，不是公钥的哈希。
- `N`：网络；`AccountMappingKey` 规范化为 `mainnet`、`testnet3`、`testnet4`、`signet` 或 `regtest`。
- `P`：恢复包 ID，16 字节的小写十六进制；`M`：消息 ID，8 字节 Unix 微秒加 8 字节随机数，合计 32 个小写十六进制字符。
- `T`：Topic 名；`D`：DID/service 名。名称经过小写和空白转下划线处理；非法字符报错，不生成哈希替代名称。
- `B`：SDK `GetDBKeyPrefix()`；轻节点为 `<env>-<chain>-`，其他模式为空。`NS`：SDK replica namespace，为 `<env>:<chain>`。
- 网络 key 以 `/` 开头，最长 256 字节；每段最长 64 字节，字符集为 `[a-z0-9._-]`，不能有空段。合法命名空间共 **10 个**：`sys`、`name`、`svc`、`account`、`personal`、`mail`、`topic`、`blob`、`contract`、`tmp`。
- 下表描述 `DKVSRecord.Value`。HTTP JSON/base64 是传输表示，不等于 value 使用 JSON 编码。

每个普通网络条目包在 `wire.DKVSRecord` 中，字段按以下顺序编码：

```text
Version:u32le
Key:CompactSize+UTF-8
Value:CompactSize+bytes
PubKey:CompactSize+bytes
Signature:CompactSize+bytes
Seq:u64le, IssueHeight:u64le, TTL:u64le
FeeProof:CompactSize+bytes
Flags:u32le
```

当前记录版本为 1。账户作用域记录从 key（`/account` 则从 value）推导签名者，可省略外层 PubKey；消息服务投递记录由服务写入，真实性在内部消息签名中验证。普通记录整体上限 16 KiB；Blob、Contract、Mail 消息的 value 上限 1 MiB、记录整体上限 1 MiB + 4 KiB。不能把 value 上限当作扣除其他字段后的可用空间。

`Seq`、CAS ETag、TTL、FeeProof 都属于记录或写入控制，不应拼进业务 key。删除并非另存一个带版本的 key。codec 版本放在 value 内；随机 nonce 导致加密结果变化是正常的，加密封装本身的字段编码仍有确定格式。

Strict 指工作区 `rgb11/strict_encoding`：固定整数为小端，集合/字符串长度前缀宽度由声明的最大长度决定，不是 Bitcoin CompactSize；uvarint 另指 Go 无符号变长整数。各编码不可混用。

来源：`satoshinet/indexer/indexer/dkvs/{key,account,sdk,record}.go`、`satoshinet/wire/msgdkvs.go`。

外层 FeeProof 使用 `fee.go` 的紧凑编码：version u8(1)、mode u8、各字符串的 uvarint 长度及 UTF-8 字节。ONESHOT 保存 PoolContract/Payer/PaymentTxID/PaidAmount；LEASE 保存 PoolContract/LeaseContract/PlanID；AUTOPAY 只保存 PoolContract（payer 从记录身份推导）；FREE_LOCAL 没有后续字段。它不属于业务 value，也不应形成新的 key 后缀。

## 2. DKVS 逻辑 key 总表

本节共 29 条模板（含通用形状、具体业务实例与仅计算使用项）。实例会落在通用模板内，因此不能将行数当作互不重叠的存储类别数量。它们采用 DKVS 逻辑 key 语法，并非全部进行网络复制：`/mail` 归属账户绑定的 CoreNode，`/topic` 仅保存在 Topic 服务所在 CoreNode，`/tmp` 与 FREE_LOCAL 记录为本地存储；其他记录按付款及命名空间策略参与网络复制。来源：节点 `replication.go`。

### 2.1 账户与个人数据

| Key 模板 | Value 内容与格式 | 写入/读取与范围 |
| --- | --- | --- |
| `/account/<N>/<root-address>` | `AccountServiceDescriptor`：版本 u8、账户公钥 32B、CoreNode 压缩公钥 33B、Capabilities u64be，后接按 Type 严格递增的扩展 TLV（Type u16be、长度 u16be、bytes）。固定部分 74B，整体至多 1024B。 | `AccountMappingKey` / `EncodeAccountServiceDescriptor`；SDK 账户绑定，节点路由发现。唯一免费账户控制记录；不是任意免费存储。 |
| `/personal/<A>/<path...>` | 通用不透明 bytes，无统一业务 schema；具体实例见下列各行。 | `AccountPersonalKey` / `PersonalKey`；账户签名。 |
| `/personal/<A>/primary_did` | DID 名的原始文本，无 JSON 包装；最长 10 字符，小写合法段名。 | `PrimaryDIDPersonalPath`；节点校验 DID 当前有效及签名权限。 |
| `/personal/<A>/account/root` | `ARWR` + codec u8（当前 1）+ Strict 编码的 Network、AccountID、Nonce、Ciphertext。AES-GCM 密文解开为 `ARWP` + u8(1) 业务载荷：账户 Secret、PackageID、StorageMode、RecordTTL、AutopayContract、可选 GuardianLocation。 | SDK `accountRootWrapperPath`、`encodeAccountRootWrapperEnvelope`；根钱包恢复入口，账户备份同一生命周期。根恢复对象使用稳定的短路径 `account/root`。 |
| `/personal/<A>/account/state` | `AME1` + nonce(12B) + AES-GCM 密文；明文为 DEFLATE 压缩的 `AMS1` 紧凑状态，包含 RootFingerprint 与 Wallets（Fingerprint、Deleted、Name、Type、Secret、AccountCount、SubAccounts）。 | `accountManagedStatePath` / `SealManagedState`；wallets/global 的当前目录，不在此存 RGB11 完整数据。 |
| `/personal/<A>/account/index` | `AMIE` + u8(1) + nonce(12B) + AES-GCM 密文；明文 `AMIX` + u8(1) + Strict 条目列表。每项 `Provider, Scope, Key, Codec, Deleted`，按 Provider/Scope 排序，最多 4096 项。 | `ManagedIndexKey` / `SealManagedIndex`；定位分模块备份。 |
| `/personal/<A>/account/recovery/<P>` | `ACRP` + u8(1) + Strict 紧凑恢复包，详细字段见第 3 节。 | `accountPackagePath` / `EncodeRecoveryPackageStorage`；每个恢复包一个不可变值，Guardian 投递另外处理。 |
| `/blob/<A>/<entry-id>` | `DKB1` 外壳，Metadata 为空；Data 为 `AMOE` + u8(1) + nonce + AES-GCM 密文，明文 `AMOD` + u8(1) + `Provider, Scope, Codec, Payload`。 | `ManagedModuleKey` / `SealManagedModule`；当前分模块备份，`entry-id` 计算见第 3 节。 |

### 2.2 Blob、资产与合约

| Key 模板 | Value 内容与格式 | 写入/读取与范围 |
| --- | --- | --- |
| `/blob/<A>/<blob-name>` | DKVS 节点把 value 当作不透明 bytes；SDK 通用 Blob API 规定 `DKB1`(4B) + Metadata 长度 u32be + Metadata + 非空 Data。无隐式压缩。 | `BlobKey`；SDK `EncodeBlobValue` / `DecodeBlobValue`。Data 与 Metadata 合计至多 1 MiB - 8B。 |
| `/contract/rgb11/<contract-id>` | u8(1) + Bitcoin CompactSize 长度 + 规范 RGB11 **公开合约**文件。包含 genesis/schema/types 等可公开材料，不包含钱包历史、私有 seal 或余额。 | SDK `RGB11RegistryKey` / `EncodeRGB11RegistryValue`；节点技术注册入口验证后写入，不能直接从该值推出钱包资产余额。Contract ID 是 32B 小写 hex。 |
| `/contract/evm/source/<contract-address>` | `EVMSourceMetadata` **Core Deterministic CBOR** 固定 16 字段数组：Version、Language、ContractAddress、DeployTxID、ContractName、Source、ABI、CompilerConfig、ConstructorArgs、InitCodeHash、RuntimeCodeHash、Verified、VerifyStatus、VerifyError、SubmittedAt、UpdatedAt。ABI 为嵌套 CBOR 数据，不嵌 JSON；CompilerConfig 为固定 7 字段数组，见第 3.4 节。 | `contract.EncodeEVMSourceMetadata` / `DecodeEVMSourceMetadata`、`IsEVMSourceKey`、节点 `contract_index.go`、`evmsource/verifier.go`；签名前编码，验证部署和编译结果。节点不改写已签名 value。 |
| `/contract/<authority-path...>/<object-id>` | 通用 authority contract 不透明 bytes；当前明确的业务实例是 RGB11 技术注册。 | `IsAuthorityContractKey`；普通本地 PUT 不可绕过 authority 验证。 |
| `/tmp/<random-id>` | 任意受限 bytes，未发现统一业务 schema。 | `TmpKey`；节点本地、短期数据，不进行网络复制。 |

### 2.3 DID 与服务

| Key 模板 | Value 内容与格式 | 写入/读取与范围 |
| --- | --- | --- |
| `/name/<D>` | 通用不透明 bytes；本次两仓库未找到统一业务 value schema。DID 所有权来自 resolver，不从 value 文本自行推断。 | `NameKey`，SDK name record API；按当前 DID 签名权限验证。 |
| `/svc/<D>/<path...>` | 通用不透明 bytes，业务自定义。 | `ServiceKey`，SDK service record API；按 service DID 权限验证。 |
| `/svc/<D>/authenticity/<app-id>` | `DKWA` + u8(1) + kind u8(4)，后接 Strict 编码：Version u32、ServiceName、AppID、Release、ArtifactHash、DownloadURL、按 key 排序的 Metadata 字符串表。 | `ServiceAuthenticityPath` / `EncodeServiceAuthenticity`；Release 放在 value，不进入 key。 |

### 2.4 Mail 与 Topic

| Key 模板 | Value 内容与格式 | 写入/读取与范围 |
| --- | --- | --- |
| `/mail/<R>/msg/<S>/<M>` | `SerializeDirectMessage(..., true)` 二进制：SenderAccount、SenderMsgID、MessageID、RecipientAccount、Ciphertext、SenderSignature。长度前缀和整数按 `wire/message_service.go` 编码。Ciphertext 内的 SDK 消息种类见第 3 节。 | `MailMsgKey` / `AppendDirectMessage`；MessageManager 在接收账户绑定的 CoreNode 写入，普通 DKVS PUT 不可代替消息服务。 |
| `/mail/<R>/share/<P>/<share-id>` | `AGRD` + u8(1) + PackageID(16B) + Strict 编码 ShareID、EphemeralPublicKey(32B)、Nonce、Ciphertext；算法为 X25519 + AES-256-GCM。 | `MailShareKey` / `EncodeGuardianCapsuleStorage`；Guardian 份额密文，按 Guardian 读取与恢复。 |
| `/mail/<R>/topic/<T>/msg/<S>/<M>` | `SerializeTopicPublish(..., true)`：TopicName、SenderAccount、SenderMsgID、MessageID、KeySeq、Ciphertext、SenderSignature；确定的二进制编码。 | `MailTopicMessageKey` / `AppendTopicMessage`；Topic 服务投递到成员邮箱。 |
| `/mail/<R>/topic/<T>/key/<key-seq>` | `SerializeTopicKeyFanout`：TopicName、KeySeq、IssuerAccount、Recipients；持久化时 Recipients 只有接收者一项，包含 Recipient、EncryptedTopicKey、IssuerSignature。`key-seq` 为十进制。 | `MailTopicKeyKey` / `AppendTopicKeyPackage`；key-seq 是群密钥轮换序号，有业务意义，不是 schema 版本后缀。 |
| `/topic/<T>/meta` | `TopicMeta` 紧凑二进制：codec u8(1)，依次 TopicName、DisplayName、OwnerAccount、ServiceCoreNode、ServicePubKey、JoinPolicy、MessagePolicy、MaxMembers(u32be)、CreatedAtHeight(u64be)。字符串/bytes 使用 uvarint 长度前缀。 | `TopicMetaKey` / `wire.SerializeTopicMeta` / `DeserializeTopicMeta` / `DKVSTopicCatalogStore.SaveTopic`；Topic 服务内部目录。 |
| `/topic/<T>/state` | `TopicState` 紧凑二进制：codec u8(1)，依次 Status（uvarint 长度+UTF-8）、KeySeq(u64be)、MemberCount(u32be)、LastCommitHash（uvarint 长度+UTF-8）。 | `TopicStateKey` / `wire.SerializeTopicState` / `DeserializeTopicState` / `SaveTopic`；服务状态。 |
| `/topic/<T>/members/<A>` | `TopicMember` 紧凑二进制：codec u8(1)，依次 AccountID、Status、Role（均为 uvarint 长度+UTF-8）、JoinedSeq(u64be)、LeftSeq(u64be)。 | `TopicMemberKey` / `wire.SerializeTopicMember` / `DeserializeTopicMember` / `SaveTopic`；成员状态。 |

### 2.5 System 与仅计算使用的 key

| Key 模板 | Value 内容与格式 | 状态 |
| --- | --- | --- |
| `/sys/params` | 未定义统一 value schema。 | `SystemParamsKey`；有构造函数和合法性检查，本次未找到实际生产写入方。 |
| `/sys/miner/<id>` | 未定义统一 value schema。 | `SystemMinerKey`；同上。 |
| `/sys/pool/<id>` | 未定义统一 value schema。 | `SystemPoolKey`；同上。 |
| `/sys/checkpoint/<id>` | 未定义统一 value schema。 | `validateNamespaceShape` 接受此形状；本次未找到业务编码器/写入方。 |
| `/sys/snapshot/<id>` | 除下行哈希构造外，未定义统一持久化 schema。 | 合法 key 形状，不据此推断存在快照存储记录。 |
| `/sys/snapshot/hash` | 临时构造的记录：Height、ActiveRecordCount、ActiveRecordTotalSize、ActiveRecordRoot、CreatedAt、按 namespace 排序的根与排序后的 RecordHash，使用 `checkpoint.go` 的确定二进制拼接。 | `SnapshotHash` 内用于计算哈希，**不写入 DKVS**。 |

## 3. 复合 value 的内部内容

### 3.1 账户模块标识与载荷

`ManagedEntryID(provider, scope)` 当前计算：`SHA256("sat20-account-entry-v1\x00" + Strict.String(provider) + Strict.String(scope))`，输出 64 字符小写 hex。字段有明确长度边界。它不含 payload、codec、账户或本地 revision；业务内容改变不会生成新 key。`v1` 位于密码学域分离输入中，不是路径后缀；本轮保留该域，避免将 key 命名清理混入密码学派生规则调整。

`Scope` 是 `global` 或 `<network>/<wallet-fingerprint>/<account-index>`。当前注册的模块为：

| Provider / Scope | 载荷内容与编码 | 代码入口 |
| --- | --- | --- |
| `wallets/global` | 第 2 节的 ManagedState，单独放在 `account/state`；index 中的 Codec 当前为 `wallets-v1`。 | `account_index_sync.go`、`account/state.go` |
| `silentpay/<scope>` | 一个字节 `01` 表示该 scope 开启 SilentPay；未开启不输出该项。外层仍为加密 ManagedModule + DKB1。 | `silentpay_account.go` |
| `rgb11/<scope>` | `RecoveryPackage`：Version、WalletID、AccountIndex、EngineBuildID、ProjectionRecords、EngineRecords；记录为 key/value 数组，携带恢复所需的公开证明、私有材料、活动转换等完整闭包。外部 `R11K` + codec 字节 + DEFLATE，内部紧凑 codec；当前业务 Version=1。 | `rgb11_account_provider.go`、`rgb11/recovery.go`、`rgb11/recovery_codec.go` |

其他模块的 index Codec 当前为 `v1`。这些 Codec 字段是 **value 内容**，不是 DKVS key；内层业务结构由 provider 负责，不能用任意模块 payload 互换。

### 3.2 恢复包

`ACRP` 恢复包按顺序保存：AccountID(32B)、PackageID(16B)、RecoveryMode、加密备份 Nonce/Ciphertext、CreatedAt、可选 Guardian locator（MailboxID、ShareID、CapsuleHash）、可选完整 Guardian capsule、DKVS share 的 Nonce/Ciphertext、知识问题列表（ID、Prompt、CaseSensitive、IgnorePunctuation、Salt、Vault、Nonce、Ciphertext）。以 `EncodeRecoveryPackageStorage` 的字段顺序和长度限制为准，省略可从 envelope 推导的重复版本、locator、算法与哈希。不能把加密备份、DKVS share、Guardian share 混为一个明文秘密。

### 3.3 DirectMessage 内的业务数据

SDK `wallet/message_payload_codec.go` 把 `AccountMessagePayload` 编为 `AMSG` + version u8(1) + Kind + ApplicationID + Body；后三项均使用 Bitcoin CompactSize 长度前缀，Body 为原始 bytes，不经过 JSON/base64。`message_manager.go` 在编码后加密，再签名完整 DirectMessage；kind 包括 GENERIC、OFFLINE、RGB11_CONSIGNMENT、RGB11_ACK、ACCOUNT_MANAGED_ACTIVE。网络上的 mailbox key 始终使用消息 ID，业务 application ID 不直接成为 mailbox key。

- RGB11 consignment：当前 `deliverRGB11AddressTransferStore` 发送 `AccountMessageKindRGB11Consignment`，载荷为接收方 consignment；当前发送路径为 DirectMessage。
- RGB11 ACK：`EncodeAddressACK` 为 4B：version u8(1)、status u8、code u16be；status 为 accepted / need-resend / rejected。
- 账户活动备份：`AMA1` + provider 长度 u8 + provider + scope 长度 u16be + scope + SHA256(payload)(32B) + payload；由账户消息层加密投递。它与持久模块 Blob 的生命周期不同。
- 旧 `DKWA` kind=3 offline codec 仍有定义（Version、FromPubKey、ToMailboxID、MessageID、EncryptedMessage、Metadata），但客户端 `SendOfflineMessage*` / `ReadOfflineMessages` 直接返回 `ErrDKVSMessageManagerRequired`。不能把它登记为当前消息发送协议。

### 3.4 EVM Source 的嵌套数据

`CompilerConfig` 的 CBOR 数组字段依次为 SolcVersion、EVMVersion、Optimizer.Enabled、Optimizer.Runs、Metadata.BytecodeHash、SingleFileOnly、AllowImports。ABI 的 JSON 接口输入转换为 CBOR 数组/映射/标量，映射按 Core Deterministic 规则排序；未提供 ABI 使用 undefined，显式 JSON null 使用 null。整数保留精度，非整数沿用接口原有 float64 语义。Version 仍为业务版本（当前 1），不另加重复的 value revision。

`EncodeEVMSourceMetadata` 必须在生成 DKVS 签名前调用；HTTP POST 仍接收完整已签名记录，GET 返回 JSON 元数据。`DecodeEVMSourceMetadata` 拒绝旧 JSON、尾随数据和非规范 CBOR，不重新编码覆盖 record.Value。新字节的 RecordHash/签名按原规则计算，未修改密码学域或验签协议。

## 4. SDK 本地 DKVS 数据库 key

本节 key **不经过 DKVS ParseKey，不是网络记录**。同一数据库还含钱包和业务状态；本文只登记 DKVS replica/outbox 及直接关联的账户备份 key。

| 本地 key | Value 内容与格式 | 定义 |
| --- | --- | --- |
| `dkvs:subscription-state:<NS>` | `DKSS` + u8(1) + uvarint/长度前缀紧凑结构：EndpointID、Prefixes、ViewHeight、LastSyncAtMS、Status、LastErrorCode；Generations 从 ActiveMeta 推导，不在这里持久化。 | `wallet/dkvs/replica.go`、`replica_codec.go` |
| `dkvs:subscription-prefix:<NS>:<prefix>` | 单字节 `01`，注册标记。 | 同上 |
| `dkvs:subscription-record:<NS>:<network-key>` | `MarshalRecord` 的完整二进制 DKVSRecord，最后验证过的订阅副本。 | 同上 |
| `dkvs:outbox:<NS>:<request-id>` | `DKOB` + u8(1) 紧凑编码：EndpointID、Mutations（原始签名 Record、ExpectedETag/ExpectAbsent）、State、Attempts、LastErrorCode/LastError、CreatedAtMS/UpdatedAtMS、OriginKey/OriginDomain/OriginGeneration、Authorization。RequestID、Namespace 从本地 key 传入解码，不重复保存在 value。Authorization 为下述紧凑 bytes。 | 同上 |
| `dkvs:active-meta:<NS>:<scope-hash>` | `DKAM` + u8(1)：EndpointID、Scope.Prefix、Scope.Keys（uvarint 个数+各 key）、Scope.Network(u8 0/1)、Generation(uvarint)、Root(32B)、ViewHeight(uvarint)。字符串均为 uvarint 长度+UTF-8；scope-hash 仍为规范 JSON(scope) 的 SHA256 hex，仅用于 key 派生，不持久化 JSON。分页 Cursor 不保存在这里。 | `wallet/dkvs/active_replica.go`、`active_replica_codec.go` |
| `dkvs:active-ack:<NS>:<prefix>` | `DKAK` + u8(1) + EndpointID（uvarint 长度+UTF-8）+ Generation(uvarint)；已确认请求的位置，不是 receive cursor 或写入授权。 | 同上 |
| `<B>account/<A>/<module-hash>` | **gob** `accountLocalKV`：Version、AccountID、Network、Provider、Scope、Key、Codec、Value、Baseline、Generation/Captured/Confirmed、Deleted/Restoring/Conflict、ConflictValue、LastError。Value 已加密，Baseline 为已验证的远端记录。module-hash = SHA256(u32be长度+provider+u32be长度+scope) 的 hex。 | `wallet/account_local_kv.go` |
| `<B>message-outbox/<application-id>` | **gob** `accountMessageOutboxRecord`：Version（当前 1）、ApplicationID、SourceCoreNode、Recipient、Kind、完整签名 DirectMessage。根账户消息发送重试保存原始密文与消息 ID。 | `wallet/message_manager.go` |
| `<B>message-outbox/<S>/<application-id>` | 与上一行相同；按发送钱包账户隔离，供钱包 DirectMessage / RGB11 消息发送使用。 | 同上 `accountMessageOutboxKeyForSender` |

Outbox 的 Authorization 为 uvarint 长度封装的 bytes，空长度表示 nil；非空时依次保存 Context.EndpointID（uvarint 长度+UTF-8）、Prefixes 数量(uvarint)、原顺序的 Prefix（uvarint 长度+UTF-8）与 Generation(uvarint)、原始 Signature(64B)。编码和解码都保留原有 `VerifyWalletWriteAuthorization` 校验；重放不重建授权，也不改变签名、CAS 或 prefix 顺序。这些是持久副本/冻结请求的一部分，不是独立的网络 key。

节点处理钱包写入时遇到数据库 I/O 错误，返回 HTTP 503 和 `DKVS_STORAGE_UNAVAILABLE`。SDK 对所有 key 都把已入队的请求保留为 `pending`，下次同步时重放同一 request-id、签名和 CAS 条件，收到确认后清理 outbox。

钱包写入的 `IssueHeight` 落后节点 tip 超过 2 块时，节点返回 HTTP 409 / `DKVS_HEIGHT_TOO_LOW`。SDK 对所有 key 统一刷新高度、重签记录和批次授权，保持 key、value、Seq、CAS、EndpointID 和原始 prefix generation 不变；一次提交最多尝试 3 次。已有 outbox 必须先原子保存新 request-id 对应的签名请求并删除旧条目，成功后才能发送；保存失败保留旧条目。超时或响应丢失时先原样重放，只有明确的高度过低拒绝才允许上述替换。其他冲突和权限拒绝不触发高度重签；已提交且当前内容仍完全相同的请求由节点幂等确认。

`accountKVOutboxDomain = "account"` 是 outbox **value 中的领域标识**，不是网络 key。账户本地前缀使用 `account/`，根恢复路径使用 `account/root`，消息发送队列前缀使用 `message-outbox/`；这些路径不包含 schema 版本或 `current`。当前未上线，不保留旧 key 兼容读取。通用的 `dkvs:` 前缀区分本地存储子系统，有明确作用。

## 5. SatoshiNet 节点内部 key

| 本地 key | Value 内容与格式 | 定义 |
| --- | --- | --- |
| `dkvs:record:<network-key>` | `MarshalRecord` 二进制 DKVSRecord。 | `indexer/indexer/dkvs/indexer.go` |
| `dkvs:hash:<record-hash-bytes>` | 网络 key 原始文本；key 后缀是 RecordHash 原始 32 字节，**不是 hex 文本**。 | 同上 `hashDBKey` |
| `dkvs:name-transfer:<name>` | 单字节 `01`，DID 转移后的重签检查标记。 | 同上 `nameTransferDBKey` |
| `dkvs:pathmeta:<path>` | 固定 84B：Version u32le（当前 1）+ 6 个 u64le（Generation、EndpointGeneration、ActiveRecords、ActiveTotalSize、MinExpiryHeight、ViewHeight）+ StateRoot(32B)。Path 从 key 恢复。 | `pathmeta.go` |
| `dkvs:pathstatus:<path>` | `DKPS` + u8(1)：Path、LastSyncPeer、LocalRetryState（Bitcoin CompactSize 长度+UTF-8）、UpdatedAt/LastSyncAt（Bitcoin CompactSize 整数）、flags u8（bit 0 Dirty、bit 1 Stale）。保留 Path 与 key 的绑定校验，未知 flags 报错。 | `pathmeta.go`、`pathstatus_codec.go` |
| `dkvs:record-change:<network-key>` | Endpoint generation 的 u64be，8B。 | `prefix_change_index.go` |
| `dkvs:prefix-change:<path>\x00<generation-u64be><network-key>` | 单字节 `01`，当前记录的 generation 二级索引；不是无限写入历史。 | 同上 |

P2P 另有合成元记录 key：`\x00dkvs-pathmeta:<SHA256(path)-hex>`。它不满足业务 ParseKey，只在路径快照消息中使用；value 为 path 长度 u16be + path + PathMeta.Version u32be + 5 个 u64be（ActiveRecords、ActiveTotalSize、MinExpiryHeight、ViewHeight、ServerTimeMS）+ StateRoot。外层 Version=1、Seq=1、Flags=`1<<31`。普通同步游标与 P2P 路径快照游标各自的格式版本也均为 1。来源：`indexer/indexer/dkvs/p2p/path_sync.go`、`sync_path.go`。不能把该传输封装当作可供钱包 PUT 的记录。

## 6. 残留定义与其他名为 DKVS 的接口

| 定义 | 发现的内容与状态 |
| --- | --- |
| `/blob/<A>/account-managed-data` | SDK `accountManagedDataBlobKey` 仍能构造该 key，本次未找到生产调用方；当前备份路径是 index + 分模块 Blob。旧 bundle codec 为 DKB1 包裹 ADE 加密 envelope，内部 AMD/ADC 压缩 bundle。不能据此恢复旧 aggregate 设计。 |
| `/blob/<S>/<message-id>` 与旧 RGB11 AddressEnvelope | `loadExistingRGB11AddressDelivery` 仍有旧 Blob 读取辅助定义：AddressEnvelope 为 u8 version + u8 mode，inline 后接 ciphertext，blob 模式只有两字节。当前真实发送函数使用 DirectMessage，不写这套双 key 结构。 |
| RGB11 `ActiveRecoveryPackage` | `wallet/rgb11/recovery_active.go` 的 Encode/Decode 函数仍为 JSON，但当前全 SDK 只有定义与测试调用；生产 `rgb11AccountProvider` 未实现 active provider 接口，实际备份使用 `R11K` 紧凑 RecoveryPackage。不能把此残留 codec 登记为当前 DirectMessage/DKVS 写入格式，也不为本次编码调整重新接入旧路径。 |
| `<B>c-<channel-id>` | `GetChannelFromDKVS` 通过 **L1 indexer GetKV** 读取，独立于本页 SatoshiNet `/...` 命名空间。value 为 `DKCH` + u8(1) + DEFLATE(gob ChannelInDB)；外层 L1 KeyValue 带 pubkey/signature，并对 JSON 表示验签。 |
| `<B>wtptx-<channel-id>-<commit-tx-id>` | watchtower 本地 key 构造；SDK 另有 `EncodePunishTxDKVSValue`：`DKPT` + u8(1) + 原始交易 bytes。两仓库范围未找到该编码器的生产调用方，不把本地 hex 交易值冒充成已写入远端的格式。 |

## 7. 命名与维护约束

1. key 只表达稳定业务身份，不加 schema/release 版本，不加无意义 `kv`、`current` 等词缀。业务上必须区分的恢复包 ID、消息 ID、群密钥序号应保留。
2. 修改 key 时同时查找构造、读、写、删除、订阅、恢复、校验、测试夹具和诊断脚本；当前未上线，不默认增加迁移、双写或 fallback。
3. 保留必要的密码学域分离、value magic 和 codec 版本；不能用字符串全局删 `v1` 的方式改变签名、加密或状态根语义。
4. DKVS value 应采用确定性紧凑编码。本轮清单中 Topic/EVM、DirectMessage 内层、SDK ActiveMeta/ACK/Authorization、节点 PathLocalStatus 已移除持久化 JSON；HTTP/RPC 对象仍可用 JSON，scope key 派生及既有验签所需的临时规范 JSON 不属于持久化业务 value。通用不透明 bytes 不应在节点落库时擅自解析或改写。
5. 新增一行至少说明：完整 key 模板、变量含义、value 字段、编码器/解码器、写入者/读取者、存储范围、权限和生命周期。删除实现时同步删除或明确标记残留行。
6. 本轮只替换编码和对应 reader/writer，不新增旧 JSON 兼容、迁移、双写或 fallback；不删除/重写已有数据库内容。旧格式数据不会被新 codec 接受，部署新代码前需另行处理既有测试数据和同步升级读写双方，不能把未运行的测试视为已通过。
7. 每次维护同时搜索通用 key helper 调用和硬编码路径，不能只搜索字符串 `dkvs`。优先入口：节点 `key.go` / `sdk.go` / `account.go`；SDK `account/managed_index.go`、`wallet/account_*`、`wallet/dkvs/*`、`wallet/rgb11_dkvs.go`；节点 `message_dkvs_store.go` / `message_topic_store.go`。

