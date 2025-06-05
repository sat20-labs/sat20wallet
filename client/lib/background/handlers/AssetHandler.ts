import { BaseHandler } from './BaseHandler'
import { Message } from '@/types/message'
import { walletError } from '@/types/error'

/**
 * 资产处理器 - 处理资产相关操作
 */
export class AssetHandler extends BaseHandler {
  /**
   * 获取支持的消息动作
   */
  getSupportedActions(): string[] {
    return [
      Message.MessageAction.GET_ASSET_INFO,
      Message.MessageAction.GET_ASSET_UTXOS,
      Message.MessageAction.GET_ASSET_BALANCE,
      Message.MessageAction.GET_ASSET_LIST,
      Message.MessageAction.GET_ASSET_HISTORY,
      Message.MessageAction.TRANSFER_ASSET,
      Message.MessageAction.MINT_ASSET,
      Message.MessageAction.BURN_ASSET,
    ]
  }

  /**
   * 处理资产相关消息
   */
  async handle(event: any): Promise<any> {
    return this.handleWithLogging(event, async () => {
      const action = event.action
      const data = this.getEventData(event)

      switch (action) {
        case Message.MessageAction.GET_ASSET_INFO:
          return this.handleGetAssetInfo(data)
        
        case Message.MessageAction.GET_ASSET_UTXOS:
          return this.handleGetAssetUtxos(data)
        
        case Message.MessageAction.GET_ASSET_BALANCE:
          return this.handleGetAssetBalance(data)
        
        case Message.MessageAction.GET_ASSET_LIST:
          return this.handleGetAssetList(data)
        
        case Message.MessageAction.GET_ASSET_HISTORY:
          return this.handleGetAssetHistory(data)
        
        case Message.MessageAction.TRANSFER_ASSET:
          return this.handleTransferAsset(data)
        
        case Message.MessageAction.MINT_ASSET:
          return this.handleMintAsset(data)
        
        case Message.MessageAction.BURN_ASSET:
          return this.handleBurnAsset(data)
        
        default:
          throw new Error(`Unsupported action: ${action}`)
      }
    })
  }

  /**
   * 处理获取资产信息
   */
  private async handleGetAssetInfo(data: any): Promise<any> {
    this.validateRequiredParams(data, ['assetId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getAssetInfo(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取资产UTXO
   */
  private async handleGetAssetUtxos(data: any): Promise<any> {
    this.validateRequiredParams(data, ['assetId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getAssetUtxos(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取资产余额
   */
  private async handleGetAssetBalance(data: any): Promise<any> {
    this.validateRequiredParams(data, ['assetId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getAssetBalance(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取资产列表
   */
  private async handleGetAssetList(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getAssetList(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取资产历史
   */
  private async handleGetAssetHistory(data: any): Promise<any> {
    this.validateRequiredParams(data, ['assetId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getAssetHistory(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理转移资产
   */
  private async handleTransferAsset(data: any): Promise<any> {
    this.validateRequiredParams(data, ['assetId', 'to', 'amount'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.transferAsset(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理铸造资产
   */
  private async handleMintAsset(data: any): Promise<any> {
    this.validateRequiredParams(data, ['assetId', 'amount'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.mintAsset(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理销毁资产
   */
  private async handleBurnAsset(data: any): Promise<any> {
    this.validateRequiredParams(data, ['assetId', 'amount'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.burnAsset(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }
}