# 未激活账户本地写与激活恢复检查点（2026-10-30）

当前 SDK HEAD 为 `fde00ec629f689839264bb328d0a08574d622ed0`，工作树仍有其他会话的大量未提交修改。本记录只描述本轮账户补丁，不把整个工作树或先前完整 wallet v5 结果说成当前候选已验收。

本轮账户源码、PWA 状态组件及 SDK `go.mod/go.sum` 的最终文件哈希保存在 `../data/contract-rgb11-dkvs-20261028/preactivation-20261030/source-hashes-v3.txt`，该清单自身 SHA-256 为 `953c24fa5edabf1ea0f9a6143d2ffb67f1091beb197b11ee387ecd997fc5a6dc`。v1/v2 清单保留；v3 相比 v2 仅修正 RGB 操作模式的过时注释。这是文件指纹，不是完整四仓构建输入冻结。

用户已确认：普通未激活账户无备份，允许本地操作；开始激活/恢复后持久记录意图，异常重启须先核实远端；已激活账户仍执行在线基线、离线只读与冲突保护。本轮源码让未激活业务写走本地资格检查，RGB reservation 与 dirty/generation 同本地原子批次提交；账户状态区分 `local_only` 和 `activation_pending`，PWA 作相应提示。未激活前账户后台备份发布被抑制，显式激活会登记所需三条 DKVS 路径。激活前保存目标 wrapper/state/data 哈希；若远端已确认但本地最终 profile 未写，激活重试或当前根钱包恢复只在签名记录及本地目录/恢复材料与该目标一致时收尾，不匹配返回冲突。该恢复分支目前没有专门执行过崩溃边界回归。

本轮源文件涉及 `wallet/account_activation_resume.go`、`wallet/account_management_profile.go`、`wallet/account_management_state.go`、`wallet/account_root_wrapper.go`、`wallet/dkvs_registry.go`、`wallet/dkvs_account_services.go`、`wallet/account_managed_ack.go`、`wallet/rgb11_managed_operation.go`、`wallet/interface.go`，以及 PWA 的账户状态类型和两个设置组件。没有修改 SDK/PWA 测试 case、skip 或选择规则；先前留下的 POS/RGB/Node 诊断改动保留原 diff，见现有工作树，未在本轮继续更改。

已执行：

| 检查 | 结果与证据 |
| --- | --- |
| `go test -mod=readonly -run '^$' ./wallet` | 当前源码编译通过、无测试运行；原 SDK 目录和默认 Go 缓存 |
| `go test -mod=readonly -json -count=1 -run '^(TestAccountPaidActivationNetworkWaitDoesNotBlockLocalState|TestAccountRootDiscoveryRestoresOriginalAccount)$' ./wallet` | 最后源码重跑 exit 0；WaitReady、CAS 与 root 恢复既有用例通过；日志 `../data/contract-rgb11-dkvs-20261028/preactivation-20261030/activation-root-v3.jsonl`，SHA-256 `17d669b168f5d82fb967c6855a02c6193b56875654d0c1e3c93010c1872cb9fe`；v2 日志保留 |
| `npm run compile`（PWA） | exit 0，`vue-tsc --noEmit`；日志 `../data/contract-rgb11-dkvs-20261028/preactivation-20261030/pwa-compile-v1.log`，SHA-256 `48dacadd52d700a07293327aea0dc2f648f3db931dee8095bc7768243804f885` |
| 定向旧语义用例三项 | exit 1，原日志 `../data/contract-rgb11-dkvs-20261028/preactivation-20261030/targeted-v1.jsonl`，SHA-256 `51b36b0bda7d13fb35c05f4d3e82e824c8c6af0f57e2434c31d1ce92d4321790`；不能计为通过 |
| 首次钱包与激活身份三项 | exit 1；首次钱包创建两项通过，激活中账户/钱包切换两子项在共同的激活前同步夹具失败；日志 `../data/contract-rgb11-dkvs-20261028/preactivation-20261030/first-wallet-v1.jsonl`，SHA-256 `89131d793456a1244c755cd66cb21c625cfe16efff4f0dff0e02debe6e6dc3de` |

旧用例需先与用户沟通再改：`TestDKVSOfflineUnconfiguredProfileRejectsMetadata` 要求未激活编辑被拒，现规则要求本地持久成功且零上传；`TestRGB11PublicOperationsRequireRecoveryBeforeMutation` 的未配置子项要求拒绝，现规则允许合法本地 RGB 操作并保存恢复材料；`TestAccountPaidActivationPreservesConcurrentLocalChanges` 的 CAS/BindingCAS 夹具在首次激活前调用 `SyncAccountManagementState`，现规则不允许把未激活账户当作已有远端备份来同步。`TestAccountPaidActivationSelectionChangeDoesNotChangeRootIdentity` 两子项也命中同一夹具的 `account_management_state_sync_test.go:756`。这些用例的并发与身份保护目标仍须保留，不能直接删除断言。用户已要求测试分组只整理、不擅改 case；本轮未调整以上用例。

待验：未激活离线业务及零上传、激活崩溃前后远端缺失/相同/不同结果、重启后 root 恢复精确收尾、并发本地写与已激活离线拒写、真实 PWA 激活状态。当前完整 `./wallet`、PWA 浏览器及 transcend `TestAll` 均未按本轮源码重跑；发布门禁仍未完成。模块入口仅整理于 `e2e-module-entries-20261030.md`，不改变任何 case 或 build tag。

## 13:43 追加验收记录

上述“待用户同意修改 case”是先前检查点。用户随后已明确同意列出的旧 case 调整和首次激活回归。本次仅在获准范围内改动：未激活 metadata 改为本地持久成功、无远端记录；未激活 RGB 发票改为合法本地生成、保存发票/relay 材料并重建 RGB 引擎读取，仍不发布 DKVS；激活夹具取消激活前同步，激活后的新增 mailbox/service 目录明确完成在线基线后才做受管写。已激活离线拒写与身份选择断言保留。

账户门闩在远端操作前持久记录完整目标和三条记录的哈希。补充三个故障用例：初始远端同步失败后冷重启，同目标可继续、不同目标拒绝；门闩写失败不触发远端发布且仍可普通本地写；远端 wrapper/state/data 已提交而最终本地 profile 写失败，冷重启按原签名记录完成收尾。`account_management_state.go` 的 nil-profile 门闩分支保存完整未激活 profile，避免解引用或空标记。此次结果仅证明这些受控 fixture 路径，不能代表真实环境恢复验收。

同一候选的定向记录在 `../data/contract-rgb11-dkvs-20261028/preactivation-20261030/account-acceptance-v4.json`，其中列出准确命令、退出码、日志 SHA-256 与六个相关源码指纹；收据自身 SHA-256 为 `95738ac6d6c8d82578ef3fe600df503a81a5d3384ef89a17e09af0a0de7ae7e0`。正常 `account-targeted-v10` 9 个唯一叶子全部通过；新增 ACK 冷收尾 `activation-ack-v2` 1 个通过；`account-race-v11` 10 个唯一叶子通过、0 fail/skip；既有激活网络等待和并发修改 `account-concurrent-race-v12` 5 个唯一叶子通过、0 fail/skip；根钱包发现与首次钱包原子初始化 `account-root-race-v13` 2 个通过、0 fail/skip。此前 v4/v5/v6/v7/v8/v9 的红例/诊断日志保留，不能混作当前通过结果。没有跑完整 wallet、PWA 浏览器、transcend `TestAll` 或真实测试网；本轮结果不构成版本发布验收完成。

质押地址与通道地址双向互斥仍未修改生产代码。现有早期 reservation 只在内存，`RS_INIT` 重启丢弃；两次无锁预检无法满足同时请求。最小持久占位的选择及风险见 `stake-channel-address-gate-20261030.md`，须按根目录 `AGENTS.md` 的复杂性规则先由用户决定。

## 账户 PWA 66 项入口（13:57 UTC+8）

本批只运行 `go test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=55m -run '^TestSDKAccountPWAConnectedBrowser$' ./e2e`，原 runner 使用临时 Core/Miner/Bootstrap、当前源码临时编译的 WASM 和真实 IndexedDB；没有运行钱包 13 组或覆盖发布 WASM。三次运行的完整输入指纹、日志、退出码、摘要和 SHA-256 在 `../data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/account-pwa-preactivation-v6-20261030-receipt.json`（SHA-256 `49be2aa60cc342c40bf2a9d666bfe75c98fe89dc378a70361aa3f9f691051e74`）。三次包终态均为 fail，均未见 panic：v4 为 0 pass、1 fail、65 not-run；v5/v6 均为 11 pass、1 fail、54 not-run。临时 WASM 存储检查在 v4 日志中通过，但不能替代浏览器 case 通过。

v4 在首个新账户用例等待 `dkvs_write_allowed=true` 超时。该账户 `recovery_configured=false`，SDK `GetAccountManagementStatus` 明确将它标记 `local_only=true`、远端写资格为 false。按已批准的新账户原则，将该用例改为断言这三个实际状态，不再让普通本地账户等待远端授权。v5 通过首个用例并到达激活后写；按已激活必须先同步的规则，v6 将原写资格等待移到 2-of-2 恢复配置完成后。没有改动其他 case 的步骤或原断言；脚本 `node --check`、限定 `git diff --check` 均通过。

v6 的激活后等待通过，后续 `PWA adds and renames subaccount and preserves root identity` 仍在 `addAccount('Savings', 1)` 后调用 `updateAccountName(1, 'Travel')` 时收到 `DKVS managed path is not synchronized`（`account-management-e2e.mjs:1083-1096`）。PWA `addAccount` 先经 SDK `EnsureAccount`，再自动 `switchToAccount(1)`；随后改名经 SDK `UpdateAccountMetadata`。SDK 的已激活写门槛要求在线会话、完整订阅前缀和账户 state/blob 基线；此次日志尚不能判定具体是哪一项在切换后失效。该失败仅用 580ms，不是 90 秒等待超时。不能在测试中继续盲加等待、降低断言或称 66 项通过；下一步须先取切换前后 `status`/订阅状态的有界诊断，区分正常新增作用域同步与后台会话失效，再决定 PWA 写资格提示或 SDK 修复。当前没有修改生产规则或质押/通道逻辑。

静态对照：上一轮 PWA v3 的同名子账户 case 通过，而本轮 SDK `prepareAccountMutationLocked` 新增了每次受管写前的资格检查，不能用旧通过证明新规则下连续写可用。`SubWalletManager.vue` 的新建按钮只按 `isCreating` 禁用，当前账户编辑入口只按 `isSwitchingAccount` 禁用；`NameSelect.vue` 的 DID 保存只按 `busy` 禁用。账户设置页虽然展示 `dkvs_write_allowed`，上述操作页并未据此门禁。这个源码事实提示真实 UI 也可能在已激活同步窗口允许用户发起注定被拒的操作，但还没有浏览器 UI 交互的独立复现；不把直接 store API case 等同于已证明的 UI 故障。
