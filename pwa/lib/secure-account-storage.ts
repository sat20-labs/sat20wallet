export type SecureStorageMode = 'native-keystore' | 'webauthn-prf' | 'pin-wrapped'

export interface SecureStorageCapabilities {
  mode: SecureStorageMode
  userPresence: boolean
  hardwareBacked: boolean
  biometricAvailable: boolean
  recoverableByAccountRecovery: true
}

export interface DeviceKeySlotMetadata {
  [key: string]: string | number | boolean
}

export interface DeviceKeySlot {
  version: 1
  accountId: string
  deviceId: string
  mode: SecureStorageMode
  wrappedAccountSecret: string
  metadata: DeviceKeySlotMetadata
}

export interface SealAccountSecretOptions { requireUserPresence?: boolean }
export interface UnsealAccountSecretOptions { requireUserPresence?: boolean; reason?: string }

/**
 * Platform boundary invoked by the Go/WASM account recovery flow.
 * IndexedDB may persist DeviceKeySlot but never the raw AccountSecret or
 * DeviceWrappingKey.
 */
export interface SecureAccountStorage {
  capabilities(): Promise<SecureStorageCapabilities>
  seal(accountId: string, accountSecret: Uint8Array, options?: SealAccountSecretOptions): Promise<DeviceKeySlot>
  unseal(accountId: string, options?: UnsealAccountSecretOptions): Promise<Uint8Array>
  remove(accountId: string): Promise<void>
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
