import { BaseHandler } from './BaseHandler'
import { Message } from '@/types/message'
import { walletError } from '@/types/error'

/**
 * UTXO处理器 - 处理UTXO相关操作
 */
export class UtxoHandler extends BaseHandler {
  /**
   * 获取支持的消息动作
   */
  getSupportedActions(): string[] {
    return [
      Message.MessageAction.GET_UTXOS,
      Message.MessageAction.LOCK_UTXO,
      Message.MessageAction.UNLOCK_UTXO,
      Message.MessageAction.UNLOCK_UTXO_SATSNET,
      Message.MessageAction.GET_LOCKED_UTXOS,
      Message.MessageAction.LOCK_TO_CHANNEL,
      Message.MessageAction.UNLOCK_FROM_CHANNEL,
    ]
  }

  /**
   * 处理UTXO相关消息
   */
  async handle(event: any): Promise<any> {
    return this.handleWithLogging(event, async () => {
      const action = event.action
      const data = this.getEventData(event)

      switch (action) {
        case Message.MessageAction.GET_UTXOS:
          return this.handleGetUtxos(data)
        
        case Message.MessageAction.LOCK_UTXO:
          return this.handleLockUtxo(data)
        
        case Message.MessageAction.UNLOCK_UTXO:
          return this.handleUnlockUtxo(data)
        
        case Message.MessageAction.UNLOCK_UTXO_SATSNET:
          return this.handleUnlockUtxoSatsnet(data)
        
        case Message.MessageAction.GET_LOCKED_UTXOS:
          return this.handleGetLockedUtxos(data)
        
        case Message.MessageAction.LOCK_TO_CHANNEL:
          return this.handleLockToChannel(data)
        
        case Message.MessageAction.UNLOCK_FROM_CHANNEL:
          return this.handleUnlockFromChannel(data)
        
        default:
          throw new Error(`Unsupported action: ${action}`)
      }
    })
  }

  /**
   * 处理获取UTXO列表
   */
  private async handleGetUtxos(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getUtxos(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理锁定UTXO
   */
  private async handleLockUtxo(data: any): Promise<any> {
    this.validateRequiredParams(data, ['utxoId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.lockUtxo(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理解锁UTXO
   */
  private async handleUnlockUtxo(data: any): Promise<any> {
    this.validateRequiredParams(data, ['utxoId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.unlockUtxo(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理解锁UTXO（SatsNet）
   */
  private async handleUnlockUtxoSatsnet(data: any): Promise<any> {
    this.validateRequiredParams(data, ['utxoId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.unlockUtxoSatsnet(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取已锁定的UTXO列表
   */
  private async handleGetLockedUtxos(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getLockedUtxos(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理锁定到通道
   */
  private async handleLockToChannel(data: any): Promise<any> {
    this.validateRequiredParams(data, ['utxoId', 'channelId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.lockToChannel(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理从通道解锁
   */
  private async handleUnlockFromChannel(data: any): Promise<any> {
    this.validateRequiredParams(data, ['utxoId', 'channelId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.unlockFromChannel(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }
}