# RGB11 本地候选执行检查点（2026-10-30）

本轮候选把前台 consignment Accept、播前 Prepare receive、前台及 L1 后台 Refresh 的快照读取、候选构造、短提交门复核交给 `wallet/rgb11_candidate_exec.go`。调用方保留提交后的备份、通知和结果处理；广播与 ACK 不进入重算段。原始 requestID、consignment、txid、vout、transferID 和选定账户固定，最多三次完整候选尝试。只有同一钱包、子账户、引擎版本的本地记录在提交前变化才产生专用 stale-local 错误；证据、资格、网络、真实冲突或 Flush 错误不重算。三次耗尽不把合法新投影标成 unavailable。

修改前 SHA-256：`rgb11_dkvs.go` b3f7709f56a8f3bcc3427e45fd9d08e6a5fe2b116f144dc426e8703b3fca7225，`rgb11_manager.go` 9a673e4e62efe470371484cb8c60b2fb2161f8c5b2f15dfbf759a71ae4a93236，`rgb11_store_sync.go` 0ce9c6f7a71ccb5dfa488b698dac724dd48bba529c53306f9502957e503e5497，`rgb11_transfer_reservation.go` aecbad03e04c93a1a75794b0568d99c2a30dcd1231a893d7ee7575b67f7de64d，`rgb11_refresh_candidate.go` 1b4dfe9f3b76e39c17b4de7b65dd61be54bdf164ef47cfeb24d3c7bcddba6c6f。原树已有其他任务 dirty，本清单只描述本次增量。

写入口核对：候选统一走 `applyRGB11CandidateSnapshot` → `importRGB11ReservationSnapshotWithMutation` → `FlushWith`，在同一短门中复核 engine/projection 基线并提交 reservation、ticker、锁和账户 dirty/generation。现有发行、导入、发票、发送准备、通道和地址收款候选仍调用同一 `apply`，但不进入自动重算：其中发送准备已选币、签名并临时预留输入，不能在只重建投影时悄悄复用旧交易。广播本地生命周期由 `rgb11_broadcast.go` 的 `CompareAndSavePendingTransferStates` 处理，ACK/代理与地址消息由 `rgb11_dkvs.go`、`rgb11_proxy_transport.go` 的精确 CompareAndSave 处理；恢复导入走 `rgb11_store_sync.go` 的 reservation 原子导入；L1 游标 `SaveLocalMetadata` 仍只在设备本地。这些路径未改成可自动重播的候选操作。

新增 `TestRGBReceiveRefreshOverlap` 用已准备的 OOB 收款、真实广播/确认夹具，控制正常 L1 监控查询与 PWA 收款完成的重叠，检查收款余额和没有再次广播；`TestRGBCancelBeforeCommit` 只检查 SDK 内部 context 在链证据返回后、短提交门前取消时不能提交。`wasm/main.go` 的 PWA Refresh 桥接传入 `context.Background()`，因此后者不是已证明的 PWA 路由取消，不计作 PWA 并发验收。提交门内再读 `ctx.Err()`；内部 stale-local 错误不再同时包装账户快照冲突，两个原基线单元测试改断言此专用错误。现有 `rgb11_api_sync_gate_test.go` 的已配置、离线投影/Direct 收款/ACK 拒写且无远端或本地副作用继续保留。旧 `TestAccountActivationACKCaptureOrdering` 的手工同值 PutBatch/固定读取屏障不是已证明的 PWA 后台时序，保留诊断但暂停扩写，不计作 PWA 并发验收。

此时只执行了 gofmt、`git diff --check` 等静态检查；**未编译或运行本次新增测试**。需由独立测试执行者在冻结输入上定向验证，再复核全组受影响回归。PWA 与 transcend 正在运行的旧固定候选不能作为此次新源码通过证据。
