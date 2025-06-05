import { BaseHandler } from './BaseHandler'
import { Message } from '@/types/message'
import { walletError } from '@/types/error'

/**
 * 合约处理器 - 处理合约相关操作
 */
export class ContractHandler extends BaseHandler {
  /**
   * 获取支持的消息动作
   */
  getSupportedActions(): string[] {
    return [
      Message.MessageAction.GET_SUPPORTED_CONTRACTS,
      Message.MessageAction.DEPLOY_CONTRACT_REMOTE,
      Message.MessageAction.INVOKE_CONTRACT_SATSNET,
      Message.MessageAction.GET_CONTRACT_INFO,
      Message.MessageAction.GET_CONTRACT_STATE,
      Message.MessageAction.GET_CONTRACT_HISTORY,
      Message.MessageAction.ESTIMATE_CONTRACT_FEE,
    ]
  }

  /**
   * 处理合约相关消息
   */
  async handle(event: any): Promise<any> {
    return this.handleWithLogging(event, async () => {
      const action = event.action
      const data = this.getEventData(event)

      switch (action) {
        case Message.MessageAction.GET_SUPPORTED_CONTRACTS:
          return this.handleGetSupportedContracts(data)
        
        case Message.MessageAction.DEPLOY_CONTRACT_REMOTE:
          return this.handleDeployContractRemote(data)
        
        case Message.MessageAction.INVOKE_CONTRACT_SATSNET:
          return this.handleInvokeContractSatsnet(data)
        
        case Message.MessageAction.GET_CONTRACT_INFO:
          return this.handleGetContractInfo(data)
        
        case Message.MessageAction.GET_CONTRACT_STATE:
          return this.handleGetContractState(data)
        
        case Message.MessageAction.GET_CONTRACT_HISTORY:
          return this.handleGetContractHistory(data)
        
        case Message.MessageAction.ESTIMATE_CONTRACT_FEE:
          return this.handleEstimateContractFee(data)
        
        default:
          throw new Error(`Unsupported action: ${action}`)
      }
    })
  }

  /**
   * 处理获取支持的合约列表
   */
  private async handleGetSupportedContracts(data: any): Promise<any> {
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getSupportedContracts(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理远程部署合约
   */
  private async handleDeployContractRemote(data: any): Promise<any> {
    this.validateRequiredParams(data, ['contractCode', 'constructorArgs'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.deployContractRemote(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理调用合约（SatsNet）
   */
  private async handleInvokeContractSatsnet(data: any): Promise<any> {
    this.validateRequiredParams(data, ['contractAddress', 'method', 'args'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.invokeContractSatsnet(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取合约信息
   */
  private async handleGetContractInfo(data: any): Promise<any> {
    this.validateRequiredParams(data, ['contractAddress'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getContractInfo(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取合约状态
   */
  private async handleGetContractState(data: any): Promise<any> {
    this.validateRequiredParams(data, ['contractAddress'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getContractState(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理获取合约历史
   */
  private async handleGetContractHistory(data: any): Promise<any> {
    this.validateRequiredParams(data, ['contractAddress'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.getContractHistory(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }

  /**
   * 处理估算合约手续费
   */
  private async handleEstimateContractFee(data: any): Promise<any> {
    this.validateRequiredParams(data, ['contractAddress', 'method', 'args'])
    
    return this.safeWasmCall(async () => {
      const result = await (globalThis as any).sat20wallet_wasm.estimateContractFee(
        JSON.stringify(data)
      )
      return JSON.parse(result)
    })
  }
}