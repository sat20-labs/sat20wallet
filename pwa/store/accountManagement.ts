import { computed, ref, watch } from 'vue'
import { defineStore } from 'pinia'
import accountSDK from '@/utils/accountManagement'
import type { AccountView, ModuleView, StorageOption } from '@/utils/walletViews'
import { useWalletStore } from './wallet'

export const useAccountManagementStore = defineStore('accountManagement', () => {
  const walletStore = useWalletStore()
  const status = ref<AccountView | null>(null)
  const loading = ref(false)
  const statusReadFailed = ref(false)
  const retrying = ref(false)
  const retryQueued = ref(false)
  const retryFailed = ref(false)
  const storageOffers = ref<StorageOption[] | null>(null)
  const storageReadFailed = ref(false)
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
  let offerGeneration = 0
  let stopIdentityWatch: (() => void) | undefined

  const clear = () => {
    requestGeneration++
    retryGeneration++
    offerGeneration++
    status.value = null
    loading.value = false
    statusReadFailed.value = false
    retrying.value = false
    retryQueued.value = false
    retryFailed.value = false
    storageOffers.value = null
    storageReadFailed.value = false
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

  const storagePhase = computed(() => {
    const view = status.value
    if (view?.state === 'local' && view.discovery_status === 'inactive') return 'activate'
    if (view?.state === 'ready' && view.discovery_status === 'active' && view.storage?.option.id === 'temporary') return 'upgrade'
    return ''
  })
  const canActivate = computed(() => storagePhase.value === 'activate' &&
    storageOffers.value?.some(option => option.id === 'temporary' && option.available) === true)
  const canUpgrade = computed(() => storagePhase.value === 'upgrade' &&
    storageOffers.value?.some(option => option.id === 'paid' && option.available) === true)
  const refreshStorageOffers = async () => {
    const generation = ++offerGeneration
    const identity = identityKey.value
    const phase = storagePhase.value
    storageOffers.value = null
    storageReadFailed.value = false
    if (!sessionReady.value || !phase) return
    const current = () => generation === offerGeneration && identity === identityKey.value &&
      phase === storagePhase.value && sessionReady.value
    try {
      const options = await accountSDK.getStorageAvailability()
      if (current()) storageOffers.value = options
    } catch {
      if (current()) storageReadFailed.value = true
    }
  }
  const quotaModuleIssues = computed(() => moduleIssues.value.filter(item => item.error_code === 'DKVS_QUOTA_EXCEEDED'))
  const hasQuotaIssue = computed(() => status.value?.error_code === 'DKVS_QUOTA_EXCEEDED' || quotaModuleIssues.value.length > 0)
  const quotaNeedsActivation = computed(() => hasQuotaIssue.value && status.value?.state === 'local')
  const quotaGuidanceKey = computed(() => status.value?.state === 'local' ? 'accountManagement.quota.activation'
    : status.value?.storage?.option.id === 'temporary' ? 'accountManagement.quota.temporary'
    : status.value?.storage?.option.id === 'paid' ? 'accountManagement.quota.paid' : 'accountManagement.quota.review')
  const quotaNoticeKey = computed(() => hasQuotaIssue.value ? quotaGuidanceKey.value : '')
  const quotaActionKey = computed(() => quotaNeedsActivation.value ? 'accountManagement.quota.activateAction' : 'accountManagement.quota.reviewAction')
  const quotaMessageKeyForCode = (code: unknown) => code === 'DKVS_QUOTA_EXCEEDED' ? quotaGuidanceKey.value : ''

  const discoveryIssue = computed(() => !hasDatabaseIssue.value && !hasQuotaIssue.value && !statusReadFailed.value &&
    status.value?.discovery_status === 'unknown' && status.value.backup_risk === 'notConfigured' &&
    Boolean(status.value.error_code) && status.value.state !== 'locked')
  const readinessError = computed(() => hasDatabaseIssue.value || statusReadFailed.value || retryFailed.value ||
    status.value?.state === 'error' || hasQuotaIssue.value || discoveryIssue.value)
  const readinessKey = computed(() => {
    if (hasDatabaseIssue.value) return 'accountManagement.readiness.databaseFailed'
    if (quotaNoticeKey.value) return quotaNoticeKey.value
    if (statusReadFailed.value) return 'accountManagement.readiness.readFailed'
    if (discoveryIssue.value) return 'accountManagement.readiness.discoveryUnverified'
    if (retryFailed.value) return 'accountManagement.readiness.retryFailed'
    if (loading.value && !status.value) return 'accountManagement.readiness.syncing'
    switch (status.value?.state) {
      case 'local': return 'accountManagement.readiness.local'
      case 'locked': return 'accountManagement.readiness.locked'
      case 'syncing': return 'accountManagement.readiness.syncing'
      case 'error': return 'accountManagement.readiness.error'
      case 'ready':
        if (moduleIssues.value.length) return 'accountManagement.readiness.moduleIssue'
        if (status.value.backup_state === 'pending') return 'accountManagement.readiness.backupPending'
        if (status.value.backup_state === 'uploading') return 'accountManagement.readiness.backupUploading'
        if (status.value.backup_state === 'cleanup') return 'accountManagement.readiness.cleanup'
    }
    return ''
  })
  const shouldShowBanner = computed(() => sessionReady.value && Boolean(readinessKey.value))

  return {
    status,
    loading,
    statusReadFailed,
    retrying,
    retryQueued,
    retryFailed,
    sessionReady,
    identityKey,
    shouldShowBanner,
    discoveryIssue,
    readinessError,
    readinessKey,
    moduleIssues,
    hasDatabaseIssue,
    canRetryBackup,
    storagePhase,
    storageOffers,
    storageReadFailed,
    canActivate,
    canUpgrade,
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
    refreshStorageOffers,
    retryNow,
  }
})
