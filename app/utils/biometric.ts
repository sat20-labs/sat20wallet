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

/**
 * 生物识别服务类
 * 封装电容插件的生物识别功能
 */
export class BiometricService {
  private isNative: boolean
  private BiometricAuth: any = null

  constructor() {
    this.isNative = this.checkNativeEnvironment()
  }

  /**
   * 检查是否为原生环境
   */
  private checkNativeEnvironment(): boolean {
    return typeof window !== 'undefined' &&
           (window.hasOwnProperty('Capacitor') ||
            (window as any).Capacitor?.isNativePlatform === true)
  }

  /**
   * 初始化生物识别插件
   */
  private async initializePlugin(): Promise<boolean> {
    if (!this.isNative) {
      return false
    }

    try {
      // 动态导入生物识别插件
      const { BiometricAuth } = await import('capacitor-biometric-auth')
      this.BiometricAuth = BiometricAuth
      return true
    } catch (error) {
      console.warn('生物识别插件加载失败:', error)
      return false
    }
  }

  /**
   * 检查设备是否支持生物识别
   */
  public async checkBiometricSupport(): Promise<{
    supported: boolean
    available: boolean
    biometryType?: string
    error?: string
  }> {
    if (!this.isNative) {
      return { supported: false, available: false, error: '非原生环境' }
    }

    try {
      const initialized = await this.initializePlugin()
      if (!initialized || !this.BiometricAuth) {
        return { supported: false, available: false, error: '插件初始化失败' }
      }

      const result = await this.BiometricAuth.checkBiometry()

      return {
        supported: true,
        available: result.isAvailable,
        biometryType: result.biometryType
      }
    } catch (error) {
      console.warn('检查生物识别支持失败:', error)
      return {
        supported: false,
        available: false,
        error: error instanceof Error ? error.message : '未知错误'
      }
    }
  }

  /**
   * 执行生物识别认证
   */
  public async authenticate(options: BiometricOptions = {}): Promise<BiometricResult> {
    if (!this.isNative) {
      return { success: false, error: '非原生环境，无法使用生物识别' }
    }

    try {
      const initialized = await this.initializePlugin()
      if (!initialized || !this.BiometricAuth) {
        return { success: false, error: '生物识别插件未初始化' }
      }

      const checkResult = await this.checkBiometricSupport()
      if (!checkResult.available) {
        return {
          success: false,
          error: checkResult.error || '设备不支持生物识别或未设置'
        }
      }

      const result = await this.BiometricAuth.verify({
        reason: options.reason || '请使用指纹验证以继续',
        title: options.title || '身份验证',
        subtitle: options.subtitle,
        description: options.description,
        cancelButtonText: options.cancelButtonText || '取消',
        fallbackButtonText: options.fallbackButtonText || '使用密码',
        disableFallback: options.disableFallback || false
      })

      if (result.verified) {
        return { success: true }
      } else {
        return { success: false, error: '生物识别验证失败' }
      }
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