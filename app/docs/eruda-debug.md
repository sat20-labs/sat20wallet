# Eruda 移动端调试工具使用指南

## 概述

SAT20 钱包已集成 [Eruda](https://github.com/liriliri/eruda) 调试工具，专为移动端 Web 应用设计，提供类似 Chrome DevTools 的调试功能。

## 功能特性

- 📱 **移动端优化**: 专为移动设备浏览器设计
- 🛠️ **完整调试工具**: Console、Elements、Network、Resources、Info、Snippets
- 🎨 **可定制界面**: 支持主题和透明度调整
- 🔧 **智能启用**: 仅在开发环境或手动启用时加载

## 启用方式

### 1. 默认启用

**🎯 SAT20 钱包已配置为默认启用 eruda 调试工具**

应用启动时会自动加载 eruda，无需任何配置。在以下场景中都会自动启用：
- ✅ 开发环境（桌面浏览器）
- ✅ 移动设备（手机/平板）
- ✅ 生产构建版本
- ✅ Capacitor 移动应用

### 2. 手动控制

在任何环境中，都可以通过控制台命令手动控制：

```javascript
// 启用调试工具（通常不需要，默认已启用）
erudaControl.enable()

// 禁用调试工具（如需要关闭调试功能）
erudaControl.disable()

// 切换调试状态
erudaControl.toggle()

// 检查是否已启用
erudaControl.isEnabled()

// 重置为默认启用状态
erudaControl.reset()

// 查看当前状态
erudaControl.getStatus()
```

## 使用方法

### 访问调试面板

1. **启动应用**: 在移动设备上打开 SAT20 钱包应用
2. **显示调试工具**:
   - 点击右下角的 eruda 图标
   - 或通过手势从屏幕右侧向左滑动

### 调试功能说明

#### 📋 Console 控制台
- 查看应用日志信息
- 执行 JavaScript 代码
- 支持 console.log、error、warn 等方法

#### 🎨 Elements 检查器
- 检查和修改 DOM 元素
- 实时查看 CSS 样式
- 编辑页面结构和样式

#### 🌐 Network 网络
- 监控 HTTP/HTTPS 请求
- 查看请求/响应详情
- 分析网络性能

#### 📦 Resources 资源
- 查看 LocalStorage、SessionStorage
- 检查 Cookie 和应用缓存
- 监控资源使用情况

#### ℹ️ Info 信息
- 查看设备和浏览器信息
- 监控应用性能指标
- 显示系统状态

#### 📝 Snippets 代码片段
- 保存常用调试代码
- 快速执行预设脚本
- 提高调试效率

## 配置选项

### 当前配置

```javascript
{
  defaults: {
    displaySize: 50,    // 调试面板大小
    transparency: 0.9   // 透明度设置
  },
  tool: [               // 启用的工具
    'console',          // 控制台
    'elements',         // 元素检查器
    'network',          // 网络监控
    'resources',        // 资源管理
    'info',             // 系统信息
    'snippets'          // 代码片段
  ],
  autoScale: true       // 自动缩放适配屏幕
}
```

### 启用逻辑说明

- **默认状态**: 应用启动时自动启用 eruda
- **禁用条件**: 只有当 localStorage 中 `eruda-debug` 设置为 `'false'` 时才禁用
- **持久化**: 启用/禁用状态会保存到本地存储
- **即时生效**: 控制命令可以立即显示/隐藏调试面板

### 自定义配置

如需修改配置，可在 `main.ts` 中的 `initEruda` 函数进行调整：

```javascript
eruda.default.init({
  defaults: {
    displaySize: 60,    // 调整面板大小
    transparency: 0.8   // 调整透明度
  },
  // ... 其他配置
})
```

## 最佳实践

### 1. 性能考虑
- eruda 仅在需要时启用，生产环境默认关闭
- 通过 `localStorage` 控制启用状态
- 使用动态导入减少包体积影响

### 2. 安全注意事项
- 生产环境请确保 eruda 已禁用
- 避免在生产代码中暴露敏感信息
- 定期检查调试模式状态

### 3. 调试技巧
- 使用 Console 过滤功能快速定位日志
- 结合 Elements 检查器调试 UI 问题
- 通过 Network 监控分析 API 调用
- 利用 Snippets 保存常用调试代码

## 常见问题

### Q: eruda 在所有环境中都默认启用吗？
A: 是的，SAT20 钱包已配置为默认启用，包括桌面浏览器和移动设备。

### Q: 如何临时禁用调试工具？
A: 在控制台执行 `erudaControl.disable()` 或设置 `localStorage.setItem('eruda-debug', 'false')`。

### Q: 启用后应用变慢了？
A: eruda 会增加一定性能开销，建议调试完成后执行 `erudaControl.disable()` 禁用。

### Q: 如何重置为默认启用状态？
A: 执行 `erudaControl.reset()` 或清除 localStorage 中的 `eruda-debug` 项。

### Q: 在生产环境中使用安全吗？
A: 建议生产环境部署时手动禁用，或通过环境变量控制默认行为。

## 相关链接

- [Eruda 官方文档](https://github.com/liriliri/eruda)
- [移动端调试最佳实践](https://developers.google.com/web/tools/chrome-devtools/remote-debugging)
- [Capacitor 调试指南](https://capacitorjs.com/docs/guides/debugging)

---

*最后更新: 2025-10-23*