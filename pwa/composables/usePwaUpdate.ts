import { ref } from 'vue'
import { useToast } from '@/components/ui/toast-new/use-toast'

const APP_CACHE_PREFIX = 'sat20-wallet-pwa-'
const UPDATE_TIMEOUT_MS = 15000

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

  const waitForControllerChange = () => {
    return new Promise<void>((resolve) => {
      if (!('serviceWorker' in navigator)) {
        resolve()
        return
      }

      let timeoutId: ReturnType<typeof setTimeout> | undefined
      const cleanup = () => {
        navigator.serviceWorker.removeEventListener('controllerchange', onControllerChange)
        if (timeoutId) {
          clearTimeout(timeoutId)
        }
      }
      const onControllerChange = () => {
        cleanup()
        resolve()
      }

      navigator.serviceWorker.addEventListener('controllerchange', onControllerChange)
      timeoutId = setTimeout(() => {
        cleanup()
        resolve()
      }, UPDATE_TIMEOUT_MS)
    })
  }

  const waitForWaitingWorker = async (registration: ServiceWorkerRegistration) => {
    if (registration.waiting) {
      return registration.waiting
    }
    const installing = registration.installing
    if (!installing) {
      return undefined
    }
    await new Promise<void>((resolve) => {
      let timeoutId: ReturnType<typeof setTimeout> | undefined
      const finish = () => {
        installing.removeEventListener('statechange', onStateChange)
        if (timeoutId) clearTimeout(timeoutId)
        resolve()
      }
      const onStateChange = () => {
        if (installing.state === 'installed' || installing.state === 'redundant' || installing.state === 'activated') {
          finish()
        }
      }
      installing.addEventListener('statechange', onStateChange)
      timeoutId = setTimeout(finish, UPDATE_TIMEOUT_MS)
    })
    return registration.waiting
  }

  const activateWaitingWorker = async (registration: ServiceWorkerRegistration) => {
    const waiting = await waitForWaitingWorker(registration)
    if (!waiting) {
      return
    }
    const controllerChanged = waitForControllerChange()
    waiting.postMessage({ type: 'SKIP_WAITING' })
    await controllerChanged
  }

  const reloadApp = async () => {
    if (isUpdating.value) {
      return
    }

    isUpdating.value = true

    try {
      if ('serviceWorker' in navigator) {
        const registration = await navigator.serviceWorker.getRegistration()
        const updatedRegistration = await registration?.update()
        if (updatedRegistration) {
          await activateWaitingWorker(updatedRegistration)
        }

        const freshRegistration = await navigator.serviceWorker.getRegistration()
        if (freshRegistration?.waiting || freshRegistration?.installing) {
          await activateWaitingWorker(freshRegistration)
        }
      }
    } catch (error) {
      console.warn('PWA update failed, falling back to reload:', error)
    } finally {
      // Normal upgrades keep the current app cache intact until the waiting
      // service worker takes control. Full cache clearing is reserved for the
      // explicit startup-failure recovery path.
      window.location.reload()
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
          void reloadApp()
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
