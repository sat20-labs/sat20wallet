# RGB11 资产名称：实现范围与验收说明

本文对应 `rgb11-asset-naming-design.md`，记录本 PR 的实际范围，不替代设计规范。

## 已实现的 SDK 行为

- 删除 RGB11 资产命名用的 fingerprint 生成、长度扩展、匹配与展示字段，不保留旧命名兼容。
- `NewContractAssetKey` 使用完整 32 字节 ContractID 的无损十六进制编码，作为现有 `indexer.AssetName` 类型中的 SDK 内部投影 key。它不是聪网注册名称；十六进制编码没有截断，也不是另一个 hash。完整的 RGB ContractID 仍单独存储和返回。
- 余额、验证回执、分配证明、转账与快照恢复使用该不受本地改名影响的 key。
- `RegisterRGB11TickerInfo` 在导入时从经过匹配的 Contract 对象中的 Genesis 分配解析原始地址，默认本地名称为 `ticker@地址后12位`。不会使用接收钱包地址代替 Genesis 地址。
- 原始 UTXO 已花费时通过原始交易解析，并校验 txid、vout 和地址脚本。所有可见初始分配必须能确定同一个地址；隐藏、地址不一致或证据缺失时保留完整 ContractID 作为显示，不伪造 provider。
- `Manager.SetRGB11LocalAssetName(ContractID, name)` 允许修改未注册资产的 SDK 本地名称。名称按钱包/账户 scope 和完整 ContractID 保存，不重写余额、回执或 RGB Contract。
- `GetRGB11State` 返回 `asset_key`、`ticker`、`contract_id`、`genesis_address`、`naming_status`。展示读取不发起新的 Bitcoin 网络查询。
- PWA 的选择、去重和转账 key 与本地名称分离；两个同名资产仍按完整身份区分。
- DID 长度（1–10 个 Unicode 码点）、canonical 名称格式、地址一致性、保留 ordinal 后缀，以及 `ticker[_ordinal]@provider` 格式已有独立校验/构造函数和测试。

项目原有 ticker 标准化继续保留：例如 `USDT` 的 canonical ticker 部分为 `usdt`。`protocol` 和 `type` 的语义没有改变。其他用途的指纹，例如账户、BIP32 或修复操作指纹，不属于资产命名，未被删除。

## 尚未完成的协议端能力——本 PR 仍为 Draft

本 PR **没有完成**以下聪网共识能力，也没有用本地字典冒充链上注册表：

1. 真正的 `Address -> Primary Ordinals DID` Bind 交易和状态转换，及链上执行的 DID ≤10 规则。
2. 针对可验证 L1 状态的 DID 当前所有权检查、Bind 失效处理及 SDK 的合格 DID 自动解析。
3. RGB11 首次正式注册的授权校验和共识入口。
4. 原子、不可变的 `ContractID <-> AssetName` 双向注册表。
5. `(ProviderDID, BaseTicker)` 下按成功注册共识顺序分配、永不复用的 ordinal，以及注册后的 provider/name 冻结。
6. STP/Transcend 的正式名称映射消费，以及浏览器 WASM/界面的手动改名入口。当前已提供 Go SDK 改名方法。

因此当前实际默认显示路径是地址后缀，不是已验证的 `ticker@DID`。`BuildLocalDisplayName` 的 DID 参数和 `BuildRegisteredAssetName` 只是调用方拥有可信 Bind/注册状态之后使用的构造能力，调用这些函数本身不构成注册或授权。

当前 SDK 仍保持 RGB11 的 L1-only 限制。外部导入的 metadata 不能通过设置 `canonical_name` 冒充聪网正式注册；这类输入会被拒绝。本地状态的 `canonical_name` 为空，`verified=false`。

现有 `CONTENT_TYPE_BINDREFERRER` 是推荐人绑定，不能作为 Primary DID Bind 复用。DID 名称所有权也不等于现实机构/商标认证。

## 注册授权的安全边界

Genesis outpoint 的地址和 DID owner 一致，是必要的关系检查，但**单独引用某地址的 outpoint 并不能证明创建 Genesis 的人控制该地址**。协议端实现还必须校验注册行为的地址控制权/授权，并将它与完整 ContractID、Genesis 证据、有效 Bind 和网络绑定，防止第三方提交指向他人 outpoint 的 Genesis 抢占 namespace 或消耗 ordinal。

本 PR 的 `ValidateProviderBinding` 只检查已经验证过的输入之间的关系，不能接受未经验证的 RPC/用户字符串作为授权证明。正式注册逻辑不得在共识执行期间依赖各节点独立、可变化的远端 HTTP 查询。

## 测试方法

GitHub Actions 工作流：`.github/workflows/rgb11-asset-naming.yml`。

依赖固定为以下提交，按 SDK 的本地 replace 布局检出到兄弟目录：

| 仓库 | 提交 |
|---|---|
| indexer | `1d259d64ee3603bbf1ff8189dc1bbada76e2f8b3` |
| rgb11 | `76eab6762fcc75ad2213a6e1cdcf896e483bf0ab` |
| satoshinet | `e6521c11cd0adbe318133cd9f930db960ee80163` |

测试包括名称/完整身份回归、DID 边界、provider 地址匹配、ordinal 格式、已花费 Genesis 输出、交易证据替换、隐藏或歧义分配、账户隔离、本地改名后的余额/回执不变、真实发行/导入流程及 PWA 装饰器行为。涉及钱包和 Bitcoin 的测试使用本地构造数据及明确的 mock evidence，不操作真实钱包。

原始 RED 证据：Actions run `36988454439`，`TestRGB11NamingIdentitySurvivesLocalRename` 实际失败，显示同一 ContractID 在修改 ticker 后其 key 从 `usdt@xkvistbj` 变成 `renamed-locally@xkvistbj`。初始实现版本 `5d64efb74ebcb98ec9b43c362fb5d4c3fcd971e7` 的 Actions run `36990697189` 已通过包测试、根包编译和 WASM 编译。最终版本的完整测试状态应以 PR checks 和 PR 验收记录为准，不将上述早期结果当作整个设计已经交付。

## 已发现的既有 CI 问题

`rgb11-address-scheme-a.yml` 的目录检查仍硬编码根目录只能有 `rgb11_api.go`、`rgb11_dkvs.go` 和 `rgb11_manager.go` 三个实现文件。PR 基线 `22d658c0ea34b35ad325a96f37c26344030fdc2a` 已存在更多 RGB11 生命周期和修复文件。该工作流因此在测试执行之前失败。本 PR 没有增加新的根目录非测试实现文件，也没有关闭该检查来制造全绿结果。

## 合并条件

在要求完整执行原设计的情况下，本 PR 不应作为已完成版本合并。需要补齐协议端 Bind/注册/授权和映射消费，并完成跨模块测试。当前变更可作为 SDK 命名与身份分离的可审查实现基础。


## Transcend registration descriptor

For RGB11 `transcend.tc` deployment, the SDK keeps a local descriptor object containing ContractID, base ticker, Genesis outpoint, and Genesis address. The on-chain binary suffix intentionally omits a second copy of ContractID: the signed contract's AssetName already contains the full ContractID as `rgb11:<type>:<ContractID>`.

The serialized suffix contains only:

```text
rgb11-reg-v1
baseTicker
genesisOutpoint
genesisAddress
```

Decoding recovers ContractID from the signed AssetName and validates the remaining descriptor fields. This avoids redundant bytes and keeps the signed channel-contract deployment within SatoshiNet's OP_RETURN payload limit.
