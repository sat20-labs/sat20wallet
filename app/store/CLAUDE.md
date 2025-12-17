[根目录](../CLAUDE.md) > **store**

# Store 模块文档

## 模块职责

Store 模块基于 Pinia 实现应用的状态管理，负责管理钱包状态、用户配置、资产数据、网络设置等核心业务状态，提供响应式的状态更新和持久化存储。

## 📍 相对路径导航
- **返回根目录**: [项目首页](../CLAUDE.md)
- **相关模块**:
  - [entrypoints/popup](../entrypoints/popup/CLAUDE.md) - 主界面
  - [lib](../lib/CLAUDE.md) - 存储层
  - [composables](../composables/CLAUDE.md) - 业务逻辑

## Store 架构概览

### 核心状态模块
```
store/
├── global.ts      # 全局配置和环境管理
├── wallet.ts      # 钱包状态和账户管理
├── l1.ts         # 比特币层 (L1) 操作状态
├── l2.ts         # SatoshiNet 层 (L2) 操作状态
└── approve.ts    # 批准流程状态管理
```

## 状态模块详解

### 1. Global Store (`global.ts`)

**职责**: 管理全局配置、环境设置、应用状态

**核心状态**:
```typescript
interface GlobalState {
  loading: boolean           // 全局加载状态
  version: number           // 应用版本
  env: 'dev' | 'test' | 'prd' // 当前环境
  stpVersion: string        // STP 协议版本
  config: Config            // 环境配置对象
}
```

**关键 Actions**:
- `setLoading(value: boolean)` - 设置全局加载状态
- `setVersion(value: number)` - 更新应用版本
- `setEnv(value: Env)` - 切换环境配置
- `setStpVersion(value: string)` - 设置 STP 版本

**使用模式**:
```typescript
const globalStore = useGlobalStore()

// 访问当前配置
const config = computed(() => configMap[globalStore.env])

// 切换环境
await globalStore.setEnv('test')
```

### 2. Wallet Store (`wallet.ts`)

**职责**: 管理钱包账户、地址、密码、锁定状态等核心钱包信息

**核心状态**:
```typescript
interface WalletState {
  address: string | null          // 当前地址
  publicKey: string | null        // 公钥
  walletId: string               // 钱包ID
  accountIndex: number           // 账户索引
  feeRate: number                // 手续费率
  btcFeeRate: number             // BTC 手续费率
  satsnetFeeRate: number         // SatoshiNet 手续费率
  password: string | null        // 密码
  network: Network               // 网络 (LIVENET/TESTNET)
  chain: Chain                   // 链 (BTC/SATNET)
  locked: boolean                // 锁定状态
  hasWallet: boolean             // 是否有钱包
  wallets: WalletData[]          // 钱包列表
  isSwitchingWallet: boolean     // 钱包切换状态
  isSwitchingAccount: boolean    // 账户切换状态
}
```

**关键 Actions**:
- `setLocked(value: boolean)` - 设置锁定状态
- `setNetwork(network: Network)` - 切换网络
- `setChain(chain: Chain)` - 切换链
- `unlockWallet(password: string)` - 解锁钱包
- `createWallet(data: CreateWalletData)` - 创建钱包
- `importWallet(data: ImportWalletData)` - 导入钱包
- `switchWallet(walletId: string)` - 切换钱包
- `switchAccount(index: number)` - 切换账户

**状态同步机制**:
```typescript
// 监听 walletStorage 状态变化
walletStorage.subscribe((key, newValue, oldValue) => {
  switch (key) {
    case 'locked':
      locked.value = newValue ?? true
      break
    case 'address':
      address.value = newValue
      break
    // ... 其他状态同步
  }
})
```

### 3. L1 Store (`l1.ts`)

**职责**: 管理比特币层 (L1) 的资产状态、UTXO 管理和交易相关数据

**核心功能**:
- BTC 余额管理
- UTXO 列表维护
- ORDX 资产数据
- 交易历史记录
- 手续费计算

### 4. L2 Store (`l2.ts`)

**职责**: 管理 SatoshiNet 层 (L2) 的状态数据和资产信息

**核心功能**:
- SATNET 资产管理
- Runes 协议支持
- BRC20 资产处理
- 通道网络操作

### 5. Approve Store (`approve.ts`)

**职责**: 管理批准流程的状态，处理用户确认和授权操作

**核心功能**:
- 待批准操作队列
- 批准历史记录
- 操作状态跟踪
- 安全策略管理

## 状态管理模式

### 1. 初始化流程
```typescript
// 应用启动时的状态初始化顺序
1. walletStorage.initializeState() - 初始化本地存储
2. useWalletStore() - 创建钱包状态实例
3. useGlobalStore() - 创建全局状态实例
4. 状态订阅建立 - Store 与 Storage 同步
5. 路由守卫验证 - 检查锁定和权限状态
```

### 2. 状态持久化
```typescript
// walletStorage 与 Store 的双向同步
class WalletStorage {
  // Store → Storage
  async setValue(key, value)

  // Storage → Store (通过订阅)
  subscribe(callback)

  // 批量更新
  async batchUpdate(updates)
}
```

### 3. 状态更新流程
```typescript
// 标准状态更新模式
1. 组件调用 Store Action
2. Action 更新内部状态
3. Action 同步到 walletStorage
4. Storage 触发订阅回调
5. Store 更新响应式状态
6. 组件自动重新渲染
```

## 响应式数据流

### 数据获取流程
```typescript
// 资产数据获取示例
Component → useL1Assets() → l1Store Actions → API 调用 → Store 状态更新 → 组件响应
```

### 用户交互流程
```typescript
// 用户操作响应示例
用户操作 → Component Event → Store Action → 状态更新 → Storage 持久化 → UI 响应
```

## 性能优化策略

### 1. 计算属性缓存
```typescript
// 使用 computed 缓存昂贵计算
const balance = computed(() => {
  return confirmed.value + unconfirmed.value
})
```

### 2. 批量状态更新
```typescript
// 避免多次单独更新，使用批量操作
await walletStorage.batchUpdate({
  address: newAddress,
  network: newNetwork,
  locked: false
})
```

### 3. 状态订阅优化
```typescript
// 精确订阅需要的状态变化
walletStorage.subscribe((key, newValue, oldValue) => {
  if (key === 'address') {
    // 只处理地址变化
  }
})
```

## 安全机制

### 1. 密码管理
```typescript
// 密码安全处理
const updatePassword = async (password: string | null) => {
  const updates = {
    password,
    passwordTime: password ? new Date().getTime() : null
  }
  await batchUpdate(updates)
}
```

### 2. 锁定策略
```typescript
// 自动锁定机制 (5分钟无操作)
const checkPassword = async () => {
  const passwordTime = walletStorage.getValue('passwordTime')
  if (passwordTime) {
    const timeDiff = Date.now() - passwordTime
    if (timeDiff > 5 * 60 * 1000) {
      await walletStorage.setValue('password', null)
      await walletStorage.setValue('locked', true)
    }
  }
}
```

### 3. 状态验证
```typescript
// 关键操作前的状态验证
const validateState = () => {
  if (!walletStore.hasWallet) {
    throw new Error('No wallet available')
  }
  if (walletStore.locked) {
    throw new Error('Wallet is locked')
  }
}
```

## 错误处理

### 1. Store 错误边界
```typescript
// Store Action 错误处理模式
const safeAction = async (operation: () => Promise<void>) => {
  try {
    loading.value = true
    await operation()
  } catch (error) {
    console.error('Store action failed:', error)
    // 回滚状态或显示错误信息
  } finally {
    loading.value = false
  }
}
```

### 2. 状态一致性检查
```typescript
// 状态一致性验证
const validateStateConsistency = () => {
  const storageState = walletStorage.getState()
  const storeState = getState()

  // 检查关键状态是否一致
  if (storageState.address !== storeState.address) {
    // 强制同步状态
    address.value = storageState.address
  }
}
```

## 开发最佳实践

### 1. 状态设计原则
- **最小状态**: 只存储必要的状态数据
- **单一职责**: 每个 Store 专注于特定领域
- **不可变性**: 避免直接修改状态对象

### 2. Action 设计模式
```typescript
// 标准 Action 设计
const performAction = async (params: ActionParams) => {
  // 1. 参数验证
  validateParams(params)

  // 2. 状态更新 (乐观更新)
  updateOptimisticState(params)

  try {
    // 3. 执行操作
    const result = await executeOperation(params)

    // 4. 确认状态更新
    updateConfirmedState(result)

    return result
  } catch (error) {
    // 5. 回滚状态
    rollbackState()
    throw error
  }
}
```

### 3. 状态订阅管理
```typescript
// 组件卸载时清理订阅
onUnmounted(() => {
  unsubscribe()
})
```

## 测试策略

### 1. 单元测试
```typescript
// Store 测试示例
describe('Wallet Store', () => {
  it('should unlock wallet with correct password', async () => {
    const store = useWalletStore()
    await store.unlockWallet('correct-password')
    expect(store.locked).toBe(false)
  })
})
```

### 2. 集成测试
- 测试 Store 与 walletStorage 的集成
- 验证状态持久化功能
- 测试状态订阅机制

### 3. 端到端测试
- 测试完整的状态流转
- 验证用户操作的状态响应
- 测试错误恢复机制

## 监控和调试

### 1. 状态变化追踪
```typescript
// 开发环境状态变化日志
if (process.env.NODE_ENV === 'development') {
  walletStorage.subscribe((key, newValue, oldValue) => {
    console.log(`State changed: ${key}`, { oldValue, newValue })
  })
}
```

### 2. 性能监控
```typescript
// Store 操作性能监控
const monitoredAction = async (action: () => Promise<void>) => {
  const start = performance.now()
  await action()
  const duration = performance.now() - start
  console.log(`Action completed in ${duration}ms`)
}
```

## 相关文件清单

### 核心 Store 文件
- `global.ts` - 全局配置管理
- `wallet.ts` - 钱包状态管理
- `l1.ts` - 比特币层状态
- `l2.ts` - SatoshiNet 层状态
- `approve.ts` - 批准流程管理

### 依赖文件
- `../lib/walletStorage.ts` - 存储适配器
- `../types/index.ts` - 类型定义
- `../config/index.ts` - 配置管理

---

*模块文档最后更新: 2024-12-03 12:09:40*
*扫描覆盖率: 90%*