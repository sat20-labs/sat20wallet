[根目录](../CLAUDE.md) > **composables**

# Composables 模块文档

## 模块职责

Composables 模块基于 Vue 3 Composition API 封装可复用的业务逻辑，提供资产操作、数据获取、状态管理等组合式函数，实现业务逻辑与 UI 组件的解耦。

## 📍 相对路径导航
- **返回根目录**: [项目首页](../CLAUDE.md)
- **相关模块**:
  - [store](../store/CLAUDE.md) - 状态管理
  - [apis](../apis/CLAUDE.md) - API 集成
  - [utils](../utils/CLAUDE.md) - 工具函数

## Composables 架构概览

### 核心业务逻辑模块
```
composables/
├── useAssetActions.ts      # 资产操作核心逻辑
├── useL1Assets.ts         # L1 资产数据处理
├── useL2Assets.ts         # L2 资产数据处理
├── useNameManager.ts      # 域名解析管理
├── hooks/                 # 钩子函数集合
│   └── useApprove.ts      # 批准流程钩子
└── webview-bridge/        # DApp 通信桥接
    ├── README.md
    └── utils/
        └── approval-handler.ts
```

## 核心 Composables 详解

### 1. useAssetActions (`useAssetActions.ts`)

**职责**: 提供资产转账、存款、提取等核心操作的业务逻辑

**核心功能**:
```typescript
export function useAssetActions() {
  // 响应式状态
  const loading = ref(false)
  const walletStore = useWalletStore()
  const l1Store = useL1Store()
  const { address, feeRate, btcFeeRate } = storeToRefs(walletStore)

  // 资产操作方法
  const deposit = async ({ toAddress, asset_name, amt, utxos, fees }) => {
    loading.value = true
    const [err] = await walletManager.deposit(
      toAddress, asset_name, amt, utxos, fees, btcFeeRate.value
    )
    loading.value = false
    return err
  }

  // 其他操作: send, withdraw, transfer, split 等
}
```

**使用模式**:
```typescript
// 在组件中使用
const { deposit, loading, send } = useAssetActions()

const handleDeposit = async () => {
  const result = await deposit({
    toAddress: 'bc1q...',
    asset_name: 'BTC',
    amt: 1000
  })
}
```

### 2. useL1Assets (`useL1Assets.ts`)

**职责**: 管理比特币层 (L1) 的资产数据获取和处理

**核心功能**:
- BTC 余额查询
- UTXO 列表获取
- ORDX 资产数据
- 交易历史查询
- 手续费估算

### 3. useL2Assets (`useL2Assets.ts`)

**职责**: 管理 SatoshiNet 层 (L2) 的资产数据处理

**核心功能**:
- SATNET 资产管理
- Runes 协议资产
- BRC20 代币处理
- 通道网络操作

### 4. useNameManager (`useNameManager.ts`)

**职责**: 域名解析服务，支持非比特币地址的域名转账

**核心功能**:
```typescript
export function useNameManager() {
  const resolveName = async (name: string): Promise<string> => {
    // 通过 Ordx API 解析域名
    const response = await ordxApi.getNsName({ name, network })
    return response.address
  }

  const validateAddress = (input: string) => {
    // 验证比特币地址格式
    return validateBitcoinAddress(input)
  }

  const resolveAddress = async (input: string): Promise<string> => {
    if (validateAddress(input)) {
      return input // 已经是比特币地址
    }
    return await resolveName(input) // 尝试域名解析
  }
}
```

**集成示例**:
```typescript
// 在资产操作中集成域名解析
const { resolveAddress } = useNameManager()

const recipientAddress = await resolveAddress(userInput)
// 支持: "bc1q...", "example.btc", "user.sat"
```

### 5. hooks/useApprove (`hooks/useApprove.ts`)

**职责**: 批准流程钩子，管理敏感操作的用户确认

**核心功能**:
```typescript
export function useApprove() {
  const requestApproval = async (operation: Operation) => {
    // 1. 验证操作合法性
    validateOperation(operation)

    // 2. 记录待批准操作
    await storePendingOperation(operation)

    // 3. 跳转到批准页面
    router.push('/wallet/approve')
  }

  const confirmApproval = async (operationId: string) => {
    // 用户确认后执行操作
    const operation = await getPendingOperation(operationId)
    await executeOperation(operation)
  }
}
```

### 6. webview-bridge DApp 通信

**职责**: 处理 DApp 与钱包应用之间的通信桥接

**核心功能**:
- Web3 连接管理
- 交易批准处理
- 账户请求处理
- 消息签名
- 事件通信

```typescript
// approval-handler.ts 关键逻辑
export class ApprovalHandler {
  async handleDAppRequest(request: DAppRequest) {
    switch (request.method) {
      case 'eth_requestAccounts':
        return this.handleAccountRequest(request)
      case 'eth_sendTransaction':
        return this.handleTransactionRequest(request)
      case 'personal_sign':
        return this.handleSignRequest(request)
    }
  }
}
```

## 设计模式与最佳实践

### 1. 状态管理模式
```typescript
// Composable 中的状态管理
export function useAssetLogic() {
  // 本地响应式状态
  const loading = ref(false)
  const error = ref<string | null>(null)

  // 全局 Store 状态
  const walletStore = useWalletStore()
  const { address } = storeToRefs(walletStore)

  // 计算属性
  const canOperate = computed(() => {
    return !loading.value && !error.value && address.value
  })

  return {
    loading: readonly(loading),
    error: readonly(error),
    canOperate,
    // methods...
  }
}
```

### 2. 错误处理模式
```typescript
// 统一错误处理
export function useErrorHandler() {
  const handleError = (error: Error, context: string) => {
    console.error(`Error in ${context}:`, error)

    // 记录错误日志
    logError(error, context)

    // 显示用户友好的错误信息
    showErrorToast(getErrorMessage(error))

    // 可选：上报错误到监控系统
    reportError(error, context)
  }

  return { handleError }
}
```

### 3. 异步操作模式
```typescript
// 异步操作的包装器
export function useAsyncOperation() {
  const loading = ref(false)
  const error = ref<string | null>(null)

  const execute = async <T>(
    operation: () => Promise<T>,
    options: { successMessage?: string } = {}
  ): Promise<T | null> => {
    loading.value = true
    error.value = null

    try {
      const result = await operation()

      if (options.successMessage) {
        showSuccessToast(options.successMessage)
      }

      return result
    } catch (err) {
      error.value = getErrorMessage(err)
      showErrorToast(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  return { loading: readonly(loading), error: readonly(error), execute }
}
```

### 4. 生命周期管理
```typescript
// 资源清理和生命周期管理
export function useResourceCleanup() {
  const cleanupTasks: (() => void)[] = []

  const addCleanup = (cleanup: () => void) => {
    cleanupTasks.push(cleanup)
  }

  onUnmounted(() => {
    cleanupTasks.forEach(cleanup => cleanup())
  })

  return { addCleanup }
}
```

## 数据流架构

### 1. 数据获取流程
```
Component → Composable → Store → API → Store → Component
    ↓           ↓         ↓      ↓      ↓       ↓
  触发请求    封装逻辑   状态管理 网络调用 持久化   UI更新
```

### 2. 用户操作流程
```
User Action → Component Event → Composable → Store → Storage → UI Update
     ↓              ↓               ↓           ↓        ↓         ↓
  用户交互      组件事件监听      业务逻辑处理   状态更新  持久化    界面响应
```

### 3. 错误处理流程
```
Error → Composable Catch → Error Handler → User Notification → Log
   ↓           ↓                 ↓                ↓              ↓
 异常发生    业务层捕获        统一处理        用户提示        错误日志
```

## 性能优化策略

### 1. 懒加载和缓存
```typescript
export function useCachedData<T>(
  key: string,
  fetcher: () => Promise<T>,
  ttl: number = 5 * 60 * 1000 // 5分钟
) {
  const data = ref<T | null>(null)
  const loading = ref(false)
  const lastFetch = ref(0)

  const fetchData = async (force = false) => {
    const now = Date.now()
    if (!force && data.value && (now - lastFetch.value) < ttl) {
      return data.value // 返回缓存数据
    }

    loading.value = true
    try {
      data.value = await fetcher()
      lastFetch.value = now
    } finally {
      loading.value = false
    }
    return data.value
  }

  return { data: readonly(data), loading: readonly(loading), fetchData }
}
```

### 2. 防抖和节流
```typescript
export function useDebouncedAction<T extends any[]>(
  action: (...args: T) => void,
  delay: number = 300
) {
  const timeoutId = ref<number>()

  const debouncedAction = (...args: T) => {
    clearTimeout(timeoutId.value)
    timeoutId.value = setTimeout(() => action(...args), delay)
  }

  onUnmounted(() => {
    clearTimeout(timeoutId.value)
  })

  return debouncedAction
}
```

### 3. 内存管理
```typescript
export function useMemoryManagement() {
  const observers: MutationObserver[] = []
  const timers: number[] = []

  const addObserver = (observer: MutationObserver) => {
    observers.push(observer)
  }

  const addTimer = (timer: number) => {
    timers.push(timer)
  }

  onUnmounted(() => {
    observers.forEach(observer => observer.disconnect())
    timers.forEach(timer => clearTimeout(timer))
  })

  return { addObserver, addTimer }
}
```

## 测试策略

### 1. 单元测试
```typescript
// useAssetActions.test.ts
describe('useAssetActions', () => {
  it('should handle deposit operation correctly', async () => {
    const { deposit } = useAssetActions()

    const mockResult = await deposit({
      toAddress: 'bc1q...',
      asset_name: 'BTC',
      amt: 1000
    })

    expect(mockResult).toBeDefined()
    // 更多断言...
  })
})
```

### 2. 集成测试
- 测试 Composable 与 Store 的集成
- 验证 API 调用和数据处理
- 测试错误处理和边界情况

### 3. 端到端测试
- 测试完整的用户流程
- 验证 DApp 通信桥接
- 测试多步骤操作流程

## 开发指南

### 1. Composable 设计原则
- **单一职责**: 每个 Composable 专注于特定功能
- **可复用性**: 设计通用的业务逻辑封装
- **响应式**: 充分利用 Vue 3 响应式系统
- **无副作用**: 保持函数纯净，便于测试

### 2. 命名规范
```typescript
// ✅ 好的命名
useAssetActions()      // 资产操作
useNameManager()       // 名称管理
useErrorHandler()     // 错误处理

// ✅ 功能性命名
useFetchAssets()      // 获取资产
useValidateAddress()  // 验证地址
useCalculateFee()     // 计算手续费
```

### 3. 参数设计
```typescript
// ✅ 清晰的参数设计
interface SendAssetParams {
  toAddress: string
  asset: AssetInfo
  amount: number
  feeRate?: number
  memo?: string
}

const sendAsset = (params: SendAssetParams) => {
  // 实现逻辑
}
```

## 常见问题与解决方案

### 1. 状态同步问题
**问题**: Composable 中的状态与 Store 不同步
**解决**: 使用 `storeToRefs` 确保响应式连接

### 2. 内存泄漏
**问题**: 未正确清理事件监听器和定时器
**解决**: 使用 `onUnmounted` 进行资源清理

### 3. 重复请求
**问题**: 用户快速点击导致重复的 API 调用
**解决**: 实现请求防抖或操作锁

### 4. 错误传播
**问题**: 底层错误未能正确传播到 UI 层
**解决**: 建立统一的错误处理机制

## 相关文件清单

### 核心 Composables
- `useAssetActions.ts` - 资产操作核心逻辑
- `useL1Assets.ts` - L1 资产数据处理
- `useL2Assets.ts` - L2 资产数据处理
- `useNameManager.ts` - 域名解析管理

### 钩子函数
- `hooks/useApprove.ts` - 批准流程钩子
- `hooks/useL1Assets.ts` - L1 资产钩子
- `hooks/useL2Assets.ts` - L2 资产钩子

### DApp 桥接
- `webview-bridge/README.md` - DApp 桥接文档
- `webview-bridge/utils/approval-handler.ts` - 批准处理

### 依赖模块
- `../store/` - 状态管理
- `../apis/` - API 集成
- `../utils/` - 工具函数

---

*模块文档最后更新: 2024-12-03 12:09:40*
*扫描覆盖率: 60% (需要深入扫描)*