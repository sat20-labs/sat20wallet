import { ref, onMounted, onUnmounted, computed } from 'vue'

export interface DeepLinkCallback {
  sessionId: string
  action: string
  data: any
  timestamp: number
}

export interface PendingRequest {
  resolve: (value: any) => void
  reject: (reason?: any) => void
  timeout: NodeJS.Timeout
  action: string
}

export function useDeepLinkBridge() {
  const isSupported = ref(false)
  const pendingRequests = ref<Map<string, PendingRequest>>(new Map())
  const lastCallback = ref<DeepLinkCallback | null>(null)
  const callbackHistory = ref<DeepLinkCallback[]>([])

  /**
   * 生成Deep Link URL
   */
  function generateDeepLinkUrl(baseUrl: string, action: string, data: any): string {
    const params = new URLSearchParams({
      sat20_action: action,
      sat20_data: JSON.stringify(data),
      sat20_timestamp: Date.now().toString()
    })

    return `${baseUrl}?${params.toString()}`
  }

  /**
   * 处理Deep Link回调
   */
  async function handleAppUrlOpen(data: { url: string }): Promise<void> {
    console.log('🔗 Deep Link received:', data.url)

    try {
      const url = new URL(data.url)
      const params = url.searchParams

      const sessionId = params.get('sat20_session')
      const action = params.get('sat20_action')
      const dataStr = params.get('sat20_data')
      const timestamp = parseInt(params.get('sat20_timestamp') || '0')

      if (!sessionId || !action) {
        console.warn('⚠️ Invalid Deep Link format, missing required parameters')
        return
      }

      const callback: DeepLinkCallback = {
        sessionId,
        action,
        data: dataStr ? JSON.parse(dataStr) : null,
        timestamp
      }

      // 添加到历史记录
      callbackHistory.value.unshift(callback)
      if (callbackHistory.value.length > 50) {
        callbackHistory.value = callbackHistory.value.slice(0, 50)
      }

      lastCallback.value = callback

      // 处理待处理的请求
      const pendingRequest = pendingRequests.value.get(sessionId)
      if (pendingRequest) {
        console.log(`✅ Found pending request for session: ${sessionId}`)

        // 清除超时
        if (pendingRequest.timeout) {
          clearTimeout(pendingRequest.timeout)
        }

        // 根据action处理响应
        if (action === 'success') {
          pendingRequest.resolve(callback.data)
        } else if (action === 'error') {
          const errorMessage = callback.data?.error || 'Unknown error'
          pendingRequest.reject(new Error(errorMessage))
        } else {
          pendingRequest.resolve(callback.data)
        }

        pendingRequests.value.delete(sessionId)
      } else {
        console.log(`📝 No pending request found for session: ${sessionId}`)
        // 触发全局事件
        window.dispatchEvent(new CustomEvent('sat20-deeplink', {
          detail: callback
        }))
      }

    } catch (error) {
      console.error('❌ Error handling Deep Link:', error)
    }
  }

  /**
   * 等待Deep Link响应
   */
  function waitForDeepLinkResponse(sessionId: string, action: string, timeout: number = 60000): Promise<any> {
    return new Promise((resolve, reject) => {
      console.log(`⏳ Waiting for Deep Link response: ${sessionId} - ${action}`)

      // 设置超时
      const timeoutId = setTimeout(() => {
        pendingRequests.value.delete(sessionId)
        reject(new Error(`Deep Link response timeout after ${timeout}ms: ${action}`))
      }, timeout)

      // 存储待处理的请求
      pendingRequests.value.set(sessionId, {
        resolve,
        reject,
        timeout: timeoutId,
        action
      })
    })
  }

  /**
   * 清理超时的请求
   */
  function cleanupExpiredRequests(): void {
    const now = Date.now()
    const expiredSessions: string[] = []

    pendingRequests.value.forEach((request, sessionId) => {
      if (request.timeout) {
        // 简单的超时检查
        expiredSessions.push(sessionId)
      }
    })

    expiredSessions.forEach(sessionId => {
      const request = pendingRequests.value.get(sessionId)
      if (request) {
        clearTimeout(request.timeout)
        request.reject(new Error('Request expired'))
        pendingRequests.value.delete(sessionId)
      }
    })
  }

  /**
   * 获取Deep Link Scheme
   */
  function getDeepLinkScheme(): string {
    return 'https'
  }

  /**
   * 构建DApp URL，包含会话参数
   */
  function buildDAppUrl(originalUrl: string, sessionId: string): string {
    try {
      const url = new URL(originalUrl)

      // 添加SAT20相关参数
      url.searchParams.append('sat20_session', sessionId)
      url.searchParams.append('sat20_platform', 'web')
      url.searchParams.append('sat20_scheme', getDeepLinkScheme())
      url.searchParams.append('sat20_timestamp', Date.now().toString())

      return url.toString()
    } catch (error) {
      console.warn('⚠️ Failed to build DApp URL:', error)
      return originalUrl
    }
  }

  // 生命周期管理
  onMounted(() => {
    console.log('ℹ️ Native Deep Link bridge is disabled in PWA mode')
  })

  onUnmounted(() => {
    const cleanupInterval = (window as any).__sat20CleanupInterval
    if (cleanupInterval) {
      clearInterval(cleanupInterval)
      delete (window as any).__sat20CleanupInterval
    }

    // 清理所有待处理的请求
    pendingRequests.value.forEach((request, sessionId) => {
      if (request.timeout) {
        clearTimeout(request.timeout)
      }
      request.reject(new Error('Component unmounted'))
    })
    pendingRequests.value.clear()

    console.log('🧹 Deep Link bridge cleaned up')
  })

  return {
    // 状态
    isSupported,
    lastCallback,
    callbackHistory,
    pendingRequestCount: computed(() => pendingRequests.value.size),

    // 方法
    generateDeepLinkUrl,
    waitForDeepLinkResponse,
    buildDAppUrl,
    getDeepLinkScheme,
    cleanupExpiredRequests
  }
}
