import { computed, ref, watch } from 'vue'
import { defineStore } from 'pinia'
import accountSDK from '@/utils/accountManagement'
import type { AccountView, ModuleView } from '@/utils/walletViews'
import { useWalletStore } from './wallet'

export const useAccountManagementStore = defineStore('accountManagement', () => {
  const walletStore = useWalletStore()
  const status = ref<AccountView | null>(null)
  const loading = ref(false)
  const statusReadFailed = ref(false)
  const retrying = ref(false)
  const retryQueued = ref(false)
  const retryFailed = ref(false)
  const sessionReady = computed(() => walletStore.hasWallet && !walletStore.locked &&
    !walletStore.isSwitchingWallet && !walletStore.isSwitchingAccount && !walletStore.isSwitchingNetwork)
  const identityKey = computed(() => JSON.stringify([
    walletStore.hasWallet,
    walletStore.rootAccountId,
    walletStore.walletId,
    walletStore.accountIndex,
    walletStore.network,
    walletStore.wallet?.fingerprint || '',
    walletStore.locked,
    walletStore.isSwitchingWallet,
    walletStore.isSwitchingAccount,
    walletStore.isSwitchingNetwork,
  ]))

  let started = false
  let requestGeneration = 0
  let retryGeneration = 0
  let statusEventGeneration = 0
  let stopIdentityWatch: (() => void) | undefined

  const clear = () => {
    requestGeneration++
    retryGeneration++
    status.value = null
    loading.value = false
    statusReadFailed.value = false
    retrying.value = false
    retryQueued.value = false
    retryFailed.value = false
  }

  const isCurrent = (generation: number, key: string) =>
    generation === requestGeneration && key === identityKey.value && sessionReady.value

  const refresh = async () => {
    if (!sessionReady.value) return
    const generation = ++requestGeneration
    const key = identityKey.value
    loading.value = true
    statusReadFailed.value = false
    try {
      const next = await accountSDK.getAccountView()
      if (!isCurrent(generation, key)) return
      status.value = next
      statusReadFailed.value = false
    } catch {
      if (!isCurrent(generation, key)) return
      status.value = null
      statusReadFailed.value = true
    } finally {
      if (generation === requestGeneration) loading.value = false
    }
  }

  const onStatusUpdated = () => {
    statusEventGeneration++
    retryQueued.value = false
    retryFailed.value = false
    if (sessionReady.value) void refresh()
  }

  const onVisibilityChange = () => {
    if (document.visibilityState === 'visible') void refresh()
  }

  const start = () => {
    if (started || typeof window === 'undefined') return
    started = true
    stopIdentityWatch = watch(identityKey, () => {
      clear()
      if (sessionReady.value) void refresh()
    }, { immediate: true })
    window.addEventListener('sat20:account-status-updated', onStatusUpdated)
    document.addEventListener('visibilitychange', onVisibilityChange)
  }

  const stop = () => {
    if (!started) return
    started = false
    stopIdentityWatch?.()
    stopIdentityWatch = undefined
    window.removeEventListener('sat20:account-status-updated', onStatusUpdated)
    document.removeEventListener('visibilitychange', onVisibilityChange)
    clear()
  }

  const retryNow = async () => {
    if (!started || !sessionReady.value || retrying.value || hasDatabaseIssue.value) return
    if (statusReadFailed.value) return refresh()
    if (!canRetryBackup.value) return
    const generation = ++retryGeneration
    const key = identityKey.value
    const eventGeneration = statusEventGeneration
    retrying.value = true
    retryFailed.value = false
    try {
      const result = await accountSDK.refreshAccount()
      if (generation !== retryGeneration || key !== identityKey.value || !sessionReady.value) return
      retryQueued.value = Boolean(result.state === 'syncing' && eventGeneration === statusEventGeneration)
      await refresh()
      if (generation === retryGeneration && key === identityKey.value && sessionReady.value) {
        retryQueued.value = Boolean(result.state === 'syncing' && eventGeneration === statusEventGeneration)
      }
    } catch {
      if (generation === retryGeneration && key === identityKey.value && sessionReady.value) retryFailed.value = true
    } finally {
      if (generation === retryGeneration) retrying.value = false
    }
  }

  const rgb11ModuleStatus = computed(() => status.value?.modules?.find(item => item.name === 'rgb11'))
  const rgb11WriteAllowed = computed(() => sessionReady.value && rgb11ModuleStatus.value?.can_write === true)
  const moduleIssues = computed(() => status.value?.modules?.filter(item => item.error_code) || [])
  const hasDatabaseIssue = computed(() => status.value?.error_code === 'ACCOUNT_DB_WRITE_FAILED')
  const canRetryBackup = computed(() => Boolean(status.value &&
    (status.value.backup_risk !== 'notConfigured' || status.value.state === 'activating' ||
      status.value.discovery_status === 'active-waiting-backup')))
  const quotaModuleIssues = computed(() => moduleIssues.value.filter(item => item.error_code === 'DKVS_QUOTA_EXCEEDED'))
  const hasQuotaIssue = computed(() => status.value?.error_code === 'DKVS_QUOTA_EXCEEDED' || quotaModuleIssues.value.length > 0)
  const quotaNeedsActivation = computed(() => hasQuotaIssue.value && status.value?.state === 'local')
  const quotaGuidanceKey = computed(() => status.value?.state === 'local' ? 'accountManagement.quota.activation'
    : status.value?.storage?.option.id === 'temporary' ? 'accountManagement.quota.temporary'
    : status.value?.storage?.option.id === 'paid' ? 'accountManagement.quota.paid' : 'accountManagement.quota.review')
  const quotaNoticeKey = computed(() => hasQuotaIssue.value ? quotaGuidanceKey.value : '')
  const quotaActionKey = computed(() => quotaNeedsActivation.value ? 'accountManagement.quota.activateAction' : 'accountManagement.quota.reviewAction')
  const quotaMessageKeyForCode = (code: unknown) => code === 'DKVS_QUOTA_EXCEEDED' ? quotaGuidanceKey.value : ''

  const shouldShowBanner = computed(() => sessionReady.value && (
    loading.value && status.value === null || statusReadFailed.value || retryFailed.value ||
    status.value?.state === 'local' || status.value?.state === 'locked' || status.value?.state === 'syncing' ||
    status.value?.state === 'error' ||
    status.value?.backup_state === 'pending' || status.value?.backup_state === 'uploading' ||
    status.value?.backup_state === 'cleanup' || moduleIssues.value.length > 0 ||
    hasQuotaIssue.value
  ))

  return {
    status,
    loading,
    statusReadFailed,
    retrying,
    retryQueued,
    retryFailed,
    sessionReady,
    shouldShowBanner,
    moduleIssues,
    hasDatabaseIssue,
    canRetryBackup,
    quotaModuleIssues,
    hasQuotaIssue,
    quotaNeedsActivation,
    quotaNoticeKey,
    quotaActionKey,
    quotaMessageKeyForCode,
    rgb11WriteAllowed,
    rgb11ModuleStatus,
    start,
    stop,
    refresh,
    retryNow,
  }
})
