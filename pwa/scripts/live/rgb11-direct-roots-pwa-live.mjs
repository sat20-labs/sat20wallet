import { chromium } from '@playwright/test'
import fs from 'node:fs'

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const SENDER_URL = process.env.SAT20_RGB11_SENDER_PWA_URL || 'http://localhost:5173/#/'
const RECEIVER_URL = process.env.SAT20_RGB11_RECEIVER_PWA_URL || 'http://127.0.0.1:5173/#/'
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456'
const RESET_STORAGE = process.env.SAT20_RESET_TEST_STORAGE === '1'
const LOCK_FILE = '/private/tmp/sat20-rgb11-direct-roots-pwa-live.lock'
const SENDER = {
  mnemonic: 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire',
  address: 'tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g',
}
const RECEIVER = {
  mnemonic: 'comfort very add tuition senior run eight snap burst appear exile dutch',
  address: 'tb1p6rk7tq5avpjmpudgut4vkhda5m8eetlzpqd6mrcr6u2022tdwfssfsra5x',
}
const ISSUE_AMOUNT = '100'
const TRANSFER_AMOUNT = '10'

const acquireLock = () => {
  try {
    const pid = Number(fs.readFileSync(LOCK_FILE, 'utf8'))
    if (Number.isInteger(pid) && pid > 0) {
      try {
        process.kill(pid, 0)
        throw new Error(`RGB11 Direct root E2E is already running as PID ${pid}`)
      } catch (error) {
        if (error?.code !== 'ESRCH') throw error
      }
    }
    fs.unlinkSync(LOCK_FILE)
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error
  }
  fs.writeFileSync(LOCK_FILE, String(process.pid), { flag: 'wx', mode: 0o600 })
  const release = () => {
    try {
      if (fs.readFileSync(LOCK_FILE, 'utf8') === String(process.pid)) fs.unlinkSync(LOCK_FILE)
    } catch {}
  }
  process.once('exit', release)
  process.once('SIGINT', () => process.exit(130))
  process.once('SIGTERM', () => process.exit(143))
}

const primeTestnet = async (page, url) => {
  await page.goto(new URL('/manifest.webmanifest', url).href, { waitUntil: 'domcontentloaded' })
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
  await page.goto(url, { waitUntil: 'domcontentloaded' })
  await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__), null, { timeout: 180_000 })
}

// A CDP-connected browser may still contain pages from a previous run.  A
// Storage.clearDataForOrigin call only removes persisted browser data; it does
// not stop the Go/WASM Manager (or its background DKVS jobs) living in those
// pages.  Close every page for the test origins before clearing storage so an
// old manager cannot race the fresh manager and delete its active recovery
// record.
const closeOriginPages = async (context, urls) => {
  const origins = new Set(urls.map((url) => new URL(url).origin))
  for (const page of context.pages()) {
    let origin = ''
    try {
      origin = new URL(page.url()).origin
    } catch {
      continue
    }
    if (origins.has(origin)) await page.close().catch(() => {})
  }
}

const callPage = (page, action, payload = {}) => page.evaluate(async ({ action, payload }) => {
  const verify = window.__SAT20_PWA_VERIFY__
  if (!verify) throw new Error('PWA verification API is unavailable')
  const wallet = verify.useWalletStore()
  const { Chain, Network, rgb11Address, sat20, walletStorage } = verify
  const unwrap = (tuple, operation) => {
    if (tuple?.[0]) throw new Error(`${operation}: ${tuple[0].message || String(tuple[0])}`)
    return tuple?.[1]
  }
  const state = async () => JSON.parse((await unwrap(await sat20.getRGB11State(), 'getRGB11State')).state)

  if (action === 'import-root') {
    await walletStorage.initializeState()
    await walletStorage.setValue('env', 'prd')
    await walletStorage.setValue('network', 'testnet')
    await walletStorage.setValue('chain', 'btc')
    const [unlockError] = await wallet.unlockWallet(payload.password)
    if (unlockError && !/no wallet/i.test(String(unlockError.message || unlockError))) throw unlockError
    await wallet.setNetwork(Network.TESTNET)
    await wallet.setChain(Chain.BTC)
    const [importError] = await wallet.importWallet(payload.mnemonic, payload.password)
    if (importError) throw importError
    const catalog = await wallet.syncWalletCatalog()
    const root = catalog.find((item) => item.accounts.some((account) => (
      Number(account.index) === 0 && account.accountId === wallet.rootAccountId
    )))
    if (!root) throw new Error('imported account-management root is missing from the catalog')
    const rootAccount = root.accounts.find((account) => Number(account.index) === 0)
    if (!rootAccount?.address || rootAccount.address !== payload.address) {
      throw new Error(`root address ${rootAccount?.address || 'missing'} does not match ${payload.address}`)
    }
    await wallet.switchWallet(root.id)
    await wallet.switchToAccount(0)
    await wallet.setChain(Chain.BTC)
    await unwrap(await sat20.switchAccount(0), 'switchAccount')
    return { walletId: root.id, accountId: rootAccount.accountId, address: rootAccount.address }
  }
  if (action === 'state') return state()
  if (action === 'issue') {
    const response = await unwrap(await sat20.issueRGB11Asset({
      schema: 'NIA', ticker: payload.ticker, name: `SAT20 RGB ${payload.ticker}`,
      precision: 0, amounts: [payload.issueAmount], min_confirmations: 1,
    }), 'issueRGB11Asset')
    return JSON.parse(response.result)
  }
  if (action === 'import-contract') {
    if (!payload.armor) return { projected: 0 }
    const response = await unwrap(await sat20.importRGB11Contract(payload.armor), 'importRGB11Contract')
    return JSON.parse(response.result)
  }
  if (action === 'enable-receive') {
    const response = await unwrap(await rgb11Address.enableReceive({}), 'enableRGB11AddressReceive')
    return JSON.parse(response.endpoint)
  }
  if (action === 'prepare') {
    const response = await unwrap(await rgb11Address.prepareTransfer({
      receiver_address: payload.receiverAddress,
      asset_name: payload.assetName,
      amount_raw: payload.transferAmount,
      fee_rate: 1,
      min_confirmations: 1,
    }), 'prepareRGB11AddressTransfer')
    return JSON.parse(response.transfer)
  }
  if (action === 'deliver') {
    return unwrap(await rgb11Address.deliverAndBroadcast({ transfer_id: payload.transferId }),
      'deliverAndBroadcastRGB11AddressTransfer')
  }
  if (action === 'sync-mailbox') {
    const response = await unwrap(await rgb11Address.syncMailbox({}), 'syncRGB11AddressMailbox')
    return JSON.parse(response.result)
  }
  if (action === 'refresh') {
    const [error] = await sat20.refreshRGB11State()
    if (error && !/witness is unresolved|outpoint status is unknown/i.test(String(error.message || error))) {
      throw error
    }
    return state()
  }
  throw new Error(`unsupported page action ${action}`)
}, { action, payload })

const waitReady = async (page, label) => {
  const deadline = Date.now() + 180_000
  let latest
  while (Date.now() < deadline) {
    latest = await callPage(page, 'state')
    if (latest?.initialized === true && latest?.sync_status === 'idle' &&
      ['ok', 'warning'].includes(latest?.consistency_status)) return latest
    if (latest?.sync_status === 'error' || latest?.consistency_status === 'broken') {
      throw new Error(`${label} RGB11 state failed: ${JSON.stringify(latest)}`)
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000))
  }
  throw new Error(`${label} RGB11 state did not become ready: ${JSON.stringify(latest)}`)
}

const assetAmount = (state, field, name) => String((state?.[field] || []).find((asset) => (
  `${asset?.Name?.Protocol || ''}:${asset?.Name?.Type || ''}:${asset?.Name?.Ticker || ''}` === name
))?.Amount?.Value ?? '0')

async function main() {
  acquireLock()
  if (new URL(SENDER_URL).origin === new URL(RECEIVER_URL).origin) {
    throw new Error('sender and receiver PWA origins must be different account-management domains')
  }
  const launch = CDP === 'launch'
  const browser = launch ? await chromium.launch({ headless: true }) : await chromium.connectOverCDP(CDP)
  const context = launch ? await browser.newContext() : browser.contexts()[0] || await browser.newContext()
  if (RESET_STORAGE) await closeOriginPages(context, [SENDER_URL, RECEIVER_URL])
  const senderPage = await context.newPage()
  const receiverPage = await context.newPage()
  for (const [label, page] of [['sender', senderPage], ['receiver', receiverPage]]) {
    page.on('console', (message) => {
      if (message.type() === 'error' || message.text().includes('RGB11')) {
        console.error(`[${label}:${message.type()}] ${message.text()}`)
      }
    })
    page.on('pageerror', (error) => console.error(`[${label}:pageerror] ${error.message}`))
  }
  try {
    if (RESET_STORAGE) {
      for (const [page, url] of [[senderPage, SENDER_URL], [receiverPage, RECEIVER_URL]]) {
        const session = await context.newCDPSession(page)
        await session.send('Storage.clearDataForOrigin', {
          origin: new URL(url).origin,
          storageTypes: 'all',
        })
      }
    }
    await Promise.all([
      primeTestnet(senderPage, SENDER_URL),
      primeTestnet(receiverPage, RECEIVER_URL),
    ])
    const [senderRoot, receiverRoot] = await Promise.all([
      callPage(senderPage, 'import-root', { ...SENDER, password: PASSWORD }),
      callPage(receiverPage, 'import-root', { ...RECEIVER, password: PASSWORD }),
    ])
    if (!senderRoot.accountId || !receiverRoot.accountId || senderRoot.accountId === receiverRoot.accountId) {
      throw new Error('Direct E2E requires two distinct account-management root identities')
    }
    await Promise.all([waitReady(senderPage, 'sender'), waitReady(receiverPage, 'receiver')])

    const ticker = `R${Date.now().toString(36).slice(-7)}`.toUpperCase()
    const issued = await callPage(senderPage, 'issue', { ticker, issueAmount: ISSUE_AMOUNT })
    const assetName = `${issued.asset_name.Protocol}:${issued.asset_name.Type}:${issued.asset_name.Ticker}`
    await callPage(receiverPage, 'import-contract', { armor: issued.armor })
    const endpoint = await callPage(receiverPage, 'enable-receive')
    if (endpoint.account_id !== receiverRoot.accountId || endpoint.address !== receiverRoot.address) {
      throw new Error(`receiver endpoint is not bound to its root: ${JSON.stringify(endpoint)}`)
    }

    const prepared = await callPage(senderPage, 'prepare', {
      receiverAddress: receiverRoot.address,
      assetName,
      transferAmount: TRANSFER_AMOUNT,
    })
    const transferId = prepared?.state?.transfer_id
    if (!transferId || !prepared?.state?.address_mode) throw new Error('invalid Direct transfer preparation')
    const senderAfterPrepare = await callPage(senderPage, 'state')
    const locallyPrepared = (senderAfterPrepare.transfers || []).find((item) => item?.transfer_id === transferId)
    if (!locallyPrepared) {
      throw new Error(`prepared Direct transfer is missing from the active sender scope: ${transferId}`)
    }
    const firstDelivery = await callPage(senderPage, 'deliver', { transferId })
    if (!firstDelivery.awaiting_ack || firstDelivery.broadcast || firstDelivery.txid) {
      throw new Error(`Direct transfer crossed ACK boundary: ${JSON.stringify(firstDelivery)}`)
    }

    const mailboxDeadline = Date.now() + 10 * 60_000
    let received
    while (Date.now() < mailboxDeadline) {
      received = await callPage(receiverPage, 'sync-mailbox')
      if (received.received > 0 || received.already_done > 0) break
      await new Promise((resolve) => setTimeout(resolve, 2_000))
    }
    if (!received || (received.received === 0 && received.already_done === 0)) {
      throw new Error(`receiver mailbox did not persist consignment: ${JSON.stringify(received)}`)
    }

    const ackDeadline = Date.now() + 10 * 60_000
    let broadcast
    while (Date.now() < ackDeadline) {
      await callPage(senderPage, 'sync-mailbox')
      broadcast = await callPage(senderPage, 'deliver', { transferId })
      if (broadcast.broadcast && broadcast.txid) break
      if (!broadcast.awaiting_ack) throw new Error(`unexpected Direct delivery state: ${JSON.stringify(broadcast)}`)
      await new Promise((resolve) => setTimeout(resolve, 2_000))
    }
    if (!broadcast?.broadcast || !broadcast.txid) {
      throw new Error(`sender did not broadcast after ACK: ${JSON.stringify(broadcast)}`)
    }

    const receiverState = await callPage(receiverPage, 'refresh')
    if (assetAmount(receiverState, 'assets', assetName) !== TRANSFER_AMOUNT) {
      throw new Error(`receiver RGB11 projection is missing ${assetName}`)
    }
    const senderState = await callPage(senderPage, 'refresh')
    console.log(JSON.stringify({
      transferTransport: 'address',
      assetName,
      transferId,
      txid: broadcast.txid,
      senderRoot,
      receiverRoot,
      received,
      senderAmount: assetAmount(senderState, 'assets', assetName),
      receiverAmount: assetAmount(receiverState, 'assets', assetName),
    }, null, 2))
  } finally {
    await Promise.all([senderPage.close(), receiverPage.close()])
    if (launch) await browser.close()
  }
}

main().then(() => process.exit(0)).catch((error) => {
  console.error(error)
  process.exit(1)
})
