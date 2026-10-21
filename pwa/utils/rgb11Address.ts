import { beginVersionDispatch } from '@/utils/pwaVersionPolicy'
import { tryit } from 'radash'
import { beginPwaWalletOperation, finishPwaOperation } from '@/utils/pwaOperationLog'
import { walletRequestSessionGuard } from '@/lib/walletSession'

type WasmResponse<T> = {
  code: number
  msg: string
  data?: T
}

const invoke = async <T>(methodName: string, args: unknown[], recordOperation: boolean): Promise<[Error | undefined, T | undefined]> => {
  return tryit(async () => {
    const checkSession = walletRequestSessionGuard(methodName)
    const operation = recordOperation ? await beginPwaWalletOperation(methodName, args as any[]) : null
    let finishVersion: (() => void) | undefined
    try {
      checkSession()
      const method = (globalThis as any).sat20wallet_wasm?.[methodName]
      if (typeof method !== 'function') throw new Error(`RGB WASM method ${methodName} is unavailable`)
      finishVersion = beginVersionDispatch(methodName)
      const response = await method(...args) as WasmResponse<T> | undefined
      checkSession()
      if (response && response.code !== 0) throw new Error(response.msg)
      if (operation) await finishPwaOperation(operation, null, response?.data)
      checkSession()
      return response?.data
    } catch (error) {
      const failure = error instanceof Error ? error : new Error(String(error))
      if (operation) await finishPwaOperation(operation, failure)
      throw failure
    } finally { finishVersion?.() }
  })()
}

const call = async <T>(methodName: string, ...args: unknown[]): Promise<[Error | undefined, T | undefined]> => (
  invoke<T>(methodName, args, true)
)

const backgroundCall = async <T>(methodName: string, ...args: unknown[]): Promise<[Error | undefined, T | undefined]> => (
  invoke<T>(methodName, args, false)
)

export type RGB11AddressReceiveRequest = {
  ttl?: number
  flags?: number
}

export type RGB11AddressSendRequest = {
  receiver_address: string
  asset_name: string
  amount_raw: string
  fee_rate?: number
  min_confirmations?: number
  expiry?: number
}

export type RGB11AddressDeliveryRequest = {
  transfer_id: string
  ttl?: number
  inline_limit?: number
}

export type RGB11AddressMailboxRequest = {
  height?: number
  ttl?: number
}

const rgb11Address = {
  enableReceive: (request: RGB11AddressReceiveRequest = {}) => call<{
    endpoint: string
    temporary: boolean
  }>('enableRGB11AddressReceive', JSON.stringify(request)),

  resolveEndpoint: (address: string) => call<{ endpoint: string }>(
    'resolveRGB11AddressEndpoint', address,
  ),

  prepareTransfer: (request: RGB11AddressSendRequest) => call<{
    transfer: string
    endpoint: string
  }>('prepareRGB11AddressTransfer', JSON.stringify(request)),

  deliverAndBroadcast: (request: RGB11AddressDeliveryRequest) => call<{
    result: string
    txid?: string
    temporary: boolean
	awaiting_ack?: boolean
	broadcast?: boolean
  }>('deliverAndBroadcastRGB11AddressTransfer', JSON.stringify(request)),

  syncMailbox: (request: RGB11AddressMailboxRequest = {}) => call<{ result: string }>(
    'syncRGB11AddressMailbox', JSON.stringify(request),
  ),

  syncMailboxInBackground: (request: RGB11AddressMailboxRequest = {}) => backgroundCall<{ result: string }>(
    'syncRGB11AddressMailbox', JSON.stringify(request),
  ),

  carrierWarning: () => call<{ warning: string }>('getRGB11AddressCarrierWarning'),
}

export default rgb11Address
