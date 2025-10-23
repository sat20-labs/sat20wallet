import { createApp } from 'vue'
import App from './entrypoints/popup/App.vue'
import router from './router'
import './assets/index.css'
import 'vue-sonner/style.css'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import en from './locales/en.json'
import zh from './locales/zh.json'
import { loadWasm } from '@/utils/wasm'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { ErudaControl } from '@/utils/eruda-control'

// 初始化 eruda 调试工具（默认启用）
const initEruda = () => {
  // 检查是否手动禁用了调试模式
  const debugDisabled = localStorage.getItem('eruda-debug') === 'false'

  if (!debugDisabled) {
    import('eruda').then(eruda => {
      eruda.default.init({
        defaults: {
          displaySize: 50,
          transparency: 0.9
        },
        tool: ['console', 'elements', 'network', 'resources', 'info', 'snippets'],
        autoScale: true
      })
      console.log('🔧 Eruda 调试工具已启动')

      // 设置默认启用状态
      localStorage.setItem('eruda-debug', 'true')
    })
  }
}

const defaultLocale = 'en'

const i18n = createI18n({
  legacy: false,
  locale: defaultLocale,
  fallbackLocale: defaultLocale,
  messages: {
    en,
    zh,
  },
})

import sat20 from './utils/sat20'
import { walletStorage } from '@/lib/walletStorage'
// import { debugStorage } from '@/lib/debug-storage'

loadWasm().then(async () => {
  // 初始化 eruda 调试工具
  initEruda()

  // 在应用启动时初始化存储状态
  await walletStorage.initializeState()

  const app = createApp(App)
  const pinia = createPinia()

  app.use(pinia)
  app.use(router)
  app.use(i18n)
  app.use(VueQueryPlugin)

  app.mount('#app')

  // 暴露全局对象和控制方法
  ;(window as any).sat20 = sat20
  ;(window as any).erudaControl = ErudaControl
  // ;(window as any).debugStorage = debugStorage
});

export type Language = 'en' | 'zh';

export const setLanguage = (locale: Language) => {
  i18n.global.locale.value = locale
}
