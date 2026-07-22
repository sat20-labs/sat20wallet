export interface AccountManagementState {
  version: 1
  status: 'active-paid' | 'active-temporary'
  accountId: string
  packageId: string
  recoveryMode: '2of2' | '2of3'
  storageMode: 'paid' | 'temporary'
  storageDescription?: string
  estimatedExpiryTime?: number
  guardianStatus?: 'none' | 'stored' | 'temporary'
  publicLocator: string
  lastRehearsalAt: number
}

const KEY = 'sat20-account-management-state-v1'

export const loadAccountManagementState = (): AccountManagementState | null => {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return null
    const value = JSON.parse(raw) as AccountManagementState
    return value?.version === 1 ? value : null
  } catch {
    return null
  }
}

export const saveAccountManagementState = (value: AccountManagementState): void => {
  localStorage.setItem(KEY, JSON.stringify(value))
}

export const clearAccountManagementState = (): void => {
  localStorage.removeItem(KEY)
}
