import { Message } from '@/types/message'
import type { Runtime } from 'wxt/browser'
import { browser } from 'wxt/browser'

export interface MessagePayload<T = any> {
  type: Message.MessageType
  action: Message.MessageAction
  data?: T
  metadata: {
    messageId: string
    from: Message.MessageFrom
    to: Message.MessageTo
    origin?: string
    windowId?: number
  }
  error?: {
    code: number
    message: string
  }
}

type MessageHandler = (message: MessagePayload) => Promise<any> | void

class MessageTransport {
  private port: Runtime.Port | null = null
  private channel: BroadcastChannel | null = null
  private handlers: Map<string, MessageHandler> = new Map()
  private pendingMessages: Map<string, { 
    resolve: (value: any) => void
    reject: (reason: any) => void
    timeout: NodeJS.Timeout
  }> = new Map()
  private connectionRetryCount = 0
  private monitorInterval: ReturnType<typeof setInterval> | null = null
  private isConnecting = false
  private readonly MAX_RETRIES = 3
  private readonly TIMEOUT = 30000
  private readonly RETRY_INTERVAL = 5000
  private readonly INITIAL_RETRY_DELAY = 1000
  private readonly portName: string
  private readonly role: Message.MessageFrom
  private readonly isInjected: boolean

  constructor(portName: string, role: Message.MessageFrom) {
    this.portName = portName
    this.role = role
    this.isInjected = role === Message.MessageFrom.INJECTED
    setTimeout(() => {
      this.setupConnectionMonitoring()
    }, this.INITIAL_RETRY_DELAY)
  }

  private setupConnectionMonitoring() {
    if (this.isInjected || this.monitorInterval) return

    this.monitorInterval = setInterval(() => {
      if (!this.port && !this.isConnecting && this.connectionRetryCount < this.MAX_RETRIES) {
        this.connect().catch((error) => {
          if (error?.message?.includes('Receiving end does not exist')) {
            console.warn('Background service not ready, will retry later')
          } else {
            console.error('Connection failed:', error)
          }
        })
      } else if (this.connectionRetryCount >= this.MAX_RETRIES) {
        console.warn('Max retry attempts reached, stopping connection monitoring')
        this.stopConnectionMonitoring()
      }
    }, this.RETRY_INTERVAL)
  }

  private stopConnectionMonitoring() {
    if (this.monitorInterval) {
      clearInterval(this.monitorInterval)
      this.monitorInterval = null
    }
  }

  private async connect(): Promise<void> {
    if (this.isConnecting) return

    try {
      this.isConnecting = true

      if (this.isInjected) {
        if (this.channel) {
          try {
            this.channel.close()
          } catch (e) {
            // Ignore close errors
          }
        }
        this.channel = new BroadcastChannel(Message.Channel.INJECT_CONTENT)
        this.channel.onmessage = (event) => {
          if (event.data && typeof event.data === 'object') {
            this.handleIncomingMessage(event.data)
          }
        }
      } else {
        if (this.port) {
          try {
            this.port.disconnect()
          } catch (e) {
            // Ignore disconnect errors
          }
          this.port = null
        }

        const lastError = browser.runtime.lastError
        if (lastError) {
          console.warn('Previous runtime error:', lastError.message)
        }

        try {
          this.port = browser.runtime.connect({ name: this.portName })
          
          this.port.onDisconnect.addListener(() => {
            const error = browser.runtime.lastError
            if (error?.message?.includes('Receiving end does not exist')) {
              console.warn('Background service not ready, will retry')
              this.handleDisconnect()
            } else if (error?.message !== 'No matching message handler') {
              console.warn('Port disconnected:', error?.message || 'Unknown reason')
              this.handleDisconnect()
            }
          })

          this.port.onMessage.addListener((message: any, port: Runtime.Port) => {
            if (message && typeof message === 'object') {
              this.handleIncomingMessage(message as MessagePayload)
            }
          })

          await this.waitForConnectionReady()
          console.log('Connection established successfully')
          this.connectionRetryCount = 0
        } catch (error) {
          console.error('Failed to establish port connection:', error)
          throw error
        }
      }
    } catch (error) {
      this.connectionRetryCount++
      const retryMsg = this.connectionRetryCount < this.MAX_RETRIES ? 
        `, will retry (${this.connectionRetryCount}/${this.MAX_RETRIES})` : 
        ', max retries reached'
      console.error(`Connection attempt failed${retryMsg}:`, error)
      throw error
    } finally {
      this.isConnecting = false
    }
  }

  private async waitForConnectionReady(): Promise<void> {
    if (this.isInjected) {
      return Promise.resolve()
    }

    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.port?.onMessage.removeListener(handler)
        reject(new Error('Connection timeout waiting for background service'))
      }, 5000)

      const handler = (msg: any) => {
        if (msg.type === Message.MessageType.CONNECTION_READY && msg.status === 'ok') {
          clearTimeout(timeout)
          this.port?.onMessage.removeListener(handler)
          resolve()
        }
      }

      this.port?.onMessage.addListener(handler)
      
      try {
        this.port?.postMessage({
          type: Message.MessageType.REQUEST,
          action: Message.MessageAction.CONNECTION_CHECK,
          metadata: {
            messageId: `check_${Date.now()}`,
            from: this.role,
            to: Message.MessageTo.BACKGROUND
          }
        })
      } catch (error) {
        clearTimeout(timeout)
        this.port?.onMessage.removeListener(handler)
        reject(error)
      }
    })
  }

  private handleDisconnect() {
    if (this.isInjected) {
      if (this.channel) {
        try {
          this.channel.close()
        } catch (e) {
          // Ignore close errors
        }
        this.channel = null
      }
    } else {
      if (this.port) {
        try {
          this.port.disconnect()
        } catch (e) {
          // Ignore disconnect errors
        }
        this.port = null
      }
    }

    // 清理所有待处理的消息
    this.pendingMessages.forEach((pending, messageId) => {
      clearTimeout(pending.timeout)
      pending.reject(new Error('Connection lost'))
      this.pendingMessages.delete(messageId)
    })

    // 如果重试次数超过限制，停止监控并输出警告
    if (this.connectionRetryCount >= this.MAX_RETRIES) {
      console.warn(`Connection failed after ${this.MAX_RETRIES} attempts, stopping reconnection attempts`)
      this.stopConnectionMonitoring()
    } else {
      console.log(`Connection lost, will retry (attempt ${this.connectionRetryCount + 1}/${this.MAX_RETRIES})`)
    }
  }

  private async handleIncomingMessage(message: MessagePayload) {
    // 确保消息格式正确
    if (!message || typeof message !== 'object') {
      console.warn('Received invalid message format:', message)
      return
    }

    // 处理连接就绪消息的特殊情况
    if (message.type === Message.MessageType.CONNECTION_READY) {
      return
    }

    // 确保 metadata 存在
    if (!message.metadata) {
      console.warn('Received message without metadata:', message)
      return
    }

    const { messageId } = message.metadata

    const pending = this.pendingMessages.get(messageId)
    if (pending) {
      clearTimeout(pending.timeout)
      if (message.error) {
        pending.reject(message.error)
      } else {
        pending.resolve(message.data)
      }
      this.pendingMessages.delete(messageId)
      return
    }

    // 确保 action 存在
    if (!message.action) {
      console.warn('Received message without action:', message)
      return
    }

    const handler = this.handlers.get(message.action)
    if (handler) {
      try {
        const response = await handler(message)
        if (response) {
          await this.sendResponse(message, response)
        }
      } catch (error) {
        await this.sendError(message, error)
      }
    }
  }

  private async sendResponse(originalMessage: MessagePayload, data: any) {
    // 确保原始消息有 metadata
    if (!originalMessage.metadata) {
      console.warn('Cannot send response: original message has no metadata')
      return
    }

    const response: MessagePayload = {
      ...originalMessage,
      metadata: {
        ...originalMessage.metadata,
        from: this.role,
        to: this.getOppositeRole(this.role),
      },
      data,
    }
    await this.sendMessage(response)
  }

  private async sendError(originalMessage: MessagePayload, error: any) {
    // 确保原始消息有 metadata
    if (!originalMessage.metadata) {
      console.warn('Cannot send error response: original message has no metadata')
      return
    }

    const response: MessagePayload = {
      ...originalMessage,
      metadata: {
        ...originalMessage.metadata,
        from: this.role,
        to: this.getOppositeRole(this.role),
      },
      error: {
        code: error.code || -1,
        message: error.message || 'Unknown error',
      },
    }
    await this.sendMessage(response)
  }

  private getOppositeRole(role: Message.MessageFrom): Message.MessageTo {
    switch (role) {
      case Message.MessageFrom.INJECTED:
        return Message.MessageTo.BACKGROUND
      case Message.MessageFrom.CONTENT:
        return Message.MessageTo.BACKGROUND
      case Message.MessageFrom.BACKGROUND:
        return Message.MessageTo.INJECTED
      case Message.MessageFrom.POPUP:
        return Message.MessageTo.BACKGROUND
      default:
        return Message.MessageTo.INJECTED
    }
  }

  public async sendMessage<T = any>(message: MessagePayload): Promise<T> {
    try {
      if (this.isInjected) {
        if (!this.channel) {
          await this.connect()
        }
        window.postMessage(message, window.location.origin)
      } else {
        if (!this.port) {
          await this.connect()
        }
        if (!this.port) {
          throw new Error('Failed to establish connection')
        }
        this.port.postMessage(message)
      }

      return new Promise((resolve, reject) => {
        const messageId = `msg_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`
        message.metadata.messageId = messageId

        const timeout = setTimeout(() => {
          this.pendingMessages.delete(messageId)
          reject(new Error('Message timeout'))
        }, this.TIMEOUT)

        this.pendingMessages.set(messageId, { resolve, reject, timeout })

        // 添加错误检查
        if (!this.isInjected && this.port) {
          const error = browser.runtime.lastError
          if (error) {
            clearTimeout(timeout)
            this.pendingMessages.delete(messageId)
            reject(new Error(error.message))
          }
        }
      })
    } catch (error: any) {
      console.error('Send message failed:', error)
      throw new Error(`Failed to send message: ${error.message}`)
    }
  }

  public registerHandler(action: Message.MessageAction, handler: MessageHandler) {
    this.handlers.set(action, handler)
  }

  public removeHandler(action: Message.MessageAction) {
    this.handlers.delete(action)
  }

  public disconnect() {
    this.stopConnectionMonitoring()
    if (this.isInjected) {
      this.channel?.close()
      this.channel = null
    } else {
      this.port?.disconnect()
      this.port = null
    }
  }
}

export const createMessageTransport = (() => {
  const instances = new Map<string, MessageTransport>()
  
  return (portName: string, role: Message.MessageFrom): MessageTransport => {
    const key = `${portName}_${role}`
    if (!instances.has(key)) {
      instances.set(key, new MessageTransport(portName, role))
    }
    return instances.get(key)!
  }
})() 