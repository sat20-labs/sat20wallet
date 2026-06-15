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
import { isOriginAuthorized } from '@/lib/authorized-origins'
import sat20Wallet from '@/utils/sat20'

const DEFAULT_ALLOWED_ORIGINS = [
  'https://app.ordx.market',
  'https://satsnet.ordx.market',
  'https://satsnet.test.ordx.market',
  'http://localhost:3000',
  'http://localhost:3001',
  'http://127.0.0.1:3001',
  'http://localhost:3006',
  'http://127.0.0.1:3006',
  'http://localhost:3007',
  'http://127.0.0.1:3007',
  'http://localhost:5173',
  'http://127.0.0.1:5173',
]

const getAllowedOrigins = () => {
  const origins = new Set(
    (import.meta.env.VITE_SAT20_DAPP_ALLOWED_ORIGINS?.split(',') ?? DEFAULT_ALLOWED_ORIGINS)
      .map((origin: string) => origin.trim())
      .filter(Boolean)
  )

  if (import.meta.env.DEV) {
    ;[3001, 3006, 3007, 5173].forEach((port) => {
      origins.add(`${window.location.protocol}//${window.location.hostname}:${port}`)
    })
  }

  return origins
}

const ACTION_ALIASES: Record<string, Message.MessageAction | string> = {
  requestAccounts: Message.MessageAction.REQUEST_ACCOUNTS,
  getAccounts: Message.MessageAction.GET_ACCOUNTS,
  getPublicKey: Message.MessageAction.GET_PUBLIC_KEY,
  getNetwork: Message.MessageAction.GET_NETWORK,
  switchNetwork: Message.MessageAction.SWITCH_NETWORK,
  signPsbt: Message.MessageAction.SIGN_PSBT,
  signMessage: Message.MessageAction.SIGN_MESSAGE,
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

const APPROVAL_ACTIONS = new Set<string>([
  Message.MessageAction.REQUEST_ACCOUNTS,
  Message.MessageAction.SWITCH_NETWORK,
  Message.MessageAction.SIGN_MESSAGE,
  Message.MessageAction.SIGN_PSBT,
  Message.MessageAction.DEPLOY_CONTRACT_REMOTE,
  Message.MessageAction.INVOKE_CONTRACT_SATSNET,
  Message.MessageAction.INVOKE_CONTRACT_V2,
  Message.MessageAction.INVOKE_CONTRACT_V2_SATSNET,
  Message.MessageAction.REGISTER_AS_REFERRER,
  Message.MessageAction.BIND_REFERRER_FOR_SERVER,
  Message.MessageAction.SEND_ASSETS_SATSNET,
  Message.MessageAction.BATCH_SEND_ASSETS_SATSNET,
  Message.MessageAction.BATCH_SEND_ASSETS_V2_SATSNET,
])

const normaliseAction = (action: string) => ACTION_ALIASES[action] ?? action

const asParamsObject = (action: string, params: unknown): Record<string, unknown> => {
  if (!Array.isArray(params)) {
    return params && typeof params === 'object' ? params as Record<string, unknown> : {}
  }

  switch (action) {
    case 'signPsbt':
      return { psbtHex: params[0], options: params[1] ?? {} }
    case 'signMessage':
      return { message: params[0] }
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
        serviceFee: params[5],
        networkFee: params[6],
      }
    case 'extractTxFromPsbt':
      return { psbtHex: params[0], chain: params[1]?.chain ?? params[1] ?? 'btc' }
    case 'getUtxosWithAsset':
    case 'getUtxosWithAsset_SatsNet':
      return { address: params[0], assetName: params[1], amt: String(params[2]) }
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
      return { name: params[0], feeRate: params[1] }
    case 'bindReferrerForServer':
      return { referrerName: params[0], serverPubKey: params[1] }
    case 'batchSendAssets_SatsNet':
      return { assetName: params[0], amt: String(params[1]), n: Number(params[2]) }
    case 'batchSendAssetsV2_SatsNet':
      return { destAddr: params[0], assetName: params[1], amtList: params[2] }
    case 'getDeployedContractStatus':
      return { url: params[0] }
    case 'deployContract_Remote':
      return { templateName: params[0], content: params[1], feeRate: params[2], bol: params[3] }
    case 'invokeContract_SatsNet':
      return { url: params[0], invoke: params[1], feeRate: params[2] }
    case 'invokeContractV2_SatsNet':
      return { url: params[0], invoke: params[1], assetName: params[2], amt: String(params[3]), feeRate: String(params[4]), metadata: params[5] ?? {} }
    case 'invokeContractV2':
      return {
        url: params[0],
        invoke: params[1],
        assetName: params[2],
        amt: String(params[3]),
        feeRate: String(params[4]),
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
  const handledRequests = new Set<string>()

  const allowedOrigins = getAllowedOrigins()

  const approvalHandler = new ApprovalHandler(new BrowserManager())

  const postToDapp = (message: Sat20DappResponse | Sat20DappEvent, targetOrigin: string) => {
    iframeWindow()?.postMessage(message, targetOrigin)
  }

  const isAllowedOrigin = (origin: string) => allowedOrigins.has(origin)

  const validateRequest = async (event: MessageEvent, request: Sat20DappRequest) => {
    if (!isAllowedOrigin(event.origin)) {
      throw new Error(`Origin is not allowed: ${event.origin}`)
    }
    if (event.source !== iframeWindow()) {
      throw new Error('Request source does not match active DApp frame')
    }
    if (!request.requestId || !request.action || !request.nonce || !request.expiresAt) {
      throw new Error('Invalid SAT20 DApp request envelope')
    }
    if (request.expiresAt < Date.now()) {
      throw new Error('SAT20 DApp request expired')
    }
    const requestKey = `${event.origin}:${request.requestId}:${request.nonce}`
    if (handledRequests.has(requestKey)) {
      throw new Error('Duplicate SAT20 DApp request')
    }
    if (request.origin && request.origin !== event.origin) {
      throw new Error('Request origin does not match message origin')
    }

    const action = normaliseAction(request.action)
    if (action !== Message.MessageAction.REQUEST_ACCOUNTS) {
      const authorized = await isOriginAuthorized(event.origin)
      if (!authorized) {
        throw new Error('DApp is not connected. Call requestAccounts first.')
      }
    }

    handledRequests.add(requestKey)
  }

  const executeAction = async (request: Sat20DappRequest, eventOrigin: string) => {
    const action = normaliseAction(request.action)
    const params = asParamsObject(request.action, request.params)

    if (APPROVAL_ACTIONS.has(action)) {
      return approvalHandler.handleWalletApproval(
        action as Message.MessageAction,
        params,
        request.requestId,
        currentUrl() || eventOrigin
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

    pendingRequests.value += 1
    lastError.value = null

    try {
      await validateRequest(event, request)
      const result = await executeAction(request, event.origin)
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
      pendingRequests.value = Math.max(0, pendingRequests.value - 1)
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
