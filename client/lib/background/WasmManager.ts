import { browser } from 'wxt/browser'
import { walletStorage } from '@/lib/walletStorage'
import { getConfig, logLevel } from '@/config/wasm'
import { Network } from '@/types'

// The Go class is available globally in the service worker context
// after importScripts('/wasm/wasm_exec.js') is called.
declare const Go: any

const loadStpWasm = async () => {
  console.log('调试: 开始加载 stpd.wasm')
  const go = new Go()
  const wasmPath = browser.runtime.getURL('/wasm/stpd.wasm')
  const env = walletStorage.getValue('env') || 'test'
  const network = walletStorage.getValue('network') as Network
  const response = await fetch(wasmPath)
  const wasmBinary = await response.arrayBuffer()
  const wasmModule = await WebAssembly.instantiate(
    wasmBinary,
    go.importObject,
  )
  go.run(wasmModule.instance)
  await (globalThis as any).stp_wasm.init(getConfig(env, network), logLevel)
  console.log('调试: stpd.wasm 加载并初始化完成')
}

export const initializeWasm = async (): Promise<void> => {
  try {
    console.log('调试: 开始加载 WASM 模块')
    importScripts('/wasm/wasm_exec.js')
    const go = new Go()
    const env = walletStorage.getValue('env') || 'test'
    const network = walletStorage.getValue('network') as Network
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
  console.log(`调试: 重新初始化WASM, 环境: ${env}, 网络: ${network}`);
  await (globalThis as any).sat20wallet_wasm.release()
  await (globalThis as any).sat20wallet_wasm.init(getConfig(env, network), logLevel)
  console.log('调试: WASM 重新初始化完成');
} 