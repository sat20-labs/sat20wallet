/**
 * 生物识别服务
 * 提供指纹、面部识别等生物识别功能
 */

export interface BiometricOptions {
  reason?: string
  title?: string
  subtitle?: string
  description?: string
  negativeButtonText?: string
  cancelButtonText?: string
  fallbackButtonText?: string
  disableFallback?: boolean
}

export interface BiometricResult {
  success: boolean
  error?: string
}

const WEBAUTHN_TIMEOUT_MS = 15000
const INSECURE_WEBAUTHN_ERROR = '生物识别需要没有证书错误的安全 HTTPS 环境。请使用有效证书的 HTTPS 地址，或在本地测试时使用 Chrome 已信任的安全 origin。'
const UNSUPPORTED_ORIGIN_ERROR = '当前环境不能安全启用生物识别。请使用有效 HTTPS 地址；Android 上生物识别仅在 HTTPS 环境下启用。'

const isLocalhost = (hostname: string): boolean => {
  return hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '[::1]' || hostname === '::1'
}

const isIpAddress = (hostname: string): boolean => {
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(hostname) || hostname.includes(':')
}

const isAndroid = (): boolean => /Android/i.test(navigator.userAgent)

export const getWebAuthnOriginError = (): string | null => {
  if (!window.isSecureContext) {
    return INSECURE_WEBAUTHN_ERROR
  }

  if (isAndroid() && location.protocol !== 'https:') {
    return UNSUPPORTED_ORIGIN_ERROR
  }

  if (isLocalhost(location.hostname)) {
    return null
  }

  if (isIpAddress(location.hostname)) {
    return UNSUPPORTED_ORIGIN_ERROR
  }

  if (location.protocol === 'https:') {
    return null
  }

  return UNSUPPORTED_ORIGIN_ERROR
}

const withTimeout = async <T>(promise: Promise<T>, label: string, ms = WEBAUTHN_TIMEOUT_MS): Promise<T> => {
  return Promise.race([
    promise,
    new Promise<T>((_, reject) => {
      setTimeout(() => reject(new Error(`${label} timed out after ${ms}ms`)), ms)
    }),
  ])
}

const normalizeWebAuthnError = (error: unknown): string => {
  const message = error instanceof Error ? error.message : String(error)
  if (/TLS certificate|certificate error|secure context|not allowed/i.test(message)) {
    return INSECURE_WEBAUTHN_ERROR
  }
  return message || 'Unknown WebAuthn error'
}

/**
 * 生物识别服务类
 * 封装电容插件的生物识别功能
 */
export class BiometricService {
  private isNative = false

  /**
   * 检查设备是否支持生物识别
   */
  public async checkBiometricSupport(): Promise<{
    supported: boolean
    available: boolean
    biometryType?: string
    error?: string
  }> {
    try {
      const originError = getWebAuthnOriginError()
      if (originError) {
        return {
          supported: false,
          available: false,
          error: originError
        }
      }

      if (!window.PublicKeyCredential) {
        return {
          supported: false,
          available: false,
          error: 'WebAuthn is not supported in this browser'
        }
      }

      if (typeof PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable !== 'function') {
        return {
          supported: true,
          available: false,
          error: 'Platform authenticator availability check is not supported'
        }
      }

      const available = await withTimeout(
        PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable(),
        'Platform authenticator availability check'
      )

      return {
        supported: true,
        available,
        biometryType: 'platform',
        error: available ? undefined : 'No platform authenticator is available'
      }
    } catch (error) {
      return {
        supported: false,
        available: false,
        error: normalizeWebAuthnError(error)
      }
    }
  }

  /**
   * 获取详细的环境信息用于调试
   */
  private getEnvironmentInfo(): any {
    try {
      const capacitor = (window as any).Capacitor
      return {
        hasCapacitor: !!capacitor,
        isNativePlatform: capacitor?.isNativePlatform,
        platform: capacitor?.getPlatform(),
        isPluginAvailable: capacitor?.isPluginAvailable('BiometricAuth'),
        userAgent: navigator.userAgent,
        isWebView: /wv|WebView/i.test(navigator.userAgent)
      }
    } catch (error) {
      return { error: error instanceof Error ? error.message : '未知错误' }
    }
  }

  /**
   * 执行生物识别认证
   */
  public async authenticate(options: BiometricOptions = {}): Promise<BiometricResult> {
    try {
      const checkResult = await this.checkBiometricSupport()
      if (!checkResult.available) {
        return {
          success: false,
          error: checkResult.error || '设备不支持生物识别或未设置'
        }
      }

      return { success: true }
    } catch (error) {
      console.error('生物识别认证失败:', error)
      return {
        success: false,
        error: error instanceof Error ? error.message : '生物识别认证异常'
      }
    }
  }

  /**
   * 获取生物识别类型
   */
  public async getBiometryType(): Promise<string | null> {
    const result = await this.checkBiometricSupport()
    return result.biometryType || null
  }

  /**
   * 检查是否为原生环境
   */
  public get isNativePlatform(): boolean {
    return this.isNative
  }
}

// 创建全局实例
export const biometricService = new BiometricService()

// 便捷方法
export const checkBiometricSupport = () => biometricService.checkBiometricSupport()
export const authenticateWithBiometric = (options?: BiometricOptions) =>
  biometricService.authenticate(options)
export const getBiometryType = () => biometricService.getBiometryType()
