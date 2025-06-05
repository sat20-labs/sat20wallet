import { BaseHandler } from './BaseHandler'
import { Message } from '@/types/message'
import { walletError } from '@/types/error'

/**
 * PSBT处理器 - 处理PSBT相关操作
 */
export class PsbtHandler extends BaseHandler {
  /**
   * 获取支持的消息动作
   */
  getSupportedActions(): string[] {
    return [
      Message.MessageAction.SIGN_PSBT,
      Message.MessageAction.SIGN_PSBTS,
      Message.MessageAction.SIGN_MESSAGE,
      Message.MessageAction.SPLIT_PSBT,
      Message.MessageAction.MERGE_PSBT,
      Message.MessageAction.ADD_PSBT_INPUT,
      Message.MessageAction.ADD_PSBT_OUTPUT,
      Message.MessageAction.FINALIZE_PSBT,
      Message.MessageAction.EXTRACT_TX_FROM_PSBT,
    ]
  }

  /**
   * 处理PSBT相关消息
   */
  async handle(event: any): Promise<any> {
    return this.handleWithLogging(event, async () => {
      const action = event.action
      const data = this.getEventData(event)

      switch (action) {
        case Message.MessageAction.SIGN_PSBT:
          return this.handleSignPsbt(data)
        
        case Message.MessageAction.SIGN_PSBTS:
          return this.handleSignPsbts(data)
        
        case Message.MessageAction.SIGN_MESSAGE:
          return this.handleSignMessage(data)
        
        case Message.MessageAction.SPLIT_PSBT:
          return this.handleSplitPsbt(data)
        
        case Message.MessageAction.MERGE_PSBT:
          return this.handleMergePsbt(data)
        
        case Message.MessageAction.ADD_PSBT_INPUT:
          return this.handleAddPsbtInput(data)
        
        case Message.MessageAction.ADD_PSBT_OUTPUT:
          return this.handleAddPsbtOutput(data)
        
        case Message.MessageAction.FINALIZE_PSBT:
          return this.handleFinalizePsbt(data)
        
        case Message.MessageAction.EXTRACT_TX_FROM_PSBT:
          return this.handleExtractTxFromPsbt(data)
        
        default:
          throw new Error(`Unsupported action: ${action}`)
      }
    })
  }

  /**
   * 处理签名PSBT
   */
  private async handleSignPsbt(data: any): Promise<any> {
    this.validateRequiredParams(data, ['psbt'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.signPsbt(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理批量签名PSBT
   */
  private async handleSignPsbts(data: any): Promise<any> {
    this.validateRequiredParams(data, ['psbts'])
    
    if (!Array.isArray(data.psbts)) {
      throw new Error('psbts must be an array')
    }
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.signPsbts(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理签名消息
   */
  private async handleSignMessage(data: any): Promise<any> {
    this.validateRequiredParams(data, ['message'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.signMessage(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理分割PSBT
   */
  private async handleSplitPsbt(data: any): Promise<any> {
    this.validateRequiredParams(data, ['psbt', 'splitCount'])
    
    if (typeof data.splitCount !== 'number' || data.splitCount < 2) {
      throw new Error('splitCount must be a number greater than 1')
    }
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.splitPsbt(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理合并PSBT
   */
  private async handleMergePsbt(data: any): Promise<any> {
    this.validateRequiredParams(data, ['psbts'])
    
    if (!Array.isArray(data.psbts) || data.psbts.length < 2) {
      throw new Error('psbts must be an array with at least 2 elements')
    }
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.mergePsbt(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理添加PSBT输入
   */
  private async handleAddPsbtInput(data: any): Promise<any> {
    this.validateRequiredParams(data, ['psbt', 'input'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.addPsbtInput(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理添加PSBT输出
   */
  private async handleAddPsbtOutput(data: any): Promise<any> {
    this.validateRequiredParams(data, ['psbt', 'output'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.addPsbtOutput(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理完成PSBT
   */
  private async handleFinalizePsbt(data: any): Promise<any> {
    this.validateRequiredParams(data, ['psbt'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.finalizePsbt(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理从PSBT提取交易
   */
  private async handleExtractTxFromPsbt(data: any): Promise<any> {
    this.validateRequiredParams(data, ['psbt'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.extractTxFromPsbt(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }
}