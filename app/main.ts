import { createApp } from 'vue'
import App from './entrypoints/popup/App.vue'
import router from './router'
import './assets/index.css'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import en from './locales/en.json'
import zh from './locales/zh.json'
import { loadWasm } from '@/utils/wasm'
import { VueQueryPlugin } from '@tanstack/vue-query'

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
  // 在应用启动时初始化存储状态
  await walletStorage.initializeState()
  
  const app = createApp(App)
  const pinia = createPinia()

  app.use(pinia)
  app.use(router)
  app.use(i18n)
  app.use(VueQueryPlugin)

  app.mount('#app')

  ;(window as any).sat20 = sat20
  // ;(window as any).debugStorage = debugStorage
});

export type Language = 'en' | 'zh';

export const setLanguage = (locale: Language) => {
  i18n.global.locale.value = locale
}
