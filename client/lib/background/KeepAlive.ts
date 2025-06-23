import { browser } from 'wxt/browser'
import type { Runtime } from 'wxt/browser'

const KEEP_ALIVE_PORT_NAME = 'KEEP_ALIVE_INTERVAL'

function handleKeepAliveConnection(port: Runtime.Port) {
  if (port.name !== KEEP_ALIVE_PORT_NAME) return
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

export function setupKeepAlive() {
  console.log('调试: 初始化 Service Worker 保持激活机制')
  browser.runtime.onConnect.addListener(handleKeepAliveConnection)
} 