import { Network, Balance, Chain, WalletAccount, WalletData, Language } from '@/types'
import { Storage } from './storage-adapter'

export type AccountRecoveryState = Pick<RootAccountRecoveryResult, 'status' | 'code'> & {
  env?: 'dev' | 'test' | 'prd'
  network?: Network
  rootAccountId?: string
}

interface WalletState {
  env: 'dev' | 'test' | 'prd'
  language: Language
  hasWallet: boolean
  locked: boolean
  walletId: string
  rootAccountId: string
  accountIndex: number
  address: string | null
  isConnected: boolean
  network: Network
  chain: Chain
  balance: Balance
  pubkey: string | null
  wallets: WalletData[]
  autoLockTime: string
  hideBalance: boolean
  accountRecovery: AccountRecoveryState | null
}

type StateKey = keyof WalletState
type StateChangeCallback = (key: StateKey, newValue: any, oldValue: any) => void
type BatchUpdateData = Partial<WalletState>

// SDK owns the durable wallet catalog, selection and public identity. These
// fields are a reactive view for this page, never a second wallet database.
const preferenceKeys = ['env', 'language', 'network', 'chain', 'autoLockTime', 'hideBalance'] as const
const preferences = (state: Partial<WalletState>): Partial<WalletState> =>
  Object.fromEntries(preferenceKeys.filter(key => key in state).map(key => [key, state[key]]))

interface WalletStateSnapshot {
  version: 1
  revision: number
  state: WalletState
}

const defaultState: WalletState = {
  env: 'prd',
  language: 'en',
  locked: true,
  hasWallet: false,
  address: null,
  isConnected: false,
  network: Network.MAINNET,
  chain: Chain.BTC,
  walletId: '',
  rootAccountId: '',
  accountIndex: 0,
  balance: { confirmed: 0, unconfirmed: 0, total: 0 },
  pubkey: null,
  wallets: [],
  autoLockTime: '5',
  hideBalance: false,
  accountRecovery: null,
}

class WalletStorage {
  private static instance: WalletStorage | null = null
  private state: WalletState
  private storageType: 'local' | 'session'
  private listeners: Set<StateChangeCallback>
  private initialized: boolean = false

  private constructor({
    storageType = 'local',
  }: {
    storageType: 'local' | 'session'
  }) {
    this.storageType = storageType
    this.state = JSON.parse(JSON.stringify(defaultState))
    this.listeners = new Set()
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
  ): `${typeof this.storageType}:wallet_${string}` {
    return `${this.storageType}:wallet_${key}`
  }

  private getSnapshotKey(): `${typeof this.storageType}:wallet_${string}` {
    return this.getStorageKey('state_snapshot_v1')
  }

  private cloneState(value: WalletState): WalletState {
    return JSON.parse(JSON.stringify(value)) as WalletState
  }

  private migrateNetworkNames(state: WalletState): boolean {
    let changed = false
    if ((state.network as string) === 'livenet') {
      state.network = Network.MAINNET
      changed = true
    }
    if (state.accountRecovery && (state.accountRecovery.network as string) === 'livenet') {
      state.accountRecovery.network = Network.MAINNET
      changed = true
    }
    return changed
  }

  private parseSnapshot(value: string | null): WalletStateSnapshot {
    if (value === null) return { version: 1, revision: 0, state: this.cloneState(defaultState) }
    const snapshot = JSON.parse(value) as WalletStateSnapshot
    if (snapshot?.version !== 1 || !snapshot.state || typeof snapshot.state !== 'object' || Array.isArray(snapshot.state) ||
      !Number.isSafeInteger(snapshot.revision) || snapshot.revision < 0 || snapshot.revision >= Number.MAX_SAFE_INTEGER) {
      throw new Error('Invalid wallet state snapshot')
    }
    return { ...snapshot, state: { ...this.cloneState(defaultState), ...preferences(snapshot.state) } }
  }

  private applySnapshot(snapshot: WalletStateSnapshot): void {
    const oldState = this.state
    this.state = { ...this.state, ...preferences(snapshot.state) }
    for (const key of Object.keys(defaultState) as StateKey[]) {
      if (JSON.stringify(oldState[key]) !== JSON.stringify(this.state[key])) {
        this.notifyListeners(key, this.state[key], oldState[key])
      }
    }
  }

  // Startup reads preferences; wallet identity is read from the SDK on unlock.
  public async initializeState(): Promise<void> {
    if (this.initialized) return
    const { value } = await Storage.get({ key: this.getSnapshotKey() })
    const snapshot = this.parseSnapshot(value)
    this.migrateNetworkNames(snapshot.state)
    this.applySnapshot(snapshot)
    this.initialized = true
  }

  // 获取状态
  public getState(): Readonly<WalletState> {
    return { ...this.state }
  }

  // 获取单个状态值
  public getValue<K extends StateKey>(key: K): WalletState[K] {
    return this.state[key]
  }

  // Commit only the requested fields against the current durable snapshot.
  // Concurrent pages can keep different caches; those caches are never a
  // replacement for fields committed by another page.
  public async setValue<K extends StateKey>(key: K, value: WalletState[K]): Promise<void> {
    await this.batchUpdate({ [key]: value } as BatchUpdateData)
  }

  public async batchUpdate(updates: BatchUpdateData): Promise<void> {
    const durable = preferences(updates)
    if (!Object.keys(durable).length) {
      this.applyRuntimeUpdates(updates)
      return
    }
    const committed = await Storage.update({
      key: this.getSnapshotKey(),
      update: value => {
        const previous = this.parseSnapshot(value)
        const nextState = {
          ...this.cloneState(defaultState),
          ...this.cloneState(previous.state),
          ...durable,
        } as WalletState
        const nextPreferences = preferences(nextState)
        if (value !== null && JSON.stringify(nextPreferences) === JSON.stringify(preferences(previous.state))) return value
        return JSON.stringify({ version: 1, revision: previous.revision + 1, state: nextPreferences })
      },
    })
    this.applySnapshot(this.parseSnapshot(committed))
    this.applyRuntimeUpdates(updates)
  }

  private applyRuntimeUpdates(updates: BatchUpdateData): void {
    const previous = this.state
    this.state = { ...this.state, ...JSON.parse(JSON.stringify(updates)) }
    for (const key of Object.keys(updates) as StateKey[]) {
      if (JSON.stringify(previous[key]) !== JSON.stringify(this.state[key])) {
        this.notifyListeners(key, this.state[key], previous[key])
      }
    }
  }

  // 订阅状态变化
  public subscribe(callback: StateChangeCallback): () => void {
    this.listeners.add(callback)
    return () => {
      this.listeners.delete(callback)
    }
  }

  // 通知所有监听器
  private notifyListeners<K extends StateKey>(
    key: K,
    newValue: WalletState[K],
    oldValue: WalletState[K]
  ): void {
    this.listeners.forEach((listener) => {
      try {
        listener(key, newValue, oldValue)
      } catch (error: unknown) {
        const errorMessage =
          error instanceof Error ? error.message : 'Unknown error'
        console.error('Error in state change listener:', errorMessage)
      }
    })
  }

  // 清除所有状态
  public async clear(): Promise<void> {
    try {
      await Storage.clear()

      const oldState = { ...this.state }
      this.state = this.cloneState(defaultState)

      // 通知所有状态的变化
      Object.keys(oldState).forEach((key) => {
        const typedKey = key as StateKey
        this.notifyListeners(typedKey, this.state[typedKey], oldState[typedKey])
      })
    } catch (error: unknown) {
      const errorMessage =
        error instanceof Error ? error.message : 'Unknown error'
      console.error('Failed to clear storage:', error)
      throw new Error(`Failed to clear storage: ${errorMessage}`)
    }
  }
}

// 导出单例实例获取器
export const walletStorage = WalletStorage.getInstance()

// 使用示例：
/*
// 获取状态
const state = walletStorage.getState()
const address = walletStorage.getValue('address')

// 更新单个状态
await walletStorage.setValue('address', '0x123...')

// 批量更新状态
await walletStorage.batchUpdate({
  address: '0x123...',
  isConnected: true
})

// 订阅状态变化
const unsubscribe = walletStorage.subscribe((key, newValue, oldValue) => {
})
// 取消订阅
unsubscribe()
*/
