# Wallet SDK 自托管账户管理系统

版本：v1 开发设计  
实现位置：`sat20wallet/sdk/account`、`sat20wallet/sdk/wallet`

## 1. 目标

账户管理系统以 DKVS 为公开密文存储层，在 Go Wallet SDK 中完成账户备份、加密、恢复和新设备恢复演练。

账户目录备份包含：

```text
多个钱包助记词
钱包名称
每个钱包的子账户数量
子账户派生 index
子账户 Ordinals DID 名称
```

RGB11 客户端验证资产的证明不能仅从 indexer 重建。SDK 的 `rgb11` account-managed provider
将可恢复资产、分配证明和引擎状态写入加密数据包，与账户目录的当前版本绑定，并按钱包公开
fingerprint 和子账户 index 在新设备恢复。进行中的交易沿用最后稳定所有权备份，恢复不得意外广播。
普通 BTC/L2 链上合约、余额和交易历史从 indexer 查询；应用设置由应用管理。

钱包名称在规范化空白后保持唯一。钱包、子账户数量及元数据字符串长度使用恢复 codec 的限制，
管理状态同时限制压缩后 DKVS 对象大小和解压后明文大小；已解锁账户的候选目录在保存前校验，
容量准入包含实际的数据包引用、版本号及保留的删除记录，发布前由同一 codec 拒绝限制外的状态。
受管理账户只接受助记词钱包；私钥钱包导入会在写入前拒绝。新增钱包默认名称带公开 fingerprint 后缀，
避免独立设备生成同名钱包而阻塞同步，用户仍可修改名称。

钱包新增、导入、重命名和子账户元数据变更，将目录记录与待同步变更通过同一数据库事务提交；
涉及选中身份的新增、导入及删除也在该事务中保存选择状态。在支持原子批提交且正确返回存储错误的 KVDB 下，任一步写入或 Flush 失败，
不改变内存目录、选择状态或待同步队列，创建和导入接口会返回持久化错误。
正常同步应用远端状态时，导入标记与目录、profile、status 同批提交，事务失败不会单独留下标记。
普通远端应用的 provider 导入失败后，下一次同步先校验并续跑 profile 中的认证目标，完成前不导出本地数据。
三方合并时 profile 仍保留远端确认基线，因此只有这种情况会在现有标记中保存额外的加密目标快照及基线哈希；
续跑核对目标版本、哈希、数据引用、账户身份和本地目录，全部 provider 导入及 RGB11 scope/锁重建成功后才删除标记。
标记损坏、未知来源、不同目录或不同目标保持拒绝；不清空数据库，也不无条件删除保护标记。
导入未完成时，创建、导入、重命名、子账户元数据变更及删除均拒绝，避免破坏同快照恢复条件。
钱包和已有子账户的选择状态先持久化再发布，写入失败返回错误并保留原选择。
账户 profile 的非“键不存在”读取错误会阻止初始化；RGB11 脏标记保存错误向调用方返回，释放锁并保留脏状态。
重新配置恢复方式前，
本地目录必须与捕获的远端版本一致；落后时先同步，避免覆盖其他设备新增的钱包。

恢复先原子提交钱包目录和管理 profile，再导入 RGB11 等 provider。导入未完成时保留既有持久标记并阻止上传。
相同账户、恢复包和认证快照可在同一数据库中重试；重试校验本地密码、秘密及目录，保留钱包 ID，
完成全部 provider 导入和 scope/锁重建后才移除标记。不同快照或已修改的目录不能借此覆盖非空数据库。

### 2026-10-06 浏览器事务存储

经用户确认，SDK WASM 在 PWA 和扩展自身的页面/后台中统一使用 IndexedDB。
`wallet/lightnode/kvdb.go` 使用独立的 `sat20-wallet-sdk` 数据库与 `kv` object store，
PWA UI 状态仍在 `sat20-wallet-pwa` 数据库；两者的清理范围相互独立。
未发布功能不增加 localStorage/chrome.storage.local 迁移、兼容或回退路径，不自动清除旧存储。

一个 WriteBatch 的写入与删除全部在同一 readwrite 事务中排队，等待整个事务完成后返回成功。
同步 JavaScript 异常、异步请求失败及事务中止均返回错误；失败事务撤销全部修改。
缺失键返回 ErrKeyNotFound，读取或打开数据库失败不能冒充缺失键。
View 在同一 readonly 事务中收集快照，再执行 Go 回调，避免异步回调期间事务提前结束或读取混入其他版本。
PWA UI 的写入、删除和清空同样等待事务完成，不在单条请求成功时提前报告成功。

原后端的 5 项失败证据保留；回归改为在真实 Chromium IndexedDB 上验证相同存储契约，
并覆盖异步失败、请求成功后中止、关闭重建、快照读取和批处理顺序。
浏览器账户门禁先执行这些 Go WASM 回归，再执行真实节点 PWA 业务与存储故障场景。
事务提交后 RGB11 等模块导入失败的保护标记和同目标续跑仍然保留。

## 2. 自托管边界

```text
DKVS 只保存密文和公开恢复 helper data
账户 owner 签名并通过同一钱包 AUTOPAY
单个 Shamir 分片不能恢复账户
单个私人知识问题不能恢复 DKVS 分片
Guardian 单独不能恢复账户
恢复与解密全部在用户设备本地完成
```

## 3. AccountSecret 与账户密文

首次初始化账户时生成随机 32-byte `AccountSecret`；配置或重新配置恢复包复用该秘密。
恢复包内的根钱包 bootstrap 备份使用 AES-256-GCM 加密；密钥通过 HKDF-SHA256 从
`AccountSecret`、account ID 和 recovery package ID 派生。AAD 绑定：

```text
version
account_id
package_id
recovery_mode
```

## 4. Shamir 恢复模式

Go SDK 使用 GF(2^8) Shamir，系数来自 `crypto/rand`，公开 share index 固定为 1..N。分片外层包含 package ID、阈值、总数、角色和 checksum。

### 2/3 便利恢复

```text
S_user
S_dkvs
S_guardian
任意两片恢复 AccountSecret
```

普通用户可以通过私人知识恢复 `S_dkvs`，再由 Guardian 提供 `S_guardian`，无需依赖长期保存纸质分片。

### 2/2 增强安全

```text
S_user + S_dkvs
```

用户自己持有的分片是密码学上的必要条件。

## 5. 私人知识 Fuzzy Recovery

不使用旧版 `float64 + SimHash + chaff` PoC。实现参考 Decentralized Identity Foundation `fuzzy-encryption`，移植为纯 Go：

```text
有限素数域
多项式 secure sketch
Berlekamp–Welch 错误恢复
scrypt 集合校验
HMAC-SHA3-512 确定性密钥派生
CSPRNG
```

每个问题独立保护 `K_dkvs` 的一个 2/3 Shamir 分片：

```text
问题 1 -> QShare1
问题 2 -> QShare2
问题 3 -> QShare3
任意两个正确问题 -> K_dkvs -> 解密 S_dkvs
```

问题应当答案明确、长期可记忆、但外部难枚举。例如书籍问题必须指定书名、作者、语言、版本/ISBN、页码和取值规则。

答案本地执行 Unicode NFKC、空白规范化和问题级标点/大小写规则。中文与自然语言通过 rune unigram/bigram/trigram 提取固定数量的 package-bound feature ID。

## 6. Guardian

Guardian 使用独立 X25519 recovery key，不复用钱包资产私钥。`S_guardian` 通过 X25519 ECDH + AES-256-GCM 加密，保存在 Guardian mailbox share 路径：

```text
/mail/<guardian_mailbox_id>/share/<package_id>/<share_id>
```

Guardian 钱包读取、解密后只把分片重新加密给恢复设备，不显示明文分片。

## 7. DKVS 数据布局

```text
/personal/<account_id>/account/recovery/<package_id>
/personal/<account_id>/account/state
/personal/<account_id>/account/root-key-wrapper/current
```

恢复包的 envelope、知识恢复材料和 manifest 编为一个不可变 compact record，整体签名并写入。
公开恢复包只提供根钱包 bootstrap 材料；恢复预览读取最新账户目录及其关联的 RGB11 密文数据包。
账户 record 使用 account owner 签名，付费存储使用根钱包账户 0 的 AUTOPAY。

### 编码边界

账户恢复数据只允许通过 Account Management 的 repository 和 codec 读写。旧的通用 DKWA wallet recovery / guardian share（kind 1、kind 2）以及对应的 `PutWalletRecoveryBackup`、`PutGuardianShare` 等接口已删除，不能作为另一套恢复协议使用。Guardian mailbox 仍由 Account Management 的 guardian capsule 流程管理。

Root wrapper 的 envelope 和加密 payload 均使用 deterministic compact binary：固定 magic/codec version、规范字段顺序、字段长度上限和严格 EOF 校验。开发测试阶段不保留旧 JSON 兼容读取；测试网上已有的 JSON record 通过测试工具清理或重写，不把迁移分支带入生产代码。

## 8. 新设备

添加新设备必须完成一次真实恢复演练：

```text
1. 从 DKVS 获取恢复包
2. 获得满足阈值的分片
3. Go SDK 恢复 AccountSecret
4. 解密账户备份
5. 只展示钱包名、子账户数量和 DID 名称
6. 用户确认
7. SDK 使用新本地密码加密钱包和账户凭据，原子提交目录，并在持久导入标记保护下恢复 RGB11 数据
8. PWA 更新非敏感状态并重新载入，清除临时秘密
```

不通过旧设备直接展示或复制助记词。

## 9. Go 包结构

```text
sdk/account
  账户模型、AES-GCM、Shamir、Fuzzy Recovery、Guardian、恢复流程

sdk/account/fuzzy
  DIF fuzzy-encryption 的纯 Go 移植

sdk/wallet/account_repository.go
  DKVS `/personal` repository、owner 签名、AUTOPAY

sdk/wallet/account_guardian.go
  Guardian mailbox share AUTOPAY 写入
```

PWA 层只负责 UI、平台安全存储和 Go/WASM 调用。

## 10. E2E

真实三节点测试验证：

```text
AUTOPAY 部署和激活
账户 recovery package 创建
owner-signed /personal record
bootstrap/core/miner P2P 同步
单个 compact recovery record 写入
prefix list 与 usage
Guardian mailbox share
从 core 节点读取后完整恢复账户
```
