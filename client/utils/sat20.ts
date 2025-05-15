import { tryit } from 'radash'
class WalletManager {
  private async _handleRequest(
    methodName: string,
    ...args: any[]
  ): Promise<[Error | undefined, any | undefined]> {
    console.log('wallet method', methodName)
    console.log('wallet arg', args)
    const method = window.sat20wallet_wasm[methodName as keyof WalletManager]
    const [err, result] = await tryit(method as any)(...args)
    console.log('wallet result', result);

    if (err) {
      console.error(`${methodName} error: ${err.message}`)
      return [err, undefined]
    }

    if (result) {
      const response = result as SatsnetResponse
      if (response?.code !== 0) {
        return [new Error(response.msg), undefined]
      }
      return [undefined, response.data]
    }

    return [undefined, undefined]
  }

  async createWallet(
    password: string
  ): Promise<
    [Error | undefined, { walletId: string; mnemonic: string } | undefined]
  > {
    return this._handleRequest('createWallet', password.toString())
  }

  async importWallet(
    mnemonic: string,
    password: string
  ): Promise<[Error | undefined, { walletId: string } | undefined]> {
    return this._handleRequest('importWallet', mnemonic, password.toString())
  }

  async unlockWallet(
    password: string
  ): Promise<[Error | undefined, { walletId: number } | undefined]> {
    return this._handleRequest('unlockWallet', password.toString())
  }

  async getAllWallets(): Promise<
    [Error | undefined, Map<number, number> | undefined]
  > {
    return this._handleRequest('getAllWallets')
  }

  async switchWallet(
    id: string
  ): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('switchWallet', id)
  }

  async switchAccount(
    accountIndex: number
  ): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('switchAccount', accountIndex)
  }

  async switchChain(
    chain: string
  ): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('switchChain', chain)
  }

  async getChain(): Promise<[Error | undefined, string | undefined]> {
    return this._handleRequest('getChain')
  }

  async getMnemonice(
    id: number,
    password: string
  ): Promise<[Error | undefined, { mnemonic: string } | undefined]> {
    return this._handleRequest('getMnemonice', id, password)
  }

  async signMessage(
    msg: string
  ): Promise<[Error | undefined, { signature: string } | undefined]> {
    return this._handleRequest('signMessage', msg)
  }

  async signPsbt(
    psbtHex: string,
    bool: boolean
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return this._handleRequest('signPsbt', psbtHex, bool)
  }



  async extractTxFromPsbt(
    psbtHex: string
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return this._handleRequest('signPsbt', psbtHex)
  }

  async signPsbt_SatsNet(
    psbtHex: string,
    bool: boolean
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return this._handleRequest('signPsbt_SatsNet', psbtHex, bool)
  }

  async extractTxFromPsbt_SatsNet(
    psbtHex: string
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return this._handleRequest('signPsbt_SatsNet', psbtHex)
  }

  async sendUtxos_SatsNet(
    destAddr: string,
    utxos: string[],
    fees: string[]
  ): Promise<[Error | undefined, string | undefined]> {
    return this._handleRequest('sendUtxos_SatsNet', destAddr, utxos, fees)
  }

  async sendAssets_SatsNet(
    destAddr: string,
    assetName: string,
    amt: number
  ): Promise<[Error | undefined, string | undefined]> {
    return this._handleRequest('sendAssets_SatsNet', destAddr, assetName, amt)
  }

  async init(
    config: any,
    logLevel: number
  ): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('init', config, logLevel)
  }

  async release(): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('release')
  }

  async isWalletExist(): Promise<
    [Error | undefined, { exists: boolean } | undefined]
  > {
    return this._handleRequest('isWalletExist')
  }

  async getVersion(): Promise<
    [Error | undefined, { version: string } | undefined]
  > {
    return this._handleRequest('getVersion')
  }

  async registerCallback(
    callback: Function
  ): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('registerCallback', callback)
  }

  async getWalletAddress(
    accountId: number
  ): Promise<[Error | undefined, { address: string } | undefined]> {
    return this._handleRequest('getWalletAddress', accountId)
  }

  async getWalletPubkey(
    accountId: number
  ): Promise<[Error | undefined, { pubKey: string } | undefined]> {
    return this._handleRequest('getWalletPubkey', accountId)
  }

  async getPaymentPubKey(): Promise<[Error | undefined, string | undefined]> {
    return this._handleRequest('getPaymentPubKey')
  }

  async getPublicKey(
    id: number
  ): Promise<[Error | undefined, Uint8Array | undefined]> {
    return this._handleRequest('getPublicKey', id)
  }

  async getCommitRootKey(
    peer: Uint8Array
  ): Promise<[Error | undefined, Uint8Array | undefined]> {
    return this._handleRequest('getCommitRootKey', peer)
  }

  async getCommitSecret(
    peer: Uint8Array,
    index: number
  ): Promise<[Error | undefined, Uint8Array | undefined]> {
    return this._handleRequest('getCommitSecret', peer, index)
  }

  async deriveRevocationPrivKey(
    commitSecret: Uint8Array
  ): Promise<[Error | undefined, Uint8Array | undefined]> {
    return this._handleRequest('deriveRevocationPrivKey', commitSecret)
  }

  async getRevocationBaseKey(): Promise<
    [Error | undefined, Uint8Array | undefined]
  > {
    return this._handleRequest('getRevocationBaseKey')
  }

  async getNodePubKey(): Promise<[Error | undefined, Uint8Array | undefined]> {
    return this._handleRequest('getNodePubKey')
  }

  async buildBatchSellOrder_SatsNet(
    utxos: string[],
    address: string,
    network: string
  ): Promise<[Error | undefined, { orderId: string } | undefined]> {
    return this._handleRequest('buildBatchSellOrder_SatsNet', utxos, address, network)
  }

  async splitBatchSignedPsbt(
    signedHex: string,
    network: string
  ): Promise<[Error | undefined, { psbts: string[] } | undefined]> {
    return this._handleRequest('splitBatchSignedPsbt', signedHex, network)
  }

  async finalizeSellOrder_SatsNet(
    psbtHex: string,
    utxos: string[],
    buyerAddress: string,
    serverAddress: string,
    network: string,
    serviceFee: number,
    networkFee: number
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return this._handleRequest(
      'finalizeSellOrder_SatsNet',
      psbtHex,
      utxos,
      buyerAddress,
      serverAddress,
      network,
      serviceFee,
      networkFee
    )
  }
  async mergeBatchSignedPsbt_SatsNet(
    psbts: string[],
    network: string
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return this._handleRequest('mergeBatchSignedPsbt_SatsNet', psbts, network)
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

export default new WalletManager()
