import { ref } from 'vue'
import { useToast } from '@/components/ui/toast-new/use-toast'

const APP_CACHE_PREFIX = 'sat20-wallet-pwa-'

const showRestartRequiredScreen = () => {
  const isChinese = navigator.language.toLowerCase().startsWith('zh')
  const title = isChinese ? '更新已准备完成' : 'Update ready'
  const description = isChinese
    ? '请关闭所有 SAT20 Wallet 窗口，然后重新打开应用。钱包数据已保留。'
    : 'Close all SAT20 Wallet windows, then open the app again. Your wallet data is preserved.'

  document.title = title
  document.body.innerHTML = `
    <main style="min-height:100vh;display:flex;align-items:center;justify-content:center;background:#09090b;color:#f4f4f5;padding:24px;font-family:system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;">
      <section style="max-width:420px;width:100%;border:1px solid rgba(244,244,245,.14);border-radius:12px;padding:24px;background:#18181b;text-align:center;">
        <h1 style="font-size:20px;margin:0 0 12px;">${title}</h1>
        <p style="font-size:14px;line-height:1.6;color:#d4d4d8;margin:0;">${description}</p>
      </section>
    </main>
  `

  // Installed PWAs may allow this. Browser tabs commonly reject window.close(),
  // in which case the blocking screen above asks the user to close the app.
  window.close()
}

export function usePwaUpdate() {
  const { toast } = useToast()
  const isUpdating = ref(false)

  const clearAppShellCache = async () => {
    if (!('caches' in window)) {
      return
    }

    const keys = await caches.keys()
    await Promise.all(
      keys
        .filter((key) => key.startsWith(APP_CACHE_PREFIX))
        .map((key) => caches.delete(key))
    )
  }

  const waitForWaitingWorker = async (registration: ServiceWorkerRegistration) => {
    if (registration.waiting) {
      return registration.waiting
    }
    const installing = registration.installing
    if (!installing) {
      throw new Error('No new PWA update worker was found')
    }
    await new Promise<void>((resolve, reject) => {
      const finish = (error?: Error) => {
        installing.removeEventListener('statechange', onStateChange)
        if (error) reject(error)
        else resolve()
      }
      const onStateChange = () => {
        if (installing.state === 'installed' || installing.state === 'activated') finish()
        else if (installing.state === 'redundant') finish(new Error('PWA update worker became redundant'))
      }
      installing.addEventListener('statechange', onStateChange)
      onStateChange()
    })
    return installing
  }

  const prepareUpdateForRestart = async (registration: ServiceWorkerRegistration) => {
    const worker = await waitForWaitingWorker(registration)
    await new Promise<void>((resolve, reject) => {
      const finish = (error?: Error) => {
        worker.removeEventListener('statechange', onStateChange)
        if (error) reject(error)
        else resolve()
      }
      const onStateChange = () => {
        if (worker.state === 'activated') finish()
        else if (worker.state === 'redundant') finish(new Error('PWA update worker became redundant'))
      }
      // Observe the selected worker before requesting activation, including
      // the case where it has already activated while installation settled.
      worker.addEventListener('statechange', onStateChange)
      onStateChange()
      if (worker.state === 'installed') {
        try {
          worker.postMessage({ type: 'SAT20_ACTIVATE_UPDATE' })
        } catch (error) {
          finish(error instanceof Error ? error : new Error(String(error)))
        }
      }
    })
  }

  const reloadApp = async () => {
    if (isUpdating.value) {
      return
    }

    isUpdating.value = true

    try {
      if (!('serviceWorker' in navigator)) {
        throw new Error('Service Worker is unavailable')
      }

      const registration = await navigator.serviceWorker.getRegistration()
      if (!registration) {
        throw new Error('PWA Service Worker is not registered')
      }

      await registration.update()
      await prepareUpdateForRestart(registration)
      showRestartRequiredScreen()
    } catch (error) {
      isUpdating.value = false
      throw error
    }
  }

  const notifyUpdateAvailable = (description?: string) => {
    toast({
      variant: 'info',
      title: '发现新版本',
      description: description || '点击“立即更新”刷新到最新版本，钱包数据会保留。',
      duration: 15000,
      action: {
        label: '立即更新',
        onClick: () => {
          void reloadApp().catch((error) => {
            console.warn('PWA update failed:', error)
          })
        },
      },
    })
  }

  return {
    isUpdating,
    clearAppShellCache,
    reloadApp,
    notifyUpdateAvailable,
  }
}
