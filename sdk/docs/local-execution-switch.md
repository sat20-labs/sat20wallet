# MCP 与本地 Codex 切换约定及当前交接

更新时间：2026-10-30 10:00 UTC+8。本文是工程交接记录，不是新增权限或自动任务框架。

## 固定约定

1. 执行环境写明：用户 Mac 的 /Users/yingfeng/github/sat20wallet；MCP 和本地 Codex 只是两种访问方式。不要称它为云电脑。
2. 始终在用户指定的原项目目录工作，不创建隔离代码 worktree，不 reset/clean/stash 覆盖其他会话的修改。
3. 同一时刻只保留一个源码修改者。接手前核对当前修改者、正在执行的命令、实际日志及 exit 文件；旧线程历史不能替代当前磁盘事实。
4. 长测试必须把完整日志和 exit code 留在 sdk/data 或项目指定 data 下。工具请求超时不代表测试停止。先检查原进程和日志终态，不重复启动、不杀其他任务进程。
5. 可留存的设计、根因、审批、测试结果放项目 docs/data；tmp 只放临时运行材料。不得把临时路径当作唯一交接依据。
6. 已批准事项按动作与目录记录，不因更换操作方式重新申请同一项批准。本地 SDK/PWA 开发、构建、测试及 Go 缓存访问已获批准；远程 102 上 /data/github 仅查看的限制不适用于本地开发。
7. 未批准事项单独列明；不把其他会话或旧批次的授权扩大为本轮新生产修改、提交、发布。
8. 切换后先读取仓库 AGENTS.md 和本检查点，再核对 staged/unstaged/untracked。测试覆盖要检查 panic、包终态和完整 Test/Example 清单，不只计算已启动测试的结束事件。

## 当前切换状态

- 当前源码修改者：原本地 Codex 线程 01a17883-8700-76c6-94fa-60ef1b25002c 已于 2026-10-30 10:00 UTC+8 接手；主线程 MCP 已停止源码修改，同一时刻仅该线程写源码。
- 用户已要求在当前完整 wallet 测试结束并落盘后，切回原本的本地 Codex。
- 拟接手线程：01a17883-8700-76c6-94fa-60ef1b25002c。其旧记录止于 v27/额度失败，明显早于当前磁盘状态；不得按旧快照回退。
- 2026-10-30 09:38 已确认 yingfengdeMacBook-Pro.local connected、attached、authorized。
- 已结束：scripts/run-wallet-full-mcp-acceptance.sh，完整 ./wallet v5，exit0，1095.065秒；没有需接管的本轮测试进程。
- 日志目录：sdk/data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028/
- 当前日志：wallet-full-mcp-v5-20261030.jsonl；终态：wallet-full-mcp-v5-20261030.exit。
- 当前脚本 SHA256：657d6a82e3664d9f05f2011d9170ea2f4afeaccc22f562a48fcd15399c5278a5。
- 源文件与依赖 hash、tracked patch、git status 已由该脚本保存为 v5 输入记录。其工具调用超时后也必须等待落盘终态。
- 另有 syntax/diff preflight 已完成：scripts/check-pwa-account-runner-mcp.sh exit0；实际 Node /usr/local/bin/node。没有正在执行的 PWA 浏览器测试。
- v5 终态已核对：exit 文件实际为 0；1131顶层项目、2131叶子、2103 pass、0 fail、28 skip；无panic、无未结束项。独立 Test/Example inventory expected=started=1131，missing=[]。本地线程接手后不重复跑v5。

## 用户批准与实现边界

用户 2026-10-29 批准自身 ACK 旧快照修复，2026-10-30 09:27 明确批准以下第二、三项：
1. account_management_state.go：冲突分类前检验已捕获快照是否仍有效；自己的后台 ACK 已推进 profile 时，复用原有有界重试，不将自己的成功提交误判为远端冲突。
2. 同文件冷恢复 profile 构造：ManagedDataGeneration 初始化为1，与新建/激活一致。原先0导致已确认 outbox 的 ACK 无法本地收尾。保留 ACK 非零约束，不加入兼容逻辑。
3. sdk/go.mod：仅增加 xaplib/chainindex v0.0.0 indirect require 和 ../../xaplib/chainindex replace。该子模块无第三方依赖，go.sum 与其余库版本未改；e2e 只读依赖编译已通过。

除以上生产修复与依赖外，本阶段主要为测试前置和旧断言的校正。没有修改 satoshinet 底层 DKVS；没有 stage、commit、push、生产服务重启或源码外传。

业务原则仍为：本地数据库是主数据；先取得已验证基线再允许写；观察到离线后只读；本地业务原子提交及脏标记后异步备份；真实冲突不得静默重新授权或字段自动合并。RGB 完整恢复及继续使用所需材料不能省略；公共注册由服务端写入共享目录。RGB 名称方案已定，区块浏览器和 WebAuthn 延期，本轮不要重新设计协议。

## 已验证结果与限制

- 完整 TestRunAll_contract 先前真实通过，原六个 RGB11 bind DKVS 未同步 case 的前置已修复；这是历史固定输入收据，不代替当前全仓结果。
- 完整 wallet v3 曾在 R1 receipt expansion nil-pointer panic 中止。先前“已跑到结尾”报告有误，已向用户更正，不能再引用为完整覆盖。
- 完整 wallet v4：1131顶层项目（1128 Test +3 Example），2131叶子：2087 pass、16 fail、28 skip，941.754秒，exit1，无panic；独立清单核对 expected=started=1131、missing=[]。所有 skip 不计通过。
- v4 的13个非 generation 失败，已由 wallet-tail-fixtures-mcp-v1 定向闭合；相同11个顶层测试的 -race v2 exit0，346.186秒。保留真实网络无锁/受控竞争测试，没有靠全局关闭生产后台逻辑修复。
- account/...、wallet/dkvs/...、wallet/rgb11/... 共5个包 -race 全pass；日志 account-rgb-dkvs-packages-race-mcp-v1-20261030.jsonl。
- RGB scope/recovery -race 整批pass；paid activation -race、自身 ACK 快照和真实并发冲突拒绝 -race 整批pass。详见 unified-acceptance-20261028.md。
- 批准 generation=1 后，新初始化回归pass。rgb-peer-backup-mcp-v3 的5个顶层选择全部pass，含原 R1 witness 两场景。fixture 额外按业务交接规则做到：源备份→目标恢复/规范化→旧端恢复编辑前先同步目标基线；这不是自动合并。
- e2e-build-mcp-v2-20261030.log 为 go test -mod=readonly -run '^$' ./e2e，exit0，仅表示编译，不是浏览器通过。
- PWA 两个 runner 文件已修改同一个 offline usage case及必跑名称：真实观察网络失败后拒绝 rename/add/delete，缓存可读，重载不写，重连接收在线端更新。node --check 与限定 git diff --check pass，浏览器未运行。
- 仍需当前 v5 整包终态、修复后关联 race、PWA fake-L1 浏览器验收和当前源码最终 review。不能提交结论为“已全部通过”。

## 接手后的顺序

1. 核对 v5 真正终态、完整清单、panic 与失败明细；主线程已在跑的命令不要重复启动。
2. 核对当前 git 变更及输入 hash，保留所有其他会话工作。若有新的生产缺陷，遵守 AGENTS.md：先给根因与最小方案取得批准；已批准的三项不重复申请。
3. 补必要定向 race 和整包回归，不把测试中的假前置、忽略错误或旧同步语义当作放宽业务校验的理由。
4. PWA 使用 SDK ./e2e 的 TestSDKAccountPWAConnectedBrowser、TestSDKWalletPWAConnectedBrowser、TestSDKCoreModulesE2E 等既有隔离 fake-L1 框架。真实临时 Core/Miner/Bootstrap 与当前临时 WASM、真实 IndexedDB；只L1为受控假索引器。不得运行 scripts/live、generic-send、l2-contract 或复用现有 CDP/生产节点。
5. 不覆盖现有 pwa/public/wasm 产物，不用旧 WASM 当作当前改动的验收。PWA测试中其余旧自动合并/离线写预期需逐项检查，保留数据与错误边界断言，不删必跑项、不添默认skip/tag。
6. 所有结果和待办更新到 unified-acceptance-20261028.md 及 data 收据。测试、最终review都达标后给出可提交结论；未经本轮明确授权不提交发布。

## 已知脚本说明

所有 run-*-mcp.sh 名称只是这轮的历史来源，脚本可由本地 Codex 或人工直接运行，不依赖 MCP。无需为切换重写脚本、迁移代码或搭建新调度系统。当前 inspect-wallet-full-mcp.sh 和 check-wallet-test-inventory-mcp.sh 已指向 v5，应先阅读固定文件名；改输出版本时保留旧日志。
