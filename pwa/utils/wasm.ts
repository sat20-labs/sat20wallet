import walletManager from '@/utils/sat20'
import satsnetStp from '@/utils/stp'
import { getConfig, logLevel } from '@/config/wasm'
import { Network } from '@/types'
import { walletStorage } from '@/lib/walletStorage'

const getRuntimeConfig = async () => {
  // 从全局存储读取环境配置，确保与UI显示一致
  // 确保存储已初始化（重复调用是安全的）
  await walletStorage.initializeState()
  const env = walletStorage.getValue('env') || 'prd'
  const network = walletStorage.getValue('network') || Network.LIVENET
  return getConfig(env, network)
}

const instantiateGoWasm = async (path: string) => {
  const go = new (window as any).Go()
  const response = await fetch(path)
  const wasmBinary = await response.arrayBuffer()
  const wasmModule = await WebAssembly.instantiate(wasmBinary, go.importObject)
  go.run(wasmModule.instance)
}

const loadWalletWasm = async () => {
  const config = await getRuntimeConfig()
  await instantiateGoWasm(`${import.meta.env.BASE_URL}wasm/sat20wallet.wasm`)
  await walletManager.init(config, logLevel)
}

const loadStpWasm = async () => {
  const config = await getRuntimeConfig()
  await instantiateGoWasm(`${import.meta.env.BASE_URL}wasm/stpd.wasm`)
  await satsnetStp.init(config, logLevel)
}

export const loadWasm = async () => {
  await loadWalletWasm()
  await loadStpWasm()
}
