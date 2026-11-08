# 账户管理 1.1

批准日期：2026-11-01。用户已明确同意实施。本文整理已批准的账户管理设计及独立 DKVS key/value 清单；原稿中的“待审核”表示批准前状态，不构成实施阻塞。本文描述目标协议，实施进度和测试结果另行记录，不表示全部代码已完成。历史设计文件保留，不做旧版上线兼容、双写或迁移。

## 职责与范围

满足正常 PWA 使用，沿用单 Manager，降低耦合和相互影响；不建设极端并发、任意故障组合或通用事务框架。业务模块继续拥有业务规则、数据库和交易流程；账户管理只管理其恢复资料。DKVS 仅负责单 KV 保存与同步，不承担账户业务和跨 key 事务。

业务先可靠保存本地，再通知账户备份。备份失败不回滚本地、不重复业务。重要 KV 本地完整持久保存，远端 TTL 不删除本地副本。切账户、网络、服务来源或 Manager 所有者后，旧异步结果不能提交。网络请求不持有业务数据库锁。新增源码标识符不超过 32 UTF-8 字节。

## 核心对象和远端版本

固定 wrapper 保存账户解密材料、当前恢复包及存储配置。固定 index 只管理 provider、scope、key、codec 和明确删除标记。wallets/global 保存钱包资料；RGB、SilentPay 等复用原 provider/scope payload，分别保存独立 KV。恢复包只负责根钱包或解密能力恢复，不承担全部业务快照。

DKVS Seq 就是各 KV 的远端版本。账户 value 不另存 Revision；移除 ManagedState.Revision、钱包项 Revision，以及绑定大 blob 的 DataRevision/DataHash。Version/Codec 仅表示格式。模块原有业务 payload 版本不改。本地变更计数只用于排队及 ACK，区分发送 N 和后来 N+1，不上传、不跨设备比较。

默认一个模块 scope 是一致性单元。必须一起恢复的数据放同一个 KV。不建设全账户原子快照、版本树或跨模块事务。现有单 KV 大小限制保留，超限明确失败；大对象拆分另审，不截断。

本地保留最新业务数据、完整重要 KV、最近核验的远端基线、持久变化标记和未决请求。尚未生成快照时靠持久标记重建，不能上传旧快照并把新本地内容标成已备份。本地敏感材料保持加密保护。

## 模块接入

沿用 AccountManagedDataProvider 的 Export/Validate/Import；只在必要时提供简单 Merge、精确 scope 导出。模块不处理 wrapper、ACK 或 DKVS 请求。

用户确认后、原业务提交或不可逆动作前检查可写；未激活允许普通本地操作，已激活检查同步资格。提交前核身份，防止旧调用跨账户落盘。确认前不因该操作写 DB、备份或发送任务，取消保持原业务语义。

在原事务/批次内 StageChange，记录 provider/scope 和本地计数，与业务同成同败。不同 DB 在自身可靠边界保留待交接标记，不假设跨 DB 事务。提交后 NotifyChanged 唤醒同一个账户任务；通知丢失仍能从持久标记恢复。

Export 只读稳定本地数据，不访问网络、不调用 Refresh、不等待备份；导出对应准确本地计数，期间变化则重取。GetStatus/RetryNow 统一供 PWA 使用。显式 WaitBackup 捕获调用时本地计数，普通业务不等待所有 dirty 清零。优先复用已有接口和任务。

## 激活与正常操作

未激活只保存本地，不备份远端。激活沿用临时 TTL 或既有付费选择、恢复资料交付和演练；付费资格失败不当作成功、不静默降级或额外付款。

wrapper、恢复包和配置先完整落本地再发布。核验服务身份、配置和可信远端基线后可写；首次业务数据备份后台进行，界面显示“首次备份进行中”，不能表示全部已备份。

新设备根钱包导入执行发现/恢复，不重新激活。已激活账户远端过期执行重传，不新建包、不重新保存分片。普通路径：用户确认 → 检查可写 → 本地事务及变化标记 → 通知 → 合并备份。

已发交易确认、既有 reservation 完成补记、链上重验由原模块独立安全对账，保持身份、证明和 DB 校验，不等待账户备份；之后通知。它不能用来绕过新交易入口检查。

## 同步、资格及模块隔离

首次/冷启动、切账户/网络/来源先取得可信基线再接受新的受管修改。核验来源、签名、完整性；网络失败和空增量不是“远端不存在”。健康基线下上传期间允许继续本地写。真实连接或同步失败暂停新的受管修改，保留数据，恢复成功后继续；结果不明先核对原请求。

账户状态表示锁定、待同步、可写或故障；备份状态表示待备份、上传中、已备份、待清理。ACK 删除失败不撤健康资格。模块导出、导入、格式或内容冲突只影响该模块及实际依赖；整体服务连接、账户身份或固定入口不可信才作为账户级问题。

## 简单合并

比较最新本地、最近同步基线和远端：只远端变则验证导入，只本地变则上传，相同则对齐、不重复写。双方变化只执行 provider 明确安全的简单合并，如互不相干钱包条目，不盲目取并集或按设备时间覆盖。

无法处理则保留双方数据并提示冲突，暂停受影响项上传/新修改，其他独立模块继续。目标是尽可能合并，不是杜绝冲突。导入保留简单进度以便重启续做；半恢复数据不能标就绪或覆盖远端。合并结果落本地后正常排队。

## 队列和最少更新

每账户一个合并任务，不给模块加定时器；只导出和更新受影响 key，同 key 不并发发送不同内容。A 的 N 上传中产生 N+1，B 也变化时，原 A 请求保持不变，下一轮合并最新 A/B，不上传中间状态、不改无关 KV。

发送前持久保存冻结内容、准确本地计数和远端条件。相同内容可省写，但仍确认对应计数；A→B→A 不漏本地收尾。多 KV 一轮调度各自成功/失败，A 成功 B 失败只重试 B。

新增先写模块数据，再登记 index；两步完成才称从根可恢复，退出后从本地任务续做。删除先发布 index Deleted，再清理数据；同步/过期重传先看目录，不能补回明确删除项。index 条件合并保留其他条目，首版不自动回收删除标记。

## ACK 和重试

ACK 只确认对应 key 冻结发送已被当前服务接受。可靠持久确认后清理请求；不改业务 DB、不导入自己旧快照、不推进前缀接收游标、不触发全模块同步、不授写资格。

ACK 只确认原发送 N，后来 N+1 仍待传。已确认 Seq 可用于该 key 后续条件写，但不表示其他远端变化已接收；迟到回执不能倒退较新核验基线。清理失败保留确认事实，幂等清理，不影响业务。确认事实未能落盘则保留原请求并核对，不回滚本地。

临时故障重试冻结原请求；明确条件冲突或授权过期结束旧尝试，保留待备份数据，刷新后生成合法新请求，不无限重放失效签名。远端相同内容可确认当前备份效果，不伪造历史请求成功。

## TTL 和恢复

wrapper、index、恢复包、模块完整副本不随远端 TTL 删除。确认过期缺失后复用原内容、PackageID、恢复配置，按当前规则重建有效期/签名/条件；不重放过期请求、不重新激活或生成分片。重建 Seq 按 DKVS 现有规则，不保证跨重建连续。

远端仍在则先比较、尽可能合并，不用旧设备覆盖；明确删除不是到期，不重传。完整设备可重建恢复链；所有副本丢失时根钱包不能凭空重建资料。

原设备重开：真实关闭/打开同 DB、解锁、读取队列和原请求、同步后继续，不复制 Manager 缓存。新设备：根钱包 → 固定 wrapper → 固定 index → 所有重要 KV → 核验并保存完整副本 → 模块导入。问答/guardian 先恢复根或解密能力，之后共用这条流程，不维护第二套业务恢复，不重复转账广播。

必要 GuardianCapsule 密文和投递引用随恢复包保存，新设备不依赖从 guardian mailbox 取回即可再次重传；不增加 guardian 解密权限。不完整或未知模块明确提示，不能把部分恢复称全部完成。本地密码与账户加密分离，改密码不导致所有模块重传；敏感值不写日志。

## 已批准 key/value

所有路径为模板。DKVSRecord 原外壳 Version/Key/Value/PubKey/Signature/Seq/IssueHeight/TTL/FeeProof/Flags 保持底层职责。

| 对象 | key | value |
| --- | --- | --- |
| wrapper | `/personal/<AccountID>/account/root-key-wrapper/current` | 账户根保护的格式/Network/AccountID/Nonce/Ciphertext；解密为 Secret、PackageID、StorageMode、RecordTTL、AutopayContract。删重复 RecoveryMode/PublicLocator/RecoveryConfigured，不加 IndexKey。 |
| index | `/personal/<AccountID>/account/index` | 账户密钥加密；Version、Entries，每项 Provider/Scope/Key/Codec/Deleted。不放内容 hash、ACK、dirty、时间和进度。只接受当前账户受管 key。 |
| wallets/global | `/personal/<AccountID>/account/state` | 加密的 Version、RootFingerprint、Wallets；每钱包 Fingerprint/Deleted/Name/Type/Secret/AccountCount/SubAccounts，子账户 Index/Name/DID。删除项不带多余秘密。不含任何 Revision、DataRevision、DataHash。 |
| 模块 | `/blob/<AccountID>/<EntryID>` | EntryID 为 Provider 和规范 Scope 无歧义编码的完整 SHA-256 hex，不截断、不按内容生成；64 字节段满足 DKVS 现有限制。DKB1 Metadata 为空，Data 为账户加密的格式/nonce/ciphertext；解密 Provider/Scope/Codec/Payload。认证绑定账户、网络、key。 |
| 恢复包 | `/personal/<AccountID>/account/recovery/<PackageID>` | 沿用主要紧凑编码，增加可选 GuardianCapsule 密文与引用。UserShare 不明文上传。TTL 重传同 PackageID、原保护内容。 |

RGB payload 保留 RecoveryPackage 的业务 Version/WalletID/AccountIndex/EngineBuildID/ProjectionRecords/EngineRecords，SnapshotRecord 为 Key/Value，完整证明、pending/transfer、预约、consignment 由 RGB 校验。SilentPay 先复用已启用 scope 的字节 1；启用登记，删除必须明确意图，不按一次 Export 缺项推断。通道、文档等 payload 待各自明确，不编造字段。

Core binding `/account/<network>/<rootAddress>`、guardian share `/mail/<mailbox>/share/<package>/<share>`、公开 RGB `/contract/rgb11/<contractID>`、Direct mailbox `/mail/<recipient>/msg/<sender>/<messageID>` 保持原职责，不纳入个人备份 ACK。binding 内容相同不重写。RGB11_ACK 业务消息不是 DKVS 写 ACK。不新增/依赖 ACCOUNT_MANAGED_ACTIVE 通道。新协议退出旧整体 `account-managed-data` blob，不双写。

## 实施与验收

重点拆 account_managed_data/state/ack/write_ready/repository，复用 provider、调度、业务提交点。RGB 接口及监控理顺循环依赖；通道在原保存点接入，但不假称当前已完整纳管。不改变业务证明、签名、余额、占用和交易规则，不删现有工作数据。

验收围绕正常 PWA：未激活/激活/首备、重开/新设备、网络重试、多模块、N 上传中产生 N+1、ACK 清理失败、简单冲突、取消、完整 TTL 重传。已有用例不能以假 ready、清缓存或额外业务步骤掩盖失败。实现者只编译/准备；Luna 逐 case 后相关组复测。最终固定候选做变更仓库及上层依赖全仓，全通过后冻结，再真实 regtest UI-only、聪网测试网验收。主网升级和发布另行授权。
