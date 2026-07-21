import { tryit } from 'radash'

type WasmResponse<T> = {
  code: number
  msg: string
  data?: T
}

const call = async <T>(methodName: string, ...args: unknown[]): Promise<[Error | undefined, T | undefined]> => {
  const method = (globalThis as any).sat20wallet_wasm?.[methodName]
  if (typeof method !== 'function') {
    return [new Error(`RGB11 WASM method ${methodName} is unavailable`), undefined]
  }
  const [invokeError, raw] = await tryit(method)(...args)
  if (invokeError) return [invokeError, undefined]
  const response = raw as WasmResponse<T> | undefined
  if (!response) return [undefined, undefined]
  if (response.code !== 0) return [new Error(response.msg), undefined]
  return [undefined, response.data]
}

export type RGB11AddressReceiveRequest = {
  ttl?: number
  expiry_height?: number
  autopay?: boolean
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
  expiry_height?: number
  autopay?: boolean
  inline_limit?: number
}

export type RGB11AddressMailboxRequest = {
  height?: number
  now?: number
  ttl?: number
  expiry_height?: number
  autopay?: boolean
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
    txid: string
    temporary: boolean
  }>('deliverAndBroadcastRGB11AddressTransfer', JSON.stringify(request)),

  syncMailbox: (request: RGB11AddressMailboxRequest = {}) => call<{ result: string }>(
    'syncRGB11AddressMailbox', JSON.stringify(request),
  ),

  carrierWarning: () => call<{ warning: string }>('getRGB11AddressCarrierWarning'),
}

export default rgb11Address
