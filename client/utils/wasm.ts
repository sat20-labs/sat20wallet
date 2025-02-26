import wasmConfig from '@/config/wasm'
export const loadWasm = async () => {
  const go = new Go()
  // 获取和加载 WASM 二进制文件
  const wasmPath = browser.runtime.getURL('/wasm/sat20wallet.wasm')
  // const wasmPath = 'https://static.sat20.org/stpd.wasm'
  const response = await fetch(wasmPath)
  const wasmBinary = await response.arrayBuffer()

  // 初始化 WebAssembly 模块
  const wasmModule = await WebAssembly.instantiate(
    wasmBinary,
    go.importObject
  )
  console.log('wasmModule', wasmModule);
  
  // 启动 Go 运行时
  go.run(wasmModule.instance)

  console.log('satsnetStp', window.sat20wallet_wasm)
  await window.sat20wallet_wasm.init(wasmConfig.config, wasmConfig.logLevel)
  // const [errVersion, version] = await satsnetStp.getVersion()
  // console.log('version', errVersion, version)
  // if (version) {
  //   globalStore.setStpVersion(version)
  // }
  // const [err, result] = await satsnetStp.isWalletExist()
  // console.log('isWalletExist', err, result)

}