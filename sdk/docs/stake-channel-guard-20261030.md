# 通道地址与质押地址互斥：候选与验收状态

本批只按目标地址检查：首开通道前查询 L2 当前质押归属；质押交易前检查本地活动通道及首开预约。查询失败明确返回错误。服务端 STP 在接受首开及签名前复核质押，在 STAKE 类型 ASCEND 准入及执行前复核通道。关闭后的通道及既有通道重开/恢复不新增限制。没有新增持久占位、锁框架或 RPC；同时请求和服务重启窗口未宣称原子互斥。

修改范围：`wallet/channel_manager.go`、`wallet/channel_operations.go`、`wallet/action_remote.go`、新增 `wallet/channel_stake_guard_test.go`；服务端对应 `transcend/stp/funding.go`、`stp/action_remote.go`，已批准的场景改写在 `stp/manager_test.go`。其他 SDK/PWA dirty 未动。

验证：SDK 两个新单测在先前候选上执行成功（命令 `go test -mod=readonly -count=1 -timeout=3m -run '^(TestStakeAddressGate|TestChannelStakeGate)$' ./wallet`，退出 0，日志见同目录 `data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/stake-gate-sdk-20261030.log`）。之后补了“质押状态存在但地址缺失须拒绝”的小修和断言，**当前最终 SDK 文件尚未重跑**。transcend `go test -mod=readonly -run '^$' ./stp` 在默认 Go 缓存读取处沙箱拒绝，未进入编译；正常升级申请也被自动审批拒绝，理由错误地沿用了旧只读/不测试限制。未换缓存或渠道重跑。最新源码只完成 `gofmt` 与限定 `git diff --check` 静态检查，均无输出。

发布门禁仍需在可获准的正常执行路径上完成当前源码的 SDK 定向单测、transcend 编译及 `TestStakeToBeMiner`、`TestCoreNode`/`TestAll`。旧格式的通道数据恢复与其他发布门禁不由这两个新单测代表。实际可复现输入哈希和被拒命令见同批 receipt。
