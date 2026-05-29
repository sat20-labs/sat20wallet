import walletManager from '@/utils/sat20'
import { getConfig, logLevel } from '@/config/wasm'
import { Network } from '@/types'
import { walletStorage } from '@/lib/walletStorage'

const loadWalletWasm = async () => {
  // 从全局存储读取环境配置，确保与UI显示一致
  // 确保存储已初始化（重复调用是安全的）
  await walletStorage.initializeState()
  const env = walletStorage.getValue('env') || 'prd'
  const network = walletStorage.getValue('network') || Network.LIVENET
  console.log('env', env, 'network', network)
  const go = new (window as any).Go()
  const wasmPath = '/wasm/sat20wallet.wasm'
  const response = await fetch(wasmPath)
  const wasmBinary = await response.arrayBuffer()
  const wasmModule = await WebAssembly.instantiate(wasmBinary, go.importObject)
  go.run(wasmModule.instance)
  const config = getConfig(env, network)
  console.log('wasm config', config)
  await walletManager.init(config, logLevel)
}

export const loadWasm = async () => {
  await loadWalletWasm()
}