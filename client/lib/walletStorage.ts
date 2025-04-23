import { storage } from 'wxt/storage'
import { Network, Balance, Chain, WalletAccount, WalletData } from '@/types'

interface WalletState {
  env: 'dev' | 'test' | 'prod'
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
  wallets: WalletData[]
}

type StateKey = keyof WalletState
type StateChangeCallback = (key: StateKey, newValue: any, oldValue: any) => void
type BatchUpdateData = Partial<WalletState>

const defaultState: WalletState = {
  env: 'dev',
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
  wallets: [],
}

class WalletStorage {
  private static instance: WalletStorage | null = null
  private state: WalletState
  private storageType: 'local' | 'session'
  private listeners: Set<StateChangeCallback>
  private updatePromises: Map<StateKey, Promise<void>>

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

  // 初始化状态
  public async initializeState(): Promise<void> {
    const loadPromises = Object.keys(defaultState).map(async (key) => {
      const storageKey = key as keyof WalletState
      const value = await storage.getItem(this.getStorageKey(storageKey))
      console.log('initializeState')
      console.log(storageKey, value);
      
      if (value !== null) {
        ;(this.state[storageKey] as any) =
          value as WalletState[typeof storageKey]
      }
    })

    await Promise.all(loadPromises)
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
    console.log('setValue', key, value);
    
    const oldValue = this.state[key]
    console.log('oldValue', oldValue);
    

    try {
      // 存储到本地
      await storage.setItem(this.getStorageKey(key), value)
      console.log('setValue', this.getStorageKey(key), value);
      console.log('storage', await storage.getItem(this.getStorageKey(key)));
      
      // 更新内存中的状态
      this.state[key] = value

      // 通知监听器
      this.notifyListeners(key, value, oldValue)
    } catch (error: unknown) {
      const errorMessage =
        error instanceof Error ? error.message : 'Unknown error'
      console.error(`Failed to update ${key}:`, error)
      throw new Error(`Failed to update ${key}: ${errorMessage}`)
    }
  }

  // 批量更新状态
  public async batchUpdate(updates: BatchUpdateData): Promise<void> {
    const oldState = { ...this.state }
    const updatePromises: Promise<void>[] = []

    try {
      // 创建所有更新的Promise
      for (const [key, value] of Object.entries(updates)) {
        const typedKey = key as StateKey
        if (this.state[typedKey] !== value) {
          const typedValue = value as WalletState[typeof typedKey]
          updatePromises.push(
            storage.setItem(this.getStorageKey(key), typedValue).then(() => {
              ;(this.state[typedKey] as any) = typedValue
              this.notifyListeners(typedKey, typedValue, oldState[typedKey])
            })
          )
        }
      }

      // 等待所有更新完成
      await Promise.all(updatePromises)
    } catch (error: unknown) {
      const errorMessage =
        error instanceof Error ? error.message : 'Unknown error'
      console.error('Batch update failed:', error)
      // 回滚状态
      this.state = oldState
      throw new Error(`Batch update failed: ${errorMessage}`)
    }
  }

  // 特殊的更新方法，用于处理密码相关的状态
  public async updatePassword(password: string | null): Promise<void> {
    const updates: BatchUpdateData = {
      password,
      passwordTime: password ? new Date().getTime() : null,
    }
    await this.batchUpdate(updates)
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
      const keys = Object.keys(this.state).map((key) => this.getStorageKey(key))
      console.log('keys', keys);
      
      await storage.removeItems(keys)

      const oldState = { ...this.state }
      this.state = JSON.parse(JSON.stringify(defaultState))

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
  console.log(`${key} changed from ${oldValue} to ${newValue}`)
})
// 取消订阅
unsubscribe()
*/

