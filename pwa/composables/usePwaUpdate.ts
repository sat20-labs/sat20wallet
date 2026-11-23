import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { versionPolicy, policyRevision } from '@/utils/pwaVersionPolicy'
import { useToast } from '@/components/ui/toast-new/use-toast'

const APP_CACHE_PREFIX = 'sat20-wallet-pwa-'
const UPDATE_TIMEOUT_MS = 60000

const untilAbort = async <T>(work: Promise<T>, signal: AbortSignal): Promise<T> => {
  let abort: () => void = () => {}
  const deadline = new Promise<never>((_, reject) => {
    abort = () => reject(signal.reason)
    if (signal.aborted) abort()
    else signal.addEventListener('abort', abort, { once: true })
  })
  try {
    return await Promise.race([work, deadline])
  } finally {
    signal.removeEventListener('abort', abort)
  }
}

const readWorkerRelease = async (worker: ServiceWorker, signal: AbortSignal) => {
  const channel = new MessageChannel()
  try {
    const response = new Promise<string | undefined>(resolve => {
      channel.port1.onmessage = ({ data }) => resolve(data?.releaseId)
      worker.postMessage({ type: 'GET_RELEASE_STATE' }, [channel.port2])
    })
    return await untilAbort(response, signal)
  } finally {
    channel.port1.close()
  }
}

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

  const awaitActivation = async (worker: ServiceWorker, signal: AbortSignal) => {
    let onStateChange: () => void = () => {}
    let requested = false
    const activated = new Promise<void>((resolve, reject) => {
      onStateChange = () => {
        if (worker.state === 'activated') resolve()
        else if (worker.state === 'redundant') reject(new Error(t('pwaUi.update.redundant')))
        else if (worker.state === 'installed' && !requested && !signal.aborted) {
          requested = true
          try { worker.postMessage({ type: 'SAT20_ACTIVATE_UPDATE' }) }
          catch (error) { reject(error) }
        }
      }
      worker.addEventListener('statechange', onStateChange)
      onStateChange()
    })
    try {
      await untilAbort(activated, signal)
    } finally {
      worker.removeEventListener('statechange', onStateChange)
    }
  }

  const prepareUpdateForRestart = async (registration: ServiceWorkerRegistration, signal: AbortSignal) => {
    const worker = registration.installing || registration.waiting
    if (worker) {
      await awaitActivation(worker, signal)
      return
    }
    const active = registration.active
    const release = versionPolicy.remote
    // Another client can activate the new worker while this page still runs the old release.
    if (!active || !release ||
        await readWorkerRelease(active, signal) !== `${release.version}-${release.buildId || ''}`) {
      throw new Error(t('pwaUi.update.noWorker'))
    }
  }

  const reloadApp = async () => {
    if (isUpdating.value) {
      return
    }

    const signal = AbortSignal.timeout(UPDATE_TIMEOUT_MS)
    try {
      await versionPolicy.waitForUpdate(signal)
      if (!('serviceWorker' in navigator)) {
        throw new Error(t('pwaUi.update.unavailable'))
      }

      const registration = await untilAbort(navigator.serviceWorker.getRegistration(), signal)
      if (!registration) {
        throw new Error(t('pwaUi.update.unregistered'))
      }

      await untilAbort(registration.update(), signal)
      await prepareUpdateForRestart(registration, signal)
      showRestartRequiredScreen(t('pwaUi.update.ready'), t('pwaUi.update.restartHint'))
    } catch (error) {
      versionPolicy.updateFailed()
      if (signal.aborted) throw new Error(t('pwaUi.update.timedOut'))
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
            toast({ variant: 'destructive', title: t('setting.updateFailed'), description: error.message })
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
