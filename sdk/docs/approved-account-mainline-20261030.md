# 账户与 RGB 公共登记统一修改候选（2026-10-30）

本记录是实现交接，尚未执行本轮业务回归。原暂存区保持不变；修改仅在工作树。RGB 资产/UTXO 故障隔离由独立 Sol 处理，不属于下述 42 文件冻结。

- 候选清单、每文件 staged/current SHA、静态检查：`sdk/data/full-staged-20261030T1446Z/account-candidate-v1.json`。补丁为同目录 `account-candidate-v1.patch`，SHA-256 `e0d983fc3073b985f9d4881f8a1b2abc38e56433ac31d5b286a3b9486a572106`。4 个 data 归档 `.go`→`.go.txt` 的逐字节改名另见 `approved-test-manifest-v1.json`、`archive-go-renames-v1.patch`，未纳入源码补丁递归打包。
- 账户服务激活只提交身份/恢复配置 wrapper；本机持久化精确初始化目标及原请求 outbox。state/data 首次备份由原后台任务完成。原初始化设备须在线确认空基线；新设备读到服务已激活但备份缺失时只读等待，不重新激活。重启必须重新联网核对。
- 每个新建/导入根账户先发现服务状态。Unknown/查询失败只读；签名同步确认未激活后本地操作可用且无自动上传。已激活仍检查身份、端点、账户依赖组及真实冲突。存储准备、付费和远端 recovery writer 同样不能绕过发现资格。移除已不参与授权的旧 not-found token。
- 本地编辑继续推进 generation；A→B→A 有实际未确认编辑时仍提交远端 seq+1；只读同步不回写。精确本机 B 的签名 outbox 可辨别于其他端变更，允许后台上传 B 时继续编辑 C；B ACK 保留 C。
- 私人备份前导出公共 consignment，向选中 Core 的 raw `contract_consignment_base64` 注册路由提交，并独立验证 authority 签名读回；与 indexer 的 `{record}` 接口分开。失败保留 dirty，不阻塞本地发行。CoreModules 夹具启用真实账户 worker，在冷恢复 checkpoint 前等待实际备份，不制造已验证公共记录。
- 回归源码改为正常业务流程：在线本地编辑后才控制备份失败/丢 ACK；真正服务端提交后保留相同请求冷启动重放；恢复配置期间本地编辑保留；真实 A→B→C 和每次 seq+1。相应 TTL/client 更换先重连。未知账户、离线、激活中断、原目标/不同目标以及真实冲突保护保留。
- 用户明确不支持的同账户多设备同时编辑已标记 `Skip("not tested: ...")`，不能计通过。正常 lifecycle、顺序恢复、只读无回声和远端删除仍保留；后两项使用各自干净基线。
- 浏览器背景备份 case 只拦截已就绪账户的精确 stateKey 写请求：先证明本地改名完成，再释放为失败；读取不被阻断，finally 释放所有请求并撤路由。未使用业务 state revision 与 DKVS record seq 混比。

静态检查：限定 `git diff --check`、两份相关 `.mjs` 的 `node --check` 已通过。v4 编译的 unused client 与中间删除范围误带存储接口均已修正，原失败记录保留。v3/v5/v6 是较早树的无 case 编译通过，不能称当前业务通过。冻结后 v7 命令/完整日志/exit/前后文件 SHA 记录为 `account-mainline-compile-v7.*`。

父线程 review 后交 Luna：`./wallet` 关注 `TestAccountActivationThenBackup`、`TestAccountDiscoveryReadOnly`、`TestActivationMarkerColdResume`、`TestActivationMarkerWriteFault`、`TestActivationAckColdFinalize`、`TestAccountReconfigureKeepsEdit`、`TestAccountGroupGate`、`TestAccountRevertedMetadataClearsPending`、`TestRGBPublicBackupRegisters`；`./e2e` 关注 `TestSDKAccountActivationRetry`、`TestSDKDKVSConfirmedReplica`、`TestSDKAccountPWAOfflineRestart`、`TestSDKAccountPWACommitResponseLossRestart`、TTL/reverted、正常 lifecycle/recovery 及完整账户/PWA/CoreModules。最终按同版输入做全面自动测试（含 race、WASM/PWA），通过后才在持续同一 regtest 网络做真实 PWA 验收；此处没有发起这些测试。

冻结后 v7 已结束：`go test -mod=readonly -run '^$' ./wallet ./wasm ./e2e` exit 0，42 个归属文件前后 hash 全部相同；没有运行业务 case。两份 JS 语法检查和限定 diff 检查通过。暂存区 SHA-256 仍为 `cff77a4528b464a582715aafd23cd70806bdfbfc57b8d28737275d4f517af31a`。当前没有本写者运行中的编译或测试命令。
