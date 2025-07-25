import { walletStorage } from '@/lib/walletStorage'
import { Network, Balance } from '@/types'
import { ordxApi } from '@/apis'
import { psbt2tx } from '@/utils/btc'
import stp from '@/utils/stp'
import sat20Wallet from '@/utils/sat20'

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
    const network = walletStorage.getValue('network')
    const res = await ordxApi.pushTx({ hex: rawtx, network })
    if (res.code === 0) {
      return [undefined, res.data]
    } else {
      return [new Error(res.msg), undefined]
    }
  }

  async pushPsbt(psbtHex: string): Promise<[Error | undefined, string | undefined]> {
    console.log('pushPsbt', psbtHex)
    const [extractErr, extractRes] = await sat20Wallet.extractTxFromPsbt(psbtHex)
    if (extractErr || !extractRes) {
      return [extractErr || new Error('提取交易失败'), undefined]
    }
    const txHex = extractRes.psbt
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
    { chain }: { chain: string }
  ): Promise<[Error | undefined, { tx: string } | undefined]> {
    let extractErr, extractRes
    if (chain === 'btc') {
      [extractErr, extractRes] = await sat20Wallet.extractTxFromPsbt(psbtHex)
    } else {
      [extractErr, extractRes] = await sat20Wallet.extractTxFromPsbt_SatsNet(psbtHex)
    }
    if (extractErr || !extractRes) {
      return [extractErr || new Error('提取交易失败'), undefined]
    }
    return [undefined, { tx: extractRes.psbt }]
  }

  async buildBatchSellOrder_SatsNet(
    utxos: string[],
    address: string,
    network: string
  ): Promise<[Error | undefined, { orderId: string } | undefined]> {
    return sat20Wallet.buildBatchSellOrder_SatsNet(utxos, address, network)
  }

  async splitBatchSignedPsbt(signedHex: string, network: string): Promise<[Error | undefined, { psbts: string[] } | undefined]> {
    return sat20Wallet.splitBatchSignedPsbt(signedHex, network)
  }
  async splitBatchSignedPsbt_SatsNet(signedHex: string, network: string): Promise<[Error | undefined, { psbts: string[] } | undefined]> {
    return sat20Wallet.splitBatchSignedPsbt(signedHex, network) // 假设 SatsNet 也用同名方法
  }

  async mergeBatchSignedPsbt_SatsNet(psbts: string[], network: string): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return sat20Wallet.mergeBatchSignedPsbt_SatsNet(psbts, network)
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
    return sat20Wallet.finalizeSellOrder_SatsNet(
      psbtHex,
      utxos,
      buyerAddress,
      serverAddress,
      network,
      serviceFee,
      networkFee
    )
  }

  async addInputsToPsbt(
    psbtHex: string,
    utxos: string[]
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return sat20Wallet.addInputsToPsbt(psbtHex, utxos)
  }

  async addOutputsToPsbt(
    psbtHex: string,
    utxos: string[]
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    return sat20Wallet.addOutputsToPsbt(psbtHex, utxos)
  }

  async lockUtxo(address: string, utxo: any, reason?: string): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.lockUtxo(address, utxo, reason)
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async lockUtxo_SatsNet(address: string, utxo: any, reason?: string): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.lockUtxo_SatsNet(address, utxo, reason)
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async unlockUtxo(address: string, utxo: any): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.unlockUtxo(address, utxo)
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async unlockUtxo_SatsNet(address: string, utxo: any): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.unlockUtxo_SatsNet(address, utxo)
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async getAllLockedUtxo(address: string): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.getAllLockedUtxo(address)
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async getAllLockedUtxo_SatsNet(address: string): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.getAllLockedUtxo_SatsNet(address)
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async getUtxos(): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.getUtxos()
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async getUtxos_SatsNet(): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.getUtxos_SatsNet()
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async getUtxosWithAsset(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.getUtxosWithAsset(address, amt, assetName)
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async getUtxosWithAsset_SatsNet(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.getUtxosWithAsset_SatsNet(address, amt.toString(), assetName)
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async getUtxosWithAssetV2(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.getUtxosWithAssetV2(address, amt, assetName)
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async getUtxosWithAssetV2_SatsNet(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.getUtxosWithAssetV2_SatsNet(address, amt, assetName)
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async getAssetAmount(address: string, assetName: string): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.getAssetAmount(address, assetName);
    if (result.code === 0) {
      return [undefined, result.data];
    } else {
      return [new Error(result.msg), undefined];
    }
  }

  async getAssetAmount_SatsNet(address: string, assetName: string): Promise<[Error | undefined, any | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.getAssetAmount_SatsNet(address, assetName);
    console.log(result);

    if (result.code === 0) {
      return [undefined, result.data];
    } else {
      return [new Error(result.msg), undefined];
    }
  }

  async getFeeForDeployContract(templateName: string, content: string, feeRate: string): Promise<[Error | undefined, { fee: any } | undefined]> {
    const result = await (globalThis as any).sat20wallet_wasm.getFeeForDeployContract(templateName, content, feeRate);
    if (result.code === 0) {
      return [undefined, result.data];
    } else {
      return [new Error(result.msg), undefined];
    }
  }
  async getFeeForInvokeContract(url: string, invoke: string): Promise<[Error | undefined, { fee: any } | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getFeeForInvokeContract(url, invoke);
  }
  async getParamForInvokeContract(templateName: string, action: string): Promise<[Error | undefined, { parameter: any } | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getParamForInvokeContract(templateName, action);
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
