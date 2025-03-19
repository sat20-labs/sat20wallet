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

  async pushTx(rawtx: string): Promise<string> {
    const res = await ordxApi.pushTx({ hex: rawtx, network: walletStorage.network })
    console.log('res', res)
    return res
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
}

export default new Service()
