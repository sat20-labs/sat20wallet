# 🔐 SAT20 Wallet Keystore 信息

## ⚠️ 重要安全警告

**此文件包含敏感的签名密钥信息，请妥善保管！**

- 🔒 **切勿**将此文件提交到 Git 或其他版本控制系统
- 🔒 **切勿**公开分享此文件中的密码
- 💾 **必须**备份到多个安全位置
- 🔐 建议使用密码管理器保存密码

---

## 📋 Keystore 详细信息

### 文件信息
- **文件名**: `sat20wallet-release.jks`
- **路径**: `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/app/sat20wallet-release.jks`
- **大小**: 2.7 KB
- **创建日期**: 2026-03-06 21:30:10

### 证书信息
- **别名**: `sat20wallet`
- **密码**: `sat20wallet2024`
- **密钥算法**: RSA
- **密钥长度**: 2048-bit
- **签名算法**: SHA384withRSA

### 证书持有者
```
CN=SAT20 Wallet
OU=SAT20 Labs
O=SAT20
L=Unknown
ST=Unknown
C=US
```

### 有效期
- **起始**: 2026-03-06 21:30:10 CST
- **结束**: 2053-07-22 21:30:10 CST
- **有效天数**: 10,000 天（约 27 年）

### 证书指纹
- **SHA256**: `5F:8B:92:27:0F:85:14:C4:94:9B:17:88:91:54:D4:F4:ED:C4:C1:01:F5:E7:62:7D:4C:AA:1B:D0:72:2C:C2:03`

---

## 📦 备份位置

| 备份位置 | 路径 | 状态 |
|---------|------|------|
| **主文件** | `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/app/sat20wallet-release.jks` | ✅ 使用中 |
| **备份 1** | `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/app/release/sat20wallet-release-backup.jks` | ✅ 已备份 |
| **备份 2** | `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/app/release/sat20wallet-release-v0.1.12.jks` | ✅ 已备份 |
| **备份 3** | `/Users/icehugh/.backup/sat20wallet/sat20wallet-release.jks` | ✅ 已备份 |

---

## 🔧 使用方法

### 1. 构建签名 APK
```bash
cd /Users/icehugh/workspace/jieziyuan/client/sat20wallet/app

# 方法 A: 使用脚本
./sign-apk.sh

# 方法 B: 手动构建
bun run build
bun run sync
cd android
./gradlew assembleRelease
```

### 2. 验证 APK 签名
```bash
cd /Users/icehugh/workspace/jieziyuan/client/sat20wallet/app
jarsigner -verify -verbose release/SAT20-Wallet-v0.1.12-release-signed.apk
```

### 3. 查看 Keystore 信息
```bash
keytool -list -v -keystore sat20wallet-release.jks \
  -alias sat20wallet \
  -storepass sat20wallet2024
```

---

## 🛡️ 安全建议

### 立即执行
- [ ] 将密码 `sat20wallet2024` 保存到密码管理器
- [ ] 备份到云盘（iCloud、Google Drive、Dropbox 等）
- [ ] 备份到外部硬盘或 USB 驱动器
- [ ] 如果是团队项目，安全分享给团队成员

### 推荐做法
- 使用密码管理器（1Password、Bitwarden、LastPass 等）
- 启用双重认证保护云存储账户
- 定期验证备份文件完整性
- 创建纸质备份存放在保险箱

### 切勿
- ❌ 不要将 keystore 文件提交到 Git
- ❌ 不要通过明文邮件或聊天发送密码
- ❌ 不要删除所有备份
- ❌ 不要修改 keystore 密码（除非必要）

---

## 📝 使用此 Keystore 签名的版本

| 版本 | 日期 | APK 文件 | 状态 |
|------|------|---------|------|
| v0.1.12 | 2026-03-06 | SAT20-Wallet-v0.1.12-release-signed.apk | ✅ 已发布 |

---

## 🔄 恢复方法

如果主文件丢失，可以从备份恢复：

```bash
# 从 release 目录恢复
cp /Users/icehugh/workspace/jieziyuan/client/sat20wallet/app/release/sat20wallet-release-backup.jks \
   /Users/icehugh/workspace/jieziyuan/client/sat20wallet/app/sat20wallet-release.jks

# 从 home 备份恢复
cp /Users/icehugh/.backup/sat20wallet/sat20wallet-release.jks \
   /Users/icehugh/workspace/jieziyuan/client/sat20wallet/app/sat20wallet-release.jks
```

---

## ⚠️ 丢失后果

如果 Keystore 文件丢失且没有备份：

1. **无法更新已发布的应用**
   - Google Play 会拒绝签名不一致的更新
   - 用户无法从旧版本升级到新版本

2. **解决方案**
   - 创建新的 Keystore
   - 更改应用包名（如：`com.sat20.wallet.v2`）
   - 作为新应用重新发布
   - 现有用户需要重新安装

3. **代价**
   - 失去现有用户基础
   - 应用评分和评论丢失
   - 品牌信誉受损

---

## 📞 紧急联系

如果遇到 Keystore 相关问题：

1. 检查所有备份位置
2. 验证密码是否正确
3. 使用 `keytool -list` 验证 Keystore 完整性
4. 联系团队技术负责人

---

**创建日期**: 2026-03-06  
**最后更新**: 2026-03-06  
**版本**: v0.1.12  
**负责人**: SAT20 Team
