import { createApp } from 'vue'
import '@/assets/index.css'
import './style.css'
import App from '@/entrypoints/popup/App.vue'
import { createPinia } from 'pinia'
import router from '@/entrypoints/popup/router'
import { walletStorage } from '@/lib/walletStorage'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { Icon } from '@iconify/vue'
import { loadWasm } from '@/utils/wasm'
import { createI18n } from 'vue-i18n'
import en from '@/locales/en.json' 
import zh from '@/locales/zh.json'

// 从 localStorage 获取语言，默认为 'en-US'
const savedLanguage = localStorage.getItem('language') || 'en-US'

const i18n = createI18n({
  locale: savedLanguage, // 使用保存的语言
  fallbackLocale: 'en-US', // 回退语言
  messages: {
    'en-US': en,
    'zh-CN': zh,
  },
})

// 导出 setLanguage 方法
export function setLanguage(lang: string) {
  if (lang === 'en-US' || lang === 'zh-CN') {
    i18n.global.locale = lang // 更新语言
    localStorage.setItem('language', lang) // 保存到 localStorage
  } else {
    console.warn(`Unsupported language: ${lang}`)
  }
}

walletStorage.initializeState().then(() => {
  loadWasm().then(() => {
    createApp(App)
      .use(i18n)
      .component('Icon', Icon)
      .use(VueQueryPlugin)
      .use(createPinia())
      .use(router)
      .mount('#app')
  })
})
