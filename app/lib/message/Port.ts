export interface IPort extends chrome.runtime.Port {}

export default class Port implements IPort {
  #port: chrome.runtime.Port;
  #connected: boolean = false;
  #portName: string;
  #reconnectAttempts: number = 0;
  #maxReconnectAttempts: number = 5;
  #reconnectDelay: number = 1000;
  readonly #onConnectCallback: (port: IPort) => void | Promise<void>;

  constructor(
    connectInfo: chrome.runtime.ConnectInfo,
    opts?: {
      onConnect?: (port: IPort) => void | Promise<void>;
      maxReconnectAttempts?: number;
      reconnectDelay?: number;
    }
  ) {
    if (!connectInfo.name) {
      throw new Error('port name is required');
    }
    this.#portName = connectInfo.name;
    this.#onConnectCallback = opts?.onConnect ?? (() => {});
    this.#maxReconnectAttempts = opts?.maxReconnectAttempts ?? 5;
    this.#reconnectDelay = opts?.reconnectDelay ?? 1000;
    this.#port = this.#createPort();
  }

  #createPort() {
    try {
      const newPort = chrome.runtime.connect({
        name: this.#portName,
      });
      
      this.#connected = true;
      this.#reconnectAttempts = 0;
      console.log(`chrome port ${this.#portName} connected`);
      
      newPort.onDisconnect.addListener(() => {
        const lastError = chrome.runtime.lastError;
        this.#connected = false;
        
        console.log(`chrome port ${this.#portName} disconnected`, lastError?.message);
        
        // 检查是否是因为 bfcache 导致的断开
        if (lastError?.message?.includes('back/forward cache')) {
          console.log('Port disconnected due to bfcache, will wait for page restore');
          // bfcache 情况下不立即重连，等待页面恢复
          return;
        }
        
        // 尝试重新连接
        this.#handleReconnect();
      });
      
      this.#onConnectCallback(newPort);
      return newPort;
    } catch (error) {
      console.error(`Failed to create port ${this.#portName}:`, error);
      this.#connected = false;
      this.#handleReconnect();
      throw error;
    }
  }

  #handleReconnect() {
    if (this.#reconnectAttempts >= this.#maxReconnectAttempts) {
      console.error(`Max reconnection attempts (${this.#maxReconnectAttempts}) reached for port ${this.#portName}`);
      return;
    }
    
    this.#reconnectAttempts++;
    console.log(`Attempting to reconnect port ${this.#portName} (attempt ${this.#reconnectAttempts}/${this.#maxReconnectAttempts})`);
    
    setTimeout(() => {
      try {
        this.#port = this.#createPort();
      } catch (error) {
        console.error(`Reconnection attempt ${this.#reconnectAttempts} failed:`, error);
      }
    }, this.#reconnectDelay * this.#reconnectAttempts); // 使用递增的延迟时间
  }

  public async postMessage(message: any) {
    if (!this.#connected) {
      console.debug('postMessage: chrome port not connected, attempting to reconnect');
      try {
        this.#port = this.#createPort();
      } catch (error) {
        console.error('Failed to reconnect port during postMessage:', error);
        throw new Error('Failed to send message: port disconnected');
      }
    }
    
    try {
      this.#port.postMessage(message);
    } catch (error) {
      console.error('Failed to post message:', error);
      // 如果发送消息失败，标记连接断开并尝试重连
      this.#connected = false;
      this.#handleReconnect();
      throw error;
    }
  }

  public disconnect() {
    if (this.#connected) {
      this.#port.disconnect();
      this.#connected = false;
    }
  }

  get connected() {
    return this.#connected;
  }

  get sender() {
    return this.#port.sender;
  }

  get onDisconnect() {
    return this.#port.onDisconnect;
  }

  get onMessage() {
    return this.#port.onMessage;
  }

  get name() {
    return this.#port.name;
  }
}
