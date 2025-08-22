import { Message } from '@/types/message'
import { Buffer as Buffer3 } from 'buffer'
import { browser } from 'wxt/browser'
import { initializeWasm, reInitializeWasm } from '@/lib/background/WasmManager'
import { ApprovalManager } from '@/lib/background/ApprovalManager'
import { handleMessage } from '@/lib/background/MessageRouter'
import { walletStorage } from '@/lib/walletStorage'
import type { Runtime } from 'wxt/browser';

globalThis.Buffer = Buffer3

class BackgroundService {
  private approvalManager = new ApprovalManager()
  private isWasmReady = false
  private messageQueue: { port: Runtime.Port; event: any }[] = []
  private portMap: {
    content?: Runtime.Port
    popup?: Runtime.Port
  } = {}

  constructor() {
    console.log('Background service worker started.')
    this.setupListeners()
    this.initialize()
  }

  private async processQueuedMessages() {
    console.log(`调试: 开始处理消息队列, 数量: ${this.messageQueue.length}`)
    while (this.messageQueue.length > 0) {
      const { port, event } = this.messageQueue.shift()!
      await handleMessage(port, event, this.approvalManager)
    }
  }

  private setupListeners() {
    // 监听来自 content_script 和 popup 的长连接
    browser.runtime.onConnect.addListener(this.onConnectHandler)
    // 监听来自 popup 的一次性消息
    browser.runtime.onMessage.addListener(this.onMessageHandler)
    // 监听审批窗口被关闭的事件
    browser.windows.onRemoved.addListener(this.approvalManager.handleWindowRemoved);
  }

  private onConnectHandler = (port: Runtime.Port) => {
    console.log('调试: 新的端口连接:', port.name);

    if (port.name === Message.Port.CONTENT_BG) {
      this.handleContentScriptConnection(port)
    } else if (port.name === Message.Port.BG_POPUP) {
      this.handlePopupConnection(port)
    } else if (port.name === 'KEEP_ALIVE_INTERVAL') {
      this.handleKeepAliveConnection(port)
    }
  }

  private onMessageHandler = (message: any, sender: Runtime.MessageSender, sendResponse: (response?: any) => void) => {
    const { type, action, metadata } = message
    if (type === Message.MessageType.EVENT) {
      if (action === Message.MessageAction.ENV_CHANGED || message.event === 'networkChanged') {
        console.log('收到网络变更事件，开始重新初始化状态和WASM')
        // 设置WASM重新初始化标志
        this.isWasmReady = false
        walletStorage.initializeState().then(async () => {
          try {
            await reInitializeWasm()
            this.isWasmReady = true
            console.log('网络变更后WASM重新初始化完成')
            // 处理可能积压的消息
            await this.processQueuedMessages()
          } catch (error) {
            console.error('网络变更后WASM重新初始化失败:', error)
            this.isWasmReady = true // 即使失败也要设置为true，避免无限等待
          }
        })
      } else if (this.portMap.content) {
        console.log('type', type);
        console.log('action', action);
        console.log('metadata', metadata);
        console.log('message', message);
        console.log('this.portMap.content', this.portMap.content);
        this.portMap.content.postMessage(message)
      }
      return true
    }
    if (
      type === Message.MessageType.REQUEST &&
      action === Message.MessageAction.GET_APPROVE_DATA &&
      metadata?.windowId
    ) {
      const response = this.approvalManager.getApprovalData(metadata.windowId)
      sendResponse(response)
      return true
    } else if (message.type === 'KEEP_ALIVE_FALLBACK') {
      // 这是为了响应 content.ts 的后备计划
      // 收到这个消息本身就意味着 background 被成功唤醒了
      console.log(`调试: 收到来自 ${message.origin} 的后备激活消息，连接即将恢复。`)
    }
    return undefined; // 返回 false 或 undefined 表示是同步响应
  }

  private handleContentScriptConnection(port: Runtime.Port) {
    this.portMap.content = port
    this.approvalManager.setContentPort(port)

    port.onDisconnect.addListener(() => {
      console.log("调试: 内容脚本端口已断开。");
      this.portMap.content = undefined;
      this.approvalManager.setContentPort(undefined)
    });

    port.onMessage.addListener(async (event: any) => {
      if (!this.isWasmReady) {
        console.log('调试: WASM 未就绪, 消息已入队', event.action)
        this.messageQueue.push({ port, event })
        return
      }
      await handleMessage(port, event, this.approvalManager)
    })
  }

  private handlePopupConnection(port: Runtime.Port) {
    this.portMap.popup = port
    port.onMessage.addListener((message: any) => {
      const { action, data, metadata = {} } = message
      if (!metadata.windowId) {
        return
      }
      if (action === Message.MessageAction.APPROVE_RESPONSE) {
        this.approvalManager.handleResponse(true, metadata.windowId, data)
      } else if (action === Message.MessageAction.REJECT_RESPONSE) {
        this.approvalManager.handleResponse(false, metadata.windowId, null)
      }
    })
  }

  private handleKeepAliveConnection(port: Runtime.Port) {
    console.log('调试: 保持激活端口已连接')
    port.onMessage.addListener((msg: unknown) => {
      if (typeof msg === 'object' && msg !== null && 'type' in msg && (msg as any).type === 'KEEP_ALIVE') {
        port.postMessage({ type: 'KEEP_ALIVE', payload: 'PONG' })
      }
    })
    port.onDisconnect.addListener(() => {
      console.log('调试: 保持激活端口已断开')
    })
  }

  private initialize() {
    initializeWasm().then(async () => {
      this.isWasmReady = true
      console.log('调试: WASM 加载成功')
      await this.processQueuedMessages()
    }).catch((error) => {
      console.error('调试: WASM 初始化失败:', error)
    })
  }
}

export default defineBackground(() => {
  // 监听插件安装或更新事件
  browser.runtime.onInstalled.addListener(async (details) => {
    if (details.reason === 'install' || details.reason === 'update') {
      console.log('调试: 插件已安装/更新, 通知所有内容脚本重连...')
      const tabs = await browser.tabs.query({})
      for (const tab of tabs) {
        if (tab.id) {
          try {
            // 向每个 tab 发送重连指令
            await browser.tabs.sendMessage(tab.id, { type: 'RECONNECT_FROM_BACKGROUND' })
          } catch (error) {
            // 如果某个 tab 没有内容脚本或无法访问，这会报错，属于正常现象，我们忽略它
          }
        }
      }
    }
  })

  // 启动主服务
  // eslint-disable-next-line no-new
  new BackgroundService()
})

