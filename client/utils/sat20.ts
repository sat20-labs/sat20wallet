import { tryit } from 'radash'
class WalletManager {
  private async _handleRequest(
    methodName: string,
    ...args: any[]
  ): Promise<[Error | undefined, any | undefined]> {
    const method = (globalThis as any).sat20wallet_wasm[methodName as keyof WalletManager]
    const [err, result] = await tryit(method as any)(...args)
    console.log(`${methodName} args: `, args)
    console.log(`${methodName} result: `, result)
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
  async changePassword(
    oldPassword: string,
    newPassword: string
  ): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('changePassword', oldPassword, newPassword)
  }
  async unlockWallet(
    password: string
  ): Promise<[Error | undefined, { walletId: number } | undefined]> {
    return this._handleRequest('unlockWallet', password.toString())
  }

  async getChannelAddrByPeerPubkey(peerPubkey: string): Promise<[Error | undefined, { channelAddr: string, peerAddr: string } | undefined]> {
    return this._handleRequest('getChannelAddrByPeerPubkey', peerPubkey)
  }

  async getAllWallets(): Promise<
    [Error | undefined, Map<number, number> | undefined]
  > {
    return this._handleRequest('getAllWallets')
  }

  async switchWallet(
    id: string,
    password: string
  ): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('switchWallet', id, password)
  }

  async switchAccount(
    accountIndex: number
  ): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('switchAccount', accountIndex)
  }

  async switchChain(
    chain: string,
    password: string
  ): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('switchChain', chain, password)
  }

  async getChain(): Promise<[Error | undefined, string | undefined]> {
    return this._handleRequest('getChain')
  }

  async getMnemonice(
    id: number,
    password: string
  ): Promise<[Error | undefined, { mnemonic: string } | undefined]> {
    return this._handleRequest('getMnemonice', id.toString(), password)
  }

  async signMessage(
    msg: string
  ): Promise<[Error | undefined, { signature: string } | undefined]> {
    return this._handleRequest('signMessage', msg)
  }

  async signData(
    data: string
  ): Promise<[Error | undefined, { signature: string } | undefined]> {
    return this._handleRequest('signData', data)
  }

  async signPsbt(
    psbtHex: string,
    bool: boolean
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return this._handleRequest('signPsbt', psbtHex, bool)
  }



  async extractTxFromPsbt(
    psbtHex: string
  ): Promise<[Error | undefined, { tx: string } | undefined]> {
    return this._handleRequest('extractTxFromPsbt', psbtHex)
  }

  async signPsbt_SatsNet(
    psbtHex: string,
    bool: boolean
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return this._handleRequest('signPsbt_SatsNet', psbtHex, bool)
  }

  async extractTxFromPsbt_SatsNet(
    psbtHex: string
  ): Promise<[Error | undefined, { tx: string } | undefined]> {
    return this._handleRequest('extractTxFromPsbt_SatsNet', psbtHex)
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
    amt: number,
    memo: string = ""
  ): Promise<[Error | undefined, string | undefined]> {
    return this._handleRequest('sendAssets_SatsNet', destAddr, assetName, amt, memo)
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

  async splitBatchSignedPsbt_SatsNet(
    signedHex: string,
    network: string
  ): Promise<[Error | undefined, { psbts: string[] } | undefined]> {
    return this._handleRequest('splitBatchSignedPsbt_SatsNet', signedHex, network)
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

  // --- UTXO Management Methods ---
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
    return this._handleRequest('lockUtxo_SatsNet', address, utxo, reason)
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
    return this._handleRequest('unlockUtxo_SatsNet', address, utxo)
  }

  async getAllLockedUtxo(
    address: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getAllLockedUtxo', address)
  }

  async getAllLockedUtxo_SatsNet(
    address: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getAllLockedUtxo_SatsNet', address)
  }

  // --- UTXO Getter Methods ---
  async getUtxos(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getUtxos')
  }

  async getUtxos_SatsNet(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getUtxos_SatsNet')
  }

  async getUtxosWithAsset(
    address: string,
    amt: number,
    assetName: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getUtxosWithAsset', address, amt, assetName)
  }

  async getUtxosWithAsset_SatsNet(
    address: string,
    amt: number,
    assetName: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getUtxosWithAsset_SatsNet', address, amt, assetName)
  }

  async getUtxosWithAssetV2(
    address: string,
    amt: number,
    assetName: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getUtxosWithAssetV2', address, amt, assetName)
  }

  async getUtxosWithAssetV2_SatsNet(
    address: string,
    amt: number,
    assetName: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getUtxosWithAssetV2_SatsNet', address, amt, assetName)
  }

  // --- Asset Amount Methods ---
  async getAssetAmount(
    address: string,
    assetName: string
  ): Promise<[Error | undefined, { amount: number; value: number } | undefined]> {
    return this._handleRequest('getAssetAmount', address, assetName)
  }

  async getAssetAmount_SatsNet(
    address: string,
    assetName: string
  ): Promise<[Error | undefined, { amount: number; value: number } | undefined]> {
    return this._handleRequest('getAssetAmount_SatsNet', address, assetName)
  }

  // --- Contract Methods ---
  async getFeeForDeployContract(
    templateName: string,
    content: string,
    feeRate: string
  ): Promise<[Error | undefined, { fee: any } | undefined]> {
    return this._handleRequest('getFeeForDeployContract', templateName, content, feeRate)
  }

  async getFeeForInvokeContract(
    url: string,
    invoke: string
  ): Promise<[Error | undefined, { fee: any } | undefined]> {
    return this._handleRequest('getFeeForInvokeContract', url, invoke)
  }

  async getParamForInvokeContract(
    templateName: string,
    action: string
  ): Promise<[Error | undefined, { parameter: any } | undefined]> {
    return this._handleRequest('getParamForInvokeContract', templateName, action)
  }
}

export default new WalletManager()
