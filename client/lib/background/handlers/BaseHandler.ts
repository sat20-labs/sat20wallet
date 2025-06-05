import { Message } from '@/types/message'
import { walletError } from '@/types/error'

/**
 * 业务处理器基类
 */
export abstract class BaseHandler {
  /**
   * 处理消息的抽象方法，子类必须实现
   */
  abstract handle(event: any): Promise<any>

  /**
   * 获取处理器支持的消息动作列表
   */
  abstract getSupportedActions(): string[]

  /**
   * 检查是否支持指定的消息动作
   */
  supports(action: string): boolean {
    return this.getSupportedActions().includes(action)
  }

  /**
   * 创建成功响应
   */
  protected createSuccessResponse(event: any, data: any): any {
    return {
      ...event,
      metadata: {
        ...event.metadata,
        from: Message.MessageFrom.BACKGROUND,
        to: Message.MessageTo.INJECTED,
      },
      data,
      error: null,
    }
  }

  /**
   * 创建错误响应
   */
  protected createErrorResponse(event: any, error: any): any {
    return {
      ...event,
      metadata: {
        ...event.metadata,
        from: Message.MessageFrom.BACKGROUND,
        to: Message.MessageTo.INJECTED,
      },
      data: null,
      error: error || walletError.unknown,
    }
  }

  /**
   * 验证事件数据
   */
  protected validateEvent(event: any): void {
    if (!event) {
      throw new Error('Event is required')
    }
    if (!event.action) {
      throw new Error('Event action is required')
    }
    if (!event.metadata) {
      throw new Error('Event metadata is required')
    }
  }

  /**
   * 验证WASM是否已初始化
   */
  protected validateWasmReady(): void {
    if (!(globalThis as any).sat20wallet_wasm || !(globalThis as any).stp_wasm) {
      throw walletError.wasmNotReady
    }
  }

  /**
   * 安全执行WASM调用
   */
  protected async safeWasmCall<T>(wasmCall: () => Promise<T>): Promise<T> {
    try {
      this.validateWasmReady()
      return await wasmCall()
    } catch (error) {
      console.error('WASM call failed:', error)
      throw error
    }
  }

  /**
   * 记录处理开始
   */
  protected logHandleStart(action: string, data?: any): void {
    console.log(`[${this.constructor.name}] Handling action: ${action}`, data ? { data } : '')
  }

  /**
   * 记录处理成功
   */
  protected logHandleSuccess(action: string, result?: any): void {
    console.log(`[${this.constructor.name}] Successfully handled: ${action}`, result ? { result } : '')
  }

  /**
   * 记录处理错误
   */
  protected logHandleError(action: string, error: any): void {
    console.error(`[${this.constructor.name}] Failed to handle: ${action}`, error)
  }

  /**
   * 通用的处理流程包装器
   */
  protected async handleWithLogging(event: any, handler: () => Promise<any>): Promise<any> {
    const action = event.action
    
    try {
      this.validateEvent(event)
      this.logHandleStart(action, event.data)
      
      const result = await handler()
      
      this.logHandleSuccess(action, result)
      return this.createSuccessResponse(event, result)
      
    } catch (error) {
      this.logHandleError(action, error)
      return this.createErrorResponse(event, error)
    }
  }

  /**
   * 提取事件数据
   */
  protected getEventData(event: any): any {
    return event.data || {}
  }

  /**
   * 提取事件元数据
   */
  protected getEventMetadata(event: any): any {
    return event.metadata || {}
  }

  /**
   * 检查是否有必需的参数
   */
  protected validateRequiredParams(data: any, requiredParams: string[]): void {
    for (const param of requiredParams) {
      if (data[param] === undefined || data[param] === null) {
        throw new Error(`Missing required parameter: ${param}`)
      }
    }
  }

  /**
   * 格式化错误信息
   */
  protected formatError(error: any): any {
    if (typeof error === 'string') {
      return { message: error, code: 'UNKNOWN_ERROR' }
    }
    
    if (error && typeof error === 'object') {
      return {
        message: error.message || 'Unknown error',
        code: error.code || 'UNKNOWN_ERROR',
        details: error.details || null,
      }
    }
    
    return walletError.unknown
  }

  /**
   * 异步重试机制
   */
  protected async retry<T>(
    operation: () => Promise<T>,
    maxRetries: number = 3,
    delay: number = 1000
  ): Promise<T> {
    let lastError: any
    
    for (let i = 0; i <= maxRetries; i++) {
      try {
        return await operation()
      } catch (error) {
        lastError = error
        
        if (i === maxRetries) {
          break
        }
        
        console.warn(`Operation failed, retrying in ${delay}ms... (${i + 1}/${maxRetries})`, error)
        await new Promise(resolve => setTimeout(resolve, delay))
      }
    }
    
    throw lastError
  }
}