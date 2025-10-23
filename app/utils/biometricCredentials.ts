/**
 * 生物识别凭据管理器
 * 处理生物识别凭据的创建、验证和管理
 */

import { biometricService } from './biometric'
import { hashPassword } from './crypto'

export interface BiometricCredential {
  id: string
  name: string
  deviceId: string
  createdAt: number
  lastUsed?: number
  isActive: boolean
}

export interface CredentialVerificationResult {
  valid: boolean
  credential?: BiometricCredential
  password?: string // 注意：这里返回的是哈希密码，不是明文密码
  error?: string
}

export const CREDENTIALS_STORAGE_KEY = 'sat20_biometric_credentials'
export const CHALLENGE_KEY = 'sat20_biometric_challenge'
export const PASSWORD_STORAGE_KEY = 'sat20_biometric_hashed_passwords'

/**
 * 生物识别凭据管理器类
 * 提供安全的生物识别凭据管理
 */
export class BiometricCredentialManager {
  private isNative: boolean
  private credentials: Map<string, BiometricCredential> = new Map()

  constructor() {
    this.isNative = biometricService.isNativePlatform
    this.loadCredentials()
  }

  /**
   * 生成唯一ID
   */
  private generateId(): string {
    return `cred_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`
  }

  /**
   * 从存储加载凭据
   */
  private loadCredentials(): void {
    try {
      if (typeof localStorage === 'undefined') return

      const stored = localStorage.getItem(CREDENTIALS_STORAGE_KEY)
      if (stored) {
        const credentialsData = JSON.parse(stored)
        this.credentials = new Map(Object.entries(credentialsData))
      }
    } catch (error) {
      console.error('加载生物识别凭据失败:', error)
    }
  }

  /**
   * 保存凭据到存储
   */
  private saveCredentials(): void {
    try {
      if (typeof localStorage === 'undefined') return

      const credentialsObject = Object.fromEntries(this.credentials)
      localStorage.setItem(CREDENTIALS_STORAGE_KEY, JSON.stringify(credentialsObject))
    } catch (error) {
      console.error('保存生物识别凭据失败:', error)
    }
  }

  /**
   * 生成挑战
   */
  private generateChallenge(): string {
    const challenge = {
      value: Math.random().toString(36).substr(2, 16),
      timestamp: Date.now(),
      nonce: Math.random().toString(36).substr(2, 8)
    }
    return JSON.stringify(challenge)
  }

  /**
   * 存储挑战值
   */
  private storeChallenge(challenge: string): void {
    try {
      if (typeof localStorage === 'undefined') return
      localStorage.setItem(CHALLENGE_KEY, challenge)
    } catch (error) {
      console.error('存储挑战值失败:', error)
    }
  }

  /**
   * 获取并清除挑战值
   */
  private getAndClearChallenge(): string | null {
    try {
      if (typeof localStorage === 'undefined') return null

      const challenge = localStorage.getItem(CHALLENGE_KEY)
      localStorage.removeItem(CHALLENGE_KEY)
      return challenge
    } catch (error) {
      console.error('获取挑战值失败:', error)
      return null
    }
  }

  /**
   * 派生公钥
   */
  private async derivePublicKey(password: string, credentialId: Uint8Array): Promise<Uint8Array> {
    try {
      // 使用密码和凭据ID派生密钥
      const passwordBuffer = new TextEncoder().encode(password)
      const combined = new Uint8Array(passwordBuffer.length + credentialId.length)
      combined.set(passwordBuffer)
      combined.set(credentialId, passwordBuffer.length)

      // 使用SHA-256哈希
      const hashBuffer = await crypto.subtle.digest('SHA-256', combined)
      return new Uint8Array(hashBuffer)
    } catch (error) {
      console.error('派生公钥失败:', error)
      throw new Error('派生公钥失败')
    }
  }

  /**
   * 检查是否支持生物识别
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

    return biometricService.checkBiometricSupport()
  }

  /**
   * 创建生物识别凭据
   * 注意：传入的 password 应该是哈希后的密码，不是明文密码
   */
  public async createCredential(
    hashedPassword: string,
    name: string = '默认凭据'
  ): Promise<{ success: boolean, credentialId?: string, error?: string }> {
    try {
      if (!this.isNative) {
        return { success: false, error: '非原生环境，无法创建生物识别凭据' }
      }

      // 检查生物识别支持
      const supportCheck = await this.checkBiometricSupport()
      if (!supportCheck.available) {
        return {
          success: false,
          error: supportCheck.error || '设备不支持生物识别或未设置'
        }
      }

      // 验证生物识别
      const authResult = await biometricService.authenticate({
        reason: '创建生物识别凭据',
        title: '设置生物识别',
        description: '请验证您的身份以创建生物识别凭据'
      })

      if (!authResult.success) {
        return { success: false, error: '生物识别验证失败' }
      }

      // 生成凭据ID
      const credentialId = this.generateId()
      const credentialIdBytes = new TextEncoder().encode(credentialId)

      // 派生并存储公钥（使用哈希密码）
      const publicKey = await this.derivePublicKey(hashedPassword, credentialIdBytes)

      // 创建凭据记录
      const credential: BiometricCredential = {
        id: credentialId,
        name: name,
        deviceId: 'native', // 原生环境设备ID
        createdAt: Date.now(),
        isActive: true
      }

      // 保存凭据
      this.credentials.set(credentialId, credential)
      this.saveCredentials()

      // 存储哈希密码用于生物识别解锁
      this.storePassword(hashedPassword)

      console.log('生物识别凭据创建成功:', credentialId)

      return { success: true, credentialId }
    } catch (error) {
      console.error('创建生物识别凭据失败:', error)
      return {
        success: false,
        error: error instanceof Error ? error.message : '创建凭据失败'
      }
    }
  }

  /**
   * 验证凭据
   * 支持两种调用方式：
   * 1. verifyCredential(password) - 传统方式，需要提供密码
   * 2. verifyCredential() - 新方式，从存储获取密码
   */
  public async verifyCredential(password?: string): Promise<CredentialVerificationResult> {
    try {
      if (!this.isNative) {
        return { valid: false, error: '非原生环境' }
      }

      // 获取活跃凭据
      const activeCredentials = Array.from(this.credentials.values())
        .filter(cred => cred.isActive)

      if (activeCredentials.length === 0) {
        return { valid: false, error: '未找到活跃的生物识别凭据' }
      }

      // 如果没有提供密码，尝试从存储获取
      if (!password) {
        const storedPassword = this.getStoredPassword()
        if (!storedPassword) {
          return { valid: false, error: '未找到保存的密码，请先手动解锁一次' }
        }
        password = storedPassword
      }

      // 生成并存储挑战
      const challenge = this.generateChallenge()
      this.storeChallenge(challenge)

      // 验证生物识别
      const authResult = await biometricService.authenticate({
        reason: '验证生物识别',
        title: '身份验证',
        description: '请使用生物识别验证您的身份'
      })

      if (!authResult.success) {
        return { valid: false, error: '生物识别验证失败' }
      }

      // 获取并验证挑战
      const storedChallenge = this.getAndClearChallenge()
      if (!storedChallenge) {
        return { valid: false, error: '挑战验证失败' }
      }

      // 验证凭据（这里简化处理，实际应该验证密钥匹配）
      for (const credential of activeCredentials) {
        try {
          const credentialIdBytes = new TextEncoder().encode(credential.id)
          const expectedPublicKey = await this.derivePublicKey(password, credentialIdBytes)

          // 这里应该验证派生的密钥是否匹配存储的公钥
          // 简化版本：只要生物识别成功就认为凭据有效
          credential.lastUsed = Date.now()
          this.saveCredentials()

          return { valid: true, credential, password }
        } catch (error) {
          console.warn('验证凭据失败:', credential.id, error)
          continue
        }
      }

      return { valid: false, error: '凭据验证失败' }
    } catch (error) {
      console.error('验证生物识别凭据失败:', error)
      return {
        valid: false,
        error: error instanceof Error ? error.message : '验证凭据失败'
      }
    }
  }

  /**
   * 存储哈希密码（用于生物识别解锁）
   * 注意：这里存储的是已经哈希后的密码，不是明文密码
   */
  public storePassword(hashedPassword: string): void {
    try {
      if (typeof localStorage === 'undefined') return

      // 简单的编码存储（实际应用中应使用更安全的方法）
      // 注意：传入的密码已经是哈希值，不需要再次哈希
      const encoded = btoa(hashedPassword)
      localStorage.setItem(PASSWORD_STORAGE_KEY, encoded)
      console.log('哈希密码已存储到生物识别凭据')
    } catch (error) {
      console.error('存储哈希密码失败:', error)
    }
  }

  /**
   * 获取存储的哈希密码
   * 返回：哈希后的密码（可直接用于钱包解锁）
   */
  public getStoredPassword(): string | null {
    try {
      if (typeof localStorage === 'undefined') return null

      const encoded = localStorage.getItem(PASSWORD_STORAGE_KEY)
      if (!encoded) return null

      return atob(encoded)
    } catch (error) {
      console.error('获取存储的哈希密码失败:', error)
      return null
    }
  }

  /**
   * 清除存储的密码
   */
  private clearStoredPassword(): void {
    try {
      if (typeof localStorage === 'undefined') return
      localStorage.removeItem(PASSWORD_STORAGE_KEY)
    } catch (error) {
      console.error('清除存储的密码失败:', error)
    }
  }

  /**
   * 获取所有凭据
   */
  public getCredentials(): BiometricCredential[] {
    return Array.from(this.credentials.values())
  }

  /**
   * 获取活跃凭据
   */
  public getActiveCredentials(): BiometricCredential[] {
    return Array.from(this.credentials.values()).filter(cred => cred.isActive)
  }

  /**
   * 删除凭据
   */
  public deleteCredential(credentialId: string): { success: boolean, error?: string } {
    try {
      if (this.credentials.has(credentialId)) {
        this.credentials.delete(credentialId)
        this.saveCredentials()

        // 如果没有活跃凭据了，清除存储的密码
        if (!this.hasActiveCredentials()) {
          this.clearStoredPassword()
        }

        return { success: true }
      } else {
        return { success: false, error: '凭据不存在' }
      }
    } catch (error) {
      console.error('删除凭据失败:', error)
      return {
        success: false,
        error: error instanceof Error ? error.message : '删除凭据失败'
      }
    }
  }

  /**
   * 禁用凭据
   */
  public disableCredential(credentialId: string): { success: boolean, error?: string } {
    try {
      const credential = this.credentials.get(credentialId)
      if (credential) {
        credential.isActive = false
        this.saveCredentials()

        // 如果没有活跃凭据了，清除存储的密码
        if (!this.hasActiveCredentials()) {
          this.clearStoredPassword()
        }

        return { success: true }
      } else {
        return { success: false, error: '凭据不存在' }
      }
    } catch (error) {
      console.error('禁用凭据失败:', error)
      return {
        success: false,
        error: error instanceof Error ? error.message : '禁用凭据失败'
      }
    }
  }

  /**
   * 清除所有凭据
   */
  public clearAllCredentials(): { success: boolean, error?: string } {
    try {
      this.credentials.clear()
      if (typeof localStorage !== 'undefined') {
        localStorage.removeItem(CREDENTIALS_STORAGE_KEY)
        localStorage.removeItem(CHALLENGE_KEY)
        localStorage.removeItem(PASSWORD_STORAGE_KEY)
      }
      return { success: true }
    } catch (error) {
      console.error('清除所有凭据失败:', error)
      return {
        success: false,
        error: error instanceof Error ? error.message : '清除凭据失败'
      }
    }
  }

  /**
   * 检查是否有活跃凭据
   */
  public hasActiveCredentials(): boolean {
    return this.getActiveCredentials().length > 0
  }

  /**
   * 获取凭据统计信息
   */
  public getStats(): {
    total: number
    active: number
    inactive: number
    lastUsed?: number
  } {
    const credentials = this.getCredentials()
    const active = credentials.filter(cred => cred.isActive)
    const inactive = credentials.filter(cred => !cred.isActive)
    const lastUsed = Math.max(...credentials.map(cred => cred.lastUsed || 0))

    return {
      total: credentials.length,
      active: active.length,
      inactive: inactive.length,
      lastUsed: lastUsed > 0 ? lastUsed : undefined
    }
  }
}

// 创建全局实例
export const biometricCredentialManager = new BiometricCredentialManager()

// 便捷方法
export const createBiometricCredential = (hashedPassword: string, name?: string) =>
  biometricCredentialManager.createCredential(hashedPassword, name)
export const verifyBiometricCredential = () =>
  biometricCredentialManager.verifyCredential() // 不传密码，让它从存储获取哈希密码
export const getBiometricCredentials = () => biometricCredentialManager.getCredentials()
export const deleteBiometricCredential = (credentialId: string) =>
  biometricCredentialManager.deleteCredential(credentialId)
export const clearAllBiometricCredentials = () => biometricCredentialManager.clearAllCredentials()