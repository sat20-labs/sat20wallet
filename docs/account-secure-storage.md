# 账户安全设备存储设计

版本：v0.2  
适用项目：sat20wallet PWA / Capacitor App  
上层依赖：Go `sdk/account` 通过 WASM 或原生绑定完成恢复

## 1. 边界

Go SDK 完成账户加密、Shamir 恢复、Fuzzy Recovery 和 DKVS 读取。恢复后的 `AccountSecret` 如何保存在当前设备，由 PWA / 原生应用层负责。

```text
助记词不明文持久化
AccountSecret 不明文持久化
生物识别只控制系统密钥的使用
IndexedDB 只保存密文和 key-slot metadata
添加设备必须完成一次真实账户恢复演练
```

## 2. 密钥层次

每台设备生成独立随机 `DeviceWrappingKey`：

```text
WrappedAccountSecret = AEAD_Encrypt(
  DeviceWrappingKey,
  AccountSecret,
  AAD = account_id || device_id || version
)
```

IndexedDB 只保存 `WrappedAccountSecret`、credential ID、KDF 参数、设备 ID 和非秘密 metadata。

## 3. 平台实现

### Android

使用 Capacitor 原生插件、Android Keystore、AES-256-GCM、`BiometricPrompt` 和设备凭据。可用时优先 hardware-backed / StrongBox。JavaScript 只调用 `seal` / `unseal`，不读取 Keystore key。

### iOS

使用 Keychain、`kSecAttrAccessibleWhenUnlockedThisDeviceOnly` 和 `SecAccessControl` 的 `userPresence`；可选 `biometryCurrentSet` 和 Secure Enclave ECDH 包装。JavaScript 不读取原生密钥。

### macOS / Windows PWA

首选 WebAuthn PRF：PRF 输出经 HKDF 派生 `DeviceWrappingKey`。运行时必须 feature-detect。

PRF 不可用时，使用设备 PIN/长密码经 Argon2id 派生 KEK，KEK 只包装随机 `DeviceWrappingKey`。忘记 PIN 后执行完整账户恢复，不提供服务器重置。

## 4. 统一接口

见 `pwa/lib/secure-account-storage.ts`。Go/WASM 恢复成功、用户确认钱包名/子账户数/DID 后，PWA 调用 `seal`。

## 5. 新设备流程

```text
1. 定位 DKVS recovery package
2. 通过私人知识问题、Guardian 或用户分片得到满足阈值的分片
3. Go SDK 本地恢复 AccountSecret
4. 解密并展示钱包名、子账户数和 Ordinals DID
5. 用户确认
6. 创建当前设备 DeviceWrappingKey
7. SecureAccountStorage.seal
8. 清理恢复过程中的临时秘密
```

## 6. 迁移

旧钱包数据迁移必须先创建 DKVS recovery package、完成一次恢复演练、验证新 key slot 可 `unseal`，之后再删除旧 IndexedDB/localStorage 中的敏感值。失败时保留旧数据并回滚。
