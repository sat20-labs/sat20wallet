[根目录](../../CLAUDE.md) > [entrypoints](../) > **popup**

# Popup 模块文档

## 模块职责

Popup 模块是 SAT20 Wallet 移动端应用的主界面入口，负责用户交互的核心流程，包括钱包管理、资产操作、设置配置等关键功能。

## 📍 相对路径导航
- **返回上级**: [entrypoints](../)
- **返回根目录**: [项目首页](../../CLAUDE.md)
- **相关模块**:
  - [store](../../store/CLAUDE.md) - 状态管理
  - [components](../../components/CLAUDE.md) - UI 组件

## 入口与启动

### 主入口文件
- **`App.vue`**: 应用根组件，定义整体布局结构
- **`main.ts`**: 应用启动入口，处理 WASM 加载和应用初始化

### 启动流程
```typescript
// main.ts 关键启动序列
1. loadWasm() - 加载 WebAssembly 钱包核心
2. walletStorage.initializeState() - 初始化存储状态
3. createApp(App) - 创建 Vue 应用实例
4. 配置 Pinia、Router、i18n 等插件
5. app.mount('#app') - 挂载到 DOM
```

## 核心页面结构

### 页面路由层级
```
popup/
├── App.vue                 # 根组件
└── pages/                  # 页面组件
    ├── Index.vue          # 首页/欢迎页
    ├── Import.vue         # 导入钱包
    ├── Create.vue         # 创建钱包
    ├── Unlock.vue         # 解锁页面
    └── wallet/            # 钱包功能模块
        ├── index.vue      # 钱包主页
        ├── Receive.vue    # 接收资产
        ├── Approve.vue    # 批准操作
        ├── split.vue      # 资产拆分
        ├── NameSelect.vue # 名称选择
        ├── Setting.vue    # 设置主页
        └── settings/      # 详细设置
            ├── phrase.vue     # 助记词管理
            ├── publickey.vue  # 公钥管理
            ├── password.vue   # 密码设置
            ├── node.vue       # 节点配置
            ├── UtxoManager.vue # UTXO 管理
            └── referrer/      # 推荐人系统
                ├── index.vue  # 推荐人主页
                └── bind.vue   # 绑定推荐人
```

## 核心业务流程

### 1. 钱包生命周期
```typescript
// 钱包创建/导入流程
Index.vue → Create.vue/Import.vue → wallet/index.vue

// 钱包解锁流程
wallet/index.vue → Unlock.vue → wallet/index.vue
```

### 2. 资产管理流程
```typescript
// 资产接收
wallet/index.vue → Receive.vue

// 资产转账
wallet/index.vue → AssetOperationDialog → Approve.vue

// 资产拆分
wallet/index.vue → split.vue → Approve.vue
```

### 3. 设置管理流程
```typescript
// 基础设置
wallet/index.vue → Setting.vue

// 安全设置
Setting.vue → password.vue/phrase.vue

// 高级设置
Setting.vue → node.vue/UtxoManager.vue
```

## 关键组件集成

### Store 集成
- **`useWalletStore()`**: 钱包状态管理
- **`useGlobalStore()`**: 全局配置管理
- **`useL1Store()` / `useL2Store()`**: 分层资产管理

### Composables 集成
- **`useAssetActions()`**: 资产操作逻辑
- **`useL1Assets()` / `useL2Assets()`**: 资产数据获取
- **`useNameManager()`**: 域名解析服务

### API 集成
- **ordxApi**: 比特币资产数据接口
- **satnetApi**: SatoshiNet 网络接口

## 状态管理模式

### 路由守卫
```typescript
// router/index.ts 关键守卫逻辑
router.beforeEach(async (to, from, next) => {
  // 1. 初始化存储状态
  await walletStorage.initializeState()

  // 2. 检查钱包存在性
  const hasWallet = walletStorage.getValue('hasWallet')

  // 3. 验证锁定状态和密码时效
  await checkPassword()

  // 4. 路由权限控制
  if (to.path.startsWith('/wallet')) {
    // 需要钱包存在且解锁
  }
})
```

### 状态同步
- **walletStorage**: 持久化存储管理
- **Pinia Stores**: 响应式状态管理
- **组件间通信**: 通过 Props/Events 和 Store

## 安全机制

### 访问控制
- **钱包锁定**: 5分钟无操作自动锁定
- **密码验证**: 敏感操作需要密码确认
- **生物识别**: 支持指纹/面容解锁

### 数据保护
- **本地加密**: 敏感数据本地加密存储
- **WASM 隔离**: 加密操作在 WebAssembly 中执行
- **通信安全**: API 通信使用 HTTPS

## 用户体验优化

### 响应式设计
- **移动优先**: 专为移动端屏幕优化
- **触摸友好**: 按钮和交互区域适配触摸操作
- **适配性**: 支持不同屏幕尺寸和方向

### 性能优化
- **懒加载**: 路由级别的组件懒加载
- **缓存策略**: 资产数据和配置信息缓存
- **状态优化**: 避免不必要的重新渲染

## 国际化支持

### 多语言配置
```typescript
// main.ts 国际化设置
const i18n = createI18n({
  legacy: false,
  locale: savedLanguage || 'en',
  fallbackLocale: 'en',
  messages: { en, zh }
})
```

### 语言切换
- 支持英语 (en) 和中文 (zh)
- 语言偏好持久化存储
- 动态语言切换无需重启

## 测试状态

### 当前测试覆盖
- **单元测试**: ❌ 未配置
- **组件测试**: ❌ 未配置
- **集成测试**: ❌ 未配置
- **端到端测试**: ❌ 未配置

### 建议测试方案
1. **组件级测试**: 使用 Vue Test Utils 测试页面组件
2. **路由测试**: 验证路由守卫和导航逻辑
3. **状态测试**: 测试 Store 和存储集成
4. **端到端测试**: 使用 Playwright 测试完整用户流程

## 常见问题与解决方案

### WASM 加载问题
**问题**: WebAssembly 模块加载失败
**解决**: 检查 `/public/wasm/sat20wallet.wasm` 文件路径和网络访问

### 状态同步问题
**问题**: Store 状态与本地存储不同步
**解决**: 确保 `walletStorage.initializeState()` 在路由守卫中正确调用

### 路由权限问题
**问题**: 用户直接访问受保护的路由
**解决**: 完善路由守卫逻辑，添加更严格的权限检查

## 开发建议

### 代码组织
- 按功能模块组织页面组件
- 使用 Composition API 保持代码简洁
- 合理拆分复杂页面为子组件

### 状态管理
- 优先使用 Pinia 进行状态管理
- 避免直接修改存储状态
- 使用 Composables 封装业务逻辑

### 性能优化
- 实现虚拟滚动处理大量数据
- 使用计算属性优化重复计算
- 合理使用 `v-memo` 指令

## 相关文件清单

### 核心文件
- `App.vue` - 应用根组件
- `pages/Index.vue` - 首页组件
- `pages/wallet/index.vue` - 钱包主页
- `pages/Unlock.vue` - 解锁页面

### 功能页面
- `pages/Create.vue` - 创建钱包
- `pages/Import.vue` - 导入钱包
- `pages/wallet/Receive.vue` - 接收资产
- `pages/wallet/Approve.vue` - 批准操作

### 设置页面
- `pages/wallet/Setting.vue` - 设置主页
- `pages/wallet/settings/password.vue` - 密码设置
- `pages/wallet/settings/phrase.vue` - 助记词管理

---

*模块文档最后更新: 2024-12-03 12:09:40*
*扫描覆盖率: 85%*