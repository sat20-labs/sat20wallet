# 账户安全设备存储设计

版本：v0.3  
适用项目：sat20wallet PWA / Capacitor App  
上层依赖：Go `sdk/account` 通过 WASM 或原生绑定完成恢复

## 1. 边界

Go SDK 完成账户加密、Shamir 恢复、Fuzzy Recovery、Guardian 和 DKVS 读取。

```text
助记词不明文持久化
AccountSecret 不返回给 PWA 页面
WrappedAccountSecret 不由 PWA IndexedDB/localStorage 保存
生物识别只控制系统密钥或 SDK vault 的使用
添加设备必须完成一次真实账户恢复
```

PWA 页面只持有公开 Locator、状态摘要和 opaque `DeviceAccountHandle`。

## 2. 设备密钥

设备保护由底层 SDK 或系统安全模块完成：

```text
Android Keystore / StrongBox
iOS Keychain / Secure Enclave
WebAuthn PRF
SDK 内部认证加密 vault
```

如果实现内部需要包装 `AccountSecret`，包装后的密文也只能由 SDK 或系统安全模块管理。PWA 业务组件、Pinia store、localStorage 和 PWA 自己管理的 IndexedDB 都不能读取或写入该密文。

## 3. 平台实现

### Android

使用 Capacitor 原生插件、Android Keystore、AES-256-GCM、`BiometricPrompt` 和设备凭据。可用时优先 hardware-backed / StrongBox。JavaScript 只获得 opaque handle。

### iOS

使用 Keychain、`kSecAttrAccessibleWhenUnlockedThisDeviceOnly` 和 `SecAccessControl` 的 `userPresence`；可选 `biometryCurrentSet` 和 Secure Enclave。JavaScript 不读取原生密钥。

### macOS / Windows PWA

优先使用 WebAuthn PRF 或平台 authenticator。浏览器无法提供合格设备保护时，账户密钥只保留在短生命周期 SDK session 中；设备重新安装后通过完整账户恢复重新建立本地状态。

## 4. 统一接口

见 `pwa/lib/secure-account-storage.ts`。

```text
PWA 传入短生命周期 session ID
SDK / TEE 提交设备存储
PWA 只收到 DeviceAccountHandle
```

不提供 `ExportAccountSecret`、`GetWrappedAccountSecret` 或 `GetDeviceWrappingKey`。

## 5. 新设备流程

```text
1. 定位 DKVS recovery package
2. 通过私人知识问题、Guardian 或用户分片满足阈值
3. Go SDK 恢复并解密账户
4. PWA 只展示钱包名、子账户数和 Ordinals DID
5. 用户确认并设置新的本地钱包密码
6. SDK 导入钱包数据库
7. SDK / TEE 建立设备安全状态
8. 清理恢复 session
```

## 6. PWA 存储范围

PWA 可以保存：

```text
account_id
package_id
公开恢复码
激活状态
存储方式摘要
Guardian 状态
上次恢复演练时间
DeviceAccountHandle
```

PWA 不保存：

```text
AccountSecret
WrappedAccountSecret
DeviceWrappingKey
助记词
Shamir 分片
私人问题答案
Guardian 私钥
```
