# SDK 回归范围

默认回归只执行功能接口测试和 E2E。模块内部测试保留在源码中；修改该模块时再单独执行，不因其他模块或 PWA 变化重跑所有内部测试。

默认项目入口为 `scripts/sat20wallet-sdk-native.sh`；两个旧项目包装脚本的 `all` 选项也转入同一范围，保留包装器原有外层时间限制。长批次仍使用已验证的 screen 启动方式，不修改 MCP 的命令或超时配置。

`scripts/sdk-functional-roots.tsv` 逐项列出默认根用例、职责和源码位置。清单按实际行为选择，不按文件名中的 `api`、`e2e` 或所在目录推断：

- `wallet`：70 个根，覆盖公开钱包导入/创建/密码、账户激活与备份、同步取消/离线写入门禁、RGB 发行/收发/取消/通道接口、SilentPay 的真实签名与冷重开。
- `e2e`：58 个根，覆盖真实临时节点的账户、DKVS、合约、消息、Agent、Bitcoin Core/SilentPay 及 Core 父入口。
- Core 父入口继续负责三段共 16 个原生业务组，只执行一次。最新只读基线中 Core 本身耗时 62 分 34 秒，已超过整套 60 分钟目标；当前分组优化不能单独达到目标，仍需针对 Core 长等待单独优化。不能直接追加 `TestSDKCoreModulesConnectedE2E` 或验证 wrapper 再执行一遍。

EVM 合约保留六个独立默认根。共享网络组的六个业务叶都通过，但同口径实测暖启动耗时约 166.8 秒，独立根合计约 74.0 秒；共享组新增的逐笔出块等待抵消了环境节省，因此暂不纳入默认范围。

DKVS paid mailbox 共享组串联 `ActiveMailbox`、`AutopayNameAndMailboxSync` 和 `ReleaseReviewNetwork`；三根历史暖启动合计 87.49 秒、9 次节点启动，本次共享组实测 15.28 秒、3 次启动，节省 72.21 秒（82.5%）。原 `SenderMsgID == 0` 断言仍保留；Autopay 使用独立 sender/recipient 账号，避免前一 mailbox case 消耗消息序号。快照验证 `/name/8888.btc` 在三节点从不存在变为 seq=1 的相同记录，mailbox 前缀前后为空。

DKVS no-plugin 共享组覆盖 `BoundWalletRPC`、`ActiveState`、`LaunchBoundReplay` 和 `ActiveManager`；同口径暖启动比较中，四个 standalone 根约 43.8–45.4 秒，合入既有共享组后增加约 10.8 秒，净省约 33–35 秒，并少 12 次节点启动。智能合约共享组替代 `SmartContractsAdditional` 和 `SmartContractsFunding` 两根；同进程去除一次性插件构建后，暖启动从约 69.7 秒降为 46.7 秒。
账户 no-plugin 共享组末尾现串联 `TestSDKAccountDKVSSyncGate` 的 12 个场景，原 standalone 函数仍保留给显式单独重跑，但已从默认清单移除。配对实测中 standalone root 为 189.44 秒（含首次插件构建 72.630 秒）并启动 3 个节点；共享账户组的 SyncGate 子组为 109.23 秒，整组 238.55 秒且只启动 3 个节点。按同批首次构建归一后，standalone 约 116.81 秒，子组估算快约 7.58 秒；两个独立默认 root 合并为一个，6 次节点启动降为 3 次。12 个业务场景中 11 PASS、原有并发编辑场景 1 SKIP；每个实际账户的绑定 seq/hash 与个人 DKVS 状态前后差值均符合预期。该组 Core 重启仍位于原顺序，独立 fake RPC 子场景与 seed 4 的 skip 均保留。

模板 review 共享组串联 `SmartContractsTemplates`、`SmartContractTemplateReleaseEscrow` 和 `SmartContractReleaseAssetValidation`；历史 warm 根合计 75.51 秒、9 次节点启动。本次配对实测共享组 47.59 秒、3 次启动，节省 27.92 秒（37.0%）；同批独立根扣除一次性 55.418 秒插件构建后合计约 82.84 秒，仍比共享组多约 35.25 秒。Templates 与 escrow 断言链高、tip 及 owner 资产状态变化；release validation 负例断言 tip 与资产状态不变。

Agent 共享组 2 个业务叶均通过，但暖启动约 130.6 秒，独立根归一化约 129.0 秒，慢约 1.6 秒；因此仍保留独立入口，不纳入默认组。各已验证共享组均保留原业务断言、独立 wrapper 与单 case/整组重跑入口。

18 个 Go package 仍是源码包清单；它不等于本次执行了 18 包的内部测试。默认执行结果必须分别报告选中的两个 package、128 个根、Core 16 组，以及 PASS / FAIL / SKIP / NOTRUN。框架解析器、codec、merge、projection 等未选中的内部用例属于 NOTRUN，不能计为 PASS。

PWA/browser 和持久 real-regtest 入口继续通过原有独立 runner 执行，不从默认原生回归偷偷启动。各功能入口见 [现有入口索引](e2e-module-entries-20261030.md)。新增业务用例需要明确登记，不能依靠宽泛名称正则自动纳入；新增包也需要检查范围。

两个独立 Bitcoin Core/SilentPay E2E 保留原有环境规则：本地没有 `bitcoind` 时明确 SKIP，CI 的 `SAT20_E2E_REQUIRE_BITCOIND=1` 要求真实执行。SKIP 必须单列为未验证的真实 L1 覆盖，不得计为 PASS；不能为了全绿从清单移除它们。Core 的 16 个原生业务组仍不允许 SKIP。

现有账户 E2E 另有 7 个源码明确跳过的“多设备同时编辑”叶子；本次入口调整不修改这些业务规则或删掉用例。报告同时列出根与叶子的计数和全部 SKIP 名称，父根 PASS 不代表跳过的子场景已经验证。

## 修改模块后的内部测试

使用原有 Go 入口，无需删除测试、增加 build tag 或复制实现。命令在 `sdk` 执行，例如：

```sh
# 只有 account 编码/恢复算法本身修改时
go test -mod=readonly -count=1 ./account/...

# 只有 RGB 引擎适配与恢复实现修改时
go test -mod=readonly -count=1 ./wallet/rgb11

# wallet 同包中只选择实际修改模块的现有内部根
go test -mod=readonly -count=1 -run '^TestNativeForceClose(AtomicBeforeBroadcast|SweepReplaysExactSignedBytesAfterRestart)$' ./wallet
```

模块内部结果与默认接口/E2E结果分别记录，零命中不是通过。不要用工作树是否有任意 Changes 自动选择所有内部测试：Changes 中可能包含用户尚待审核的其他任务。

失败处理顺序保持为：先完成所属组，统一修复其失败用例，只复测失败用例，全绿后复测所属组，最后执行默认完整接口/E2E回归。当前运行中的旧批次保留其原始范围和结果；清单切换不追溯修改历史 status。
