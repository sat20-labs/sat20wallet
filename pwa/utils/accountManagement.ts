import { walletRequestSessionGuard } from '@/lib/walletSession'
import { beginVersionDispatch } from '@/utils/pwaVersionPolicy'
import { wasmResponseError } from '@/utils/wasmError'
import { noteWasmOperation } from '@/utils/wasmRuntimeDiagnostics'
import type { AccountView, RecoverySummary, StorageOption, StorageView, PaymentQuote, PaymentView,
  ExportFile, RecoverySetup, RecoverySetupView, ActivationInput, RecoveryView, RecoveryFactors, RecoveryResult } from './walletViews'

class AccountManagementSDK {
  private async request<T>(methodName: string, input: unknown = {}): Promise<T> {
    const diagnosticMethod = `account.${methodName}`
    const check = walletRequestSessionGuard(diagnosticMethod)
    check()
    const finish = beginVersionDispatch(methodName, true)
    try {
      noteWasmOperation(diagnosticMethod, 'dispatch')
      const result = await (globalThis as any).sat20account_wasm[methodName](JSON.stringify(input))
      noteWasmOperation(diagnosticMethod, 'returned')
      check()
      if (!result || result.code !== 0) throw wasmResponseError(result?.msg || '账户管理调用失败', result?.error_code)
      return result.data as T
    } catch (error) {
      noteWasmOperation(diagnosticMethod, 'failed')
      throw error
    } finally { finish() }
  }
  getAccountView() { return this.request<AccountView>('getAccountView') }
  refreshAccount() { return this.request<AccountView>('refreshAccount') }
  preflightRecovery(password: string, walletIDs: number[]) {
    return this.request<RecoverySummary>('preflightRecovery', { password, wallet_ids: walletIDs })
  }
  getStorageOptions(recordCount = 0) { return this.request<StorageOption[]>('getStorageOptions', { record_count: recordCount }) }
  confirmStorage(confirmed: StorageOption) { return this.request<StorageView>('confirmStorage', confirmed) }
  cancelStorageSetup() { return this.request<void>('cancelStorageSetup') }
  fundAutopay(confirmed: PaymentQuote) { return this.request<PaymentView>('fundAutopay', confirmed) }
  createRecovery(input: RecoverySetup) { return this.request<RecoverySetupView>('createRecovery', input) }
  activateRecovery(sessionID: string, input: ActivationInput) {
    return this.request<AccountView>('activateRecovery', { session_id: sessionID, input })
  }
  loadRecovery(locator: string, rehearsal = false) { return this.request<RecoveryView>('loadRecovery', { locator, rehearsal }) }
  previewRecovery(sessionID: string, factors: RecoveryFactors) {
    return this.request<RecoveryView>('previewRecovery', { session_id: sessionID, factors })
  }
  commitRecovery(sessionID: string, newPassword: string) {
    return this.request<RecoveryResult>('commitRecovery', { session_id: sessionID, new_password: newPassword })
  }
  abortRecovery(sessionID: string) { return this.request<void>('abortRecovery', { session_id: sessionID }) }
}
export default new AccountManagementSDK()

// Browser-only file presentation. The content is generated and validated by SDK.
export function exportFileText(file: ExportFile): string {
  return new TextDecoder().decode(Uint8Array.from(atob(file.data), ch => ch.charCodeAt(0)))
}
export function saveExportFile(file: ExportFile): void {
  const data = Uint8Array.from(atob(file.data), ch => ch.charCodeAt(0))
  const url = URL.createObjectURL(new Blob([data], { type: file.mime }))
  const link = document.createElement('a')
  link.href = url; link.download = file.name; link.click()
  URL.revokeObjectURL(url)
}

// Translate known SDK display messages at the UI boundary; retain unknown diagnostics.
const accountDisplayMessageKeys: Record<string, string> = {
  "guardian recovery is disabled": "accountManagement.ui.guardianDisabled",
  "account management requires a mnemonic root wallet": "accountManagement.ui.mnemonicRootRequired",
  "invalid recovery package": "accountManagement.ui.invalidRecoveryPackage",
  "insufficient recovery shares": "accountManagement.ui.insufficientShares",
  "account recovery failed": "accountManagement.ui.recoveryFailed",
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
