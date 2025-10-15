# Netlify CSP 配置指南

## 概述

为了解决iframe脚本注入问题，我们需要在Netlify上配置适当的Content Security Policy (CSP)头部。这个文档说明如何配置和使用不同环境的CSP设置。

## 配置文件说明

### 1. `public/_headers` - 生产环境配置
- 安全的CSP策略，适用于生产环境
- 允许必要的脚本注入，但保持安全限制
- 支持iframe和跨域资源

### 2. `public/_headers.dev` - 开发环境配置  
- 更宽松的CSP策略，便于开发和测试
- 允许所有来源的脚本和资源
- 适用于本地开发和测试环境

### 3. `netlify.toml` - Netlify配置文件
- 包含完整的Netlify部署配置
- 定义了不同路径的CSP策略
- 包含构建设置和重定向规则

## 使用方法

### 快速设置

```bash
# 设置生产环境CSP（推荐用于部署）
bun run csp:prod

# 设置开发环境CSP（用于测试）
bun run csp:dev

# 构建并应用生产环境CSP
bun run build:prod

# 构建并应用开发环境CSP
bun run build:dev
```

### 手动设置

```bash
# 使用脚本设置CSP
node scripts/setup-csp.js production   # 生产环境
node scripts/setup-csp.js development # 开发环境
```

## CSP策略说明

### 生产环境策略
```
Content-Security-Policy: 
  default-src 'self';
  script-src 'self' 'unsafe-inline' 'unsafe-eval' blob: data: https:;
  style-src 'self' 'unsafe-inline' https:;
  img-src 'self' data: https: blob:;
  font-src 'self' data: https:;
  connect-src 'self' https: wss: ws:;
  frame-src 'self' https:;
  frame-ancestors 'self' https:;
  object-src 'none';
  base-uri 'self';
  form-action 'self' https:;
```

### 关键策略解释
- `script-src 'unsafe-inline' 'unsafe-eval'` - 允许内联脚本和eval，用于iframe注入
- `frame-src 'self' https:` - 允许加载HTTPS iframe
- `frame-ancestors 'self' https:` - 允许被HTTPS页面嵌入
- `connect-src wss: ws:` - 允许WebSocket连接

## 部署到Netlify

### 方法1：使用 `_headers` 文件（推荐）
1. 运行 `bun run csp:prod` 设置生产环境CSP
2. 构建项目：`bun run build`
3. 部署 `dist` 目录到Netlify
4. `_headers` 文件会自动被Netlify识别

### 方法2：使用 `netlify.toml` 文件
1. 确保项目根目录有 `netlify.toml` 文件
2. 构建并部署项目
3. Netlify会自动应用配置文件中的设置

## 验证CSP配置

### 1. 检查HTTP头部
```bash
curl -I https://your-app.netlify.app
```

### 2. 浏览器开发者工具
1. 打开Network标签
2. 刷新页面
3. 查看响应头中的 `Content-Security-Policy`

### 3. CSP验证工具
- 使用在线CSP验证器检查策略语法
- 检查浏览器控制台是否有CSP违规报告

## 常见问题

### Q: iframe脚本注入仍然失败？
A: 检查以下几点：
1. 确认CSP头部已正确设置
2. 检查目标网站是否有自己的CSP限制
3. 查看浏览器控制台的CSP违规报告
4. 尝试使用开发环境的宽松策略测试

### Q: 如何调试CSP问题？
A: 
1. 使用 `bun run csp:dev` 设置宽松策略
2. 在浏览器控制台查看详细错误信息
3. 逐步收紧CSP策略直到找到最小权限集

### Q: 生产环境和开发环境的区别？
A:
- 生产环境：安全优先，最小权限原则
- 开发环境：便于调试，允许更多权限

## 安全注意事项

1. **生产环境**应使用严格的CSP策略
2. **避免使用** `'unsafe-inline'` 和 `'unsafe-eval'`（除非必要）
3. **定期审查**CSP策略，移除不必要的权限
4. **监控**CSP违规报告，及时发现安全问题

## 更新CSP配置

当需要修改CSP策略时：

1. 编辑对应的配置文件（`_headers` 或 `_headers.dev`）
2. 运行相应的设置脚本
3. 测试新配置
4. 重新部署到Netlify

## 相关链接

- [Netlify Headers Documentation](https://docs.netlify.com/routing/headers/)
- [CSP Reference](https://developer.mozilla.org/en-US/docs/Web/HTTP/CSP)
- [CSP Evaluator](https://csp-evaluator.withgoogle.com/)

