import { ref, computed } from 'vue'
import { versionPolicy, policyRevision, acceptVersionPolicy, versionUrl } from '@/utils/pwaVersionPolicy'
import { versionCompare } from '@/utils/versionPolicy'
import { useI18n } from 'vue-i18n'
import { useToast } from '@/components/ui/toast-new/use-toast'
import { usePwaUpdate } from './usePwaUpdate'

export interface RemoteVersionInfo {
  version: string
  buildId?: string
  commit?: string
  releaseNotes: string
  forceUpdate: boolean
  minVersion: string
  publishedAt: string
}

const getEmbeddedVersion = () => (
  typeof __SAT20_APP_VERSION__ === 'string' ? __SAT20_APP_VERSION__ : '0.0.0'
)

const getEmbeddedBuildId = () => (
  typeof __SAT20_BUILD_ID__ === 'string' ? __SAT20_BUILD_ID__ : ''
)

function compareVersions(current: string, remote: string): number { return -(versionCompare(current, remote) ?? 0) }
const isChecking = ref(false)
const lastCheckedRelease = ref<string | null>(null)
const localVersion = ref(getEmbeddedVersion())
const localBuildId = ref(getEmbeddedBuildId())
const remoteVersion = computed(() => { void policyRevision.value; return versionPolicy.remote as RemoteVersionInfo | null })
const hasUpdate = computed(() => { void policyRevision.value; return !!versionPolicy.remote && versionPolicy.isNew(versionPolicy.remote) })
const isForceUpdate = computed(() => { void policyRevision.value; return versionPolicy.blocked })
let fetchGeneration = 0

export function useAppVersion() {
  const { t } = useI18n()
  const { toast } = useToast()
  const { isUpdating, notifyUpdateAvailable, reloadApp } = usePwaUpdate()

  // 检查是否已经提醒过当前版本（避免重复提醒）
  const releaseKey = (info: RemoteVersionInfo) => info.buildId ? `${info.version}+${info.buildId}` : info.version
  const shouldShowNotification = (info: RemoteVersionInfo): boolean => {
    const key = releaseKey(info)
    const skipped = localStorage.getItem('skipVersion')
    if (skipped === key) {
      return false // 用户选择跳过此版本
    }
    return lastCheckedRelease.value !== key
  }

  const fetchRemoteVersion = async (): Promise<RemoteVersionInfo | null> => {
    const generation = ++fetchGeneration
    try {
      const timestamp = Date.now()
      const separator = versionUrl.includes('?') ? '&' : '?'
      const response = await fetch(`${versionUrl}${separator}t=${timestamp}`)

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`)
      }

      const data = await response.json()
      if (generation !== fetchGeneration || !acceptVersionPolicy(data)) return null
      return data as RemoteVersionInfo
    } catch (error) {
      console.error('Failed to fetch remote version:', error)
      return null
    }
  }

  const showUpdateNotification = (info: RemoteVersionInfo) => {
    // 如果用户选择跳过此版本，不再提醒
    if (!shouldShowNotification(info)) {
      console.log('已跳过版本提醒:', releaseKey(info))
      return
    }

    if (isForceUpdate.value) {
      toast({
        variant: 'destructive',
        title: t('setting.forceUpdateTitle'),
        description: t('setting.forceUpdateDescription', { releaseNotes: info.releaseNotes }),
        duration: 10000,
      })
      lastCheckedRelease.value = releaseKey(info)
    } else {
      const current = localBuildId.value ? `${localVersion.value}+${localBuildId.value}` : localVersion.value
      notifyUpdateAvailable(`当前：v${current} → 最新：v${releaseKey(info)}`)
      lastCheckedRelease.value = releaseKey(info)
    }
  }

  const checkForUpdates = async (silent = false): Promise<boolean> => {
    isChecking.value = true

    try {
      const info = await fetchRemoteVersion()

      if (!info) {
        if (!silent) {
          toast({
            variant: 'destructive',
            title: t('setting.updateCheckFailed'),
            description: t('setting.updateCheckFailedDescription'),
          })
        }
        return false
      }


      if (hasUpdate.value || isForceUpdate.value) {
        showUpdateNotification(info)
        return true
      } else {
        if (!silent) {
          toast({
            variant: 'success',
            title: t('setting.latestVersionTitle'),
            description: t('setting.currentVersionDescription', { version: localVersion.value }),
          })
        }
        return false
      }
    } catch (error) {
      console.error('Version check failed:', error)
      if (!silent) {
        toast({
          variant: 'destructive',
          title: t('setting.updateCheckFailed'),
          description: error instanceof Error ? error.message : t('common.error'),
        })
      }
      return false
    } finally {
      isChecking.value = false
    }
  }

  const manualCheck = async () => {
    return checkForUpdates(false)
  }

  const checkAndUpdate = async (): Promise<boolean> => {
    isChecking.value = true

    try {
      const info = await fetchRemoteVersion()

      if (!info) {
        toast({
          variant: 'destructive',
          title: t('setting.updateCheckFailed'),
          description: t('setting.updateCheckFailedDescription'),
        })
        return false
      }


      if (!hasUpdate.value && !isForceUpdate.value) {
        toast({
          variant: 'success',
          title: t('setting.latestVersionTitle'),
          description: t('setting.currentVersionDescription', { version: localVersion.value }),
        })
        return false
      }

      toast({
        variant: 'info',
        title: t('setting.updateAvailableTitle'),
        description: t('setting.updatingVersionDescription', {
          current: localBuildId.value ? `${localVersion.value}+${localBuildId.value}` : localVersion.value,
          latest: info.buildId ? `${info.version}+${info.buildId}` : info.version,
        }),
        duration: 3000,
      })
      await reloadApp()
      return true
    } catch (error) {
      console.error('Version update failed:', error)
      toast({
        variant: 'destructive',
        title: t('setting.updateFailed'),
        description: error instanceof Error ? error.message : t('common.error'),
      })
      return false
    } finally {
      isChecking.value = false
    }
  }



  return {
    isChecking,
    isUpdating,
    hasUpdate,
    remoteVersion,
    isForceUpdate,
    localVersion,
    localBuildId,
    checkForUpdates,
    checkAndUpdate,
    manualCheck,
    compareVersions
  }
}
