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
import { createDappGrant, getCurrentDappScope } from '@/lib/authorized-origins'
import { DAPP_CAPABILITIES } from '@/lib/dapp-grant-model'
import { useApproveStore } from '@/store/approve'
import { installDappIdentityEventCoordinator } from '@/lib/dapp-identity-events'
import { installWalletIdentityCrossTabBoundary } from '@/lib/identity-boundary'

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
import rgb11Address from './utils/rgb11Address'
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

const reportPageReadyToServiceWorker = async () => {
  if (import.meta.env.DEV || !('serviceWorker' in navigator)) {
    return
  }
  try {
    const registration = await navigator.serviceWorker.ready
    registration.active?.postMessage({
      type: 'SAT20_PAGE_READY',
      releaseId: `${__SAT20_APP_VERSION__}-${__SAT20_BUILD_ID__}`,
    })
  } catch (error) {
    console.warn('Failed to report PWA release readiness:', error)
  }
}

let startupSlowTimer: ReturnType<typeof setTimeout> | undefined
const finishStartupShell = () => {
  if (startupSlowTimer) clearTimeout(startupSlowTimer)
  startupSlowTimer = undefined
}

// Render before awaiting WASM/network initialization. Retry uses a fresh page,
// never a second WASM instance or a cache/database reset in the current page.
const renderStartupStatus = (state: 'loading' | 'slow' | 'error') => {
  const root = document.getElementById('app')
  if (!root) return
  const shell = document.createElement('main')
  shell.style.cssText = 'min-height:100vh;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:16px;background:#09090b;color:#f4f4f5;padding:24px;font-family:system-ui,sans-serif;text-align:center;'
  const title = document.createElement('h1')
  title.textContent = i18n.global.t('startup.title')
  const status = document.createElement('p')
  status.setAttribute('role', state === 'error' ? 'alert' : 'status')
  status.textContent = i18n.global.t(`startup.${state}`)
  const retry = document.createElement('button')
  retry.type = 'button'
  retry.style.cssText = 'padding:10px 20px;border:1px solid currentColor;border-radius:6px;cursor:pointer;'
  retry.textContent = i18n.global.t('startup.retry')
  retry.addEventListener('click', () => window.location.reload())
  shell.append(title, status, retry)
  root.replaceChildren(shell)
}

const renderStartupError = (error: unknown) => {
  finishStartupShell()
  console.error('SAT20 Wallet startup failed:', error)
  renderStartupStatus('error')
}

renderStartupStatus('loading')
// This changes feedback only; it does not mark a pending runtime ready/failed.
startupSlowTimer = setTimeout(() => renderStartupStatus('slow'), 10000)

window.addEventListener('sat20:wasm-runtime-error', (event) => {
  renderStartupError(event.detail)
})

clearDevelopmentServiceWorkerCache().then(() => {
  // Registration must not wait for wallet or network initialization. This lets
  // the browser cache the app shell and WASM as early as possible.
  registerServiceWorker()
  return loadWasm()
}).then(async () => {
  // 在应用启动时初始化存储状态
  await walletStorage.initializeState()
  installDappIdentityEventCoordinator()
  installWalletIdentityCrossTabBoundary()

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

  finishStartupShell()
  app.mount('#app')

  // A newly activated worker keeps the previous page and cache untouched until
  // this release has initialized its WASM runtime, storage, router, and Vue UI.
  void reportPageReadyToServiceWorker()

  // 暴露全局对象
  ;(window as any).sat20 = sat20
  if (import.meta.env.DEV) {
    ;(window as any).__SAT20_PWA_VERIFY__ = {
      createDappGrant,
      getCurrentDappScope,
      DAPP_CAPABILITIES,
      Chain,
      Network,
      rgb11Address,
      sat20,
      useGlobalStore,
      useApproveStore,
      usePwaDappBridge,
      useWalletStore,
      walletStorage,
    }
  }
}).catch(renderStartupError);

export const setLanguage = (locale: Language) => {
  i18n.global.locale.value = locale
  walletStorage.setValue('language', locale)
}
