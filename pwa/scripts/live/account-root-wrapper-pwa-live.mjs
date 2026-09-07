import { chromium } from '@playwright/test'
import { createHash } from 'node:crypto'
import { spawn } from 'node:child_process'
import { existsSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456'
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/#/'
const CDP_PORT = Number(process.env.SAT20_ROOT_WRAPPER_CDP_PORT || '9231')
const CDP_URL = `http://127.0.0.1:${CDP_PORT}`
const PROFILE_DIR = process.env.SAT20_ROOT_WRAPPER_PROFILE_DIR ||
  mkdtempSync(join(tmpdir(), 'sat20-root-wrapper-pwa-'))
const BROWSER_EXECUTABLE = process.env.SAT20_BROWSER_EXECUTABLE || [
  '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge',
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
].find(existsSync)
const HEADLESS = process.env.SAT20_BROWSER_HEADLESS === '1'
const STATIC_ONLY = process.env.SAT20_ROOT_WRAPPER_STATIC_ONLY === '1'
const SYNC_TIMEOUT = Number(process.env.SAT20_ROOT_WRAPPER_SYNC_TIMEOUT_MS || 6 * 60_000)
const RECOVERY_TIMEOUT = Number(process.env.SAT20_ROOT_WRAPPER_RECOVERY_TIMEOUT_MS || 6 * 60_000)

const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds))
const sha256 = (value) => createHash('sha256').update(value).digest('hex')
const stableJSON = (value) => JSON.stringify(value, Object.keys(value || {}).sort())

const normalizeCatalog = (wallets) => (wallets || []).map((wallet) => ({
  fingerprint: wallet.fingerprint || '',
  name: wallet.name || '',
  accounts: (wallet.accounts || []).map((account) => ({
    index: Number(account.index),
    name: account.name || '',
    did: account.did || '',
    address: account.address || '',
    pubKey: account.pub_key || account.pubKey || '',
  })).sort((left, right) => left.index - right.index),
})).sort((left, right) => (
  (left.fingerprint || left.accounts[0]?.address || '').localeCompare(
    right.fingerprint || right.accounts[0]?.address || '',
  )
))

const catalogDigest = (wallets) => sha256(JSON.stringify(normalizeCatalog(wallets)))

const assertCatalogEqual = (actual, expected, label) => {
  const actualNormalized = normalizeCatalog(actual)
  const expectedNormalized = normalizeCatalog(expected)
  if (JSON.stringify(actualNormalized) !== JSON.stringify(expectedNormalized)) {
    throw new Error(
      `${label}: catalog mismatch ${sha256(JSON.stringify(actualNormalized))} != ${sha256(JSON.stringify(expectedNormalized))}`,
    )
  }
}

const browserArguments = () => [
  `--remote-debugging-port=${CDP_PORT}`,
  `--user-data-dir=${PROFILE_DIR}`,
  '--no-first-run',
  '--no-default-browser-check',
  '--disable-sync',
  '--disable-background-networking',
  '--remote-allow-origins=*',
  ...(HEADLESS ? ['--headless=new', '--disable-gpu'] : []),
  'about:blank',
]

const waitForCDP = async (timeout = 30_000) => {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`${CDP_URL}/json/version`)
      if (response.ok) return
    } catch {}
    await sleep(250)
  }
  throw new Error(`isolated browser CDP did not become ready on ${CDP_URL}`)
}

const assertCDPPortUnused = async () => {
  try {
    const response = await fetch(`${CDP_URL}/json/version`)
    if (response.ok) {
      throw new Error(`CDP port ${CDP_PORT} is already in use; refusing to attach to a non-isolated browser`)
    }
  } catch (error) {
    if (String(error?.message || error).includes('already in use')) throw error
  }
}

const launchIsolatedBrowser = async () => {
  if (!Number.isInteger(CDP_PORT) || CDP_PORT < 1024 || CDP_PORT > 65535) {
    throw new Error(`invalid SAT20_ROOT_WRAPPER_CDP_PORT ${CDP_PORT}`)
  }
  if (!BROWSER_EXECUTABLE || !existsSync(BROWSER_EXECUTABLE)) {
    throw new Error('set SAT20_BROWSER_EXECUTABLE to a Chrome or Edge executable')
  }
  await assertCDPPortUnused()
  const child = spawn(BROWSER_EXECUTABLE, browserArguments(), {
    detached: false,
    stdio: 'ignore',
  })
  child.unref()
  await waitForCDP()
  return child
}

const primeTestnetEnvironment = async (page) => {
  const manifest = new URL('/manifest.webmanifest', PWA_URL)
  await page.goto(manifest.href, { waitUntil: 'domcontentloaded' })
  await page.evaluate(async () => {
    const db = await new Promise((resolve, reject) => {
      const request = indexedDB.open('sat20-wallet-pwa', 1)
      request.onupgradeneeded = () => {
        if (!request.result.objectStoreNames.contains('wallet-state')) {
          request.result.createObjectStore('wallet-state')
        }
      }
      request.onsuccess = () => resolve(request.result)
      request.onerror = () => reject(request.error)
    })
    await new Promise((resolve, reject) => {
      const transaction = db.transaction('wallet-state', 'readwrite')
      const store = transaction.objectStore('wallet-state')
      store.put(JSON.stringify('prd'), 'local:wallet_env')
      store.put(JSON.stringify('testnet'), 'local:wallet_network')
      store.put(JSON.stringify('btc'), 'local:wallet_chain')
      transaction.oncomplete = resolve
      transaction.onerror = () => reject(transaction.error)
    })
    db.close()
  })
  await page.goto(PWA_URL, { waitUntil: 'domcontentloaded' })
  await page.waitForFunction(() => Boolean(
    window.__SAT20_PWA_VERIFY__ && globalThis.sat20account_wasm,
  ), null, { timeout: 180_000 })
}

const clearWalletOriginAndReload = async (context, page) => {
  const session = await context.newCDPSession(page)
  await session.send('Storage.clearDataForOrigin', {
    origin: new URL(PWA_URL).origin,
    storageTypes: 'all',
  })
  await page.goto('about:blank', { waitUntil: 'domcontentloaded' })
  await primeTestnetEnvironment(page)
}

const createDKVSWriteObserver = (page) => {
  const accepted = new Set()
  const matchedRequests = new WeakMap()
  const markers = new Map([
    ['state', '/account/state'],
    ['blob', '/account-managed-data'],
    ['wrapper', '/account/root-key-wrapper/current'],
  ])
  page.on('request', (request) => {
    if (request.method() !== 'POST' || !request.url().includes('/v3/dkvs/')) return
    const body = request.postData() || ''
    const matches = [...markers.entries()]
      .filter(([, marker]) => body.includes(marker))
      .map(([name]) => name)
    if (matches.length) matchedRequests.set(request, matches)
  })
  page.on('response', async (response) => {
    const matches = matchedRequests.get(response.request())
    if (!matches || !response.ok()) return
    try {
      const payload = await response.json()
      if (payload?.code !== undefined && Number(payload.code) !== 0) return
    } catch {
      // A successful non-JSON response is still sufficient transport evidence.
    }
    for (const match of matches) accepted.add(match)
  })
  return {
    snapshot: () => [...accepted].sort(),
    waitForAll: async (timeout = SYNC_TIMEOUT) => {
      const deadline = Date.now() + timeout
      while (Date.now() < deadline) {
        if (['blob', 'state', 'wrapper'].every((name) => accepted.has(name))) return
        await sleep(500)
      }
      throw new Error(`DKVS writes not observed: ${[...accepted].sort().join(',') || 'none'}`)
    },
  }
}

const createManagedAccount = async (page) => page.evaluate(async ({ password, syncTimeout }) => {
  const verify = window.__SAT20_PWA_VERIFY__
  if (!verify || !globalThis.sat20account_wasm) throw new Error('PWA verification APIs are unavailable')
  const wallet = verify.useWalletStore()
  const sat20 = verify.sat20
  const callAccount = async (method, payload = {}) => {
    const response = await globalThis.sat20account_wasm[method](JSON.stringify(payload))
    if (!response || response.code !== 0) throw new Error(`${method}: ${response?.msg || 'failed'}`)
    return response.data
  }
  const unwrap = (tuple, operation) => {
    if (tuple?.[0]) throw new Error(`${operation}: ${tuple[0].message || String(tuple[0])}`)
    return tuple?.[1]
  }
  const withoutSecretConsole = async (operation) => {
    const originalLog = console.log
    const originalError = console.error
    const originalDebug = console.debug
    console.log = () => {}
    console.error = () => {}
    console.debug = () => {}
    try {
      return await operation()
    } finally {
      console.log = originalLog
      console.error = originalError
      console.debug = originalDebug
    }
  }
  const rawCatalog = async () => unwrap(await sat20.getWalletCatalog(), 'getWalletCatalog').wallets
	const credential = password
  await verify.walletStorage.initializeState()
  await verify.walletStorage.setValue('env', 'prd')
  await verify.walletStorage.setValue('network', 'testnet')
  await verify.walletStorage.setValue('chain', 'btc')

  const rootMnemonic = await withoutSecretConsole(async () => {
		const [error, mnemonic] = await wallet.createWallet(credential)
    if (error || !mnemonic) throw error || new Error('CreateWallet returned no mnemonic')
    return mnemonic
  })
  const rootWalletID = wallet.walletId
  await wallet.updateWalletName(rootWalletID, 'Root Wrapper E2E Root')
  await wallet.addAccount('Root Managed Account 2', 1)

  const nonRootMnemonic = await withoutSecretConsole(async () => {
		const [error, mnemonic] = await wallet.createWallet(credential)
    if (error || !mnemonic) throw error || new Error('secondary CreateWallet returned no mnemonic')
    return mnemonic
  })
  const nonRootWalletID = wallet.walletId
  await wallet.updateWalletName(nonRootWalletID, 'Root Wrapper E2E Secondary')
  await wallet.addAccount('Secondary Managed Account 2', 1)
  await wallet.updateAccountName(1, 'Secondary Managed Metadata')

  const deadline = Date.now() + syncTimeout
  let status
  while (Date.now() < deadline) {
    status = await callAccount('status')
    if (status.active && status.account_id && Number(status.state_seq || 0) > 0 &&
      Number(status.managed_data_revision || 0) > 0 && !status.managed_data_dirty &&
      Number(status.pending_changes || 0) === 0) break
    await new Promise((resolve) => setTimeout(resolve, 1_000))
  }
  if (!status?.active || !status.account_id || status.managed_data_dirty ||
    Number(status.pending_changes || 0) !== 0) {
    throw new Error(`managed account did not synchronize: ${JSON.stringify(status)}`)
  }
  const catalog = await rawCatalog()
  if (catalog.length !== 2 || catalog.some((entry) => entry.accounts.length !== 2)) {
    throw new Error('source managed catalog does not contain two wallets with two accounts each')
  }
  return {
    rootMnemonic,
    nonRootMnemonic,
    accountID: status.account_id,
    stateSeq: Number(status.state_seq),
    managedDataRevision: Number(status.managed_data_revision),
    storageMode: status.storage_mode,
    rootWalletID,
    nonRootWalletID,
    catalog,
  }
}, { password: PASSWORD, syncTimeout: SYNC_TIMEOUT })

const importRootWithRetry = async (page, rootMnemonic, expectedAccountID) => page.evaluate(async ({
  password, mnemonic, accountID, recoveryTimeout,
}) => {
  const verify = window.__SAT20_PWA_VERIFY__
  if (!verify || !globalThis.sat20account_wasm) throw new Error('PWA verification APIs are unavailable')
  const wallet = verify.useWalletStore()
  const sat20 = verify.sat20
  const callAccount = async (method, payload = {}) => {
    const response = await globalThis.sat20account_wasm[method](JSON.stringify(payload))
    if (!response || response.code !== 0) throw new Error(`${method}: ${response?.msg || 'failed'}`)
    return response.data
  }
  const unwrap = (tuple, operation) => {
    if (tuple?.[0]) throw new Error(`${operation}: ${tuple[0].message || String(tuple[0])}`)
    return tuple?.[1]
  }
  const withoutSecretConsole = async (operation) => {
    const originalLog = console.log
    const originalError = console.error
    const originalDebug = console.debug
    console.log = () => {}
    console.error = () => {}
    console.debug = () => {}
    try {
      return await operation()
    } finally {
      console.log = originalLog
      console.error = originalError
      console.debug = originalDebug
    }
  }
  const rawCatalog = async () => unwrap(await sat20.getWalletCatalog(), 'getWalletCatalog').wallets
	const credential = password
  await verify.walletStorage.initializeState()
  const deadline = Date.now() + recoveryTimeout
  const statuses = []
  let attempts = 0
  while (Date.now() < deadline) {
    attempts++
		const [error] = await withoutSecretConsole(() => wallet.importWallet(mnemonic, credential))
    if (error) throw error
    const recovery = wallet.accountRecovery ? { ...wallet.accountRecovery } : null
    statuses.push(recovery?.status || 'missing')
    if (recovery?.status === 'found') break
    if (recovery?.status === 'not_found') {
      throw new Error('root wrapper was not found after managed DKVS writes completed')
    }
    if (recovery?.status !== 'pending') {
      throw new Error(`unexpected root recovery status ${recovery?.status || 'missing'}`)
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000))
  }
  if (wallet.accountRecovery?.status !== 'found') {
    throw new Error(`root recovery timed out after ${attempts} attempts`)
  }
  const statusDeadline = Date.now() + recoveryTimeout
  let status
  while (Date.now() < statusDeadline) {
    status = await callAccount('status')
    if (status.active && status.account_id === accountID && !status.managed_data_dirty &&
      Number(status.pending_changes || 0) === 0 && Number(status.managed_data_revision || 0) > 0) break
    await new Promise((resolve) => setTimeout(resolve, 1_000))
  }
  if (!status?.active || status.account_id !== accountID || status.managed_data_dirty ||
    Number(status.pending_changes || 0) !== 0) {
    throw new Error(`restored managed account is not ready: ${JSON.stringify(status)}`)
  }
  return {
    attempts,
    statuses,
    status,
    catalog: await rawCatalog(),
  }
}, {
  password: PASSWORD,
  mnemonic: rootMnemonic,
  accountID: expectedAccountID,
  recoveryTimeout: RECOVERY_TIMEOUT,
})

const importNonRootControl = async (page, mnemonic) => page.evaluate(async ({ password, mnemonic }) => {
  const verify = window.__SAT20_PWA_VERIFY__
  if (!verify || !globalThis.sat20account_wasm) throw new Error('PWA verification APIs are unavailable')
  const wallet = verify.useWalletStore()
  const sat20 = verify.sat20
  const callAccount = async (method, payload = {}) => {
    const response = await globalThis.sat20account_wasm[method](JSON.stringify(payload))
    if (!response || response.code !== 0) throw new Error(`${method}: ${response?.msg || 'failed'}`)
    return response.data
  }
  const unwrap = (tuple, operation) => {
    if (tuple?.[0]) throw new Error(`${operation}: ${tuple[0].message || String(tuple[0])}`)
    return tuple?.[1]
  }
  const withoutSecretConsole = async (operation) => {
    const originalLog = console.log
    const originalError = console.error
    const originalDebug = console.debug
    console.log = () => {}
    console.error = () => {}
    console.debug = () => {}
    try {
      return await operation()
    } finally {
      console.log = originalLog
      console.error = originalError
      console.debug = originalDebug
    }
  }
  const rawCatalog = async () => unwrap(await sat20.getWalletCatalog(), 'getWalletCatalog').wallets
	const credential = password
  await verify.walletStorage.initializeState()
	const [error] = await withoutSecretConsole(() => wallet.importWallet(mnemonic, credential))
  if (error) throw error
  await wallet.syncWalletCatalog()
  const status = await callAccount('status')
  const catalog = await rawCatalog()
  return {
    recovery: wallet.accountRecovery ? { ...wallet.accountRecovery } : null,
    status,
    catalog,
  }
}, { password: PASSWORD, mnemonic })

const runStaticChecks = () => {
  const source = [{ fingerprint: 'b', name: 'two', accounts: [{ index: 0, address: 'b' }] },
    { fingerprint: 'a', name: 'one', accounts: [{ index: 1, address: 'a1' }, { index: 0, address: 'a0' }] }]
  const reordered = [source[1], source[0]]
  assertCatalogEqual(reordered, source, 'static catalog normalization')
  if (!PROFILE_DIR || !Number.isInteger(CDP_PORT) || stableJSON({ ok: true }) !== '{"ok":true}') {
    throw new Error('static harness configuration check failed')
  }
  console.log(JSON.stringify({ static: 'PASS', script: 'account-root-wrapper-pwa-live.mjs' }))
}

const main = async () => {
  if (STATIC_ONLY) {
    runStaticChecks()
    return
  }

  let browserProcess
  let browser
  const secrets = []
  try {
    browserProcess = await launchIsolatedBrowser()
    browser = await chromium.connectOverCDP(CDP_URL)
    const context = browser.contexts()[0] || await browser.newContext()
    const page = context.pages()[0] || await context.newPage()
    const writes = createDKVSWriteObserver(page)
    await primeTestnetEnvironment(page)

    const source = await createManagedAccount(page)
    secrets.push(source.rootMnemonic, source.nonRootMnemonic)
    await writes.waitForAll()

    const sourceDigest = catalogDigest(source.catalog)
    await clearWalletOriginAndReload(context, page)
    const restored = await importRootWithRetry(page, source.rootMnemonic, source.accountID)
    assertCatalogEqual(restored.catalog, source.catalog, 'root recovery')
    if (Number(restored.status.managed_data_revision || 0) < source.managedDataRevision) {
      throw new Error('restored managed-data revision regressed')
    }

    await clearWalletOriginAndReload(context, page)
    const nonRoot = await importNonRootControl(page, source.nonRootMnemonic)
    if (nonRoot.recovery?.status !== 'not_found') {
      throw new Error(`non-root import recovery status is ${nonRoot.recovery?.status || 'missing'}, expected not_found`)
    }
    const expectedNonRoot = source.catalog.find((wallet) => String(wallet.name).includes('Secondary'))
    if (!expectedNonRoot) throw new Error('source secondary wallet is missing')
    if (!nonRoot.status?.active || !nonRoot.status?.account_id) {
      throw new Error('former non-root import did not activate its fresh account')
    }
    if (nonRoot.status.account_id === source.accountID) {
      throw new Error('former non-root import unexpectedly joined the original managed account')
    }
    if (nonRoot.status.root_fingerprint !== expectedNonRoot.fingerprint) {
      throw new Error('fresh account root fingerprint does not match the imported secondary wallet')
    }
    if (nonRoot.catalog.length !== 1) {
      throw new Error(`ordinary non-root import restored ${nonRoot.catalog.length} wallets, expected 1`)
    }
    if (nonRoot.catalog[0]?.fingerprint !== expectedNonRoot.fingerprint) {
      throw new Error('ordinary non-root import did not restore only its own wallet')
    }

    console.log(JSON.stringify({
      result: 'PASS',
      network: 'testnet',
      cdpPort: CDP_PORT,
      profileDir: PROFILE_DIR,
      browser: BROWSER_EXECUTABLE,
      accountID: source.accountID,
      storageMode: source.storageMode,
      sourceStateSeq: source.stateSeq,
      sourceManagedDataRevision: source.managedDataRevision,
      restoredStateSeq: Number(restored.status.state_seq || 0),
      restoredManagedDataRevision: Number(restored.status.managed_data_revision || 0),
      rootMnemonicSha256: sha256(source.rootMnemonic),
      nonRootMnemonicSha256: sha256(source.nonRootMnemonic),
      sourceCatalogSha256: sourceDigest,
      restoredCatalogSha256: catalogDigest(restored.catalog),
      sourceWalletIDs: [source.rootWalletID, source.nonRootWalletID],
      restoredWalletIDs: restored.catalog.map((wallet) => wallet.id),
      rootRecoveryAttempts: restored.attempts,
      rootRecoveryStatuses: restored.statuses,
      dkvsWritesObserved: writes.snapshot(),
      nonRootRecoveryStatus: nonRoot.recovery.status,
      nonRootWalletID: nonRoot.catalog[0]?.id,
    }, null, 2))
  } catch (error) {
    let message = String(error?.stack || error?.message || error)
    for (const secret of secrets) message = message.split(secret).join('[REDACTED_MNEMONIC]')
    throw new Error(message)
  } finally {
    if (browser) await browser.close().catch(() => {})
    if (browserProcess && !browserProcess.killed) browserProcess.kill('SIGTERM')
  }
}

main().catch((error) => {
  console.error(`[Account root wrapper E2E] ${error.message}`)
  process.exitCode = 1
})
