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

### Android 构建和签名命令
```bash
# 1. 升级版本号
bun run bump-version

# 2. 构建 Web 资源
bun run build:skip-check

# 3. 同步到 Android
cd android && ./gradlew clean

# 4. 构建 Release APK（自动签名）
./gradlew assembleRelease

# 5. 验证 APK 签名
cd ..
./verify-apk.sh [version]  # 例：./verify-apk.sh 0.1.13

# 6. 手动验证签名
keytool -printcert -jarfile release/SAT20-Wallet-v[VERSION]-release-signed.apk
jarsigner -verify release/SAT20-Wallet-v[VERSION]-release-signed.apk
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

## 📦 版本管理

### version.json 文件位置
项目中有多个 `version.json` 文件，需要保持同步更新：
- `public/version.json` - 主要版本文件（源文件）
- `dist/version.json` - 构建输出文件（自动同步）
- `ios/App/App/public/version.json` - iOS 版本文件（通过 `bun run sync` 同步）
- `android/.../public/version.json` - Android 版本文件（通过 `bun run sync` 同步）

### version.json 格式
```json
{
  "version": "0.1.13",
  "releaseNotes": "移除了转账地址的 Taproot 验证限制，现在支持向所有有效的比特币地址转账",
  "forceUpdate": false,
  "minVersion": "0.1.0",
  "publishedAt": "2026-03-06T22:30:00.000Z"
}
```

### 版本号规范
- **格式**: SemVer (语义化版本) - `MAJOR.MINOR.PATCH`
  - `MAJOR`: 重大变更，不向后兼容
  - `MINOR`: 新功能，向后兼容
  - `PATCH`: Bug 修复，向后兼容
- **示例**: `0.1.12` → `0.1.13` → `0.2.0` → `1.0.0`

### 发布流程
1. **更新版本号**: `bun run bump-version`
2. **更新 version.json**: 修改 `public/version.json` 中的版本号和发布说明
3. **构建 Web 资源**: `bun run build:skip-check`
4. **构建 Android APK**: 执行 Android 签名流程
5. **同步到移动端**: `bun run sync`（更新 iOS/Android 的 version.json）
6. **验证和测试**: 使用 `./verify-apk.sh` 验证签名
7. **发布到应用商店**: 上传签名的 APK

---

## 🔐 Release 签名

### Keystore 信息
- **文件路径**: `/Users/icehugh/workspace/jieziyuan/client/sat20wallet/app/sat20wallet-release.jks`
- **别名**: `sat20wallet`
- **密码**: `sat20wallet2024`
- **有效期**: 27 年（到 2053-07-22）
- **算法**: SHA384withRSA
- **密钥长度**: 2048-bit RSA

### 证书指纹
```
SHA256: 5F:8B:92:27:0F:85:14:C4:94:9B:17:88:91:54:D4:F4:ED:C4:C1:01:F5:E7:62:7D:4C:AA:1B:D0:72:2C:C2:03
SHA1: 3C:E7:D2:76:9E:C9:DF:FF:4F:C3:2E:7A:EA:21:EE:A3:16:27:70:C3
```

### Android 签名配置
Gradle 配置文件：`android/app/build.gradle`
```groovy
signingConfigs {
    release {
        if (project.hasProperty('MYAPP_UPLOAD_STORE_FILE')) {
            storeFile file(MYAPP_UPLOAD_STORE_FILE)
            storePassword MYAPP_UPLOAD_STORE_PASSWORD
            keyAlias MYAPP_UPLOAD_KEY_ALIAS
            keyPassword MYAPP_UPLOAD_KEY_PASSWORD
        }
    }
}
```

环境变量配置：`android/gradle.properties`
```properties
MYAPP_UPLOAD_STORE_FILE=/Users/icehugh/workspace/jieziyuan/client/sat20wallet/app/sat20wallet-release.jks
MYAPP_UPLOAD_KEY_ALIAS=sat20wallet
MYAPP_UPLOAD_KEY_PASSWORD=sat20wallet2024
MYAPP_UPLOAD_STORE_PASSWORD=sat20wallet2024
```

### 签名验证方法

#### 方法 1: 使用验证脚本（推荐）
```bash
./verify-apk.sh 0.1.13
```

#### 方法 2: 手动验证证书
```bash
keytool -printcert -jarfile release/SAT20-Wallet-v0.1.13-release-signed.apk
```

#### 方法 3: 验证 APK 完整性
```bash
jarsigner -verify release/SAT20-Wallet-v0.1.13-release-signed.apk
```

### 签名最佳实践
1. ✅ **备份 Keystore**: 已备份到多个安全位置
   - `release/sat20wallet-release-backup.jks`
   - `release/sat20wallet-release-v0.1.12.jks`
   - `/Users/icehugh/.backup/sat20wallet/sat20wallet-release.jks`

2. ✅ **保持一致性**: 所有版本使用同一密钥签名，确保可以无缝升级

3. ✅ **验证签名**: 每次构建后都要验证签名是否正确

4. ⚠️ **安全警告**: 
   - 切勿将 Keystore 文件提交到 Git
   - 切勿公开分享密钥密码
   - 使用密码管理器保存密码

### Release 目录结构
```
release/
├── RELEASE-v0.1.13.md              # 发布说明文档
├── SAT20-Wallet-v0.1.13-release-signed.apk  # 已签名的 APK
├── KEYSTORE_INFO.md                # Keystore 详细信息
├── SIGNING_SUMMARY.md              # 签名摘要
├── TESTING-GUIDE.md                # 测试指南
└── sat20wallet-release-backup.jks  # 备份密钥库
```

### 常见问题

**Q: APK 安装失败，显示"应用未安装"**
A: 可能是签名不一致。确保使用相同的 Keystore 签名。

**Q: 如何检查 APK 是否已签名？**
A: 运行 `keytool -printcert -jarfile your-app.apk`，如果有证书信息说明已签名。

**Q: Keystore 丢失了怎么办？**
A: 从备份位置恢复。如果所有备份都丢失，需要创建新的 Keystore 并更改应用包名重新发布。

---

## 📱 移动端构建

### Android 构建流程
```bash
# 1. 清理并构建
cd android && ./gradlew clean assembleRelease

# 2. 查找 APK
ls -lh app/build/outputs/apk/release/

# 3. 复制到 release 目录
cp app/build/outputs/apk/release/app-release.apk \
   ../../release/SAT20-Wallet-v[VERSION]-release-signed.apk

# 4. 验证签名
../../verify-apk.sh [VERSION]
```

### iOS 构建注意事项
- iOS 构建需要 Xcode 和 Apple Developer 证书
- 使用 Xcode Archive 进行构建和签名
- 通过 App Store Connect 发布

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

### 开发环境
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

### 发布版本
```bash
# 1. 升级版本号 (PATCH)
bun run bump-version

# 2. 更新 public/version.json 中的版本号和发布说明

# 3. 构建 Web 资源
bun run build:skip-check

# 4. 构建签名 APK
cd android && ./gradlew clean assembleRelease

# 5. 验证签名
cd ..
./verify-apk.sh 0.1.13

# 6. 同步到移动端
bun run sync
```

---

## 🔗 相关文档

- [CLAUDE.md](./CLAUDE.md) - 详细项目文档
- [README.md](./README.md) - 项目说明
- [store/CLAUDE.md](./store/CLAUDE.md) - Store 模块文档
- [components/ui/CLAUDE.md](./components/ui/CLAUDE.md) - UI 组件文档
