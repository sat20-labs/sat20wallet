import KeepAliveConnection from '@/lib/message/KeepAliveConnection'
import Port from '@/lib/message/Port'
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
    const keepAlive = new KeepAliveConnection('CONTENT_SCRIPT')
    keepAlive.connect()
    const port = new Port({ name: Message.Port.CONTENT_BG })
    const channel = new BroadcastChannel(Message.Channel.INJECT_CONTENT)

    const sendToBackground = async (data: any) => {
      await port.postMessage(data)
      console.log('Content 发送 BACKGROUND 消息成功:', data)
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
    port.onMessage.addListener(async (event: any) => {
      console.log('Content 收到 BACKGROUND 消息:', event)

      const { metadata = {} } = event
      const { to, from } = metadata
      if (from === Message.MessageFrom.BACKGROUND) {
        console.log('Content 收到 BACKGROUND 消息:', event)
        if (to === Message.MessageTo.INJECTED) {
          channel.postMessage(event)
        }
      }
    })
  },
})
