import { tryit } from 'radash'
// import { Decimal } from 'decimal.js'; // 移除 Decimal 导入

// Define the expected response structure from WASM functions
interface WasmResponse<T = any> {
  code: number;
  msg?: string;
  data?: T;
}

// Define the interface for the WASM module attached to globalThis
// We assume all methods return a Promise resolving to WasmResponse
// Specific data types (T in WasmResponse<T>) can be refined if known
interface StpWasmModule {
  closeChannel: (...args: any[]) => Promise<WasmResponse>;
  isWalletExisting: (...args: any[]) => Promise<WasmResponse<boolean>>;
  createWallet: (...args: any[]) => Promise<WasmResponse>;
  hello: (...args: any[]) => Promise<WasmResponse<string>>;
  start: (...args: any[]) => Promise<WasmResponse>;
  importWallet: (...args: any[]) => Promise<WasmResponse>;
  init: (...args: any[]) => Promise<WasmResponse>;
  getVersion: (...args: any[]) => Promise<WasmResponse<string>>;
  registerCallback: (...args: any[]) => Promise<WasmResponse>;
  lockUtxo: (...args: any[]) => Promise<WasmResponse>;
  lockUtxo_SatsNet: (...args: any[]) => Promise<WasmResponse>;
  unlockUtxo: (...args: any[]) => Promise<WasmResponse>;
  unlockUtxo_SatsNet: (...args: any[]) => Promise<WasmResponse>;
  getAllLockedUtxo: (...args: any[]) => Promise<WasmResponse<any[]>>; // Assuming array
  getAllLockedUtxo_SatsNet: (...args: any[]) => Promise<WasmResponse<any[]>>; // Assuming array
  openChannel: (...args: any[]) => Promise<WasmResponse>;
  release: (...args: any[]) => Promise<WasmResponse>;
  getWallet: (...args: any[]) => Promise<WasmResponse>; // Specify wallet type if known
  getTickerInfo: (...args: any[]) => Promise<WasmResponse>; // Specify ticker info type if known
  runesAmtV2ToV3: (...args: any[]) => Promise<WasmResponse<string>>; // Assuming string result
  runesAmtV3ToV2: (...args: any[]) => Promise<WasmResponse<string>>; // Assuming string result
  getAllChannels: (...args: any[]) => Promise<WasmResponse<any[]>>; // Assuming array
  getChannel: (...args: any[]) => Promise<WasmResponse>; // Specify channel type if known
  getChannelStatus: (...args: any[]) => Promise<WasmResponse>; // Specify status type if known
  sendUtxos: (...args: any[]) => Promise<WasmResponse>;
  sendUtxos_SatsNet: (...args: any[]) => Promise<WasmResponse>;
  sendAssets_SatsNet: (...args: any[]) => Promise<WasmResponse>;
  sendAssets: (...args: any[]) => Promise<WasmResponse>;
  deposit: (...args: any[]) => Promise<WasmResponse>;
  withdraw: (...args: any[]) => Promise<WasmResponse>;
  unlockWallet: (...args: any[]) => Promise<WasmResponse>;
  getMnemonice: (...args: any[]) => Promise<WasmResponse<string>>; // Assuming mnemonic is string
  splicingIn: (...args: any[]) => Promise<WasmResponse>;
  splicingOut: (...args: any[]) => Promise<WasmResponse>;
  lockToChannel: (...args: any[]) => Promise<WasmResponse>;
  unlockFromChannel: (...args: any[]) => Promise<WasmResponse>;
  getUtxos: (...args: any[]) => Promise<WasmResponse<any[]>>; // Assuming array
  getUtxos_SatsNet: (...args: any[]) => Promise<WasmResponse<any[]>>; // Assuming array
  getUtxosWithAsset: (...args: any[]) => Promise<WasmResponse<any[]>>; // Assuming array
  getUtxosWithAsset_SatsNet: (...args: any[]) => Promise<WasmResponse<any[]>>; // Assuming array
  getUtxosWithAssetV2: (...args: any[]) => Promise<WasmResponse<any[]>>; // Assuming array
  getUtxosWithAssetV2_SatsNet: (...args: any[]) => Promise<WasmResponse<any[]>>; // Assuming array
  // --- Added Asset Amount Getters ---
  getAssetAmount: (...args: any[]) => Promise<WasmResponse<{ amount: string; value: string }>>;
  getAssetAmount_SatsNet: (...args: any[]) => Promise<WasmResponse<{ amount: string; value: string }>>;
  batchSendAssets_SatsNet: (...args: any[]) => Promise<WasmResponse<any>>;
  getTxAssetInfoFromPsbt: (...args: any[]) => Promise<WasmResponse<any>>;
  getTxAssetInfoFromPsbt_SatsNet: (...args: any[]) => Promise<WasmResponse<any>>;
  getCommitTxAssetInfo: (...args: any[]) => Promise<WasmResponse<any>>;
}


class SatsnetStp {
  // Use WasmResponse for the result type
  private async _handleRequest<T>(
    methodName: keyof StpWasmModule, // Use keyof for type safety
    ...args: any[]
  ): Promise<[Error | undefined, T | undefined]> {
    console.log('stp method', methodName)
    console.log('stp arg', args)
    // Check if running in a browser-like environment with 'window' and 'window.stp_wasm'
    const globalStp = (globalThis as any).stp_wasm;

    // Use double assertion (as unknown as StpWasmModule) to bypass strict overlap checks
    const stpModuleTyped = globalStp
      ? (globalStp as unknown as StpWasmModule)
      : undefined;

    if (!stpModuleTyped || typeof stpModuleTyped[methodName] !== 'function') {
      const errorMsg = `stp_wasm or method "${methodName}" not found on globalThis.`
      console.error(errorMsg)
      return [new Error(errorMsg), undefined]
    }
    const method = stpModuleTyped[methodName] as (...args: any[]) => Promise<WasmResponse<T>>; // Use the correctly typed module

    const [err, result] = await tryit(method)(...args)

    if (err) {
      console.error(`stp ${methodName} error: ${err.message}`)
      return [err, undefined]
    }

    // Check if result is defined and has 'code' property before accessing
    if (result && typeof result.code === 'number' && result.code !== 0) {
      // Use optional chaining for msg, provide default message
      const errorMsg = result.msg || `stp ${methodName} failed with code ${result.code}`;
      console.error(errorMsg);
      return [new Error(errorMsg), undefined];
    }

    // Return data using optional chaining
    return [undefined, result?.data]
  }

  async closeChannel(
    chanPoint: string,
    feeRate: number,
    force: boolean
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'closeChannel', // Pass method name as string (keyof StpWasmModule)
      chanPoint,
      String(feeRate),
      force
    )
  }
  async isWalletExisting(): Promise<[Error | undefined, boolean | undefined]> { // Refined return type
    return this._handleRequest<boolean>('isWalletExisting')
  }

  async createWallet(
    password: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'createWallet',
      password.toString()
    )
  }

  async hello(): Promise<[Error | undefined, string | undefined]> { // Refined return type
    return this._handleRequest<string>('hello')
  }

  async start(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('start')
  }

  async importWallet(
    mnemonic: string,
    password: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'importWallet',
      mnemonic,
      password.toString()
    )
  }

  async init(
    cfg: any,
    logLevel: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('init', cfg, logLevel)
  }
  async getVersion(): Promise<[Error | undefined, string | undefined]> { // Refined return type
    return this._handleRequest<string>('getVersion')
  }
  async registerCallback(
    cb: any
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'registerCallback',
      cb
    )
  }

  async lockUtxo(
    address: string,
    utxo: any,
    reason?: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('lockUtxo', address, utxo, reason)
  }

  async lockUtxo_SatsNet(
    address: string,
    utxo: any,
    reason?: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'lockUtxo_SatsNet',
      address,
      utxo,
      reason
    )
  }

  async unlockUtxo(
    address: string,
    utxo: any
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('unlockUtxo', address, utxo)
  }

  async unlockUtxo_SatsNet(
    address: string,
    utxo: any
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'unlockUtxo_SatsNet',
      address,
      utxo
    )
  }

  async getAllLockedUtxo(address: string): Promise<[Error | undefined, any[] | undefined]> { // Refined return type
    return this._handleRequest<any[]>(
      'getAllLockedUtxo',
      address
    )
  }

  async getAllLockedUtxo_SatsNet(address: string): Promise<[Error | undefined, any[] | undefined]> { // Refined return type
    return this._handleRequest<any[]>(
      'getAllLockedUtxo_SatsNet',
      address
    )
  }

  async openChannel(
    feeRate: number,
    amt: number,
    utxoList: string[],
    memo: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'openChannel',
      String(feeRate),
      String(amt),
      utxoList,
      memo
    )
  }

  async release(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('release')
  }

  async getWallet(): Promise<[Error | undefined, any | undefined]> { // TODO: Specify wallet type
    return this._handleRequest('getWallet')
  }
  async getTickerInfo(
    asset: string
  ): Promise<[Error | undefined, any | undefined]> { // TODO: Specify ticker info type
    return this._handleRequest(
      'getTickerInfo',
      asset
    )
  }
  async runesAmtV2ToV3(
    asset: string,
    assetAmt: string | number
  ): Promise<[Error | undefined, string | undefined]> { // Refined return type
    return this._handleRequest<string>(
      'runesAmtV2ToV3',
      asset,
      assetAmt // Keep original type, WASM might handle number/string
    )
  }
  async runesAmtV3ToV2(
    asset: string,
    assetAmt: string | number
  ): Promise<[Error | undefined, string | undefined]> { // Refined return type
    return this._handleRequest<string>(
      'runesAmtV3ToV2',
      asset,
      String(assetAmt) // Explicitly convert to string if required by WASM
    )
  }

  async getAllChannels(): Promise<[Error | undefined, any | undefined]> { // Refined return type
    return this._handleRequest<any[]>('getAllChannels')
  }

  async getChannel(id: string): Promise<[Error | undefined, any | undefined]> { // TODO: Specify channel type
    return this._handleRequest('getChannel', id)
  }
  async getChannelStatus(
    id: string
  ): Promise<[Error | undefined, any | undefined]> { // TODO: Specify status type
    return this._handleRequest(
      'getChannelStatus',
      id
    )
  }

  async sendUtxos(
    address: string,
    utxos: string[],
    amt: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'sendUtxos',
      address,
      utxos,
      String(amt)
    )
  }
  async sendUtxos_SatsNet(
    address: string,
    utxos: string[],
    amt: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'sendUtxos_SatsNet',
      address,
      utxos,
      amt // Keep original type
    )
  }
  async sendAssets_SatsNet(
    address: string,
    assetName: string,
    amt: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'sendAssets_SatsNet',
      address,
      assetName,
      String(amt)
    )
  }
  async sendAssets(
    address: string,
    assetName: string,
    amt: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'sendAssets',
      address,
      assetName,
      String(amt)
    )
  }
  async deposit(
    destAddr: string,
    assetName: string,
    amt: string,
    utxos: string[],
    fees: string[],
    feeRate: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'deposit',
      destAddr,
      assetName,
      amt, // Keep original type
      utxos,
      fees,
      String(feeRate)
    )
  }
  async withdraw(
    destAddr: string,
    assetName: string,
    amt: string,
    utxos: string[],
    fees: string[],
    feeRate: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'withdraw',
      destAddr,
      assetName,
      amt, // Keep original type
      utxos,
      fees,
      String(feeRate)
    )
  }
  async unlockWallet(
    password: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'unlockWallet',
      password.toString()
    )
  }
  async getMnemonice(
    password: string
  ): Promise<[Error | undefined, string | undefined]> { // Refined return type
    return this._handleRequest<string>(
      'getMnemonice',
      password.toString()
    )
  }
  async splicingIn(
    chanPoint: string,
    assetName: string,
    utxos: string[],
    fees: string[],
    feeRate: number,
    amt: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'splicingIn',
      chanPoint,
      assetName,
      utxos,
      fees,
      String(feeRate),
      String(amt)
    )
  }
  async splicingOut(
    chanPoint: string,
    toAddress: string,
    assetName: string,
    fees: string[],
    feeRate: number,
    amt: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'splicingOut',
      chanPoint,
      toAddress,
      assetName,
      fees,
      String(feeRate),
      String(amt)
    )
  }

  async lockToChannel(
    chanPoint: string,
    assetName: string,
    amt: number,
    utxos: string[],
    feeUtxoList?: any[]
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'lockToChannel',
      chanPoint,
      assetName,
      String(amt),
      utxos,
      feeUtxoList
    )
  }

  async unlockFromChannel(
    channelUtxo: string,
    assetName: string,
    amt: number,
    feeUtxoList?: any[]
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'unlockFromChannel',
      channelUtxo,
      assetName,
      String(amt),
      feeUtxoList
    )
  }

  // --- Added UTXO Getter Methods ---

  async getUtxos(): Promise<[Error | undefined, any[] | undefined]> { // Refined return type
    return this._handleRequest<any[]>('getUtxos')
  }

  async getUtxos_SatsNet(): Promise<[Error | undefined, any[] | undefined]> { // Refined return type
    return this._handleRequest<any[]>('getUtxos_SatsNet')
  }

  async getUtxosWithAsset(address: string, amt: number, assetName: string): Promise<[Error | undefined, any[] | undefined]> { // Refined return type, Added address parameter
    return this._handleRequest<any[]>('getUtxosWithAsset', address, amt, assetName) // Pass address
  }

  async getUtxosWithAsset_SatsNet(address: string, amt: string, assetName: string): Promise<[Error | undefined, any[] | undefined]> { // Refined return type, Added address parameter
    return this._handleRequest<any[]>('getUtxosWithAsset_SatsNet', address, amt, assetName) // Pass address
  }

  async getUtxosWithAssetV2(address: string, amt: number, assetName: string): Promise<[Error | undefined, any[] | undefined]> { // Refined return type, Added address parameter
    return this._handleRequest<any[]>('getUtxosWithAssetV2', address, amt, assetName) // Pass address
  }

  async getUtxosWithAssetV2_SatsNet(address: string, amt: number, assetName: string): Promise<[Error | undefined, any[] | undefined]> { // Refined return type, Added address parameter
    return this._handleRequest<any[]>('getUtxosWithAssetV2_SatsNet', address, amt, assetName) // Pass address
  }
  // --- End Added UTXO Getter Methods ---

  // --- Added Asset Amount Getter Methods ---
  async getAssetAmount(address: string, assetName: string): Promise<[Error | undefined, { amount: string; value: string } | undefined]> {
    return this._handleRequest<{ amount: string; value: string }>('getAssetAmount', address, assetName);
  }

  async getAssetAmount_SatsNet(address: string, assetName: string): Promise<[Error | undefined, { amount: string; value: string } | undefined]> {
    return this._handleRequest<{ amount: string; value: string }>('getAssetAmount_SatsNet', address, assetName);
  }
  async batchSendAssets_SatsNet(destAddr: string,
    assetName: string, amt: string, n: number): Promise<[Error | undefined, { amount: string; value: string } | undefined]> {
    return this._handleRequest<any>('batchSendAssets_SatsNet', destAddr, assetName, amt, n);
  }
  async getTxAssetInfoFromPsbt(
    psbtHex: string,
    network: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getTxAssetInfoFromPsbt', psbtHex, network)
  }

  async getTxAssetInfoFromPsbt_SatsNet(
    psbtHex: string,
    network: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getTxAssetInfoFromPsbt_SatsNet', psbtHex, network)
  }
  async getCommitTxAssetInfo(
    channelId: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getCommitTxAssetInfo', channelId)
  }
  // --- End Added Asset Amount Getter Methods ---
}

export default new SatsnetStp()
