import { BaseHandler } from './BaseHandler'
import { Message } from '@/types/message'
import { walletError } from '@/types/error'

/**
 * 网络处理器 - 处理网络相关操作
 */
export class NetworkHandler extends BaseHandler {
  /**
   * 获取支持的消息动作
   */
  getSupportedActions(): string[] {
    return [
      Message.MessageAction.SWITCH_NETWORK,
      Message.MessageAction.GET_NETWORK,
      Message.MessageAction.GET_NETWORK_LIST,
      Message.MessageAction.ADD_NETWORK,
      Message.MessageAction.REMOVE_NETWORK,
      Message.MessageAction.GET_NETWORK_STATUS,
      Message.MessageAction.TEST_NETWORK_CONNECTION,
    ]
  }

  /**
   * 处理网络相关消息
   */
  async handle(event: any): Promise<any> {
    return this.handleWithLogging(event, async () => {
      const action = event.action
      const data = this.getEventData(event)

      switch (action) {
        case Message.MessageAction.SWITCH_NETWORK:
          return this.handleSwitchNetwork(data)
        
        case Message.MessageAction.GET_NETWORK:
          return this.handleGetNetwork(data)
        
        case Message.MessageAction.GET_NETWORK_LIST:
          return this.handleGetNetworkList(data)
        
        case Message.MessageAction.ADD_NETWORK:
          return this.handleAddNetwork(data)
        
        case Message.MessageAction.REMOVE_NETWORK:
          return this.handleRemoveNetwork(data)
        
        case Message.MessageAction.GET_NETWORK_STATUS:
          return this.handleGetNetworkStatus(data)
        
        case Message.MessageAction.TEST_NETWORK_CONNECTION:
          return this.handleTestNetworkConnection(data)
        
        default:
          throw new Error(`Unsupported action: ${action}`)
      }
    })
  }

  /**
   * 处理切换网络
   */
  private async handleSwitchNetwork(data: any): Promise<any> {
    this.validateRequiredParams(data, ['network'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.switchNetwork(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取当前网络
   */
  private async handleGetNetwork(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getNetwork(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取网络列表
   */
  private async handleGetNetworkList(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getNetworkList(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理添加网络
   */
  private async handleAddNetwork(data: any): Promise<any> {
    this.validateRequiredParams(data, ['networkConfig'])
    
    // 验证网络配置的必需字段
    const { networkConfig } = data
    this.validateRequiredParams(networkConfig, ['name', 'rpcUrl', 'chainId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.addNetwork(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理移除网络
   */
  private async handleRemoveNetwork(data: any): Promise<any> {
    this.validateRequiredParams(data, ['networkId'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.removeNetwork(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取网络状态
   */
  private async handleGetNetworkStatus(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getNetworkStatus(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理测试网络连接
   */
  private async handleTestNetworkConnection(data: any): Promise<any> {
    this.validateRequiredParams(data, ['rpcUrl'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.testNetworkConnection(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }
}