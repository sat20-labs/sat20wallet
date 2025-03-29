import { Message } from '@/types/message'
import { browser } from 'wxt/browser'
import type { Runtime } from 'wxt/browser'

export default defineContentScript({
  matches: ['*://*/*'],
  async main() {
    console.log('Hello content script!')

    await injectScript('/injected.js', {
      keepInDom: true,
    })

    let port: Runtime.Port | null = null
    const channel = new BroadcastChannel(Message.Channel.INJECT_CONTENT)

    // 连接到 background script
    const connect = () => {
      return new Promise<Runtime.Port>((resolve, reject) => {
        try {
          const newPort = browser.runtime.connect({ name: Message.Port.CONTENT_BG })
          
          // 设置一个超时，如果在指定时间内没有收到消息，就认为连接失败
          const timeoutId = setTimeout(() => {
            reject(new Error('连接超时'))
          }, 5000)

          // 监听第一条消息来确认连接成功
          const onFirstMessage = (msg: any) => {
            clearTimeout(timeoutId)
            newPort.onMessage.removeListener(onFirstMessage)
            resolve(newPort)
          }

          newPort.onMessage.addListener(onFirstMessage)
          
          // 设置消息监听器
          newPort.onMessage.addListener(async (event: any) => {
            console.log('Content 收到 BACKGROUND 消息:', event);
            
            const { metadata = {} } = event
            const { to, from } = metadata
            if (from === Message.MessageFrom.BACKGROUND) {
              console.log('Content 收到 BACKGROUND 消息:', event)
              if (to === Message.MessageTo.INJECTED) {
                channel.postMessage(event)
              }
            }
          })

          port = newPort
          console.log('Port 已创建，等待连接确认')
        } catch (error) {
          reject(error)
        }
      })
    }

    // 断开连接
    const disconnect = () => {
      if (port) {
        port.disconnect()
        port = null
        console.log('Port 已断开连接')
      }
    }

    // 初始连接
    try {
      await connect()
      console.log('Port 已成功连接')
    } catch (error) {
      console.error('初始连接失败:', error)
    }

    const sendToBackground = async (data: any) => {
      try {
        if (!port) {
          console.log('Port 不存在，尝试重新连接')
          port = await connect()
        }
        await port.postMessage(data)
        console.log('Content 发送 BACKGROUND 消息成功:', data)
      } catch (error) {
        console.error('发送消息失败，尝试最后一次重连:', error)
        try {
          // 确保之前的连接已断开
          disconnect()
          // 重新连接并发送
          port = await connect()
          await port.postMessage(data)
          console.log('重连后消息发送成功')
        } catch (retryError) {
          console.error('重试发送消息失败:', retryError)
          throw new Error('消息发送失败，请检查连接状态')
        }
      }
    }

    // 处理页面消息
    window.addEventListener('message', async (event) => {
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

    // 处理页面生命周期事件
    window.addEventListener('pagehide', () => {
      disconnect()
    })

    window.addEventListener('pageshow', async (event: PageTransitionEvent) => {
      if (event.persisted) { // 页面从 bfcache 恢复
        try {
          await connect()
          console.log('从 bfcache 恢复后重新连接成功')
        } catch (error) {
          console.error('从 bfcache 恢复后重新连接失败:', error)
        }
      }
    })
  },
})
