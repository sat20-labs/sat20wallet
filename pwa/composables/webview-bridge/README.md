# WebView Bridge 模块重构文档

## 概述

这个目录包含了 WebView Bridge 模块的重构代码，将原本 1757 行的单一文件拆分为多个模块化的组件，提高了代码的可维护性和可读性。

## 目录结构

```
webview-bridge/
├── types.ts                    # 类型定义
├── constants.ts                # 常量配置
├── index.ts                    # 导出接口
├── README.md                   # 本文档
├── utils/                      # 工具类
│   ├── browser-manager.ts      # InAppBrowser 管理器
│   ├── response-handler.ts     # 响应处理器
│   ├── approval-handler.ts     # 授权处理器
│   └── message-manager.ts      # 消息管理器
├── providers/                  # Provider 注入器
│   └── sat20-provider-injection.ts  # SAT20 Provider 注入逻辑
└── handlers/                   # 消息处理器
    ├── index.ts               # 处理器导出
    ├── handler-factory.ts     # 处理器工厂
    ├── account-handlers.ts    # 账户相关处理器
    ├── transaction-handlers.ts # 交易相关处理器
    ├── asset-handlers.ts      # 资产相关处理器
    ├── contract-handlers.ts   # 合约相关处理器
    └── utility-handlers.ts    # 工具类处理器
```

## 主要组件

### 1. BrowserManager (utils/browser-manager.ts)
负责 InAppBrowser 的生命周期管理：
- 打开和关闭浏览器
- 显示/隐藏浏览器（用于原生弹窗）
- 执行脚本注入
- 事件监听器管理

### 2. ResponseHandler (utils/response-handler.ts)
处理向 WebView 发送响应：
- 构建成功/错误响应脚本
- 安全的回调处理
- 注入攻击防护

### 3. ApprovalHandler (utils/approval-handler.ts)
处理用户授权逻辑：
- 需要授权的操作处理
- 直接请求处理（无需授权）
- 钱包状态验证

### 4. MessageManager (utils/message-manager.ts)
统一消息处理入口：
- 消息路由和分发
- Origin 授权检查
- 错误处理和日志记录

### 5. Sat20ProviderInjection (providers/sat20-provider-injection.ts)
SAT20 Provider 注入逻辑：
- 生成 Provider 脚本
- 注入验证
- 事件监听器管理

### 6. Handler Classes (handlers/)
按功能分类的消息处理器：
- **AccountHandlers**: 账户管理相关
- **TransactionHandlers**: 交易签名相关
- **AssetHandlers**: 资产操作相关
- **ContractHandlers**: 智能合约相关
- **UtilityHandlers**: 工具类操作

## 使用方式

### 基本用法（与原 API 兼容）

```typescript
import { useWebViewBridge } from '@/composables/useWebViewBridge';

const {
  isReady,
  injectionStatus,
  lastError,
  currentUrl,
  openDApp,
  close,
  cleanup,
  verifyInjection
} = useWebViewBridge();

// 打开 DApp
const success = await openDApp('https://example.com');
```

### 高级用法（访问内部组件）

```typescript
import { useWebViewBridge, BrowserManager } from '@/composables/useWebViewBridge';

const bridge = useWebViewBridge();

// 访问内部管理器
const browserManager = bridge._browserManager;
const approvalHandler = bridge._approvalHandler;

// 直接使用管理器
browserManager.hideBrowser();
browserManager.showBrowser();
```

### 独立使用组件

```typescript
import {
  BrowserManager,
  ApprovalHandler,
  Sat20ProviderInjection
} from '@/composables/webview-bridge';

// 创建独立的管理器实例
const browserManager = new BrowserManager();
const approvalHandler = new ApprovalHandler(browserManager);
const providerInjection = new Sat20ProviderInjection(browserManager);
```

## 常量和配置

### ACTIONS_REQUIRING_ORIGIN_AUTH
需要 Origin 授权的操作列表。

### INAPP_BROWSER_CONFIG
InAppBrowser 的配置选项，包括：
- 浏览器选项
- 超时时间
- 版本信息
- 平台标识

### LOG_PREFIXES
日志前缀常量，便于日志过滤和调试。

## 类型定义

### 主要接口
- `WebViewBridgeState`: WebView Bridge 状态
- `InjectionStatus`: 注入状态类型
- `InAppBrowserEvent`: 浏览器事件类型
- `ApprovalMetadata`: 授权元数据
- `MessageHandlerMap`: 消息处理器映射

## 迁移指南

### 从原版本迁移

原版本的 API 完全兼容，无需修改现有代码：

```typescript
// 原代码仍然有效
const { isReady, openDApp } = useWebViewBridge();
```

### 扩展新功能

如需访问新的内部功能：

```typescript
// 访问内部状态
const { _state } = useWebViewBridge();
console.log(_state.isBrowserVisible);

// 访问内部管理器
const { _browserManager } = useWebViewBridge();
_browserManager.hideBrowser();
```

## 优势

1. **模块化**: 功能按职责分离，便于维护
2. **可测试性**: 每个组件可独立测试
3. **可扩展性**: 易于添加新的处理器和功能
4. **向后兼容**: 保持原有 API 不变
5. **类型安全**: 完整的 TypeScript 类型支持
6. **代码复用**: 组件可在其他地方独立使用

## 注意事项

1. 内部 API（以下划线开头）用于高级用法和测试，不建议在生产环境中直接使用
2. 新的模块化结构可能会影响某些依赖注入的测试代码
3. 如需扩展新的消息类型，请在对应的 Handler 类中添加方法

## 维护建议

1. 添加新功能时，优先考虑现有的 Handler 分类
2. 保持日志前缀的一致性，便于调试
3. 新增的常量请放入 `constants.ts`
4. 新增的类型请放入 `types.ts`
5. 遵循单一职责原则，保持组件的独立性