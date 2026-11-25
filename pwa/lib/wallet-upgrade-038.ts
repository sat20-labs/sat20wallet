import { Buffer } from 'buffer'
import { Network } from '@/types'
import { Storage } from './storage-adapter'

const statusKey = 'wallet-status'
const markerKey = 'pwa-038-migration'
const walletKey = /^wallet-id-([1-9][0-9]*)$/
const businessKey = /^prd-(mainnet|testnet)-(status$|(?:resv|c|bc|t|u|l|lt|tc|tch|tci|tchbk|tcr|tcu|tcsr|tclp|wtptx|wtucm|wtbctx)-.+)/
const finished = Buffer.from('done').toString('base64')

type Pending = {
  network: Network.MAINNET | Network.TESTNET
  keys: Array<[string, string]>
}
type Prepared = { wallets: Record<string, string>; network: Pending['network'] }
type MigrationWasm = {
  preparePwa038: (input: string) => { code: number; msg: string; data: Prepared | null }
  upgradePwa038Password: (legacy: string, password: string) => Promise<{ code: number; msg: string }>
}

let pending: Pending | undefined

const migrationWasm = () => (globalThis as unknown as { sat20wallet_wasm: MigrationWasm }).sat20wallet_wasm

const openStore = () => new Promise<IDBDatabase>((resolve, reject) => {
  const request = indexedDB.open('sat20-wallet-sdk', 1)
  request.onupgradeneeded = () => request.result.createObjectStore('kv')
  request.onsuccess = () => resolve(request.result)
  request.onerror = () => reject(request.error)
})

const withWalletStore = async <T>(
  write: (store: IDBObjectStore, result: (value: T) => void, abort: (error: unknown) => void) => void,
): Promise<T> => {
  const db = await openStore()
  try {
    return await new Promise<T>((resolve, reject) => {
      const tx = db.transaction('kv', 'readwrite')
      let result: T
      let failure: unknown
      const abort = (error: unknown) => { failure = error; tx.abort() }
      tx.oncomplete = () => resolve(result)
      tx.onabort = () => reject(failure ?? tx.error ?? new Error('Wallet upgrade transaction aborted'))
      try {
        write(tx.objectStore('kv'), value => { result = value }, abort)
      } catch (error) {
        abort(error)
      }
    })
  } finally {
    db.close()
  }
}

const fingerprint = async (value: string) => Buffer.from(
  await crypto.subtle.digest('SHA-256', new TextEncoder().encode(value)),
).toString('hex')

// Called by the PWA under its existing startup lock, before the SDK opens
// the database. The entry check and copy share one IndexedDB transaction.
export const migrateWallet038 = async () => {
  const records = new Map<string, string>()
  const wallets: Record<string, string> = {}
  for (let i = 0; i < localStorage.length; i++) {
    const key = localStorage.key(i)
    if (!key) continue
    const id = walletKey.exec(key)?.[1]
    if (!id && key !== statusKey && !businessKey.test(key)) continue
    const value = localStorage.getItem(key)
    if (value === null) continue
    records.set(key, value)
    if (id) wallets[id] = value
  }
  if (records.size === 0) return

  const { value: catalog } = await Storage.get({ key: 'local:wallet_wallets' })
  const keys = await Promise.all([...records].map(async ([key, value]): Promise<[string, string]> =>
    [key, await fingerprint(value)]))
  pending = await withWalletStore<Pending | undefined>((store, result, abort) => {
    const marker = store.get(markerKey)
    const destKeys = store.getAllKeys(null, 2)
    const target = store.get(statusKey)
    target.onsuccess = () => {
      try {
        if (marker.result) {
          if (marker.result !== finished) {
            result(JSON.parse(Buffer.from(marker.result, 'base64').toString('utf8')))
          }
          return
        }
        if (Object.keys(wallets).length === 0 || destKeys.result.some(key => key !== statusKey)) return
        if ([...records].some(([key, value]) => localStorage.getItem(key) !== value)) {
          throw new Error('0.1.38 wallet changed during upgrade. Close the previous wallet and retry.')
        }
        const metadata = JSON.parse(catalog ?? '[]') as Array<{
          id: string | number; name: string; accounts: Array<{ index: number; name: string }>
        }>
        const prepared = migrationWasm().preparePwa038(JSON.stringify({
          status: records.get(statusKey),
          target: target.result ?? '',
          wallets,
          catalog: metadata.map(item => ({ ...item, id: String(item.id) })),
        }))
        if (prepared.code !== 0 && prepared.msg === 'Invalid 0.1.38 wallet data') {
          console.warn('Skipped 0.1.38 wallet migration because the old records failed validation.')
          return
        }
        if (prepared.code !== 0) throw new Error(prepared.msg)
        if (!prepared.data) return
        const migration = { keys, network: prepared.data.network }
        for (const [key, value] of records) store.put(prepared.data.wallets[key] ?? value, key)
        store.add(Buffer.from(JSON.stringify(migration)).toString('base64'), markerKey)
        result(migration)
      } catch (error) {
        abort(error)
      }
    }
  })
  return pending?.network
}

export const upgradeWallet038Password = async (password: string, error: Error) => {
  if (!pending || error.message !== 'password is incorrect') return false
  const result = await migrationWasm().upgradePwa038Password(await fingerprint(password), password)
  if (result.code !== 0) throw new Error(result.msg)
  return true
}

// A successful unlock has authenticated every copied wallet record. Preserve
// source values that changed independently while the upgrade was in progress.
export const finishWallet038 = async () => {
  if (!pending) return
  for (const [key, expected] of pending.keys) {
    const value = localStorage.getItem(key)
    if (value !== null && await fingerprint(value) === expected && localStorage.getItem(key) === value) {
      localStorage.removeItem(key)
    }
  }
  await withWalletStore<void>((store) => { store.put(finished, markerKey) })
  pending = undefined
}
