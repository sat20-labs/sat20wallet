import { computed, ref, watch } from 'vue'
import { defineStore } from 'pinia'
import accountSDK, { type AccountManagementStatus, type AccountModuleStatus } from '@/utils/accountManagement'
import { useWalletStore } from './wallet'

export const useAccountManagementStore = defineStore('accountManagement', () => {
  const walletStore = useWalletStore()
  const status = ref<AccountManagementStatus | null>(null)
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
      const next = await accountSDK.status()
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
    const generation = ++retryGeneration
    const key = identityKey.value
    const eventGeneration = statusEventGeneration
    retrying.value = true
    retryFailed.value = false
    try {
      const result = await accountSDK.retryDKVSSync()
      if (generation !== retryGeneration || key !== identityKey.value || !sessionReady.value) return
      retryQueued.value = Boolean(result.queued && eventGeneration === statusEventGeneration)
      await refresh()
      if (generation === retryGeneration && key === identityKey.value && sessionReady.value) {
        retryQueued.value = Boolean(result.queued && eventGeneration === statusEventGeneration)
      }
    } catch {
      if (generation === retryGeneration && key === identityKey.value && sessionReady.value) retryFailed.value = true
    } finally {
      if (generation === retryGeneration) retrying.value = false
    }
  }

  const currentRGB11Scope = computed(() => {
    const fingerprint = walletStore.wallet?.fingerprint?.trim().toLowerCase()
    const network = String(walletStore.network || '').trim().toLowerCase()
    const accountIndex = Number(walletStore.accountIndex)
    if (!fingerprint || !network || !Number.isInteger(accountIndex) || accountIndex < 0) return ''
    return `${network}/${fingerprint}/${accountIndex}`
  })

  const moduleStatus = (provider: string, scope: string): AccountModuleStatus | undefined =>
    status.value?.modules?.find(item => item.provider === provider && item.scope === scope)

  const canWrite = (provider: string, scope: string) => {
    const current = status.value
    if (!current || !sessionReady.value || !current.write_allowed ||
      (current.account_state !== 'local' && current.account_state !== 'ready')) return false
    if (!scope) return false
    const module = moduleStatus(provider, scope)
    return module ? module.write_allowed : true
  }

  const rgb11WriteAllowed = computed(() => canWrite('rgb11', currentRGB11Scope.value))
  const rgb11ModuleStatus = computed(() => moduleStatus('rgb11', currentRGB11Scope.value))
  const moduleHasIssue = (module: AccountModuleStatus) =>
    module.backup_state === 'error' || module.backup_state === 'conflict' || module.backup_state === 'restoring'
  const moduleIssues = computed(() => status.value?.modules?.filter(moduleHasIssue) || [])
  const hasDatabaseIssue = computed(() => status.value?.last_dkvs_sync_error_code === 'ACCOUNT_DB_WRITE_FAILED')
  const quotaModuleIssues = computed(() => status.value?.modules?.filter(module =>
    moduleHasIssue(module) && module.last_error_code === 'DKVS_QUOTA_EXCEEDED') || [])
  const hasQuotaIssue = computed(() => status.value?.last_dkvs_sync_error_code === 'DKVS_QUOTA_EXCEEDED' ||
    quotaModuleIssues.value.length > 0)
  const quotaGuidanceKey = computed(() => {
    const current = status.value
    if (!current) return 'accountManagement.quota.review'
    if (!current.recovery_configured || current.local_only) return 'accountManagement.quota.activation'
    if (current.storage_mode === 'temporary') return 'accountManagement.quota.temporary'
    if (current.storage_mode === 'paid') return 'accountManagement.quota.paid'
    return 'accountManagement.quota.review'
  })
  const quotaNeedsActivation = computed(() => hasQuotaIssue.value &&
    (!status.value?.recovery_configured || status.value.local_only === true))
  const quotaNoticeKey = computed(() => hasQuotaIssue.value ? quotaGuidanceKey.value : '')
  const quotaActionKey = computed(() => quotaNeedsActivation.value
    ? 'accountManagement.quota.activateAction'
    : 'accountManagement.quota.reviewAction')
  const quotaMessageKeyForCode = (code: unknown) =>
    code === 'DKVS_QUOTA_EXCEEDED' ? quotaGuidanceKey.value : ''
  const shouldShowBanner = computed(() => sessionReady.value && (
    loading.value && status.value === null || statusReadFailed.value || retryFailed.value ||
    status.value?.account_state === 'local' || status.value?.account_state === 'locked' || status.value?.account_state === 'syncing' ||
    status.value?.account_state === 'error' ||
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
    quotaModuleIssues,
    hasQuotaIssue,
    quotaNeedsActivation,
    quotaNoticeKey,
    quotaActionKey,
    quotaMessageKeyForCode,
    currentRGB11Scope,
    rgb11WriteAllowed,
    rgb11ModuleStatus,
    moduleStatus,
    canWrite,
    start,
    stop,
    refresh,
    retryNow,
  }
})
