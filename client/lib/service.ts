import { walletStorage } from '@/lib/walletStorage'
import { Network, Balance } from '@/types'

class Service {
  async getHasWallet(): Promise<boolean> {
    console.log('walletStorage.hasWallet:', walletStorage);
    console.log('walletStorage.hasWallet:', walletStorage.hasWallet);
    
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
    // This should be implemented with actual blockchain interaction
    throw new Error('Not implemented')
  }

  async pushPsbt(psbtHex: string): Promise<string> {
    // This should be implemented with actual blockchain interaction
    throw new Error('Not implemented')
  }
}

export default new Service()
