import wasmConfig from '@/config/wasm'
import satsnetStp from '@/utils/stp'
import walletManager from '@/utils/sat20'
const loadWalletWasm = async () => {
  const go = new Go()
  const wasmPath = browser.runtime.getURL('/wasm/sat20wallet.wasm')
  const response = await fetch(wasmPath)
  const wasmBinary = await response.arrayBuffer()
  const wasmModule = await WebAssembly.instantiate(wasmBinary, go.importObject)
  go.run(wasmModule.instance)
  await walletManager.init(wasmConfig.config as any, wasmConfig.logLevel)
}

const loadStpWasm = async () => {
  const go = new Go()
  const wasmPath = browser.runtime.getURL('/wasm/stpd.wasm')
  const response = await fetch(wasmPath)
  const wasmBinary = await response.arrayBuffer()
  const wasmModule = await WebAssembly.instantiate(wasmBinary, go.importObject)
  go.run(wasmModule.instance)
  await satsnetStp.init(wasmConfig.config, wasmConfig.logLevel)
}
export const loadWasm = async () => {
  await loadWalletWasm()
  await loadStpWasm()
}
