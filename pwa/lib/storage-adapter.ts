const DB_NAME = 'sat20-wallet-pwa'
const DB_VERSION = 1
const STORE_NAME = 'wallet-state'

let dbPromise: Promise<IDBDatabase> | null = null

const shouldUseIndexedDb = (key: string) => {
  return key.startsWith('local:wallet_') ||
    key.startsWith('session:wallet_') ||
    key.startsWith('local:dapp_') ||
    key.startsWith('local:authorized_origins')
}

const openDatabase = (): Promise<IDBDatabase> => {
  if (!dbPromise) {
    dbPromise = new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open(DB_NAME, DB_VERSION)

      request.onupgradeneeded = () => {
        const db = request.result
        if (!db.objectStoreNames.contains(STORE_NAME)) {
          db.createObjectStore(STORE_NAME)
        }
      }

      request.onsuccess = () => {
        const db = request.result
        db.onversionchange = () => { db.close(); dbPromise = null }
        db.onclose = () => { dbPromise = null }
        resolve(db)
      }
      request.onerror = () => reject(request.error)
    }).catch(error => { dbPromise = null; throw error })
  }

  return dbPromise!
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
    transaction.oncomplete = () => resolve()
    transaction.onabort = () => reject(transaction.error ?? new Error('IndexedDB write aborted'))
    store.put(value, key)
  })
}

// Read and merge under the same transaction so another tab's committed fields
// cannot be replaced by this tab's cached snapshot.
const updateIndexedDb = async (key: string, update: (value: string | null) => string): Promise<string> => {
  const db = await openDatabase()
  return new Promise((resolve, reject) => {
    const transaction = db.transaction(STORE_NAME, 'readwrite')
    const store = transaction.objectStore(STORE_NAME)
    let value: string
    let failure: unknown
    transaction.oncomplete = () => resolve(value)
    transaction.onabort = () => reject(failure ?? transaction.error ?? new Error('IndexedDB update aborted'))
    const request = store.get(key)
    request.onsuccess = () => {
      try {
        value = update(request.result ?? null)
        if (value !== request.result) store.put(value, key)
      } catch (error) {
        failure = error
        transaction.abort()
      }
    }
  })
}

const removeIndexedDb = async (key: string): Promise<void> => {
  const db = await openDatabase()

  return new Promise((resolve, reject) => {
    const transaction = db.transaction(STORE_NAME, 'readwrite')
    const store = transaction.objectStore(STORE_NAME)
    transaction.oncomplete = () => resolve()
    transaction.onabort = () => reject(transaction.error ?? new Error('IndexedDB delete aborted'))
    store.delete(key)
  })
}

const clearIndexedDb = async (): Promise<void> => {
  const db = await openDatabase()

  return new Promise((resolve, reject) => {
    const transaction = db.transaction(STORE_NAME, 'readwrite')
    const store = transaction.objectStore(STORE_NAME)
    transaction.oncomplete = () => resolve()
    transaction.onabort = () => reject(transaction.error ?? new Error('IndexedDB clear aborted'))
    store.clear()
  })
}

/**
 * PWA preferences, DApp authorization and biometric bindings use IndexedDB.
 * Wallet catalog, selection and identity are persisted by the SDK. Other
 * non-core cached data can still use localStorage.
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
      console.error('PWA storage read failed:', error)
      throw error
    }
  },

  async update({ key, update }: { key: string; update: (value: string | null) => string }): Promise<string> {
    if (!shouldUseIndexedDb(key)) throw new Error('Transactional updates require IndexedDB')
    return updateIndexedDb(key, update)
  },

  async set({ key, value }: { key: string; value: string }): Promise<void> {
    try {
      if (shouldUseIndexedDb(key)) {
        await writeIndexedDb(key, value)
        return
      }
      localStorage.setItem(key, value)
    } catch (error) {
      console.error('PWA storage write failed:', error)
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
        if (key && (key.startsWith('local:wallet_') || key.startsWith('session:wallet_') || key.startsWith('local:dapp_') || key.startsWith('authorized_origins') || key.startsWith('node_stake_') || key.startsWith('referrer_'))) {
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
