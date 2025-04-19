import Port, { IPort } from './Port'

export default class KeepAliveConnection {
  // 配置常量
  private static KEEP_ALIVE_INTERVAL = 5000  // 增加到5秒
  private static MAX_RECONNECT_ATTEMPTS = 5
  private static INITIAL_RECONNECT_DELAY = 1000
  private static MAX_RECONNECT_DELAY = 30000

  // 私有属性
  #port: IPort | null = null
  #timer: NodeJS.Timeout | null = null
  #origin: string = 'UNKNOWN'
  #isConnected: boolean = false
  #reconnectAttempts: number = 0
  #currentReconnectDelay: number = KeepAliveConnection.INITIAL_RECONNECT_DELAY
  #reconnectTimer: NodeJS.Timeout | null = null

  constructor(origin: string) {
    this.#origin = origin
  }

  /**
   * Workaround to avoid service-worker be killed by Chrome
   * https://stackoverflow.com/questions/66618136/persistent-service-worker-in-chrome-extension
   */
  public connect() {
    if (this.#isConnected) {
      console.log('Already connected, skipping connection attempt')
      return
    }

    try {
      const newPort = new Port({ name: 'KEEP_ALIVE_INTERVAL' })
      
      // 处理断开连接
      newPort.onDisconnect.addListener(() => {
        console.log(`Keep alive connection disconnected from ${this.#origin}`)
        this.#handleDisconnect()
      })

      this.#port = newPort
      this.#isConnected = true
      this.#reconnectAttempts = 0
      this.#currentReconnectDelay = KeepAliveConnection.INITIAL_RECONNECT_DELAY
      
      // 开始心跳
      this.#startHeartbeat()
      
      console.log(`Keep alive connection established for ${this.#origin}`)
    } catch (error) {
      console.error('Failed to establish keep alive connection:', error)
      this.#handleDisconnect()
    }
  }

  /**
   * 处理断开连接的情况
   */
  #handleDisconnect() {
    this.#cleanup()

    // 检查是否超过最大重试次数
    if (this.#reconnectAttempts >= KeepAliveConnection.MAX_RECONNECT_ATTEMPTS) {
      console.log(`Max reconnection attempts (${KeepAliveConnection.MAX_RECONNECT_ATTEMPTS}) reached, stopping reconnection`)
      return
    }

    // 使用指数退避策略计算下次重连延迟
    this.#currentReconnectDelay = Math.min(
      this.#currentReconnectDelay * 2,
      KeepAliveConnection.MAX_RECONNECT_DELAY
    )

    console.log(`Scheduling reconnection attempt ${this.#reconnectAttempts + 1} in ${this.#currentReconnectDelay}ms`)
    
    // 清除之前的重连计时器
    if (this.#reconnectTimer) {
      clearTimeout(this.#reconnectTimer)
    }

    // 设置新的重连计时器
    this.#reconnectTimer = setTimeout(() => {
      this.#reconnectAttempts++
      this.connect()
    }, this.#currentReconnectDelay)
  }

  /**
   * 清理当前连接的资源
   */
  #cleanup() {
    this.#isConnected = false
    
    if (this.#timer) {
      clearInterval(this.#timer)
      this.#timer = null
    }

    if (this.#port) {
      try {
        this.#port.disconnect()
      } catch (e) {
        // 忽略断开连接时的错误
      }
      this.#port = null
    }
  }

  /**
   * 启动心跳机制
   */
  #startHeartbeat() {
    if (this.#timer) {
      clearInterval(this.#timer)
    }

    this.#timer = setInterval(() => {
      this.#sendHeartbeat()
    }, KeepAliveConnection.KEEP_ALIVE_INTERVAL)
  }

  /**
   * 发送心跳包
   */
  #sendHeartbeat() {
    if (!this.#port || !this.#isConnected) {
      console.log('Cannot send heartbeat: connection not established')
      return
    }

    try {
      this.#port.postMessage({
        type: 'KEEP_ALIVE',
        origin: this.#origin,
        payload: 'PING',
        timestamp: Date.now()  // 添加时间戳以便调试
      })
    } catch (error) {
      console.error('Failed to send heartbeat:', error)
      this.#handleDisconnect()
    }
  }

  /**
   * 主动断开连接
   */
  public disconnect() {
    console.log(`Manually disconnecting keep alive connection for ${this.#origin}`)
    this.#cleanup()
    
    // 清除重连计时器
    if (this.#reconnectTimer) {
      clearTimeout(this.#reconnectTimer)
      this.#reconnectTimer = null
    }
    
    // 重置重连相关状态
    this.#reconnectAttempts = 0
    this.#currentReconnectDelay = KeepAliveConnection.INITIAL_RECONNECT_DELAY
  }
}
