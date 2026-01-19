import { browser } from 'wxt/browser'
import { walletStorage } from '@/lib/walletStorage'
import { getConfig, logLevel } from '@/config/wasm'
import type { Network } from '@/types'

// The Go class is available globally in the service worker context
// after importScripts('/wasm/wasm_exec.js') is called.
declare const Go: any

export const initializeWasm = async (): Promise<void> => {
  try {
    console.log('调试: 开始加载 WASM 模块')
    importScripts('/wasm/wasm_exec.js')
    const go = new Go()
    const env = walletStorage.getValue('env') || 'test'
    const network = walletStorage.getValue('network') as Network
    console.log('调试: 加载 WASM 模块, 环境: ', env, '网络: ', network)
    console.log('调试: 加载 WASM 模块, 配置: ', getConfig(env, network))
    const wasmPath = browser.runtime.getURL('/wasm/sat20wallet.wasm')
    const response = await fetch(wasmPath)
    const wasmBinary = await response.arrayBuffer()
    const wasmModule = await WebAssembly.instantiate(
      wasmBinary,
      go.importObject,
    )
    go.run(wasmModule.instance)
    await (globalThis as any).sat20wallet_wasm.init(
      getConfig(env, network),
      logLevel,
    )
    console.log('调试: sat20wallet.wasm 加载并初始化完成')

    // await loadStpWasm()
    console.log('调试: 所有 WASM 模块加载成功')
  } catch (error) {
    console.error('调试: WASM 模块加载失败:', error)
    throw error
  }
}

export const reInitializeWasm = async (): Promise<void> => {
  const env = walletStorage.getValue('env') || 'test'
  const network = walletStorage.getValue('network') as Network
  console.log(`调试: 重新初始化WASM, 环境: ${env}, 网络: ${network}`)
  const config = getConfig(env, network)
  console.log('调试: 重新初始化WASM, 配置: ', config)
  try {
    if (!(globalThis as any).sat20wallet_wasm) {
      console.log('调试: WASM 未初始化，正在执行初始化...')
      await initializeWasm()
      return
    }
    await (globalThis as any).sat20wallet_wasm.release()
    await (globalThis as any).sat20wallet_wasm.init(config, logLevel)
    console.log('调试: WASM 重新初始化完成')
  } catch (error) {
    console.error('调试: WASM 重新初始化失败:', error)
    throw error
  }
}
