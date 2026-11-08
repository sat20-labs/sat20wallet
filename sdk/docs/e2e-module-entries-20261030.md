# SDK / PWA E2E 模块入口（2026-10-30）

这是现有入口的运行索引，不更改 case 步骤、断言、skip 或选择规则。以下命令均在 `sat20wallet/sdk` 执行；仅在隔离 fake-L1/Core 环境与测试执行窗口确认后运行。`-run` 选中的项目才算执行，未选中项目不能计入通过。

| 修改范围 | 现有入口 | 主要覆盖 |
| --- | --- | --- |
| 账户管理、DKVS、冷恢复 | `go test -mod=readonly -run '^TestSDKAccountPWAConnectedBrowser$' ./e2e` | 账户 PWA 完整浏览器入口；SDK 层另选 `TestSDKAccount.*`、`TestSDKDKVS.*` 的现有用例 |
| 账户恢复相关三项 | `go test -mod=readonly -run '^TestPWAAccountRecovery$' ./e2e` | DID 清空、远端删除、离线目录；focused runner 用现有 `fresh/setup` 独立建立恢复材料，未选中项不计通过 |
| RGB11 NIA | `go test -mod=readonly -run '^TestSDKWalletPWAConnectedBrowser$/^rgb-nia-workflow$' ./e2e` | 现有 NIA 浏览器 group |
| RGB11 IFA / UDA | 将上式第二段改为 `^rgb-ifa-issue$`、`^rgb-uda-issue$` 或 `^rgb-uda-transfer$` | 现有发行和转移 group；恢复材料还需对应 SDK 测试 |
| 通道、通道合约 | `go test -mod=readonly -run '^TestSDKSmartContracts(Admission|Templates|Additional|EVM)$' ./e2e` | SDK 合约准入、模板、EVM；PWA 通道另用既有 real-regtest 入口，transcend 的 `TestAll` 是独立发布门禁 |
| POS / 节点 / 挖矿 | 将 wallet PWA 入口第二段选为 `^pos$`、`^node$` 或 `^mining$` | 现有三个浏览器 group |
| 基础钱包 / 工具 | 将 wallet PWA 入口第二段选为 `^wallet-basic$`、`^tools$`、`^dapp$` 或 `^biometric$` | 现有钱包与工具浏览器 group |
| L1 协议 / 铸造 | 将 wallet PWA 入口第二段选为 `^l1-protocols$` 或 `^mint$` | 现有资产协议与铸造 group |

`TestSDKWalletPWAConnectedBrowser` 无 `-run` 子分组过滤时仍运行其全部既有必跑 group。其 `SAT20_PWA_E2E_CASES` 是原有的 JSON case 名列表入口，显式空列表和未知名称会失败；本索引不改变该语义。`TestSDKAccountPWAConnectedBrowser` 保留账户完整入口；`TestPWAAccountRecovery` 只选既有三项，复用同一 `runAccountPWABrowser`。三项定向入口新增后尚未执行浏览器。

发布验收仍需要同一候选输入上的完整 SDK、PWA、transcend 测试和历史通道数据升级证据。局部入口只用于定位与受影响回归。`sdk/e2e` 当前实际源码仅有 `unix`、`!darwin` 两个操作系统构建约束；`testdata` 补丁文本中的 `rpctest` 不参与本包编译。未发现业务功能 build tag。现有 skip、可选 case 和真实 regtest 前置保持原样，运行报告须逐项列出 pass/fail/skip/not-run。
