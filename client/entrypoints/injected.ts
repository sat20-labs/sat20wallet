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
    private tickerCache: Record<string, any> = {}

    constructor() {
      window.addEventListener('message', (event) => {
        console.log('injected message', event.data);
        
        const { type, data, event: eventName, metadata = {} } = event.data || {}
        const { to } = metadata
        if (to !== Message.MessageTo.INJECTED) return

        // Handle events
        if (type === Message.MessageType.EVENT && eventName) {
          const listeners = this.eventListeners[eventName]
          console.log('listeners', listeners);
          
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
          console.log('Content Script response:', event.data)
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

    async buildBatchSellOrder_SatsNet(
      utxos: string[],
      address: string,
      network: string
    ): Promise<string> {
      return this.send<string>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.BUILD_BATCH_SELL_ORDER,
        data: { utxos, address, network },
      })
    }

    async splitBatchSignedPsbt_SatsNet(
      signedHex: string,
      network: string
    ): Promise<string[]> {
      return this.send<string[]>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT_SATSNET,
        data: { signedHex, network },
      })
    }

    async finalizeSellOrder_SatsNet(
      psbtHex: string,
      utxos: string[],
      buyerAddress: string,
      serverAddress: string,
      network: string,
      serviceFee: number,
      networkFee: number
    ): Promise<{ psbt: string }> {
      return this.send<{ psbt: string }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.FINALIZE_SELL_ORDER,
        data: {
          psbtHex,
          utxos,
          buyerAddress,
          serverAddress,
          network,
          serviceFee,
          networkFee,
        },
      })
    }

    async mergeBatchSignedPsbt_SatsNet(
      psbts: string[],
      network: string
    ): Promise<{ psbt: string }> {
      return this.send<{ psbt: string }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.MERGE_BATCH_SIGNED_PSBT,
        data: { psbts, network },
      })
    }

    async addInputsToPsbt(
      psbtHex: string,
      utxos: string[]
    ): Promise<{ psbt: string }> {
      return this.send<{ psbt: string }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.ADD_INPUTS_TO_PSBT,
        data: { psbtHex, utxos },
      })
    }

    async addOutputsToPsbt(
      psbtHex: string,
      utxos: string[]
    ): Promise<{ psbt: string }> {
      return this.send<{ psbt: string }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.ADD_OUTPUTS_TO_PSBT,
        data: { psbtHex, utxos },
      })
    }

    async splitAsset(assetKey: string, amount: number): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.APPROVE,
        action: Message.MessageAction.SPLIT_ASSET,
        data: { asset_key: assetKey, amount },
      })
    }
    async batchSendAssets_SatsNet(assetName: string, amt: string, n: number): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.APPROVE,
        action: Message.MessageAction.BATCH_SEND_ASSETS_SATSNET,
        data: { assetName, amt, n },
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

    async extractTxFromPsbt(psbtHex: string, chain: string): Promise<string> {
      return this.send<string>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.EXTRACT_TX_FROM_PSBT,
        data: { psbtHex, chain },
      })
    }

    async extractTxFromPsbt_SatsNet(psbtHex: string): Promise<string> {
      return this.send<string>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.EXTRACT_TX_FROM_PSBT_SATSNET,
        data: { psbtHex },
      })
    }

    async lockUtxo(address: string, utxo: any, reason?: string): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.LOCK_UTXO,
        data: { address, utxo, reason },
      })
    }

    async lockUtxo_SatsNet(address: string, utxo: any, reason?: string): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.LOCK_UTXO_SATSNET,
        data: { address, utxo, reason },
      })
    }

    async unlockUtxo(address: string, utxo: any): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.UNLOCK_UTXO,
        data: { address, utxo },
      })
    }

    async unlockUtxo_SatsNet(address: string, utxo: any): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.UNLOCK_UTXO_SATSNET,
        data: { address, utxo },
      })
    }

    async getAllLockedUtxo(address: string): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_ALL_LOCKED_UTXO,
        data: { address },
      })
    }

    async getAllLockedUtxo_SatsNet(address: string): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_ALL_LOCKED_UTXO_SATSNET,
        data: { address },
      })
    }

    async getUtxos(): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_UTXOS,
      })
    }

    async getUtxos_SatsNet(): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_UTXOS_SATSNET,
      })
    }

    async getUtxosWithAsset(address: string, assetName: string, amt: number): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_UTXOS_WITH_ASSET,
        data: { address, assetName, amt },
      })
    }

    async getUtxosWithAsset_SatsNet(address: string, assetName: string, amt: number): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_UTXOS_WITH_ASSET_SATSNET,
        data: { address, assetName, amt },
      })
    }

    async getUtxosWithAssetV2(address: string, assetName: string, amt: number): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_UTXOS_WITH_ASSET_V2,
        data: { address, assetName, amt },
      })
    }

    async getUtxosWithAssetV2_SatsNet(address: string, assetName: string, amt: number): Promise<any> {
      return this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_UTXOS_WITH_ASSET_V2_SATSNET,
        data: { address, assetName, amt },
      })
    }

    async getAssetAmount(address: string, assetName: string): Promise<{ amount: string; value: string }> {
      return this.send<{ amount: string; value: string }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_ASSET_AMOUNT,
        data: { address, assetName },
      });
    }

    async getAssetAmount_SatsNet(address: string, assetName: string): Promise<{ amount: string; value: string }> {
      console.log(Message.MessageAction);
      
      return this.send<{ amount: string; value: string }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_ASSET_AMOUNT_SATSNET,
        data: { address, assetName },
      });
    }

    async getTickerInfo(asset: string): Promise<any> {
      if (this.tickerCache[asset]) {
        return Promise.resolve(this.tickerCache[asset]);
      }
      const result = await this.send<any>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_TICKER_INFO,
        data: { asset },
      });
      this.tickerCache[asset] = result;
      return result;
    }

    // --- 合约相关方法 ---
    async getSupportedContracts(): Promise<{ contractContents: any[] }> {
      return this.send<{ contractContents: any[] }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_SUPPORTED_CONTRACTS,
      })
    }
    async getDeployedContractsInServer(): Promise<{ contractURLs: any[] }> {
      return this.send<{ contractURLs: any[] }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_DEPLOYED_CONTRACTS_IN_SERVER,
      })
    }
    async getDeployedContractStatus(url: string): Promise<{ contractStatus: any }> {
      return this.send<{ contractStatus: any }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_DEPLOYED_CONTRACT_STATUS,
        data: { url },
      })
    }
    async getFeeForDeployContract(templateName: string, content: string, feeRate: string): Promise<{ fee: any }> {
      return this.send<{ fee: any }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_FEE_FOR_DEPLOY_CONTRACT,
        data: { templateName, content, feeRate },
      })
    }
    async getParamForInvokeContract(templateName: string): Promise<{ parameter: any }> {
      return this.send<{ parameter: any }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_PARAM_FOR_INVOKE_CONTRACT,
        data: { templateName },
      })
    }
    async getFeeForInvokeContract(url: string, invoke: string): Promise<{ fee: any }> {
      return this.send<{ fee: any }>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_FEE_FOR_INVOKE_CONTRACT,
        data: { url, invoke },
      })
    }
    // 新增：合约远程部署
    async deployContract_Remote(templateName: string, content: string, feeRate: string, bol: boolean): Promise<{ txId: string; resvId: string }> {
      return this.send<{ txId: string; resvId: string }>({
        type: Message.MessageType.APPROVE,
        action: Message.MessageAction.DEPLOY_CONTRACT_REMOTE,
        data: { templateName, content, feeRate, bol },
      })
    }
    // 新增：合约调用（SatsNet）
    async invokeContract_SatsNet(url: string, invoke: string, feeRate: string): Promise<{ txId: string }> {
      return this.send<{ txId: string }>({
        type: Message.MessageType.APPROVE,
        action: Message.MessageAction.INVOKE_CONTRACT_SATSNET,
        data: { url, invoke, feeRate },
      })
    }
    async getAddressStatusInContract(url: string, address: string): Promise<string> {
      return this.send<string>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_CONTRACT_STATUS_BY_ADDRESS,
        data: { url, address },
      })
    }
    async getAllAddressInContract(url: string): Promise<string> {
      return this.send<string>({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_CONTRACT_ALL_ADDRESSES,
        data: { url },
      })
    }
  }

  const sat20 = new Sat20()
  window.sat20 = sat20
})
