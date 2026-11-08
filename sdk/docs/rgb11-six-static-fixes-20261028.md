# RGB11/DKVS 六项静态修补收据（2026-10-28）

基线 `fde00ec629f689839264bb328d0a08574d622ed0`。此前独立 Astra 静态复核提出六个可达条件；本轮在既有共享 dirty 工作树中只追加针对性 SDK/PWA 源码和回归测试源码，未重置或覆盖其他会话文件。此表为**源码状态，不是通过的测试结果**。

| 静态发现 | 本轮源码候选 | 尚需统一验收 |
| --- | --- | --- |
| viewer 未知明文可能泄露 | `wallet/account_dkvs_inspect.go` 默认隐藏无法证明安全的 raw；允许字段投影保留；PWA 显示隐藏原因 | 未知 UTF-8/非 UTF-8、密文与已知格式、账户作用域 |
| viewer 扫描可能混合两代缓存 | 使用 `SnapshotDB.NewReadView` 固定 state/record 读视图，游标绑定快照 | 并发更新与跨页读取、无快照后端拒绝 |
| IFA 混合载体第二枚 seal 被误判重复 | `rgb11_refresh_candidate.go`、`rgb11_recovery_capacity.go` 按 allocation/seal 分项校验；`rgb11_manager.go` 合并兼容同类找零 | 完整 prepare→签名/广播→接收→刷新，R>0 与同类不可合并负例 |
| 未决 ACK 超范围阻塞 | `dkvs_subscription_sync.go`、`dkvs_outbox_sync.go` 按 key/账户 state-blob-wrapper 依赖组限制，独立请求可继续 | Pending/Inflight/Acknowledged 与网络断线/真实冲突 |
| 本地 ACK 收尾错误不自动重试 | ACK 持久化失败、删 outbox 后授权读取失败进入有界重试 | 注错后无 watch/no job、重启和最终授权 |
| 公共引用未覆盖所有完成路径 | `account_management_state.go` 将公共读回检验覆盖 state-only、dirty/no-op、导入/恢复完成；失败保留未完成状态 | 公共资料失联/恢复、ACK 后失联、generation 竞态 |

相关回归源码已放在 `wallet/account_dkvs_inspect_test.go`、`wallet/dkvs_review_sixth_test.go`、`wallet/rgb11_mixed_change_test.go`、`wallet/rgb11_backup_material_test.go` 等文件中。当前工作树**未构建、未跑普通/race、未跑 PWA 或通道合约测试**；此前历史 14/101 通过不能证明本轮候选通过。最新文件哈希与补丁收据将保存到 `data/contract-rgb11-dkvs-20261028/` 的新版本目录，旧冻结材料保持不变。

本轮追加的公共技术登记是独立范围：SDK/PWA 公开文件导出与签名读回、市场部署前串联、SatoshiNet Core 业务校验、transcend 部署/splicing-in/expand 前置检查。技术登记不依赖发行人 DID，但短正式名协议键目前仍未接通；不能把登记完成与短名落地混为一谈。
