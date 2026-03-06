# 版本更新提醒功能使用指南

## ✅ 已实现功能

### 1. 自动检查更新
- **触发时机**: 应用启动后 2 秒自动检查
- **检查方式**: 从 GitHub 读取远程 version.json 文件
- **提醒方式**: Toast 消息通知

### 2. 手动检查更新
- **位置**: 设置页面
- **操作**: 点击「检查更新」按钮
- **反馈**: 实时显示检查结果

---

## 📁 新增文件

### 1. `composables/useAppVersion.ts`
版本检查核心逻辑
- `checkForUpdates(silent: boolean)`: 检查更新
- `manualCheck()`: 手动检查（设置页面使用）
- `compareVersions(current, remote)`: 版本号比较

### 2. `public/version.json` (需上传到 GitHub)
远程版本配置文件

---

## 🚀 使用流程

### 步骤 1: 上传 version.json 到 GitHub

将 `public/version.json` 文件上传到你的 GitHub 仓库根目录：

```bash
# 示例文件内容
{
  "version": "0.1.13",
  "releaseNotes": "修复了转账手续费计算问题，优化了资产列表加载性能",
  "forceUpdate": false,
  "minVersion": "0.1.0",
  "publishedAt": "2026-03-06T10:00:00.000Z"
}
```

**文件路径**: 
```
GitHub 仓库根目录/version.json
→ https://raw.githubusercontent.com/jieziyuan/sat20wallet/main/version.json
```

### 步骤 2: 更新版本号

当需要发布新版本时：

1. **更新本地版本** (package.json):
   ```bash
   bun run bump-version
   ```

2. **更新远程版本** (GitHub version.json):
   - 修改 `version` 字段
   - 填写 `releaseNotes` 更新说明
   - 设置 `forceUpdate` 是否强制更新

3. **提交到 GitHub**:
   ```bash
   git add version.json
   git commit -m "chore: update version to 0.1.13"
   git push
   ```

---

## ⚙️ 配置说明

### version.json 字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `version` | string | ✅ | 版本号（语义化版本） |
| `releaseNotes` | string | ✅ | 更新内容说明 |
| `forceUpdate` | boolean | ❌ | 是否强制更新（默认 false） |
| `minVersion` | string | ❌ | 最低支持版本 |
| `publishedAt` | string | ❌ | 发布时间（ISO 8601） |

### 版本号比较规则

基于语义化版本（SemVer）:
- `0.1.12` < `0.1.13` ✅ 有新版本
- `0.1.12` = `0.1.12` ✅ 已是最新
- `0.2.0` > `0.1.12` ✅ 已是最新（当前版本更新）

---

## 🎨 UI 交互

### 自动检查（silent 模式）
- **无更新**: 静默，不显示任何提示
- **有更新**: 显示 Toast 通知
  - 普通更新：蓝色 info Toast
  - 强制更新：红色 destructive Toast

### 手动检查
- **检查中**: 按钮显示加载动画
- **无更新**: 绿色成功 Toast
- **有更新**: 显示更新提示
- **检查失败**: 红色错误 Toast

---

## 🔍 代码位置

### 修改的文件

1. **`entrypoints/popup/App.vue`**
   ```typescript
   import { useAppVersion } from '@/composables/useAppVersion'
   
   onMounted(() => {
     setTimeout(() => checkForUpdates(true), 2000);
   });
   ```

2. **`entrypoints/popup/pages/wallet/Setting.vue`**
   ```typescript
   const { isChecking, checkForUpdates } = useAppVersion()
   
   // Template
   <Button @click="checkForUpdates(false)">
     检查更新
   </Button>
   ```

3. **`locales/zh.json` & `locales/en.json`**
   ```json
   {
     "setting": {
       "checkUpdate": "检查更新",
       "checking": "检查中..."
     }
   }
   ```

---

## 🛠️ 调试技巧

### 本地测试

1. **修改本地版本号** (package.json):
   ```json
   {
     "version": "0.1.10"  // 改为更小的版本号
   }
   ```

2. **访问远程 version.json**:
   ```
   https://raw.githubusercontent.com/jieziyuan/sat20wallet/main/version.json
   ```

3. **查看 Toast 提示**:
   - 启动应用 → 自动检查
   - 设置页面 → 手动检查

### 常见问题

**Q: 检查失败，提示网络错误？**
- 确保 GitHub 可访问
- 检查 version.json 路径是否正确
- 查看浏览器控制台错误日志

**Q: 版本号相同但提示更新？**
- 检查 version.json 的 version 字段格式
- 确保使用语义化版本（数字.数字。数字）

**Q: 如何测试强制更新？**
- 设置 `forceUpdate: true`
- 重启应用查看红色 Toast

---

## 📝 最佳实践

### 1. 版本发布流程
```
1. 开发完成 → 2. 测试验证 → 3. 更新 version.json → 4. 推送 GitHub → 5. 构建发布
```

### 2. 更新说明撰写
- ✅ 简洁明了：「修复了 XXX 问题」
- ✅ 突出重点：「新增了 XXX 功能」
- ❌ 避免过于技术化

### 3. 强制更新使用场景
- 严重安全漏洞修复
- 关键业务逻辑变更
- 不兼容的 API 更新

---

## 🔮 未来扩展

### 可选功能
- [ ] 更新弹窗（带详细更新日志）
- [ ] 跳过版本选项
- [ ] 更新历史记录
- [ ] 分平台版本控制（iOS/Android/Chrome）
- [ ] 灰度发布支持

### 改进建议
1. 添加更新日志详情页面
2. 支持后台下载更新
3. 集成应用商店更新（Capacitor Updater）
4. 添加更新检查间隔配置

---

## 📞 技术支持

遇到问题？
- 检查浏览器控制台错误
- 查看 network 面板的 version.json 请求
- 确认 GitHub 文件访问权限（公开仓库）

---

*文档创建时间：2026-03-06*
*最后更新：2026-03-06*
