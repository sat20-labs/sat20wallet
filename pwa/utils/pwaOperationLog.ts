import { beginOperationLog, updateOperationLog } from '@/utils/operationLog'

export interface PwaOperationContext {
  id: string
  title: string
  successMessage: string
}

type OperationSpec = {
  category: string
  action: string
  title: string
  summary: string
  successMessage?: string
  parameters?: (args: any[]) => Record<string, string>
}

function text(value: unknown): string {
  if (value === undefined || value === null) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') return String(value)
  return ''
}

function safeRecord(values: Record<string, unknown>): Record<string, string> {
  const result: Record<string, string> = {}
  for (const [key, value] of Object.entries(values)) {
    const rendered = text(value).trim()
    if (rendered) result[key] = rendered
  }
  return result
}

function parseObject(value: unknown): Record<string, any> {
  if (value && typeof value === 'object' && !Array.isArray(value)) return value as Record<string, any>
  if (typeof value !== 'string' || !value.trim()) return {}
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {}
  } catch {
    return {}
  }
}

function arrayCount(value: unknown): string {
  return Array.isArray(value) ? String(value.length) : '0'
}

function requestFields(arg: unknown, fields: string[]): Record<string, string> {
  const req = parseObject(arg)
  const values: Record<string, unknown> = {}
  for (const field of fields) values[field] = req[field]
  return safeRecord(values)
}

function rgbInvoiceParameters(args: any[]): Record<string, string> {
  const req = parseObject(args[0])
  return safeRecord({
    mode: req.mode,
    transport_mode: req.transport_mode,
    contract_id: req.contract_id,
    amount_raw: req.amount_raw,
    assignment_name: req.assignment_name,
    expiry: req.expiry,
  })
}

function rgbIssueParameters(args: any[]): Record<string, string> {
  const req = parseObject(args[0])
  return safeRecord({
    schema: req.schema,
    ticker: req.ticker,
    name: req.name,
    precision: req.precision,
    allocations: Array.isArray(req.amounts) ? req.amounts.length : undefined,
  })
}

function unifiedContractParameters(args: any[]): Record<string, string> {
  const req = parseObject(args[0])
  const result: Record<string, unknown> = {}
  for (const key of [
    'contractType', 'contract_type', 'subtype', 'url', 'contractURL', 'contract_url',
    'action', 'assetName', 'asset_name', 'amount', 'amt', 'network',
  ]) {
    if (req[key] !== undefined) result[key] = req[key]
  }
  return safeRecord(result)
}

const operationSpecs: Record<string, OperationSpec> = {
  createWallet: {
    category: 'wallet', action: 'create_wallet', title: 'Create wallet', summary: 'Creating a new wallet',
  },
  importWallet: {
    category: 'wallet', action: 'import_wallet', title: 'Import wallet', summary: 'Importing a wallet from recovery words',
  },
  recoverAccountManagementFromRootMnemonic: {
    category: 'account', action: 'recover_account_root', title: 'Recover managed account', summary: 'Recovering account management from the root wallet',
  },
  changePassword: {
    category: 'security', action: 'change_password', title: 'Change wallet password', summary: 'Changing the wallet password',
  },
  deleteWallet: {
    category: 'wallet', action: 'delete_wallet', title: 'Delete wallet', summary: 'Deleting a wallet from this device',
    parameters: args => safeRecord({ wallet_id: args[0] }),
  },
  updateWalletName: {
    category: 'wallet', action: 'rename_wallet', title: 'Rename wallet', summary: 'Updating the wallet name',
    parameters: args => safeRecord({ wallet_id: args[0], name: args[1] }),
  },
  ensureAccount: {
    category: 'account', action: 'create_account', title: 'Create wallet account', summary: 'Creating or ensuring a wallet sub-account',
    parameters: args => safeRecord({ wallet_id: args[0], account_index: args[1], name: args[2] }),
  },
  updateAccountMetadata: {
    category: 'account', action: 'update_account', title: 'Update wallet account', summary: 'Updating wallet account metadata',
    parameters: args => safeRecord({ wallet_id: args[0], account_index: args[1], name: args[2] }),
  },
  switchChain: {
    category: 'wallet', action: 'switch_chain', title: 'Switch network', summary: 'Switching the active wallet network',
    parameters: args => safeRecord({ network: args[0] }),
  },
  signMessage: {
    category: 'security', action: 'sign_message', title: 'Sign message', summary: 'Signing a message with the active wallet',
  },
  signData: {
    category: 'security', action: 'sign_data', title: 'Sign data', summary: 'Signing application data with the active wallet',
  },
  signPsbt: {
    category: 'security', action: 'sign_psbt_l1', title: 'Sign Bitcoin transaction', summary: 'Signing a Bitcoin PSBT',
    parameters: args => safeRecord({ finalize: args[1] }),
  },
  signPsbts: {
    category: 'security', action: 'sign_psbts_l1', title: 'Sign Bitcoin transactions', summary: 'Signing multiple Bitcoin PSBTs',
    parameters: args => safeRecord({ count: arrayCount(args[0]), finalize: args[1] }),
  },
  signPsbt_SatsNet: {
    category: 'security', action: 'sign_psbt_l2', title: 'Sign SatoshiNet transaction', summary: 'Signing a SatoshiNet PSBT',
    parameters: args => safeRecord({ finalize: args[1] }),
  },
  sendAssets: {
    category: 'transfer', action: 'send_asset_l1', title: 'Send L1 asset', summary: 'Sending an asset on Bitcoin',
    parameters: args => safeRecord({ destination: args[0], asset: args[1], amount: args[2], fee_rate: args[3] }),
    successMessage: 'L1 asset transaction submitted',
  },
  sendAssets_SatsNet: {
    category: 'transfer', action: 'send_asset_l2', title: 'Send L2 asset', summary: 'Sending an asset on SatoshiNet',
    parameters: args => safeRecord({ destination: args[0], asset: args[1], amount: args[2] }),
    successMessage: 'L2 asset transaction submitted',
  },
  sendUtxos_SatsNet: {
    category: 'transfer', action: 'send_utxos_l2', title: 'Send L2 UTXOs', summary: 'Sending selected SatoshiNet UTXOs',
    parameters: args => safeRecord({ destination: args[0], utxo_count: arrayCount(args[1]) }),
    successMessage: 'L2 UTXO transaction submitted',
  },
  batchSendAssetsV2_SatsNet: {
    category: 'transfer', action: 'batch_send_l2', title: 'Batch send L2 asset', summary: 'Sending an asset to multiple SatoshiNet addresses',
    parameters: args => safeRecord({ recipient_count: arrayCount(args[0]), asset: args[1] }),
    successMessage: 'L2 batch transaction submitted',
  },
  batchSendAssets_SatsNet: {
    category: 'transfer', action: 'split_send_l2', title: 'Split L2 asset', summary: 'Sending an asset into multiple SatoshiNet outputs',
    parameters: args => safeRecord({ destination: args[0], asset: args[1], amount: args[2], outputs: args[3] }),
    successMessage: 'L2 split transaction submitted',
  },
  batchSendAssets: {
    category: 'transfer', action: 'split_send_l1', title: 'Split L1 asset', summary: 'Sending an asset into multiple Bitcoin outputs',
    parameters: args => safeRecord({ destination: args[0], asset: args[1], amount: args[2], outputs: args[3], fee_rate: args[4] }),
    successMessage: 'L1 split transaction submitted',
  },
  createRGB11Invoice: {
    category: 'rgb11', action: 'rgb11_create_invoice', title: 'Create RGB invoice', summary: 'Creating an RGB11 receive request',
    parameters: rgbInvoiceParameters,
  },
  importRGB11Contract: {
    category: 'rgb11', action: 'rgb11_import_contract', title: 'Import RGB contract', summary: 'Importing an RGB11 contract',
    parameters: () => ({ source: 'consignment' }),
  },
  importRGB11ContractFile: {
    category: 'rgb11', action: 'rgb11_import_contract_file', title: 'Import RGB contract file', summary: 'Importing an RGB11 contract from a file',
    parameters: () => ({ source: 'file' }),
  },
  issueRGB11Asset: {
    category: 'rgb11', action: 'rgb11_issue_asset', title: 'Issue RGB asset', summary: 'Issuing a new RGB11 asset',
    parameters: rgbIssueParameters,
  },
  resumeRGB11PreparedTransfer: {
    category: 'rgb11', action: 'rgb11_resume_transfer', title: 'Resume RGB transfer', summary: 'Resuming a prepared RGB11 transfer',
    parameters: args => safeRecord({ transfer_id: args[0] }),
  },
  acceptRGB11Consignment: {
    category: 'rgb11', action: 'rgb11_accept_consignment', title: 'Accept RGB transfer', summary: 'Accepting an RGB11 consignment',
    parameters: args => safeRecord({ request_id: args[0] }),
  },
  prepareRGB11Consignment: {
    category: 'rgb11', action: 'rgb11_prepare_consignment', title: 'Validate RGB transfer', summary: 'Preparing an incoming RGB11 consignment for acceptance',
    parameters: args => safeRecord({ request_id: args[0] }),
  },
  receiveRGB11ProxyConsignment: {
    category: 'rgb11', action: 'rgb11_receive_proxy', title: 'Receive RGB proxy transfer', summary: 'Receiving an RGB11 consignment from proxy transport',
    parameters: args => safeRecord({ request_id: args[0] }),
  },
  cancelRGB11OutOfBandTransfer: {
    category: 'rgb11', action: 'rgb11_cancel_transfer', title: 'Cancel RGB transfer', summary: 'Cancelling a prepared RGB11 transfer',
    parameters: args => safeRecord({ transfer_id: args[0] }),
  },
  cancelExpiredRGB11Transfer: {
    category: 'rgb11', action: 'rgb11_cancel_expired_transfer', title: 'Cancel expired RGB transfer', summary: 'Cancelling an expired RGB11 transfer',
    parameters: args => safeRecord({ transfer_id: args[0] }),
  },
  startBTCLuckyMining: {
    category: 'mining', action: 'start_lucky_mining', title: 'Start BTC lucky mining', summary: 'Starting local BTC lucky mining',
    parameters: args => {
      const req = parseObject(args[0])
      return safeRecord({ jobs: req.jobs, low_priority: req.lowPriority })
    },
  },
  stopBTCLuckyMining: {
    category: 'mining', action: 'stop_lucky_mining', title: 'Stop BTC lucky mining', summary: 'Stopping local BTC lucky mining',
  },
  lockUtxo: {
    category: 'utxo', action: 'lock_utxo_l1', title: 'Lock L1 UTXO', summary: 'Locking a Bitcoin UTXO for local wallet use',
    parameters: args => safeRecord({ address: args[0], reason: args[2] }),
  },
  unlockUtxo: {
    category: 'utxo', action: 'unlock_utxo_l1', title: 'Unlock L1 UTXO', summary: 'Unlocking a Bitcoin UTXO',
    parameters: args => safeRecord({ address: args[0] }),
  },
  lockUtxo_SatsNet: {
    category: 'utxo', action: 'lock_utxo_l2', title: 'Lock L2 UTXO', summary: 'Locking a SatoshiNet UTXO for local wallet use',
    parameters: args => safeRecord({ address: args[0], reason: args[2] }),
  },
  unlockUtxo_SatsNet: {
    category: 'utxo', action: 'unlock_utxo_l2', title: 'Unlock L2 UTXO', summary: 'Unlocking a SatoshiNet UTXO',
    parameters: args => safeRecord({ address: args[0] }),
  },
  deployUnifiedContract: {
    category: 'contract', action: 'deploy_unified_contract', title: 'Deploy contract', summary: 'Deploying a contract through the unified contract API',
    parameters: args => unifiedContractParameters(args),
    successMessage: 'Contract deployment transaction submitted',
  },
  invokeUnifiedContract: {
    category: 'contract', action: 'invoke_unified_contract', title: 'Invoke contract', summary: 'Invoking a contract through the unified contract API',
    parameters: args => unifiedContractParameters(args),
    successMessage: 'Contract invocation transaction submitted',
  },
  registerAsReferrer: {
    category: 'referrer', action: 'register_referrer', title: 'Register referrer', summary: 'Registering a referrer name',
    parameters: args => safeRecord({ name: args[0], fee_rate: args[1] }),
  },
  bindReferrerForServer: {
    category: 'referrer', action: 'bind_referrer', title: 'Bind referrer', summary: 'Binding a referrer to a service node',
    parameters: args => safeRecord({ referrer: args[0], server_pubkey: args[1] }),
  },
  deployTickerOrdx: {
    category: 'asset', action: 'deploy_ordx_asset', title: 'Deploy ORDX asset', summary: 'Deploying a new ORDX ticker',
    parameters: args => safeRecord({ ticker: args[0], max: args[1], limit: args[2], binding_sat: args[3], fee_rate: args[4] }),
  },
  mintAssetOrdx: {
    category: 'asset', action: 'mint_ordx_asset', title: 'Mint ORDX asset', summary: 'Minting an ORDX asset',
    parameters: args => safeRecord({ ticker: args[0], amount: args[1], fee_rate: args[2] }),
  },
  mintAssetRunes: {
    category: 'asset', action: 'mint_runes_asset', title: 'Mint Runes asset', summary: 'Minting a Runes asset',
    parameters: args => safeRecord({ ticker: args[0], fee_rate: args[1] }),
  },
  deployTickerBrc20: {
    category: 'asset', action: 'deploy_brc20_asset', title: 'Deploy BRC20 asset', summary: 'Deploying a new BRC20 ticker',
    parameters: args => safeRecord({ ticker: args[0], max: args[1], limit: args[2], decimal: args[3], fee_rate: args[4] }),
  },
  mintAssetBrc20: {
    category: 'asset', action: 'mint_brc20_asset', title: 'Mint BRC20 asset', summary: 'Minting a BRC20 asset',
    parameters: args => safeRecord({ ticker: args[0], amount: args[1], fee_rate: args[2] }),
  },
  inscribeName: {
    category: 'name', action: 'inscribe_name', title: 'Inscribe name', summary: 'Inscribing a name on Bitcoin',
    parameters: args => safeRecord({ name: args[0], fee_rate: args[1] }),
  },
  switchWallet: {
    category: 'wallet', action: 'switch_wallet', title: 'Switch wallet', summary: 'Switching the active wallet',
    parameters: args => safeRecord({ wallet_id: args[0] }),
  },
  switchAccount: {
    category: 'wallet', action: 'switch_account', title: 'Switch account', summary: 'Switching the active wallet account',
    parameters: args => safeRecord({ account_index: args[0] }),
  },
  importWalletWithPrivKey: {
    category: 'wallet', action: 'import_wallet_private_key', title: 'Import wallet', summary: 'Importing a wallet from a private key',
  },
  unlockWallet: {
    category: 'security', action: 'unlock_wallet', title: 'Unlock wallet', summary: 'Unlocking the active wallet',
  },
  resumeLockWithExpandFromL1Tx: {
    category: 'channel', action: 'resume_lock_expand', title: 'Resume channel lock', summary: 'Resuming lock-with-expand after the L1 transaction',
    parameters: args => safeRecord({ reservation_id: args[0], l1_txid: args[1] }),
  },
  commitmentExport: {
    category: 'safety', action: 'export_commitment', title: 'Export channel commitment', summary: 'Exporting channel commitment data for recovery',
    parameters: args => safeRecord({ channel_id: args[0] }),
  },
  punishBuild: {
    category: 'safety', action: 'build_punishment', title: 'Build punishment transaction', summary: 'Building a channel punishment transaction',
    parameters: args => safeRecord({ channel_id: args[0], commit_txid: args[1] }),
  },
  punishBroadcast: {
    category: 'safety', action: 'broadcast_punishment', title: 'Broadcast punishment transaction', summary: 'Broadcasting a channel punishment transaction',
    parameters: args => safeRecord({ channel_id: args[0], commit_txid: args[1] }),
    successMessage: 'Punishment transaction broadcast',
  },
  sweepBuild: {
    category: 'safety', action: 'build_channel_sweep', title: 'Build channel sweep', summary: 'Building a channel sweep transaction',
    parameters: args => safeRecord({ channel_id: args[0], commit_txid: args[1], height: args[2], broadcast: args[3] }),
    successMessage: 'Channel sweep operation completed',
  },
  enableRGB11AddressReceive: {
    category: 'rgb11', action: 'rgb11_enable_address_receive', title: 'Enable RGB address receive', summary: 'Enabling RGB11 address-based receiving',
  },
  buildBatchSellOrder_SatsNet: {
    category: 'dex', action: 'build_sell_order_l2', title: 'Create L2 sell order', summary: 'Creating a SatoshiNet sell order',
    parameters: args => safeRecord({ utxo_count: arrayCount(args[0]), address: args[1], network: args[2] }),
  },
  finalizeSellOrder_SatsNet: {
    category: 'dex', action: 'finalize_sell_order_l2', title: 'Finalize sell order', summary: 'Finalizing a SatoshiNet sell order',
    parameters: args => safeRecord({ utxo_count: arrayCount(args[1]), buyer: args[2], server: args[3], network: args[4] }),
  },
}

function extractTxID(data: any): string {
  if (typeof data === 'string' && /^[0-9a-fA-F]{64}$/.test(data)) return data
  if (!data || typeof data !== 'object') return ''
  for (const key of ['txId', 'txid', 'tx_id', 'commitTxId', 'revealTxId']) {
    const value = text(data[key]).trim()
    if (value) return value
  }
  return ''
}

function extractSafeResult(data: any): Record<string, string> | undefined {
  if (!data || typeof data !== 'object') return undefined
  const result: Record<string, unknown> = {}
  for (const key of [
    'walletId', 'txId', 'txid', 'commitTxId', 'revealTxId', 'resvId', 'reservationId',
    'transfer_id', 'transfer_ids', 'request_id', 'contractAddress', 'contractType', 'orderId',
  ]) {
    const value = data[key]
    if (Array.isArray(value)) result[key] = value.length
    else if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') result[key] = value
  }
  const safe = safeRecord(result)
  return Object.keys(safe).length ? safe : undefined
}

export async function beginNamedPwaOperation(input: {
  category: string
  action: string
  title: string
  summary: string
  parameters?: Record<string, string>
  successMessage?: string
}): Promise<PwaOperationContext | null> {
  try {
    const [error, id] = await beginOperationLog({
      category: input.category,
      action: input.action,
      title: input.title,
      summary: input.summary,
      parameters: input.parameters,
    })
    if (error || !id) {
      if (error) console.warn('Unable to create operation log:', error)
      return null
    }
    return {
      id,
      title: input.title,
      successMessage: input.successMessage || `${input.title} completed`,
    }
  } catch (error) {
    console.warn('Unable to create operation log:', error)
    return null
  }
}

export async function beginPwaWalletOperation(methodName: string, args: any[]): Promise<PwaOperationContext | null> {
  const spec = operationSpecs[methodName]
  if (!spec) return null
  let parameters: Record<string, string> | undefined
  try {
    parameters = spec.parameters?.(args)
  } catch (error) {
    console.warn(`Unable to prepare operation-log parameters for ${methodName}:`, error)
  }
  return beginNamedPwaOperation({
    category: spec.category,
    action: spec.action,
    title: spec.title,
    summary: spec.summary,
    parameters,
    successMessage: spec.successMessage,
  })
}

export async function finishPwaOperation(
  context: PwaOperationContext | null,
  error?: Error | null,
  data?: any,
): Promise<void> {
  if (!context) return
  try {
    if (error) {
      await updateOperationLog(context.id, {
        status: 'failed',
        message: `${context.title} failed`,
        details: { error: error.message },
      })
      return
    }
    const txid = extractTxID(data)
    await updateOperationLog(context.id, {
      status: 'succeeded',
      message: context.successMessage,
      txid: txid || undefined,
      result: extractSafeResult(data),
    })
  } catch (logError) {
    console.warn('Unable to update operation log:', logError)
  }
}

export function safeAccountOperationParameters(values: Record<string, unknown>): Record<string, string> {
  return safeRecord(values)
}

export function safeRequestOperationParameters(request: unknown, fields: string[]): Record<string, string> {
  return requestFields(request, fields)
}
