# RGB11 资产名称设计规范

> 状态：设计基线  
> 适用范围：RGB11 / SAT20 Wallet / SatoshiNet / STP  
> 目标：作为后续 RGB11 资产命名、注册、展示与索引实现的统一依据。

## 1. 背景

聪网统一资产名称采用：

```text
protocol:type:ticker
```

RGB11 固定使用：

```text
rgb11:f:<ticker>
```

其中：

- `rgb11`：协议；
- `f`：RGB11 fungible asset 类型，含义固定；
- `ticker`：聪网中的 RGB11 canonical ticker。

RGB ContractID 可以严格唯一确定一个 RGB Contract，但长度较长，不适合作为日常用户界面的资产名称。

此前考虑过使用：

```text
ticker@fingerprint
```

来缩短名称，但 fingerprint 只是 ContractID 的摘要，对用户没有实际语义。

本设计取消 fingerprint 命名，改为：

```text
ticker[_ordinal]@provider
```

其中 provider 使用 Ordinals DID。

示例：

```text
rgb11:f:USDT@tether
rgb11:f:USDT_2@tether
rgb11:f:USDT@alice
rgb11:f:GOLD@sat20
```

RGB11 尚未正式发布，因此本设计**不考虑旧 RGB11 资产名称兼容**。新规则启用后直接成为唯一规则。

---

## 2. 设计原则

RGB11 资产名称遵循以下原则：

1. **ContractID 是严格资产身份。**
2. **AssetName 是聪网中的稳定、可读名称。**
3. 不再生成或使用 fingerprint 作为资产名称的一部分。
4. 需要严格识别资产时直接使用完整 ContractID。
5. Provider 必须由 Bitcoin/RGB 状态和聪网 DID Bind 自动确定，不能由发行者任意填写。
6. AssetName 一旦在聪网注册成功即永久冻结。
7. 同一 provider 可以重复发行相同 ticker，通过 ordinal 区分。
8. 聪网维护 `ContractID <-> AssetName` 的双向映射。
9. 尚未进入聪网的 RGB11 资产只有 SDK 本地资产名称；该名称可随本地状态和命名规则变化，不属于协议共识，也不承担严格唯一身份职责。

---

## 3. 两层资产身份

RGB11 明确区分两层身份。

### 3.1 严格机器身份

```text
RGB ContractID
```

ContractID 用于：

- RGB validation；
- 精确识别 Contract；
- 钱包内部资产匹配；
- 跨节点传递严格资产身份；
- 聪网注册前的唯一性判断；
- AssetName 冲突或歧义时的最终校验。

不再引入 fingerprint 作为第二套缩短身份。

### 3.2 聪网可读身份

```text
rgb11:f:<canonicalTicker>
```

其中：

```text
canonicalTicker = <baseTicker>@<provider>
```

或：

```text
canonicalTicker = <baseTicker>_<ordinal>@<provider>
```

例如：

```text
rgb11:f:USDT@tether
rgb11:f:USDT_2@tether
```

AssetName 主要解决：

> 这个资产在聪网里叫什么？

ContractID 解决：

> 这究竟是哪一个 RGB Contract？

---

## 4. Provider 定义

Provider 使用 Ordinals DID 表示。

Provider 不是：

- Bitcoin 地址；
- ContractID；
- fingerprint；
- block height；
- transaction index；
- 自由填写的字符串。

Provider 必须通过聪网上的 Primary DID Bind 从 Genesis outpoint 对应地址确定。

关系为：

```text
RGB11 Contract
      ↓
Genesis Outpoint
      ↓
Bitcoin Address
      ↓
SatoshiNet Primary DID Bind
      ↓
Provider DID
```

即：

```text
Provider(contract) =
    PrimaryDID(
        Address(
            GenesisOutpoint(contract)
        )
    )
```

---

## 5. Primary DID Bind

### 5.1 一个地址可以拥有多个 Ordinals DID

例如：

```text
Address A

├── alice
├── company
└── sat20
```

聪网通过 Bind 操作指定其中一个 DID 作为该地址的首要名称：

```text
Address A -> company
```

此时：

```text
PrimaryDID(Address A) = company
```

### 5.2 一个地址同时只能存在一个有效 Primary DID

如果之后执行：

```text
Address A -> alice
```

则当前 Primary DID 变为：

```text
alice
```

旧的 `company` Bind 不再是该地址的当前 Primary DID。

### 5.3 Bind 必须验证 DID 所有权

Bind 成功必须满足：

```text
Owner(DID) == Address
```

例如只有：

```text
Owner(company) = Address A
```

时，才允许：

```text
BindPrimaryDID(Address A, company)
```

不得把不属于该地址的 DID Bind 为 Primary DID。

---

## 6. DID 长度限制

为了控制最终 RGB11 AssetName 的长度和可读性，能够在聪网上进行 Primary DID Bind 的 DID 必须满足：

```text
len(canonical DID name) <= 10
```

即 DID canonical name 最多 10 个字符。

长度校验在 Bind 操作时执行，而不是在 RGB11 注册时临时截断。

规则：

```text
DID length <= 10
    -> 可以参与 Bind

DID length > 10
    -> Bind 失败
```

禁止：

- 自动截断 DID；
- 对 DID 做 hash/fingerprint 缩写；
- 使用前 10 位或后 10 位代替真实 DID。

长度计算基于 Ordinals DID 经现有规范标准化后的 canonical name。DID 本身的字符合法性、大小写及标准化继续遵循 Ordinals DID 的既有规则。

因此所谓“合格的 DID”至少需要同时满足：

1. 是有效 Ordinals DID；
2. 当前所有权属于执行 Bind 的 Bitcoin 地址；
3. canonical DID name 长度不超过 10；
4. 满足聪网 AssetName 对分隔符和编码的基本要求。

---

## 7. Genesis Address 与 Provider 的强约束

RGB11 Provider 必须与 RGB11 Genesis outpoint 所属 Bitcoin 地址一致。

设：

```text
GenesisOutpoint(contract) -> Address A
```

则 RGB11 注册时必须满足：

```text
PrimaryDID(Address A) = DID X
Owner(DID X) = Address A
```

即：

```text
Genesis Address
    ==
Provider DID Owner Address
```

最终：

```text
Provider(contract) = DID X
```

不能：

- 从另一个地址借用 DID；
- 由注册者填写任意 provider；
- 使用钱包当前登录身份代替 Genesis 地址身份；
- 由接收该资产的当前 owner 决定 provider。

Provider 身份只从 Genesis outpoint 对应地址解析。

---

## 8. RGB11 进入聪网的前置条件

一个 RGB11 Contract 要注册进入聪网，必须能够解析出合格 Provider DID。

注册前检查：

```text
GenesisOutpoint
      ↓
GenesisAddress
      ↓
PrimaryDID
      ↓
DID ownership
      ↓
DID length <= 10
```

任何一步失败，RGB11 都不能完成聪网正式注册。

特别是：

```text
PrimaryDID(GenesisAddress) == nil
```

或：

```text
len(PrimaryDID) > 10
```

时：

```text
SatoshiNet registration failed
```

不得在聪网 canonical AssetName 中 fallback 到：

- fingerprint；
- address 后 12 位；
- block height；
- tx index；
- ContractID 的缩写。

如果需要注册进入聪网，provider 地址应先完成合格的 DID Bind。

---

## 9. Canonical Ticker 格式

正式进入聪网后：

```text
<baseTicker>@<provider>
```

如果同一个 provider 已经注册过相同 baseTicker，则：

```text
<baseTicker>_<ordinal>@<provider>
```

### 9.1 第一个资产

```text
USDT@tether
```

不使用：

```text
USDT_1@tether
```

### 9.2 第二个资产

```text
USDT_2@tether
```

### 9.3 第三个资产

```text
USDT_3@tether
```

完整 AssetName：

```text
rgb11:f:USDT@tether
rgb11:f:USDT_2@tether
rgb11:f:USDT_3@tether
```

---

## 10. Ordinal 命名空间

Ordinal 的作用域是：

```text
(ProviderDID, BaseTicker)
```

而不是：

```text
(Address, BaseTicker)
```

例如：

```text
Provider = tether
BaseTicker = USDT
```

注册序列：

```text
1 -> USDT@tether
2 -> USDT_2@tether
3 -> USDT_3@tether
```

不同 provider 拥有独立命名空间：

```text
USDT@tether
USDT@alice
USDT@company
```

三者互不冲突。

---

## 11. Ordinal 分配规则

Ordinal 由聪网自动分配，发行者不能指定。

逻辑上：

```text
nextOrdinal(provider, baseTicker)
```

等于该 `(provider, baseTicker)` 命名空间中已经成功注册的最大 ordinal 加一。

如果此前不存在：

```text
ordinal = 1
```

如果已经存在：

```text
USDT@tether
```

则下一个：

```text
ordinal = 2
```

### 11.1 按聪网成功注册顺序分配

Ordinal 不依据：

- RGB Genesis 创建时间；
- Bitcoin block height；
- tx index；
- ContractID 字典序；
- 钱包发现顺序。

只依据 RGB11 Contract **成功注册进入聪网的共识顺序**。

例如 BTC 上已有 Contract A、B、C，但进入聪网的顺序是：

```text
B
A
C
```

则：

```text
B -> USDT@tether
A -> USDT_2@tether
C -> USDT_3@tether
```

### 11.2 Ordinal 永久不复用

一旦分配：

```text
USDT_2@tether
```

即使对应 Contract 后续：

- 被废弃；
- 停止使用；
- 资产归零；
- 不再被钱包展示；

`2` 也不得重新分配。

后续继续：

```text
USDT_4@tether
```

而不能重新使用：

```text
USDT_2@tether
```

---

## 12. BaseTicker 与 ordinal 后缀

因为 canonical ticker 使用：

```text
<baseTicker>_<ordinal>@<provider>
```

所以 `_数字` 是 RGB11 canonical ticker 中的 ordinal 保留形式。

为了避免：

```text
USDT_2@tether
```

既可能表示：

- `baseTicker = USDT` 的第二个资产；

又可能表示：

- `baseTicker = USDT_2` 的第一个资产；

进入聪网的 RGB11 `baseTicker` 不应以：

```text
_<positive integer>
```

结尾。

如果 RGB11 原始 ticker 命中该保留形式，聪网注册层应拒绝注册，而不是猜测含义。

内部状态始终分别保存：

```text
BaseTicker
Ordinal
ProviderDID
```

不能只通过解析最终字符串恢复全部状态。

---

## 13. ContractID 与 AssetName 映射

聪网建立并维护正式的：

```text
ContractID <-> AssetName
```

映射。

至少包括：

```text
ContractID -> AssetRegistration
AssetName  -> ContractID
```

其中注册数据建议包含：

```text
RGB11AssetRegistration {
    ContractID
    BaseTicker
    ProviderDID
    Ordinal
    CanonicalTicker
    AssetName
    GenesisOutpoint
    GenesisAddress
}
```

例如：

```text
ContractID X
    ->
BaseTicker      = USDT
ProviderDID     = tether
Ordinal         = 1
CanonicalTicker = USDT@tether
AssetName       = rgb11:f:USDT@tether
```

反向：

```text
rgb11:f:USDT@tether
    ->
ContractID X
```

---

## 14. ContractID 是唯一性最终依据

AssetName 负责用户可读性，但系统判断“是否为同一个 RGB11 资产”时必须以：

```text
ContractID
```

为最终依据。

例如两个钱包需要确认资产是否相同：

```text
compare ContractID
```

而不是：

```text
compare ticker
compare provider string only
```

如果 UI、调试、协议错误处理等场景需要进一步消歧，直接展示完整 ContractID。

不再生成：

```text
short fingerprint
short ContractID hash
```

作为另一套资产身份。

---

## 15. Provider Snapshot

Primary DID 只在 RGB11 首次成功注册进入聪网时解析一次。

例如注册时：

```text
GenesisAddress = A
PrimaryDID(A)  = company
```

则：

```text
Contract X -> ProviderDID = company
```

并生成：

```text
ABC@company
```

之后即使 Address A 把 Primary DID 改成：

```text
alice
```

已经注册的 Contract X 仍然：

```text
ABC@company
```

不得变成：

```text
ABC@alice
```

因此：

```text
PrimaryDID
```

是 RGB11 注册时的输入，不是资产展示时动态查询的属性。

---

## 16. DID 转移

Ordinals DID 的所有权可能随其对应 sat 转移。

例如：

```text
DID alice
Address A -> Address B
```

发生转移后，如果：

```text
Owner(alice) != Address A
```

则 Address A 原来的：

```text
Address A -> alice
```

Primary Bind 应视为失效。

Address B 如果需要将 `alice` 作为 Primary DID，应重新执行：

```text
BindPrimaryDID(Address B, alice)
```

并验证：

```text
Owner(alice) == Address B
```

---

## 17. DID 转移不得修改已有 AssetName

假设：

```text
A owns tether
A -> PrimaryDID tether
```

并已经注册：

```text
rgb11:f:USDT@tether
```

之后 `tether` DID 转移给 B。

原资产仍然：

```text
rgb11:f:USDT@tether
```

不得更名。

如果 B 重新 Bind：

```text
B -> tether
```

并发行新的 `USDT` Contract，则继续使用同一个 provider namespace：

```text
rgb11:f:USDT_2@tether
```

因为 ordinal scope 是：

```text
ProviderDID + BaseTicker
```

而不是 provider 当前地址。

---

## 18. 尚未进入聪网时的 SDK 本地资产名称

RGB11 在 BTC/RGB 层已经存在、但尚未正式注册进入聪网时，**不存在聪网协议级 AssetName**。

此时 SDK 可以为资产生成一个便于钱包和用户识别的本地名称：

```text
LocalRGB11AssetName
```

该名称只是本地 SDK metadata：

- 不进入聪网共识；
- 不写入 `ContractID <-> AssetName` 注册表；
- 不要求跨钱包、跨节点永久保持不变；
- 可以随着 DID Bind、SDK 命名规则或本地状态变化而重新生成；
- 不能作为资产的严格 identity 或持久化业务主键。

未进入聪网时，资产的严格 identity 始终是：

```text
ContractID
```

### 18.1 有合格 DID

如果 Genesis address 当前存在合格 Primary DID，SDK 可以将本地名称设置为：

```text
<baseTicker>@<providerDID>
```

例如：

```text
USDT@tether
```

此名称只是当前 SDK 根据现有状态生成的本地名称。

在正式注册进入聪网以前：

- 不分配聪网 ordinal；
- 不认为该名称已经占用聪网 namespace；
- 不建立正式 `ContractID -> AssetName` 映射；
- 可以在后续状态变化时修改。

### 18.2 没有合格 DID

如果 Genesis address：

- 没有 Primary DID；
- DID 长度超过 10；
- DID ownership 已失效；
- 或其他原因导致 DID 不合格；

SDK 本地名称使用：

```text
<baseTicker>@<GenesisAddress 最后 12 位>
```

例如：

```text
USDT@8k3u9qm7p4sx
```

其中：

```text
8k3u9qm7p4sx
```

是 Genesis Bitcoin address canonical string 的最后 12 个字符。

规则：

1. 不使用 fingerprint；
2. 不对地址后 12 位再次 hash；
3. 该名称只属于本地 SDK；
4. 不保证全局唯一；
5. 需要严格确认资产身份时直接使用完整 ContractID。

### 18.3 本地名称允许修改

进入聪网以前，RGB11 的本地资产名称可以修改。

例如最初没有合格 DID：

```text
USDT@8k3u9qm7p4sx
```

之后 Genesis address 完成：

```text
Address -> tether
```

Primary DID Bind，SDK 可以立即将本地名称重新生成成：

```text
USDT@tether
```

如果之后 Primary DID 再发生变化，只要资产仍未进入聪网，SDK 也可以继续按照当前规则重新生成本地名称。

这是允许的，因为此阶段名称只是 SDK 本地 metadata，不影响：

- RGB Contract 本身；
- ContractID；
- Bitcoin/RGB 状态；
- 其他钱包或节点；
- 聪网共识。

### 18.4 进入聪网是名称冻结点

只有 RGB11 正式注册进入聪网时，系统才第一次生成协议级 canonical AssetName：

```text
rgb11:f:<baseTicker>[_ordinal]@<providerDID>
```

注册成功后：

```text
ContractID <-> AssetName
```

进入聪网状态并永久冻结。

因此命名生命周期明确分为：

```text
未进入聪网
    ↓
SDK LocalAssetName
可修改 / 可重算
    ↓
注册进入聪网
    ↓
SatoshiNet Canonical AssetName
永久不可修改
```

---

## 19. SDK 本地名称的唯一性处理

SDK 本地名称不承担严格唯一性。

地址最后 12 位只是未注册资产的一种可读 fallback；不同 RGB Contract 理论上可能得到相同本地名称。

出现歧义时：

```text
LocalAssetName
+
Full ContractID
```

共同展示或校验。

例如：

```text
USDT@8k3u9qm7p4sx
ContractID: <full-contract-id>
```

不得通过追加 fingerprint 解决。

本地数据库、钱包资产对象和协议消息中的严格资产 key 必须继续使用 ContractID，而不是 LocalAssetName。

---

## 20. 正式注册流程

建议统一实现：

```text
RegisterRGB11Asset(contract)
```

流程如下。

### Step 1：验证 RGB11 Contract

完成现有 RGB11 Contract 有效性验证，并获取：

```text
ContractID
BaseTicker
GenesisOutpoint
```

### Step 2：检查是否已经注册

查询：

```text
ContractID -> AssetRegistration
```

如果存在，直接返回已经注册的 AssetName。

不得重新解析 provider 或重新分配 ordinal。

### Step 3：解析 Genesis Address

```text
GenesisOutpoint
    ->
GenesisAddress
```

如果不能得到 RGB11 支持的 Bitcoin address：

```text
registration failed
```

### Step 4：查询 Primary DID

```text
ProviderDID =
    PrimaryDID(GenesisAddress)
```

不存在：

```text
registration failed
```

### Step 5：验证 DID

必须同时满足：

```text
ValidOrdinalsDID(ProviderDID)
Owner(ProviderDID) == GenesisAddress
len(CanonicalDIDName) <= 10
```

否则：

```text
registration failed
```

### Step 6：确定 ordinal

查询：

```text
maxOrdinal(ProviderDID, BaseTicker)
```

生成：

```text
ordinal = max + 1
```

首次：

```text
ordinal = 1
```

### Step 7：生成 canonical ticker

如果：

```text
ordinal == 1
```

生成：

```text
<BaseTicker>@<ProviderDID>
```

否则：

```text
<BaseTicker>_<ordinal>@<ProviderDID>
```

### Step 8：生成 AssetName

```text
rgb11:f:<CanonicalTicker>
```

### Step 9：检查反向冲突

检查：

```text
AssetName -> ContractID
```

如果 AssetName 已经映射到另一个 ContractID：

```text
registration failed
```

这属于状态冲突，不能通过生成 fingerprint 自动规避。

### Step 10：原子写入注册状态

至少原子写入：

```text
ContractID -> AssetRegistration
AssetName  -> ContractID
(provider, baseTicker) -> maxOrdinal
```

注册成功后：

```text
ContractID
ProviderDID
BaseTicker
Ordinal
AssetName
```

均不可修改。

---

## 21. Bind 与 RGB11 注册的时序

推荐时序：

```text
1. 用户拥有 Ordinals DID
2. DID 所在地址执行 Primary DID Bind
3. Bind 校验 DID <= 10
4. RGB11 Genesis outpoint 使用同一地址
5. RGB11 Contract 注册进入聪网
6. 聪网解析 Primary DID
7. 分配 ordinal
8. 建立 ContractID <-> AssetName 映射
```

如果 RGB Contract 已经在 BTC 上存在，也可以之后再完成 Bind，只要在**正式注册进入聪网时**满足：

```text
GenesisAddress
    ==
Owner(PrimaryDID)
```

即可。

---

## 22. AssetName 不可变性

以下关系一旦注册成功全部不可变：

```text
ContractID -> AssetName
ContractID -> ProviderDID
ContractID -> BaseTicker
ContractID -> Ordinal
```

之后以下事件都不能修改历史 AssetName：

- Primary DID 被修改；
- DID 被转移；
- DID profile metadata 修改；
- Genesis UTXO 被花费；
- RGB asset 后续发生转移；
- provider 地址不再持有该 DID；
- 钱包显示规则升级。

---

## 23. Genesis UTXO 后续花费

Genesis outpoint 只用于注册阶段确定 provider。

注册完成以后：

```text
Genesis UTXO spent
```

不影响：

```text
ProviderDID
AssetName
ContractID mapping
```

因为 provider 已经 snapshot 到聪网注册状态。

---

## 24. 不使用 fingerprint

新规则启用后，RGB11 命名逻辑中不再存在 fingerprint fallback。

应逐步移除或停止使用类似：

```text
NewCanonicalAssetNameWithFingerprintLength(...)
```

用于 RGB11 AssetName 构造的路径。

需要严格身份时：

```text
use ContractID
```

需要用户名称时：

```text
use AssetName
```

需要未注册资产的 SDK 本地名称时：

```text
use ticker@provider
```

或：

```text
use ticker@address-last-12
```

该名称允许在正式注册前重新生成和修改。

不再引入介于 ContractID 和 AssetName 之间的 fingerprint identity。

---

## 25. 无旧命名兼容逻辑

由于 RGB11 尚未正式发布，本设计不需要：

- naming v1 / v2；
- 激活高度；
- `ticker@fingerprint` 历史资产保留；
- 旧 AssetName alias；
- 旧 fingerprint mapping；
- 自动迁移；
- 双格式解析。

实施时直接以本设计替换当前 RGB11 AssetName 生成逻辑。

测试数据库或开发环境中的旧 RGB11 名称可以直接重建，不应为未发布数据增加长期兼容代码。

---

## 26. 必要索引

聪网至少维护以下索引。

### 26.1 ContractID -> Asset

```text
ContractID -> RGB11AssetRegistration
```

### 26.2 AssetName -> ContractID

```text
AssetName -> ContractID
```

### 26.3 Provider/Ticker -> maxOrdinal

```text
(ProviderDID, BaseTicker) -> MaxOrdinal
```

### 26.4 Address -> Primary DID

```text
BitcoinAddress -> PrimaryDID
```

### 26.5 DID -> current owner

该关系由 Ordinals DID / L1 indexer 提供：

```text
DID -> BitcoinAddress
```

---

## 27. 注册失败条件

以下情况必须拒绝 RGB11 正式注册进入聪网。

### 27.1 Contract 已注册但状态冲突

同一 ContractID 不允许生成第二个 AssetName。

### 27.2 Genesis address 无法解析

无法从 Genesis outpoint 得到受支持的 Bitcoin address。

### 27.3 没有 Primary DID

```text
PrimaryDID(GenesisAddress) == nil
```

### 27.4 DID 超过 10 位

```text
len(CanonicalDIDName) > 10
```

### 27.5 DID 已不属于 Genesis address

```text
Owner(ProviderDID) != GenesisAddress
```

### 27.6 BaseTicker 与 ordinal 保留格式冲突

baseTicker 使用聪网 RGB11 canonical ticker 保留的 `_数字` 后缀格式。

### 27.7 AssetName 已属于另一个 ContractID

```text
AssetName -> ContractID A
```

但当前正在注册：

```text
ContractID B
```

则视为状态异常并拒绝注册。

---

## 28. 安全模型

Provider 身份建立在三层关系之上：

```text
RGB Genesis Outpoint
        ↓
Bitcoin Address
        ↓
Ordinals DID Ownership
        ↓
SatoshiNet Primary DID Bind
        ↓
RGB11 Provider
```

攻击者不能仅填写：

```text
provider = tether
```

而必须实际满足：

```text
GenesisAddress owns DID "tether"
```

且：

```text
PrimaryDID(GenesisAddress) = tether
```

同时 DID 长度必须：

```text
<= 10
```

因此 RGB11 可读名称中的 provider 具有 Bitcoin 原生所有权依据。

---

## 29. Bind 是聪网通用身份能力

Primary DID Bind 不应仅服务于 RGB11。

它定义的是：

```text
Bitcoin Address -> Primary Human-Readable Identity
```

后续可以被以下模块复用：

- Wallet；
- P2P Message；
- DKVS；
- Service Registry；
- Smart Contract deployer；
- AI Agent；
- RGB11 provider。

RGB11 只是该身份机制的重要使用场景之一。

---

## 30. 典型示例

### 30.1 首次发行

```text
Genesis Address:
bc1p...

Address owns DID:
tether

PrimaryDID:
tether

BaseTicker:
USDT
```

结果：

```text
rgb11:f:USDT@tether
```

映射：

```text
ContractID X
    <->
rgb11:f:USDT@tether
```

### 30.2 同 provider 第二个同名资产

已有：

```text
rgb11:f:USDT@tether
```

再注册另一个 `USDT` Contract：

```text
rgb11:f:USDT_2@tether
```

### 30.3 不同 provider

```text
rgb11:f:USDT@tether
rgb11:f:USDT@alice
```

互不冲突。

### 30.4 Primary DID 修改

最初：

```text
Address A -> alice
```

注册：

```text
rgb11:f:ABC@alice
```

之后：

```text
Address A -> company
```

再注册新资产：

```text
rgb11:f:XYZ@company
```

旧资产继续：

```text
rgb11:f:ABC@alice
```

### 30.5 DID 转移

已有：

```text
rgb11:f:USDT@tether
```

`tether` DID 从 A 转移到 B。

原资产名称保持不变。

B 重新 Bind `tether` 后，再发行同 ticker：

```text
rgb11:f:USDT_2@tether
```

### 30.6 未进入聪网且没有合格 DID

Genesis address：

```text
bc1pxxxxxxxxxxxxxxxxxxxxx8k3u9qm7p4sx
```

无合格 Primary DID。

钱包临时显示：

```text
USDT@8k3u9qm7p4sx
```

严格身份：

```text
ContractID = <full ContractID>
```

如果后续该地址 Bind：

```text
tether
```

则临时 UI 可以改为：

```text
USDT@tether
```

最终进入聪网后，再由聪网确定是否需要 ordinal。

---

## 31. 实现建议

### 31.1 RGB11 命名函数拆分

建议不要继续把：

```text
ContractID -> fingerprint -> AssetName
```

封装在一个函数中。

拆为：

```text
ResolveRGB11Provider(...)
AllocateRGB11Ordinal(...)
BuildRGB11CanonicalTicker(...)
BuildRGB11AssetName(...)
RegisterRGB11Asset(...)
BuildTemporaryRGB11DisplayName(...)
```

### 31.2 AssetName 不依赖动态 DID 查询

`BuildRGB11AssetName` 应使用已经确定的：

```text
BaseTicker
ProviderDID
Ordinal
```

而不是每次展示时重新查询：

```text
Address -> PrimaryDID
```

### 31.3 注册操作需要原子性

以下状态必须在同一逻辑事务中提交：

```text
ContractID -> AssetName
AssetName -> ContractID
(provider, ticker) -> MaxOrdinal
```

避免两个并发注册得到相同 ordinal。

### 31.4 SDK 本地名称不作为资产主键

钱包和 PWA 对未注册 RGB11 的内部 identity 必须继续使用 ContractID。

例如：

```text
asset.key = ContractID
asset.localName = USDT@8k3u9qm7p4sx
```

而不是：

```text
asset.key = USDT@8k3u9qm7p4sx
```

`asset.localName` 可以在 DID Bind 或命名规则变化后重新计算。

只有注册进入聪网以后，才增加稳定的：

```text
asset.assetName = rgb11:f:USDT@tether
```

并将其视为不可变的聪网 canonical AssetName。

---

## 32. 必要测试

### 32.1 DID Bind

覆盖：

- DID 长度 1；
- DID 长度 10；
- DID 长度 11，必须拒绝；
- DID 不属于地址；
- 地址拥有多个 DID；
- Primary DID 更新；
- DID 转移后旧 Bind 失效。

### 32.2 Provider 解析

覆盖：

- Genesis address 有有效 Bind；
- 无 Bind；
- Bind DID > 10；
- DID ownership 已改变；
- Genesis address 与 DID owner 不一致。

### 32.3 Ordinal

验证：

```text
1 -> USDT@provider
2 -> USDT_2@provider
3 -> USDT_3@provider
```

以及：

- 不同 provider 独立计数；
- 不同 baseTicker 独立计数；
- ordinal 永久不复用；
- 并发注册不能分配相同 ordinal。

### 32.4 ContractID 映射

验证：

- ContractID 只能对应一个 AssetName；
- AssetName 只能对应一个 ContractID；
- 重复注册同 ContractID 返回原映射；
- 不会重新解析 provider；
- 不会重新分配 ordinal。

### 32.5 Provider Snapshot

验证：

- Primary DID 修改后旧资产不变；
- DID 转移后旧资产不变；
- Genesis UTXO 花费后旧资产不变。

### 32.6 SDK 本地资产名称

验证：

```text
ticker@address-last-12
```

并验证：

- 不生成 fingerprint；
- 本地名称不进入聪网 mapping；
- 有合格 DID 后可以从 `ticker@address-last-12` 修改为 `ticker@provider`；
- Primary DID 再变化时，只要尚未注册进入聪网，本地名称仍可继续修改；
- 本地名称修改不影响 ContractID；
- 严格 identity 始终仍为 ContractID；
- 一旦注册进入聪网，canonical AssetName 不再随本地名称变化。

### 32.7 不存在旧规则兼容

测试代码中不应要求：

- 解析旧 fingerprint AssetName；
- 迁移旧 AssetName；
- 保留 naming version；
- fallback 到 fingerprint。

---

## 33. 核心不变量

实现中应维护以下 invariants。

### Invariant 1

```text
ContractID -> exactly one AssetName
```

### Invariant 2

```text
AssetName -> exactly one ContractID
```

### Invariant 3

注册时：

```text
GenesisAddress == Owner(ProviderDID)
```

### Invariant 4

注册时：

```text
PrimaryDID(GenesisAddress) == ProviderDID
```

### Invariant 5

Bind 时：

```text
len(CanonicalDIDName) <= 10
```

### Invariant 6

```text
(ProviderDID, BaseTicker, Ordinal)
```

全局唯一。

### Invariant 7

Ordinal 单调递增且永久不复用。

### Invariant 8

注册完成后：

```text
AssetName
ProviderDID
BaseTicker
Ordinal
```

不可修改。

### Invariant 9

fingerprint 不参与新 RGB11 AssetName。

### Invariant 10

未注册资产的 SDK LocalAssetName 可以修改，不是唯一身份，ContractID 才是。

### Invariant 11

只有正式注册进入聪网以后，AssetName 才成为不可变 canonical name。

---

## 34. 最终规则摘要

### 正式进入聪网

严格身份：

```text
ContractID
```

聪网名称：

```text
rgb11:f:<ticker>[_ordinal]@<providerDID>
```

例如：

```text
rgb11:f:USDT@tether
rgb11:f:USDT_2@tether
```

Provider：

```text
GenesisOutpoint
      ↓
GenesisAddress
      ↓
PrimaryDID
```

必须满足：

```text
Owner(PrimaryDID) == GenesisAddress
len(PrimaryDID) <= 10
```

聪网保存：

```text
ContractID <-> AssetName
```

### 尚未进入聪网

尚未进入聪网时没有协议级 AssetName，只有 SDK 本地名称。

如果有合格 DID，可设置为：

```text
USDT@tether
```

如果没有合格 DID：

```text
USDT@<GenesisAddress 最后 12 位>
```

例如：

```text
USDT@8k3u9qm7p4sx
```

该本地名称：

- 可以修改；
- 可以随 DID Bind 变化而重新生成；
- 不保证唯一；
- 不进入聪网资产映射。

需要唯一识别时：

```text
直接使用完整 ContractID
```

只有成功注册进入聪网时，才生成并冻结 canonical AssetName。

### 明确取消

新规则启用后不再使用：

```text
ticker@fingerprint
```

也不再引入任何 fingerprint fallback 或旧 RGB11 名称兼容层。

---

## 35. 设计结论

RGB11 最终采用：

```text
ContractID
```

作为严格资产身份，

采用：

```text
ticker[_ordinal]@providerDID
```

作为聪网中的人类可读资产名称。

Provider 通过 Genesis outpoint 所属 Bitcoin 地址的 Primary Ordinals DID Bind 确定；DID 必须不超过 10 个字符才能在聪网上 Bind。

同一 provider 第一次使用 ticker：

```text
USDT@tether
```

第二次：

```text
USDT_2@tether
```

第三次：

```text
USDT_3@tether
```

聪网建立稳定的：

```text
ContractID <-> AssetName
```

映射。

对于尚未进入聪网且没有合格 DID 的 RGB11 资产，钱包临时显示：

```text
ticker@address-last-12
```

需要严格身份时直接使用完整 ContractID。

该设计彻底移除 fingerprint 作为 RGB11 用户名称或中间身份的必要性，同时保留 ContractID 的严格唯一性，并通过 Ordinals DID 为资产名称提供清晰、稳定且具有发行者语义的人类可读身份。
