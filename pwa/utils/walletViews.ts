import type { Chain } from '@/types/index'

export interface RGBSendRequest {
  recipients: RGBRecipient[]
  fee_rate: number
}

export interface RGBRecipient {
  destination: string
  asset: string
  amount: string
}

export interface RGBInvoiceRequest {
  asset: string
  amount: string
  receive_mode: string
  transport: string
  proxy_endpoint: string
}

export interface RGBIssueRequest {
  kind: string
  ticker: string
  name: string
  details: string
  precision: number
  amounts: string[]
  inflation: string[]
  reject_url: string
}

export interface AssetAmount {
  asset: string
  amount: string
  precision: number
}

export interface RGBAssetView {
  canonical_name: string
  genesis_address: string
  naming_status: string
  verified: boolean
  asset: string
  contract_id: string
  kind: string
  ticker: string
  display_ticker?: string
  name: string
  precision: number
  available: string
  pending: string
  can_send: boolean
  can_invoice: boolean
  can_inflate: boolean
  can_export: boolean
}

export function parseRGB11AssetName(name: unknown) {
  if (typeof name !== 'string') return null
  const match = /^rgb11:(f|o|control):([a-z0-9]+(?:-[a-z0-9]+)*@[0-9a-f]{4})$/.exec(name)
  return match?.[0] === name ? { protocol: 'rgb11', type: match[1], ticker: match[2] } : null
}

export function assetNameLabel(key: string, _assets: readonly RGBAssetView[] = []): string {
  return !key.startsWith('rgb11:') || parseRGB11AssetName(key) ? key : 'RGB'
}

export function assetApprovalLabel(key: string, assets: readonly RGBAssetView[] = []): string {
  const label = assetNameLabel(key, assets)
  // Show the exact invalid request rather than disguising it as a known asset.
  return label === 'RGB' ? key : label
}

export interface RGBRecipientView {
  destination: string
  amount: AssetAmount
}

export interface RGBTransferView {
  id: string
  recipients: RGBRecipientView[]
  fee_sats: number
  status: string
  tx_id: string
  error_code: string
  warning_codes: string[]
  needs_receipts: boolean
  can_confirm: boolean
  can_cancel: boolean
  can_export: boolean
}

export interface RGBReceiveView {
  id: string
  amount: AssetAmount
  invoice: string
  receive_mode: string
  transport: string
  expires_at: number
  status: string
  tx_id: string
  error_code: string
  receipt: string
  can_import: boolean
  can_accept: boolean
}

export interface RGBLockView {
  outpoint: string
  chain: 'btc' | 'satsnet'
  scope: 'current' | 'unknown'
  reason: string
  locked_time: number
  value?: number
  assets?: AssetAmount[]
  transfer_ids: string[]
}

export interface RGBView {
  lock_scope: { network: string; wallet_id: number; account_index: number }
  locks: RGBLockView[]
  assets: RGBAssetView[]
  transfers: RGBTransferView[]
  receives: RGBReceiveView[]
  syncing: boolean
  can_write: boolean
  error_code: string
}

export interface ExportFile {
  name: string
  mime: string
  data: string
}

export interface RGBTransferFile {
  recipient_index: number
  file: ExportFile
}

export interface AccountView {
  backup_risk: "unconfirmed" | "notConfigured" | ""
  active: boolean
  account_id: string
  state: string
  discovery_status: string
  recovery_mode: string
  locator: string
  backup_state: string
  last_rehearsal_at: number
  modules: ModuleView[]
  storage: StorageView | null
  autopay: PaymentView | null
  error_code: string
}

export interface ModuleView {
  name: string
  can_write: boolean
  error_code: string
}

export interface StorageOption {
  id: string
  available: boolean
  record_count: number
  ttl_blocks: number
  annual_cost: string
  warning_codes: string[]
  unavailable_code: string
  payment: PaymentQuote | null
}

export interface StorageView {
  option: StorageOption
  status: string
  tx_id: string
  error_code: string
  can_cancel: boolean
}

export interface PaymentQuote {
  source_wallet: string
  source_account: number
  source_address: string
  network: string
  contract: string
  amount: AssetAmount
  rate_per_block: string
  funding_blocks: number
  module_limit: number
  tx_id: string
}

export interface PaymentView {
  status: string
  balance: string
  expiry_height: number
  quote: PaymentQuote | null
  tx_id: string
  can_fund: boolean
  error_code: string
}

export interface RecoverySetup {
  password: string
  wallet_ids: number[]
  mode: string
  questions: QuestionInput[]
  guardian_contact: string
}

export interface Question {
  id: string
  prompt: string
  case_sensitive: boolean
  ignore_punctuation: boolean
}

export interface QuestionInput {
  question: Question
  answer: string
  confirmation: string
}

export interface Answer {
  question_id: string
  text: string
}

export interface RecoveryFactors {
  answers: Answer[]
  guardian_response: string
}

export interface ActivationInput {
  password: string
  recovery_file: string
  factors: RecoveryFactors
}

export interface RecoverySetupView {
  session_id: string
  summary: RecoverySummary
  locator: string
  questions: Question[]
  files: ExportFile[]
  can_activate: boolean
}

export interface RecoveryView {
  session_id: string
  mode: string
  questions: Question[]
  summary: RecoverySummary | null
  can_commit: boolean
}

export interface RecoverySummary {
  account_id: string
  wallets: WalletSummary[]
}

export interface WalletSummary {
  name: string
  kind: string
  account_count: number
  dids: string[]
}

export interface RecoveryResult {
  account_id: string
  root_wallet_id: number
  complete: boolean
  failed_modules: ModuleView[]
}

export interface OperationView {
  id: string
  rgb_transfer_id?: string
  kind: string
  status: string
  created_at: number
  updated_at: number
  amounts: AssetAmount[]
  tx_ids: string[]
  error_code: string
  history: OperationEvent[]
  contract?: {
    url: string
    action: string
    chain: Chain
  }
}

export type ContractLogResult = {
  status: 'ready'
  txid: string
  chain: Chain
} | {
  status: 'pending' | 'none' | 'unavailable'
}

export interface OperationEvent {
  time: number
  status: string
  error_code: string
}
