# 📦 SAT20 Wallet v0.1.14

## 🎉 发布亮点

本次更新优化了版本检查体验，让应用启动更加安静，不再打扰用户！

---

## ✨ 功能优化

### 📱 版本检查体验改进

**优化前的问题**:
- 每次打开应用都会显示"已是最新版本"提示
- 用户体验被打断，感到困扰

**优化后的体验**:
- ✅ 启动应用时静默检查更新，不显示任何提示
- ✅ 有更新时及时提醒，不错过重要版本
- ✅ 用户手动检查时才显示当前版本状态

**技术实现**:
```typescript
// App.vue - 启动时静默检查
checkForUpdates(true)  // true = 静默模式

// useAppVersion.ts - 智能提示逻辑
if (hasUpdate.value) {
  showUpdateNotification(info)  // 始终显示
} else {
  if (!silent) {
    toast({ title: '已是最新版本' })  // 仅手动检查时显示
  }
}
```

---

## 📋 变更清单

### 修改文件

- `composables/useAppVersion.ts` - 优化版本检查提示逻辑
- `entrypoints/popup/App.vue` - 启动检查改为静默模式
- `public/version.json` - 更新版本信息和发布说明

### 技术细节

**Composable 优化**:
- 有更新时：无论静默/非静默模式，都显示更新提示
- 版本一致时：仅在非静默模式（用户手动检查）下显示反馈

**启动流程**:
- 延迟 2 秒后静默检查更新
- 不干扰用户正常使用流程

---

## 📲 安装说明

### Android 用户

1. **下载安装**:
   - 下载 [SAT20-Wallet-v0.1.14-release-signed.apk](https://github.com/sat20-labs/sat20wallet/releases/download/v0.1.14/SAT20-Wallet-v0.1.14-release-signed.apk)
   - 安装到 Android 设备

2. **升级用户**:
   - 直接覆盖安装，数据会自动保留
   - 支持从任意旧版本升级

3. **验证签名**:
   ```bash
   jarsigner -verify SAT20-Wallet-v0.1.14-release-signed.apk
   ```

### iOS 用户

> ⚠️ iOS 版本暂不可用，仅支持 Android

---

## 🔐 安全信息

### APK 签名

- **证书持有者**: SAT20 Wallet, SAT20 Labs
- **有效期**: 到 2053-07-22
- **签名算法**: SHA384withRSA (2048-bit)

### 证书指纹

```
SHA256: 5F:8B:92:27:0F:85:14:C4:94:9B:17:88:91:54:D4:F4:ED:C4:C1:01:F5:E7:62:7D:4C:AA:1B:D0:72:2C:C2:03
SHA1: 3C:E7:D2:76:9E:C9:DF:FF:4F:C3:2E:7A:EA:21:EE:A3:16:27:70:C3
```

---

## 📊 构建信息

- **版本号**: v0.1.14
- **构建时间**: 2026-03-06 23:26
- **APK 大小**: 16 MB
- **最低兼容版本**: v0.1.0
- **Android 版本**: 构建于 Gradle 8.13
- **Capacitor**: 7.4.3

---

## 🧪 测试建议

### 必测项目

- [ ] **启动测试**: 打开应用，检查是否安静启动（无提示）
- [ ] **手动检查**: 设置 → 检查更新，显示"已是最新版本"
- [ ] **核心功能**: 钱包解锁、余额查询、转账
- [ ] **升级测试**: 从旧版本升级，数据保留完整

---

## 📞 反馈与支持

遇到问题？请通过以下方式反馈：

- 📝 [GitHub Issues](https://github.com/sat20-labs/sat20wallet/issues)
- 🐦 [Twitter @sat20labs](https://twitter.com/sat20labs)
- 📧 Email: support@sat20labs.com

---

## 🔗 相关链接

- 📱 [上一版本 v0.1.13](https://github.com/sat20-labs/sat20wallet/releases/tag/v0.1.13)
- 📋 [完整变更日志](https://github.com/sat20-labs/sat20wallet/blob/main/release/RELEASE-v0.1.14.md)
- 📖 [项目文档](https://github.com/sat20-labs/sat20wallet)

---

**发布者**: SAT20 Wallet Team  
**发布日期**: 2026-03-06  
**下一条**: v0.1.15 (coming soon) 🚀
