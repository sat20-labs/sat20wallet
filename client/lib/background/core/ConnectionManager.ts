import { Runtime } from 'wxt/browser'
import { Message } from '@/types/message'

/**
 * 连接管理器 - 负责管理端口连接
 */
export class ConnectionManager {
  // 存储端口连接，以端口名称为键
  private portMap = new Map<string, Runtime.Port>()
  
  // 连接事件监听器
  private connectionListeners: ((port: Runtime.Port) => void)[] = []
  private disconnectionListeners: ((portName: string) => void)[] = []

  /**
   * 注册新的端口连接
   */
  registerPort(port: Runtime.Port): void {
    const portName = port.name || 'unknown'
    
    console.log(`Port connected: ${portName}`)
    this.portMap.set(portName, port)
    
    // 监听端口断开连接
    port.onDisconnect.addListener(() => {
      this.handlePortDisconnect(portName)
    })
    
    // 通知连接监听器
    this.connectionListeners.forEach(listener => listener(port))
  }

  /**
   * 处理端口断开连接
   */
  private handlePortDisconnect(portName: string): void {
    console.log(`Port disconnected: ${portName}`)
    this.portMap.delete(portName)
    
    // 通知断开连接监听器
    this.disconnectionListeners.forEach(listener => listener(portName))
  }

  /**
   * 获取指定名称的端口
   */
  getPort(portName: string): Runtime.Port | undefined {
    return this.portMap.get(portName)
  }

  /**
   * 获取所有活跃的端口
   */
  getAllPorts(): Map<string, Runtime.Port> {
    return new Map(this.portMap)
  }

  /**
   * 检查端口是否存在
   */
  hasPort(portName: string): boolean {
    return this.portMap.has(portName)
  }

  /**
   * 向指定端口发送消息
   */
  sendToPort(portName: string, message: any): boolean {
    const port = this.portMap.get(portName)
    if (port) {
      try {
        port.postMessage(message)
        return true
      } catch (error) {
        console.error(`Failed to send message to port ${portName}:`, error)
        // 如果发送失败，可能端口已断开，清理它
        this.portMap.delete(portName)
        return false
      }
    }
    return false
  }

  /**
   * 向所有端口广播消息
   */
  broadcastToAllPorts(message: any): number {
    let successCount = 0
    const portsToRemove: string[] = []
    
    for (const [portName, port] of this.portMap.entries()) {
      try {
        port.postMessage(message)
        successCount++
      } catch (error) {
        console.error(`Failed to broadcast to port ${portName}:`, error)
        portsToRemove.push(portName)
      }
    }
    
    // 清理失效的端口
    portsToRemove.forEach(portName => {
      this.portMap.delete(portName)
    })
    
    return successCount
  }

  /**
   * 向内容脚本发送消息
   */
  sendToContentScript(message: any): boolean {
    return this.sendToPort('content-script', message)
  }

  /**
   * 向弹窗发送消息
   */
  sendToPopup(message: any): boolean {
    return this.sendToPort('popup', message)
  }

  /**
   * 根据消息目标发送消息
   */
  sendByTarget(message: any): boolean {
    const target = message.metadata?.to
    
    switch (target) {
      case Message.MessageTo.INJECTED:
        return this.sendToContentScript(message)
      case Message.MessageTo.POPUP:
        return this.sendToPopup(message)
      default:
        console.warn(`Unknown message target: ${target}`)
        return false
    }
  }

  /**
   * 添加连接监听器
   */
  onConnection(listener: (port: Runtime.Port) => void): void {
    this.connectionListeners.push(listener)
  }

  /**
   * 添加断开连接监听器
   */
  onDisconnection(listener: (portName: string) => void): void {
    this.disconnectionListeners.push(listener)
  }

  /**
   * 移除连接监听器
   */
  removeConnectionListener(listener: (port: Runtime.Port) => void): void {
    const index = this.connectionListeners.indexOf(listener)
    if (index > -1) {
      this.connectionListeners.splice(index, 1)
    }
  }

  /**
   * 移除断开连接监听器
   */
  removeDisconnectionListener(listener: (portName: string) => void): void {
    const index = this.disconnectionListeners.indexOf(listener)
    if (index > -1) {
      this.disconnectionListeners.splice(index, 1)
    }
  }

  /**
   * 获取连接统计信息
   */
  getConnectionStats(): {
    totalConnections: number
    activeConnections: string[]
    hasContentScript: boolean
    hasPopup: boolean
  } {
    const activeConnections = Array.from(this.portMap.keys())
    
    return {
      totalConnections: this.portMap.size,
      activeConnections,
      hasContentScript: this.hasPort('content-script'),
      hasPopup: this.hasPort('popup')
    }
  }

  /**
   * 断开指定端口
   */
  disconnectPort(portName: string): boolean {
    const port = this.portMap.get(portName)
    if (port) {
      try {
        port.disconnect()
        this.portMap.delete(portName)
        return true
      } catch (error) {
        console.error(`Failed to disconnect port ${portName}:`, error)
        // 即使断开失败，也要从映射中移除
        this.portMap.delete(portName)
        return false
      }
    }
    return false
  }

  /**
   * 断开所有端口连接
   */
  disconnectAllPorts(): void {
    for (const [portName, port] of this.portMap.entries()) {
      try {
        port.disconnect()
      } catch (error) {
        console.error(`Failed to disconnect port ${portName}:`, error)
      }
    }
    this.portMap.clear()
  }

  /**
   * 清理无效的端口连接
   */
  cleanupInvalidPorts(): number {
    const invalidPorts: string[] = []
    
    for (const [portName, port] of this.portMap.entries()) {
      try {
        // 尝试发送一个测试消息来检查端口是否有效
        // 这里使用一个空对象，如果端口无效会抛出异常
        port.postMessage({ type: 'ping' })
      } catch (error) {
        invalidPorts.push(portName)
      }
    }
    
    // 移除无效端口
    invalidPorts.forEach(portName => {
      this.portMap.delete(portName)
    })
    
    if (invalidPorts.length > 0) {
      console.log(`Cleaned up ${invalidPorts.length} invalid ports:`, invalidPorts)
    }
    
    return invalidPorts.length
  }

  /**
   * 重置连接管理器
   */
  reset(): void {
    this.disconnectAllPorts()
    this.connectionListeners = []
    this.disconnectionListeners = []
  }
}