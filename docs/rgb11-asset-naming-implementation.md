# RGB11 资产名称：SDK 实现与验收说明

本文记录 Wallet SDK / PWA 对 RGB11 命名设计的实现范围。

## 核心身份

RGB11 严格身份始终是完整 ContractID。SDK 内部资产 key 使用完整 ContractID 的无损表示，不再使用 fingerprint，也不再把用户可修改名称作为余额、证明、转账或快照主键。

## 未进入聪网前的本地名称

RGB11 尚未正式注册进入聪网时，名称只是 SDK 本地 metadata，可以修改。

- 能可靠解析 Genesis address 时，默认显示 ticker@<address-last-12>；
- 无法可靠解析 Genesis origin 时，显示完整 ContractID；
- 本地名称修改不改变 ContractID、余额、proof、UTXO 或 RGB contract。

SDK 提供 SetRGB11LocalAssetName(contractID, name)。本地名称不是 canonical SatoshiNet AssetName。

## Primary DID

Primary DID 存在 DKVS 的 /personal/<account_id>/primary_did，value 只有 DID 字符串。

SDK helper 为 PutPrimaryDID(...) 和 GetPrimaryDID(...)。

规则：

- 可 Bind DID 限定为 1–10 位小写 ASCII `a-z0-9._-`，这是 Ordinals DID 的 DKVS-safe 子集；
- 不截断、不做 fingerprint；
- 不保存 inscription_id、owner address、owner UTXO 等 L1 Indexer 已可查询的信息；
- SatoshiNet DKVS 写入端通过 L1 DID resolver 校验当前 ownership；
- Primary DID 可以修改；
- 仅设置 Primary DID 不会把本地 RGB11 名称冻结成 canonical name。

## 聪网上的 canonical registry

RGB11 canonical registry 由 SatoshiNet DKVS 保存：

    /rgb11/<providerDID>/<baseTicker>/<ordinal>

value 为最小 33-byte 编码：1 byte 原资产类型（FT 为 `f`，NFT 为 `o`）+ 32-byte ContractID。使用共享 Indexer 的 `ASSET_TYPE_FT` / `ASSET_TYPE_NFT` 常量；`n` 是名称资产类型，不是 NFT，不作为 RGB11 类型兼容。类型只用于恢复完整 SatoshiNet AssetName，不参与 ordinal namespace。

例如：

    /rgb11/tether/usdt/1 -> <ContractID A>
    /rgb11/tether/usdt/2 -> <ContractID B>

对应：

    rgb11:f:usdt@tether
    rgb11:f:usdt_2@tether

NFT 示例为 `rgb11:o:art@artist`。

Wallet SDK 只读取该 registry，不直接创建 /rgb11 记录。GetRGB11Registration(providerDID, ticker, contractID) 通过普通 DKVS read API 查询已同步的 canonical registration，并在本地验证整个响应：记录签名、可信注册权威、provider/ticker 范围、完整 ContractID、永久记录约束以及重复 key/ContractID。

默认可信根与节点一致，来自本地网络配置的 bootstrap/default CoreNode 公钥，而不是 HTTP 响应。GetRGB11RegistrationWithVerifier(...) 支持显式指定可信注册策略；该策略只能来自可信本地配置，nil 策略直接拒绝。测试使用独立测试权威，不把测试公钥加入生产可信根。

注册记录要求 Seq=1、TTL=0、无 tombstone、无 FeeProof。即使某条匹配记录合法，也不能提前返回并忽略同一响应中的其他非法记录。签名认证不证明服务端没有遗漏记录，也不替代后续 STP 对 Genesis/provider 所有权的验证。

## Transcend/STP 边界

本 PR 不实现 RGB11 的 Transcend deposit/withdraw。

后续首次 RGB11 进入聪网时，Transcend/STP 应完成：

1. 验证完整 RGB ContractID 与 Genesis；
2. 读取 Genesis address 所属账户选择的 Primary DID；
3. 使用 L1 Indexer 确认 DID 当前 owner 与 Genesis address 一致；
4. 通过 CoreNode 内部注册路径在 DKVS 分配下一个 ordinal；
5. 获得 canonical AssetName；
6. 后续聪网上的通道合约、余额和交易统一使用 canonical AssetName。

命名事实不需要重复塞进 transcend.tc deploy payload。

## DKVS 与区块重放

RGB11 canonical naming 是 DKVS 状态，不属于 SatoshiNet block index state。

节点恢复预期顺序：DKVS sync -> RGB11 registry available -> block/index replay。

区块重放不会重新生成名称、重新分配 ordinal 或重新查询历史 DID owner。

## 已实现的 SDK 行为

- 删除 RGB11 命名用 fingerprint；
- ContractID 与本地显示名称分离；
- Genesis address 默认本地名称；
- 未注册名称允许修改；
- Primary DID DKVS helper；
- DID <= 10 校验；
- provider / ticker / ordinal 名称构造与解析测试；
- SDK 可认证读取 SatoshiNet DKVS 中已有 RGB11 registration；
- PWA 资产选择、去重和转账继续使用稳定 asset key，而不是本地显示名称。

## SDK e2e

SDK 测试覆盖两个层次；测试代码存在不等同于所有 connected core 场景已执行，执行结果以对应提交的 CI/验收记录为准。

1. connected core e2e：真实发行 RGB11、默认本地名称、修改本地名称、balance/key 不变、真实 DKVS Primary DID 写入/读取、11 位 DID 与非 owner DID 拒绝、Primary DID 修改后本地名称仍可修改；
2. SDK-DKVS registry e2e：本地 HTTP 测试适配器连接真实 SatoshiNet DKVS Indexer，真实 SatsNetDKVSClient 发出请求并验证结果，不依赖公网、真实钱包或私有测试开关。

第二层包括 ordinal 1–12、幂等注册、FT/NFT 共享编码、路径和全量快照恢复、Index­er 实例重建后读取，以及未签名、陌生签名者、类型篡改、错误 provider/ticker、重复身份、合法匹配后的恶意尾记录、可信签名下非法 TTL/Seq 的拒绝。Index­er 重建使用同一内存数据库，不宣称磁盘崩溃恢复或完整生产 RPC 验收。

STP/Transcend RGB deposit/withdraw e2e 留到对应功能实现时补充。

## 不兼容旧 RGB11 命名

RGB11 尚未发布，因此不保留 ticker@fingerprint、naming v1/v2、fingerprint fallback、旧名称迁移或双格式解析。测试数据可以直接重建。

## 当前阶段边界

Wallet PR 与 SatoshiNet RGB11 naming PR 是配套变更。本阶段完成 SDK 本地名称、Primary DID、DKVS canonical registry 基础以及 SDK 对 registry 的读取；Transcend/STP RGB 资产进出聪网仍属于下一阶段。
