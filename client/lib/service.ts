import { walletStorage } from '@/lib/walletStorage'
import { Network, Balance } from '@/types'
import { ordxApi } from '@/apis'
import { psbt2tx } from '@/utils/btc'
import stp from '@/utils/stp'
import sat20Wallet from '@/utils/sat20'
import { reInitializeWasm } from './background/WasmManager'

class Service {
  async getHasWallet(): Promise<boolean> {
    console.log('walletStorage:', walletStorage)
    const hasWallet = walletStorage.getValue('hasWallet')
    console.log('walletStorage.hasWallet:', hasWallet)
    return hasWallet
  }

  async getAccounts(): Promise<string[]> {
    const address = walletStorage.getValue('address')
    return address ? [address] : []
  }

  async getNetwork(): Promise<Network> {
    return walletStorage.getValue('network')
  }

  async getPublicKey(): Promise<string> {
    const pubkey = walletStorage.getValue('pubkey')
    if (!pubkey) {
      throw new Error('Public key not available')
    }
    return pubkey
  }

  async getBalance(): Promise<Balance> {
    return walletStorage.getValue('balance')
  }

  async pushTx(rawtx: string): Promise<[Error | undefined, string | undefined]> {
    await reInitializeWasm()
    const network = walletStorage.getValue('network')
    const res = await ordxApi.pushTx({ hex: rawtx, network })
    if (res.code === 0) {
      return [undefined, res.data]
    } else {
      return [new Error(res.msg), undefined]
    }
  }

  async pushPsbt(psbtHex: string): Promise<[Error | undefined, string | undefined]> {
    await reInitializeWasm()
    console.log('pushPsbt', psbtHex)
    const [extractErr, extractRes] = await sat20Wallet.extractTxFromPsbt(psbtHex)
    console.log('extractErr', extractErr)
    console.log('extractRes', extractRes)

    if (extractErr || !extractRes) {
      return [extractErr || new Error('提取交易失败'), undefined]
    }
    const txHex = extractRes.tx
    const network = walletStorage.getValue('network')
    const res = await ordxApi.pushTx({ hex: txHex, network })
    console.log('res', res)
    if (res.code === 0) {
      return [undefined, res.data]
    } else {
      return [new Error(res.msg), undefined]
    }
  }

  async extractTxFromPsbt(
    psbtHex: string,
    { chain }: { chain: string },
  ): Promise<[Error | undefined, { tx: string } | undefined]> {
    await reInitializeWasm()
    let extractErr, extractRes
    if (chain === 'btc') {
      [extractErr, extractRes] = await sat20Wallet.extractTxFromPsbt(psbtHex)
    } else {
      [extractErr, extractRes] = await sat20Wallet.extractTxFromPsbt_SatsNet(psbtHex)
    }
    if (extractErr || !extractRes) {
      return [extractErr || new Error('提取交易失败'), undefined]
    }
    return [undefined, { tx: extractRes.tx }]
  }

  async buildBatchSellOrder_SatsNet(
    utxos: string[],
    address: string,
    network: string,
  ): Promise<[Error | undefined, { orderId: string } | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.buildBatchSellOrder_SatsNet(utxos, address, network)
  }

  async splitBatchSignedPsbt(signedHex: string, network: string): Promise<[Error | undefined, { psbts: string[] } | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.splitBatchSignedPsbt(signedHex, network)
  }
  async splitBatchSignedPsbt_SatsNet(signedHex: string, network: string): Promise<[Error | undefined, { psbts: string[] } | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.splitBatchSignedPsbt_SatsNet(signedHex, network)
  }

  async mergeBatchSignedPsbt_SatsNet(psbts: string[], network: string): Promise<[Error | undefined, { psbt: string } | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.mergeBatchSignedPsbt_SatsNet(psbts, network)
  }

  async finalizeSellOrder_SatsNet(
    psbtHex: string,
    utxos: string[],
    buyerAddress: string,
    serverAddress: string,
    network: string,
    serviceFee: number,
    networkFee: number,
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.finalizeSellOrder_SatsNet(
      psbtHex,
      utxos,
      buyerAddress,
      serverAddress,
      network,
      serviceFee,
      networkFee,
    )
  }

  async addInputsToPsbt(
    psbtHex: string,
    utxos: string[],
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.addInputsToPsbt(psbtHex, utxos)
  }

  async addOutputsToPsbt(
    psbtHex: string,
    utxos: string[],
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.addOutputsToPsbt(psbtHex, utxos)
  }

  async lockUtxo(address: string, utxo: any, reason?: string): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.lockUtxo(address, utxo, reason)
  }

  async lockUtxo_SatsNet(address: string, utxo: any, reason?: string): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.lockUtxo_SatsNet(address, utxo, reason)
  }

  async unlockUtxo(address: string, utxo: any): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.unlockUtxo(address, utxo)
  }

  async unlockUtxo_SatsNet(address: string, utxo: any): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.unlockUtxo_SatsNet(address, utxo)
  }

  async getAllLockedUtxo(address: string): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getAllLockedUtxo(address)
  }

  async getAllLockedUtxo_SatsNet(address: string): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getAllLockedUtxo_SatsNet(address)
  }

  async getUtxos(): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getUtxos()
  }

  async getUtxos_SatsNet(): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getUtxos_SatsNet()
  }

  async getUtxosWithAsset(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getUtxosWithAsset(address, amt, assetName)
  }

  async getUtxosWithAsset_SatsNet(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getUtxosWithAsset_SatsNet(address, amt, assetName)
  }

  async getUtxosWithAssetV2(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getUtxosWithAssetV2(address, amt, assetName)
  }

  async getUtxosWithAssetV2_SatsNet(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getUtxosWithAssetV2_SatsNet(address, amt, assetName)
  }

  async getAssetAmount(address: string, assetName: string): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getAssetAmount(address, assetName)
  }

  async getAssetAmount_SatsNet(address: string, assetName: string): Promise<[Error | undefined, any | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getAssetAmount_SatsNet(address, assetName)
  }

  async getFeeForDeployContract(templateName: string, content: string, feeRate: string): Promise<[Error | undefined, { fee: any } | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getFeeForDeployContract(templateName, content, feeRate)
  }
  async getFeeForInvokeContract(url: string, invoke: string): Promise<[Error | undefined, { fee: any } | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getFeeForInvokeContract(url, invoke)
  }
  async getParamForInvokeContract(templateName: string, action: string): Promise<[Error | undefined, { parameter: any } | undefined]> {
    await reInitializeWasm()
    return sat20Wallet.getParamForInvokeContract(templateName, action)
  }

  // 获取名字存储的 key
  private getNameStorageKey(address: string): string {
    return `user_name_${address}`
  }

  // 获取当前地址保存的名字
  async getCurrentName(address: string): Promise<string> {
    try {
      const savedName = localStorage.getItem(this.getNameStorageKey(address))
      return savedName || ''
    } catch (error) {
      console.error('Failed to get current name:', error)
      return ''
    }
  }
}

export default new Service()

// --- STP Service ---
