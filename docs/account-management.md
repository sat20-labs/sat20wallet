# Wallet SDK 自托管账户管理系统

版本：v1 开发设计  
实现位置：`sat20wallet/sdk/account`、`sat20wallet/sdk/wallet`

## 1. 目标

账户管理系统以 DKVS 为公开密文存储层，在 Go Wallet SDK 中完成账户备份、加密、恢复和新设备恢复演练。

账户备份只包含：

```text
多个钱包助记词
钱包名称
每个钱包的子账户数量
子账户派生 index
子账户 Ordinals DID 名称
```

链上合约、余额、资产、交易历史从 indexer 重新获取；应用设置由应用管理。

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

创建恢复包时生成随机 32-byte `AccountSecret`。账户备份使用 AES-256-GCM 加密；密钥通过 HKDF-SHA256 从 `AccountSecret`、account ID 和 recovery package ID 派生。AAD 绑定：

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
/personal/<account_id>/account/recovery/<package_id>/envelope
/personal/<account_id>/account/recovery/<package_id>/share/dkvs
/personal/<account_id>/account/recovery/<package_id>/questions
/personal/<account_id>/account/recovery/<package_id>/manifest
```

写入顺序固定为：

```text
envelope
share/dkvs
questions
manifest
```

Manifest 最后写入，作为应用层 commit marker。账户 record 使用 account owner 签名并由同一 wallet 的 AUTOPAY 支付。

## 8. 新设备

添加新设备必须完成一次真实恢复演练：

```text
1. 从 DKVS 获取恢复包
2. 获得满足阈值的分片
3. Go SDK 恢复 AccountSecret
4. 解密账户备份
5. 只展示钱包名、子账户数量和 DID 名称
6. 用户确认
7. PWA/native SecureAccountStorage.seal
8. 清除临时秘密
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
manifest-last 写入
prefix list 与 usage
Guardian mailbox share
从 core 节点读取后完整恢复账户
```
