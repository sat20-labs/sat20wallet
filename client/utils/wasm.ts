import satsnetStp from '@/utils/stp'
import walletManager from '@/utils/sat20'
import { walletStorage } from '@/lib/walletStorage'
import { getConfig, logLevel } from '@/config/wasm'
import { Network } from '@/types'

const loadWalletWasm = async () => {
  const env = walletStorage.getValue('env') || 'test'
  const network = walletStorage.getValue('network') as Network
  console.log('env', env, 'network', network)
  const go = new Go()
  const wasmPath = browser.runtime.getURL('/wasm/sat20wallet.wasm')
  const response = await fetch(wasmPath)
  const wasmBinary = await response.arrayBuffer()
  const wasmModule = await WebAssembly.instantiate(wasmBinary, go.importObject)
  go.run(wasmModule.instance)
  const config = getConfig(env, network)
  console.log('wasm config', config)
  await walletManager.init(config, logLevel)
}

const loadStpWasm = async () => {
  const env = walletStorage.getValue('env') || 'test'
  const network = walletStorage.getValue('network') as Network
  const go = new Go()
  const wasmPath = browser.runtime.getURL('/wasm/stpd.wasm')
  const response = await fetch(wasmPath)
  const wasmBinary = await response.arrayBuffer()
  const wasmModule = await WebAssembly.instantiate(wasmBinary, go.importObject)
  go.run(wasmModule.instance)
  const config = getConfig(env, network)
  console.log('wasm config', config)
  await satsnetStp.init(config, logLevel)
}

export const loadWasm = async () => {
  await loadWalletWasm()
  await loadStpWasm()
}
