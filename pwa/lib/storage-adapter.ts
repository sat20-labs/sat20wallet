const DB_NAME = 'sat20-wallet-pwa'
const DB_VERSION = 1
const STORE_NAME = 'wallet-state'

let dbPromise: Promise<IDBDatabase> | null = null

const shouldUseIndexedDb = (key: string) => {
  return key.startsWith('local:wallet_') ||
    key.startsWith('session:wallet_') ||
    key.startsWith('local:authorized_origins')
}

const openDatabase = () => {
  if (!dbPromise) {
    dbPromise = new Promise((resolve, reject) => {
      const request = indexedDB.open(DB_NAME, DB_VERSION)

      request.onupgradeneeded = () => {
        const db = request.result
        if (!db.objectStoreNames.contains(STORE_NAME)) {
          db.createObjectStore(STORE_NAME)
        }
      }

      request.onsuccess = () => resolve(request.result)
      request.onerror = () => reject(request.error)
    })
  }

  return dbPromise
}

const readIndexedDb = async (key: string): Promise<string | null> => {
  const db = await openDatabase()

  return new Promise((resolve, reject) => {
    const transaction = db.transaction(STORE_NAME, 'readonly')
    const store = transaction.objectStore(STORE_NAME)
    const request = store.get(key)

    request.onsuccess = () => resolve(request.result ?? null)
    request.onerror = () => reject(request.error)
  })
}

const writeIndexedDb = async (key: string, value: string): Promise<void> => {
  const db = await openDatabase()

  return new Promise((resolve, reject) => {
    const transaction = db.transaction(STORE_NAME, 'readwrite')
    const store = transaction.objectStore(STORE_NAME)
    const request = store.put(value, key)

    request.onsuccess = () => resolve()
    request.onerror = () => reject(request.error)
  })
}

const removeIndexedDb = async (key: string): Promise<void> => {
  const db = await openDatabase()

  return new Promise((resolve, reject) => {
    const transaction = db.transaction(STORE_NAME, 'readwrite')
    const store = transaction.objectStore(STORE_NAME)
    const request = store.delete(key)

    request.onsuccess = () => resolve()
    request.onerror = () => reject(request.error)
  })
}

const clearIndexedDb = async (): Promise<void> => {
  const db = await openDatabase()

  return new Promise((resolve, reject) => {
    const transaction = db.transaction(STORE_NAME, 'readwrite')
    const store = transaction.objectStore(STORE_NAME)
    const request = store.clear()

    request.onsuccess = () => resolve()
    request.onerror = () => reject(request.error)
  })
}

/**
 * PWA storage adapter. Wallet state and DApp authorization live in IndexedDB so
 * they survive standalone PWA restarts; direct localStorage writes remain for UI
 * preferences and non-core cached data.
 */
export const Storage = {
  async get({ key }: { key: string }): Promise<{ value: string | null }> {
    try {
      if (shouldUseIndexedDb(key)) {
        return { value: await readIndexedDb(key) }
      }
      const value = localStorage.getItem(key)
      return { value }
    } catch (error) {
      console.error('localStorage.getItem error:', error)
      return { value: null }
    }
  },

  async set({ key, value }: { key: string; value: string }): Promise<void> {
    try {
      if (shouldUseIndexedDb(key)) {
        await writeIndexedDb(key, value)
        return
      }
      localStorage.setItem(key, value)
    } catch (error) {
      console.error('localStorage.setItem error:', error)
      throw error
    }
  },

  async remove({ key }: { key: string }): Promise<void> {
    try {
      if (shouldUseIndexedDb(key)) {
        await removeIndexedDb(key)
        return
      }
      localStorage.removeItem(key)
    } catch (error) {
      console.error('localStorage.removeItem error:', error)
      throw error
    }
  },

  async clear(): Promise<void> {
    try {
      await clearIndexedDb()

      // 只清除钱包相关的 localStorage 项，而不是全部清除
      const keysToRemove: string[] = []
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i)
        if (key && (key.startsWith('local:wallet_') || key.startsWith('session:wallet_') || key.startsWith('authorized_origins') || key.startsWith('node_stake_') || key.startsWith('referrer_'))) {
          keysToRemove.push(key)
        }
      }
      keysToRemove.forEach(key => localStorage.removeItem(key))
    } catch (error) {
      console.error('localStorage.clear error:', error)
      throw error
    }
  }
}
