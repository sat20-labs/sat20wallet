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

/**
 * Persisted device-local metadata. The raw AccountSecret and the
 * DeviceWrappingKey must never be serialized into this object.
 */
export interface DeviceKeySlot {
  version: 1
  accountId: string
  deviceId: string
  mode: SecureStorageMode
  wrappedAccountSecret: string
  metadata: DeviceKeySlotMetadata
}

export interface SealAccountSecretOptions {
  requireUserPresence?: boolean
}

export interface UnsealAccountSecretOptions {
  requireUserPresence?: boolean
  reason?: string
}

/**
 * Platform boundary used by account recovery onboarding.
 *
 * Native implementations delegate key operations to Android Keystore or the
 * iOS Keychain. Browser implementations use WebAuthn PRF when available and an
 * Argon2id-wrapped random device key as an explicit fallback.
 */
export interface SecureAccountStorage {
  capabilities(): Promise<SecureStorageCapabilities>

  seal(
    accountId: string,
    accountSecret: Uint8Array,
    options?: SealAccountSecretOptions,
  ): Promise<DeviceKeySlot>

  unseal(
    accountId: string,
    options?: UnsealAccountSecretOptions,
  ): Promise<Uint8Array>

  remove(accountId: string): Promise<void>
}

export class SecureAccountStorageUnavailableError extends Error {
  constructor(message = 'Secure account storage is unavailable on this device') {
    super(message)
    this.name = 'SecureAccountStorageUnavailableError'
  }
}

export const assertSecureStorageAccountId = (accountId: string): void => {
  if (!/^[0-9a-f]{64}$/.test(accountId)) {
    throw new Error('accountId must be a lowercase SHA-256 hex string')
  }
}
