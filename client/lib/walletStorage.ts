import { storage } from 'wxt/storage'
import { Network, Balance, Chain } from '@/types'

interface WalletState {
  hasWallet: boolean
  locked: boolean
  walletId: number
  accountIndex: number
  address: string | null
  isConnected: boolean
  password: string | null
  network: Network
  chain: Chain
  passwordTime: number | null
  balance: Balance
  pubkey: string | null
}
const defaultState: WalletState = {
  locked: true,
  hasWallet: false,
  address: null,
  isConnected: false,
  password: null,
  network: Network.LIVENET,
  chain: Chain.BTC,
  walletId: 0,
  accountIndex: 0,
  balance: { confirmed: 0, unconfirmed: 0, total: 0 },
  pubkey: null,
  passwordTime: null,
}

class WalletStorage {
  private static instance: WalletStorage | null = null
  private state: WalletState
  private storageType: 'local' | 'session'

  private constructor({
    storageType = 'local',
  }: {
    storageType: 'local' | 'session'
  }) {
    this.storageType = storageType
    this.state = JSON.parse(JSON.stringify(defaultState))
  }

  public static getInstance(
    config: { storageType: 'local' | 'session' } = { storageType: 'local' }
  ): WalletStorage {
    if (!WalletStorage.instance) {
      WalletStorage.instance = new WalletStorage(config)
    }
    return WalletStorage.instance
  }

  private getStorageKey(
    key: string
  ): `local:wallet_${string}` | `session:wallet_${string}` {
    return `${this.storageType}:wallet_${key}`
  }

  public async initializeState() {
    for (const key of Object.keys(this.state) as Array<keyof WalletState>) {
      const value: any = await storage.getItem(this.getStorageKey(key))

      if (value !== null) {
        ;(this.state[key] as any) = value
      }
    }
  }

  private async persistState(key: keyof WalletState, value: any) {
    await storage.setItem(this.getStorageKey(key), value)
  }

  get address() {
    return this.state.address
  }
  get password() {
    return this.state.password
  }
  get locked() {
    return this.state.locked
  }

  get isConnected() {
    return this.state.isConnected
  }

  get network() {
    return this.state.network
  }
  get passwordTime() {
    return this.state.passwordTime
  }
  get chain() {
    return this.state.chain
  }

  get walletId() {
    return this.state.walletId
  }
  get accountIndex() {
    return this.state.accountIndex
  }
  get hasWallet() {
    return this.state.hasWallet
  }

  get balance() {
    return this.state.balance
  }

  get pubkey() {
    return this.state.pubkey
  }

  // Setters
  set address(value: string | null) {
    this.state.address = value
    this.persistState('address', value)
  }
  set locked(value: boolean) {
    this.state.locked = value
    this.persistState('locked', value)
  }
  set password(value: string | null) {
    this.state.password = value
    if (value) {
      this.persistState('passwordTime', new Date().getTime())
    } else {
      this.persistState('passwordTime', null)
    }
    this.persistState('password', value)
  }
  set isConnected(value: boolean) {
    this.state.isConnected = value
    this.persistState('isConnected', value)
  }
  set hasWallet(value: boolean) {
    this.state.hasWallet = value
    this.persistState('hasWallet', value)
  }
  set network(value: Network) {
    this.state.network = value
    this.persistState('network', value)
  }

  set chain(value: Chain) {
    this.state.chain = value
    this.persistState('chain', value)
  }

  set walletId(value: number) {
    this.state.walletId = value
    this.persistState('walletId', value)
  }
  set accountIndex(value: number) {
    this.state.accountIndex = value
    this.persistState('accountIndex', value)
  }

  set balance(value: Balance) {
    this.state.balance = value
    this.persistState('balance', value)
  }

  set pubkey(value: string | null) {
    this.state.pubkey = value
    this.persistState('pubkey', value)
  }

  // Helper methods
  async clear() {
    const keys = (Object.keys(this.state) as Array<keyof WalletState>).map(
      (key) => this.getStorageKey(key)
    )
    await storage.removeItems(keys)
    this.state = JSON.parse(JSON.stringify(defaultState))
  }
}

// Export singleton instance getter
export const walletStorage = WalletStorage.getInstance()
