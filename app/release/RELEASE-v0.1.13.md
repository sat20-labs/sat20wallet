# 📦 SAT20 Wallet v0.1.13 Release

## 🎉 发布信息

- **版本号**: v0.1.13
- **构建日期**: 2026-03-06
- **应用名称**: SAT20Wallet
- **包名**: com.sat20.wallet
- **目标平台**: Android

---

## 📋 变更内容

### 主要更新
- ✅ 移除了转账地址的 Taproot 地址验证限制
  - 现在支持向所有有效的比特币地址转账（包括 Taproot 和非 Taproot 地址）
  - 删除了 `isNonTaprootAddress()` 和 `isNonTaprootAddressAuto()` 函数
  - 移除了相关 UI 错误提示和验证逻辑

### 技术改进
- 🧹 清理了 `AssetOperationDialog.vue` 中的 Taproot 检测代码
- 🧹 删除了 `utils/index.ts` 中的 Taproot 验证函数
- 🧹 移除了多语言文件中的相关翻译文本（`taprootError`, `taprootAddress`）

---

## 🔐 签名信息

### 证书详情
- **别名**: sat20wallet
- **持有者**: CN=SAT20 Wallet, OU=SAT20 Labs, O=SAT20, L=Unknown, ST=Unknown, C=US
- **算法**: SHA384withRSA
- **密钥长度**: 2048-bit RSA
- **有效期**: 2026-03-06 至 2053-07-22（约 27 年）

### 证书指纹
- **SHA256**: `5F:8B:92:27:0F:85:14:C4:94:9B:17:88:91:54:D4:F4:ED:C4:C1:01:F5:E7:62:7D:4C:AA:1B:D0:72:2C:C2:03`
- **SHA1**: `3C:E7:D2:76:9E:C9:DF:FF:4F:C3:2E:7A:EA:21:EE:A3:16:27:70:C3`

---

## 📦 构建产物

### APK 文件
| 文件名 | 大小 | 类型 | 状态 |
|--------|------|------|------|
| `SAT20-Wallet-v0.1.13-release-signed.apk` | 16MB | Release | ✅ 已签名 |

### 文件位置
```
/Users/icehugh/workspace/jieziyuan/client/sat20wallet/app/release/SAT20-Wallet-v0.1.13-release-signed.apk
```

---

## 🔍 验证方法

### 1. 验证 APK 签名
```bash
cd /Users/icehugh/workspace/jieziyuan/client/sat20wallet/app
keytool -printcert -jarfile release/SAT20-Wallet-v0.1.13-release-signed.apk
```

### 2. 验证 APK 完整性
```bash
cd /Users/icehugh/workspace/jieziyuan/client/sat20wallet/app
jarsigner -verify release/SAT20-Wallet-v0.1.13-release-signed.apk
```

### 3. 查看 APK 信息
```bash
aapt dump badging release/SAT20-Wallet-v0.1.13-release-signed.apk
```

---

## 📱 安装方法

### 方法 A: 直接安装（ADB）
```bash
# 连接设备后执行
adb install release/SAT20-Wallet-v0.1.13-release-signed.apk
```

### 方法 B: 传输到设备
1. 将 APK 文件传输到 Android 设备
2. 在设备上打开文件管理器
3. 点击 APK 文件进行安装
4. 允许"未知来源"安装权限（如果需要）

### 方法 C: 通过 Android Studio
1. 在 Android Studio 中打开 `android/` 目录
2. 点击 `Build` > `Build Bundle(s) / APK(s)` > `Build APK(s)`
3. 安装已构建的 APK

---

## ⚠️ 注意事项

### 签名一致性
- ✅ 此版本使用与 v0.1.12 相同的密钥签名
- ✅ 可以无缝升级已安装的旧版本
- ✅ 不会丢失用户数据

### 兼容性
- **最低 Android 版本**: Android 5.0 (API 21)
- **目标 Android 版本**: Android 14 (API 34)
- **架构支持**: arm64-v8a, armeabi-v7a, x86, x86_64

### 数据迁移
- 从旧版本升级：自动保留所有数据
- 全新安装：需要导入或创建新钱包

---

## 🧪 测试建议

### 核心功能测试
1. ✅ 创建新钱包
2. ✅ 导入现有钱包
3. ✅ 接收资产
4. ✅ 发送资产到 Taproot 地址（bc1p 开头）
5. ✅ 发送资产到非 Taproot 地址（1, 3, bc1q 开头）
6. ✅ 域名解析功能
7. ✅ 生物识别解锁
8. ✅ 设置和安全功能

### 兼容性测试
- [ ] Android 5.0 (API 21)
- [ ] Android 6.0 (API 23)
- [ ] Android 7.0 (API 24)
- [ ] Android 8.0 (API 26)
- [ ] Android 9.0 (API 28)
- [ ] Android 10 (API 29)
- [ ] Android 11 (API 30)
- [ ] Android 12 (API 31)
- [ ] Android 13 (API 33)
- [ ] Android 14 (API 34)

---

## 📝 已知问题

暂无

---

## 🔗 相关链接

- [项目仓库](https://github.com/sat20/sat20wallet)
- [问题反馈](https://github.com/sat20/sat20wallet/issues)
- [文档](https://github.com/sat20/sat20wallet/wiki)

---

## 📞 支持

如有问题，请通过以下方式联系：
- GitHub Issues: https://github.com/sat20/sat20wallet/issues
- Email: support@sat20.org

---

**构建者**: SAT20 Team  
**发布日期**: 2026-03-06  
**版本**: v0.1.13  
**状态**: ✅ 已发布
