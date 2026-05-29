import { ref } from 'vue'
import { useToast } from '@/components/ui/toast-new/use-toast'

const APP_CACHE_PREFIX = 'sat20-wallet-pwa-'

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

  const reloadApp = async () => {
    if (isUpdating.value) {
      return
    }

    isUpdating.value = true

    try {
      await clearAppShellCache()

      if ('serviceWorker' in navigator) {
        const registration = await navigator.serviceWorker.getRegistration()
        await registration?.update()

        if (registration?.waiting) {
          registration.waiting.postMessage({ type: 'SKIP_WAITING' })
        }
      }
    } catch (error) {
      console.warn('PWA update failed, falling back to reload:', error)
    } finally {
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
    reloadApp,
    notifyUpdateAvailable,
  }
}
