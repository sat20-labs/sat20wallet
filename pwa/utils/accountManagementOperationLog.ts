import {
  beginNamedPwaOperation,
  finishPwaOperation,
  safeAccountOperationParameters,
  type PwaOperationContext,
} from '@/utils/pwaOperationLog'

type AccountOperationSpec = {
  action: string
  title: string
  summary: string
  parameters?: (payload: any) => Record<string, string>
}

const accountOperationSpecs: Record<string, AccountOperationSpec> = {
  fundAutopay: {
    action: 'account_autopay_fund',
    title: 'Fund account AUTOPAY',
    summary: 'Funding AUTOPAY for paid account-management storage',
  },
  confirmStorage: {
    action: 'account_storage_confirm',
    title: 'Configure account storage',
    summary: 'Confirming account-management storage settings',
    parameters: payload => safeAccountOperationParameters({
      storage_mode: payload?.option_id,
      record_count: payload?.record_count,
    }),
  },
  createRecovery: {
    action: 'account_recovery_create',
    title: 'Create account recovery',
    summary: 'Creating the self-custody account recovery package',
  },
  acceptGuardianSetup: {
    action: 'guardian_setup_accept',
    title: 'Accept guardian setup',
    summary: 'Accepting a guardian recovery setup request',
  },
  rehearse: {
    action: 'account_recovery_rehearse',
    title: 'Test account recovery',
    summary: 'Running an account recovery rehearsal',
  },
  recoverKnowledge: {
    action: 'account_recovery_knowledge',
    title: 'Verify recovery knowledge',
    summary: 'Verifying account recovery knowledge locally',
  },
  setUserShare: {
    action: 'account_recovery_share',
    title: 'Provide recovery share',
    summary: 'Providing the local recovery share for account recovery',
  },
  createGuardianRequest: {
    action: 'guardian_recovery_request',
    title: 'Create guardian recovery request',
    summary: 'Creating a request for a guardian recovery share',
  },
  createGuardianResponse: {
    action: 'guardian_recovery_response',
    title: 'Approve guardian recovery',
    summary: 'Creating a guardian response to a recovery request',
  },
  consumeGuardianResponse: {
    action: 'guardian_recovery_consume',
    title: 'Use guardian recovery response',
    summary: 'Applying a guardian response to the recovery session',
  },
  commitRecovery: {
    action: 'account_recovery_commit',
    title: 'Restore managed account',
    summary: 'Restoring wallets and accounts from the validated recovery package',
  },
  abortSession: {
    action: 'account_recovery_abort',
    title: 'Cancel account recovery',
    summary: 'Cancelling the active account recovery session',
  },
}

export async function beginAccountManagementOperation(
  methodName: string,
  payload: unknown,
): Promise<PwaOperationContext | null> {
  const spec = accountOperationSpecs[methodName]
  if (!spec) return null

  let parameters: Record<string, string> | undefined
  try {
    parameters = spec.parameters?.(payload)
  } catch (error) {
    console.warn(`Unable to prepare account operation-log parameters for ${methodName}:`, error)
  }

  return beginNamedPwaOperation({
    category: 'account',
    action: spec.action,
    title: spec.title,
    summary: spec.summary,
    parameters,
  })
}

export { finishPwaOperation }
