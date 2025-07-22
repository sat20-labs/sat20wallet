import { walletStorage } from '@/lib/walletStorage'
import { Network, Balance } from '@/types'
import { ordxApi } from '@/apis'
import { psbt2tx } from '@/utils/btc'
import stp from '@/utils/stp'

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
    const txHexRes = await (globalThis as any).sat20wallet_wasm.extractTxFromPsbt(psbtHex)
    const txHex = txHexRes.data.tx

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
    let res = null
    if (chain === 'btc') {
      res = await (globalThis as any).sat20wallet_wasm.extractTxFromPsbt(psbtHex)
    } else {
      res = await (globalThis as any).sat20wallet_wasm.extractTxFromPsbt_SatsNet(psbtHex)
    }
    if (res.code === 0) {
      return [undefined, res.data]
    } else {
      return [new Error(res.msg), undefined]
    }
  }

  async buildBatchSellOrder_SatsNet(
    utxos: string[],
    address: string,
    network: string
  ): Promise<string> {
    console.log('buildBatchSellOrder_SatsNet', utxos, address, network)
    const res = await (globalThis as any).sat20wallet_wasm.buildBatchSellOrder_SatsNet(
      utxos,
      address,
      network
    )
    console.log('res', res)
    return res
  }

  async splitBatchSignedPsbt(signedHex: string, network: string): Promise<string[]> {
    console.log('splitBatchSignedPsbt', signedHex, network)
    const res = (globalThis as any).sat20wallet_wasm.splitBatchSignedPsbt(
      signedHex,
      network
    )
    console.log('splitBatchSignedPsbt res', res)
    return res
  }
  async splitBatchSignedPsbt_SatsNet(signedHex: string, network: string): Promise<string[]> {
    const res = (globalThis as any).sat20wallet_wasm.splitBatchSignedPsbt_SatsNet(
      signedHex,
      network
    )
    return res
  }

  async mergeBatchSignedPsbt_SatsNet(psbts: string[], network: string): Promise<string> {
    const res = (globalThis as any).sat20wallet_wasm.mergeBatchSignedPsbt_SatsNet(
      psbts,
      network
    )
    return res
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
    console.log('finalizeSellOrder_SatsNet', {
      psbtHex,
      utxos,
      buyerAddress,
      serverAddress,
      network,
      serviceFee,
      networkFee,
    })
    const result = await (globalThis as any).sat20wallet_wasm.finalizeSellOrder_SatsNet(
      psbtHex,
      utxos,
      buyerAddress,
      serverAddress,
      network,
      serviceFee,
      networkFee
    )
    if (result.code === 0) {
      return [undefined, result.data]
    } else {
      return [new Error(result.msg), undefined]
    }
  }

  async addInputsToPsbt(
    psbtHex: string,
    utxos: string[]
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    console.log('addInputsToPsbt', { psbtHex, utxos })
    const [err, res] = await (globalThis as any).sat20wallet_wasm.addInputsToPsbt(
      psbtHex,
      utxos
    )
    if (err) {
      return [err, undefined]
    }
    return [undefined, res]
  }

  async addOutputsToPsbt(
    psbtHex: string,
    utxos: string[]
  ): Promise<[Error | undefined, { psbt: string } | undefined]> {
    console.log('addOutputsToPsbt', { psbtHex, utxos })
    const [err, res] = await (globalThis as any).sat20wallet_wasm.addOutputsToPsbt(
      psbtHex,
      utxos
    )
    if (err) {
      return [err, undefined]
    }
    return [undefined, res]
  }

  async lockUtxo(address: string, utxo: any, reason?: string): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.lockUtxo(address, utxo, reason)
  }

  async lockUtxo_SatsNet(address: string, utxo: any, reason?: string): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.lockUtxo_SatsNet(address, utxo, reason)
  }

  async unlockUtxo(address: string, utxo: any): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.unlockUtxo(address, utxo)
  }

  async unlockUtxo_SatsNet(address: string, utxo: any): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.unlockUtxo_SatsNet(address, utxo)
  }

  async getAllLockedUtxo(address: string): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getAllLockedUtxo(address)
  }

  async getAllLockedUtxo_SatsNet(address: string): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getAllLockedUtxo_SatsNet(address)
  }

  async getUtxos(): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getUtxos()
  }

  async getUtxos_SatsNet(): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getUtxos_SatsNet()
  }

  async getUtxosWithAsset(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getUtxosWithAsset(address, amt, assetName)
  }

  async getUtxosWithAsset_SatsNet(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getUtxosWithAsset_SatsNet(address, amt.toString(), assetName)
  }

  async getUtxosWithAssetV2(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getUtxosWithAssetV2(address, amt, assetName)
  }

  async getUtxosWithAssetV2_SatsNet(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getUtxosWithAssetV2_SatsNet(address, amt, assetName)
  }

  async getAssetAmount(address: string, assetName: string): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getAssetAmount(address, assetName);
  }

  async getAssetAmount_SatsNet(address: string, assetName: string): Promise<[Error | undefined, any | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getAssetAmount_SatsNet(address, assetName);
  }
  
  async getFeeForDeployContract(templateName: string, content: string, feeRate: string): Promise<[Error | undefined, { fee: any } | undefined]> {
    return (globalThis as any).sat20wallet_wasm.getFeeForDeployContract(templateName, content, feeRate);
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
