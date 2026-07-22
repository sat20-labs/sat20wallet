export type SecureStorageBackend = 'tee' | 'native-keystore' | 'sdk-vault'

export interface SecureStorageCapabilities {
  backend: SecureStorageBackend
  userPresence: boolean
  hardwareBacked: boolean
  biometricAvailable: boolean
  exportsAccountSecret: false
}

/**
 * Opaque reference returned by the Go SDK or a native TEE/keystore adapter.
 * PWA business code cannot use this handle to export AccountSecret.
 */
export interface DeviceAccountHandle {
  version: 1
  accountId: string
  deviceId: string
  backend: SecureStorageBackend
  handle: string
}

export interface CommitAccountStorageOptions {
  requireUserPresence?: boolean
}

/**
 * The PWA passes only a short-lived SDK session ID. AccountSecret,
 * WrappedAccountSecret and DeviceWrappingKey are never returned to page code
 * or persisted by the PWA IndexedDB/localStorage layer.
 */
export interface SecureAccountStorage {
  capabilities(): Promise<SecureStorageCapabilities>
  commit(sessionId: string, options?: CommitAccountStorageOptions): Promise<DeviceAccountHandle>
  open(handle: DeviceAccountHandle, reason?: string): Promise<string>
  remove(handle: DeviceAccountHandle): Promise<void>
}

export class SecureAccountStorageUnavailableError extends Error {
  constructor(message = 'Secure account storage is unavailable on this device') {
    super(message)
    this.name = 'SecureAccountStorageUnavailableError'
  }
}

export const assertSecureStorageAccountId = (accountId: string): void => {
  if (!/^[0-9a-f]{64}$/.test(accountId)) throw new Error('accountId must be a lowercase 32-byte x-only public key hex string')
}
