import { tryit } from 'radash'
import { walletRequestSessionGuard } from '@/lib/walletSession'
import { beginPwaWalletOperation, finishPwaOperation } from '@/utils/pwaOperationLog'

// Define the expected response structure from WASM functions
interface WasmResponse<T = any> {
  code: number;
  msg?: string;
  data?: T;
}

export interface CommitTxAssetUtxo {
  UtxoId: number;
  Outpoint: string;
  Value: number;
  PkScript: string;
  Assets: Array<{
    Name: { Ticker: string; [key: string]: unknown };
    Amount: string;
    [key: string]: unknown;
  }>;
}

export interface CommitTxAssetInfo {
  txId: string;
  txHex: string;
  inputs: string;
  outputs: string;
}

export interface ChannelOpenFeeInfo {
  openFee: {
    manageFee: number
    mortgageFee: number
    minReserveSats: number
    commitmentFee: number
    commitmentFeeRate: number
    splicingInFee: number
    splicingOutFee: number
  }
  openFeeTotal: number
  feeToDao: number
  minCapacity: number
  minAvailableValue: number
  amount?: number
  channelCapacity?: number
  estimatedNetworkFee?: number
  requiredInputSats?: number
  selectedInputSats?: number
  changeSats?: number
  valid: boolean
  validationError?: string
}

// Channel/STP methods are now exported by sat20wallet.wasm.
interface StpWasmModule {
  // 基础方法
  init: (...args: any[]) => Promise<WasmResponse>;
  getVersion: (...args: any[]) => Promise<WasmResponse<string>>;
  registerCallback: (...args: any[]) => Promise<WasmResponse>;

  // 钱包状态方法
  switchWallet: (...args: any[]) => Promise<WasmResponse>;
  switchAccount: (...args: any[]) => Promise<WasmResponse>;
  importWallet: (...args: any[]) => Promise<WasmResponse>;
  importWalletWithPrivKey: (...args: any[]) => Promise<WasmResponse>;
  unlockWallet: (...args: any[]) => Promise<WasmResponse>;

  // 通道管理
  closeChannel: (...args: any[]) => Promise<WasmResponse>;
  isWalletExisting: (...args: any[]) => Promise<WasmResponse<boolean>>;
  hello: (...args: any[]) => Promise<WasmResponse<string>>;
  previewOpenChannel: (...args: any[]) => Promise<WasmResponse<ChannelOpenFeeInfo>>;
  openChannel: (...args: any[]) => Promise<WasmResponse>;
  release: (...args: any[]) => Promise<WasmResponse>;
  getWallet: (...args: any[]) => Promise<WasmResponse>;
  runesAmtV2ToV3: (...args: any[]) => Promise<WasmResponse<string>>;
  runesAmtV3ToV2: (...args: any[]) => Promise<WasmResponse<string>>;
  getAllChannels: (...args: any[]) => Promise<WasmResponse<any[]>>;
  getCurrentChannel: (...args: any[]) => Promise<WasmResponse>;
  getChannel: (...args: any[]) => Promise<WasmResponse>;
  getChannelStatus: (...args: any[]) => Promise<WasmResponse>;
  reservationStatus: (...args: any[]) => Promise<WasmResponse>;
  allReservations: (...args: any[]) => Promise<WasmResponse>;
  resumeLockWithExpandFromL1Tx: (...args: any[]) => Promise<WasmResponse>;
  safetySnapshot: (...args: any[]) => Promise<WasmResponse>;
  commitmentExport: (...args: any[]) => Promise<WasmResponse>;
  punishStatus: (...args: any[]) => Promise<WasmResponse>;
  punishBuild: (...args: any[]) => Promise<WasmResponse>;
  punishBroadcast: (...args: any[]) => Promise<WasmResponse>;
  forceClosePlan: (...args: any[]) => Promise<WasmResponse>;
  sweepBuild: (...args: any[]) => Promise<WasmResponse>;
  splicingIn: (...args: any[]) => Promise<WasmResponse>;
  splicingOut: (...args: any[]) => Promise<WasmResponse>;
  lockToChannel: (...args: any[]) => Promise<WasmResponse>;
  lockToChannelWithExpand: (...args: any[]) => Promise<WasmResponse>;
  unlockFromChannel: (...args: any[]) => Promise<WasmResponse>;
  getCommitTxAssetInfo: (...args: any[]) => Promise<WasmResponse<CommitTxAssetInfo>>;
  deployContract_Local: (templateName: string, content: string, feeRate: string | number) => Promise<WasmResponse<{ txId: string; resvId: string }>>;
  deployContract_Remote: (templateName: string, content: string, feeRate: string | number, bol: boolean) => Promise<WasmResponse<{ txId: string; resvId: string }>>;
  stakeToBeMiner: (bCoreNode: boolean, btcFeeRate: string | number) => Promise<WasmResponse<{ txId: string; resvId: string; assetName: string; amt: string }>>;
  minerUnstake: (btcFeeRate: string | number) => Promise<WasmResponse<{ txId: string }>>;
  splitBatchSignedPsbt: (signedHex: string, network: string) => Promise<WasmResponse<{ psbts: string[] }>>;
  addInputsToPsbt: (psbtHex: string, utxos: string[]) => Promise<WasmResponse<{ psbt: string }>>;
  addOutputsToPsbt: (psbtHex: string, utxos: string[]) => Promise<WasmResponse<{ psbt: string }>>;
}

export const parseLockExpandRequiredAmount = (message: string): string | undefined => {
  if (message.trim().toLowerCase() === 'not allow lock, no assets') {
    return '0'
  }
  const match = message.match(/^not allow lock, only\s+([0-9][0-9,]*(?:\.[0-9]+)?)\s+assets can be used$/i)
  return match?.[1]
}

class SatsnetStp {
  private async _handleRequest<T>(
    methodName: keyof StpWasmModule,
    ...args: any[]
  ): Promise<[Error | undefined, T | undefined]> {
    return tryit(async () => {
      const checkSession = walletRequestSessionGuard(methodName)
      const module = (globalThis as any).sat20wallet_wasm as StpWasmModule | undefined
      if (!module || typeof module[methodName] !== 'function') {
        throw new Error(`sat20wallet_wasm or method "${methodName}" not found on globalThis.`)
      }
      const operation = await beginPwaWalletOperation(methodName, args)
      try {
        checkSession()
        const method = module[methodName] as (...args: any[]) => Promise<WasmResponse<T>>
        const response = await method(...args)
        checkSession()
        if (response && typeof response.code === 'number' && response.code !== 0) {
          throw new Error(response.msg || `sat20wallet channel ${methodName} failed with code ${response.code}`)
        }
        await finishPwaOperation(operation, null, response?.data)
        checkSession()
        return response?.data
      } catch (error) {
        console.error(`sat20wallet channel ${methodName} failed`)
        const failure = error instanceof Error ? error : new Error(String(error))
        await finishPwaOperation(operation, failure)
        throw failure
      }
    })()
  }

  // --- 通道管理相关方法 ---

  async init(
    cfg: any,
    logLevel: number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('init', cfg, logLevel)
  }

  async getVersion(): Promise<[Error | undefined, string | undefined]> {
    return this._handleRequest<string>('getVersion')
  }

  async registerCallback(
    cb: any
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('registerCallback', cb)
  }

  // --- 钱包状态方法，由 sat20wallet.wasm 统一导出 ---

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
    feeRate: string | number,
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

  async previewOpenChannel(
    feeRate: string | number,
    amt: string | number
  ): Promise<[Error | undefined, ChannelOpenFeeInfo | undefined]> {
    return this._handleRequest<ChannelOpenFeeInfo>('previewOpenChannel', String(feeRate), String(amt))
  }

  async openChannel(
    feeRate: string | number,
    amt: string | number,
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
      String(assetAmt)
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

  async reservationStatus(
    id: string | number
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('reservationStatus', String(id))
  }

  async allReservations(): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('allReservations')
  }

  async resumeLockWithExpandFromL1Tx(
    id: string | number,
    l1TxId: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('resumeLockWithExpandFromL1Tx', String(id), l1TxId)
  }

  async safetySnapshot(
    channelId: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('safetySnapshot', channelId)
  }

  async commitmentExport(
    channelId: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('commitmentExport', channelId)
  }

  async punishStatus(
    channelId: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('punishStatus', channelId)
  }

  async punishBuild(
    channelId: string,
    commitTxId: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('punishBuild', channelId, commitTxId)
  }

  async punishBroadcast(
    channelId: string,
    commitTxId: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('punishBroadcast', channelId, commitTxId)
  }

  async forceClosePlan(
    channelId: string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('forceClosePlan', channelId)
  }

  async sweepBuild(
    channelId: string,
    commitTxId?: string,
    height?: string | number,
    broadcast?: boolean
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest('sweepBuild', channelId, commitTxId || '', height || '', Boolean(broadcast))
  }


  async splicingIn(
    chanPoint: string,
    assetName: string,
    utxos: string[],
    fees: string[],
    feeRate: string | number,
    amt: string | number
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
    feeRate: string | number,
    amt: string | number
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
    amt: string | number,
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

  async lockToChannelWithExpand(
    chanPoint: string,
    assetName: string,
    amt: number | string,
    feeRate: number | string
  ): Promise<[Error | undefined, any | undefined]> {
    return this._handleRequest(
      'lockToChannelWithExpand',
      chanPoint,
      assetName,
      String(amt),
      String(feeRate)
    )
  }

  async unlockFromChannel(
    channelUtxo: string,
    assetName: string,
    amt: string | number,
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
	): Promise<[Error | undefined, CommitTxAssetInfo | undefined]> {
    return this._handleRequest('getCommitTxAssetInfo', channelId)
  }

  /** 本地部署合约 */
  async deployContract_Local(
    templateName: string,
    content: string,
    feeRate: string | number
  ): Promise<[
    Error | undefined,
    { txId: string; resvId: string } | undefined
  ]> {
    return this._handleRequest<{ txId: string; resvId: string }>('deployContract_Local', templateName, content, String(feeRate))
  }

  /** 远程部署合约 */
  async deployContract_Remote(
    templateName: string,
    content: string,
    feeRate: string | number,
    bol: boolean
  ): Promise<[
    Error | undefined,
    { txId: string; resvId: string } | undefined
  ]> {
    return this._handleRequest<{ txId: string; resvId: string }>('deployContract_Remote', templateName, content, String(feeRate), bol)
  }

  /** 质押成为矿工/核心节点 */
  async stakeToBeMiner(
    bCoreNode: boolean,
    btcFeeRate: string | number
  ): Promise<[Error | undefined, { txId: string; resvId: string; assetName: string; amt: string } | undefined]> {
    return this._handleRequest<{ txId: string; resvId: string; assetName: string; amt: string }>('stakeToBeMiner', bCoreNode, String(btcFeeRate))
  }

  /** 取消质押 */
  async minerUnstake(
    btcFeeRate: string | number
  ): Promise<[Error | undefined, { txId: string } | undefined]> {
    return this._handleRequest<{ txId: string }>('minerUnstake', String(btcFeeRate))
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
