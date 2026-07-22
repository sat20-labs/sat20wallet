# PWA 自托管账户管理

版本：v1

## 目标

PWA 提供自托管账户激活和恢复界面。Go SDK 负责账户加密、Shamir、私人知识恢复、Guardian、DKVS record 和支付逻辑。

账户备份只包含：

```text
多个钱包助记词
钱包名称
子账户数量和派生 index
子账户 Ordinals DID
```

余额、资产、交易、contracts 和应用设置由 indexer 或应用重新获取。

## 页面入口

已有钱包：

```text
设置 -> 自托管账户恢复
```

新设备：

```text
创建钱包
导入助记词
恢复自托管账户
```

## 激活流程

```text
账户预检查
-> 选择 2/3 或 2/2
-> 选择临时缓存或付费保存
-> 确认 SDK 返回的条件
-> 设置三个私人知识问题
-> 2/3 设置 Guardian
-> SDK 加密并发布到 DKVS
-> 保存公开恢复码和秘密用户分片
-> 完成恢复演练
```

临时缓存策略来自当前连接节点的：

```http
GET /v3/dkvs/config
```

PWA 不计算 TTL、费用或 AUTOPAY 参数，只展示 SDK 返回的结果。

## 恢复流程

```text
输入公开恢复码
-> SDK 从 DKVS 加载恢复包
-> 回答至少两个私人问题
-> 提供 Guardian 响应或用户分片
-> SDK 解密账户并返回非敏感摘要
-> 用户确认钱包名、子账户数和 DID
-> 设置新的本地钱包密码
-> SDK 一次性恢复全部钱包
```

恢复过程不向页面展示助记词。

## 敏感数据边界

PWA IndexedDB、localStorage、Pinia store 不保存：

```text
AccountSecret
WrappedAccountSecret
DeviceWrappingKey
助记词
Shamir 分片
私人问题答案
Guardian 私钥
```

PWA 只保存公开和非敏感状态：

```text
account_id
package_id
公开恢复码
激活状态
存储方式摘要
Guardian 状态
上次恢复演练时间
```

Guardian recovery key 由 Go SDK 使用钱包密码加密保存在钱包数据库中。账户管理 WASM 接口使用独立的无日志调用通道。

## DKVS 数据

```text
/personal/<account_id>/account/recovery/<package_id>/envelope
/personal/<account_id>/account/recovery/<package_id>/share/dkvs
/personal/<account_id>/account/recovery/<package_id>/questions
/personal/<account_id>/account/recovery/<package_id>/manifest
```

Guardian 分片：

```text
/mail/<guardian_mailbox_id>/share/<package_id>/<share_id>
```

Manifest 最后写入，作为恢复包完整提交标记。
