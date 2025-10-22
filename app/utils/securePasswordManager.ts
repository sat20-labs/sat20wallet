/**
 * 安全密码管理器
 * 提供端到端的密码加密存储功能
 */

import { biometricService } from './biometric'

export interface EncryptedPasswordData {
  data: string // 加密后的密码数据
  iv: string   // 初始化向量
  salt: string // 盐值
  algorithm: string
  keyLength: number
  deviceId?: string
  createdAt: number
  version: string
}

export interface SecurePasswordManagerOptions {
  iterationCount?: number
  keyLength?: number
  ivLength?: number
  saltLength?: number
  algorithm?: string
}

/**
 * 安全密码管理器类
 * 使用AES-GCM算法进行密码加密
 */
export class SecurePasswordManager {
  private readonly defaultOptions: Required<SecurePasswordManagerOptions>
  private deviceId: string

  constructor(options: SecurePasswordManagerOptions = {}) {
    this.defaultOptions = {
      iterationCount: options.iterationCount || 100000,
      keyLength: options.keyLength || 32,
      ivLength: options.ivLength || 12,
      saltLength: options.saltLength || 16,
      algorithm: options.algorithm || 'AES-GCM'
    }
    this.deviceId = this.generateDeviceId()
  }

  /**
   * 生成设备ID
   */
  private generateDeviceId(): string {
    if (typeof window !== 'undefined' && (window as any).Capacitor?.getPlatform) {
      const platform = (window as any).Capacitor.getPlatform()
      return `${platform}-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`
    }
    return `web-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`
  }

  /**
   * 生成安全随机数
   */
  private generateSecureRandom(length: number): Uint8Array {
    if (typeof crypto !== 'undefined' && crypto.getRandomValues) {
      return crypto.getRandomValues(new Uint8Array(length))
    }
    // 降级处理
    const array = new Uint8Array(length)
    for (let i = 0; i < length; i++) {
      array[i] = Math.floor(Math.random() * 256)
    }
    return array
  }

  /**
   * 生成盐值
   */
  private generateSalt(length: number): Uint8Array {
    return this.generateSecureRandom(length)
  }

  /**
   * 数组转十六进制字符串
   */
  private arrayBufferToHex(buffer: ArrayBuffer): string {
    return Array.from(new Uint8Array(buffer))
      .map(b => b.toString(16).padStart(2, '0'))
      .join('')
  }

  /**
   * 十六进制字符串转数组
   */
  private hexToArrayBuffer(hex: string): ArrayBuffer {
    const bytes = new Uint8Array(hex.length / 2)
    for (let i = 0; i < hex.length; i += 2) {
      bytes[i / 2] = parseInt(hex.substr(i, 2), 16)
    }
    return bytes.buffer
  }

  /**
   * 字符串转ArrayBuffer
   */
  private stringToArrayBuffer(str: string): ArrayBuffer {
    return new TextEncoder().encode(str).buffer
  }

  /**
   * ArrayBuffer转字符串
   */
  private arrayBufferToString(buffer: ArrayBuffer): string {
    return new TextDecoder().decode(buffer)
  }

  /**
   * 使用PBKDF2派生密钥
   */
  private async deriveKey(
    password: string,
    salt: Uint8Array,
    iterations: number,
    keyLength: number
  ): Promise<CryptoKey> {
    const passwordBuffer = this.stringToArrayBuffer(password)
    const importedKey = await crypto.subtle.importKey(
      'raw',
      passwordBuffer,
      { name: 'PBKDF2' },
      false,
      ['deriveKey']
    )

    return crypto.subtle.deriveKey(
      {
        name: 'PBKDF2',
        salt: salt,
        iterations: iterations,
        hash: 'SHA-256'
      },
      importedKey,
      { name: this.defaultOptions.algorithm, length: keyLength * 8 },
      false,
      ['encrypt', 'decrypt']
    )
  }

  /**
   * 加密密码
   */
  public async encryptPassword(
    password: string,
    masterKey: string,
    deviceId?: string
  ): Promise<EncryptedPasswordData> {
    try {
      const iv = this.generateSecureRandom(this.defaultOptions.ivLength)
      const salt = this.generateSecureRandom(this.defaultOptions.saltLength)
      const targetDeviceId = deviceId || this.deviceId

      // 使用主密钥和盐值派生加密密钥
      const encryptionKey = await this.deriveKey(
        masterKey,
        salt,
        this.defaultOptions.iterationCount,
        this.defaultOptions.keyLength
      )

      // 加密密码
      const passwordData = this.stringToArrayBuffer(password)
      const encryptedData = await crypto.subtle.encrypt(
        {
          name: this.defaultOptions.algorithm,
          iv: iv
        },
        encryptionKey,
        passwordData
      )

      return {
        data: this.arrayBufferToHex(encryptedData),
        iv: this.arrayBufferToHex(iv.buffer),
        salt: this.arrayBufferToHex(salt.buffer),
        algorithm: this.defaultOptions.algorithm,
        keyLength: this.defaultOptions.keyLength,
        deviceId: targetDeviceId,
        createdAt: Date.now(),
        version: '1.0'
      }
    } catch (error) {
      console.error('密码加密失败:', error)
      throw new Error('密码加密失败: ' + (error instanceof Error ? error.message : '未知错误'))
    }
  }

  /**
   * 解密密码
   */
  public async decryptPassword(
    encryptedData: EncryptedPasswordData,
    masterKey: string
  ): Promise<string> {
    try {
      // 检查版本兼容性
      if (encryptedData.version !== '1.0') {
        throw new Error(`不支持的加密版本: ${encryptedData.version}`)
      }

      // 重新派生密钥
      const salt = new Uint8Array(this.hexToArrayBuffer(encryptedData.salt))
      const encryptionKey = await this.deriveKey(
        masterKey,
        salt,
        this.defaultOptions.iterationCount,
        encryptedData.keyLength
      )

      // 解密数据
      const iv = new Uint8Array(this.hexToArrayBuffer(encryptedData.iv))
      const encryptedBuffer = this.hexToArrayBuffer(encryptedData.data)

      const decryptedData = await crypto.subtle.decrypt(
        {
          name: encryptedData.algorithm,
          iv: iv
        },
        encryptionKey,
        encryptedBuffer
      )

      return this.arrayBufferToString(decryptedData)
    } catch (error) {
      console.error('密码解密失败:', error)
      throw new Error('密码解密失败: ' + (error instanceof Error ? error.message : '未知错误'))
    }
  }

  /**
   * 使用生物识别加密密码
   */
  public async encryptPasswordWithBiometric(
    password: string,
    biometricKey: string
  ): Promise<EncryptedPasswordData> {
    try {
      // 验证生物识别
      const biometricResult = await biometricService.authenticate({
        reason: '使用生物识别验证以加密密码'
      })

      if (!biometricResult.success) {
        throw new Error('生物识别验证失败')
      }

      // 使用生物识别密钥作为主密钥
      return this.encryptPassword(password, biometricKey)
    } catch (error) {
      console.error('生物识别加密密码失败:', error)
      throw new Error('生物识别加密密码失败: ' + (error instanceof Error ? error.message : '未知错误'))
    }
  }

  /**
   * 使用生物识别解密密码
   */
  public async decryptPasswordWithBiometric(
    encryptedData: EncryptedPasswordData,
    biometricKey: string
  ): Promise<string> {
    try {
      // 验证生物识别
      const biometricResult = await biometricService.authenticate({
        reason: '使用生物识别验证以解密密码'
      })

      if (!biometricResult.success) {
        throw new Error('生物识别验证失败')
      }

      // 使用生物识别密钥作为主密钥
      return this.decryptPassword(encryptedData, biometricKey)
    } catch (error) {
      console.error('生物识别解密密码失败:', error)
      throw new Error('生物识别解密密码失败: ' + (error instanceof Error ? error.message : '未知错误'))
    }
  }

  /**
   * 安全清除内存中的敏感数据
   */
  public secureClear(data: string | Uint8Array | ArrayBuffer): void {
    if (typeof data === 'string') {
      // 字符串清零（JavaScript中的字符串是不可变的，这里主要是为了接口一致性）
      return
    }

    if (data instanceof Uint8Array) {
      data.fill(0)
    } else if (data instanceof ArrayBuffer) {
      new Uint8Array(data).fill(0)
    }
  }

  /**
   * 检查加密数据是否过期
   */
  public isExpired(encryptedData: EncryptedPasswordData, maxAge: number = 30 * 24 * 60 * 60 * 1000): boolean {
    const now = Date.now()
    const age = now - encryptedData.createdAt
    return age > maxAge
  }

  /**
   * 获取当前设备ID
   */
  public getDeviceId(): string {
    return this.deviceId
  }
}

// 创建全局实例
export const securePasswordManager = new SecurePasswordManager()

// 便捷方法
export const encryptPassword = (password: string, masterKey: string, deviceId?: string) =>
  securePasswordManager.encryptPassword(password, masterKey, deviceId)
export const decryptPassword = (encryptedData: EncryptedPasswordData, masterKey: string) =>
  securePasswordManager.decryptPassword(encryptedData, masterKey)
export const encryptPasswordWithBiometric = (password: string, biometricKey: string) =>
  securePasswordManager.encryptPasswordWithBiometric(password, biometricKey)
export const decryptPasswordWithBiometric = (encryptedData: EncryptedPasswordData, biometricKey: string) =>
  securePasswordManager.decryptPasswordWithBiometric(encryptedData, biometricKey)