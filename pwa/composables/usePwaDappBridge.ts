import { onBeforeUnmount, ref } from 'vue'
import { Message } from '@/types/message'
import {
  SAT20_DAPP_PROTOCOL,
  type Sat20DappEvent,
  type Sat20DappRequest,
  type Sat20DappResponse,
} from '@/types/sat20-dapp-connect'
import { ApprovalHandler } from '@/composables/webview-bridge/utils/approval-handler'
import { BrowserManager } from '@/composables/webview-bridge/utils/browser-manager'
import { getCurrentDappScope, isDappCapabilityGranted } from '@/lib/authorized-origins'
import { getAllowedDappOrigins } from '@/lib/dapp-origin-policy'
import { getDappActionPolicy } from '@/lib/dapp-policy'
import { normalizeCapabilities } from '@/lib/dapp-grant-model'
import { DappRequestGuard } from '@/lib/dapp-request-guard'
import sat20Wallet from '@/utils/sat20'
import { assertWalletIdentityReady } from '@/lib/identity-boundary'
import { assertNoMnemonicView } from '@/lib/sensitive-session'
import { buildWalletMessagePayload, walletMessageDigest } from '@/lib/wallet-message-domain'
import { toAssetAmountString, toBoundedNumber, toDecimalString } from '@/lib/strict-integers'

const getAllowedOrigins = () => {
  return getAllowedDappOrigins({
    configured: import.meta.env.VITE_SAT20_DAPP_ALLOWED_ORIGINS,
    development: import.meta.env.DEV,
    test: import.meta.env.MODE === 'test',
    currentProtocol: window.location.protocol,
    currentHostname: window.location.hostname,
  })
}

const ACTION_ALIASES: Record<string, Message.MessageAction | string> = {
  requestAccounts: Message.MessageAction.REQUEST_ACCOUNTS,
  getAccounts: Message.MessageAction.GET_ACCOUNTS,
  getPublicKey: Message.MessageAction.GET_PUBLIC_KEY,
  getNetwork: Message.MessageAction.GET_NETWORK,
  switchNetwork: Message.MessageAction.SWITCH_NETWORK,
  signPsbt: Message.MessageAction.SIGN_PSBT,
  signMessage: Message.MessageAction.SIGN_MESSAGE,
  signData: Message.MessageAction.SIGN_DATA,
  pushTx: Message.MessageAction.PUSH_TX,
  pushPsbt: Message.MessageAction.PUSH_PSBT,
  buildBatchSellOrder: Message.MessageAction.BUILD_BATCH_SELL_ORDER,
  buildBatchSellOrder_SatsNet: Message.MessageAction.BUILD_BATCH_SELL_ORDER,
  splitBatchSignedPsbt_SatsNet: Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT_SATSNET,
  mergeBatchSignedPsbt_SatsNet: Message.MessageAction.MERGE_BATCH_SIGNED_PSBT,
  finalizeSellOrder_SatsNet: Message.MessageAction.FINALIZE_SELL_ORDER,
  extractTxFromPsbt: Message.MessageAction.EXTRACT_TX_FROM_PSBT,
  getUtxosWithAsset: Message.MessageAction.GET_UTXOS_WITH_ASSET,
  getUtxosWithAsset_SatsNet: Message.MessageAction.GET_UTXOS_WITH_ASSET_SATSNET,
  getAssetAmount: Message.MessageAction.GET_ASSET_AMOUNT,
  getAssetAmount_SatsNet: Message.MessageAction.GET_ASSET_AMOUNT_SATSNET,
  lockUtxo: Message.MessageAction.LOCK_UTXO,
  lockUtxo_SatsNet: Message.MessageAction.LOCK_UTXO_SATSNET,
  unlockUtxo: Message.MessageAction.UNLOCK_UTXO,
  unlockUtxo_SatsNet: Message.MessageAction.UNLOCK_UTXO_SATSNET,
  deployContract_Remote: Message.MessageAction.DEPLOY_CONTRACT_REMOTE,
  invokeContract_SatsNet: Message.MessageAction.INVOKE_CONTRACT_SATSNET,
  invokeUnifiedContract: Message.MessageAction.INVOKE_UNIFIED_CONTRACT,
  invokeContractV2: Message.MessageAction.INVOKE_CONTRACT_V2,
  invokeContractV2_SatsNet: Message.MessageAction.INVOKE_CONTRACT_V2_SATSNET,
  getParamForInvokeContract: Message.MessageAction.QUERY_PARAM_FOR_INVOKE_CONTRACT,
  getSupportedContracts: 'getSupportedContracts',
  getDeployedContractStatus: 'getDeployedContractStatus',
  registerAsReferrer: Message.MessageAction.REGISTER_AS_REFERRER,
  bindReferrerForServer: Message.MessageAction.BIND_REFERRER_FOR_SERVER,
  batchSendAssets_SatsNet: Message.MessageAction.BATCH_SEND_ASSETS_SATSNET,
  batchSendAssetsV2_SatsNet: Message.MessageAction.BATCH_SEND_ASSETS_V2_SATSNET,
}

const MESSAGE_ACTION_BY_KEY = Message.MessageAction as unknown as Record<string, Message.MessageAction | string>

const normaliseAction = (action: string) =>
  ACTION_ALIASES[action] ?? MESSAGE_ACTION_BY_KEY[action] ?? action

const asParamsObject = (action: string, params: unknown): Record<string, unknown> => {
  if (!Array.isArray(params)) {
    return params && typeof params === 'object' ? params as Record<string, unknown> : {}
  }

  switch (action) {
    case 'signPsbt':
      return { psbtHex: params[0], options: params[1] ?? {} }
    case 'signMessage':
      return { message: params[0] }
    case 'signData':
      return { message: params[0], signData: true }
    case 'pushTx':
      return { rawtx: params[0], options: params[1] ?? {} }
    case 'pushPsbt':
      return { psbtHex: params[0], options: params[1] ?? {} }
    case 'buildBatchSellOrder':
      return { utxos: params[0], address: params[1], network: params[2], chain: 'btc' }
    case 'buildBatchSellOrder_SatsNet':
      return { utxos: params[0], address: params[1], network: params[2], chain: 'satsnet' }
    case 'splitBatchSignedPsbt_SatsNet':
      return { signedHex: params[0], network: params[1] }
    case 'mergeBatchSignedPsbt_SatsNet':
      return { psbts: params[0], network: params[1] }
    case 'finalizeSellOrder_SatsNet':
      return {
        psbtHex: params[0],
        utxos: params[1],
        buyerAddress: params[2],
        serverAddress: params[3],
        network: params[4],
        serviceFee: toDecimalString(params[5], 'serviceFee'),
        networkFee: toDecimalString(params[6], 'networkFee'),
      }
    case 'extractTxFromPsbt':
      return { psbtHex: params[0], chain: params[1]?.chain ?? params[1] ?? 'btc' }
    case 'getUtxosWithAsset':
    case 'getUtxosWithAsset_SatsNet':
      return { address: params[0], assetName: params[1], amt: toAssetAmountString(params[2], 'amount') }
    case 'getAssetAmount':
    case 'getAssetAmount_SatsNet':
      return { address: params[0], assetName: params[1] }
    case 'lockUtxo':
    case 'lockUtxo_SatsNet':
      return { address: params[0], utxo: params[1], reason: params[2] }
    case 'unlockUtxo':
    case 'unlockUtxo_SatsNet':
      return { address: params[0], utxo: params[1] }
    case 'getParamForInvokeContract':
      return { templateName: params[0], action: params[1] }
    case 'registerAsReferrer':
      return { name: params[0], feeRate: toDecimalString(params[1], 'feeRate') }
    case 'bindReferrerForServer':
      return { referrerName: params[0], serverPubKey: params[1] }
    case 'batchSendAssets_SatsNet':
      return { assetName: params[0], amt: toAssetAmountString(params[1], 'amount'), n: toBoundedNumber(params[2], 'count', 1_000) }
    case 'batchSendAssetsV2_SatsNet':
      return {
		destAddr: params[0],
		assetName: params[1],
		amtList: Array.isArray(params[2])
			  ? params[2].map((amount, index) => toAssetAmountString(amount, `amount[${index}]`))
		  : (() => { throw new Error('amount list must be an array') })(),
	  }
    case 'getDeployedContractStatus':
      return { url: params[0] }
    case 'deployContract_Remote':
      return { templateName: params[0], content: params[1], feeRate: toDecimalString(params[2], 'feeRate'), bol: params[3] }
    case 'invokeContract_SatsNet':
      return { url: params[0], invoke: params[1], feeRate: toDecimalString(params[2], 'feeRate') }
    case 'invokeUnifiedContract':
    case 'INVOKE_UNIFIED_CONTRACT':
      return { req: params[0] }
    case 'invokeContractV2_SatsNet':
      return { url: params[0], invoke: params[1], assetName: params[2], amt: toAssetAmountString(params[3], 'amount'), feeRate: toDecimalString(params[4], 'feeRate'), metadata: params[5] ?? {} }
    case 'invokeContractV2':
      return {
        url: params[0],
        invoke: params[1],
        assetName: params[2],
        amt: toAssetAmountString(params[3], 'amount'),
        feeRate: toDecimalString(params[4], 'feeRate'),
        metadata: params[5] ?? {},
      }
    default:
      return { args: params }
  }
}

const unwrapTuple = <T>(tuple: [Error | undefined, T | undefined]) => {
  const [error, result] = tuple
  if (error) {
    throw error
  }
  return result
}

export function usePwaDappBridge(iframeWindow: () => Window | null, currentUrl: () => string) {
  const isReady = ref(false)
  const lastError = ref<string | null>(null)
  const pendingRequests = ref(0)
  const requestGuard = new DappRequestGuard()

  const allowedOrigins = getAllowedOrigins()

  const approvalHandler = new ApprovalHandler(new BrowserManager())

  const postToDapp = (message: Sat20DappResponse | Sat20DappEvent, targetOrigin: string) => {
    iframeWindow()?.postMessage(message, targetOrigin)
  }

  const isAllowedOrigin = (origin: string) => allowedOrigins.has(origin)

  const validateRequest = async (event: MessageEvent, request: Sat20DappRequest) => {
	assertNoMnemonicView()
	const identityGeneration = assertWalletIdentityReady()
    if (!isAllowedOrigin(event.origin)) {
      throw new Error(`Origin is not allowed: ${event.origin}`)
    }
    if (event.source !== iframeWindow()) {
      throw new Error('Request source does not match active DApp frame')
    }
    if (!request.requestId || !request.action || !request.nonce || !request.timestamp || !request.expiresAt) {
      throw new Error('Invalid SAT20 DApp request envelope')
    }
    if (request.origin && request.origin !== event.origin) {
      throw new Error('Request origin does not match message origin')
    }

    const action = normaliseAction(request.action)
    const actionPolicy = getDappActionPolicy(action)
    if (!actionPolicy) throw new Error(`Unsupported DApp action: ${action}`)
    const finish = requestGuard.begin(event.origin, request.requestId, request.nonce, request.expiresAt)
    try {
      const scope = await getCurrentDappScope()
	  assertWalletIdentityReady(identityGeneration)
      if (request.network && request.network !== scope.network) throw new Error('DApp request network does not match the active wallet')
      if (action === Message.MessageAction.REQUEST_ACCOUNTS) {
        const requested = (request.params && typeof request.params === 'object' && !Array.isArray(request.params))
          ? (request.params as Record<string, unknown>).capabilities
          : undefined
        if (requested !== undefined && !normalizeCapabilities(requested)) throw new Error('Invalid DApp capabilities request')
      } else {
        if (!actionPolicy.capability || !await isDappCapabilityGranted(event.origin, scope, actionPolicy.capability)) {
          throw new Error(`DApp capability is not granted for ${action}`)
        }
      }
      return { action, actionPolicy, scope, finish, identityGeneration }
    } catch (error) {
      finish()
      throw error
    }
  }

  const executeAction = async (
    request: Sat20DappRequest,
    eventOrigin: string,
    action: string,
    actionPolicy: NonNullable<ReturnType<typeof getDappActionPolicy>>,
    scope: Awaited<ReturnType<typeof getCurrentDappScope>>,
	identityGeneration: number,
  ) => {
    const params = asParamsObject(request.action, request.params)
	if (action === Message.MessageAction.SIGN_DATA) {
	  throw new Error('Raw DApp signData is disabled; use signMessage with the SAT20 Wallet Message domain')
	}
	if (action === Message.MessageAction.SIGN_MESSAGE) {
	  const message = typeof params.message === 'string' ? params.message : ''
	  const domainPayload = buildWalletMessagePayload({
		network: scope.network,
		origin: eventOrigin,
		timestamp: request.timestamp,
		nonce: request.nonce,
		message,
	  })
	  Object.assign(params, {
		message,
		domainPayload,
		digest: await walletMessageDigest(domainPayload),
		origin: eventOrigin,
		network: scope.network,
		timestamp: request.timestamp,
		nonce: request.nonce,
	  })
	}
    if ([
      Message.MessageAction.LOCK_UTXO,
      Message.MessageAction.LOCK_UTXO_SATSNET,
      Message.MessageAction.UNLOCK_UTXO,
      Message.MessageAction.UNLOCK_UTXO_SATSNET,
    ].includes(action as Message.MessageAction)) {
      params.__dappOwner = {
        origin: eventOrigin,
        network: scope.network,
        wallet_fingerprint: scope.walletFingerprint,
        account_index: scope.accountIndex,
      }
    }

    const context = { origin: eventOrigin, url: currentUrl() || eventOrigin, expiresAt: request.expiresAt, identityGeneration }

    if (actionPolicy.approval === 'fail-closed') {
      throw new Error(`Wallet approval renderer is unavailable for ${action}`)
    }
    if (actionPolicy.approval === 'component') {
      return approvalHandler.handleWalletApproval(
        action as Message.MessageAction,
        params,
        request.requestId,
        context,
      )
    }
    if (actionPolicy.approval === 'component-then-direct') {
      return approvalHandler.handleApprovedDirectRequest(
        action as Message.MessageAction,
        params,
        request.requestId,
        context,
      )
    }

    if (action === 'getSupportedContracts') {
      return unwrapTuple(await sat20Wallet.getSupportedContracts())
    }

    if (action === 'getDeployedContractStatus') {
      return unwrapTuple(await sat20Wallet.getDeployedContractStatus(String(params.url ?? '')))
    }

    return approvalHandler.handleDirectRequest(action as Message.MessageAction, params)
  }

  const handleMessage = async (event: MessageEvent) => {
    const request = event.data as Sat20DappRequest
    if (!request || request.protocol !== SAT20_DAPP_PROTOCOL || request.type !== 'SAT20_DAPP_REQUEST') {
      return
    }

    lastError.value = null
    let finish: (() => void) | undefined

    try {
      const validated = await validateRequest(event, request)
      finish = validated.finish
      pendingRequests.value += 1
      const result = await executeAction(request, event.origin, validated.action, validated.actionPolicy, validated.scope, validated.identityGeneration)
      postToDapp({
        type: 'SAT20_DAPP_RESPONSE',
        protocol: SAT20_DAPP_PROTOCOL,
        requestId: request.requestId,
        success: true,
        result,
      }, event.origin)
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      lastError.value = message
      postToDapp({
        type: 'SAT20_DAPP_RESPONSE',
        protocol: SAT20_DAPP_PROTOCOL,
        requestId: request.requestId,
        success: false,
        error: {
          code: 'SAT20_PWA_REQUEST_FAILED',
          message,
        },
      }, event.origin)
    } finally {
      if (finish) {
        finish()
        pendingRequests.value = Math.max(0, pendingRequests.value - 1)
      }
    }
  }

  const start = () => {
    window.addEventListener('message', handleMessage)
  }

  const stop = () => {
    window.removeEventListener('message', handleMessage)
  }

  const announceReady = (targetOrigin: string, payload: Record<string, unknown> = {}) => {
    isReady.value = true
    postToDapp({
      type: 'SAT20_DAPP_EVENT',
      protocol: SAT20_DAPP_PROTOCOL,
      event: 'ready',
      payload: {
        mode: 'pwa-embedded',
        version: '1.0.0',
        origin: window.location.origin,
        methods: Object.keys(ACTION_ALIASES),
        ...payload,
      },
    }, targetOrigin)
  }

  const announceEvent = (event: Sat20DappEvent['event'], targetOrigin: string, payload?: unknown) => {
    if (!isReady.value) {
      return
    }

    postToDapp({
      type: 'SAT20_DAPP_EVENT',
      protocol: SAT20_DAPP_PROTOCOL,
      event,
      payload,
    }, targetOrigin)
  }

  onBeforeUnmount(stop)

  return {
    isReady,
    lastError,
    pendingRequests,
    start,
    stop,
    announceReady,
    announceEvent,
    isAllowedOrigin,
  }
}
