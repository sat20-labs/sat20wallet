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

  async validateBitcoinAddress(address: string): Promise<[Error | undefined, { valid: boolean } | undefined]> {
    return this._handleRequest('validateBitcoinAddress', address)
  }

  async validateSatsNetAddress(address: string): Promise<[Error | undefined, { valid: boolean } | undefined]> {
    return this._handleRequest('validateSatsNetAddress', address)
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

  async signPsbts(
    psbtHexs: string[],
    bool: boolean
  ): Promise<[Error | undefined, { psbts: string[] } | undefined]> {
    return this._handleRequest('signPsbts', psbtHexs, bool)
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

  async extractUnsignedTxFromPsbt(
    psbtHex: string
  ): Promise<[Error | undefined, { tx: string } | undefined]> {
    return this._handleRequest('extractUnsignedTxFromPsbt', psbtHex)
  }

  async extractUnsignedTxFromPsbt_SatsNet(
    psbtHex: string
  ): Promise<[Error | undefined, { tx: string } | undefined]> {
    return this._handleRequest('extractUnsignedTxFromPsbt_SatsNet', psbtHex)
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
    amt: string | number,
    memo: string = ""
  ): Promise<[Error | undefined, string | undefined]> {
    return this._handleRequest('sendAssets_SatsNet', destAddr, assetName, String(amt), String(memo))
  }

  async sendAssets(
    address: string,
    assetName: string,
    amt: string | number,
    feeRate: string | number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('sendAssets', address, assetName, String(amt), String(feeRate))
  }

  async batchSendAssetsV2_SatsNet(
    destAddr: string[],
    assetName: string,
    amtList: string[]
  ): Promise<[Error | undefined, { txId: string } | undefined]> {
    return this._handleRequest('batchSendAssetsV2_SatsNet', destAddr, assetName, amtList)
  }

  async init(
    config: any,
    logLevel: number
  ): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('init', config, logLevel)
  }

  async start(): Promise<[Error | undefined, void | undefined]> {
    return this._handleRequest('start')
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

  async getRGB11State(): Promise<
    [Error | undefined, { state: string } | undefined]
  > {
    return this._handleRequest('getRGB11State')
  }

  async createRGB11Invoice(request: {
    mode?: 'blind' | 'witness'
    contract_id: string
    schema_id?: string
    amount_raw: number | string
    assignment_name?: string
    expiry?: number
    witness_vout?: number
  }): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('createRGB11Invoice', JSON.stringify(request))
  }

  async importRGB11Contract(consignment: string): Promise<
    [Error | undefined, { result: string } | undefined]
  > {
    return this._handleRequest('importRGB11Contract', consignment)
  }

  async issueRGB11Asset(request: {
    schema: 'NIA' | 'IFA' | 'UDA'
    ticker?: string
    name: string
    details?: string
    precision: number
    terms?: string
    amounts: string[]
    inflation_amounts?: string[]
    reject_list_url?: string
    min_confirmations?: number
  }): Promise<[Error | undefined, { result: string } | undefined]> {
    return this._handleRequest('issueRGB11Asset', JSON.stringify(request))
  }

  async prepareRGB11Transfer(request: {
    invoice?: string
    invoices?: string[]
    fee_rate?: number
    min_confirmations?: number
  }): Promise<[Error | undefined, { transfer: string } | undefined]> {
    return this._handleRequest('prepareRGB11Transfer', JSON.stringify(request))
  }

  async buildRGB11RelayRecord(transferId: string): Promise<
    [Error | undefined, { record: string } | undefined]
  > {
    return this._handleRequest('buildRGB11RelayRecord', transferId)
  }

  async publishRGB11RelayRecord(transferId: string): Promise<
    [Error | undefined, { record: string } | undefined]
  > {
    return this._handleRequest('publishRGB11RelayRecord', transferId)
  }

  async acceptRGB11Consignment(requestId: string, consignment: string): Promise<
    [Error | undefined, any | undefined]
  > {
    return this._handleRequest('acceptRGB11Consignment', requestId, consignment)
  }

  async acceptRGB11RelayConsignment(requestId: string, relayRecord: string, consignment: string): Promise<
	[Error | undefined, { receipt: string; ack: string } | undefined]
  > {
	return this._handleRequest('acceptRGB11RelayConsignment', requestId, relayRecord, consignment)
  }

  async rejectRGB11RelayConsignment(requestId: string, relayRecord: string): Promise<
	[Error | undefined, { ack: string } | undefined]
  > {
	return this._handleRequest('rejectRGB11RelayConsignment', requestId, relayRecord)
  }

  async publishRGB11AckRecord(key: string, ack: string): Promise<
    [Error | undefined, { published: boolean } | undefined]
  > {
    return this._handleRequest('publishRGB11AckRecord', key, ack)
  }

  async fetchRGB11AckRecord(transferId: string): Promise<
    [Error | undefined, { ack: string } | undefined]
  > {
    return this._handleRequest('fetchRGB11AckRecord', transferId)
  }

  async cancelRGB11BatchByNack(transferId: string, relayRecord: string, nack: string): Promise<
	[Error | undefined, { cancelled: boolean } | undefined]
  > {
	return this._handleRequest('cancelRGB11BatchByNack', transferId, relayRecord, nack)
  }

  async broadcastRGB11Transfer(transferId: string, relayRecord: string, ack: string): Promise<
    [Error | undefined, { txid: string } | undefined]
  > {
    return this._handleRequest('broadcastRGB11Transfer', transferId, relayRecord, ack)
  }

  async broadcastRGB11Batch(request: {
    transfer_ids: string[]
    relay_records: unknown[]
    acks: unknown[]
  }): Promise<[Error | undefined, { txid: string } | undefined]> {
    return this._handleRequest('broadcastRGB11Batch', JSON.stringify(request))
  }

  async broadcastRGB11OutOfBand(transferIds: string[]): Promise<
    [Error | undefined, { txid: string } | undefined]
  > {
    return this._handleRequest('broadcastRGB11OutOfBand', JSON.stringify(transferIds))
  }

  async refreshRGB11State(): Promise<
    [Error | undefined, { result: string } | undefined]
  > {
    return this._handleRequest('refreshRGB11State')
  }

  async backupRGB11WalletState(request: {
    wallet_id?: string
    ttl?: number
    expiry_height?: number
  } = {}): Promise<[Error | undefined, { head: string } | undefined]> {
    return this._handleRequest('backupRGB11WalletState', JSON.stringify(request))
  }

  async restoreRGB11WalletState(request: {
    wallet_id?: string
    height?: number
    now?: number
  } = {}): Promise<[Error | undefined, { head: string } | undefined]> {
    return this._handleRequest('restoreRGB11WalletState', JSON.stringify(request))
  }

  async startBTCLuckyMining(config: {
    jobs: string
    lowPriority: boolean
    lowPrioritySleep: string
  }): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('startBTCLuckyMining', config)
  }

  async stopBTCLuckyMining(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('stopBTCLuckyMining')
  }

  async getBTCLuckyMiningStatus(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getBTCLuckyMiningStatus')
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
    amt: string | number,
    assetName: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getUtxosWithAsset', address, String(amt), assetName)
  }

  async getUtxosWithAsset_SatsNet(
    address: string,
    amt: string | number,
    assetName: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getUtxosWithAsset_SatsNet', address, String(amt), assetName)
  }

  async getUtxosWithAssetV2(
    address: string,
    amt: string | number,
    assetName: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getUtxosWithAssetV2', address, String(amt), assetName)
  }

  async getUtxosWithAssetV2_SatsNet(
    address: string,
    amt: string | number,
    assetName: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getUtxosWithAssetV2_SatsNet', address, String(amt), assetName)
  }

  // --- Asset Amount Methods ---
  async getAssetAmount(address: string, assetName: string): Promise<[Error | undefined, {
    availableAmt: number,
    lockedAmt: number
  } | undefined]> {
    return this._handleRequest('getAssetAmount', address, assetName);
  }

  async getAssetAmount_SatsNet(address: string, assetName: string): Promise<[Error | undefined, {
    availableAmt: number,
    lockedAmt: number
  } | undefined]> {
    return this._handleRequest('getAssetAmount_SatsNet', address, assetName);
  }

  // --- Contract Methods ---
  async getSupportedContracts(): Promise<[Error | undefined, { contractContents: any[] } | undefined]> {
    return this._handleRequest('getSupportedContracts')
  }

  async getDeployedContractsInServer(): Promise<[Error | undefined, { contractURLs: any[] } | undefined]> {
    return this._handleRequest('getDeployedContractsInServer')
  }

  async getDeployedContractStatus(url: string): Promise<[Error | undefined, { contractStatus: any } | undefined]> {
    return this._handleRequest('getDeployedContractStatus', url)
  }

  async getFeeForDeployContract(
    templateName: string,
    content: string,
    feeRate: string
  ): Promise<[Error | undefined, { fee: any } | undefined]> {
    return this._handleRequest('getFeeForDeployContract', templateName, content, String(feeRate))
  }

  async deployContract_Remote(
    templateName: string,
    content: string,
    feeRate: string,
    bol: boolean
  ): Promise<[Error | undefined, { txId: string; resvId: string } | undefined]> {
    return this._handleRequest('deployContract_Remote', templateName, content, String(feeRate), bol)
  }

  async queryContract(req: Record<string, unknown>): Promise<[Error | undefined, { result: string } | undefined]> {
    return this._handleRequest('queryContract', JSON.stringify(req))
  }

  async deployUnifiedContract(req: Record<string, unknown>): Promise<
    [Error | undefined, {
      contractType: string,
      txid: string,
      contractAddress?: string,
      caller?: string,
      gasAssetAmount?: string,
      gasFeeAmount?: string,
      gasFundAmount?: string,
      gasLimit?: string,
      nonce?: string,
    } | undefined]
  > {
    return this._handleRequest('deployUnifiedContract', JSON.stringify(req))
  }

  async estimateDeployUnifiedContract(req: Record<string, unknown>): Promise<
    [Error | undefined, {
      contractType: string,
      contractAddress?: string,
      caller?: string,
      gasAssetAmount?: string,
      gasFeeAmount?: string,
      gasFundAmount?: string,
      gasLimit?: string,
      nonce?: string,
    } | undefined]
  > {
    return this._handleRequest('estimateDeployUnifiedContract', JSON.stringify(req))
  }

  async buildUnifiedContractContent(
    contractType: string,
    subtype: string,
    jsonContent: string
  ): Promise<[Error | undefined, { content: string; contentEncoding: string } | undefined]> {
    return this._handleRequest('buildUnifiedContractContent', contractType, subtype, jsonContent)
  }

  async invokeUnifiedContract(req: Record<string, unknown>): Promise<
    [Error | undefined, {
      contractType: string,
      txid: string,
      contractAddress?: string,
      caller?: string,
      gasAssetAmount?: string,
      gasFeeAmount?: string,
      gasFundAmount?: string,
      gasLimit?: string,
      nonce?: string,
    } | undefined]
  > {
    return this._handleRequest('invokeUnifiedContract', JSON.stringify(req))
  }

  async getParamForInvokeUnifiedContract(
    contractType: string,
    subtype: string,
    action: string
  ): Promise<[Error | undefined, { parameter: any } | undefined]> {
    return this._handleRequest('getParamForInvokeUnifiedContract', contractType, subtype, action)
  }

  async getFeeForInvokeUnifiedContract(
    req: Record<string, unknown>
  ): Promise<[Error | undefined, { fee: any } | undefined]> {
    return this._handleRequest('getFeeForInvokeUnifiedContract', JSON.stringify(req))
  }

  async getParamForInvokeContract(
    templateName: string,
    action: string
  ): Promise<[Error | undefined, { parameter: any } | undefined]> {
    return this._handleRequest('getParamForInvokeContract', templateName, action)
  }

  async getFeeForInvokeContract(
    url: string,
    invoke: string
  ): Promise<[Error | undefined, { fee: any } | undefined]> {
    return this._handleRequest('getFeeForInvokeContract', url, invoke)
  }

  async invokeContract_SatsNet(
    url: string,
    invoke: string,
    feeRate: string
  ): Promise<[Error | undefined, { txId: string } | undefined]> {
    return this._handleRequest('invokeContract_SatsNet', url, invoke, String(feeRate))
  }

  async invokeContractV2_SatsNet(
    url: string,
    invoke: string,
    assetName: string,
    amt: string | number,
    feeRate: string | number
  ): Promise<[Error | undefined, { txId: string } | undefined]> {
    return this._handleRequest('invokeContractV2_SatsNet', url, invoke, assetName, String(amt), String(feeRate))
  }

  async invokeContractV2(
    url: string,
    invoke: string,
    assetName: string,
    amt: string | number,
    feeRate: string | number
  ): Promise<[Error | undefined, { txId: string } | undefined]> {
    return this._handleRequest('invokeContractV2', url, invoke, assetName, String(amt), String(feeRate))
  }

  async getContractInvokeHistoryInServer(url: string, start: number, limit: number): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getContractInvokeHistoryInServer', url, start, limit)
  }

  async getContractInvokeHistoryByAddressInServer(url: string, address: string, start: number, limit: number): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getContractInvokeHistoryByAddressInServer', url, address, start, limit)
  }

  async getAllAddressInContract(url: string, start: number, limit: number): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getAllAddressInContract', url, start, limit)
  }

  async getAddressStatusInContract(url: string, address: string): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getAddressStatusInContract', url, address)
  }

  // --- Referrer Methods ---
  async getAllRegisteredReferrerName(
    pubkey: string
  ): Promise<[Error | undefined, { names: string[] } | undefined]> {
    return this._handleRequest('getAllRegisteredReferrerName', pubkey)
  }

  async registerAsReferrer(
    name: string,
    feeRate: string | number
  ): Promise<[Error | undefined, { txId: string } | undefined]> {
    return this._handleRequest('registerAsReferrer', name, String(feeRate))
  }

  async bindReferrerForServer(
    referrerName: string,
    serverPubKey: string
  ): Promise<[Error | undefined, { txId: string } | undefined]> {
    return this._handleRequest('bindReferrerForServer', referrerName, serverPubKey)
  }

  async deposit(
    destAddr: string,
    assetName: string,
    amt: string | number,
    utxos: string[],
    fees: string[],
    feeRate: string | number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'deposit',
      destAddr,
      assetName,
      String(amt),
      utxos,
      fees,
      String(feeRate)
    )
  }

  async withdraw(
    destAddr: string,
    assetName: string,
    amt: string | number,
    utxos: string[],
    fees: string[],
    feeRate: string | number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'withdraw',
      destAddr,
      assetName,
      String(amt),
      utxos,
      fees,
      String(feeRate)
    )
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

  async getTickerInfo(
    asset: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('getTickerInfo', asset)
  }

  async batchSendAssets_SatsNet(
    destAddr: string,
    assetName: string,
    amt: string | number,
    n: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('batchSendAssets_SatsNet', destAddr, assetName, String(amt), n)
  }

  async batchSendAssets(
    destAddr: string,
    assetName: string,
    amt: string | number,
    n: number,
    feeRate: string | number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('batchSendAssets', destAddr, assetName, String(amt), n, String(feeRate))
  }

  async stakeToBeMiner(bCoreNode: boolean, btcFeeRate: string | number): Promise<
    [Error | undefined, {
      txId: string,
      resvId: string,
      assetName: string,
      amt: string
    } | undefined]
  > {
    return this._handleRequest('stakeToBeMiner', bCoreNode, String(btcFeeRate))
  }

  async minerUnstake(btcFeeRate: string | number): Promise<
    [Error | undefined, {
      txId: string,
      resvId: string,
    } | undefined]
  > {
    return this._handleRequest('minerUnstake', String(btcFeeRate))
  }

  async DeployRunes_Remote(
    assetName: string,
    symbol: number,
    maxSupply: string,
    limit: string,
    selfMint: boolean,
    destAddr: string,
    divisibility: string | number,
    feeRate: string
  ): Promise<
    [Error | undefined, {
      txId: string,
      resvId: string,
      result: string,
    } | undefined]
  > {
    return this._handleRequest(
      'DeployRunes_Remote',
      assetName,
      symbol,
      String(maxSupply),
      String(limit),
      selfMint,
      destAddr,
      String(divisibility),
      String(feeRate)
    )
  }

  async deployTickerOrdx(
    ticker: string,
    max: string,
    limit: string,
    bindingSat: number,
    feeRate: string
  ): Promise<[Error | undefined, { txId: string; commitTxId: string; revealTxId: string; resvId: string } | undefined]> {
    return this._handleRequest('deployTickerOrdx', ticker, String(max), String(limit), bindingSat, String(feeRate))
  }

  async mintAssetOrdx(
    ticker: string,
    amount: string,
    feeRate: string
  ): Promise<[Error | undefined, { txId: string; commitTxId: string; revealTxId: string; resvId: string } | undefined]> {
    return this._handleRequest('mintAssetOrdx', ticker, String(amount), String(feeRate))
  }

  async mintAssetRunes(
    ticker: string,
    feeRate: string
  ): Promise<[Error | undefined, { txId: string } | undefined]> {
    return this._handleRequest('mintAssetRunes', ticker, String(feeRate))
  }

  async deployTickerBrc20(
    ticker: string,
    max: string,
    limit: string,
    decimal: string | number,
    feeRate: string
  ): Promise<[Error | undefined, { txId: string; commitTxId: string; revealTxId: string; resvId: string } | undefined]> {
    return this._handleRequest('deployTickerBrc20', ticker, String(max), String(limit), String(decimal), String(feeRate))
  }

  async mintAssetBrc20(
    ticker: string,
    amount: string,
    feeRate: string
  ): Promise<[Error | undefined, { txId: string; commitTxId: string; revealTxId: string; resvId: string } | undefined]> {
    return this._handleRequest('mintAssetBrc20', ticker, String(amount), String(feeRate))
  }

  async inscribeName(
    name: string,
    feeRate: string
  ): Promise<[Error | undefined, { txId: string; commitTxId: string; revealTxId: string; resvId: string } | undefined]> {
    return this._handleRequest('inscribeName', name, String(feeRate))
  }
}

export default new WalletManager()
