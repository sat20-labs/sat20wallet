export interface AccountStorageOption {
  id: string
  mode: 'temporary' | 'paid'
  available: boolean
  title: string
  description: string
  warnings?: string[]
  ttl_blocks?: number
  estimated_expiry_height?: number
  fee_asset?: string
  estimated_cost?: string
  estimated_annual_cost?: string
  minimum_retention?: string
  recommended_retention?: string
  record_count?: number
  default_record_count?: number
  full_record_fee_per_block?: string
  minimum_amount_per_block?: string
  amount_per_block?: string
  contract_address?: string
}

export interface AccountWalletMetadataInput {
  id: number
  name: string
  sub_accounts: Record<number, string>
}

export interface AccountQuestionInput {
  id: string
  prompt: string
  answer: string
  confirmation: string
  case_sensitive?: boolean
  ignore_punctuation?: boolean
}

export interface AccountSummaryWallet {
  name: string
  account_count: number
  dids: string[]
}

export interface AccountRecoverySummary {
  account_id: string
  package_id: string
  recovery_mode: '2of2' | '2of3'
  wallets: AccountSummaryWallet[]
}

export interface RestoredWallet {
  id: number
  name: string
  accounts: Array<{
    index: number
    did: string
    address: string
    pub_key: string
  }>
}

type SDKResponse<T> = { code: number; msg: string; data?: T }

class AccountManagementSDK {
  private async request<T>(methodName: string, payload: unknown = {}): Promise<T> {
    const api = (globalThis as any).sat20account_wasm
    const method = api?.[methodName]
    if (typeof method !== 'function') throw new Error('账户管理 SDK 尚未加载')

    // This path deliberately does not log payloads or results. Requests can
    // contain wallet passwords, private-knowledge answers and recovery shares.
    let response: SDKResponse<T>
    try {
      response = await method(JSON.stringify(payload))
    } catch (error: any) {
      throw new Error(error?.message || '账户管理调用失败')
    }
    if (!response || response.code !== 0) {
      throw new Error(response?.msg || '账户管理调用失败')
    }
    return response.data as T
  }

  preflight(password: string, wallets: AccountWalletMetadataInput[]) {
    return this.request<any>('preflight', { password, wallets })
  }

  status() {
    return this.request<{
      active: boolean
      recovery_configured: boolean
      managed_data_revision?: number
      managed_data_dirty?: boolean
      account_id?: string
      package_id?: string
      recovery_mode?: '2of2' | '2of3'
      storage_mode?: 'paid' | 'temporary'
      public_locator?: string
      root_wallet_id?: number
      state_seq?: number
      pending_changes?: number
      last_rehearsal_at?: number
    }>('status')
  }

  getStorageOptions() {
    return this.request<{ options: AccountStorageOption[] }>('getStorageOptions')
  }

  confirmStorage(optionId: string, recordCount?: number) {
    return this.request<any>('confirmStorage', {
      option_id: optionId,
      record_count: optionId === 'paid' ? recordCount : undefined,
    })
  }

  guardianIdentity(password: string) {
    return this.request<any>('guardianIdentity', { password })
  }

  createRecovery(request: Record<string, unknown>) {
    return this.request<any>('createRecovery', request)
  }

  acceptGuardianSetup(password: string, setupPayload: string, storageAuthorizationId: string) {
    return this.request<{ receipt: string }>('acceptGuardianSetup', {
      password,
      setup_payload: setupPayload,
      storage_authorization_id: storageAuthorizationId,
    })
  }

  checkGuardianSetup(sessionId: string, receipt: string) {
    return this.request<any>('checkGuardianSetup', { session_id: sessionId, receipt })
  }

  rehearse(sessionId: string, answers: Array<{ question_id: string; answer: string }>, userShare = '', password = '') {
    return this.request<any>('rehearse', { session_id: sessionId, answers, user_share: userShare, password })
  }

  loadRecovery(locator: string) {
    return this.request<any>('loadRecovery', { locator })
  }

  recoverKnowledge(sessionId: string, answers: Array<{ question_id: string; answer: string }>) {
    return this.request<any>('recoverKnowledge', { session_id: sessionId, answers })
  }

  setUserShare(sessionId: string, userShare: string) {
    return this.request<any>('setUserShare', { session_id: sessionId, user_share: userShare })
  }

  createGuardianRequest(sessionId: string) {
    return this.request<{ request: string }>('createGuardianRequest', { session_id: sessionId })
  }

  createGuardianResponse(password: string, request: string) {
    return this.request<{ response: string }>('createGuardianResponse', { password, request })
  }

  consumeGuardianResponse(sessionId: string, response: string) {
    return this.request<any>('consumeGuardianResponse', { session_id: sessionId, response })
  }

  previewRecovery(sessionId: string) {
    return this.request<{ summary: AccountRecoverySummary }>('previewRecovery', { session_id: sessionId })
  }

  commitRecovery(sessionId: string, password: string) {
    return this.request<{ wallets: RestoredWallet[] }>('commitRecovery', { session_id: sessionId, password })
  }

  abortSession(sessionId: string) {
    return this.request<any>('abortSession', { session_id: sessionId })
  }
}

export default new AccountManagementSDK()
