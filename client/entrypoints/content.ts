import KeepAliveConnection from '@/lib/message/KeepAliveConnection'
import Port from '@/lib/message/Port'
import { Message } from '@/types/message'
import { browser } from 'wxt/browser'
import type { Runtime } from 'wxt/browser'

export default defineContentScript({
  matches: ['*://*/*'],
  async main() {
    console.log('Hello content script! 12')
    
    let keepAlive: KeepAliveConnection | null = null
    let port: Port | null = null
    let channel: BroadcastChannel | null = null
    let isExtensionValid = true
    
    // 监听扩展上下文失效
    const handleExtensionInvalidation = () => {
      console.log('Extension context invalidated, cleaning up...')
      isExtensionValid = false
      cleanup()
    }

    // 清理资源
    const cleanup = () => {
      if (channel) {
        channel.close()
        channel = null
      }
      
      if (port) {
        try {
          port.disconnect()
        } catch (e) {
          console.warn('Error during port cleanup:', e)
        }
        port = null
      }
      
      if (keepAlive) {
        try {
          keepAlive.disconnect()
        } catch (e) {
          console.warn('Error during keepAlive cleanup:', e)
        }
        keepAlive = null
      }
    }

    // 初始化连接
    const initializeConnection = async () => {
      try {
        if (!isExtensionValid) {
          console.warn('Extension context is invalid, skipping connection initialization')
          return
        }

        // 注入脚本
        await injectScript('/injected.js', {
          keepInDom: true,
        })

        // 初始化 Keep Alive
        if (!keepAlive) {
          keepAlive = new KeepAliveConnection('CONTENT_SCRIPT')
          keepAlive.connect()
        }

        // 初始化端口
        if (!port) {
          console.log('初始化端口');
          
          console.log(Port);
          
          port = new Port({ 
            name: Message.Port.CONTENT_BG
          })
          
          // 设置端口消息监听
          port.onMessage.addListener(async (event: any) => {
            if (!isExtensionValid) {
              console.warn('Extension context is invalid, ignoring port message')
              return
            }

            console.log('Content 收到 BACKGROUND 消息:', event)
            const { metadata = {}, type } = event
            const { to, from } = metadata
            if (type === Message.MessageType.EVENT) {
              window.postMessage({ ...event, metadata: { ...metadata } }, '*')
              return
            }
            // 2. 兼容原有：from BACKGROUND 且 to INJECTED
            if (from === Message.MessageFrom.BACKGROUND && to === Message.MessageTo.INJECTED) {
              channel?.postMessage(event)
            }
          })

          // 设置端口断开监听
          port.onDisconnect.addListener(() => {
            console.log('Port disconnected, checking extension context...')
            const error = browser.runtime.lastError
            if (error && typeof error === 'object' && 'message' in error && 
                typeof error.message === 'string' && 
                error.message.includes('Extension context invalidated')) {
              handleExtensionInvalidation()
            } else {
              // 其他原因导致的断开，尝试重新连接
              cleanup()
              setTimeout(initializeConnection, 2000)
            }
          })
        }

        // 初始化广播通道
        if (!channel) {
          channel = new BroadcastChannel(Message.Channel.INJECT_CONTENT)
        }

        console.log('Connection initialization completed')
      } catch (error) {
        console.error('Failed to initialize connection:', error)
        if (error instanceof Error && error.message.includes('Extension context invalidated')) {
          handleExtensionInvalidation()
        } else {
          // 其他错误，尝试重新初始化
          cleanup()
          setTimeout(initializeConnection, 2000)
        }
      }
    }

    // 发送消息到背景脚本
    const sendToBackground = async (data: any) => {
      if (!isExtensionValid) {
        console.warn('Extension context is invalid, cannot send message')
        return
      }

      if (!port) {
        console.warn('Port not available, attempting to reinitialize connection')
        await initializeConnection()
        if (!port) {
          console.error('Failed to establish port connection')
          return
        }
      }

      try {
        console.log(port);
        
        await port.postMessage(data)
        console.log('Content 发送 BACKGROUND 消息成功:', data)
      } catch (error) {
        console.error('Failed to send message to background:', error)
        if (error instanceof Error && error.message.includes('Extension context invalidated')) {
          handleExtensionInvalidation()
        } else {
          // 尝试重新初始化连接
          await initializeConnection()
        }
      }
    }

    // 初始化连接
    await initializeConnection()

    // 监听来自后台脚本的一次性消息
    browser.runtime.onMessage.addListener((message: any) => {
      if (message.type === 'RECONNECT_FROM_BACKGROUND') {
        console.log('调试: 收到来自后台的重连指令, 开始执行...')
        cleanup()
        initializeConnection()
      }
      return true // 表示可能会有异步响应
    })

    // 监听扩展上下文变化
    if (browser.runtime.onMessage) {
      browser.runtime.onMessage.addListener((message, sender, sendResponse) => {
        // 如果监听器被调用，说明扩展上下文仍然有效
        isExtensionValid = true
        return undefined
      })
    }

    // 处理页面消息
    window.addEventListener('message', async (event) => {
      if (!isExtensionValid) {
        console.warn('Extension context is invalid, ignoring message')
        return
      }

      const eventData = event.data
      const { metadata = {} } = eventData
      const { to, from } = metadata
      
      if (event.source !== window) return
      
      if (from === Message.MessageTo.INJECTED) {
        console.log('Content 收到 INJECTED 消息:', event.data)
        eventData.metadata.from = Message.MessageFrom.CONTENT
        if (to === Message.MessageTo.BACKGROUND) {
          await sendToBackground(eventData)
        }
      }
    })

    // 清理函数
    return () => {
      console.log('Content script cleanup initiated')
      cleanup()
    }
  },
})
