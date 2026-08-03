import { chromium } from '@playwright/test'
import { appendFileSync } from 'node:fs'

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456'
const PRIMARY_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/#/'
const RECOVERY_URL = process.env.SAT20_RECOVERY_PWA_URL || 'http://127.0.0.1:5173/#/'
const CONTINUE_ACTIVE = process.env.SAT20_ACCOUNT_CONTINUE_ACTIVE === '1'
const TRACE_DKVS = process.env.SAT20_TRACE_DKVS === '1'
const TRACE_DKVS_FILE = process.env.SAT20_TRACE_DKVS_FILE || ''
const RESET_PRIMARY = process.env.SAT20_ACCOUNT_RESET_PRIMARY === '1'
const TEST_MNEMONICS = [
  'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire',
  'comfort very add tuition senior run eight snap burst appear exile dutch',
]
if (process.env.SAT20_ACCOUNT_REVERSE_WALLETS === '1') TEST_MNEMONICS.reverse()
const ANSWERS = [
  { question_id: 'account-e2e-a', answer: 'sat20 account recovery answer alpha' },
  { question_id: 'account-e2e-b', answer: 'sat20 account recovery answer beta' },
  { question_id: 'account-e2e-c', answer: 'sat20 account recovery answer gamma' },
]

const primeEnvironment = async (page, targetURL) => {
  const manifest = new URL('/manifest.webmanifest', targetURL)
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
  await page.goto(targetURL, { waitUntil: 'domcontentloaded' })
  await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__ && globalThis.sat20account_wasm), null, {
    timeout: 180_000,
  })
}

const traceDKVSRequests = (page) => {
  if (!TRACE_DKVS) return
  page.on('request', (request) => {
    if (request.method() !== 'POST' || !request.url().includes('/v3/dkvs/')) return
    const body = request.postData()
    if (!body) return
    if (TRACE_DKVS_FILE) appendFileSync(TRACE_DKVS_FILE, `${body}\n`)
    try {
      const payload = JSON.parse(body)
      const mutations = Array.isArray(payload.mutations) ? payload.mutations : []
      const field = (record, name) => record?.[name] ?? record?.[name[0].toUpperCase() + name.slice(1)]
      console.log('[Account live][DKVS request]', JSON.stringify({
        url: request.url(),
        endpoint_id: payload.endpoint_id || '',
        mutations: mutations.map(({ record, expected_hash, expect_absent }) => ({
          fields: Object.keys(record || {}),
          key: field(record, 'key'),
          seq: field(record, 'seq'),
          path_generation: field(record, 'path_generation') ?? field(record, 'pathGeneration'),
          ttl: field(record, 'ttl'),
          expiry_height: field(record, 'expiry_height') ?? field(record, 'expiryHeight'),
          flags: field(record, 'flags'),
          value_bytes: typeof field(record, 'value') === 'string' ? field(record, 'value').length : 0,
          pub_key: field(record, 'pub_key') ?? field(record, 'pubKey') ?? '',
          signature: field(record, 'signature') || '',
          fee_proof: field(record, 'fee_proof') ?? field(record, 'feeProof') ?? '',
          expected_hash: expected_hash || '',
          expect_absent: Boolean(expect_absent),
        })),
        path_preconditions: payload.path_preconditions || [],
      }))
    } catch {
      console.log('[Account live][DKVS request]', request.url(), body.length)
    }
  })
}

const main = async () => {
  const browser = await chromium.connectOverCDP(CDP)
  const context = browser.contexts()[0] || await browser.newContext()
  let primary = context.pages().find((page) => page.url().startsWith(new URL(PRIMARY_URL).origin))
  if (!primary) primary = await context.newPage()
  traceDKVSRequests(primary)
  primary.on('console', (message) => {
    if (message.type() === 'error' || message.text().includes('[Account live]')) {
      console.error(`[page:${message.type()}] ${message.text()}`)
    }
  })
  if (RESET_PRIMARY) {
    const cdp = await context.newCDPSession(primary)
    await cdp.send('Storage.clearDataForOrigin', {
      origin: new URL(PRIMARY_URL).origin,
      storageTypes: 'all',
    })
  }
  await primeEnvironment(primary, PRIMARY_URL)
  console.log('[Account live] primary PWA ready')

  const activation = await primary.evaluate(async ({ password, mnemonics, answers, continueActive }) => {
    const verify = window.__SAT20_PWA_VERIFY__
    const wallet = verify.useWalletStore()
    const account = globalThis.sat20account_wasm
    const hashed = await verify.hashPassword(password)
    const call = async (method, payload = {}) => {
      const response = await account[method](JSON.stringify(payload))
      if (!response || response.code !== 0) throw new Error(`${method}: ${response?.msg || 'failed'}`)
      return response.data
    }
    const tuple = (result, operation) => {
      if (result?.[0]) throw new Error(`${operation}: ${result[0].message || result[0]}`)
      return result?.[1]
    }
    const summarizeCatalog = (wallets) => wallets.map((item) => ({
      id: item.id,
      name: item.name,
      accounts: item.accounts.map((entry) => ({ index: entry.index, address: entry.address })),
    }))
    const metadata = () => wallet.wallets.map((item) => ({
      id: Number(item.id),
      name: item.name,
      sub_accounts: Object.fromEntries(item.accounts.map((entry) => [entry.index, entry.did || ''])),
    }))
    const waitSynced = async (label) => {
      const deadline = Date.now() + 180_000
      let status
      while (Date.now() < deadline) {
        status = await call('status')
        if (status.active && Number(status.pending_changes || 0) === 0) return status
        await new Promise((resolve) => setTimeout(resolve, 1_000))
      }
      throw new Error(`${label}: account state did not synchronize (${JSON.stringify(status)})`)
    }

    await verify.walletStorage.initializeState()
    await verify.walletStorage.setValue('env', 'prd')
    await verify.walletStorage.setValue('network', 'testnet')
    await verify.walletStorage.setValue('chain', 'btc')
    await wallet.syncWalletCatalog().catch(() => [])
    for (const mnemonic of mnemonics) {
      const known = wallet.wallets.some((item) => item.accounts.some((entry) => entry.index === 0))
      if (wallet.wallets.length >= mnemonics.length && known) break
      tuple(await wallet.importWallet(mnemonic, hashed), 'importWallet')
    }
    await wallet.setPassword(hashed)
    await wallet.setNetwork(verify.Network.TESTNET)
    await wallet.setChain(verify.Chain.BTC)
    const [unlockError] = await wallet.unlockWallet(hashed)
    if (unlockError && !/already unlocked/i.test(String(unlockError.message || unlockError))) throw unlockError
    await wallet.syncWalletCatalog()
    if (wallet.wallets.length < 2) throw new Error('two test wallets are required')

    const initialCatalog = summarizeCatalog(wallet.wallets)
    const preflight = await call('preflight', { password: hashed, wallets: metadata() })
    let status = await call('status')
    let locator
    let userShare
    if (!status.active) {
      const storage = await call('confirmStorage', { option_id: 'paid', record_count: 100 })
      const questions = answers.map((entry, index) => ({
        id: entry.question_id,
        prompt: `SAT20 account E2E recovery question ${index + 1}`,
        answer: entry.answer,
        confirmation: entry.answer,
        ignore_punctuation: true,
      }))
      const created = await call('createRecovery', {
        password: hashed,
        wallets: metadata(),
        recovery_mode: '2of2',
        questions,
        storage_authorization_id: storage.id,
      })
      locator = created.locator
      userShare = created.user_share
      const rehearsal = await call('rehearse', {
        session_id: created.session_id,
        answers,
        user_share: userShare,
        password: hashed,
      })
      if (!rehearsal.verified) throw new Error('account recovery rehearsal failed')
      status = await waitSynced('activation')
    } else if (continueActive) {
      status = await waitSynced('existing pending changes')
    } else {
      throw new Error('test account management is already active; locator is not available to this isolated run')
    }

    const rootWalletID = wallet.wallets[0].id
    const secondWalletID = wallet.wallets[1].id
    const [rootDeleteError] = await wallet.deleteWallet(rootWalletID)
    if (!rootDeleteError) throw new Error('root wallet deletion was unexpectedly allowed')

    await wallet.switchWallet(secondWalletID)
    const second = wallet.wallets.find((item) => item.id === secondWalletID)
    if (!second.accounts.some((entry) => entry.index === 2)) {
      await wallet.addAccount('Account 3', 2)
    }
    await waitSynced('new subaccount')

    tuple(await wallet.createWallet(hashed), 'create temporary wallet')
    const temporaryWalletID = wallet.walletId
    await wallet.addAccount('Temporary Account 2', 1)
    await waitSynced('temporary wallet creation')
    const [deleteError] = await wallet.deleteWallet(temporaryWalletID)
    if (deleteError) throw deleteError
    status = await waitSynced('temporary wallet deletion')
    await wallet.syncWalletCatalog()
    if (wallet.wallets.some((item) => item.id === temporaryWalletID)) {
      throw new Error('deleted temporary wallet remains in the catalog')
    }
    const finalCatalog = summarizeCatalog(wallet.wallets)
    const finalSecond = finalCatalog.find((item) => item.id === secondWalletID)
    if (!finalSecond?.accounts.some((entry) => entry.index === 2)) {
      throw new Error('managed subaccount is missing before recovery')
    }
    return { hashed, locator, userShare, preflight, initialCatalog, finalCatalog, status }
  }, { password: PASSWORD, mnemonics: TEST_MNEMONICS, answers: ANSWERS, continueActive: CONTINUE_ACTIVE })

  console.log('[Account live] activation and managed mutations synchronized')
  if (!activation.locator || !activation.userShare) {
    console.log(JSON.stringify({
      network: 'production/testnet',
      storageMode: activation.status.storage_mode,
      accountStateSeq: activation.status.state_seq,
      pendingChanges: activation.status.pending_changes,
      preflightWalletCount: activation.preflight.wallets.length,
      initialCatalog: activation.initialCatalog,
      finalCatalog: activation.finalCatalog,
      recovery: 'not-run: existing activation share is intentionally unavailable',
    }, null, 2))
    return
  }
  const recoveryOrigin = new URL(RECOVERY_URL).origin
  const recovery = await context.newPage()
  const cdp = await context.newCDPSession(recovery)
  await cdp.send('Storage.clearDataForOrigin', { origin: recoveryOrigin, storageTypes: 'all' })
  await primeEnvironment(recovery, RECOVERY_URL)
  console.log('[Account live] independent recovery PWA ready')

  const restored = await recovery.evaluate(async ({ hashed, locator, userShare, answers }) => {
    const account = globalThis.sat20account_wasm
    const verify = window.__SAT20_PWA_VERIFY__
    const call = async (method, payload = {}) => {
      const response = await account[method](JSON.stringify(payload))
      if (!response || response.code !== 0) throw new Error(`${method}: ${response?.msg || 'failed'}`)
      return response.data
    }
    const summarizeCatalog = (wallets) => wallets.map((item) => ({
      id: item.id,
      name: item.name,
      accounts: item.accounts.map((entry) => ({ index: entry.index, address: entry.address })),
    }))
    const loaded = await call('loadRecovery', { locator })
    await call('recoverKnowledge', { session_id: loaded.session_id, answers })
    await call('setUserShare', { session_id: loaded.session_id, user_share: userShare })
    const preview = await call('previewRecovery', { session_id: loaded.session_id })
    const committed = await call('commitRecovery', { session_id: loaded.session_id, password: hashed })
    await verify.walletStorage.initializeState()
    const wallet = verify.useWalletStore()
    const catalog = await wallet.syncWalletCatalog()
    return { preview: preview.summary, committed: committed.wallets, catalog: summarizeCatalog(catalog) }
  }, {
    hashed: activation.hashed,
    locator: activation.locator,
    userShare: activation.userShare,
    answers: ANSWERS,
  })

  if (restored.catalog.length !== activation.finalCatalog.length) {
    throw new Error(`restored wallet count mismatch: ${restored.catalog.length} != ${activation.finalCatalog.length}`)
  }
  for (const expected of activation.finalCatalog) {
    const expectedRootAddress = expected.accounts.find((entry) => entry.index === 0)?.address
    const actual = restored.catalog.find((item) =>
      item.accounts.some((entry) => entry.index === 0 && entry.address === expectedRootAddress))
    if (!actual || actual.accounts.length !== expected.accounts.length) {
      throw new Error(`restored catalog mismatch for wallet ${expectedRootAddress}`)
    }
    for (const account of expected.accounts) {
      const restoredAccount = actual.accounts.find((entry) => entry.index === account.index)
      if (!restoredAccount || restoredAccount.address !== account.address) {
        throw new Error(`restored account mismatch for wallet ${expectedRootAddress} account ${account.index}`)
      }
    }
  }

  console.log(JSON.stringify({
    network: 'production/testnet',
    storageMode: activation.status.storage_mode,
    accountStateSeq: activation.status.state_seq,
    pendingChanges: activation.status.pending_changes,
    preflightWalletCount: activation.preflight.wallets.length,
    initialCatalog: activation.initialCatalog,
    finalCatalog: activation.finalCatalog,
    recoveredWalletCount: restored.catalog.length,
    recoveredAccounts: restored.catalog.map((wallet) => ({ id: wallet.id, count: wallet.accounts.length })),
  }, null, 2))
}

main().then(() => process.exit(0)).catch((error) => {
  console.error(error)
  process.exit(1)
})
