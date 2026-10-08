# PWA 自托管账户管理

版本：v1

## 目标

PWA 提供自托管账户激活和恢复界面。Go SDK 负责账户加密、Shamir、私人知识恢复、Guardian、DKVS record 和支付逻辑。

账户目录备份包含：

```text
多个钱包助记词
钱包名称
子账户数量和派生 index
子账户 Ordinals DID
```

RGB11 的可恢复资产、分配证明和引擎状态由 SDK 的 `rgb11` account-managed provider
写入加密数据包，并与账户目录的当前版本绑定；新设备同时恢复各钱包、子账户的 RGB11 数据。
客户端验证资产的证明不能仅靠 indexer 重建。进行中的交易沿用最后稳定所有权备份，恢复不得意外广播。
普通 BTC/L2 链上余额、交易和 contracts 从 indexer 查询，应用设置由应用管理。

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

2/2 使用知识恢复的分片与用户分片。2/3 支持知识+用户、知识+Guardian、用户+Guardian 三种组合；
没有知识答案时，可在知识步骤选择“使用用户分片和 Guardian”，必须提供另外两份材料。

取消或离开恢复页面会终止该 WASM 恢复会话；离开设置页面会清理未完成的设置会话。
Manager release 清理全部账户会话。锁屏后钱包所属的账户写入和付费操作不可派发，
新设备通过恢复材料恢复账户的入口仍可使用。
最终确认后的恢复写入沿用 SDK 的原子提交流程，该阶段禁用取消；提交持有独立材料快照，
避免页面退出或会话过期清理破坏已确认写入的账户与 RGB11 数据。

恢复过程不向页面展示助记词。

## 敏感数据边界

PWA UI 数据库与 Pinia store 不保存下列账户恢复材料；SDK IndexedDB 另行保存钱包记录，其中敏感材料沿用 SDK 既有加密和封装方式：

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

Guardian recovery key 由 Go SDK 使用钱包密码加密保存在钱包数据库中。
账户管理使用独立 WASM 接口；操作日志去除密码、助记词、知识答案和秘密分片等敏感材料。

Root wrapper 的持久化格式完全由 Go SDK 管理，PWA 不解析其 envelope 或 payload。SDK 直接使用 deterministic compact binary，不保留旧 JSON 格式的兼容分支。

## DKVS 数据

```text
/personal/<account_id>/account/recovery/<package_id>
/personal/<account_id>/account/state
/personal/<account_id>/account/root-key-wrapper/current
```

Guardian 分片：

```text
/mail/<guardian_mailbox_id>/share/<package_id>/<share_id>
```

恢复包的 envelope、知识恢复材料和 manifest 编为一个不可变 compact record，整体签名并写入。
公开恢复包只提供根钱包 bootstrap 材料；恢复预览再读取最新账户目录及其关联的 RGB11 密文数据包。

RGB11 导入失败会保留 SDK 的持久导入标记并阻止上传部分数据。同一恢复预览可以使用相同本地密码
在原数据库重试；SDK 只允许匹配原认证快照的重试，完成 provider 导入和锁重建后才解除保护。
目录变更及待同步队列由 SDK 原子提交，新增、导入和删除同时提交选择状态；持久化失败会返回错误，
并保留原本的目录和选择。正常同步的导入标记与账户状态同批写入，事务失败后可以直接重试同步。
普通同步在 provider 导入失败后，下次同步会先校验原认证目标并续跑导入，完成前不会上传部分数据。
三方合并的加密目标暂存于现有导入标记，并绑定 profile 的远端基线；未知或损坏标记仍拒绝。
恢复或导入未完成时，目录变更会返回错误，同一恢复预览仍可使用原密码重试。
切换钱包和子账户的选择状态写入失败会返回错误，SDK 和页面保留原选择；读取账户 profile 的存储错误也会阻止初始化。
解锁时 PWA 重新读取本机 SDK 目录，并以认证返回的当前钱包 ID 核对选择、地址与公钥，
包括运行中锁屏、远端删除和节点 API 不可达的场景。

### 2026-10-06 IndexedDB 存储验收

SDK WASM 在 PWA 和扩展自身环境中统一使用 `sat20-wallet-sdk` IndexedDB，UI 使用独立的 `sat20-wallet-pwa` 数据库。
按用户确认的未发布功能原则，不增加旧 localStorage/chrome.storage.local 数据迁移、兼容或回退，也不自动清理旧存储。
SDK 批量保存钱包目录、profile、待同步变更和必要选择状态时使用同一个事务；全部提交成功后才返回成功，
异常、请求错误或事务中止正常返回，整批失败不会留下部分修改。UI 单条写入也等待事务完成。

浏览器门禁先运行 Go WASM 的真实 IndexedDB 故障回归，再验证账户创建、导入、改名及子账户操作的 profile 写入失败，
检查内存目录与持久记录不变，并在页面重启后复核。旧后端的 5 项失败证据仍保留，不能用原有功能通过记录替代新存储验收。
账户事务后的 RGB11 模块导入仍可能失败，继续使用既有保护标记和认证目标续跑。
