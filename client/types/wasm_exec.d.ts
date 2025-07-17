// types/wasm_exec.d.ts

declare class Go {
  importObject: WebAssembly.Imports
  run(instance: WebAssembly.Instance): void
  // 根据
}
interface SatsnetResponse<T = any> {
  code: number
  data: T
  msg: string
}

interface Config {
  Chain: string
  Mode: string
  Btcd: {
    Host: string
    User: string
    Password: string
    Zmqpubrawblock: string
    Zmqpubrawtx: string
  }
  IndexerL1: {
    Host: string
    Scheme?: string
  }
  IndexerL2: {
    Host: string
    Scheme?: string
  }
  Log: string
}

declare interface WalletManager {
  // Initialize the wallet manager with config and log level
  init(config: Config, logLevel: number): Promise<SatsnetResponse<void>>

  // Release and cleanup wallet manager resources
  release(): SatsnetResponse<void>

  // Check if wallet exists
  isWalletExist(): SatsnetResponse<{ exists: boolean }>

  // Get software version
  getVersion(): SatsnetResponse<{ version: string }>

  // Register callbacks for wallet events
  registerCallback(callback: Function): SatsnetResponse<void>

  // Get wallet address for account
  getWalletAddress(accountId: number): SatsnetResponse<{ address: string }>

  // Get wallet public key for account
  getWalletPubkey(accountId: number): SatsnetResponse<{ pubKey: string }>

  // Get payment public key
  getPaymentPubKey(): SatsnetResponse<string>

  // Get current chain
  getChain(): SatsnetResponse<string>

  // Get wallet mnemonic
  getMnemonicee(
    walletId: string,
    password: string
  ): SatsnetResponse<{ mnemonic: string }>

  // Send UTXOs and assets on SatsNet
  sendUtxos_SatsNet(
    destAddr: string,
    utxos: string[],
    fees: string[]
  ): Promise<SatsnetResponse<{ txId: string }>>
  sendAssets_SatsNet(
    destAddr: string,
    assetName: string,
    amount: number
  ): Promise<SatsnetResponse<{ txId: string }>>

  getChannelAddrByPeerPubkey(peerPubkey: string): Promise<SatsnetResponse<{ channelAddr: string, peerAddr }>>

  // Creates a new wallet with a password and returns the wallet ID and mnemonic.
  createWallet(
    password: string
  ): Promise<SatsnetResponse<{ walletId: string; mnemonic: string }>>

  // Imports an existing wallet using a mnemonic and password, and returns the wallet ID.
  importWallet(
    mnemonic: string,
    password: string
  ): Promise<SatsnetResponse<{ walletId: string }>>

  // Unlocks an existing wallet using a password and returns the wallet ID.
  unlockWallet(password: string): Promise<SatsnetResponse<{ walletId: string }>>

  // Returns a map of all wallet IDs to the number of accounts they contain.
  getAllWallets(): Promise<SatsnetResponse<Map<number, number>>>

  // Switches to the wallet with the specified ID.
  switchWallet(id: number, password: string): Promise<SatsnetResponse<void>>
  changePassword(oldPassword: string, newPassword: string): Promise<SatsnetResponse<void>>
  // Switches to the account with the specified ID.
  switchAccount(id: number): Promise<SatsnetResponse<void>>

  // Switches to the specified chain (e.g., "mainnet" or "testnet").
  switchChain(chain: string, password: string): Promise<SatsnetResponse<void>>

  // Returns the current chain.
  getChain(): Promise<SatsnetResponse<string>>

  // Returns the mnemonic for the wallet with the specified ID, using the provided password.
  getMnemonice(
    id: number,
    password: string
  ): Promise<SatsnetResponse<{ mnemonic: string }>>

  // Returns the commit root key for the specified peer.
  getCommitRootKey(peer: Uint8Array): Promise<SatsnetResponse<Uint8Array>>

  // Returns the commit secret for the specified peer and index.
  getCommitSecret(
    peer: Uint8Array,
    index: number
  ): Promise<SatsnetResponse<Uint8Array>>

  // Derives a revocation private key from the provided commit secret.
  deriveRevocationPrivKey(
    commitSecret: Uint8Array
  ): Promise<SatsnetResponse<Uint8Array>>

  // Returns the revocation base key.
  getRevocationBaseKey(): Promise<SatsnetResponse<Uint8Array>>

  // Returns the node public key.
  getNodePubKey(): Promise<SatsnetResponse<Uint8Array>>

  // Returns the public key for the specified account ID.
  getPublicKey(id: number): Promise<SatsnetResponse<Uint8Array>>

  // Returns the payment public key.
  getPaymentPubKey(): Promise<SatsnetResponse<Uint8Array>>

  // Signs a message and returns the signature.
  signMessage(msg: string): Promise<SatsnetResponse<{ signature: string }>>

  // Signs a PSBT (Partially Signed Bitcoin Transaction) and returns the signed PSBT in hex format.
  signPsbt(
    psbtHex: string,
    bool: boolean
  ): Promise<SatsnetResponse<{ psbt: string }>>

  // Signs a PSBT for the SatsNet network and returns the signed PSBT in hex format.
  signPsbt_SatsNet(
    psbtHex: string,
    bool: boolean
  ): Promise<SatsnetResponse<{ psbt: string }>>

  extractTxFromPsbt(psbtHex: string): Promise<SatsnetResponse<{ psbt: string }>>
  getTxAssetInfoFromPsbt_SatsNet(
    psbtHex: string,
    network: string
  ): Promise<SatsnetResponse<any>>
  extractTxFromPsbt_SatsNet(
    psbtHex: string
  ): Promise<SatsnetResponse<{ psbt: string }>>

  // Sends UTXOs to the specified address on the SatsNet network.
  sendUtxos_SatsNet(
    destAddr: string,
    utxos: string[],
    fees: string[]
  ): Promise<SatsnetResponse<string>>

  // Sends assets to the specified address on the SatsNet network.
  sendAssets_SatsNet(
    destAddr: string,
    assetName: string,
    amt: number
  ): Promise<SatsnetResponse<string>>

  // Build a batch sell order
  buildBatchSellOrder_SatsNet(
    utxos: string[],
    address: string,
    network: string
  ): Promise<SatsnetResponse<{ orderId: string }>>

  // Split a batch signed PSBT
  splitBatchSignedPsbt(
    signedHex: string,
    network: string
  ): Promise<SatsnetResponse<{ psbts: string[] }>>

  // Split a batch signed PSBT for SatsNet
  splitBatchSignedPsbt_SatsNet(
    signedHex: string,
    network: string
  ): Promise<SatsnetResponse<{ psbts: string[] }>>

  // 添加新的方法定义
  finalizeSellOrder_SatsNet(
    psbtHex: string,
    utxos: string[],
    buyerAddress: string,
    serverAddress: string,
    network: string,
    serviceFee: number,
    networkFee: number
  ): Promise<SatsnetResponse<{ psbt: string }>>
  mergeBatchSignedPsbt_SatsNet(
    psbts: string[],
    network: string
  ): Promise<SatsnetResponse<{ psbt: string }>>
  addInputsToPsbt(
    psbtHex: string,
    utxos: string[]
  ): Promise<SatsnetResponse<{ psbt: string }>>

  addOutputsToPsbt(
    psbtHex: string,
    utxos: string[]
  ): Promise<SatsnetResponse<{ psbt: string }>>
}
interface SatsnetStp {
  closeChannel(
    chanPoint: string,
    feeRate: number,
    force: boolean
  ): SatsnetResponse
  createWallet(password: string): SatsnetResponse
  hello(): SatsnetResponse
  start(): SatsnetResponse
  getVersion(): SatsnetResponse
  registerCallback(callback: any): SatsnetResponse
  isWalletExisting(): SatsnetResponse
  importWallet(mnemonic: string, passwork: string): SatsnetResponse
  init(cfg: any, logLevel: number): SatsnetResponse
  getTickerInfo(asset: string): SatsnetResponse
  runesAmtV2ToV3(asset, runesAmt: string | number): SatsnetResponse
  runesAmtV3ToV2(asset, runesAmt: string | number): SatsnetResponse
  lockToChannel(
    chanPoint: string,
    assetName: string,
    amt: number,
    utxos: string[],
    feeUtxoList?: any[]
  ): SatsnetResponse
  openChannel(
    feeRate: number,
    amt: number,
    utxoList: string[],
    memo: string
  ): SatsnetResponse
  splicingIn(
    chanPoint: string,
    assetName: string,
    utxos: string[],
    fees: string[],
    feeRate: number,
    amt: number
  ): SatsnetResponse
  splicingOut(
    chanPoint: string,
    toAddress: string,
    assetName: string,
    fees: string[],
    feeRate: number,
    amt: number
  ): SatsnetResponse
  release(): SatsnetResponse
  getWallet(): SatsnetResponse
  // Switches to the wallet with the specified ID.
  switchWallet(id: number, password: string): Promise<SatsnetResponse<void>>
  changePassword(oldPassword: string, newPassword: string): Promise<SatsnetResponse<void>>
  // Switches to the account with the specified ID.
  switchAccount(id: number): Promise<SatsnetResponse<void>>
  stakeToBeMiner(bCoreNode: boolean, btcFeeRate: string): Promise<SatsnetResponse<{
    txId: string,
    resvId: string
    assetName: string
    amt: string
  }>>
  // Switches to the specified chain (e.g., "mainnet" or "testnet").
  switchChain(chain: string, password: string): Promise<SatsnetResponse<void>>

  getAllChannels(): SatsnetResponse
  getCurrentChannel(): SatsnetResponse
  getChannel(id: string): SatsnetResponse
  getChannelStatus(id: string): SatsnetResponse
  sendUtxos_SatsNet(
    address: string,
    utxos: string[],
    amt: number
  ): SatsnetResponse
  sendAssets(address: string, assetName: string, amt: number, feeRate: string): SatsnetResponse
  sendAssets_SatsNet(
    address: string,
    assetName: string,
    amt: number
  ): SatsnetResponse
  unlockFromChannel(
    channelUtxo: string,
    assetName: string,
    amt: number,
    feeUtxoList?: any[]
  ): SatsnetResponse
  unlockWallet(password: string): SatsnetResponse
  getMnemonice(password: string): SatsnetResponse
  deposit(
    destAddr: string,
    assetName: string,
    amt: string,
    utxos: string[],
    fees: string[],
    feeRate: number
  ): SatsnetResponse<{ txId: string; value: number }>

  withdraw(
    destAddr: string,
    assetName: string,
    amt: string,
    utxos: string[],
    fees: string[],
    feeRate: number
  ): SatsnetResponse<{ txId: string; value: number }>

  getAllLockedUtxo(address: string): Promise<SatsnetResponse<any>>
  getAllLockedUtxo_SatsNet(address: string): Promise<SatsnetResponse<any>>
  lockUtxo(address: string, utxo: any, reason?: string): Promise<SatsnetResponse<any>>
  lockUtxo_SatsNet(address: string, utxo: any, reason?: string): Promise<SatsnetResponse<any>>
  unlockUtxo(address: string, utxo: any): Promise<SatsnetResponse<any>>
  unlockUtxo_SatsNet(address: string, utxo: any): Promise<SatsnetResponse<any>>

  // --- Added UTXO Getter Methods ---
  getUtxos(): Promise<SatsnetResponse<any>> // Replace 'any' with specific return type if known
  getUtxos_SatsNet(): Promise<SatsnetResponse<any>> // Replace 'any' with specific return type if known
  getUtxosWithAsset(address: string, amt: number, assetName: string,): Promise<SatsnetResponse<any>> // Add params, replace 'any'
  getUtxosWithAsset_SatsNet(address: string, amt: number, assetName: string): Promise<SatsnetResponse<any>> // Add params, replace 'any'
  getUtxosWithAssetV2(address: string, amt: number, assetName: string): Promise<SatsnetResponse<any>> // Add params, replace 'any'
  getUtxosWithAssetV2_SatsNet(address: string, amt: number, assetName: string): Promise<SatsnetResponse<any>> // Add params, replace 'any'
  getCommitTxAssetInfo(
    channelId: string
  ): Promise<SatsnetResponse<any>>
  // 新增方法定义
  getAssetAmount(
    address: string,
    assetName: string
  ): Promise<SatsnetResponse<{ amount: number; value: number }>>;

  getAssetAmount_SatsNet(
    address: string,
    assetName: string
  ): Promise<SatsnetResponse<{ amount: number; value: number }>>;

  batchSendAssets_SatsNet(destAddr: string,
    assetName: string, amt: string, n: number): Promise<SatsnetResponse<any>>;
  batchSendAssets(destAddr: string,
    assetName: string, amt: string, n: number, feeRate: number): Promise<SatsnetResponse<any>>;

  /** 获取支持的合约模板 */
  getSupportedContracts(): Promise<SatsnetResponse<{ contractContents: any[] }>>;

  /** 获取服务器已部署的合约 */
  getDeployedContractsInServer(): Promise<SatsnetResponse<{ contractURLs: any[] }>>;

  /** 获取已部署合约的状态 */
  getDeployedContractStatus(url: string): Promise<SatsnetResponse<{ contractStatus: any }>>;

  /** 查询部署合约所需费用 */
  getFeeForDeployContract(
    templateName: string,
    content: string, // json string
    feeRate: string
  ): Promise<SatsnetResponse<{ fee: any }>>;

  /** 远程部署合约 */
  deployContract_Remote(
    templateName: string,
    content: string, // json string
    feeRate: string,
    bol: boolean
  ): Promise<SatsnetResponse<{ txId: string; resvId: string }>>;

  /** 本地部署合约 */
  deployContract_Local(
    templateName: string,
    content: string, // json string
    feeRate: string
  ): Promise<SatsnetResponse<{ txId: string; resvId: string }>>;

  /** 查询合约调用参数 */
  getParamForInvokeContract(
    templateName: string,
    action: string
  ): Promise<SatsnetResponse<{ parameter: any }>>;

  /** 查询调用合约所需费用 */
  getFeeForInvokeContract(
    url: string,
    invoke: string // json string
  ): Promise<SatsnetResponse<{ fee: any }>>;

  /** 调用合约 */
  invokeContract_SatsNet(
    url: string,
    invoke: string, // json string
    feeRate: string
  ): Promise<SatsnetResponse<{ txId: string }>>;

  /** 根据地址获取合约状态 */
  getAddressStatusInContract(url: string, address: string): Promise<SatsnetResponse<string>>;
  /** 获取合约所有地址 */
  getContractAllAddresses(url: string): Promise<SatsnetResponse<string>>;
}
declare interface Window {
  sat20wallet_wasm: WalletManager
  stp_wasm: SatsnetStp
  sat20: Sat20
}
declare interface GlobalThis {
  sat20wallet_wasm: WalletManager
  stp_wasm: SatsnetStp
  sat20: Sat20
}

