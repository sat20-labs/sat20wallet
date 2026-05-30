import { ref } from 'vue'
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

const localVersion = __SAT20_APP_VERSION__
const localBuildId = __SAT20_BUILD_ID__

function compareVersions(current: string, remote: string): number {
  const currentParts = current.replace('v', '').split('.').map(Number)
  const remoteParts = remote.replace('v', '').split('.').map(Number)

  for (let i = 0; i < Math.max(currentParts.length, remoteParts.length); i++) {
    const currentNum = currentParts[i] || 0
    const remoteNum = remoteParts[i] || 0

    if (currentNum !== remoteNum) {
      return remoteNum > currentNum ? 1 : -1
    }
  }
  return 0
}

export function useAppVersion() {
  const { t } = useI18n()
  const { toast } = useToast()
  const { isUpdating, notifyUpdateAvailable, reloadApp } = usePwaUpdate()
  const isChecking = ref(false)
  const hasUpdate = ref(false)
  const remoteVersion = ref<RemoteVersionInfo | null>(null)
  const isForceUpdate = ref(false)
  const lastCheckedVersion = ref<string | null>(null)
  const versionUrl = import.meta.env.VITE_SAT20_VERSION_URL
    || `${import.meta.env.BASE_URL}version.json`

  // 检查是否已经提醒过当前版本（避免重复提醒）
  const shouldShowNotification = (version: string): boolean => {
    const skipped = localStorage.getItem('skipVersion')
    if (skipped === version) {
      return false // 用户选择跳过此版本
    }
    return lastCheckedVersion.value !== version
  }

  const fetchRemoteVersion = async (): Promise<RemoteVersionInfo | null> => {
    try {
      const timestamp = Date.now()
      const separator = versionUrl.includes('?') ? '&' : '?'
      const response = await fetch(`${versionUrl}${separator}t=${timestamp}`)

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`)
      }

      const data = await response.json()
      return data as RemoteVersionInfo
    } catch (error) {
      console.error('Failed to fetch remote version:', error)
      return null
    }
  }

  const showUpdateNotification = (info: RemoteVersionInfo) => {
    // 如果用户选择跳过此版本，不再提醒
    if (!shouldShowNotification(info.version)) {
      console.log('已跳过版本提醒:', info.version)
      return
    }

    if (info.forceUpdate) {
      toast({
        variant: 'destructive',
        title: t('setting.forceUpdateTitle'),
        description: t('setting.forceUpdateDescription', { releaseNotes: info.releaseNotes }),
        duration: 10000,
      })
      lastCheckedVersion.value = info.version
    } else {
      notifyUpdateAvailable(`当前：v${localVersion} → 最新：v${info.version}`)
      lastCheckedVersion.value = info.version
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

      remoteVersion.value = info
      const comparison = compareVersions(localVersion, info.version)
      const buildChanged = comparison === 0 && Boolean(info.buildId) && info.buildId !== localBuildId

      hasUpdate.value = comparison > 0 || buildChanged
      isForceUpdate.value = info.forceUpdate

      if (hasUpdate.value) {
        showUpdateNotification(info)
        return true
      } else {
        if (!silent) {
          toast({
            variant: 'success',
            title: t('setting.latestVersionTitle'),
            description: t('setting.currentVersionDescription', { version: localVersion }),
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

      remoteVersion.value = info
      const comparison = compareVersions(localVersion, info.version)
      const buildChanged = comparison === 0 && Boolean(info.buildId) && info.buildId !== localBuildId

      hasUpdate.value = comparison > 0 || buildChanged
      isForceUpdate.value = info.forceUpdate

      if (!hasUpdate.value) {
        toast({
          variant: 'success',
          title: t('setting.latestVersionTitle'),
          description: t('setting.currentVersionDescription', { version: localVersion }),
        })
        return false
      }

      toast({
        variant: 'info',
        title: t('setting.updateAvailableTitle'),
        description: t('setting.updatingVersionDescription', {
          current: localBuildId ? `${localVersion}+${localBuildId}` : localVersion,
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
