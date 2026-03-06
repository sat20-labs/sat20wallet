import { ref } from 'vue'
import { useToast } from '@/components/ui/toast-new/use-toast'
import { version as localVersion } from '@/package.json'

export interface RemoteVersionInfo {
  version: string
  releaseNotes: string
  forceUpdate: boolean
  minVersion: string
  publishedAt: string
}

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
  const { toast } = useToast()
  const isChecking = ref(false)
  const hasUpdate = ref(false)
  const remoteVersion = ref<RemoteVersionInfo | null>(null)
  const isForceUpdate = ref(false)

  const fetchRemoteVersion = async (): Promise<RemoteVersionInfo | null> => {
    try {
      const timestamp = Date.now()
      const response = await fetch(
        `https://raw.githubusercontent.com/jieziyuan/sat20wallet/main/version.json?t=${timestamp}`
      )
      
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
    if (info.forceUpdate) {
      toast({
        variant: 'destructive',
        title: '⚠️ 发现重要版本更新',
        description: `${info.releaseNotes} - 请前往 GitHub 下载最新版本`,
      })
    } else {
      toast({
        variant: 'info',
        title: '🎉 发现新版本',
        description: `当前：v${localVersion} → 最新：v${info.version}`,
      })
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
            title: '检查失败',
            description: '无法获取版本信息，请稍后重试',
          })
        }
        return false
      }
      
      remoteVersion.value = info
      const comparison = compareVersions(localVersion, info.version)
      
      hasUpdate.value = comparison > 0
      isForceUpdate.value = info.forceUpdate
      
      if (hasUpdate.value) {
        if (!silent) {
          showUpdateNotification(info)
        }
        return true
      } else {
        if (!silent) {
          toast({
            variant: 'success',
            title: '已是最新版本',
            description: `当前版本：v${localVersion}`,
          })
        }
        return false
      }
    } catch (error) {
      console.error('Version check failed:', error)
      if (!silent) {
        toast({
          variant: 'destructive',
          title: '检查失败',
          description: error instanceof Error ? error.message : '未知错误',
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

  return {
    isChecking,
    hasUpdate,
    remoteVersion,
    isForceUpdate,
    localVersion,
    checkForUpdates,
    manualCheck,
    compareVersions
  }
}
