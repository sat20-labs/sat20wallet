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
    console.log('txHexRes', txHexRes)
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
    return stp.lockUtxo(address, utxo, reason)
  }

  async lockUtxo_SatsNet(address: string, utxo: any, reason?: string): Promise<[Error | undefined, any | undefined]> {
    return stp.lockUtxo_SatsNet(address, utxo, reason)
  }

  async unlockUtxo(address: string, utxo: any): Promise<[Error | undefined, any | undefined]> {
    return stp.unlockUtxo(address, utxo)
  }

  async unlockUtxo_SatsNet(address: string, utxo: any): Promise<[Error | undefined, any | undefined]> {
    return stp.unlockUtxo_SatsNet(address, utxo)
  }

  async getAllLockedUtxo(address: string): Promise<[Error | undefined, any | undefined]> {
    return stp.getAllLockedUtxo(address)
  }

  async getAllLockedUtxo_SatsNet(address: string): Promise<[Error | undefined, any | undefined]> {
    return stp.getAllLockedUtxo_SatsNet(address)
  }

  async lockToChannel(
    chanPoint: string,
    assetName: string,
    amt: number,
    utxos: string[],
    feeUtxoList?: any[]
  ): Promise<[Error | undefined, any | undefined]> {
    return stp.lockToChannel(chanPoint, assetName, amt, utxos, feeUtxoList)
  }

  async unlockFromChannel(
    channelUtxo: string,
    assetName: string,
    amt: number,
    feeUtxoList?: any[]
  ): Promise<[Error | undefined, any | undefined]> {
    return stp.unlockFromChannel(channelUtxo, assetName, amt, feeUtxoList)
  }

  async getUtxos(): Promise<[Error | undefined, any | undefined]> {
    return stp.getUtxos()
  }

  async getUtxos_SatsNet(): Promise<[Error | undefined, any | undefined]> {
    return stp.getUtxos_SatsNet()
  }

  async getUtxosWithAsset(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    return stp.getUtxosWithAsset(address, amt, assetName)
  }

  async getUtxosWithAsset_SatsNet(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    return stp.getUtxosWithAsset_SatsNet(address, amt.toString(), assetName)
  }

  async getUtxosWithAssetV2(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    return stp.getUtxosWithAssetV2(address, amt, assetName)
  }

  async getUtxosWithAssetV2_SatsNet(address: string, amt: number, assetName: string): Promise<[Error | undefined, any | undefined]> {
    return stp.getUtxosWithAssetV2_SatsNet(address, amt, assetName)
  }

  async getAssetAmount(address: string, assetName: string): Promise<[Error | undefined, { amount: string; value: string } | undefined]> {
    return stp.getAssetAmount(address, assetName);
  }

  async getAssetAmount_SatsNet(address: string, assetName: string): Promise<[Error | undefined, { amount: string; value: string } | undefined]> {
    return stp.getAssetAmount_SatsNet(address, assetName);
  }
}

export default new Service()
