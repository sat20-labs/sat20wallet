# AGENTS.md - SAT20 Wallet 代码库开发指南

## 📦 项目概述

SAT20 Wallet 是一个基于 Vue 3、TypeScript 和 Capacitor 构建的比特币钱包移动应用，支持多链操作（BTC、ORDX、Runes、BRC20）。

**技术栈**: Vue 3.5+ | TypeScript 5.6 | Pinia | Vite 7 | Tailwind CSS | Shadcn-Vue | Radix-Vue

---

## 🛠️ 开发命令

### 基础命令
```bash
bun run dev              # 启动开发服务器 (http://localhost:5173)
bun run build            # 生产构建（含类型检查）
bun run build:skip-check # 生产构建（跳过类型检查）
bun run compile          # 仅类型检查 (vue-tsc --noEmit)
bun run preview          # 预览构建结果
```

### 移动端命令
```bash
bun run sync             # 同步到 Capacitor (iOS/Android)
npm run ionic:build      # Ionic 构建
npm run ionic:serve      # Ionic 开发服务
```

### 工具命令
```bash
bun run bump-version     # 版本号升级 (SemVer)
bun run copy-latest-zip  # 复制最新构建包到 release 目录
```

### 测试命令
**当前未配置测试框架**。建议未来添加：
- Vitest + Vue Test Utils (单元测试)
- Playwright (端到端测试)

---

## 📝 代码风格指南

### 1. 文件命名
- **Vue 组件**: PascalCase (例：`AssetOperationDialog.vue`)
- **Composables**: `useXxx.ts` (例：`useL1Assets.ts`)
- **Pinia Stores**: kebab-case (例：`global.ts`, `wallet.ts`)
- **工具函数**: kebab-case (例：`walletStorage.ts`)

### 2. 导入顺序
```typescript
// 1. Vue 核心
import { ref, computed } from 'vue'

// 2. 第三方库
import { defineStore } from 'pinia'
import { z } from 'zod'

// 3. 项目别名 (@ = 根目录)
import { config } from '@/config'
import { walletStorage } from '@/lib/walletStorage'

// 4. 相对路径
import { Button } from '@/components/ui/button'
```

### 3. Vue 组件结构
```vue
<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'

// Props 定义 (使用 TypeScript interface)
interface Props {
  variant?: 'default' | 'destructive'
  class?: HTMLAttributes['class']
}

// 默认值
const props = withDefaults(defineProps<Props>(), {
  variant: 'default',
})

// Emits 定义
const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()
</script>

<template>
  <div :class="cn('base-class', props.class)">
    <slot />
  </div>
</template>
```

### 4. TypeScript 规范
- **严格模式**: `strict: true` (tsconfig.json)
- **类型优先**: 使用 `interface` 定义对象类型，`type` 定义联合/交叉类型
- **禁止**: `any`, `as any`, `@ts-ignore`, `@ts-expect-error`
- **路径别名**: `@/*` 映射到根目录

### 5. 错误处理
```typescript
// async/await + try/catch 模式
try {
  const result = await riskyOperation()
  return result
} catch (error) {
  console.error('Operation failed:', error)
  throw new Error('用户友好的错误信息')
}

// Zod 验证
const schema = z.object({
  email: z.string().email(),
  amount: z.number().positive()
})
const validated = schema.parse(input)
```

### 6. 状态管理 (Pinia)
```typescript
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export const useGlobalStore = defineStore('global', () => {
  // State
  const loading = ref(false)
  const env = ref<'dev' | 'test' | 'prd'>('prd')

  // Computed
  const config = computed(() => configMap[env.value])

  // Actions
  const setLoading = (value: boolean) => {
    loading.value = value
  }

  return { loading, env, config, setLoading }
})
```

### 7. 组件开发 (Shadcn-Vue)
- **安装**: `npx shadcn-vue@latest add [component]`
- **位置**: `@/components/ui/`
- **可访问性**: 必须包含 ARIA 属性 (tabindex, aria-label)
- **样式**: 使用 `cn()` 合并 Tailwind 类名

```vue
<template>
  <Button 
    :aria-label="accessibleLabel"
    @click="handleClick"
    @keydown.enter="handleClick"
  >
    {{ buttonText }}
  </Button>
</template>
```

---

## 📁 项目结构

```
app/
├── apis/              # API 客户端 (ordx.ts, satnet.ts)
├── components/        # Vue 组件
│   ├── ui/           # Shadcn UI 组件
│   ├── wallet/       # 钱包相关组件
│   └── asset/        # 资产管理组件
├── composables/       # 组合式函数 (useAssetActions.ts)
├── config/            # 环境配置
├── entrypoints/       # 应用入口 (popup/)
├── lib/               # 核心库 (walletStorage.ts)
├── store/             # Pinia Stores
├── types/             # TypeScript 类型定义
├── utils/             # 工具函数 (wasm.ts, btc.ts)
└── public/wasm/       # WASM 模块
```

---

## 🔒 Cursor Rules

### .cursor/rules/vue3-shadcn.mdc
- **适用范围**: `*.vue`, `*.ts`
- **核心**: Vue 3 Composition API + Shadcn-Vue + Radix-Vue
- **验证**: Zod Schema + Vee-Validate
- **图标**: @iconify/vue
- **数据获取**: @tanstack/vue-query

### .cursor/rules/chrome-extension-development.mdc
- **适用范围**: `*.ts`, `*.vue`
- **Manifest V3**: Service Worker 后台脚本
- **安全**: CSP、XSS 防护、数据加密
- **权限**: 最小权限原则

### .cursor/rules/design.mdc
- **适用范围**: 前端 UI 设计任务
- **样式**: Tailwind CSS + Flowbite
- **字体**: Google Fonts (JetBrains Mono, Inter, etc.)
- **设计迭代**: `.superdesign/design_iterations/`

---

## ⚠️ 重要注意事项

### WASM 加载
```typescript
// 应用挂载前必须加载 WASM
import { loadWasm } from '@/utils/wasm'

loadWasm().then(() => {
  const app = createApp(App)
  app.mount('#app')
})
```

### 环境配置
```typescript
import { useGlobalStore } from '@/store/global'

const globalStore = useGlobalStore()
// 环境：'dev' | 'test' | 'prd'
// API 端点自动根据环境切换
```

### 存储模式
```typescript
import { walletStorage } from '@/lib/walletStorage'

// 读取
const address = walletStorage.getValue('address')

// 写入
await walletStorage.setValue('address', newAddress)

// 批量更新
await walletStorage.batchUpdate({ address, network })
```

---

## 🧪 测试策略 (建议)

当前无测试配置，推荐添加：

```bash
# 安装 Vitest
bun add -D vitest @vitejs/plugin-vue @vue/test-utils

# vitest.config.ts
import { defineConfig } from 'vitest/config'
export default defineConfig({
  test: {
    environment: 'jsdom',
    globals: true
  }
})
```

---

## 📊 构建配置

### TypeScript (tsconfig.json)
- `strict: true`
- `moduleResolution: "bundler"`
- `jsx: "preserve"`
- 路径别名：`@/*` → `./*`

### Vite (vite.config.ts)
- Vue 3 插件
- 路径别名：`@`, `~` 均指向根目录

### PostCSS (postcss.config.mjs)
- Tailwind CSS 4.x
- Autoprefixer

---

## 🚀 快速开始

```bash
# 1. 安装依赖
bun install

# 2. 启动开发服务器
bun run dev

# 3. 类型检查
bun run compile

# 4. 生产构建
bun run build

# 5. 同步到移动端
bun run sync
```

---

## 🔗 相关文档

- [CLAUDE.md](./CLAUDE.md) - 详细项目文档
- [README.md](./README.md) - 项目说明
- [store/CLAUDE.md](./store/CLAUDE.md) - Store 模块文档
- [components/ui/CLAUDE.md](./components/ui/CLAUDE.md) - UI 组件文档
