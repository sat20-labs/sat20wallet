import { beginVersionDispatch } from '@/utils/pwaVersionPolicy'
import { walletRequestSessionGuard } from '@/lib/walletSession'
import { beginAccountManagementOperation, finishPwaOperation } from '@/utils/accountManagementOperationLog'
import { noteWasmOperation } from '@/utils/wasmRuntimeDiagnostics'
import { preserveWasmError, wasmResponseError } from '@/utils/wasmError'

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
  fingerprint: string
  accounts: Array<{
    index: number
    did: string
    address: string
    pub_key: string
    account_id: string
  }>
}

export interface AccountManagementStatus {
  account_state: 'local' | 'locked' | 'syncing' | 'ready' | 'error'
  write_allowed: boolean
  backup_state: 'none' | 'pending' | 'uploading' | 'backed_up' | 'cleanup'
  modules?: AccountModuleStatus[]
  dkvs_status?: 'SYNCING' | 'READY' | 'OFFLINE_READY' | 'RESET_REQUIRED' | 'ERROR'
  dkvs_cache_at_ms?: number
  dkvs_cache_height?: number
  dkvs_write_allowed?: boolean
  dkvs_pending_requests?: number
  active: boolean
  recovery_configured: boolean
  local_only?: boolean
  discovery_status?: 'unknown' | 'inactive' | 'active-waiting-backup' | 'active'
  activation_pending?: boolean
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
  last_dkvs_sync_error_code?: string
  last_dkvs_sync_error?: string
  last_dkvs_sync_error_at?: number
}

export interface AccountModuleStatus {
  provider: string
  scope: string
  write_allowed: boolean
  backup_state: 'none' | 'pending' | 'uploading' | 'backed_up' | 'cleanup' | 'error' | 'conflict' | 'restoring'
  last_error?: string
  last_error_code?: string
}

export interface AccountDKVSInspection {
  scope: { account_id: string; wallet_subscopes?: string[] }
  snapshot: {
    id: string
    status: string
    endpoint_id?: string
    verified_at_ms?: number
    cache_height?: number
    covered_prefixes: string[]
    complete: boolean
    remote_current: boolean
    local_dirty: boolean
    pending_changes: number
    total_cached_count: number
  }
  records: Array<{
    key: string
    raw_hex?: string
    raw_hidden?: boolean
    raw_hidden_reason?: string
    record_hash: string
    seq: number
    decoded: {
      type: string
      status: string
      revision?: number
      data_revision?: number
      wallet_count?: number
      item_count?: number
      providers?: string[]
      subscopes?: string[]
    }
  }>
  next_cursor?: string
}

export interface AccountAutopayFundingStatus {
  funding_transaction_id?: string
  funding_error?: string
  funding_pending?: boolean
  funding_can_resume?: boolean
  required: boolean
  ready: boolean
  needs_funding: boolean
  can_fund: boolean
  reason: string
  message?: string
  contract_address?: string
  fee_asset?: string
  payer?: string
  current_block?: number
  last_pay_height?: number
  amount_per_block?: string
  balance?: string
  required_amount_per_block?: string
  effective_amount_per_block?: string
  recommended_funding_amount?: string
  recommended_funding_blocks?: number
  remaining_payment_blocks?: number
  estimated_expiry_height?: number
  estimated_funded_expiry_height?: number
}

export interface AccountAutopayFundingResult {
  transaction_id?: string
  contract_address: string
  fee_asset: string
  amount_per_block: string
  funding_amount: string
  funding_blocks: number
  reused: boolean
  pending?: boolean
}

export class AccountAutopayPendingError extends Error {
  code?: string
  constructor(message: string, public data: { transaction_id: string; pending: true }, code?: string) {
    super(message)
    this.code = code
  }
}

type SDKResponse<T> = { code: number; msg: string; data?: T; error_code?: string }

class AccountManagementSDK {
  private async request<T>(methodName: string, payload: unknown = {}): Promise<T> {
    const diagnosticMethod = `account.${methodName}`
    const checkSession = walletRequestSessionGuard(diagnosticMethod)
    noteWasmOperation(diagnosticMethod, 'request-start')
    const api = (globalThis as any).sat20account_wasm
    const method = api?.[methodName]
    if (typeof method !== 'function') throw new Error('账户管理 SDK 尚未加载')

    // Operation-log policy is deliberately scrubbed: passwords, mnemonics,
    // recovery answers/shares and setup payloads are never written to logs.
    const operation = await beginAccountManagementOperation(methodName, payload)
    let finishVersion: (() => void) | undefined
    try {
    let response: SDKResponse<T>
    try {
      checkSession()
      finishVersion = beginVersionDispatch(methodName, true)
      noteWasmOperation(diagnosticMethod, 'dispatch')
      response = await method(JSON.stringify(payload))
      try {
        checkSession()
      } catch (error) {
        const sessionId = (response?.data as any)?.session_id
        if (sessionId) await this.abortSession(sessionId).catch(() => undefined)
        throw error
      }
      noteWasmOperation(diagnosticMethod, 'returned')
    } catch (error: any) {
      noteWasmOperation(diagnosticMethod, 'failed')
      const requestError = preserveWasmError(error, '账户管理调用失败')
      await finishPwaOperation(operation, requestError)
      throw requestError
    }
    if (!response || response.code !== 0) {
      noteWasmOperation(diagnosticMethod, 'failed')
      const pending = response?.data as any
      const responseError = pending?.pending && pending?.transaction_id
        ? new AccountAutopayPendingError(response.msg, pending, response.error_code)
        : wasmResponseError(response?.msg || '账户管理调用失败', response?.error_code)
      await finishPwaOperation(operation, responseError)
      throw responseError
    }
    // The inspector returns raw record bytes. Never copy them into the
    // persistent PWA operation log or diagnostic payloads.
    await finishPwaOperation(operation, null, methodName === 'inspectDKVS'
      ? { inspected: true, record_count: (response.data as AccountDKVSInspection)?.records?.length ?? 0 }
      : response.data)
    checkSession()
    noteWasmOperation(diagnosticMethod, 'finished')
    return response.data as T
    } finally { finishVersion?.() }
  }

  preflight(password: string, wallets: AccountWalletMetadataInput[]) {
    return this.request<any>('preflight', { password, wallets })
  }

  status() {
    return this.request<AccountManagementStatus>('status')
  }

  retryDKVSSync() {
    return this.request<{ queued: boolean }>('retryDKVSSync')
  }

  inspectDKVS(cursor?: string, limit = 20) {
    return this.request<AccountDKVSInspection>('inspectDKVS', { cursor, limit })
  }

  getStorageOptions() {
    return this.request<{ options: AccountStorageOption[] }>('getStorageOptions')
  }

  autopayStatus() {
    return this.request<AccountAutopayFundingStatus>('autopayStatus')
  }

  fundAutopay(confirmed: AccountAutopayFundingStatus) {
    return this.request<AccountAutopayFundingResult>('fundAutopay', confirmed)
  }

  confirmStorage(optionId: string, recordCount?: number) {
    return this.request<any>('confirmStorage', {
      option_id: optionId,
      record_count: optionId === 'paid' ? recordCount : undefined,
    })
  }

  async resumePendingStorageAuthorization() {
    try {
      return await this.request<any>('resumeStorageAuthorization')
    } catch {
      return null
    }
  }

  cancelPendingStorageAuthorization() {
    return this.request<any>('cancelStorageAuthorization')
  }

  reusePaidStorage(recordCount?: number) {
    return this.request<any>('reusePaidStorage', {
      record_count: recordCount,
    })
  }

  guardianIdentity(password: string) {
    return this.request<any>('guardianIdentity', { password })
  }

  createRecovery(request: Record<string, unknown>) {
    return this.request<any>('createRecovery', request)
  }

  acceptGuardianSetup(password: string, setupPayload: string) {
    return this.request<{ receipt: string }>('acceptGuardianSetup', {
      password,
      setup_payload: setupPayload,
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
    return this.request<{ wallets: RestoredWallet[]; root_wallet_id: number; account_id: string }>('commitRecovery', { session_id: sessionId, password })
  }

  abortSession(sessionId: string) {
    return this.request<any>('abortSession', { session_id: sessionId })
  }
}

export default new AccountManagementSDK()

// Translate known SDK display messages at the UI boundary; retain unknown diagnostics.
const accountDisplayMessageKeys: Record<string, string> = {
  "账户管理 SDK 尚未加载": "accountManagement.ui.sdkNotLoaded",
  "账户管理调用失败": "accountManagement.ui.sdkRequestFailed",
  "由当前连接节点临时保存；到期后数据可能被删除。": "accountManagement.ui.temporaryDescription",
  "这不是长期账户备份。": "accountManagement.ui.temporaryWarning",
  "恢复时需要能够访问保存数据的同一节点。": "accountManagement.ui.sameNodeWarning",
  "当前连接节点不提供临时 DKVS 缓存。": "accountManagement.ui.temporaryUnavailable",
  "无法读取当前节点的 DKVS 配置。": "accountManagement.ui.configReadFailed",
  "当前网络尚未提供可用的 DKVS 付费保存配置。": "accountManagement.ui.paidUnavailable",
  "通过 AUTOPAY 按区块持续支付后，全网保存加密账户数据。": "accountManagement.ui.paidDescription",
  "余额不足、未继续支付时，付费同步会失败或停止；已付费记录不会自动降级为节点临时缓存。": "accountManagement.ui.paidWarning",
  "当前账户未使用 AUTOPAY 付费存储。": "accountManagement.ui.autopayNotRequired",
  "AUTOPAY 合约状态无效，无法充值。": "accountManagement.ui.invalidContract",
  "AUTOPAY 合约当前不可用，请检查网络或联系节点维护者。": "accountManagement.ui.contractUnavailable",
  "账户尚未配置 AUTOPAY，请充值以恢复付费同步。": "accountManagement.ui.delegateMissing",
  "账户 AUTOPAY 已停止，请充值以恢复付费同步。": "accountManagement.ui.delegateInactive",
  "账户 AUTOPAY 已过期，请充值以恢复付费同步。": "accountManagement.ui.paymentExpired",
  "AUTOPAY 支付参数无效，无法充值。": "accountManagement.ui.invalidRate",
  "账户 AUTOPAY 每区块费率不足，请充值以恢复付费同步。": "accountManagement.ui.rateInsufficient",
  "AUTOPAY 模块容量不足，请确认调整容量；增加余额不会自动增加模块数量。": "accountManagement.ui.blobCapacity",
  "AUTOPAY 余额状态无效，无法充值。": "accountManagement.ui.invalidBalance",
  "账户 AUTOPAY 余额不足，请充值以继续付费同步。": "accountManagement.ui.balanceInsufficient",
  "AUTOPAY 合约尚未恢复支付，请检查网络或联系节点维护者。": "accountManagement.ui.contractNotResumed",
  "AUTOPAY 支付正常。": "accountManagement.ui.paymentNormal",
  "原充值交易等待提交或合约确认；可查询或继续提交同一笔交易。": "accountManagement.ui.pendingOriginal",
  "原充值交易等待提交或合约确认；可查询或继续提交同一笔交易。 当前合约查询暂不可用。": "accountManagement.ui.pendingQueryUnavailable",
  "AUTOPAY 充值未提交": "accountManagement.ui.fundingNotSubmitted",
  "合约查询确认 AUTOPAY 充值已成功": "accountManagement.ui.fundingSucceeded",
  "付费保存": "accountManagement.ui.paidStorage",
  "临时缓存": "accountManagement.ui.temporaryStorage"
}

export const accountDisplayMessage = (t: (key: string) => string, message?: string): string => {
  if (!message) return ''
  const key = accountDisplayMessageKeys[message]
  return key ? t(key) : message
}
