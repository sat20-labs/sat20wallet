import { tryit } from 'radash'

// Define the expected response structure from WASM functions
interface WasmResponse<T = any> {
  code: number;
  msg?: string;
  data?: T;
}

// Define the interface for the WASM module attached to globalThis
// 只保留 STP 特有的方法
interface StpWasmModule {
  // STP wasm 自己的基础方法
  init: (...args: any[]) => Promise<WasmResponse>;
  registerCallback: (...args: any[]) => Promise<WasmResponse>;

  // 钱包状态同步方法 (stp wasm 需要与 sat20 wasm 保持状态同步)
  switchWallet: (...args: any[]) => Promise<WasmResponse>;
  switchAccount: (...args: any[]) => Promise<WasmResponse>;
  importWallet: (...args: any[]) => Promise<WasmResponse>;
  importWalletWithPrivKey: (...args: any[]) => Promise<WasmResponse>;
  unlockWallet: (...args: any[]) => Promise<WasmResponse>;

  // 通道管理
  closeChannel: (...args: any[]) => Promise<WasmResponse>;
  isWalletExisting: (...args: any[]) => Promise<WasmResponse<boolean>>;
  hello: (...args: any[]) => Promise<WasmResponse<string>>;
  start: (...args: any[]) => Promise<WasmResponse>;
  openChannel: (...args: any[]) => Promise<WasmResponse>;
  release: (...args: any[]) => Promise<WasmResponse>;
  getWallet: (...args: any[]) => Promise<WasmResponse>;
  runesAmtV2ToV3: (...args: any[]) => Promise<WasmResponse<string>>;
  runesAmtV3ToV2: (...args: any[]) => Promise<WasmResponse<string>>;
  getAllChannels: (...args: any[]) => Promise<WasmResponse<any[]>>;
  getCurrentChannel: (...args: any[]) => Promise<WasmResponse>;
  getChannel: (...args: any[]) => Promise<WasmResponse>;
  getChannelStatus: (...args: any[]) => Promise<WasmResponse>;
  splicingIn: (...args: any[]) => Promise<WasmResponse>;
  splicingOut: (...args: any[]) => Promise<WasmResponse>;
  lockToChannel: (...args: any[]) => Promise<WasmResponse>;
  unlockFromChannel: (...args: any[]) => Promise<WasmResponse>;
  getCommitTxAssetInfo: (...args: any[]) => Promise<WasmResponse<any>>;
  deployContract_Local: (templateName: string, content: string, feeRate: string) => Promise<WasmResponse<{ txId: string; resvId: string }>>;
  deployContract_Remote: (templateName: string, content: string, feeRate: string, bol: boolean) => Promise<WasmResponse<{ txId: string; resvId: string }>>;
  stakeToBeMiner: (bCoreNode: boolean, btcFeeRate: string) => Promise<WasmResponse<{ txId: string; resvId: string; assetName: string; amt: string }>>;
  splitBatchSignedPsbt: (signedHex: string, network: string) => Promise<WasmResponse<{ psbts: string[] }>>;
  addInputsToPsbt: (psbtHex: string, utxos: string[]) => Promise<WasmResponse<{ psbt: string }>>;
  addOutputsToPsbt: (psbtHex: string, utxos: string[]) => Promise<WasmResponse<{ psbt: string }>>;
}

class SatsnetStp {
  private async _handleRequest<T>(
    methodName: keyof StpWasmModule,
    ...args: any[]
  ): Promise<[Error | undefined, T | undefined]> {
    const globalStp = (globalThis as any).stp_wasm;

    const stpModuleTyped = globalStp
      ? (globalStp as unknown as StpWasmModule)
      : undefined;

    if (!stpModuleTyped || typeof stpModuleTyped[methodName] !== 'function') {
      const errorMsg = `stp_wasm or method "${methodName}" not found on globalThis.`
      console.error(errorMsg)
      return [new Error(errorMsg), undefined]
    }
    const method = stpModuleTyped[methodName] as (...args: any[]) => Promise<WasmResponse<T>>;
    console.log('stp method', methodName, args);
    const [err, result] = await tryit(method)(...args)
    console.log('stp method', methodName, args, result);

    if (err) {
      console.error(`stp ${methodName} error: ${err.message}`)
      return [err, undefined]
    }

    if (result && typeof result.code === 'number' && result.code !== 0) {
      const errorMsg = result.msg || `stp ${methodName} failed with code ${result.code}`;
      console.error(errorMsg);
      return [new Error(errorMsg), undefined];
    }

    // Return data using optional chaining
    return [undefined, result?.data]
  }

  // --- STP 特有方法 (通道管理相关) ---

  async init(
    cfg: any,
    logLevel: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('init', cfg, logLevel)
  }

  async registerCallback(
    cb: any
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('registerCallback', cb)
  }

  // --- 钱包状态同步方法 (stp wasm 需要与 sat20 wasm 保持状态同步) ---

  async switchWallet(
    id: string,
    password: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('switchWallet', id, password)
  }

  async switchAccount(
    id: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('switchAccount', id)
  }

  async importWallet(
    mnemonic: string,
    password: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('importWallet', mnemonic, password.toString())
  }

  async importWalletWithPrivKey(
    privKey: string,
    password: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('importWalletWithPrivKey', privKey, password.toString())
  }

  async unlockWallet(
    password: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('unlockWallet', password.toString())
  }

  async closeChannel(
    chanPoint: string,
    feeRate: number,
    force: boolean
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'closeChannel',
      chanPoint,
      String(feeRate),
      force
    )
  }

  async isWalletExisting(): Promise<[Error | undefined, boolean | undefined]> {
    return this._handleRequest<boolean>('isWalletExisting')
  }

  async hello(): Promise<[Error | undefined, string | undefined]> {
    return this._handleRequest<string>('hello')
  }

  async start(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('start')
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

  async getWallet(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getWallet')
  }

  async runesAmtV2ToV3(
    asset: string,
    assetAmt: string | number
  ): Promise<[Error | undefined, string | undefined]> {
    return this._handleRequest<string>(
      'runesAmtV2ToV3',
      asset,
      assetAmt
    )
  }

  async runesAmtV3ToV2(
    asset: string,
    assetAmt: string | number
  ): Promise<[Error | undefined, string | undefined]> {
    return this._handleRequest<string>(
      'runesAmtV3ToV2',
      asset,
      String(assetAmt)
    )
  }

  async getAllChannels(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest<any[]>('getAllChannels')
  }

  async getChannel(id: string): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getChannel', id)
  }

  async getCurrentChannel(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getCurrentChannel')
  }

  async getChannelStatus(
    id: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'getChannelStatus',
      id
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
      chanPoint.toString(),
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
      chanPoint.toString(),
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
      chanPoint.toString(),
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
      channelUtxo.toString(),
      assetName,
      String(amt),
      feeUtxoList
    )
  }

  async getCommitTxAssetInfo(
    channelId: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getCommitTxAssetInfo', channelId)
  }

  /** 本地部署合约 */
  async deployContract_Local(
    templateName: string,
    content: string,
    feeRate: string
  ): Promise<[
    Error | undefined,
    { txId: string; resvId: string } | undefined
  ]> {
    return this._handleRequest<{ txId: string; resvId: string }>('deployContract_Local', templateName, content, feeRate)
  }

  /** 远程部署合约 */
  async deployContract_Remote(
    templateName: string,
    content: string,
    feeRate: string,
    bol: boolean
  ): Promise<[
    Error | undefined,
    { txId: string; resvId: string } | undefined
  ]> {
    return this._handleRequest<{ txId: string; resvId: string }>('deployContract_Remote', templateName, content, feeRate, bol)
  }

  /** 质押成为矿工/核心节点 */
  async stakeToBeMiner(
    bCoreNode: boolean,
    btcFeeRate: string
  ): Promise<[Error | undefined, { txId: string; resvId: string; assetName: string; amt: string } | undefined]> {
    return this._handleRequest<{ txId: string; resvId: string; assetName: string; amt: string }>('stakeToBeMiner', bCoreNode, btcFeeRate)
  }

  async splitBatchSignedPsbt(
    signedHex: string,
    network: string
  ): Promise<[Error | undefined, { psbts: string[] } | undefined]> {
    return this._handleRequest('splitBatchSignedPsbt', signedHex, network)
  }

  async addInputsToPsbt(
    psbtHex: string,
    utxos: string[]
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return this._handleRequest('addInputsToPsbt', psbtHex, utxos)
  }

  async addOutputsToPsbt(
    psbtHex: string,
    utxos: string[]
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return this._handleRequest('addOutputsToPsbt', psbtHex, utxos)
  }
}

export default new SatsnetStp()
