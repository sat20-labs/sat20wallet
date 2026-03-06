# SAT20 Wallet v0.1.12 Release Notes

## 📦 发布信息

- **版本号**: v0.1.12
- **构建时间**: 2026-03-06
- **构建类型**: Android APK (已签名)
- **包大小**: 16 MB
- **签名状态**: ✅ 已签名 (SHA384withRSA, 2048-bit)
- **证书有效期**: 2026-03-06 至 2053-07-22

---

## ✨ 新增功能

### 版本更新提醒功能

新增了自动版本检查和更新提醒功能，支持：

#### 1. 自动检查更新
- 应用启动后自动检测最新版本
- 静默检查，有更新时才提醒用户
- Toast 消息通知，不干扰正常使用

#### 2. 手动检查更新
- 设置页面新增「检查更新」按钮
- 实时显示检查状态（检查中动画）
- 支持中英文界面

#### 3. 版本管理机制
- 通过 GitHub 文件链接对比版本号
- 语义化版本比较（SemVer）
- 支持强制更新和普通更新

---

## 🔧 技术实现

### 新增文件
- `composables/useAppVersion.ts` - 版本检查逻辑
- `public/version.json` - 远程版本配置（需上传 GitHub）
- `docs/version-update-feature.md` - 使用文档

### 修改文件
- `entrypoints/popup/App.vue` - 启动时自动检查
- `entrypoints/popup/pages/wallet/Setting.vue` - 手动检查按钮
- `locales/zh.json` & `en.json` - 国际化文本

### 工作流程
1. 应用启动 → 自动检查 GitHub version.json
2. 版本号对比 → 如果有更新则显示 Toast
3. 用户点击设置 → 可手动触发检查

---

## 📁 构建产物

### Android APK
- **Debug 版本**: `SAT20-Wallet-v0.1.12-debug.apk` (20 MB) ✅ 已签名
- **Release 版本**: `SAT20-Wallet-v0.1.12-release-signed.apk` (16 MB) ✅ **已签名**
- **构建方式**: `./gradlew assembleDebug` / `assembleRelease`
- **签名信息**: 
  - Keystore: `sat20wallet-release.jks`
  - 别名：sat20wallet
  - 算法：SHA384withRSA
  - 密钥长度：2048-bit
  - 证书所有者：CN=SAT20 Wallet, OU=SAT20 Labs, O=SAT20

---

## 🚀 部署步骤

### 1. 测试 Debug APK
```bash
# 通过 ADB 安装到设备
adb install release/SAT20-Wallet-v0.1.12-debug.apk

# 或直接发送到手机安装
```

### 2. 准备正式发布

**签名 Release APK**（三选一）：

#### 方法 A: Android Studio（推荐）
```
1. 打开 android/ 目录
2. Build → Generate Signed Bundle / APK
3. 创建新的 Keystore 或选择现有
4. 签名并发布
```

#### 方法 B: 命令行（有 Keystore）
```bash
cd android
./gradlew assembleRelease -PMYAPP_UPLOAD_STORE_FILE=/path/to.keystore \
  -PMYAPP_UPLOAD_KEY_ALIAS=alias \
  -PMYAPP_UPLOAD_KEY_PASSWORD=xxx \
  -PMYAPP_UPLOAD_STORE_PASSWORD=xxx
```

#### 方法 C: jarsigner
```bash
jarsigner -verbose -sigalg SHA256withRSA -digestalg SHA-256 \
  -keystore your-keystore.jks \
  -signedjar release/SAT20-Wallet-v0.1.12-signed.apk \
  release/SAT20-Wallet-v0.1.12-release-unsigned.apk \
  your-alias
```

### 3. 更新远程版本文件

将 `dist/version.json` 上传到 GitHub：

```json
{
  "version": "0.1.12",
  "releaseNotes": "新增版本更新提醒功能",
  "forceUpdate": false,
  "minVersion": "0.1.0",
  "publishedAt": "2026-03-06T10:00:00.000Z"
}
```

```bash
git add dist/
git commit -m "release: v0.1.12 - 新增版本更新提醒功能"
git tag v0.1.12
git push origin main --tags
```

---

## 📝 测试验证

### 功能测试
- ✅ 类型检查通过（vue-tsc）
- ✅ Web 构建成功（vite build）
- ✅ Android Debug APK 构建成功
- ✅ 版本检查逻辑正常
- ✅ Toast 通知显示正确
- ✅ 中英文界面正常

### 兼容性
- ✅ Android 7.0+ (API 24+)
- ✅ Debug 版本可安装测试
- ✅ **Release 版本已签名，可直接安装和发布**

---

## 🎯 下一版本计划

### 待办事项
- [ ] 更新日志详情页面
- [ ] 跳过版本选项
- [ ] 后台下载更新
- [ ] 集成应用商店更新（Capacitor Updater）
- [ ] 分平台版本控制

---

## 📞 技术支持

### 问题反馈
- GitHub Issues: https://github.com/jieziyuan/sat20wallet/issues
- Twitter: @sat20labs

### 文档
- 功能文档：`docs/version-update-feature.md`
- 项目文档：`README.md`
- AGENTS.md：开发指南

---

## 📊 构建统计

```
Web 构建时间：4.31s
Android 构建时间：~33 秒
Gradle Tasks: 287 (153 executed, 104 from cache)
模块数量：2935
APK 大小：
  - Debug: 20 MB
  - Release (signed): 16 MB
签名信息：
  - 算法：SHA384withRSA
  - 密钥长度：2048-bit
  - 有效期：27 年（至 2053-07-22）
  - 证书所有者：CN=SAT20 Wallet, OU=SAT20 Labs, O=SAT20
```

---

## ⚠️ 注意事项

1. **签名文件**: `sat20wallet-release.jks` 需安全备份
2. **密码管理**: Keystore 密码为 `sat20wallet2024`
3. **版本同步**: 及时更新 package.json 和 version.json
4. **测试建议**: 先在测试设备验证
5. **Google Play**: Release APK 已签名，可直接用于发布
6. **证书有效期**: 27 年，至 2053-07-22

---

**发布日期**: 2026-03-06  
**发布负责人**: SAT20 Team  
**状态**: ✅ 已发布
