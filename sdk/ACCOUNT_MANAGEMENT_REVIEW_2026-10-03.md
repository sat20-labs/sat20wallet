# 账户管理跨仓库审查与 SDK E2E 验证

审查日期：2026-10-03（Asia/Taipei）  
最后一次账户 E2E：2026-10-03 20:51:46—20:55:34 +08:00  
范围：sat20wallet/sdk、SatoshiNet 和 Transcend 中的账户管理相关链路。不是仅审查未提交 diff，也不是对三个仓库全部模块作安全认证。

## 结论

本轮完成账户管理相关源码审查，新增 4 个 SDK E2E 文件和 1 个固定测试证据入口文件；没有修改生产代码、既有测试、MCP 命令脚本或 Git 暂存区。工作区原有的 DKVS 等改动保持原状。

新增测试包含 24 个叶子场景：16 个通过，8 个失败。8 个失败归为 3 类问题：首次激活与 CoreNode 绑定顺序冲突、恢复提交未校验输入一致性、显式 Stop 后旧授权仍可消费。另重跑既有的付费账户恢复和多设备生命周期两条 E2E，均通过。

当前不能把账户管理模块判为上线通过。保留真实失败断言，没有将缺陷改成“预期错误即通过”或用 Skip 隐藏。

## 审查链路与真实执行边界

SDK 的主要审查范围包括：wallet/interface.go、wallet/wallet_catalog.go、wallet/account_management_profile.go、wallet/account_management_state.go、wallet/account_storage_operation.go、wallet/account_pwa.go、wallet/account_root_wrapper.go、wallet/dkvs_account.go、wallet/dkvs_account_services.go、wallet/dkvs_core_binding_client.go 和 wallet/message_manager.go，以及现有相关测试。

SatoshiNet 重点核对 message_binding.go、indexer/indexer/dkvs/rpc_admission.go 及相关 RPC 绑定入口。Transcend 核对 stp/interface.go 中 SDK Manager 的集成、钱包身份切换及回调桥接，并核对 stp/dkvs.go 的通道备份边界。后者是通道备份路径，不能与新的账户状态 DKVS 路径混为一谈。

E2E 复用隔离网络夹具：真实 SatoshiNet Bootstrap/Core/Miner 进程、真实 Transcend STP 插件和 SDK 钱包插件、实际 HTTP/RPC、P2P 及临时数据库。L1 索引证据由测试夹具提供。所有新账户操作均从 SDK 公开接口进入，不通过私有字段注入业务结果；直接读取节点记录仅用于结果断言。

新增客户端明确配置本机两个节点的地址，不启动后台自动绑定，以便首次激活测试不会被后台任务提前建立的绑定掩盖。测试没有使用用户真实钱包、生产数据库或公共网络资金；既有 AUTOPAY E2E 的部署、充值和出块发生在隔离测试网络。

## F1 — P1：首次激活先写 KV、后执行绑定，遭拒绝后触发 panic

### 位置

- sdk/wallet/account_management_state.go：ActivateAccountManagement / activateAccountManagement，当前 store.Update 调用位于第 690 行。
- sdk/wallet/message_manager.go：prepareAccountCoreNodeBinding（第 124 行）返回执行绑定的闭包，准备闭包不等于已经完成绑定。
- sdk/wallet/dkvs_outbox_sync.go：markDKVSOutboxSubmissionFailure，当前 panic 栈指向第 211 行。
- satoshinet/indexer/indexer/dkvs/rpc_admission.go：WalletRPCAdmission.PutRecords。
- satoshinet/message_binding.go：AcceptLocalBinding（第 105 行）。

### 复现与原因

TestSDKAccountReviewActivationWithoutPrebinding 使用全新临时钱包及全新节点，先确认远端根账户 AccountMappingKey 不存在，再通过 UseAccountStorageAuthorization 调用 ActivateAccountManagement。

激活代码虽然准备了 CoreNode 绑定闭包，但在真正执行闭包之前先调用 store.Update。服务器的新规则正确拒绝未在本 CoreNode 本地接受绑定的钱包：DKVS_PERMISSION_DENIED / wallet is not locally accepted by this CoreNode。SDK 随后按永久 outbox 错误路径 panic。

这证明激活流程依赖额外预绑定或后台绑定已经完成；不能据此推断所有 PWA 首次激活必然失败。已有正向 E2E 在激活前手动 Bind，因此不能暴露这个顺序问题。

### 修复边界

实际完成并确认根账户 / account 0 的 CoreNode 绑定之后，才能发起第一笔受保护的账户 KV 写入。调整时保留已有身份/节点快照校验，避免为网络调用重新扩大身份锁的范围。不得放宽 SatoshiNet 的绑定准入规则，也不得仅给失败测试补一条预绑定来掩盖问题。

首次激活的拒绝路径还应有明确的错误与状态处理，不能留下无法解释的初始化中断。此轮未修改生产实现。

## F2 — P1：恢复提交信任可变的解密结果，允许不一致密钥、密文、身份和版本落库

### 位置

sdk/wallet/account_management_state.go：RecoveredAccountManagementState、RestoreAccountManagementState、restoreAccountManagementStateOperation。

### 复现

先用正确凭据，通过真实节点执行 LoadAccountManagementStateForRecovery，取得可正常恢复的状态。每次深拷贝该状态，在独立空数据库设备上仅变更一项，然后调用公开 RestoreAccountManagementState：

| 子测试 | 唯一变更 | 实际结果 |
| --- | --- | --- |
| WrongSecret | 翻转传入的 32 字节 secret 的一位 | 返回 nil，创建钱包并激活 |
| CorruptCiphertext | 改变 State Envelope 的一个字节 | 返回 nil，创建钱包并激活 |
| PlaintextNotMatchingCiphertext | 只改解密后的钱包名称，不改密文 | 修改后的名称被安装 |
| DifferentAccountLocator | 替换 locator.AccountID | 返回 nil，创建钱包并激活 |
| MismatchedRevision | 只增加 Seq | 返回 nil，创建钱包并激活 |
| CorruptManagedDataCiphertext | 改变 ManagedDataEnvelope 的一个字节 | 返回 nil，创建钱包并激活 |

各场景记录的共同证据为 restore_error=<nil>、wallets_after=1、active_after=true。正确输入的基本恢复测试通过，读取阶段使用错误密钥或错误根助记词则会失败并保持空状态。

### 原因与影响边界

提交入口对公开、可变的 RecoveredAccountManagementState 做了结构性检查，但没有在落库之前重新认证密文并核对 secret、locator、状态与数据引用、版本、哈希及明文的一致性。调用方混用不同恢复会话、错误传参或在读取后改变对象，都可能造成错误状态被当作成功恢复。

这是公开 SDK 提交边界的数据完整性问题，不是 AES 被破解，也没有证明外部攻击者可以绕过正常解密流程取得助记词。读取入口自身的错误凭据拒绝测试已通过；高层恢复流程的验证不能替代公开提交入口的防御。

### 修复边界

在钱包、profile、恢复标记及 managed data 落库之前，用给定身份和 secret 重新打开并验证各认证 envelope，核对 root / network / AccountID、状态版本和数据引用，并仅从验证后的内容构造恢复结果。另一种接口方向是使已验证恢复结果不可被调用方任意修改。失败时不得安装钱包或激活 profile，不用清标记或重导来掩盖根因。

## F3 — P2：显式 Runtime Stop 后，旧存储授权仍可取得并成功执行

### 位置

- sdk/wallet/interface.go：Manager.Stop。
- sdk/wallet/account_management_profile.go：clearAccountManagementSessionLocked。
- sdk/wallet/account_storage_operation.go：PendingAccountStorageAuthorization / UseAccountStorageAuthorization。

### 复现与影响

TestSDKAccountReviewAuthorization/RuntimeStopDiscardsGrant：初始化并确认授权，调用 Manager.Stop，随后读取授权并实际调用 UseAccountStorageAuthorization。

实测日志：

```text
account-review: after_stop pending=true pending_error=<nil> use_error=<nil> callback_called=true
```

Stop 会清理账户 secret/password 并推进会话 generation，但没有使 pending storage grant 失效；授权校验仍允许旧回调被调用。测试回调没有资金操作，所以这一证据证明的是会话授权边界缺口，不是已发生支付或资产损失。

### 修复边界

显式 Stop / Close 的会话终止应使旧 pending grant 失效，并正确处理仍在执行的回调，不能让旧回调消费新会话的替代授权。

这与 PWA 锁屏不同：界面锁屏不应停止 SDK 后台安全任务，不应因此删除授权。运行中的 UnlockWallet 只验证密码、切换钱包不改变根账户和授权的测试已通过，不能为了修复 Stop 而回退这些语义。

## 新增 E2E 覆盖与结果

以下统计不重复计入 Go 的父测试容器。

| 场景组 | 场景数 | 通过 | 失败 |
| --- | ---: | ---: | ---: |
| 基础发布恢复、错误凭据只读拒绝、无变更同步、已有钱包保护、目录保护 | 5 | 5 | 0 |
| 授权一次消费、失败重试用途、不可变快照、并发、取消替换、切换与重新验证、禁用跨链切换、Stop | 8 | 7 | 1 |
| 恢复提交输入一致性 | 6 | 0 | 6 |
| 未预绑定的首次激活 | 1 | 0 | 1 |
| 多设备独立变更合并、同步不产生回声版本、删除当前钱包后根账户回退及第三设备恢复 | 3 | 3 | 0 |
| 真实磁盘重启与密码变更、错误密码无半解锁、钱包删除持久化 | 1 | 1 | 0 |
| 新增合计 | 24 | 16 | 8 |
| 既有付费账户 E2E 与多设备生命周期 E2E | 2 | 2 | 0 |
| 本轮账户 E2E 合计 | 26 | 18 | 8 |

Go JSON run_count=30，包括上表 26 个叶子场景及 4 个父测试容器。skipped=0，没有超时。覆盖清单中的源文件在最后一次测试执行期间没有检测到变化。

另执行 account 包全量测试和选定 wallet 账户回归测试，固定入口 TestAccountManagementReviewBaselineValidation 返回 exit code 0。该结果不等于 wallet 包全量测试通过。

## 本轮新增文件

业务测试均位于 sdk/e2e：

```text
e2e/account_management_review_e2e_test.go
e2e/account_management_review_authorization_e2e_test.go
e2e/account_management_review_multidevice_e2e_test.go
e2e/account_management_review_restart_e2e_test.go
```

固定证据入口：wallet/account_management_review_validation_test.go。入口只在显式点名时执行，避免在全仓测试中递归重复运行；业务 E2E 本身没有新增 build tag 或环境变量开关，可以被普通 go test ./... 发现。

## 复跑命令与证据

从 /Users/yingfeng/github/sat20wallet/sdk 执行：

```sh
# 新增测试与既有账户 E2E；当前保留缺陷红测，预期整体非零退出。
go test ./e2e -run '^(TestSDKAccountReview|TestRealSatoshiNetAccountManagement)' -count=1 -timeout=7m

# 本轮基线：account 包全量 + 选定 wallet 回归，并保存证据。
go test ./wallet -run '^TestAccountManagementReviewBaselineValidation$' -count=1 -timeout=10m

# 包装入口：保存 Go JSON 日志、结果、源文件哈希及函数位置。
go test ./wallet -run '^TestAccountManagementReviewValidation$' -count=1 -timeout=10m
```

最终结果文件（路径相对 SDK）：

```text
review-evidence/dkvs-release-account-management-20261003T205146.355311000.json
review-evidence/dkvs-release-account-management-20261003T205146.355311000.jsonl
review-evidence/TestAccountManagementReviewValidation-sources.json
review-evidence/TestAccountManagementReviewBaselineValidation-sources.json
```

结果文件沿用既有 DKVS 证据工具的命名前缀；本次真正执行的账户选择器和 verdicts 在文件内有明确记录。原始 JSONL 日志、失败节点日志路径与源文件快照可用于后续红绿验证。

## 尚未验收与后续门槛

此轮没有执行三个仓库的完整 go test ./...、race detector、浏览器/WASM/PWA 界面验收、运行中进程强杀的崩溃注入，以及所有存储/网络故障组合。授权并发 E2E 通过不等于完成 race 检测；真实 Go SDK E2E 通过也不等于浏览器验收通过。

建议先修复 F1 与 F2，再收敛 F3 的显式会话终止边界；以本轮 8 个失败场景转绿且原有正向测试继续通过为最低验收门槛，之后再做完整回归。生产代码修复未在本轮执行。
