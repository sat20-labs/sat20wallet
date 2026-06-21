import { useChannelStore } from '@/store/channel'
import { useGlobalStore } from '@/store/global'
import { useWalletStore } from '@/store/wallet'
import { Network } from '@/types'
import { getConfig } from '@/config/wasm'
import walletManager from '@/utils/sat20'
import satsnetStp from '@/utils/stp'
import {
  assessStpValueMovementSafety,
  isStpAgentValueMovementOperation,
} from '@/composables/usePwaAgentAdapterSafety'

export const PWA_AGENT_METHODS = [
  'wallet.status',
  'wallet.create',
  'wallet.import',
  'wallet.export_mnemonic',
  'wallet.change_password',
  'wallet.send_assets',
  'wallet.transaction',
  'stp.status',
  'stp.open',
  'stp.close',
  'stp.splicing_in',
  'stp.splicing_out',
  'stp.lock',
  'stp.lock_with_expand',
  'stp.unlock',
  'stp.transaction',
  'stp.safety_snapshot',
  'stp.commitment_export',
  'stp.punish_status',
  'stp.punish_build',
  'stp.punish_broadcast',
  'stp.force_close_plan',
  'stp.sweep_build',
] as const

export const PWA_AGENT_APPROVAL_OPERATIONS = new Set<string>([
  'wallet.create',
  'wallet.import',
  'wallet.export_mnemonic',
  'wallet.change_password',
  'wallet.send_assets',
  'stp.open',
  'stp.close',
  'stp.splicing_in',
  'stp.splicing_out',
  'stp.lock',
  'stp.lock_with_expand',
  'stp.unlock',
  'stp.punish_broadcast',
  'stp.sweep_build',
])

export const isPwaAgentOperation = (action: string) => (
  action.startsWith('wallet.') || action.startsWith('stp.')
)

type AgentParams = Record<string, any>

interface AgentExecutionOptions {
  approved?: boolean
}

const success = (operation: string, result: Record<string, any> = {}) => ({
  ok: true,
  operation,
  ...result,
})

const failure = (operation: string, code: string, message: string, details?: any) => ({
  ok: false,
  operation,
  error: {
    code,
    message,
    details,
  },
})

const errorMessage = (error: unknown) => (
  error instanceof Error ? error.message : String(error)
)

const unwrap = async <T>(operation: string, tuple: Promise<[Error | undefined, T | undefined]>) => {
  const [err, result] = await tuple
  if (err) {
    return failure(operation, 'WASM_ERROR', err.message || String(err))
  }
  return success(operation, { result })
}

const unwrapJson = async <T>(operation: string, tuple: Promise<[Error | undefined, T | undefined]>) => {
  const [err, result] = await tuple
  if (err) {
    return failure(operation, 'WASM_ERROR', err.message || String(err))
  }
  return success(operation, jsonSuccessPayload(result))
}

const requireApproved = (operation: string, options: AgentExecutionOptions) => {
  if (PWA_AGENT_APPROVAL_OPERATIONS.has(operation) && !options.approved) {
    return failure(
      operation,
      'AUTH_REQUIRED',
      'This Agent operation must be confirmed in the SAT20 PWA Wallet before execution.'
    )
  }
  return null
}

const requireWalletReady = (operation: string) => {
  const walletStore = useWalletStore()
  if (!walletStore.hasWallet || !walletStore.address) {
    return failure(operation, 'WALLET_NOT_READY', 'No active wallet is available.')
  }
  if (walletStore.locked) {
    return failure(operation, 'WALLET_LOCKED', 'Unlock the wallet before executing this operation.')
  }
  return null
}

const requireString = (params: AgentParams, name: string) => {
  const value = params[name]
  if (typeof value !== 'string' || value.trim() === '') {
    throw new Error(`Missing required string parameter: ${name}`)
  }
  return value.trim()
}

const optionalString = (params: AgentParams, name: string, fallback = '') => {
  const value = params[name]
  return typeof value === 'string' ? value : fallback
}

const requireAmount = (params: AgentParams, name = 'amount') => {
  const value = params[name] ?? params.amount_sats
  const amount = Number(value)
  if (!Number.isFinite(amount) || amount <= 0) {
    throw new Error(`Missing required positive numeric parameter: ${name}`)
  }
  return amount
}

const optionalNumber = (params: AgentParams, name: string, fallback: number) => {
  const value = Number(params[name])
  return Number.isFinite(value) && value > 0 ? value : fallback
}

const asStringArray = (value: any): string[] => {
  if (Array.isArray(value)) {
    return value.map((item) => String(item)).filter(Boolean)
  }
  return []
}

const txIdList = (params: AgentParams): string[] => {
  const ids = asStringArray(params.tx_ids)
  const single = String(params.transaction_id ?? params.txid ?? '').trim()
  if (single) {
    ids.push(single)
  }
  return Array.from(new Set(ids))
}

const normalizeLayer = (params: AgentParams) => {
  const layer = String(params.layer ?? params.chain ?? 'btc_l1').toLowerCase()
  if (['satsnet', 'satoshinet', 'l2', 'satnet'].includes(layer)) {
    return 'satsnet'
  }
  return 'btc_l1'
}

const getChannelId = (params: AgentParams) => (
  String(params.channel_id ?? params.channel_point ?? params.chan_point ?? params.channelUtxo ?? '').trim()
)

const rawChannelJson = (value: any) => {
  if (!value) return null
  if (value.json && typeof value.json === 'string') {
    try {
      return JSON.parse(value.json)
    } catch {
      return null
    }
  }
  return value
}

const rawJsonPayload = (value: any) => rawChannelJson(value)

const jsonSuccessPayload = (value: any) => {
  const parsed = rawJsonPayload(value)
  if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
    return parsed
  }
  return { result: parsed ?? value }
}

const getAgentChannel = async (params: AgentParams = {}) => {
  const channelId = getChannelId(params)
  if (channelId) {
    const byId = await safeTuple(() => satsnetStp.getChannel(channelId))
    if (byId.result) {
      return { channel: rawChannelJson(byId.result), error: byId.error }
    }
    if (byId.error) {
      return { channel: null, error: byId.error }
    }
  }
  const current = await safeTuple(() => satsnetStp.getCurrentChannel())
  return { channel: rawChannelJson(current.result), error: current.error }
}

const txIndexerUrl = (layer: 'btc_l1' | 'satsnet', txid: string) => {
  const walletStore = useWalletStore()
  const globalStore = useGlobalStore()
  const network = (walletStore.network || Network.TESTNET) as Network
  const cfg = getConfig(globalStore.env, network)
  const indexer = layer === 'satsnet' ? cfg.IndexerL2 : cfg.IndexerL1
  const proxy = indexer.Proxy.replace(/^\/|\/$/g, '')
  return `${indexer.Scheme}://${indexer.Host}/${proxy}/btc/tx/simpleinfo/${txid}`
}

const queryTxStatus = async (layer: 'btc_l1' | 'satsnet', txid: string) => {
  const url = txIndexerUrl(layer, txid)
  try {
    const response = await fetch(url)
    const body = await response.json().catch(() => undefined)
    const data = body?.data ?? body?.Data
    const code = body?.code ?? body?.Code
    if (!response.ok || (typeof code === 'number' && code !== 0)) {
      return {
        layer,
        txid,
        url,
        visible: false,
        confirmed: false,
        status: 'BROADCASTED_OR_PENDING_INDEXER',
        error_code: 'TX_NOT_FOUND_OR_NOT_INDEXED',
        message: body?.msg ?? body?.Msg ?? response.statusText,
      }
    }
    const confirmations = Number(data?.confirmations ?? data?.Confirmations ?? 0)
    const blockHeight = Number(data?.blockHeight ?? data?.BlockHeight ?? data?.height ?? data?.Height ?? 0)
    return {
      layer,
      txid,
      url,
      visible: true,
      confirmed: confirmations > 0 || blockHeight > 0,
      confirmations,
      block_height: blockHeight,
      status: confirmations > 0 || blockHeight > 0 ? 'CONFIRMED' : 'BROADCASTED',
      tx_info: data ?? body,
    }
  } catch (error: any) {
    return {
      layer,
      txid,
      url,
      visible: false,
      confirmed: false,
      status: 'UNKNOWN',
      error_code: layer === 'satsnet' ? 'MISSING_L2_TX_STATUS' : 'MISSING_L1_TX_STATUS',
      message: error?.message || String(error),
    }
  }
}

const pollTransactions = async (operation: string, params: AgentParams) => {
  const ids = txIdList(params)
  if (!ids.length) {
    if (operation === 'stp.transaction' && params.reservation_id) {
      const resv = await safeTuple(() => satsnetStp.reservationStatus(params.reservation_id))
      if (resv.result) {
        const reservation = rawJsonPayload(resv.result)
        return success(operation, {
          reservation_id: params.reservation_id,
          reservation,
          status: Number((resv.result as any)?.status ?? reservation?.status) > 0 ? 'PENDING' : 'FINISHED_OR_FAILED',
          next_check: 'poll stp.transaction again or inspect returned reservation state',
        })
      }
      return failure(
        operation,
        'MISSING_RESERVATION_STATE',
        resv.error || 'reservation state is not available from STP WASM'
      )
    }
    return failure(
      operation,
      operation === 'stp.transaction' ? 'MISSING_RESERVATION_STATE' : 'MISSING_TRANSACTION_ID',
      operation === 'stp.transaction'
        ? 'stp.transaction requires tx_ids until the PWA adapter exposes reservation polling.'
        : 'wallet.transaction requires transaction_id or tx_ids.'
    )
  }

  const requestedLayer = normalizeLayer(params)
  const layers: Array<'btc_l1' | 'satsnet'> = operation === 'stp.transaction' && !params.layer
    ? ['btc_l1', 'satsnet']
    : [requestedLayer]
  const statuses = []
  for (const txid of ids) {
    for (const layer of layers) {
      statuses.push(await queryTxStatus(layer, txid))
    }
  }
  const visible = statuses.filter((item) => item.visible)
  const missingEvidence = statuses
    .filter((item) => item.status === 'UNKNOWN')
    .map((item) => item.error_code)
    .filter(Boolean)
  const confirmed = visible.length > 0 && visible.every((item) => item.confirmed)
  return success(operation, {
    transaction_id: String(params.transaction_id ?? ids[0]),
    reservation_id: params.reservation_id,
    tx_ids: ids,
    tx_statuses: statuses,
    missing_evidence: Array.from(new Set(missingEvidence)),
    status: confirmed ? 'CONFIRMED' : visible.length > 0 ? 'BROADCASTED' : 'BROADCASTED_OR_PENDING_INDEXER',
    next_check: confirmed ? 'done' : 'poll transaction again',
  })
}

const safeTuple = async <T>(fn: () => Promise<[Error | undefined, T | undefined]>) => {
  try {
    const [err, result] = await fn()
    return err ? { error: err.message || String(err) } : { result }
  } catch (error: any) {
    return { error: error?.message || String(error) }
  }
}

const walletStatus = async (operation: string) => {
  const walletStore = useWalletStore()
  const channelStore = useChannelStore()
  const channels = await safeTuple(() => satsnetStp.getAllChannels())
  const currentChannel = await safeTuple(() => satsnetStp.getCurrentChannel())
  const reservations = await safeTuple(() => satsnetStp.allReservations())
  const l1Utxos = walletStore.hasWallet
    ? await safeTuple(() => walletManager.getUtxos())
    : { result: undefined }
  const l2Utxos = walletStore.hasWallet
    ? await safeTuple(() => walletManager.getUtxos_SatsNet())
    : { result: undefined }

  return success(operation, {
    wallet: {
      has_wallet: Boolean(walletStore.hasWallet),
      locked: Boolean(walletStore.locked),
      network: walletStore.network,
      chain: walletStore.chain,
      wallet_id: walletStore.walletId,
      account_index: walletStore.accountIndex,
      address: walletStore.address,
      public_key: walletStore.publicKey,
    },
    channels: {
      current: currentChannel,
      all: channels,
      reservations,
      store_current: channelStore.channel,
      store_assets: {
        btc: channelStore.plainList,
        ordx: channelStore.sat20List,
        runes: channelStore.runesList,
        brc20: channelStore.brc20List,
        ord: channelStore.ordList,
      },
    },
    utxos: {
      btc_l1: l1Utxos,
      satsnet: l2Utxos,
    },
  })
}

const safetySnapshot = async (operation: string, params: AgentParams) => {
  const wasm = await safeTuple(() => satsnetStp.safetySnapshot(getChannelId(params)))
  if (wasm.result) {
    return success(operation, jsonSuccessPayload(wasm.result))
  }

  const { channel, error } = await getAgentChannel(params)
  if (!channel) {
    return failure(operation, 'MISSING_PEER_CHANNEL_STATE', wasm.error || error || 'No current STP channel is available.')
  }

  const localCommitment = channel.localcommitment ?? channel.localCommitment
  const remoteCommitment = channel.remotecommitment ?? channel.remoteCommitment
  const commitHeight = Number(channel.commitheight ?? channel.commitHeight ?? 0)
  const missing = []
  if (!localCommitment) missing.push('MISSING_LOCAL_COMMITMENT')
  if (!remoteCommitment) missing.push('MISSING_REMOTE_COMMITMENT')
  missing.push('MISSING_PUNISH_COVERAGE')

  const ready = Number(channel.status) > 0 && localCommitment && remoteCommitment && missing.length === 1
  return success(operation, {
    channel_id: channel.channelId ?? channel.channel_id ?? channel.chanid ?? getChannelId(params),
    channel_address: channel.address,
    chan_point: channel.channelId ?? channel.chanid ?? getChannelId(params),
    raw_status: channel.status,
    status: ready ? 'READY_DEGRADED' : 'UNSAFE',
    commit_height: commitHeight,
    csv_delay: channel.csvdelay ?? channel.csvDelay,
    local_commitment_present: Boolean(localCommitment),
    remote_commitment_present: Boolean(remoteCommitment),
    local_balance: channel.localbalanceL1 ?? channel.localBalanceL1 ?? [],
    remote_balance: channel.remotebalance_L1 ?? channel.remoteBalanceL1 ?? [],
    l2_spendable_balance: channel.localutxo_L2 ?? channel.localUtxoL2 ?? null,
    l2_pending_balance: channel.localunhandledutxos_L2 ?? channel.localUnhandledUtxosL2 ?? [],
    punish_coverage: {
      status: 'PUNISH_COVERAGE_UNKNOWN',
      missing: ['MISSING_PUNISH_COVERAGE'],
    },
    missing_evidence: missing,
    next_check: 'Expose stp.punish_status in the PWA/STP WASM adapter before value-moving operations.',
  })
}

const commitmentExport = async (operation: string, params: AgentParams) => {
  const wasm = await safeTuple(() => satsnetStp.commitmentExport(getChannelId(params)))
  if (wasm.result) {
    return success(operation, jsonSuccessPayload(wasm.result))
  }

  const { channel, error } = await getAgentChannel(params)
  if (!channel) {
    return failure(operation, 'MISSING_COMMITMENT_EXPORT', wasm.error || error || 'No current STP channel is available.')
  }
  const localCommitment = channel.localcommitment ?? channel.localCommitment
  const remoteCommitment = channel.remotecommitment ?? channel.remoteCommitment
  if (!localCommitment || !remoteCommitment) {
    return failure(operation, 'MISSING_COMMITMENT_EXPORT', 'Current channel does not expose both local and remote commitments.')
  }
  return success(operation, {
    channel_id: channel.channelId ?? channel.channel_id ?? channel.chanid ?? getChannelId(params),
    chan_point: channel.channelId ?? channel.chanid ?? getChannelId(params),
    commit_height: Number(channel.commitheight ?? channel.commitHeight ?? 0),
    csv_delay: channel.csvdelay ?? channel.csvDelay,
    local_commitment: localCommitment,
    remote_commitment: remoteCommitment,
    local_deanchor_tx: channel.localDeAnchorTx,
    remote_deanchor_tx: channel.remoteDeAnchorTx,
    local_balance: channel.localbalanceL1 ?? [],
    remote_balance: channel.remotebalance_L1 ?? [],
  })
}

const forceClosePlan = async (operation: string, params: AgentParams) => {
  const wasm = await safeTuple(() => satsnetStp.forceClosePlan(getChannelId(params)))
  if (wasm.result) {
    return success(operation, jsonSuccessPayload(wasm.result))
  }

  const exported = await commitmentExport(operation, params)
  if (!exported.ok) return exported
  return success(operation, {
    ...(exported as any),
    status: 'PLAN_AVAILABLE_PARTIAL',
    missing_evidence: ['MISSING_SWEEP_BUILD'],
    user_action_required: 'Broadcast local commitment only if cooperative close is unavailable or safety requires unilateral exit.',
    next_check: 'Expose stp.sweep_build to dry-run CSV sweep after force close.',
  })
}

const requireStpSafetyForValueMovement = async (operation: string, params: AgentParams) => {
  if (!isStpAgentValueMovementOperation(operation)) {
    return null
  }

  const snapshot = await safetySnapshot('stp.safety_snapshot', params)
  if (!snapshot.ok) {
    return failure(
      operation,
      'SAFETY_BLOCKED',
      'Unable to verify STP safety before executing a value-moving Agent operation.',
      (snapshot as any).error
    )
  }

  const assessment = assessStpValueMovementSafety(snapshot)
  if (!assessment.allowed) {
    return failure(
      operation,
      'SAFETY_BLOCKED',
      assessment.reason || 'STP safety preflight blocked this value-moving Agent operation.',
      assessment.details
    )
  }

  return null
}

export const executePwaAgentOperation = async (
  operation: string,
  params: AgentParams = {},
  options: AgentExecutionOptions = {}
) => {
  if (!isPwaAgentOperation(operation)) {
    return failure(operation, 'UNSUPPORTED_OPERATION', `Unsupported Agent operation: ${operation}`)
  }

  const approvalError = requireApproved(operation, options)
  if (approvalError) {
    return approvalError
  }

  try {
    switch (operation) {
      case 'wallet.status':
      case 'stp.status':
        return walletStatus(operation)

      case 'wallet.create': {
        const password = requireString(params, 'password')
        const walletStore = useWalletStore()
        const [err, mnemonic] = await walletStore.createWallet(password)
        if (err) {
          return failure(operation, 'WALLET_CREATE_FAILED', errorMessage(err))
        }
        return success(operation, {
          mnemonic,
          wallet_id: walletStore.walletId,
          address: walletStore.address,
          public_key: walletStore.publicKey,
        })
      }

      case 'wallet.import': {
        const mnemonic = requireString(params, 'mnemonic')
        const password = requireString(params, 'password')
        const walletStore = useWalletStore()
        const [err] = await walletStore.importWallet(mnemonic, password)
        if (err) {
          return failure(operation, 'WALLET_IMPORT_FAILED', errorMessage(err))
        }
        return success(operation, {
          wallet_id: walletStore.walletId,
          address: walletStore.address,
          public_key: walletStore.publicKey,
        })
      }

      case 'wallet.export_mnemonic': {
        const walletStore = useWalletStore()
        const readyError = requireWalletReady(operation)
        if (readyError) return readyError
        const password = requireString(params, 'password')
        const walletId = Number(walletStore.walletId)
        if (!Number.isFinite(walletId)) {
          return failure(operation, 'INVALID_WALLET_ID', 'Current wallet id is not numeric.')
        }
        const [err, result] = await walletManager.getMnemonice(walletId, password)
        if (err) {
          return failure(operation, 'EXPORT_MNEMONIC_FAILED', err.message || String(err))
        }
        return success(operation, { mnemonic: result?.mnemonic })
      }

      case 'wallet.change_password': {
        const oldPassword = requireString(params, 'old_password')
        const newPassword = requireString(params, 'new_password')
        return unwrap(operation, walletManager.changePassword(oldPassword, newPassword))
      }

      case 'wallet.send_assets': {
        const readyError = requireWalletReady(operation)
        if (readyError) return readyError
        const to = requireString(params, 'to')
        const asset = requireString(params, 'asset')
        const amount = requireAmount(params)
        const layer = normalizeLayer(params)
        if (layer === 'satsnet') {
          return unwrap(operation, walletManager.sendAssets_SatsNet(to, asset, amount, optionalString(params, 'memo')))
        }
        return unwrap(operation, walletManager.sendAssets(to, asset, amount, optionalNumber(params, 'fee_rate', 1)))
      }

      case 'wallet.transaction':
      case 'stp.transaction':
        return pollTransactions(operation, params)

      case 'stp.safety_snapshot':
        return safetySnapshot(operation, params)

      case 'stp.commitment_export':
        return commitmentExport(operation, params)

      case 'stp.punish_status':
        return unwrapJson(operation, satsnetStp.punishStatus(getChannelId(params)))

      case 'stp.punish_build': {
        const commitTxId = requireString(params, 'commit_txid')
        return unwrapJson(operation, satsnetStp.punishBuild(getChannelId(params), commitTxId))
      }

      case 'stp.punish_broadcast': {
        const commitTxId = requireString(params, 'commit_txid')
        return unwrapJson(operation, satsnetStp.punishBroadcast(getChannelId(params), commitTxId))
      }

      case 'stp.force_close_plan':
        return forceClosePlan(operation, params)

      case 'stp.sweep_build': {
        const sweep = await safeTuple(() => satsnetStp.sweepBuild(
          getChannelId(params),
          optionalString(params, 'commit_txid'),
          params.height,
          Boolean(params.broadcast)
        ))
        if (sweep.result) {
          return success(operation, jsonSuccessPayload(sweep.result))
        }
        return failure(
          operation,
          'MISSING_SWEEP_BUILD',
          sweep.error || 'The PWA Agent Adapter cannot dry-run or broadcast sweep transactions yet.'
        )
      }

      case 'stp.open': {
        const readyError = requireWalletReady(operation)
        if (readyError) return readyError
        const amount = Number(params.amount_sats ?? params.amount)
        if (!Number.isFinite(amount) || amount <= 0) {
          return failure(operation, 'INVALID_AMOUNT', 'stp.open requires amount_sats.')
        }
        return unwrap(
          operation,
          satsnetStp.openChannel(
            optionalNumber(params, 'fee_rate', 1),
            amount,
            asStringArray(params.utxos),
            optionalString(params, 'memo')
          )
        )
      }

      case 'stp.close': {
        const readyError = requireWalletReady(operation)
        if (readyError) return readyError
        const channelId = getChannelId(params)
        if (!channelId) {
          return failure(operation, 'MISSING_CHANNEL_ID', 'stp.close requires channel_id.')
        }
        const safetyError = await requireStpSafetyForValueMovement(operation, params)
        if (safetyError) return safetyError
        return unwrap(
          operation,
          satsnetStp.closeChannel(channelId, optionalNumber(params, 'fee_rate', 1), Boolean(params.force))
        )
      }

      case 'stp.splicing_in': {
        const readyError = requireWalletReady(operation)
        if (readyError) return readyError
        const channelId = getChannelId(params)
        if (!channelId) {
          return failure(operation, 'MISSING_CHANNEL_ID', 'stp.splicing_in requires channel_id.')
        }
        const safetyError = await requireStpSafetyForValueMovement(operation, params)
        if (safetyError) return safetyError
        return unwrap(
          operation,
          satsnetStp.splicingIn(
            channelId,
            requireString(params, 'asset'),
            asStringArray(params.utxos),
            asStringArray(params.fees),
            optionalNumber(params, 'fee_rate', 1),
            requireAmount(params)
          )
        )
      }

      case 'stp.splicing_out': {
        const readyError = requireWalletReady(operation)
        if (readyError) return readyError
        const channelId = getChannelId(params)
        if (!channelId) {
          return failure(operation, 'MISSING_CHANNEL_ID', 'stp.splicing_out requires channel_id.')
        }
        const safetyError = await requireStpSafetyForValueMovement(operation, params)
        if (safetyError) return safetyError
        return unwrap(
          operation,
          satsnetStp.splicingOut(
            channelId,
            requireString(params, 'to'),
            requireString(params, 'asset'),
            asStringArray(params.fees),
            optionalNumber(params, 'fee_rate', 1),
            requireAmount(params)
          )
        )
      }

      case 'stp.lock': {
        const readyError = requireWalletReady(operation)
        if (readyError) return readyError
        const channelId = getChannelId(params)
        if (!channelId) {
          return failure(operation, 'MISSING_CHANNEL_ID', 'stp.lock requires channel_id.')
        }
        const safetyError = await requireStpSafetyForValueMovement(operation, params)
        if (safetyError) return safetyError
        return unwrap(
          operation,
          satsnetStp.lockToChannel(
            channelId,
            requireString(params, 'asset'),
            requireAmount(params),
            asStringArray(params.utxos),
            params.fee_utxos
          )
        )
      }

      case 'stp.lock_with_expand': {
        const readyError = requireWalletReady(operation)
        if (readyError) return readyError
        const channelId = getChannelId(params)
        if (!channelId) {
          return failure(operation, 'MISSING_CHANNEL_ID', 'stp.lock_with_expand requires channel_id.')
        }
        const safetyError = await requireStpSafetyForValueMovement(operation, params)
        if (safetyError) return safetyError
        return unwrap(
          operation,
          satsnetStp.lockToChannelWithExpand(
            channelId,
            requireString(params, 'asset'),
            requireAmount(params),
            optionalNumber(params, 'fee_rate', 1)
          )
        )
      }

      case 'stp.unlock': {
        const readyError = requireWalletReady(operation)
        if (readyError) return readyError
        const channelId = getChannelId(params)
        if (!channelId) {
          return failure(operation, 'MISSING_CHANNEL_ID', 'stp.unlock requires channel_id.')
        }
        const safetyError = await requireStpSafetyForValueMovement(operation, params)
        if (safetyError) return safetyError
        return unwrap(
          operation,
          satsnetStp.unlockFromChannel(
            channelId,
            requireString(params, 'asset'),
            requireAmount(params),
            params.fee_utxos
          )
        )
      }

      default:
        return failure(operation, 'UNSUPPORTED_OPERATION', `Unsupported Agent operation: ${operation}`)
    }
  } catch (error: any) {
    return failure(operation, 'INVALID_PARAMS', error?.message || String(error))
  }
}
