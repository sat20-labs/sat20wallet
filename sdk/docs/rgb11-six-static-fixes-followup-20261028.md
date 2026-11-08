# RGB11 / DKVS 静态复核窄修复续项（2026-10-28）

本记录接续 `rgb11-six-static-fixes-20261028.md`。此轮只编辑 SDK 源码与回归测试源码，并完成格式及补丁静态检查；**尚未构建或执行测试**。第④项未决 ACK 按依赖组隔离在上一候选已闭合，此轮没有重做。

| 复核项 | 本轮候选改动 | 回归源码 |
| --- | --- | --- |
| ① viewer 明文边界 | 账户数据 blob 的外层 `Metadata` 非空时隐藏原始值，并标明 `plaintext_metadata`；不以关键词决定放行 | `TestAccountDKVSInspectionHidesEncryptedBlobWithPlaintextMetadata` |
| ② WASM 同一快照 | `jsDB.NewReadView` 在一个 IndexedDB readonly transaction 中收集数据，后续只读已固定的字节；viewer 只要求真实读视图能力，不虚构磁盘同步能力 | 既有 viewer 跨页/并发缓存写测试；新增浏览器 IndexedDB 单事务与后续读写隔离测试，待统一验收 |
| ③ IFA 混合载体 | 对已声明本地找零 output 上的未知 seal 拒绝证明；其他载体仍可忽略 | `TestRGB11LocalChangeAllocationsAllowsDistinctSealsOnMixedCarrier` 的额外分支 |
| ⑤ ACK 本地收尾 | durable ACK 的直接 outbox Delete，以及账户 ACK 原子 batch 的 Delete/Flush 失败，返回可退避重试的 committed/pending 错误 | `TestDKVSFinalACKDeleteFailureBackgroundRetriesWithoutWatchChange`、`TestAccountManagedACKBatchFailureBackgroundRetriesWithoutWatchChange` |
| ⑥ 公共引用门槛 | remote-import marker 重放在导入/删除标记前，锁外核验目标包的独立公共引用；再核对当前账户身份、generation 和原标记 | `TestRemoteImportMarkerKeepsCommittedTargetWhenPublicReferenceIsInvalid`；`TestRemoteImportMarkerRetriesAfterPublicContractReturns` 用真实发行的恢复包与签名公共记录覆盖失联、保留标记、恢复后继续重放 |

统一验收仍须编译 SDK 原生与 WASM，并运行上述定向测试及相关账户/RGB/DKVS 普通与 race 回归。该候选没有改变 RGB11 协议 AssetName、nativeproof 或链上部署格式；短正式名另待 12hex 规则和协议接线定案。

只读取证：当前 transcend 合约部署的脚本编码在 `sdk/wallet/contract.go:900-905` 只有模板名、AssetName、起止块高；市场提交参数在 `market_satsnet/app/transcend/create/page.tsx:156-172` 也没有独立完整 ContractID 字段。现有 RGB11 资产名仍含完整 ID，因此现格式间接携带它；改为短名后，不能把该字段当作独立完整 ID 承诺。`transcend/stp/contract_deploy.go:343-350,435-459` 以合同 URL 查重，URL 由通道地址与合约名组成（`sdk/wallet/contract.go:1351-1355`），合约名由 AssetName 与模板名组成（`sdk/wallet/contract_swap.go:65-70`）；这没有证明跨通道、同块并发时短名只能绑定一个完整 ID。`satoshinet/anchortx/nativeproof/validate.go:62,90-96` 从 proof 提取完整 ID 并要求它等于当前 AssetName ticker；`anchortx/anchortx.go:240-245` 传入的只有 source 与 Bitcoin resolver，未见历史部署绑定读取接口。服务端 splicing-in/expand 共用入口在 `transcend/stp/handler.go:805-855` 先查公共登记再收集 RGB 证明，之后按请求名及证明入账；SDK 客户端在 `sdk/wallet/channel_operations.go:533-540`、`channel_expanding_client.go:303-309` 也检查现有登记。当前登记实现 `transcend/stp/rgb11_registration.go:74-96` 仍直接把 ticker 当完整 ID；因此已查入口对现有 fullID 模式有检查，但不能据此宣称短名切换后已具有不可绕过的 `canonical ↔ fullID` 绑定或全局唯一性。此处是当前接口证据，不构成新共识索引的实施决定。
