import { walletStorage } from '@/lib/walletStorage'
import { Network, Balance } from '@/types'
import { ordxApi } from '@/apis'
import { psbt2tx } from '@/utils/btc'

class Service {
  async getHasWallet(): Promise<boolean> {
    console.log('walletStorage.hasWallet:', walletStorage)
    console.log('walletStorage.hasWallet:', walletStorage.hasWallet)

    return walletStorage.hasWallet
  }
  async getAccounts(): Promise<string[]> {
    const address = walletStorage.address
    return address ? [address] : []
  }
  async getNetwork(): Promise<Network> {
    return walletStorage.network
  }

  async getPublicKey(): Promise<string> {
    const pubkey = walletStorage.pubkey
    if (!pubkey) {
      throw new Error('Public key not available')
    }
    return pubkey
  }

  async getBalance(): Promise<Balance> {
    return walletStorage.balance
  }

  async pushTx(rawtx: string): Promise<[Error | undefined, string | undefined]> {
    const res = await ordxApi.pushTx({ hex: rawtx, network: walletStorage.network })
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

    const res = await ordxApi.pushTx({ hex: txHex, network: walletStorage.network })
    console.log('res', res)
    if (res.code === 0) {
      return [undefined, res.data]
    } else {
      return [new Error(res.msg), undefined]
    }
  }

  async buildBatchSellOrder(utxos: string[], address: string, network: string): Promise<string> {
    console.log('buildBatchSellOrder', utxos, address, network)
    
    const res = await (globalThis as any).sat20wallet_wasm.buildBatchSellOrder(utxos, address, network)
    console.log('res', res)
    return res
  }

  async splitBatchSignedPsbt(signedHex: string, network: string): Promise<string[]> {
    console.log('splitBatchSignedPsbt', signedHex, network)
    const res = (globalThis as any).sat20wallet_wasm.splitBatchSignedPsbt(signedHex, network)
    console.log('splitBatchSignedPsbt res', res)
    return res
  }
}

export default new Service()
