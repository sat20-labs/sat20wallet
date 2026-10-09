# 共享 fake indexer

从 `transcend/stp/restclient_test.go` 迁入的测试组件，供 Transcend 的进程内虚拟网络和 SDK E2E 的 L1 HTTP 夹具共用。生产钱包、生产节点不引用这个包。

- `Client` 提供原有 indexer 客户端接口；资产解析与分配只保留一份，包含 ORDX、BRC20 铭文与地址账本、Runestone 分配，以及原来的 L2/DKVS 模拟能力。
- `NewTestNetworks()` 返回 Transcend 原有的 L1/L2 初始夹具，两个网络共享同一 `State`。
- `NewNetwork()` 返回空 UTXO 网络；SDK HTTP 夹具通过 `SeedOutput()` 导入明确声明的初始持有量。有效 BRC20 分配代表可转移铭文，`Invalid` 表示已经消费或铸造的载体。
- `State` 只在一个夹具内部共享。独立 SDK 浏览器夹具不共享脚本、ticker、BRC20 余额或交易状态。

SDK 的 `e2e/pos_pwa_l1_test.go` 保留 HTTP、真实交易验签、重复输入和双花检查、矿工费中不能丢失绑定资产的检查、真实序列化字节及 Bitcoin 区块证据。协议模拟先在 `Network.Clone()` 上执行，成功才接纳；预检和失败不写回原模型。交易确认仍由 `ConfirmPending()` 显式推进；BRC20 已确认余额使用确认时的模型快照。

Transcend 保留 `TestNodeClient` 的 STP peer 适配器和 RGB 证据适配器，通过嵌入共享 `Client` 使用相同底层模型。原有的读高度推进区块行为保留在 Transcend 客户端中，SDK HTTP 查询不会调用该行为。

这是确定性测试夹具，复用原有 Transcend 支持范围，不代表生产 indexer 的完整协议校验或完整发行索引。不要以 fake 的协议接受结果代替真实 indexer 验收。

本轮仅编译检查，没有执行业务测试。已增加 `TestPOSPWAL1IndexerSharedBRC20AndRunes`，验证真实签名交易通过 HTTP 预检／广播／确认后得到独立预期余额，BRC20 已消费载体不会再次成为可转移铭文，未知 Rune ID 拒绝后可继续提交，独立夹具互不污染。原有 BTC／ORDX 验签、资产偏移、双花及重播场景继续保留。
