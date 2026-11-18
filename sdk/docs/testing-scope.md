# SDK 回归范围

## 默认完整 Go 回归

在仓库根执行一条命令：

```sh
scripts/sat20wallet-sdk-native.sh
```

默认入口动态发现 `sdk` 下全部 Go package，再以单个 Go 进程执行 `go test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=150m ./...`。包括内部包、现有单元测试、公开 Manager 接口和原生业务 E2E，不再使用 128 根功能清单冒充全仓。

`-skip` 只排除需要父进程配置的 `TestSDKCoreModulesConnectedE2E` 和显式便利包装器 `TestSDKCoreModulesValidation`，避免 Core 重复运行。真正父入口 `TestSDKCoreModulesE2E` 仍在 E2E 包完整执行。已有 opt-in 诊断、live、保留网络验收的源码 gate 仍有效，其 SKIP 必须单列，不能算 PASS。

脚本清除继承的 live、持久 regtest、浏览器、诊断和故障注入开关。原生 fixture 在测试组内使用自己的临时目录和端口，不连接、重建、清理用户已有持久网络。同组复用真实链及其索引器，钱包数据库仍按设备/场景隔离。需要本机 Go、Bitcoin Core 和已有兄弟项目依赖，不需要 `SAT20_ACCOUNT_UI_CONFIG`、私密 fixture 或浏览器。完整 runner 要求实际 `bitcoind` 执行，缺少它应失败，不能用缺失依赖 SKIP 宣称完整验证。

`all` 包装入口也转入同一脚本，外层默认预算为 3 小时。此变更只涉及工程脚本，不修改 MCP 实现、配置或权限。历史 `sdk-functional-roots.tsv` 只保留为集中业务回归/补充脚本的清单，不是完整默认范围。

## Guardian 与 PWA 编译标签

Guardian 业务默认不注册，显式启用：

```sh
scripts/sat20wallet-sdk-native.sh -tags=guardian
# 也可在 sdk 中选定原生组：
go test -mod=readonly -tags=guardian -count=1 ./e2e -run '^TestSDKAccountGuardianIdentitySurvivesAccountRecovery$'
```

独立 Guardian 测试文件使用 `//go:build guardian`。共享账户和付费组使用该标签选择的编译期常量保留同一普通流程：默认 2of2，显式 Guardian 才准备/激活 Guardian、发布邮箱份额和验证 2of3。不开标签时不执行这些业务步骤；普通账户、AUTOPAY、DKVS 复制与付费证据断言仍在。恢复即时权威读取 case 默认验证 2of2，标签版本保留原 2of3 与 Guardian 权威读取。底层分片、编码、签名和授权目的隔离测试不因名称包含 Guardian 而删除。

真正页面、IndexedDB、浏览器生命周期以及 JS/WASM 桥接契约使用 `//go:build pwa`，须显式选择目标，例如：

```sh
cd sdk
go test -mod=readonly -tags=pwa -count=1 -timeout=60m ./e2e -run '^TestSDKAccountPWAConnectedBrowser$'
```

不要用无选择器的 `-tags=pwa ./...` 代替 SDK 原生全仓验证。持久 PWA 验收继续使用原有专门入口和经确认的环境；同时涉及 Guardian 的独立浏览器入口需要 `-tags=pwa,guardian`。PWA 页面可见性、导航、剪贴板、WebAuthn、多标签页 Manager 互斥和 JS 错误/数值转换不能由 Go API 断言冒充。

## Core 覆盖与证据

默认 Core 是 14 个非 Guardian 原生组，`-tags=guardian` 是 15 个原生组。PWA RGB 恢复组只在 `pwa` 标签构建中插入，不再由默认 root 偷偷启动 Node。普通/恢复组共用一个环境，长历史组单独一个环境；两段共用原来的 85 分钟预算。父进程显式将编译标签传到钱包子进程，预编译产物必须匹配父进程的可选标签。

原有 selector 静态测试保留，分别核对 14/15 组与长历史隔离。runner 同时核对包结果和本次 Core JSON 证据：selector 完全一致、两段真实 RPC 不同、每组恰好一个终态、无遗漏/重复/子组 SKIP、报告完成时间落在本次父测试期间。旧 16 组清单中的已删除 R11 不再计入。完整 Go 命令成功但 Core 证据缺失，也不能通过验收。

## 浏览器业务迁移

业务测试直接调用生产 Manager API。旧 WASM 接口中的钱包、密码、签名、PSBT、UTXO 锁、操作历史及资金交易与新账户/RGB/DID 旅程在原生测试中执行；真正 JS 桥接和页面断言保留在 `pwa` 文件，不删除。

- `native_iface_*_test.go`：旧 SDK-WASM 接口业务，按共用真实临时链/独立设备分组。`TestSDKNativeInterfaces/real_l2` 复用同组真实 Bitcoin/Core/indexer，从 H1002 成熟 coinbase 的 SDK 纯白拆分、实际 H1/H2/H3 Anchor 和 POS31/32/33，走到 BRC20 deploy/mint/transfer inscription、通道 splice/unlock、精确分发及 L2 接口断言。旧独立 `TestSDKNativeL2Interfaces` 已并入，不重复执行，也不再以受控 L1 Anchor 代替这条实链准备路径。
- `native_ui_*_test.go`：两因素、付费、重导入、RGB 手工/Direct/blind/proxy、DID 等公开业务 API。 `TestSDKAccountFlows/NoACK` 分别验证显式 Retry 与不调用任何同步入口的自动重连；拒写无副作用后必须完成全新本机编辑、自己的备份确认、离线数据库重启和空数据库恢复。自动路径若不能恢复会明确失败，不能用显式 Retry 掩盖；仅上传失败与完整 DKVS 断线是分开的故障。
- `wallet/native_ui_recovery_test.go`：使用已有 test-only RGB provider 包装器注入 RGB11 Import 失败，不增加生产 hook；检查实际部分恢复、持久状态、冷重启和重试。
- `silentpay_manager_e2e_test.go`：真实 Bitcoin/SP 已确认收款，另验证错误 checksum 不广播、失败/取消扫描保留已验证 receipt、成功重试和子账户资金隔离。已有纯 Go SP 冷重启及多钱包扫描用例继续默认运行。
- 原来的 Go 进程组取消/迟到注册测试、selector/config/端口解析和 PWA fixture 隔离测试仍属于原生默认范围，不能因为文件名有 PWA 而排除。

迁移中有真实链、受控链证据和 test-only 故障注入三种边界，报告必须注明。Go 不能替代的 UI/桥接断言保留独立验收；未等价实现的业务项必须明确列为缺口，而不是以通用错误断言、SKIP 或相近测试名称代替。

## 验证与报告

开发不运行测试；本轮测试由办公 Mac 的本地 Codex Luna Max 执行。先验证默认、Guardian 和 PWA 构建边界，再跑相关失败/迁移组，最后跑完整 Go 回归。生产修复仍需先复现、定位并由用户审核。

报告包括动态包清单、根/叶 PASS、FAIL、SKIP、未返回终态、Core 14/15 组和独立 PWA NOTRUN。旧范围的失败批次不会追溯改写成新范围结果。源码静态审计中全部 Node 启动点应只位于 `pwa` 标签文件；这与完整运行时没有 Node/browser 子进程是两项不同证据，不能仅凭瞬间进程采样宣称后者。

首轮默认全包编译发现 3 个测试 import 问题，已在源码修正；其余标签和业务定向当时未运行。资金/BRC20 后续候选只完成静态核查，尚未重新编译或测试。按用户要求，等 RGB11 更新进入 SDK 后再由 Luna 统一复测，不能据源码修正声明全仓通过。
