# 验收收尾协调（2026-10-30 14:23 UTC）

本文件是当前协调入口；逐次历史与哈希仍保存在 unified-acceptance-20261028.md 和 data 收据中。当前结论：按基线全集加最终改动影响集合，本轮限定 PWA 实际流程的本地功能验收已收敛，可进入经授权的真实测试环境验证；不是主网发布或真实历史数据库升级通过。

## 最新范围与权限

- 主任务：SDK / PWA / transcend 上线验收。XAP 监看已由用户取消，自动任务已关闭，不再访问 XAP。
- 用户 13:10 批准测试清单 2–6：PWA local-only 等待、SDK 本地模式语义与真实激活 fixture、ACK 重配置、sats L1 等待和可靠 cleanup。
- 用户 13:22 批准 SDK RGB 统一数据更新层：上层提交操作请求，模块内部组织快照、锁外证据、校验和提交；不是在各 API 分散加重试。仅提交前同身份本地基线过时可集中重建，广播/ACK 不重放，不改 SatoshiNet DKVS。
- 用户 13:20 限定测试于 PWA 实际操作与正常后台重叠，不扩展任意 SDK 并发/低概率人造交错。已有历史诊断不擅自删除，但不能计为 PWA 场景证据。
- 未授权 stage / commit / push / 部署。MCP execute_command 服务实现、接口、配置、执行模式不得未经批准修改。原项目目录开发；测试数据独立临时目录，证据留项目 docs/data。

## 当前代码候选

SDK RGB 统一候选入口已实现，生产冻结时间 13:38:58 UTC。详见 rgb11-candidate-20261030.md。Accept、Prepare receive、Refresh 统一内部执行，保留短本地提交门和原子 DB/RAM 记录发布；本地 stale 使用独立内部错误，不误标有效投影不可用。网络不持钱包/存储锁。13:43 仅更正新增测试的注释：WASM Refresh 用 context.Background，人工取消只能作为内部边界检查，不能称 PWA 路由取消。

## 已有结果与版本边界

- 账户浏览器 v7：63/66 pass，3 fail；随后批准的恢复 focused 3/3 pass。不能把两次拼成新候选完整66项终态。
- POS 最终 fixture v4：27/27 pass，固定输入。包含真实 raw funding 的 heartbeat/core-stake/miner-stake，资产与 fee 断言保留。
- PWA 非RGB：L1协议4、DApp9、工具9、挖矿3、节点6均pass；钱包基础复验16/16 pass且hash稳定。Mint早期10/10跨源码变更的结果不采用；最终 unified 10/10通过，生产依赖前后稳定，与NIA使用相同实际WASM hash。
- NIA旧候选8个业务case通过但pageerror失败，不能计通过。新统一候选NIA 9/9通过（含无未处理页面错误），IFA/UDA三项普通场景3/3通过；后三项Go/embed构建输入稳定但临时WASM二进制hash未直接采集，保留证据限制。
- wallet最新账户/恢复10入口17叶pass，固定输入。另5项获批测试4pass，首次钱包用例误把排队当上传；修正后已随统一模块定向组通过。
- 统一模块定向4入口全pass，包13.065秒，无panic。其中 TestRGBReceiveRefreshOverlap 是实际PWA收款/后台刷新重叠，TestRGBCancelBeforeCommit 仅内部context边界。既有公共离线门禁4叶通过；双scope恢复身份负例和恢复后两个资产继续使用4叶通过，不扩写压力矩阵。
- wallet补覆盖925名称用同一旧二进制加正确cwd续跑全部覆盖；合并898pass/21fail/6skip，不是一次干净全包通过。21失败包含9个人工选择器误开启的opt-in诊断，普通失败须按后续定向结果逐项闭合。旧Gob升级6个实际入口在正确cwd已pass，不能代替全部升级验收。
- transcend TestAll v4不完整失败：sats查询L1尚未active，随后失败cleanup未停sender导致等待。后5入口单独21叶全部pass。sats修正后单例pass；TestAll v5于13:22:03启动并已exit0，2470.60秒、54个完成标记、31叶pass。该二进制早于新RGB实现，不能冒称覆盖新代码；最终新候选TestChannel_rgb11+TestContractRGB11已exit0、19叶pass、前后生产/依赖hash一致。v5期间3个SDK生产文件修改并新增candidate_exec，已核对内容hash。

## 当前执行分工

- Sol xhigh 唯一源码写者：01a17883-8700-76c6-94fa-60ef1b25002c。当前源码已冻结，后续修改须协调输入，不改PWA/Transcend冻结文件。
- PWA Luna max：01a18c69-90f2-7564-8082-c85efd3200f4，NIA unified、Mint unified、IFA/UDA基本流程均终态通过，当前无运行测试。
- wallet Luna max：01a18c77-a4ac-7290-9805-daa3444a60f4，公共门禁及双scope恢复均终态通过，当前只更新最终验收ledger，不运行测试。使用精确 -run，禁止大名单意外开启旧 opt-in 验证器。
- transcend Luna max：01a18c8d-e46f-7601-b038-603f489540c2，TestAll v5及最终RGB影响两组均终态通过。只管理自己的测试进程，按日志实际 finished 标记判断进展。
- 主协调者通过 MCP 亲读源码与日志、核对失败根源、候选边界和测试范围，不能只等待worker文字结果。

## 剩余决策与收尾

- 双scope RGB恢复预期已由用户14:03批准并修正；基线检查、两个scope身份错配负例及恢复后分别继续使用资产共4叶通过。
- root-wrapper人工未激活fixture强求远端PUT冲突、ACK手工重复同值PutBatch都未证明对应PWA正常时序，暂停扩写，保留诊断原文件，不当成已确认生产故障。
- 已完成NIA/Mint、IFA/UDA及受影响RGB通道/合约普通流程。最终限定只读复核未发现新增重大阻塞。WebAuthn/biometric与浏览器名称显示仍延期。
- 下一阶段需要明确真实测试环境、隔离历史通道数据库样本与升级/回退演练安排；旧程序生成Gob样本通过不等于真实旧库资金状态演练通过。提交/部署仍需另行确认，本轮未stage/commit/push/deploy。
- 需要分别说明固定候选通过、旧二进制通过、未跑、失败、范围外诊断；不得将日志启动/编译/零退出之外的推算计作验收通过。
