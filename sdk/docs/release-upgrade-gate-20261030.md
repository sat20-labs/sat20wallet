# SDK 版本升级验收门禁（2026-10-30）

本页记录候选的实际验证范围，不构成提交、发布或生产数据库迁移许可。SDK、RGB、智能合约、账户管理和 lucky mining 为首次上线；**已有通道和通道合约已有历史数据，升级兼容另行验收**。新模块不增加旧格式兼容。

2026-10-30 追加门禁：用户明确未激活且无备份的新账户可直接本地操作，激活后才要求在线同步。旧“一律等 DKVS 写资格”的生产 UI 方案撤回；两处 wallet-basic/mint 测试等待的通过收据只作历史记录，不作为新规则通过证据。根因与待审边界见 `account-preactivation-local-write-review-20261030.md`。

## 固定候选与已有结果

- 当前 sat20wallet HEAD 为 `fde00ec629f689839264bb328d0a08574d622ed0`，工作树含其他会话的未提交改动；最终候选尚未冻结，不能以 HEAD 单独标识版本。完整 `./wallet` v5 的源码/依赖哈希、patch、命令、日志和独立 inventory 存于 `sdk/data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/`。v5 exit 0：2131 个唯一叶子中 2103 pass、28 skip、0 fail；未运行的外层 E2E 不计入。
- 两项已批准的账户生产修复关联 `-race` 定向 3/3 pass，日志 `local-switch-account-fixes-race-v2-20261030.jsonl`。这不是完整 `./wallet -race`。
- 本地 fake-L1 `TestSDKAccountPWAConnectedBrowser` v3 exit 1，完整日志 `local-account-pwa-browser-v3-20261030.jsonl`，SHA-256 `b0cace6420fb8064c60a290916a29b9f570cc286e0276553a7e7967c9a8e75ca`。运行器 `caseSummary` 为 66 必跑、54 pass、12 fail、0 not-run；顶层测试 fail，无 panic 记录。先前按单行解析误记为 11 fail/1 无终态：后台状态刷新用例的 fail JSON 被 Go 输出拆行，原运行器仍明确计为 fail。其余失败主要在新页面取得在线写资格前进入写动作，DID 页面也实际显示 DKVS 未同步；后台状态刷新另需独立定位。不能把统一等待加入测试后直接宣布 UI 门禁通过。
- 本地 fake-L1 `TestSDKWalletPWAConnectedBrowser` v1 exit 1，日志 SHA-256 `9d136303e7f821fc75ea46548b87b663e9225bb23e72c19d40186169fd7d1302`。13 个隔离组全部闭合，7 pass、6 fail；运行器合计 104 required、78 pass、5 fail、21 not-run。`rgb-nia-workflow` 8 个业务 case 通过，但后台 RGB 刷新发生 `account management snapshot changed` 的组级页面错误，不能算通过。其余失败覆盖公开 BTC 提款未产生待确认交易、基础钱包/铸造夹具过早进入 DKVS 受管写、biometric 密码变更后旧绑定未清、Core 安全退出后本地 reservation 响应缺失且 WASM 报已退出。完整收据与分组见 `sdk/docs/local-acceptance-checkpoint-20261030.md` 和同前缀 `local-wallet-pwa-browser-v1-20261030.*`。无 Go 日志 `panic:`，但不能据此排除 WASM 运行时退出。共享 XAP 依赖在本轮前后已有改动，最终候选须重新固定输入。
- 仅针对基础钱包直接 `addAccount` 和铸造夹具直接 `refreshRGB11State` 的在线写资格前置，两个测试脚本做了最小修正，选择原两项失败 case 定向复跑 exit 0：两个隔离组均 `2/2` pass，无 fail/not-run；日志 SHA-256 `82502a654d1dc5026c82c680196b758208aa998024d75be976625cd05923ebf2`，同目录前缀 `local-wallet-pwa-fixture-targeted-v1-20261030.*`。这不是完整钱包 PWA 重验，也不能替代待审核的真实 UI 写资格门禁。
- 后续三组定向诊断 exit 1：POS 18/1/1（`withdraw` 返回 fake-L1 `tx not found`、无新待确认交易），RGB NIA 8/0/1（业务通过但本地投影基线冲突仍触发页面错误），Node Core stake/unstake 3/0/0（先前 WASM 退出未复现）。输入、日志及退出收据为 `local-wallet-pwa-three-diagnostics-v1-20261030.*`，日志 SHA-256 `2661876937b8e1dca6dcff35cac1c65e786e4c24ba51383f2fbdd875bbb7349a`。这不关闭原完整钱包门禁。
- POS 提现失败的夹具证据已补充定位：缺失的 raw tx ID 为 `c2f24bca30cb07eaf1842d1b0ee1f93140659d037d0887d66a240f2bac3b1735`。原 heartbeat 用 `templateLockedOutPoint("pwa-pos:heartbeat", 0)` 虚构 outpoint，`SeedUTXO` 仅写 outputs/assets；rawtx/simpleinfo 仅查 transactions。用户已批准测试夹具修正：现仅 heartbeat 在节点启动前用 `FundAddress` 生成真实序列化交易、同 txid outpoint 和资产记录，原提现金额、fee、确认断言保留；**修后浏览器尚未执行**，其他 stake seed 不变。
- `transcend/stp.TestAll` 在原仓库、本地临时数据库执行，exit 1，77.281 秒；日志 SHA-256 `a2839dd4ae4c9cdd751714cdf2f0fbfe128ad3bd3641904c6ebba55e11049740`。`runAll_wallet` 的末项 `TestCoreNode` 在质押后首次打开通道被 `channel history exists without local recovery material` 拒绝；后续通道组和合约组未运行。命令、PID、四仓 HEAD/status、模块哈希、日志及终态存 `transcend/data/sdk-acceptance-20261030/transcend-testall-v1-20261030.*`，2791 个 Go 文件的聚合输入指纹为 `ea49d7c4e4826985344905106fbd88b351e7a429aeb96b3fc387c4acd50e9a6e`。根因范围及保护边界见同仓 `docs/testall-first-open-stake-20261030.md`。

## 本版本必须闭合

| 门禁 | 已有证据 | 仍需验收 |
| --- | --- | --- |
| SDK 钱包、账户、RGB | 完整 wallet v5 与分组 race；冷恢复后再次使用、离线只读及真实同键冲突已有定向回归 | 修改后的同版源码复核、最终独立 review；任何相关生产修复后重新跑受影响组 |
| PWA 与 WASM | fake-L1 启动真实临时 Core/Miner/Bootstrap、当次临时 WASM 和真实 IndexedDB；账户 v3 共 54 case pass，钱包 v1 共 78 case pass | 账户 v3 的 12 fail；钱包 v1 的 6 失败组和 21 not-run；`TestSDKCoreModulesE2E`、Guardian 等必跑；发布 WASM 必须从同一 SDK 候选构建并核哈希，旧 `pwa/public/wasm` 不算 |
| transcend / 智能合约 | `TestRunAll_contract` 的旧固定输入曾 pass；当前 `TestAll` 到 `TestCoreNode` 失败 | 修复并复跑同版 `TestAll`；当前通道组和合约组未执行，不能把 Go 编译或旧结果当作通过 |
| lucky mining | 属首次上线，本线程未修改其代码 | 汇总其他会话同一候选的功能、失败、跳过、依赖和 review 收据；缺收据记未执行 |
| 真实测试环境 | 本地隔离 fake-L1 与临时节点 | 先冻结版本和全部本地门禁，再在批准的真实测试环境逐项验证升级、冷恢复、通道资金安全与回退；目前未获部署授权 |

## 历史通道与通道合约升级矩阵

| 历史状态 → 新版操作 | 当前证据 | 证据缺口 |
| --- | --- | --- |
| 旧版 plain/ORDX gob 通道 → 新版打开/列举 | `wallet/testdata/legacy_pre_native` 的旧程序生成合成样本；`TestLegacyGobChannelReadsPreserveOldFieldsAndAvoidNativeRecovery` 比较旧导出字段及数据库不变 | 样本非真实签名链上交易或真实生产库 |
| 旧承诺/余额 → 新版更新、保存、重启 | `TestLegacyGobChannelLedgerContinuesAcrossPhysicalDatabaseRestart` 验证余额、承诺交易字节和重启后字段语义 | 真实旧版本双方签名、继续支付、强关和链上结算尚未证明 |
| 旧 close/splice reservation → 新版加载/监控状态保存 | `TestLegacyGobPendingReservationsPreservePackagesAcrossRestart` 验证原交易包字节与重启 | 广播、确认、失败恢复、watchtower 惩罚材料未由该样本证明 |
| 旧 STP 通道、reservation、watchtower、status → 新版加载 | `transcend/stp/old_data_compat_test.go` 有四项入口 | 均依赖 `STP_OLD_DATA_DB` 指向**隔离复制的**历史 LevelDB；未提供该输入时 skip，不是通过；不能设置删除旧失败记录的环境开关 |
| 旧通道合约运行状态 → 新版加载、继续操作、安全退出 | `transcend/stp/db.go` 有运行状态加载入口；`TestContractStateMigrationSeparatesCompletedReservation` 用当前编码器**模拟** 0.1.4 仅保存 deploy reservation 的布局，并检查重启后的独立状态与查询；新建态 `TestAll` 不代表历史升级 | 仍缺由可验证旧版本实际生成的合约状态 fixture，以及旧资产承诺比较、继续业务或安全退出回归；该迁移测试不在本次 `TestAll` 选择器内 |

旧版样本说明见 `wallet/testdata/legacy_pre_native/README.md`；它明确不证明链/对端 E2E，也不包含旧 punish 和合约加载。其 manifest 指向旧 commit `1a9aa9d67d741ba66556982fa53b1b81f4d41559`，本轮独立核对其中 7 个源码哈希均与该 Git 对象一致，4 个 fixture 的运行时哈希仍由现有测试校验。另在 transcend Git 历史中核到迁移前 commit `9c86df0cfb71dfe9b084a6373ae88b6af1e37d18`：该版 `DB_VERSION=0.1.4`，完成的合约通过 `saveReservation` 以旧 `ContractDeployDataInDB` 编码写入 `resv-` 命名空间，尚无独立 `contract-state-` 记录；下一提交 `4c2fcbcf` 才引入 0.1.5 独立运行状态。旧源码对象可读，但其 `go.mod` 使用本地 SDK/Indexer/SatoshiNet/RGB11 replace，本轮未固定当时对应的依赖提交、未执行旧版生成器，也没有旧版生成的合约 fixture；现有迁移测试仍是当前编码器模拟布局，不能称作真实旧字节升级已通过。发布前应使用可验证历史版本及固定依赖生成不含用户秘密的合约 fixture，或经批准的隔离数据库副本补齐缺口。不得在真实钱包数据库上试迁移。升级前的完整备份、版本/数据库哈希及恢复演练是回退边界；升级写入后是否可直接运行旧二进制尚未证明，不能承诺直接降级。任何必要生产兼容修复先按根目录 `AGENTS.md` 提出最小方案审核。

## 发布结论的记录格式

每一门禁记录同一候选源码与本地 replace 依赖哈希、准确命令、日志、exit、pass/fail/skip/not-run、panic 和最后 review；失败修复后标明失效的旧结果。构建和升级步骤须以冻结候选和隔离恢复演练核实；真实环境部署、推送和发布另行授权。当前结论：**未达到可进入发布/升级的验收门槛**。
