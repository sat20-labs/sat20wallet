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

const clearDevelopmentServiceWorkerCache = async () => {
  if (!import.meta.env.DEV || !('serviceWorker' in navigator)) {
    return
  }
  try {
    const registrations = await navigator.serviceWorker.getRegistrations()
    await Promise.all(
      registrations
        .filter((registration) => registration.scope.startsWith(window.location.origin))
        .map((registration) => registration.unregister())
    )

    if ('caches' in window) {
      const keys = await caches.keys()
      await Promise.all(
        keys
          .filter((key) => key.startsWith('sat20-wallet-pwa-'))
          .map((key) => caches.delete(key))
      )
    }
  } catch (error) {
    console.warn('Failed to clear development service worker cache:', error)
  }
}

const registerServiceWorker = () => {
  if (!('serviceWorker' in navigator)) {
    return
  }

  if (import.meta.env.DEV) {
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

const renderStartupError = (error: unknown) => {
  console.error('SAT20 Wallet startup failed:', error)
  const appRoot = document.getElementById('app')
  if (!appRoot) return

  appRoot.innerHTML = `
    <div style="min-height:100vh;display:flex;align-items:center;justify-content:center;background:#09090b;color:#f4f4f5;padding:24px;font-family:system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;">
      <div style="max-width:420px;width:100%;border:1px solid rgba(244,244,245,.14);border-radius:8px;padding:20px;background:#18181b;">
        <h1 style="font-size:18px;margin:0 0 10px;">SAT20 Wallet failed to start</h1>
        <p style="font-size:14px;line-height:1.55;color:#d4d4d8;margin:0 0 16px;">The app cache may be inconsistent after an update. Wallet data is kept locally; clearing the app shell cache will not delete the wallet.</p>
        <button id="sat20-recover-cache" style="width:100%;height:40px;border:0;border-radius:6px;background:#f4f4f5;color:#09090b;font-weight:600;">Clear cache and reload</button>
        <p style="font-size:12px;line-height:1.45;color:#a1a1aa;margin:14px 0 0;">If this screen remains, clear sat20.org site data from the browser settings and open the wallet again.</p>
      </div>
    </div>
  `

  document.getElementById('sat20-recover-cache')?.addEventListener('click', async () => {
    try {
      if ('serviceWorker' in navigator) {
        const registrations = await navigator.serviceWorker.getRegistrations()
        await Promise.all(
          registrations
            .filter((registration) => registration.scope.includes('/pwa/'))
            .map((registration) => registration.unregister())
        )
      }

      if ('caches' in window) {
        const keys = await caches.keys()
        await Promise.all(
          keys
            .filter((key) => key.startsWith('sat20-wallet-pwa-'))
            .map((key) => caches.delete(key))
        )
      }
    } finally {
      window.location.replace(`${import.meta.env.BASE_URL}?recover=${Date.now()}`)
    }
  })
}

clearDevelopmentServiceWorkerCache().then(loadWasm).then(async () => {
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
}).catch(renderStartupError);

export const setLanguage = (locale: Language) => {
  i18n.global.locale.value = locale
  walletStorage.setValue('language', locale)
}
