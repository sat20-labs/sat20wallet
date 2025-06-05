import { BaseHandler } from './BaseHandler'
import { Message } from '@/types/message'
import { walletError } from '@/types/error'

/**
 * 交易处理器 - 处理交易相关操作
 */
export class TransactionHandler extends BaseHandler {
  /**
   * 获取支持的消息动作
   */
  getSupportedActions(): string[] {
    return [
      Message.MessageAction.SEND_BITCOIN,
      Message.MessageAction.SEND_INSCRIPTION,
      Message.MessageAction.PUSH_TX,
      Message.MessageAction.PUSH_PSBT,
      Message.MessageAction.GET_TRANSACTION_HISTORY,
      Message.MessageAction.ESTIMATE_FEE,
      Message.MessageAction.BUILD_ORDER,
      Message.MessageAction.SPLIT_ASSET,
      Message.MessageAction.BATCH_SEND_ASSETS_SATSNET,
    ]
  }

  /**
   * 处理交易相关消息
   */
  async handle(event: any): Promise<any> {
    return this.handleWithLogging(event, async () => {
      const action = event.action
      const data = this.getEventData(event)

      switch (action) {
        case Message.MessageAction.SEND_BITCOIN:
          return this.handleSendBitcoin(data)
        
        case Message.MessageAction.SEND_INSCRIPTION:
          return this.handleSendInscription(data)
        
        case Message.MessageAction.PUSH_TX:
          return this.handlePushTransaction(data)
        
        case Message.MessageAction.PUSH_PSBT:
          return this.handlePushPsbt(data)
        
        case Message.MessageAction.GET_TRANSACTION_HISTORY:
          return this.handleGetTransactionHistory(data)
        
        case Message.MessageAction.ESTIMATE_FEE:
          return this.handleEstimateFee(data)
        
        case Message.MessageAction.BUILD_ORDER:
          return this.handleBuildOrder(data)
        
        case Message.MessageAction.SPLIT_ASSET:
          return this.handleSplitAsset(data)
        
        case Message.MessageAction.BATCH_SEND_ASSETS_SATSNET:
          return this.handleBatchSendAssets(data)
        
        default:
          throw new Error(`Unsupported action: ${action}`)
      }
    })
  }

  /**
   * 处理发送比特币
   */
  private async handleSendBitcoin(data: any): Promise<any> {
    this.validateRequiredParams(data, ['to', 'amount'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.sendBitcoin(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理发送铭文
   */
  private async handleSendInscription(data: any): Promise<any> {
    this.validateRequiredParams(data, ['to', 'inscriptionId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.sendInscription(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理推送交易
   */
  private async handlePushTransaction(data: any): Promise<any> {
    this.validateRequiredParams(data, ['txHex'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.pushTx(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理推送PSBT
   */
  private async handlePushPsbt(data: any): Promise<any> {
    this.validateRequiredParams(data, ['psbt'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.pushPsbt(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取交易历史
   */
  private async handleGetTransactionHistory(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getTransactionHistory(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理估算手续费
   */
  private async handleEstimateFee(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.estimateFee(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理构建订单
   */
  private async handleBuildOrder(data: any): Promise<any> {
    this.validateRequiredParams(data, ['orderType'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.buildOrder(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理资产分割
   */
  private async handleSplitAsset(data: any): Promise<any> {
    this.validateRequiredParams(data, ['assetId', 'amounts'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.splitAsset(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理批量发送资产（SatsNet）
   */
  private async handleBatchSendAssets(data: any): Promise<any> {
    this.validateRequiredParams(data, ['transfers'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.batchSendAssetsSatsnet(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }
}