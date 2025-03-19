import { Message } from '@/types/message'
import { Network, Balance } from '@/types'

export namespace Sat20WalletTypes {
  export type AccountsChangedEvent = (
    event: 'accountsChanged' | 'networkChanged',
    handler: (accounts: Array<string> | string) => void
  ) => void
}

interface SendData {
  data?: any
  type: Message.MessageType
  action: Message.MessageAction
}
export default defineUnlistedScript(() => {
  class Sat20 {
    private eventListeners: { [key: string]: Function[] } = {}

    constructor() {
      window.addEventListener('message', (event) => {
        const { type, data, event: eventName, metadata = {} } = event.data || {}
        const { to } = metadata
        if (to !== Message.MessageTo.INJECTED) return

        // Handle events
        if (type === Message.MessageType.EVENT && eventName) {
          const listeners = this.eventListeners[eventName]
          if (listeners) {
            listeners.forEach((handler) => handler(data))
          }
        }
      })
    }

    send<T>({ data, type, action }: SendData): Promise<T> {
      return new Promise((resolve, reject) => {
        const channel = new BroadcastChannel(Message.Channel.INJECT_CONTENT)
        const _messageId = `msg_${type}_${action}_${Date.now()}`
        const listener = (event: MessageEvent) => {
          console.log('Content Script response:', event.data);
          const { type, data, error, metadata = {} } = event.data || {}
          const { messageId, to } = metadata
          if (
            ![
              Message.MessageType.APPROVE,
              Message.MessageType.REQUEST,
            ].includes(type) &&
            to !== Message.MessageTo.INJECTED
          )
            return

          if (messageId === _messageId) {
            
            
            if (data) {
              resolve(data)
            }
            if (error) {
              reject(new Error(error.message))
            }
            channel.removeEventListener('message', listener)
            channel.close()
          }
        }
        channel.addEventListener('message', listener)

        window.postMessage({
          metadata: {
            origin: window.location.origin,
            messageId: _messageId,
            from: Message.MessageFrom.INJECTED,
            to: Message.MessageTo.BACKGROUND,
          },
          type,
          action,
          data,
        })

        setTimeout(() => {
          channel.removeEventListener('message', listener)
          channel.close()
          reject(new Error('Content Script response timeout'))
        }, 5000000)
      })
    }

    on: Sat20WalletTypes.AccountsChangedEvent = (event, handler) => {
      if (!this.eventListeners[event]) {
        this.eventListeners[event] = []
      }
      this.eventListeners[event].push(handler)
    }

    removeListener: Sat20WalletTypes.AccountsChangedEvent = (
      event,
      handler
    ) => {
      if (this.eventListeners[event]) {
        this.eventListeners[event] = this.eventListeners[event].filter(
          (h) => h !== handler
        )
      }
    }

    async requestAccounts(): Promise<string[]> {
      return this.send<string[]>({
        type: Message.MessageType.APPROVE,
        action: Message.MessageAction.REQUEST_ACCOUNTS,
      })
    }

    async getAccounts(): Promise<string[]> {
      return this.send<string[]>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_ACCOUNTS,
      })
    }

    async getNetwork(): Promise<Network> {
      return this.send<Network>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_NETWORK,
      })
    }

    async switchNetwork(network: Network): Promise<void> {
      return this.send<void>({
        type: Message.MessageType.APPROVE,
        action: Message.MessageAction.SWITCH_NETWORK,
        data: { network },
      })
    }

    async getPublicKey(): Promise<string> {
      return this.send<string>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_PUBLIC_KEY,
      })
    }

    async getBalance(): Promise<Balance> {
      return this.send<Balance>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_BALANCE,
      })
    }

    async sendBitcoin(
      address: string,
      amount: number,
      options?: { feeRate: number }
    ): Promise<string> {
      return this.send<string>({
        type: Message.MessageType.APPROVE,
        action: Message.MessageAction.SEND_BITCOIN,
        data: { address, amount, options },
      })
    }

    async signMessage(
      message: string,
      type?: 'ecdsa' | 'bip322-simple'
    ): Promise<string> {
      return this.send<string>({
        type: Message.MessageType.APPROVE,
        action: Message.MessageAction.SIGN_MESSAGE,
        data: { message, type },
      })
    }

    async signPsbt(psbtHex: string, options?: any): Promise<string> {
      return this.send<string>({
        type: Message.MessageType.APPROVE,
        action: Message.MessageAction.SIGN_PSBT,
        data: { psbtHex, options },
      })
    }

    async signPsbts(psbtHexs: string[], options?: any): Promise<string[]> {
      return this.send<string[]>({
        type: Message.MessageType.APPROVE,
        action: Message.MessageAction.SIGN_PSBTS,
        data: { psbtHexs, options },
      })
    }

    async pushTx(rawtx: string, options?: any): Promise<string> {
      return this.send<string>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.PUSH_TX,
        data: { rawtx, options },
      })
    }

    async pushPsbt(psbtHex: string, options?: any): Promise<string> {
      return this.send<string>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.PUSH_PSBT,
        data: { psbtHex, options },
      })
    }
  }

  const sat20 = new Sat20()
  window.sat20 = sat20
})
