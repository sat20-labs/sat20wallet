import '@/utils/debug'
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
import type { Language } from '@/types'
import { Chain, Network } from '@/types'
import { useGlobalStore, useWalletStore } from '@/store'
import { usePwaDappBridge } from '@/composables/usePwaDappBridge'
import { addAuthorizedOrigin } from '@/lib/authorized-origins'
import { useApproveStore } from '@/store/approve'
import { hashPassword } from '@/utils/crypto'

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

const registerServiceWorker = () => {
  if (!('serviceWorker' in navigator)) {
    return
  }

  const register = () => {
    navigator.serviceWorker.register(`${import.meta.env.BASE_URL}service-worker.js`, {
      scope: import.meta.env.BASE_URL,
    }).catch((error) => {
      console.warn('Service worker registration failed:', error)
    })
  }

  if (document.readyState === 'complete') {
    register()
  } else {
    window.addEventListener('load', register, { once: true })
  }
}

loadWasm().then(async () => {
  // 在应用启动时初始化存储状态
  await walletStorage.initializeState()

  const savedLanguage = walletStorage.getValue('language')
  if (savedLanguage) {
    i18n.global.locale.value = savedLanguage
  }

  const app = createApp(App)
  const pinia = createPinia()

  app.use(pinia)
  app.use(router)
  app.use(i18n)
  app.use(VueQueryPlugin)

  app.mount('#app')

  // 暴露全局对象
  ;(window as any).sat20 = sat20
  if (import.meta.env.DEV) {
    ;(window as any).__SAT20_PWA_VERIFY__ = {
      addAuthorizedOrigin,
      Chain,
      hashPassword,
      Network,
      sat20,
      useGlobalStore,
      useApproveStore,
      usePwaDappBridge,
      useWalletStore,
      walletStorage,
    }
  }

  registerServiceWorker()
});

export const setLanguage = (locale: Language) => {
  i18n.global.locale.value = locale
  walletStorage.setValue('language', locale)
}
