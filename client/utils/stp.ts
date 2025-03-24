import { tryit } from 'radash'

class SatsnetStp {
  private async _handleRequest(
    method: (...args: any[]) => any,
    methodName: string,
    ...args: any[]
  ): Promise<[Error | undefined, any | undefined]> {
    console.log('stp method', methodName)
    console.log('stp arg', args)

    const [err, result] = await tryit(method)(...args)

    if (err) {
      console.error(`stp ${methodName} error: ${err.message}`)
      return [err, undefined]
    } else if (result && result?.code !== 0) {
      console.error(`stp ${methodName} error: ${result.msg}`)
      return [new Error(result.msg), undefined]
    }
    return [err, result?.data]
  }

  async closeChannel(
    chanPoint: string,
    feeRate: number,
    force: boolean
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.closeChannel,
      'closeChannel',
      chanPoint,
      String(feeRate),
      force
    )
  }
  async isWalletExisting(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(window.stp_wasm.isWalletExisting, 'isWalletExisting')
  }

  async createWallet(
    password: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.createWallet,
      'createWallet',
      password.toString()
    )
  }

  async hello(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(window.stp_wasm.hello, 'hello')
  }

  async start(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(window.stp_wasm.start, 'start')
  }

  async importWallet(
    mnemonic: string,
    password: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.importWallet,
      'importWallet',
      mnemonic,
      password.toString()
    )
  }

  async init(
    cfg: any,
    logLevel: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(window.stp_wasm.init, 'init', cfg, logLevel)
  }
  async getVersion(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(window.stp_wasm.getVersion, 'getVersion')
  }
  async registerCallback(
    cb: any
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.registerCallback,
      'registerCallback',
      cb
    )
  }

  async lockUtxo(
    chanPoint: string,
    assetName: string,
    amt: number,
    utxos: string[],
    feeUtxoList?: any[]
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.lockUtxo,
      'lockUtxo',
      chanPoint,
      assetName,
      String(amt),
      utxos,
      feeUtxoList
    )
  }

  async openChannel(
    feeRate: number,
    amt: number,
    utxoList: string[],
    memo: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.openChannel,
      'openChannel',
      String(feeRate),
      String(amt),
      utxoList,
      memo
    )
  }

  async release(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(window.stp_wasm.release, 'release')
  }

  async getWallet(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(window.stp_wasm.getWallet, 'getWallet')
  }
  async getTickerInfo(
    asset: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.getTickerInfo,
      'getTickerInfo',
      asset
    )
  }
  async runesAmtV2ToV3(
    asset: string,
    assetAmt: string | number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.runesAmtV2ToV3,
      'runesAmtV2ToV3',
      asset,
      assetAmt
    )
  }
  async runesAmtV3ToV2(
    asset: string,
    assetAmt: string | number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.runesAmtV3ToV2,
      'runesAmtV3ToV2',
      asset,
      String(assetAmt)
    )
  }

  async getAllChannels(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(window.stp_wasm.getAllChannels, 'getAllChannels')
  }

  async getChannel(id: string): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(window.stp_wasm.getChannel, 'getChannel', id)
  }
  async getChannelStatus(
    id: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.getChannelStatus,
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
      window.stp_wasm.sendUtxos,
      'sendUtxos',
      address,
      utxos,
      String(amt)
    )
  }
  async sendUtxosSatsNet(
    address: string,
    utxos: string[],
    amt: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.sendUtxos_SatsNet,
      'sendUtxos_SatsNet',
      address,
      utxos,
      amt
    )
  }
  async sendAssetsSatsNet(
    address: string,
    assetName: string,
    amt: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.sendAssets_SatsNet,
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
      window.stp_wasm.sendAssets,
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
      window.stp_wasm.deposit,
      'deposit',
      destAddr,
      assetName,
      amt,
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
      window.stp_wasm.withdraw,
      'withdraw',
      destAddr,
      assetName,
      amt,
      utxos,
      fees,
      String(feeRate)
    )
  } 
  async unlockUtxo(
    channelUtxo: string,
    assetName: string,
    amt: number,
    feeUtxoList?: any[]
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.unlockUtxo,
      'unlockUtxo',
      channelUtxo,
      assetName,
      String(amt),
      feeUtxoList
    )
  }

  async unlockWallet(
    password: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.unlockWallet,
      'unlockWallet',
      password.toString()
    )
  }
  async getMnemonice(
    password: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      window.stp_wasm.getMnemonice,
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
      window.stp_wasm.splicingIn,
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
      window.stp_wasm.splicingOut,
      'splicingOut',
      chanPoint,
      toAddress,
      assetName,
      fees,
      String(feeRate),
      String(amt)
    )
  }
}

export default new SatsnetStp()
