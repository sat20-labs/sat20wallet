/**
 * WebAuthn PRF based biometric unlock.
 *
 * The wallet password hash is never stored directly. Enabling biometric unlock
 * wraps the current wallet password hash with an AES-GCM key derived from the
 * platform authenticator PRF output. Unlocking requires user verification on
 * the same authenticator to reproduce that PRF output and decrypt the hash.
 */

import { Storage } from '@/lib/storage-adapter'
import { biometricService, getWebAuthnOriginError } from './biometric'
import { isDebugEnabled } from './debug'

export interface BiometricCredential {
  id: string
  name: string
  createdAt: number
  lastUsed?: number
  isActive: boolean
  transports?: AuthenticatorTransport[]
  prfMode?: WebAuthnPrfMode
  salt: string
  iv: string
  encryptedPassword: string
}

export interface CredentialVerificationResult {
  valid: boolean
  credential?: BiometricCredential
  password?: string
  error?: string
}

export const CREDENTIALS_STORAGE_KEY = 'local:wallet_biometric_credentials'
const PRF_UNAVAILABLE_ERROR = '当前浏览器或系统生物识别凭据不支持 WebAuthn PRF，无法安全保存钱包解锁密码。请继续使用密码解锁。'
const WEBAUTHN_OPERATION_TIMEOUT_MS = 60000

type WebAuthnPrfResults = {
  prf?: {
    enabled?: boolean
    results?: {
      first?: ArrayBuffer
      second?: ArrayBuffer
    }
  }
}

type WebAuthnPrfMode = 'evalByCredential' | 'eval'

const KNOWN_AUTHENTICATOR_TRANSPORTS = new Set<string>([
  'ble',
  'hybrid',
  'internal',
  'nfc',
  'usb',
])

const textEncoder = new TextEncoder()
const textDecoder = new TextDecoder()

const randomBytes = (length: number): Uint8Array => {
  const bytes = new Uint8Array(length)
  crypto.getRandomValues(bytes)
  return bytes
}

const withWebAuthnTimeout = async <T>(
  createPromise: (signal: AbortSignal) => Promise<T>,
  label: string,
  ms = WEBAUTHN_OPERATION_TIMEOUT_MS
): Promise<T> => {
  const abortController = new AbortController()
  let timeoutId: ReturnType<typeof setTimeout> | undefined

  try {
    return await Promise.race([
      createPromise(abortController.signal),
      new Promise<T>((_, reject) => {
        timeoutId = setTimeout(() => {
          abortController.abort()
          reject(new Error(`${label} timed out after ${ms}ms`))
        }, ms)
      }),
    ])
  } finally {
    if (timeoutId) {
      clearTimeout(timeoutId)
    }
  }
}

const normalizeWebAuthnError = (error: unknown): Error => {
  const message = error instanceof Error ? error.message : String(error)
  if (/TLS certificate|certificate error|secure context|not allowed/i.test(message)) {
    return new Error('生物识别需要没有证书错误的安全 HTTPS 环境。请使用有效证书的 HTTPS 地址，或在本地测试时使用 Chrome 已信任的安全 origin。')
  }
  if (/credential manager|unknown error occurred while talking to the credential manager/i.test(message)) {
    return new Error('Android Credential Manager 无法创建通行密钥。请确认 Chrome、Google Play services 和系统已更新，并已启用屏幕锁/指纹；本次将继续使用密码解锁。')
  }
  return error instanceof Error ? error : new Error(message || 'WebAuthn 操作失败')
}

const toBase64Url = (value: ArrayBuffer | Uint8Array): string => {
  const bytes = value instanceof Uint8Array ? value : new Uint8Array(value)
  let binary = ''
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte)
  })
  return btoa(binary)
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/g, '')
}

const fromBase64Url = (value: string): Uint8Array => {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64.padEnd(base64.length + (4 - base64.length % 4) % 4, '=')
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes
}

const describePrfResults = (credential: PublicKeyCredential) => {
  const extensionResults = credential.getClientExtensionResults() as WebAuthnPrfResults
  const prf = extensionResults.prf

  return {
    hasPrf: Boolean(prf),
    enabled: prf?.enabled,
    hasFirst: Boolean(prf?.results?.first),
    hasSecond: Boolean(prf?.results?.second),
  }
}

const getWebAuthnRuntimeInfo = () => ({
  origin: location.origin,
  hostname: location.hostname,
  displayMode: window.matchMedia?.('(display-mode: standalone)').matches
    ? 'standalone'
    : window.matchMedia?.('(display-mode: fullscreen)').matches
      ? 'fullscreen'
      : window.matchMedia?.('(display-mode: minimal-ui)').matches
        ? 'minimal-ui'
        : 'browser',
  userAgent: navigator.userAgent,
})

const debugWebAuthn = (phase: string, data: Record<string, unknown>) => {
  if (!isDebugEnabled()) return
  const payload = {
    phase,
    ...getWebAuthnRuntimeInfo(),
    ...data,
  }

  ;(window as Window & { __SAT20_WEBAUTHN_DEBUG__?: unknown[] }).__SAT20_WEBAUTHN_DEBUG__ = [
    ...((window as Window & { __SAT20_WEBAUTHN_DEBUG__?: unknown[] }).__SAT20_WEBAUTHN_DEBUG__ ?? []),
    payload,
  ]
  ;(window as Window & { __SAT20_WEBAUTHN_SUMMARY__?: () => unknown }).__SAT20_WEBAUTHN_SUMMARY__ = () => ({
    support: (window as Window & { __SAT20_WEBAUTHN_SUPPORT__?: unknown[] }).__SAT20_WEBAUTHN_SUPPORT__ ?? [],
    debug: (window as Window & { __SAT20_WEBAUTHN_DEBUG__?: unknown[] }).__SAT20_WEBAUTHN_DEBUG__ ?? [],
  })
  console.warn(`[SAT20 WebAuthn] ${JSON.stringify(payload)}`)
}

const isRecoverablePrfModeError = (error: unknown): boolean => {
  const name = error instanceof DOMException ? error.name : error instanceof Error ? error.name : ''
  const message = error instanceof Error ? error.message : String(error)

  return name === 'NotSupportedError' ||
    name === 'SyntaxError' ||
    /prf|extension|evalByCredential|not supported|unsupported/i.test(message)
}

const isApplePlatform = (): boolean => /Macintosh|Mac OS X|iPhone|iPad|iPod/i.test(navigator.userAgent)

const getWebAuthnPlatformPath = (): 'apple-tested' | 'passkeyprf' => {
  return isApplePlatform() ? 'apple-tested' : 'passkeyprf'
}

const deriveAesKey = async (prfOutput: ArrayBuffer): Promise<CryptoKey> => {
  return crypto.subtle.importKey(
    'raw',
    prfOutput,
    { name: 'AES-GCM' },
    false,
    ['encrypt', 'decrypt']
  )
}

const encryptPasswordWithKey = async (
  password: string,
  key: CryptoKey
): Promise<{ iv: string; encryptedPassword: string }> => {
  const iv = randomBytes(12)
  const encrypted = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv },
    key,
    textEncoder.encode(password)
  )

  return {
    iv: toBase64Url(iv),
    encryptedPassword: toBase64Url(encrypted),
  }
}

const decryptPasswordWithKey = async (
  encryptedPassword: string,
  iv: string,
  key: CryptoKey
): Promise<string> => {
  const decrypted = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: fromBase64Url(iv) },
    key,
    fromBase64Url(encryptedPassword)
  )

  return textDecoder.decode(decrypted)
}

const encryptPasswordWithPrf = async (
  password: string,
  prfOutput: ArrayBuffer
): Promise<{ iv: string; encryptedPassword: string }> => {
  return encryptPasswordWithKey(password, await deriveAesKey(prfOutput))
}

const decryptPasswordWithPrf = async (
  encryptedPassword: string,
  iv: string,
  prfOutput: ArrayBuffer
): Promise<string> => {
  return decryptPasswordWithKey(encryptedPassword, iv, await deriveAesKey(prfOutput))
}

export class BiometricCredentialManager {
  private credentials: Map<string, BiometricCredential> = new Map()
  private initialized = false

  private async ensureLoaded(): Promise<void> {
    if (this.initialized) return

    const { value } = await Storage.get({ key: CREDENTIALS_STORAGE_KEY })
    if (value) {
      const credentialsData = JSON.parse(value) as Record<string, BiometricCredential>
      this.credentials = new Map(Object.entries(credentialsData))
    }
    this.initialized = true
  }

  private async saveCredentials(): Promise<void> {
    const credentialsObject = Object.fromEntries(this.credentials)
    await Storage.set({
      key: CREDENTIALS_STORAGE_KEY,
      value: JSON.stringify(credentialsObject),
    })
  }

  private getRpName(): string {
    return 'SAT20 Wallet'
  }

  private getRpId(): string {
    return location.hostname
  }

  private getUserName(): string {
    return `sat20-wallet-${location.origin}`
  }

  private ensureWebAuthnSecureContext(): void {
    const originError = getWebAuthnOriginError()
    if (originError) {
      throw new Error(originError)
    }
  }

  private readPrfOutput(credential: PublicKeyCredential): ArrayBuffer | undefined {
    const extensionResults = credential.getClientExtensionResults() as WebAuthnPrfResults
    const prf = extensionResults.prf

    return prf?.results?.first
  }

  private readPrfEnabled(credential: PublicKeyCredential): boolean {
    const extensionResults = credential.getClientExtensionResults() as WebAuthnPrfResults
    return extensionResults.prf?.enabled === true
  }

  private async createWebAuthnCredential(salt: Uint8Array): Promise<{
    credentialId: string
    prfOutput?: ArrayBuffer
    transports?: AuthenticatorTransport[]
  }> {
    this.ensureWebAuthnSecureContext()

    let credential: PublicKeyCredential | null
    try {
      const platformPath = getWebAuthnPlatformPath()
      const useApplePlatformPath = platformPath === 'apple-tested'

      debugWebAuthn('create-start', {
        rpId: this.getRpId(),
        platformPath,
        residentKey: useApplePlatformPath ? 'discouraged' : 'required',
        userVerification: 'required',
        requestCreatePrf: useApplePlatformPath ? 'eval' : 'capability',
      })

      const extensions = useApplePlatformPath
        ? {
            credProps: true,
            prf: {
              eval: {
                first: salt,
              },
            },
          }
        : {
            credProps: true,
            prf: {},
          }

      const authenticatorSelection = useApplePlatformPath
        ? {
            authenticatorAttachment: 'platform',
            residentKey: 'discouraged',
            requireResidentKey: false,
            userVerification: 'required',
          }
        : {
            authenticatorAttachment: 'platform',
            residentKey: 'required',
            userVerification: 'required',
          }

      credential = await withWebAuthnTimeout((signal) => navigator.credentials.create({
        publicKey: {
          challenge: randomBytes(32),
          rp: {
            id: this.getRpId(),
            name: this.getRpName(),
          },
          user: {
            id: randomBytes(16),
            name: this.getUserName(),
            displayName: this.getRpName(),
          },
          pubKeyCredParams: [
            { type: 'public-key', alg: -7 },
            { type: 'public-key', alg: -257 },
          ],
          authenticatorSelection,
          attestation: 'none',
          timeout: WEBAUTHN_OPERATION_TIMEOUT_MS,
          extensions: extensions as any,
        },
        signal,
      } as any) as Promise<PublicKeyCredential | null>, 'WebAuthn credential creation')
    } catch (error) {
      throw normalizeWebAuthnError(error)
    }

    if (!credential) {
      throw new Error('未创建 WebAuthn 凭据')
    }

    const response = credential.response as AuthenticatorAttestationResponse & {
      getTransports?: () => AuthenticatorTransport[]
    }
    const transports = response.getTransports
      ? response.getTransports()
        .filter((transport): transport is AuthenticatorTransport => KNOWN_AUTHENTICATOR_TRANSPORTS.has(transport))
      : undefined
    debugWebAuthn('create', {
      credentialId: credential.id,
      transports,
      prf: describePrfResults(credential),
    })

    const prfOutput = this.readPrfOutput(credential)
    if (!isApplePlatform() && !this.readPrfEnabled(credential)) {
      throw new Error(PRF_UNAVAILABLE_ERROR)
    }

    return {
      credentialId: toBase64Url(credential.rawId),
      prfOutput,
      transports,
    }
  }

  private async tryGetPrfOutput(
    credential: BiometricCredential,
    mode: WebAuthnPrfMode
  ): Promise<ArrayBuffer | undefined> {
    const salt = fromBase64Url(credential.salt)
    const prf = mode === 'evalByCredential'
      ? {
          evalByCredential: {
            [credential.id]: {
              first: salt,
            },
          },
        }
      : {
          eval: {
            first: salt,
          },
        }

    this.ensureWebAuthnSecureContext()

    let assertion: PublicKeyCredential | null
    try {
      assertion = await withWebAuthnTimeout((signal) => navigator.credentials.get({
        publicKey: {
          challenge: randomBytes(32),
          rpId: this.getRpId(),
          allowCredentials: [
            {
              id: fromBase64Url(credential.id),
              type: 'public-key',
            },
          ],
          userVerification: 'required',
          timeout: WEBAUTHN_OPERATION_TIMEOUT_MS,
          extensions: {
            prf,
          } as any,
        },
        signal,
      } as any) as Promise<PublicKeyCredential | null>, 'WebAuthn PRF verification')
    } catch (error) {
      throw normalizeWebAuthnError(error)
    }

    if (!assertion) {
      throw new Error('未完成 WebAuthn 验证')
    }

    debugWebAuthn('get', {
      mode,
      credentialId: assertion.id,
      allowedCredentialId: credential.id,
      transports: credential.transports,
      prf: describePrfResults(assertion),
    })

    return this.readPrfOutput(assertion)
  }

  private async getPrfOutput(credential: BiometricCredential): Promise<{
    output: ArrayBuffer
    mode: WebAuthnPrfMode
  }> {
    const modes: WebAuthnPrfMode[] = credential.prfMode
      ? [credential.prfMode, credential.prfMode === 'evalByCredential' ? 'eval' : 'evalByCredential']
      : isApplePlatform() ? ['evalByCredential', 'eval'] : ['eval', 'evalByCredential']

    for (const mode of modes) {
      try {
        const output = await this.tryGetPrfOutput(credential, mode)
        if (output) {
          return { output, mode }
        }
      } catch (error) {
        debugWebAuthn('get-prf-mode-failed', {
          mode,
          credentialId: credential.id,
          error: error instanceof Error ? error.message : String(error),
        })

        if (!isRecoverablePrfModeError(error)) {
          throw error
        }
      }
    }

    debugWebAuthn('get-prf-unavailable', {
      credentialId: credential.id,
      modes,
    })
    throw new Error(PRF_UNAVAILABLE_ERROR)
  }

  public async checkBiometricSupport(): Promise<{
    supported: boolean
    available: boolean
    biometryType?: string
    error?: string
  }> {
    return biometricService.checkBiometricSupport()
  }

  public async createCredential(
    hashedPassword: string,
    name = 'SAT20 钱包生物识别凭据'
  ): Promise<{ success: boolean; credentialId?: string; error?: string }> {
    try {
      await this.ensureLoaded()

      const salt = randomBytes(32)
      const createdCredential = await this.createWebAuthnCredential(salt)
      const credentialId = createdCredential.credentialId
      const credential: BiometricCredential = {
        id: credentialId,
        name,
        createdAt: Date.now(),
        isActive: true,
        salt: toBase64Url(salt),
        iv: '',
        encryptedPassword: '',
        transports: createdCredential.transports,
      }

      const prfResult = createdCredential.prfOutput
        ? { output: createdCredential.prfOutput, mode: 'eval' as WebAuthnPrfMode }
        : await this.getPrfOutput(credential)

      credential.prfMode = prfResult.mode
      const encrypted = await encryptPasswordWithPrf(hashedPassword, prfResult.output)

      credential.iv = encrypted.iv
      credential.encryptedPassword = encrypted.encryptedPassword

      this.credentials.clear()
      this.credentials.set(credentialId, credential)
      await this.saveCredentials()

      return { success: true, credentialId }
    } catch (error) {
      console.error('创建生物识别凭据失败:', error)
      return {
        success: false,
        error: error instanceof Error ? error.message : '创建凭据失败',
      }
    }
  }

  public async verifyCredential(): Promise<CredentialVerificationResult> {
    try {
      await this.ensureLoaded()

      const credential = this.getActiveCredentialsSync()[0]
      if (!credential) {
        return { valid: false, error: '未找到活跃的生物识别凭据' }
      }

      const prfResult = await this.getPrfOutput(credential)
      const password = await decryptPasswordWithPrf(
        credential.encryptedPassword,
        credential.iv,
        prfResult.output
      )
      credential.prfMode = prfResult.mode

      credential.lastUsed = Date.now()
      await this.saveCredentials()

      return { valid: true, credential, password }
    } catch (error) {
      console.error('验证生物识别凭据失败:', error)
      const message = error instanceof Error ? error.message : '验证凭据失败'
      const shouldRecreateCredential = message.includes('通行密钥') || message.includes('credential') || message.includes('Credential')

      return {
        valid: false,
        error: shouldRecreateCredential
          ? '未找到可用的本机生物识别凭据，请使用密码解锁后，在安全设置中关闭并重新开启生物识别'
          : message,
      }
    }
  }

  private getActiveCredentialsSync(): BiometricCredential[] {
    return Array.from(this.credentials.values()).filter((credential) => credential.isActive)
  }

  public async getCredentials(): Promise<BiometricCredential[]> {
    await this.ensureLoaded()
    return Array.from(this.credentials.values())
  }

  public async getActiveCredentials(): Promise<BiometricCredential[]> {
    await this.ensureLoaded()
    return this.getActiveCredentialsSync()
  }

  public async deleteCredential(credentialId: string): Promise<{ success: boolean; error?: string }> {
    try {
      await this.ensureLoaded()
      if (!this.credentials.has(credentialId)) {
        return { success: false, error: '凭据不存在' }
      }

      this.credentials.delete(credentialId)
      await this.saveCredentials()
      return { success: true }
    } catch (error) {
      console.error('删除凭据失败:', error)
      return {
        success: false,
        error: error instanceof Error ? error.message : '删除凭据失败',
      }
    }
  }

  public async disableCredential(credentialId: string): Promise<{ success: boolean; error?: string }> {
    try {
      await this.ensureLoaded()
      const credential = this.credentials.get(credentialId)
      if (!credential) {
        return { success: false, error: '凭据不存在' }
      }

      credential.isActive = false
      await this.saveCredentials()
      return { success: true }
    } catch (error) {
      console.error('禁用凭据失败:', error)
      return {
        success: false,
        error: error instanceof Error ? error.message : '禁用凭据失败',
      }
    }
  }

  public async clearAllCredentials(): Promise<{ success: boolean; error?: string }> {
    try {
      await this.ensureLoaded()
      this.credentials.clear()
      await Storage.remove({ key: CREDENTIALS_STORAGE_KEY })
      return { success: true }
    } catch (error) {
      console.error('清除所有凭据失败:', error)
      return {
        success: false,
        error: error instanceof Error ? error.message : '清除凭据失败',
      }
    }
  }

  public async hasActiveCredentials(): Promise<boolean> {
    return (await this.getActiveCredentials()).length > 0
  }

  public async getStats(): Promise<{
    total: number
    active: number
    inactive: number
    lastUsed?: number
  }> {
    const credentials = await this.getCredentials()
    const active = credentials.filter((credential) => credential.isActive)
    const inactive = credentials.filter((credential) => !credential.isActive)
    const lastUsed = Math.max(...credentials.map((credential) => credential.lastUsed || 0))

    return {
      total: credentials.length,
      active: active.length,
      inactive: inactive.length,
      lastUsed: lastUsed > 0 ? lastUsed : undefined,
    }
  }
}

export const biometricCredentialManager = new BiometricCredentialManager()

export const createBiometricCredential = (hashedPassword: string, name?: string) =>
  biometricCredentialManager.createCredential(hashedPassword, name)
export const verifyBiometricCredential = () =>
  biometricCredentialManager.verifyCredential()
export const getBiometricCredentials = () => biometricCredentialManager.getCredentials()
export const deleteBiometricCredential = (credentialId: string) =>
  biometricCredentialManager.deleteCredential(credentialId)
export const clearAllBiometricCredentials = () => biometricCredentialManager.clearAllCredentials()
