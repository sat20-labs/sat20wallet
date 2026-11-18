import accountManagementSDK from '@/utils/accountManagement'

export type PendingBackupRisk = 'unconfirmed' | 'notConfigured' | 'unknown' | null

// This checks only the current device. Another device's local changes are not observable here.
export const pendingBackupRisk = async (deletingWallet = false): Promise<PendingBackupRisk> => {
  try {
    const { backup_risk } = await accountManagementSDK.getAccountView()
    if (backup_risk === 'notConfigured' && !deletingWallet) return null
    return backup_risk || null
  } catch {
    return 'unknown'
  }
}

export const confirmPendingBackupRisk = async (
  translate: (key: string) => string,
  deletingWallet = false,
): Promise<boolean> => {
  const risk = await pendingBackupRisk(deletingWallet)
  if (!risk) return true
  const key = risk === 'unconfirmed' ? 'backupRiskUnconfirmed'
    : risk === 'notConfigured' ? 'backupRiskNotConfigured' : 'backupRiskUnknown'
  return window.confirm(translate(`accountManagement.${key}`))
}
