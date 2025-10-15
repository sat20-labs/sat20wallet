# Storage Migration: @capacitor/storage → localStorage

## 概述

将项目中的存储系统从 `@capacitor/storage` 迁移到 `localStorage`，以支持纯 web 环境的使用。

## 修改内容

### 1. 创建存储适配器
- 新建 `lib/storage-adapter.ts` 文件
- 提供与 `@capacitor/storage` 相同的 API 接口
- 使用 `localStorage` 作为底层存储

### 2. 更新的文件
- `lib/walletStorage.ts` - 主要钱包存储逻辑
- `lib/authorized-origins.ts` - 授权域名存储
- `lib/nodeStakeStorage.ts` - 节点质押数据存储
- `composables/useReferrerManager.ts` - 推荐人管理
- `components/setting/DeleteWallet.vue` - 删除钱包组件

### 3. API 兼容性
新的存储适配器提供完全相同的接口：
```typescript
interface StorageAdapter {
  get({ key }: { key: string }): Promise<{ value: string | null }>
  set({ key, value }: { key: string; value: string }): Promise<void>
  remove({ key }: { key: string }): Promise<void>
  clear(): Promise<void>
}
```

### 4. 安全改进
- `clear()` 方法只清除钱包相关的数据，不会影响其他 localStorage 数据
- 支持的前缀：`local:wallet_`, `session:wallet_`, `authorized_origins`, `node_stake_`, `referrer_`

## 使用方法

```typescript
import { Storage } from '@/lib/storage-adapter'

// 使用方式与之前完全相同
await Storage.set({ key: 'test', value: 'data' })
const result = await Storage.get({ key: 'test' })
await Storage.remove({ key: 'test' })
await Storage.clear()
```

## 测试

可以使用 `lib/storage-test.ts` 中的测试函数来验证存储适配器是否正常工作：

```typescript
import { testStorageAdapter } from '@/lib/storage-test'
await testStorageAdapter()
```

## 注意事项

1. **数据迁移**: 如果之前使用 `@capacitor/storage` 存储了数据，需要手动迁移到新的存储格式
2. **浏览器兼容性**: 确保目标浏览器支持 `localStorage`
3. **存储限制**: `localStorage` 通常有 5-10MB 的存储限制
4. **同步操作**: `localStorage` 是同步的，但适配器保持了异步接口以保持兼容性

## 依赖关系

现在可以考虑从 `package.json` 中移除 `@capacitor/storage` 依赖（如果不再需要 Capacitor 功能）。
