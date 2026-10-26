# DKVS 本地读写视图偏差：延后处理

状态：2026-10-26，用户要求先完成当前验收、独立审核与提交，再单独修复。本文不代表生产修复获批。原激活层 80–140 行方案暂停，不实施。

## 设计契约与偏差

用户要求 managed 数据由 SDK 统一维护：启动先与服务器同步，随后应用读写同一本地逻辑视图。

历史证据：sat20wallet@7f4972d9399e0d9a51c822a21b07db0deb0a36fc 的 docs/dkvs-manager-design.md:48–61 要求每次会话初始同步后才允许领域读写，写入基于确认本地记录，远端成功后原子更新本地副本。satoshinet@4a0013bebc966ac68bfb1fabf1a5cf362edf22f8 的 docs/dkvs-design.md:379–405 保留 session 屏障。2026-10-05 的 7a867a3 重构删除该明确 session 条款，引入 ACK/确认副本分离；提交历史不能证明用户批准过对外语义变化。

当前代码：
- sdk/wallet/dkvs_subscription_manager.go:GetVerified 对 managed key 只读 confirmed replica；currentKeyState 本地缺项/过期时直接查询服务器，不先安装进普通 Get 所用副本；updateValuesWithOrigin 重试也可直接权威读取。
- dkvs_application_client.go:putRecordBatchCASWithOrigin 将完整签名请求持久化 outbox；验证服务器 ACK 后 ApplyWriteResultAndAck 保存 ACK 下界、删除 outbox，不更新 confirmed KV。putBatchCASLockedWithOrigin 仅 wakeSync 后返回。
- sdk/wallet/dkvs/active_replica.go:InstallActiveState 是验证来源/root/基线后的原子副本安装入口。内部 outbox/replica 分离可保留，但不应向领域暴露写成功后读不到或隐含不同视图。
- WaitReady 接受持久 READY/OFFLINE_READY；Manager.start 未为普通 store 建立本次启动同步屏障；Manager.IsReady 不包含 DKVS 初始同步。初次无副本需同步，而重启可复用上次确认资格。上次确认并不等于本次启动已同步。

## 已有红例与边界

现有 TestDKVSFinalLocalPutRequiresDeltaBeforeAdvancingToken 明确要求 Put 成功、WaitReady 成功而 Get NotFound，SyncCurrent 后才可读。这是需要修正的上层契约，不能仅删除底层 ACK 不直写副本的安全断言。TestDKVSReviewConfirmedOfflineReplicaReadableAfterManagerRestart 接受重启同步失败后的旧副本普通读取，也需按新会话契约重新评估。

自然 UDA 转移恢复演练：2026-10-25 18:05:42.792 UTC state/data ACK applied=2；18:05:44.516 activation local_builder_compare 报 state captured absent/current present, seq=1，current 摘要前缀 eeb8d6603dae762d 与 ACK 匹配；激活业务写入前拒绝。证据在本机任务 logs/uda-root-cause-20261025-v1/activation-warm-sanitized-trace.json 和 activation-real-mismatch-lines.json，原始敏感日志不纳入仓库。

缺少实际 capture 时间，不能确定 ACK 在 capture 前/后、原 outbox 在 capture 后完成，或 builder 该次取本地/远端。writer 编号是浏览器 context 观测序号，不是 Go writer 身份。受控三时序不能替代自然时序证明。

TestAccountActivationRejectsNewOwnerSignedWrapperBaseline 走原生产激活代码：受控 owner-signed wrapper 单键变更同步可见后仍可能被旧 profile 激活覆盖。这证明盲目 SyncCurrent/接受新 hash 不安全，不等于正常多设备三键流程已证明丢数据。对应红例保留；历史多资产 UDA consistency warning/sync error 独立保留，尚未归并。

## 后续最小方向（待审批）

优先在 DKVS Manager 恢复本会话初始同步屏障；managed Get/Update builder 统一确认视图，需要刷新时先安全安装；领域写成功需具有本地可见性保证，保留 outbox 和安全单入口安装。服务器已提交但同步失败必须明确保留提交事实，不能当未提交重签业务写入。仍保留真实竞争拒绝、profile 与 wrapper/state/data 的领域关联校验。离线旧缓存可读与普通业务 ready 的取舍、写完成延迟需显式审定。不得通过刷新、放宽 CAS、增加重试或延长 timeout 掩盖。

当前 sdk/wallet/account_management_state.go 的 12 行失败分支脱敏 Warning 曾单独获批，仅观测；最终是否纳入本轮提交待独立审核，不视为业务修复授权。

当前验收允许明确保留缺陷，所有 FAIL/SKIP/未执行仍按真实结果报告；分组通过不替代完整门禁。
