import { createApp } from 'vue'
import '@/assets/index.css'
import './style.css'
import 'vue-sonner/style.css' // 添加 vue-sonner 样式
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

walletStorage.initializeState()
  .then(() => {
    return loadWasm()
  })
  .then(() => {
    const app = createApp(App)
      .use(i18n)
      .component('Icon', Icon)
      .use(VueQueryPlugin)
      .use(createPinia())
      .use(router)
    
    app.mount('#app')
  })
  .catch((error) => {
    console.error('❌ 初始化失败:', error)
    
    // 显示错误信息给用户
    const appElement = document.getElementById('app')
    if (appElement) {
      appElement.innerHTML = `
        <div style="
          padding: 20px;
          color: white;
          background: #1a1a1a;
          border-radius: 8px;
          margin: 20px;
          font-family: Arial, sans-serif;
        ">
          <h2 style="color: #ff6b6b;">插件初始化失败</h2>
          <p>错误信息: ${error.message}</p>
          <p>请检查控制台获取详细信息</p>
        </div>
      `
    }
  })
