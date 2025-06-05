import { BaseHandler } from './BaseHandler'
import { AccountHandler } from './AccountHandler'
import { TransactionHandler } from './TransactionHandler'
import { PsbtHandler } from './PsbtHandler'
import { UtxoHandler } from './UtxoHandler'
import { AssetHandler } from './AssetHandler'
import { ContractHandler } from './ContractHandler'
import { NetworkHandler } from './NetworkHandler'

/**
 * 处理器管理器 - 统一管理所有业务处理器
 */
export class HandlerManager {
  private handlers: Map<string, BaseHandler> = new Map()
  private actionToHandlerMap: Map<string, string> = new Map()

  constructor() {
    this.initializeHandlers()
  }

  /**
   * 初始化所有处理器
   */
  private initializeHandlers(): void {
    const handlers = [
      { name: 'account', handler: new AccountHandler() },
      { name: 'transaction', handler: new TransactionHandler() },
      { name: 'psbt', handler: new PsbtHandler() },
      { name: 'utxo', handler: new UtxoHandler() },
      { name: 'asset', handler: new AssetHandler() },
      { name: 'contract', handler: new ContractHandler() },
      { name: 'network', handler: new NetworkHandler() },
    ]

    for (const { name, handler } of handlers) {
      this.registerHandler(name, handler)
    }

    console.log(`Initialized ${this.handlers.size} handlers with ${this.actionToHandlerMap.size} actions`)
  }

  /**
   * 注册处理器
   */
  private registerHandler(name: string, handler: BaseHandler): void {
    this.handlers.set(name, handler)
    
    // 建立动作到处理器的映射
    const supportedActions = handler.getSupportedActions()
    for (const action of supportedActions) {
      if (this.actionToHandlerMap.has(action)) {
        console.warn(`Action ${action} is already registered by another handler`)
      }
      this.actionToHandlerMap.set(action, name)
    }
    
    console.log(`Registered handler '${name}' with ${supportedActions.length} actions:`, supportedActions)
  }

  /**
   * 根据动作获取对应的处理器
   */
  getHandlerByAction(action: string): BaseHandler | null {
    const handlerName = this.actionToHandlerMap.get(action)
    if (!handlerName) {
      return null
    }
    return this.handlers.get(handlerName) || null
  }

  /**
   * 根据名称获取处理器
   */
  getHandlerByName(name: string): BaseHandler | null {
    return this.handlers.get(name) || null
  }

  /**
   * 检查是否支持指定动作
   */
  supportsAction(action: string): boolean {
    return this.actionToHandlerMap.has(action)
  }

  /**
   * 处理消息
   */
  async handleMessage(event: any): Promise<any> {
    const action = event.action
    
    if (!action) {
      throw new Error('Missing action in event')
    }

    const handler = this.getHandlerByAction(action)
    if (!handler) {
      throw new Error(`No handler found for action: ${action}`)
    }

    try {
      return await handler.handle(event)
    } catch (error) {
      console.error(`Handler failed for action ${action}:`, error)
      throw error
    }
  }

  /**
   * 获取所有支持的动作列表
   */
  getAllSupportedActions(): string[] {
    return Array.from(this.actionToHandlerMap.keys())
  }

  /**
   * 获取处理器统计信息
   */
  getHandlerStats(): {
    totalHandlers: number
    totalActions: number
    handlerDetails: Array<{
      name: string
      actionCount: number
      actions: string[]
    }>
  } {
    const handlerDetails = []
    
    for (const [name, handler] of this.handlers.entries()) {
      const actions = handler.getSupportedActions()
      handlerDetails.push({
        name,
        actionCount: actions.length,
        actions
      })
    }

    return {
      totalHandlers: this.handlers.size,
      totalActions: this.actionToHandlerMap.size,
      handlerDetails
    }
  }

  /**
   * 添加自定义处理器
   */
  addCustomHandler(name: string, handler: BaseHandler): void {
    if (this.handlers.has(name)) {
      throw new Error(`Handler with name '${name}' already exists`)
    }
    
    this.registerHandler(name, handler)
  }

  /**
   * 移除处理器
   */
  removeHandler(name: string): boolean {
    const handler = this.handlers.get(name)
    if (!handler) {
      return false
    }

    // 移除动作映射
    const supportedActions = handler.getSupportedActions()
    for (const action of supportedActions) {
      this.actionToHandlerMap.delete(action)
    }

    // 移除处理器
    this.handlers.delete(name)
    
    console.log(`Removed handler '${name}' and ${supportedActions.length} actions`)
    return true
  }

  /**
   * 重新加载处理器
   */
  reloadHandler(name: string, newHandler: BaseHandler): void {
    this.removeHandler(name)
    this.registerHandler(name, newHandler)
  }

  /**
   * 验证所有处理器
   */
  validateHandlers(): {
    isValid: boolean
    errors: string[]
    warnings: string[]
  } {
    const errors: string[] = []
    const warnings: string[] = []

    // 检查重复的动作
    const actionCounts = new Map<string, number>()
    for (const action of this.actionToHandlerMap.keys()) {
      actionCounts.set(action, (actionCounts.get(action) || 0) + 1)
    }

    for (const [action, count] of actionCounts.entries()) {
      if (count > 1) {
        errors.push(`Action '${action}' is registered by multiple handlers`)
      }
    }

    // 检查处理器是否有支持的动作
    for (const [name, handler] of this.handlers.entries()) {
      const actions = handler.getSupportedActions()
      if (actions.length === 0) {
        warnings.push(`Handler '${name}' has no supported actions`)
      }
    }

    return {
      isValid: errors.length === 0,
      errors,
      warnings
    }
  }

  /**
   * 重置所有处理器
   */
  reset(): void {
    this.handlers.clear()
    this.actionToHandlerMap.clear()
    this.initializeHandlers()
  }

  /**
   * 获取处理器列表
   */
  getHandlerNames(): string[] {
    return Array.from(this.handlers.keys())
  }

  /**
   * 检查处理器是否存在
   */
  hasHandler(name: string): boolean {
    return this.handlers.has(name)
  }
}