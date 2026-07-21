# 账户安全设备存储设计

版本：v0.1  
适用项目：sat20wallet PWA / Capacitor App  
上层依赖：wallet-sdk `AccountManager.restoreOnNewDevice(..., persist)`

## 1. 目标

账户管理模块在 Wallet SDK 中完成账户加密、Shamir 恢复和 DKVS 数据读取；恢复后的账户如何保存在当前设备，由 sat20wallet 的 PWA / 原生应用层负责。

本设计需要同时覆盖：

- Android 原生应用；
- iOS 原生应用；
- macOS 上安装或使用的 PWA；
- Windows 上安装或使用的 PWA；
- 不支持高级 WebAuthn 能力的浏览器 fallback。

核心要求：

```text
助记词不明文持久化
AccountSecret 不明文持久化
生物识别只作为系统密钥访问控制
IndexedDB 只保存密文和 key-slot metadata
清除浏览器数据后仍可通过 DKVS 账户恢复重新添加设备
```

## 2. 当前实现问题

### 2.1 PWA Storage Adapter

当前 `pwa/lib/storage-adapter.ts` 把 `local:wallet_*`、`session:wallet_*` 等值直接作为字符串保存到 IndexedDB。IndexedDB 适合保存密文和缓存，但不能直接作为账户秘密的安全边界。

后续应把存储拆成：

```text
普通 UI / 缓存状态 -> 现有 Storage adapter
账户密文 -> AccountCiphertextStore (IndexedDB)
设备解锁密钥 -> SecureAccountStorage
```

### 2.2 现有生物识别辅助代码

现有代码存在以下不适用于账户秘密的模式：

```text
Math.random 生成 challenge
base64 保存密码 hash
密码相关 metadata 保存到 localStorage
生物识别成功后直接信任 localStorage 中的结果
```

生物识别不是加密密钥。正确模型是：

```text
系统生物识别 / device credential
        ↓
允许操作系统安全模块使用或释放设备包装密钥
        ↓
设备包装密钥解密 AccountSecret
```

## 3. 本地密钥层次

### 3.1 AccountSecret

`AccountSecret` 由 Wallet SDK 恢复，用于解密 DKVS 中的账户密文。

它只在以下时刻短暂进入内存：

1. 创建账户；
2. 当前设备解锁；
3. 添加新设备；
4. 恢复账户。

### 3.2 DeviceWrappingKey

每台设备生成独立随机密钥：

```text
DeviceWrappingKey = random(32 bytes)
```

本地保存：

```text
WrappedAccountSecret = AEAD_Encrypt(
  DeviceWrappingKey,
  AccountSecret,
  AAD = account_id || device_id || version
)
```

不同设备有不同的 `DeviceWrappingKey`。设备密钥泄露不改变 Shamir recovery package，但应撤销该设备并重新评估账户风险。

### 3.3 IndexedDB 中允许保存的数据

```text
account_id
package_id
device_id
encrypted account envelope cache
WrappedAccountSecret
key slot type
credential id / salt / KDF params
created_at
last_unlocked_at
```

IndexedDB 中不保存：

```text
明文 AccountSecret
明文助记词
明文 DeviceWrappingKey
可直接作为解密密钥使用的密码 hash
生物识别结果
```

## 4. 统一接口

```ts
export type SecureStorageMode =
  | 'native-keystore'
  | 'webauthn-prf'
  | 'pin-wrapped';

export interface SecureStorageCapabilities {
  mode: SecureStorageMode;
  userPresence: boolean;
  hardwareBacked: boolean;
  biometricAvailable: boolean;
  recoverableByAccountRecovery: true;
}

export interface DeviceKeySlot {
  version: 1;
  accountId: string;
  deviceId: string;
  mode: SecureStorageMode;
  wrappedAccountSecret: string;
  metadata: Record<string, string | number | boolean>;
}

export interface SecureAccountStorage {
  capabilities(): Promise<SecureStorageCapabilities>;

  seal(
    accountId: string,
    accountSecret: Uint8Array,
    options?: { requireUserPresence?: boolean }
  ): Promise<DeviceKeySlot>;

  unseal(
    accountId: string,
    options?: { requireUserPresence?: boolean; reason?: string }
  ): Promise<Uint8Array>;

  remove(accountId: string): Promise<void>;
}
```

上层 `AccountManager.restoreOnNewDevice` 的 `persist` 回调调用 `seal`。Wallet SDK 不直接依赖任何平台插件。

## 5. Android 实现

Android 原生 App 使用 Capacitor 自定义插件：

```text
CapacitorNativeSecureAccountStorage
```

原生实现：

1. 使用 Android Keystore 创建每账户或每设备 AES-256-GCM key；
2. 优先要求 hardware-backed / StrongBox（可用时）；
3. 设置 `setUserAuthenticationRequired(true)`；
4. 使用 `BiometricPrompt`，允许设备 PIN / pattern / password fallback；
5. 可选设置 biometric enrollment change 后 key invalidation；
6. 密钥只存在于 Keystore；
7. JavaScript 只得到 `WrappedAccountSecret`，不得到 Keystore key；
8. App 后台或超时后清理 JS/WASM 内存中的账户秘密。

建议 alias：

```text
sat20.account.<account_id>.<device_id>
```

## 6. iOS 实现

iOS 原生 App 使用同一个 Capacitor 插件接口。

原生实现：

1. 使用 Keychain 保存设备包装材料；
2. 使用 `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`；
3. 使用 `SecAccessControl` 的 `userPresence`；
4. 支持 `biometryCurrentSet`，用户重新录入 Face ID / Touch ID 后使旧 slot 失效；
5. 如使用 Secure Enclave EC key，则通过 ECDH 派生/包装对称密钥；
6. JavaScript 只请求 `seal` / `unseal`，不读取原生私钥或对称 key；
7. iCloud Keychain 不作为唯一恢复机制，账户恢复仍依赖 DKVS + Shamir。

## 7. macOS / Windows PWA 实现

纯浏览器 PWA 不能统一直接调用 macOS Keychain 或 Windows DPAPI，因此采用两级方案。

### 7.1 首选：WebAuthn PRF

如果浏览器与 authenticator 支持 WebAuthn PRF extension：

1. 为当前账户创建 passkey / WebAuthn credential；
2. 为当前 `account_id` 使用随机 PRF salt；
3. 调用 PRF 得到本地 key material；
4. 通过 HKDF 派生 `DeviceWrappingKey`；
5. 用该 key 包装 `AccountSecret`；
6. 每次解锁要求 WebAuthn user verification；
7. IndexedDB 保存 credential ID、PRF salt 和密文。

运行时必须 feature-detect，不能假设所有 Safari、Chrome、Edge、Android WebView 均支持 PRF。

### 7.2 Fallback：设备 PIN + Argon2id

PRF 不可用时：

1. 用户设置当前设备专用 PIN 或长密码；
2. 生成独立随机 salt；
3. 使用 Argon2id 派生 KEK；
4. KEK 只用于解密随机 `DeviceWrappingKey`；
5. `DeviceWrappingKey` 再解密 `AccountSecret`；
6. 忘记设备 PIN 时，不做服务器重置，直接走完整账户恢复；
7. KDF 参数保存在 key slot metadata；
8. 登录失败做本地指数退避，但不能把它当作抵御离线破解的主要边界。

设备 PIN 是本地便利解锁，不是账户恢复秘密。

## 8. 运行时内存与会话

解锁后：

1. `AccountSecret` 和助记词只保留在需要它们的最短时间；
2. 页面隐藏、App 进入后台、用户主动锁定或超时后清理内存；
3. 默认解锁会话建议 5–15 分钟，由产品层配置；
4. 交易签名可要求再次 user presence；
5. 不把助记词放入 Pinia 持久化、Vue devtools、console 或错误上报；
6. crash report 和 analytics 过滤账户字段。

JavaScript 无法保证所有 Buffer 被物理覆盖，但仍应使用 `fill(0)`、缩短生命周期、避免复制，并把长期密钥操作尽量放到原生安全模块或 WebCrypto non-extractable key 中。

## 9. 新设备流程

添加新设备就是一次恢复演练：

```text
1. 定位 DKVS recovery package
2. 选择知识问题、Guardian 或用户分片
3. 恢复满足阈值的两片
4. 本地恢复 AccountSecret
5. 解密并确认钱包名、子账户数和 DID 名称
6. 创建该设备的 DeviceWrappingKey
7. 调用 SecureAccountStorage.seal
8. 清理恢复过程中的临时秘密
9. 标记恢复演练完成
```

不直接把旧设备的明文助记词复制到新设备。

## 10. 账户恢复模式与本地存储

### 10.1 普通用户

默认使用 Shamir 2/3：

```text
S_user
S_dkvs
S_guardian
任意两片恢复
```

用户不必依赖长期保存自己的分片；可以通过 DKVS 知识恢复 + Guardian 添加新设备。

### 10.2 增强安全

使用 Shamir 2/2：

```text
S_user + S_dkvs
```

用户自己的分片是必需的。适合愿意承担更多保管责任的用户。

本地设备存储不改变 recovery policy，只减少日常使用时重复恢复的成本。

## 11. 现有数据迁移

迁移步骤：

1. 用户使用现有密码解锁旧钱包；
2. 在内存中读取现有钱包助记词；
3. 创建 AccountBackup 和 DKVS recovery package；
4. 完成一次新恢复演练；
5. 创建新 `DeviceKeySlot`；
6. 验证新 secure storage 可以 unseal；
7. 删除旧 IndexedDB / localStorage 中的敏感值；
8. 保留非敏感 UI 设置；
9. 重新扫描 storage，确保不存在 mnemonic / xpriv / password hash 明文。

迁移失败时保留旧数据并回滚，不得先删除后验证。

## 12. 测试矩阵

### 12.1 通用

1. seal / unseal round-trip；
2. AAD account ID 被修改时解密失败；
3. key slot 被篡改时失败；
4. 用户取消认证时失败；
5. 删除 key slot 后失败；
6. 会话锁定后清理内存；
7. IndexedDB 中无明文助记词与 AccountSecret。

### 12.2 Android

1. 指纹；
2. 面容（设备支持时）；
3. device PIN fallback；
4. biometric enrollment 变化；
5. App 卸载/重装；
6. Keystore invalidation。

### 12.3 iOS

1. Face ID；
2. Touch ID；
3. passcode fallback；
4. biometryCurrentSet 变化；
5. Keychain item accessibility；
6. App 重装后的恢复路径。

### 12.4 Browser/PWA

1. Chrome / Edge WebAuthn PRF；
2. Safari / Firefox feature detection；
3. PIN + Argon2id fallback；
4. PWA standalone restart；
5. 浏览器清除数据；
6. macOS / Windows 锁屏后重新认证。

## 13. 开发顺序

1. 定义 `SecureAccountStorage` 接口和 IndexedDB ciphertext schema；
2. 实现 PIN + Argon2id browser fallback；
3. 实现 WebAuthn PRF adapter；
4. 实现 Capacitor Android Keystore adapter；
5. 实现 Capacitor iOS Keychain adapter；
6. 把 `restoreOnNewDevice.persist` 接入 secure storage；
7. 编写迁移工具；
8. 完成跨平台测试矩阵。
