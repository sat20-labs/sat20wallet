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
    // 默认就是原生环境
    console.log('生物识别模块：默认启用原生环境')
    return true
  }

  /**
   * 初始化生物识别插件
   */
  private async initializePlugin(): Promise<boolean> {
    console.log('开始初始化生物识别插件...')

    try {
      // 动态导入生物识别插件
      const { BiometricAuth } = await import('@aparajita/capacitor-biometric-auth')
      this.BiometricAuth = BiometricAuth
      console.log('生物识别插件初始化成功')
      return true
    } catch (error) {
      console.error('生物识别插件加载失败:', error)
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
    console.log('开始检查生物识别支持...')

    // 详细的环境检查（仅用于调试）
    const envInfo = this.getEnvironmentInfo()
    console.log('当前环境信息:', envInfo)

    try {
      console.log('初始化生物识别插件...')
      const initialized = await this.initializePlugin()
      if (!initialized || !this.BiometricAuth) {
        const error = '生物识别插件未正确安装或配置'
        console.error('生物识别插件初始化失败', envInfo)
        return { supported: false, available: false, error }
      }

      console.log('调用原生生物识别检查...')
      const result = await this.BiometricAuth.checkBiometry()
      console.log('生物识别检查结果:', result)

      return {
        supported: true,
        available: result.isAvailable,
        biometryType: result.biometryType,
        error: result.reason || undefined
      }
    } catch (error) {
      console.error('检查生物识别支持失败:', error)
      let errorMessage = '未知错误'

      if (error instanceof Error) {
        // 常见错误处理
        if (error.message.includes('not implemented')) {
          errorMessage = '生物识别功能未正确配置，请检查应用权限设置'
        } else if (error.message.includes('permission')) {
          errorMessage = '缺少生物识别权限，请重新安装应用'
        } else if (error.message.includes('BiometricAuth')) {
          errorMessage = '生物识别插件未正确安装，请重新构建应用'
        } else {
          errorMessage = error.message
        }
      }

      return {
        supported: false,
        available: false,
        error: errorMessage
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

      // 调用实际的认证方法
      await this.BiometricAuth.authenticate({
        reason: options.reason || '请使用指纹验证以继续',
        title: options.title || '身份验证',
        subtitle: options.subtitle,
        description: options.description,
        cancelTitle: options.cancelButtonText || '取消',
        iosFallbackTitle: options.fallbackButtonText || '使用密码',
        allowDeviceCredential: true
      })

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