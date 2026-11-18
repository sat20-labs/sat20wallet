# 新业务接口的 PWA 场景与 E2E

2026-11-15 调整：Guardian 已按用户要求暂停，原成功旅程不再是当前验收目标；以下表格已对齐新的关闭断言和双因素付费流程。最新账户 6 条、RGB/DID 5 条完整接口旅程及 SDK 全仓均已通过；受影响 PWA 自动化和发布检查已通过。独立真实 regtest UI 已通过私钥限制、双因素恢复、删除后新导入、付费升级与一次续费；恢复退出导航已修复，自动化及三条实际退出路径均通过。

2026-11-15。范围为已批准的 RGB 13 个、账户 18 个、操作记录 2 个业务接口，以及新增 DID 名称检查。原通道管理、通道合约和保留的三个旧 L1 执行入口不纳入本轮改造。执行状态以 `sdk-ui-e2e.md` 的实测结果为准；有用例代码不等于通过。

## 执行边界

使用当前生产 WASM、真实 Bitcoin/L1 索引器、bootstrap + Core regtest、真实 IndexedDB。钱包连接 Core。测试复用现有 Bitcoin 出块控制器；代理模式使用现有转运服务保存并返回 SDK 生成的原始证明和 ACK，不代替 SDK 验证。没有伪造钱包余额、链上交易、节点返回或恢复份额。

一个用例执行完整用户流程；每个流程有独立身份和浏览器存储，恢复前关闭原设备以保持唯一 writer。失败保留现场，不重置节点数据库。Go 子测试单独报告；调用记录 `sdk-ui-N-coverage.json` 仅记接口名称、返回码，不包含参数或私密数据。

## 场景映射

| Go 叶子用例 | 对应 PWA 入口 | 完整流程和业务断言 |
| --- | --- | --- |
| RGBManual | RGB 发行、导入、发送、收款、增发、待办、操作记录 | IFA 多份初始分配及增发额度 → 导出/导入合约 → witness 手工收款 → 错误证明和回执拒绝且不广播 → 原 ID 确认 → 真实出块和余额核对 → 增发 → 创建另一待办 → 切子账户不能读取/执行原操作 → 切回并重启 → 删除历史保留待办 → 取消恢复余额 → 导出已结算证明 → 先验证 witness/blind 混合批次被拒绝，再用两条 witness invoice 批量发送；缺少或重复回执不广播，完整回执按原批次广播，余额及重启核对。 |
| RGBDirectOffline | RGB 地址发送、刷新收款、账户备份入口 | 双方配置账户备份 → NIA 地址发送 → 接收方离线时等待 ACK → 接收方保存证明后再次离线 → 原 ID 广播 → 发送方通过第二笔真实 RGB 手工自收花费同笔交易的 RGB 找零 → 真实索引器确认自己的输出仍在 → 上线重启，余额增加 10.25 → 刷新期间正常出块仍保持余额。 |
| RGBBlindUDA | RGB 文件/粘贴导入、blind invoice、UDA 发行 | NIA armored 合约导入 → 无效合约/金额/地址拒绝 → blind invoice → 二进制证明 → 双方重启后从原待办继续 → 收款 → UDA 发行及导入 → JSON 包中的证明 → blind 收款 → 重启核对 NIA 和唯一资产。 |
| RGBProxy | RGB invoice 的代理传输选项、发送继续、收款刷新 | witness 与 blind 两种 invoice 各完成一次真实转账；接收方离线时投递并等待 ACK，未广播；发送方重启后读取原 ID；接收方刷新生成 ACK；原交易广播、出块、余额及收款状态核对；只读等待发送方后台监控结算找零，再执行下一笔；重启保持余额。 |
| DIDHistory | Tools 的 DID 铭刻、操作记录 | 无效/可用名称 → 真实铭刻 → 尚未出块时操作日志判定 submitted → 重复提交拒绝 → 出块后索引器判定 registered → 日志含交易 ID → 清理日志后仍由索引器判定已注册。 |
| TwoFactorRecovery | 账户设置、备份风险提示、恢复账户 | 两钱包及子账户清单 → 错误密码预检拒绝 → 存储选择/取消 → 创建并取消恢复设置 → 2/2 激活 → 离线修改子账户元数据显示未备份风险 → 上线重试同步 → 原设备关闭 → 无效恢复码/错误答案拒绝 → 未预览不可提交 → 预览取消、刷新使会话失效 → 新设备恢复并重启，核对完整清单；重复提交拒绝。 |
| GuardianDisabled | Guardian 功能暂停 | 五个业务接口及 2of3 创建明确拒绝，未消费存储授权；同一授权可继续创建并取消双因素包。 |
| PaidTwoFactorRecovery | 自己的付费存储、AUTOPAY 续费和双因素恢复 | 报价防篡改 → 首次付费 → 知识答案和用户份额激活 → 续费 → 演练拒绝提交 → 新设备恢复并重启检查身份和付费状态。 |
| WalletReimportPending / WalletReimportConfirmed | 删除非根钱包与明确重导入 | 分别在删除尚未上传与远端已确认时重导入；新 ID、默认单账户、空 DID/SP 配置，不恢复原 RGB 合约或发票；同步与重启保持空状态，明确新建的数据可再备份并冷恢复，旧数据保持不存在。 |
| PartialRecovery | 恢复页面 | 在模块导入边界持续注入失败，完整性从落库状态产生；页面显示部分完成及失败模块，不显示整体成功或再次提交整个恢复。 |

## 接口映射

| 接口组 | 覆盖用例 |
| --- | --- |
| GetRGBView / RefreshRGBState | 四条 RGB 流程中的资产、待办、收款状态、刷新与重启 |
| IssueRGBAsset / ImportRGBContract / ExportRGBContract | RGBManual（IFA）、RGBDirectOffline / RGBProxy（NIA）、RGBBlindUDA（NIA/UDA；文件与 armored 文本） |
| CreateRGBInvoice / PrepareRGBConsignment / AcceptRGBConsignment | RGBManual、RGBBlindUDA；RGBProxy 由 SDK 刷新处理代理证明 |
| PrepareRGBTransfer / ConfirmRGBTransfer / CancelRGBTransfer / ExportRGBTransfer | RGBManual、RGBDirectOffline、RGBBlindUDA、RGBProxy；增发后复用同一组确认/导出接口 |
| PrepareRGBInflation | RGBManual 的真实增发及余额核对 |
| GetAccountView / RefreshAccount | 账户完整流程及 Direct；备份风险、模块可写状态、离线后同步、付款状态 |
| PreflightRecovery / GetStorageOptions / ConfirmStorage / CancelStorageSetup | 账户完整流程；钱包清单、密码、容量、免费/付费报价、取消及重复查询原付款 |
| FundAutopay | PaidTwoFactorRecovery 的续费和错误报价拒绝 |
| CreateRecovery / ActivateRecovery | 双因素恢复完整流程；Guardian 联系、设置与确认接口只验证暂停拒绝 |
| LoadRecovery / PreviewRecovery / CommitRecovery / AbortRecovery | 双因素演练、取消、刷新、新设备恢复；Guardian 请求和回复只验证暂停拒绝 |
| GetOperationViews / DeleteOperationHistory | RGBManual、DIDHistory；详情、交易 ID、进行中的操作保留、清理不删除业务数据 |
| CheckDIDName | DIDHistory 的格式校验、索引器和本地提交日志两来源判断 |

## 减少手工验收的范围

通过后，金额换算、证明验证、投递/ACK/广播、存储付款、恢复材料处理、重启继续、账户隔离和日志职责由这些 E2E 回归。PWA 手工验收只需聚焦页面入口、布局与文案、确认/取消按钮接线、剪贴板和文件选择/下载、移动端与安装体验。接口 E2E 不替代这些浏览器 UI 行为，也不证明任意故障注入或所有可能网络故障已覆盖。

Go 入口：`TestSDKUIFlows`、`TestSDKAccountFlows`、`TestSDKPaidAccountFlow`、`TestSDKAccountReview`、`TestSDKPartialRestore`。需要同一真实环境配置和每条流程独立的已准备身份；复用原 runner，不运行 TestAll。完整命令：

```sh
go test ./e2e -run '^TestSDK(UIFlows|AccountFlows|PaidAccountFlow|AccountReview|PartialRestore)$' -count=1 -timeout=35m -json
```

## PWA 自动化边界

账户页面独立入口为 `TestPWAAccountViews`，也纳入 `TestSDKAccountPWAConnectedBrowser`。实际页面覆盖双因素恢复设置、文件下载、错误输入、激活、演练、离页取消、恢复和冷解锁，以及自己的不同容量报价和 Guardian 入口关闭。运行时组保留钱包目录/密码/Manager 生命周期，补充真实一分钟自动锁、恢复响应丢失不重复落库、后台备份失败与恢复显示。 新增 `PrivateKey` 分支：SDK 仅准备私钥钱包前置，实际冷刷新和密码表单解锁、备份预检拒绝激活、身份保持、再次冷解锁均走页面。原一分钟锁屏旅程补上真实密码解锁后的浏览器返回，且保留旧恢复会话失效断言；独立入口 `TestPWAUnlockNavigation` 复用同一用例。上述最新页面 3/3 与导航 1/1 已通过；生物识别既有四项也通过。 TwoFactor 继续补齐正确答案缺用户份额、错误答案的明确文案，以及演练完成后账户页可见返回按钮的导航断言；该完整旅程定向通过，最新发布检查 8/8。实际中文、英文页面的错误提示与只读预览也已通过。

RGB 页面保持三条完整流程：NIA 手工批量转账、IFA 增发、UDA Direct。发票表单实际切换 blind/proxy，验证空地址拒绝、真实代理参数及 SDK 返回值；完整代理转账复用 SDK E2E。固定 fixture 子账户按 index 和地址核对；Direct 只使用单独供资和激活的测试根钱包，不能回退到已有 account 0。页面读取新业务视图，独立链上交易证据保留；不为测试重新开放 proofs、reservations 等内部状态。

通道本地导入失败的旧边界和 AUTOPAY 实际签名交易核对继续保留。原 SDK 及节点 E2E 负责证明/资产绑定验证，PWA 侧只核对它使用的接口、确认内容与页面结果。测试中的网络/存储失败注入会在结果中单列。
