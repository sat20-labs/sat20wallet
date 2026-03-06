# SAT20 Wallet v0.1.14 签名摘要

## ✅ 构建成功

- **版本号**: v0.1.14
- **构建时间**: 2026-03-06 23:26
- **APK 文件名**: SAT20-Wallet-v0.1.14-release-signed.apk
- **文件大小**: 16 MB
- **APK 路径**: `release/SAT20-Wallet-v0.1.14-release-signed.apk`

## 🔐 签名验证

### 证书信息

**证书持有者**:
- CN=SAT20 Wallet
- OU=SAT20 Labs
- O=SAT20
- L=Unknown, ST=Unknown, C=US

**有效期**: 
- 从：2026-03-06 21:30:10 CST
- 到：2053-07-22 21:30:10 CST

**证书指纹**:
```
SHA256: 5F:8B:92:27:0F:85:14:C4:94:9B:17:88:91:54:D4:F4:ED:C4:C1:01:F5:E7:62:7D:4C:AA:1B:D0:72:2C:C2:03
SHA1: 3C:E7:D2:76:9E:C9:DF:FF:4F:C3:2E:7A:EA:21:EE:A3:16:27:70:C3
```

**签名算法**: SHA384withRSA
**密钥长度**: 2048-bit RSA

### 验证结果

✅ **APK 签名验证通过**: `jarsigner -verify` 成功
✅ **证书有效**: 与之前版本使用同一密钥签名
✅ **兼容性保证**: 用户可以从旧版本无缝升级

## 📦 版本更新说明

### 主要变更

**功能优化**:
1. 版本检查逻辑优化
   - 启动时静默检查更新（不显示提示）
   - 版本一致时不再打扰用户
   - 有更新时仍会及时提醒

**技术实现**:
- `useAppVersion.ts`: 优化提示逻辑
- `App.vue`: 启动检查改为静默模式
- `version.json`: 更新版本号和发布说明

### 修改文件清单

```
composables/useAppVersion.ts          - 版本检查逻辑优化
entrypoints/popup/App.vue             - 启动检查模式修改
public/version.json                   - 版本信息更新
package.json                          - 版本号升级 (0.1.13 → 0.1.14)
```

## 📲 测试建议

### 必测项目

1. **启动测试**
   - [ ] 打开应用，检查是否安静启动（无"已是最新版本"提示）
   - [ ] 应用正常加载到主页面

2. **手动检查更新**
   - [ ] 进入设置页面
   - [ ] 点击"检查更新"按钮
   - [ ] 当版本一致时，应显示"已是最新版本"
   - [ ] 当有更新时，应显示更新提示

3. **核心功能测试**
   - [ ] 钱包解锁
   - [ ] 余额查询
   - [ ] 转账功能
   - [ ] 资产管理

4. **升级测试**（从旧版本）
   - [ ] 从 v0.1.13 升级，数据保留完整
   - [ ] 从 v0.1.12 升级，数据保留完整
   - [ ] 从更早版本升级，数据保留完整

## 📋 发布检查清单

- [x] 代码修改完成
- [x] 类型检查通过
- [x] Web 资源构建成功
- [x] 版本号已更新
- [x] version.json 已更新
- [x] Android APK 构建成功
- [x] APK 签名验证通过
- [x] 发布说明文档已创建
- [ ] iOS 同步（Xcode 环境问题，暂不处理）
- [ ] 测试验证
- [ ] 发布到 GitHub
- [ ] 通知用户更新

## 🚀 部署步骤

### 1. 发布到 GitHub

```bash
# 提交代码
git add .
git commit -m "chore: release v0.1.14 - 优化版本检查逻辑"
git tag v0.1.14
git push origin main --tags

# 在 GitHub 创建 Release
# 上传 APK 文件：release/SAT20-Wallet-v0.1.14-release-signed.apk
# 附上发布说明：release/RELEASE-v0.1.14.md
```

### 2. 通知用户

- Twitter/X: @sat20labs
- GitHub Releases
- 应用内通知（下次启动时自动检测）

## 📝 其他信息

### Keystore 备份

Keystore 文件已备份到以下位置：
- `release/sat20wallet-release-backup.jks`
- `release/sat20wallet-release-v0.1.12.jks`
- `/Users/icehugh/.backup/sat20wallet/sat20wallet-release.jks`

### 构建环境

- Node.js: 最新版本
- Bun: 最新版本
- Android Gradle: 8.13
- Capacitor: 7.4.3

---

**构建完成时间**: 2026-03-06 23:27
**发布准备状态**: ✅ 就绪，等待测试验证
