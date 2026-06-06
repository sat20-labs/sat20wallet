import { useChannelStore } from '@/store/channel'
import { useWalletStore } from '@/store/wallet'
import walletManager from '@/utils/sat20'
import satsnetStp from '@/utils/stp'

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
        return failure(operation, 'NOT_IMPLEMENTED', 'Transaction polling is not implemented in the PWA Agent Adapter yet.')

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
