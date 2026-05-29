export const SAT20_DAPP_PROTOCOL = 'sat20-dapp-connect'

export interface Sat20DappRequest {
  type?: 'SAT20_DAPP_REQUEST'
  protocol?: typeof SAT20_DAPP_PROTOCOL
  requestId: string
  origin?: string
  action: string
  params?: unknown
  network?: string
  nonce: string
  expiresAt: number
}

export interface Sat20DappResponse {
  type: 'SAT20_DAPP_RESPONSE'
  protocol: typeof SAT20_DAPP_PROTOCOL
  requestId: string
  success: boolean
  result?: unknown
  error?: {
    code: string
    message: string
  }
}

export interface Sat20DappEvent {
  type: 'SAT20_DAPP_EVENT'
  protocol: typeof SAT20_DAPP_PROTOCOL
  event: 'ready' | 'disconnect' | 'accountChanged' | 'networkChanged'
  payload?: unknown
}

export type Sat20DappMessage = Sat20DappRequest | Sat20DappResponse | Sat20DappEvent
