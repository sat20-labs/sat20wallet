import { Runtime } from 'wxt/browser'
import { Message } from '@/types/message'
import { AuthorizationManager } from './AuthorizationManager'
import { ApprovalManager } from './ApprovalManager'
import { WasmManager } from './WasmManager'
import { ErrorHandler } from '../utils/ErrorHandler'
import { ResponseBuilder } from '../utils/ResponseBuilder'
import { BaseHandler } from '../handlers/BaseHandler'
import { walletError } from '@/types/error'
import { walletStorage } from '@/lib/walletStorage'
import service from '@/lib/service'

/**
 * 消息路由器 - 负责消息的接收、分发和响应
 */
export class MessageRouter {
  private handlers: Map<string, BaseHandler> = new Map()
  private authManager: AuthorizationManager
  private approvalManager: ApprovalManager
  private wasmManager: WasmManager
  private errorHandler: ErrorHandler
  private responseBuilder: ResponseBuilder

  constructor(
    authManager: AuthorizationManager,
    approvalManager: ApprovalManager,
    wasmManager: WasmManager
  ) {
    this.authManager = authManager
    this.approvalManager = approvalManager
    this.wasmManager = wasmManager
    this.errorHandler = new ErrorHandler()
    this.responseBuilder = new ResponseBuilder()
  }

  /**
   * 注册业务处理器
   */
  registerHandler(action: string, handler: BaseHandler): void {
    this.handlers.set(action, handler)
  }

  /**
   * 处理消息
   */
  async handleMessage(port: Runtime.Port, event: any): Promise<void> {
    const eventData = event
    const { action, type, data } = eventData
    const { origin, messageId } = eventData.metadata

    // 设置消息来源和目标
    eventData.metadata.from = Message.MessageFrom.BACKGROUND
    eventData.metadata.to = Message.MessageTo.INJECTED

    try {
      // 初始化钱包状态
      await walletStorage.initializeState()
      const hasWallet = await service.getHasWallet()
      if (!hasWallet) {
        this.sendErrorResponse(port, eventData, walletError.noWallet)
        return
      }

      // 权限验证
      if (this.authManager.requiresAuthorization(action)) {
        const authorized = await this.authManager.isAuthorized(origin)
        if (!authorized) {
          this.sendErrorResponse(port, eventData, {
            code: -32603,
            message: '未授权的来源，请先调用 REQUEST_ACCOUNTS 方法',
          })
          return
        }
      }

      if (type === Message.MessageType.REQUEST) {
        await this.handleRequestMessage(port, eventData, action, data)
      } else if (type === Message.MessageType.APPROVE) {
        await this.handleApprovalMessage(port, eventData, action, origin)
      }
    } catch (error: any) {
      console.error('Error handling message:', error)
      this.sendErrorResponse(port, eventData, {
        code: -32603,
        message: error?.message || '处理消息时发生内部错误',
      })
    }
  }

  /**
   * 处理请求类型消息
   */
  private async handleRequestMessage(
    port: Runtime.Port,
    eventData: any,
    action: string,
    data: any
  ): Promise<void> {
    const handler = this.handlers.get(action)
    if (!handler) {
      console.warn(`Unhandled REQUEST action: ${action}`)
      this.sendErrorResponse(port, eventData, {
        code: -32601,
        message: 'Method not found'
      })
      return
    }

    try {
      const result = await handler.handle(data)
      const response = this.responseBuilder.buildSuccessResponse(eventData, result)
      port.postMessage(response)
    } catch (error: any) {
      const errorResponse = this.errorHandler.handleError(error, eventData)
      port.postMessage(errorResponse)
    }
  }

  /**
   * 处理审批类型消息
   */
  private async handleApprovalMessage(
    port: Runtime.Port,
    eventData: any,
    action: string,
    origin: string
  ): Promise<void> {
    if (this.approvalManager.requiresApproval(action)) {
      await this.approvalManager.createApprovalWindow(eventData, origin)
    } else {
      console.warn(`Received APPROVE message for action ${action} which doesn't require approval.`)
      this.sendErrorResponse(port, eventData, {
        code: -32600,
        message: 'Invalid Request: Action does not require approval.'
      })
    }
  }

  /**
   * 发送错误响应
   */
  private sendErrorResponse(port: Runtime.Port, eventData: any, error: any): void {
    port.postMessage({
      ...eventData,
      data: null,
      error: error,
    })
  }

  /**
   * 检查WASM是否就绪
   */
  isWasmReady(): boolean {
    return this.wasmManager.isReady()
  }

  /**
   * 将消息加入队列
   */
  queueMessage(port: Runtime.Port, event: any): void {
    this.wasmManager.queueMessage(port, event)
  }

  /**
   * 处理队列中的消息
   */
  async processQueuedMessages(): Promise<void> {
    const queuedMessages = this.wasmManager.getQueuedMessages()
    console.log(`Processing queued messages, count: ${queuedMessages.length}`)
    
    for (const { port, event } of queuedMessages) {
      await this.handleMessage(port, event)
    }
    
    this.wasmManager.clearQueue()
  }
}