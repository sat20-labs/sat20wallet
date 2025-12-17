# Android APK 签名指南

## 🔐 概述

本文档介绍如何为 SAT20 Wallet 应用生成签名的 APK 文件。签名的 APK 可以用于生产环境发布和安装。

## 📋 前置要求

- Java JDK (包含 keytool 工具)
- Node.js 和 Bun 包管理器
- Android SDK (已配置)
- SAT20 Wallet 项目源码

## 🚀 快速开始

### 方法一：使用自动化脚本（推荐）

1. **生成密钥库并签名 APK**
   ```bash
   ./sign-apk.sh
   ```

   脚本会自动：
   - 检查并生成密钥库（如不存在）
   - 构建前端应用
   - 同步到 Capacitor
   - 构建并签名的 APK
   - 将签名的 APK 复制到 `builds/` 目录

### 方法二：手动步骤

1. **生成签名密钥库**
   ```bash
   ./generate-keystore.sh
   ```

   或手动生成：
   ```bash
   keytool -genkey -v -keystore sat20wallet-release.jks \
           -keyalg RSA -keysize 2048 -validity 10000 -alias sat20wallet
   ```

2. **配置签名信息**

   编辑 `android/gradle.properties`，替换以下占位符：
   ```properties
   MYAPP_UPLOAD_STORE_PASSWORD=你的密钥库密码
   MYAPP_UPLOAD_KEY_PASSWORD=你的密钥密码
   ```

3. **构建并签名 APK**
   ```bash
   # 构建前端
   bun run build

   # 同步到 Capacitor
   bun run sync

   # 构建已签名的 APK
   cd android
   ./gradlew assembleRelease
   ```

   签名后的 APK 位置：
   ```
   android/app/build/outputs/apk/release/app-release.apk
   ```

## 📁 文件结构

签名配置相关文件：

```
app/
├── sat20wallet-release.jks          # 签名密钥库文件（需妥善保管）
├── generate-keystore.sh             # 密钥库生成脚本
├── sign-apk.sh                      # APK 签名构建脚本
├── builds/                          # 构建输出目录
│   └── SAT20Wallet_20241203_143022.apk
├── android/
│   ├── app/build.gradle             # 应用构建配置
│   └── gradle.properties            # Gradle 属性配置（包含签名信息）
```

## ⚠️ 重要安全提醒

1. **密钥库安全**
   - 🔐 `sat20wallet-release.jks` 是应用签名的关键文件
   - 📤 **切勿** 将密钥库文件提交到版本控制系统
   - 💾 **必须** 备份到多个安全位置
   - 🔄 密钥库丢失后将无法更新应用

2. **密码管理**
   - 🔑 使用强密码（至少8位，包含字母、数字、特殊字符）
   - 📝 记录并安全存储密码信息
   - 🔄 `gradle.properties` 包含敏感信息，应在 `.gitignore` 中排除

3. **版本控制**
   ```bash
   # 在 .gitignore 中添加：
   sat20wallet-release.jks
   android/gradle.properties
   builds/
   ```

## 🔍 验证签名

使用 `jarsigner` 验证 APK 签名：
```bash
jarsigner -verify -certs -verbose your-app.apk
```

或使用 `apksigner`（Android SDK 工具）：
```bash
apksigner verify your-app.apk
```

## 📱 安装和测试

1. **卸载调试版本**（如果已安装）
   ```bash
   adb uninstall com.sat20.wallet
   ```

2. **安装签名版本**
   ```bash
   adb install builds/SAT20Wallet_*.apk
   ```

3. **验证安装**
   - 检查应用是否正确安装
   - 确认应用名称为 "SAT20 Wallet"
   - 测试基本功能

## 🛠️ 常见问题

### Q: APK 安装失败，提示签名不一致？
A: 这是因为设备上已安装使用不同密钥签名的应用版本。需要先卸载现有版本：
```bash
adb uninstall com.sat20.wallet
```

### Q: 构建时提示签名密码错误？
A: 检查 `android/gradle.properties` 中的密码配置是否正确：
```properties
MYAPP_UPLOAD_STORE_PASSWORD=正确的密钥库密码
MYAPP_UPLOAD_KEY_PASSWORD=正确的密钥密码
```

### Q: 密钥库文件损坏或丢失？
A: 如果密钥库文件丢失，将无法生成具有相同包名的更新版本。需要：
1. 创建新的密钥库
2. 更改应用的包名
3. 重新发布应用（用户需要重新安装）

### Q: 如何检查 APK 是否已签名？
A: 使用以下命令：
```bash
# 使用 aapt（Android SDK 工具）
aapt dump badging your-app.apk | grep "application-label"

# 使用 jarsigner
jarsigner -verify your-app.apk
```

## 📋 发布清单

发布到应用商店前的检查清单：

- [ ] APK 已正确签名
- [ ] 签名验证通过
- [ ] 应用功能测试完成
- [ ] 密钥库文件已备份
- [ ] 版本号已更新
- [ ] 应用图标和截图准备就绪
- [ ] 应用描述和隐私政策准备就绪

## 🆘 获取帮助

如果遇到问题，请检查：
1. Java JDK 和 Android SDK 版本兼容性
2. 密钥库文件和密码配置
3. 构建日志中的错误信息
4. 确保所有依赖项正确安装

---

**注意：** 本指南基于 Android 应用签名标准流程。对于生产环境发布，建议参考官方文档和安全最佳实践。