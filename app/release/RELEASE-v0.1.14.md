# SAT20 Wallet v0.1.14 发布说明

## 📦 版本信息

- **版本号**: v0.1.14
- **发布日期**: 2026-03-06
- **APK 文件**: SAT20-Wallet-v0.1.14-release-signed.apk
- **文件大小**: 16 MB
- **最低兼容版本**: v0.1.0

## ✨ 更新内容

### 功能优化

1. **版本检查逻辑优化**
   - 启动应用时静默检查更新，不打扰用户
   - 版本一致时不再显示"已是最新版本"提示
   - 有更新时仍会及时提醒用户
   - 用户手动检查时显示当前版本状态

### 用户体验改进

- ✅ 减少不必要的弹窗打扰
- ✅ 保持重要更新的及时通知
- ✅ 更智能的版本更新策略

## 🔧 技术变更

### 修改文件

1. `composables/useAppVersion.ts`
   - 优化 `checkForUpdates` 方法的提示逻辑
   - 有更新时始终显示提示（无论静默模式）
   - 版本一致时仅在非静默模式下提示

2. `entrypoints/popup/App.vue`
   - 启动检查改为静默模式：`checkForUpdates(true)`

3. `public/version.json`
   - 更新版本号至 0.1.14
   - 更新发布说明

## 📲 安装说明

### Android 安装步骤

1. 下载 `SAT20-Wallet-v0.1.14-release-signed.apk`
2. 在 Android 设备上启用"未知来源"安装权限
3. 打开 APK 文件进行安装
4. 启动应用，享受新功能

### 升级说明

- **从旧版本升级**: 直接安装新版本 APK，数据会自动保留
- **全新安装**: 安装后需要创建或导入钱包

## 🔐 签名验证

### 证书信息

```
SHA256: 5F:8B:92:27:0F:85:14:C4:94:9B:17:88:91:54:D4:F4:ED:C4:C1:01:F5:E7:62:7D:4C:AA:1B:D0:72:2C:C2:03
SHA1: 3C:E7:D2:76:9E:C9:DF:FF:4F:C3:2E:7A:EA:21:EE:A3:16:27:70:C3
```

### 验证方法

```bash
# 验证 APK 签名
jarsigner -verify SAT20-Wallet-v0.1.14-release-signed.apk

# 查看证书信息
keytool -printcert -jarfile SAT20-Wallet-v0.1.14-release-signed.apk
```

## 📝 测试建议

### 功能测试

- [ ] 启动应用，检查是否安静启动（无更新提示）
- [ ] 前往设置页面，手动检查更新
- [ ] 验证版本信息显示正确
- [ ] 测试钱包基本功能（余额查询、转账等）
- [ ] 检查已有钱包数据完整性

### 兼容性测试

- [ ] Android 10+ 安装测试
- [ ] 从 v0.1.13 升级测试
- [ ] 从更早版本升级测试

## 🐛 已知问题

暂无

## 📞 反馈与支持

如遇到任何问题，请通过以下方式反馈：

- GitHub Issues: https://github.com/sat20-labs/sat20wallet/issues
- Twitter: @sat20labs

---

**发布者**: SAT20 Wallet Team
**发布日期**: 2026-03-06
