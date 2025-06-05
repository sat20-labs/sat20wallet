import { BaseHandler } from './BaseHandler'
import { Message } from '@/types/message'
import { walletError } from '@/types/error'

/**
 * 账户处理器 - 处理账户相关操作
 */
export class AccountHandler extends BaseHandler {
  /**
   * 获取支持的消息动作
   */
  getSupportedActions(): string[] {
    return [
      Message.MessageAction.REQUEST_ACCOUNTS,
      Message.MessageAction.GET_ACCOUNTS,
      Message.MessageAction.GET_CURRENT_ACCOUNT,
      Message.MessageAction.SWITCH_ACCOUNT,
      Message.MessageAction.GET_BALANCE,
      Message.MessageAction.GET_INSCRIPTIONS,
      Message.MessageAction.GET_ACCOUNT_ASSETS,
    ]
  }

  /**
   * 处理账户相关消息
   */
  async handle(event: any): Promise<any> {
    return this.handleWithLogging(event, async () => {
      const action = event.action
      const data = this.getEventData(event)

      switch (action) {
        case Message.MessageAction.REQUEST_ACCOUNTS:
          return this.handleRequestAccounts(data)
        
        case Message.MessageAction.GET_ACCOUNTS:
          return this.handleGetAccounts(data)
        
        case Message.MessageAction.GET_CURRENT_ACCOUNT:
          return this.handleGetCurrentAccount(data)
        
        case Message.MessageAction.SWITCH_ACCOUNT:
          return this.handleSwitchAccount(data)
        
        case Message.MessageAction.GET_BALANCE:
          return this.handleGetBalance(data)
        
        case Message.MessageAction.GET_INSCRIPTIONS:
          return this.handleGetInscriptions(data)
        
        case Message.MessageAction.GET_ACCOUNT_ASSETS:
          return this.handleGetAccountAssets(data)
        
        default:
          throw new Error(`Unsupported action: ${action}`)
      }
    })
  }

  /**
   * 处理请求账户权限
   */
  private async handleRequestAccounts(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.requestAccounts()
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取账户列表
   */
  private async handleGetAccounts(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getAccounts()
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取当前账户
   */
  private async handleGetCurrentAccount(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getCurrentAccount()
      return JSON.parse(result)
    })
  }

  /**
   * 处理切换账户
   */
  private async handleSwitchAccount(data: any): Promise<any> {
    this.validateRequiredParams(data, ['accountIndex'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.switchAccount(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取余额
   */
  private async handleGetBalance(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getBalance(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取铭文列表
   */
  private async handleGetInscriptions(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getInscriptions(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取账户资产
   */
  private async handleGetAccountAssets(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getAccountAssets(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }
}