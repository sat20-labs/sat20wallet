import { Network, Balance, Chain, WalletAccount, WalletData, Language } from '@/types'
import { Storage } from './storage-adapter'

interface WalletState {
  env: 'dev' | 'test' | 'prd'
  language: Language
  hasWallet: boolean
  locked: boolean
  walletId: string
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
}

type StateKey = keyof WalletState
type StateChangeCallback = (key: StateKey, newValue: any, oldValue: any) => void
type BatchUpdateData = Partial<WalletState>

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
  network: Network.LIVENET,
  chain: Chain.BTC,
  walletId: '',
  accountIndex: 0,
  balance: { confirmed: 0, unconfirmed: 0, total: 0 },
  pubkey: null,
  wallets: [],
  autoLockTime: '5',
  hideBalance: false,
}

class WalletStorage {
  private static instance: WalletStorage | null = null
  private state: WalletState
  private storageType: 'local' | 'session'
  private listeners: Set<StateChangeCallback>
  private updatePromises: Map<StateKey, Promise<void>>
  private snapshotRevision: number = 0
  private initialized: boolean = false

  private constructor({
    storageType = 'local',
  }: {
    storageType: 'local' | 'session'
  }) {
    this.storageType = storageType
    this.state = JSON.parse(JSON.stringify(defaultState))
    this.listeners = new Set()
    this.updatePromises = new Map()
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

  private async persistSnapshot(nextState: WalletState): Promise<void> {
    const snapshot: WalletStateSnapshot = {
      version: 1,
      revision: this.snapshotRevision + 1,
      state: this.cloneState(nextState),
    }
    await Storage.set({
      key: this.getSnapshotKey(),
      value: JSON.stringify(snapshot),
    })
    this.snapshotRevision = snapshot.revision
  }

  // Legacy per-key values are compatibility mirrors only. Once the complete
  // snapshot is committed, mirror failures must not turn a successful atomic
  // state transition into an apparent rollback.
  private async persistLegacyMirrors(updates: BatchUpdateData): Promise<void> {
    const results = await Promise.allSettled(Object.entries(updates).map(([key, value]) => (
      Storage.set({ key: this.getStorageKey(key), value: JSON.stringify(value) })
    )))
    const failures = results.filter((result) => result.status === 'rejected')
    if (failures.length) {
      console.warn(`Wallet state snapshot committed, but ${failures.length} compatibility mirror(s) failed`)
    }
  }

  // 初始化状态
  public async initializeState(): Promise<void> {
    if (this.initialized) return

    const { value: snapshotValue } = await Storage.get({ key: this.getSnapshotKey() })
    if (snapshotValue !== null) {
      try {
        const snapshot = JSON.parse(snapshotValue) as WalletStateSnapshot
        if (snapshot?.version === 1 && snapshot.state && typeof snapshot.revision === 'number') {
          this.state = { ...this.cloneState(defaultState), ...this.cloneState(snapshot.state) }
          this.snapshotRevision = snapshot.revision
          this.initialized = true
          return
        }
      } catch (error) {
        console.error('Invalid wallet state snapshot; falling back to compatibility keys:', error)
      }
    }

    const loadPromises = Object.keys(defaultState).map(async (key) => {
      const storageKey = key as keyof WalletState
      const { value } = await Storage.get({ key: this.getStorageKey(storageKey) })
      if (value !== null) {
        ;(this.state[storageKey] as any) = JSON.parse(value) as WalletState[typeof storageKey]
      }
    })
    await Promise.all(loadPromises)
    // Migrate a complete legacy state into the authoritative snapshot.
    await this.persistSnapshot(this.state)
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

  // 更新单个状态
  public async setValue<K extends StateKey>(
    key: K,
    value: WalletState[K]
  ): Promise<void> {
    const oldValue = this.state[key]
    if (oldValue === value) return
    const nextState = this.cloneState(this.state)
    nextState[key] = value
    try {
      await this.persistSnapshot(nextState)
      this.state = nextState
      this.notifyListeners(key, value, oldValue)
      await this.persistLegacyMirrors({ [key]: value } as BatchUpdateData)
    } catch (error: unknown) {
      const errorMessage = error instanceof Error ? error.message : 'Unknown error'
      console.error(`Failed to update ${key}:`, error)
      throw new Error(`Failed to update ${key}: ${errorMessage}`)
    }
  }

  // 批量更新状态
  public async batchUpdate(updates: BatchUpdateData): Promise<void> {
    const oldState = this.cloneState(this.state)
    const nextState = this.cloneState(this.state)
    const changedKeys: StateKey[] = []
    for (const [key, value] of Object.entries(updates)) {
      const typedKey = key as StateKey
      if (nextState[typedKey] !== value) {
        ;(nextState[typedKey] as any) = value
        changedKeys.push(typedKey)
      }
    }
    if (!changedKeys.length) return

    try {
      // This single storage write is the commit point for the entire update.
      await this.persistSnapshot(nextState)
      this.state = nextState
      for (const key of changedKeys) {
        this.notifyListeners(key, nextState[key], oldState[key])
      }
      await this.persistLegacyMirrors(updates)
    } catch (error: unknown) {
      const errorMessage = error instanceof Error ? error.message : 'Unknown error'
      console.error('Batch update failed before snapshot commit:', error)
      throw new Error(`Batch update failed: ${errorMessage}`)
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
      this.snapshotRevision = 0

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
