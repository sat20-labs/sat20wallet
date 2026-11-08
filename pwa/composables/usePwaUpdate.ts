import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { versionPolicy, policyRevision } from '@/utils/pwaVersionPolicy'
import { useToast } from '@/components/ui/toast-new/use-toast'

const APP_CACHE_PREFIX = 'sat20-wallet-pwa-'

const showRestartRequiredScreen = (title: string, description: string) => {
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
  const { t } = useI18n()
  const { toast } = useToast()
  const isUpdating = computed(() => { void policyRevision.value; return versionPolicy.updating })

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
    const installing = registration.installing
    if (!installing) {
      if (registration.waiting) {
        return registration.waiting
      }
      throw new Error(t('pwaUi.update.noWorker'))
    }
    await new Promise<void>((resolve, reject) => {
      const finish = (error?: Error) => {
        installing.removeEventListener('statechange', onStateChange)
        if (error) reject(error)
        else resolve()
      }
      const onStateChange = () => {
        if (installing.state === 'installed' || installing.state === 'activated') finish()
        else if (installing.state === 'redundant') finish(new Error(t('pwaUi.update.redundant')))
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
        else if (worker.state === 'redundant') finish(new Error(t('pwaUi.update.redundant')))
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

    const drained = versionPolicy.waitForUpdate()
    try {
      await drained
      if (!('serviceWorker' in navigator)) {
        throw new Error(t('pwaUi.update.unavailable'))
      }

      const registration = await navigator.serviceWorker.getRegistration()
      if (!registration) {
        throw new Error(t('pwaUi.update.unregistered'))
      }

      await registration.update()
      await prepareUpdateForRestart(registration)
      showRestartRequiredScreen(t('pwaUi.update.ready'), t('pwaUi.update.restartHint'))
    } catch (error) {
      versionPolicy.updateFailed()
      throw error
    }
  }

  const notifyUpdateAvailable = (description?: string) => {
    toast({
      variant: 'info',
      title: t('pwaUi.update.available'),
      description: description || t('pwaUi.update.installHint'),
      duration: 15000,
      action: {
        label: t('pwaUi.update.now'),
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
