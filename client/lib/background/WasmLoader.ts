import { getConfig, logLevel } from '@/config/wasm'
import { walletStorage } from '@/lib/walletStorage'
import { browser } from 'wxt/browser'
import { setWasmReady } from './MessageHandler'

export async function loadStpWasm() {
  console.log('[WasmLoader] 开始加载 stpd.wasm')
  const go = new Go()
  const wasmPath = browser.runtime.getURL('/wasm/stpd.wasm')
  const env = walletStorage.getValue('env') || 'test'
  const response = await fetch(wasmPath)
  const wasmBinary = await response.arrayBuffer()
  const wasmModule = await WebAssembly.instantiate(wasmBinary, go.importObject)
  go.run(wasmModule.instance)
  await (globalThis as any).stp_wasm.init(getConfig(env), logLevel)
  console.log('[WasmLoader] stpd.wasm 加载完成')
}

export async function loadWalletWasm() {
  try {
    console.log('[WasmLoader] 开始加载 sat20wallet.wasm')
    importScripts('/wasm/wasm_exec.js')
    const go = new Go()
    const env = walletStorage.getValue('env') || 'test'
    const wasmPath = browser.runtime.getURL('/wasm/sat20wallet.wasm')
    const response = await fetch(wasmPath)
    const wasmBinary = await response.arrayBuffer()
    const wasmModule = await WebAssembly.instantiate(wasmBinary, go.importObject)
    go.run(wasmModule.instance)
    await (globalThis as any).sat20wallet_wasm.init(getConfig(env), logLevel)
    await loadStpWasm()
    setWasmReady(true)
    console.log('[WasmLoader] sat20wallet.wasm 加载完成，WASM已就绪')
  } catch (error) {
    console.error('[WasmLoader] 加载WASM失败:', error)
    throw error
  }
} 