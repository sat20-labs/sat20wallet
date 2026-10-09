import assert from 'node:assert/strict'
import { appendFileSync, existsSync, readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { createHash } from 'node:crypto'
import { dirname, join } from 'node:path'
import { chromium, expect } from '@playwright/test'
import { createServer } from 'vite'
import { runPwaUsageCases, runRgbPwaCases, runAutopayPwaReviewCases } from './account-management-usage-e2e.mjs'
import { requiredPwaBiometricCases, runPwaBiometricCases } from './wallet-biometric-e2e.mjs'
import { requiredPwaPosCases, optionalPwaEscapeCases, runPwaPosCases } from './pos-pwa-e2e.mjs'
import { requiredPwaL1ProtocolCases, runPwaL1ProtocolCases } from './wallet-l1-assets-e2e.mjs'
import { requiredPwaWalletBasicCases, runPwaWalletBasicCases } from './wallet-basic-e2e.mjs'
import { createPwaIntegrationDapp, requiredPwaIntegrationCases, runPwaIntegrationCases } from './wallet-integrations-e2e.mjs'
import { requiredPwaMiningCases, runPwaMiningCases } from './wallet-mining-e2e.mjs'
import { requiredPwaNodeCases, runPwaNodeCases } from './wallet-node-e2e.mjs'
import { requiredPwaToolsCases, runPwaToolsCases } from './wallet-tools-e2e.mjs'
import { requiredPwaMintCases, runPwaMintCases } from './wallet-mint-e2e.mjs'
import { requiredSDKWASMCases, runSDKWASMCases } from './sdk-wasm-e2e.mjs'

// Launched by sdk/e2e against its real temporary CoreNode. Inject only endpoint
// configuration; wallet, account, crypto, storage and RPC implementations are real.
const rgbFixture = process.argv[2] === '--rgb-fixture' ? JSON.parse(readFileSync(process.argv[3], 'utf8')) : null
const posFixture = process.argv[2] === '--pos-fixture' ? JSON.parse(readFileSync(process.argv[3], 'utf8')) : null
const sdkWASM = process.argv.includes('--sdk-wasm')
assert.ok(!sdkWASM || posFixture, 'SDK WASM gate requires the existing real-node fixture')
const config = posFixture?.config ?? rgbFixture?.config ?? JSON.parse(process.env.SAT20_ACCOUNT_E2E_CONFIG || 'null')
const storageWasm = process.argv[2] === '--storage-wasm' ? process.argv[3] : null
if (!storageWasm) {
  assert.ok(config?.IndexerL2?.Host, 'launch via go test ./e2e -run ^TestSDKAccountPWAConnectedBrowser$')
  assert.equal(config.Chain, 'testnet')
}
const nodeAPIOrigins = [...new Set([
  ...[config?.IndexerL1, config?.IndexerL2].filter(Boolean).map(endpoint => `${endpoint.Scheme}://${endpoint.Host}`),
  ...(config?.Peers || []).map(peer => new URL(peer.split('@').at(-1)).origin),
])]
for (const origin of nodeAPIOrigins) {
  assert.ok(['127.0.0.1', 'localhost'].includes(new URL(origin).hostname), 'fixture endpoints must be loopback')
}
const executable = process.env.SAT20_BROWSER_EXECUTABLE || [
  '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge',
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
].find(existsSync)
const integrationDapp = posFixture ? await createPwaIntegrationDapp() : null
const server = await createServer({
  server: { host: '127.0.0.1', port: posFixture?.browserPort ?? 0, strictPort: Boolean(posFixture?.browserPort), open: false },
  ...(integrationDapp ? { define: {
    'import.meta.env.VITE_SAT20_MARKET_URL': JSON.stringify(integrationDapp.url),
    'import.meta.env.VITE_SAT20_DAPP_ALLOWED_ORIGINS': JSON.stringify(integrationDapp.origin),
  } } : {}),
})
// SDK browser tests use the current SDK compiled into Go's temporary directory. The
// production loader still verifies the actual bytes, MIME, size and SHA-256.
const walletWasmPath = storageWasm ? null : posFixture?.wasmPath ?? process.env.SAT20_PWA_E2E_WASM
const walletRuntimePath = storageWasm ? null : posFixture?.wasmRuntimePath ?? process.env.SAT20_PWA_E2E_WASM_RUNTIME
assert.equal(Boolean(walletWasmPath), Boolean(walletRuntimePath), 'both current SDK WASM and its Go runtime are required')
const walletRuntimeAssets = walletWasmPath ? [
  { role: 'go-runtime', path: '__wallet_e2e__/wasm_exec.js', mimeTypes: ['text/javascript', 'application/javascript'], bytes: readFileSync(walletRuntimePath) },
  { role: 'wallet-wasm', path: '__wallet_e2e__/wallet.wasm', mimeTypes: ['application/wasm'], bytes: readFileSync(walletWasmPath) },
] : []
const walletRuntimeManifest = walletWasmPath ? {
  schemaVersion: 1,
  releaseId: `${JSON.parse(server.config.define.__SAT20_APP_VERSION__)}-${JSON.parse(server.config.define.__SAT20_BUILD_ID__)}`,
  assets: walletRuntimeAssets.map(({ bytes, ...asset }) => ({ ...asset, size: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') })),
} : null
let browser
// Explicit required cases also report later cases as not-run after an early failure.
const requiredAccountCases = [

  "usage: committed creation and import pages recover from a catalog read failure without replay",
  "usage: maintenance rehearsal obeys the one minute inactivity lock and discards its session",
  "usage: independent address DID entry persists changes and clearing through recovery",
  "usage: address DID persistence failure leaves catalog and recovery value unchanged",
  "usage: remote deletion consumes stale local edits after reload and publishes unrelated changes",
  "usage: configured account exposes maintenance and cancels without replacing recovery",
  "usage: configured recovery rehearsal previews real backup without committing or changing wallet",
  "usage: Guardian paid confirmation uses 100 records and root identity after a 200 record child-wallet quote",
  ...requiredPwaBiometricCases,
  "first wallet initializes account; PWA identity matches SDK",
  "IndexedDB failed operations remain unchanged after page restart",
  "preflight rejects wrong password without changing catalog",
  "storage choice can be resumed and canceled",
  "locked account writes and requests interrupted by lock cannot dispatch",
  "2of2 setup uses real WASM recovery session and rehearsal",
  "PWA adds and renames subaccount and preserves root identity",
  "root wallet cannot be deleted",
  "recovery cannot commit before preview; wrong knowledge is read only",
  "preview reads latest state; commit restores with new local password",
  "aborted recovery session cannot be reused",
  "invalid locator and user share leave an empty device recoverable",
  "manager release discards recovery preview and shares",
  "PWA root mnemonic import discovers latest account without recovery shares",
  "remote selected-wallet deletion refreshes PWA selection address and public key",
  "recovery page cancel aborts its WASM session without a reload",
  "PWA recovery page completes locator knowledge share preview and password steps",
  "independent Guardian accepts setup and owner verifies receipt",
  "2of3 rehearsal requires actual Guardian response",
  "2of3 recovery request response preview commit across devices",
  "2of3 knowledge and user share recover without Guardian",
  "2of3 PWA page recovers with user share and Guardian without knowledge",
  "PWA password change survives page reload and retains selection",
  "PWA offline reload unlocks persisted account and retains local wallet data",
  "network password prompt rejects wrong password and cancel preserves account",
  "locked remote deletion followed by offline unlock refreshes PWA identity from local SDK",
  "usage: SDK creation page delivers mnemonic without redundant PWA persistence",
  "usage: import page and cold selection use SDK without redundant PWA persistence",
  "usage: IndexedDB open failure recovers on explicit retry",
  "usage: PWA snapshot read failure preserves durable and displayed account state",
  "usage: cold startup SDK status read failure preserves selection and retries from page",
  "usage: browser password transaction failure preserves every wallet and original password",
  "usage: background catalog refresh cannot invalidate a subaccount switch",
  "usage: newly created account publishes first recovery package after initial sync settles",
  "usage: account page refreshes background backup failure and success without navigation",
  "usage: offline catalog edits survive reload and converge with another device",
  "usage: shared IndexedDB tabs invalidate pending writes when another tab locks",
  "usage: shared IndexedDB subaccount switch discards another tab previous-identity response",
  "usage: shared IndexedDB wallet switch survives another tab rebuilding before commit",
  "usage: shared IndexedDB password change cannot authorize an old-password tab",
  "usage: real 2of2 setup page validates answers and completes rehearsal",
  "usage: verified setup survives final status read failure without replaying rehearsal",
  "usage: same temporary mode reconfiguration converges across cold devices and root recovery",
  "usage: real 2of3 settings and independent Guardian pages complete setup and rehearsal",
  "usage: committed recovery with lost response survives reload without duplicate wallets",
  "usage: recovery commits once and reloads SDK data without redundant UI persistence",
  "usage: own Guardian contact from an earlier page is rejected by UI and high-level WASM",
  "usage: paid setup confirmation rejects invalid count and cancellation never pays",
  "usage: leaving a loaded recovery page aborts session and clears temporary material",
  "IndexedDB profile failure leaves updateWalletName unchanged",
  "IndexedDB profile failure leaves ensureAccount unchanged",
  "IndexedDB profile failure leaves updateAccountMetadata unchanged",
  "IndexedDB profile failure leaves createWallet unchanged",
  "IndexedDB profile failure leaves importWallet unchanged"
]
const requiredRGBCases = [
  "RGB PWA: AutopayFundingConfirmationMatchesEffectiveRate",
  "RGB PWA: same paid mode reconfiguration converges across cold devices and root recovery",
  "RGB PWA: submitted AUTOPAY funding survives timeout reload and repeated confirmation",
  "RGB PWA: cold remote import retry keeps unfinished local channel visible",
  "RGB PWA: single SDK recovery preserves ownership proofs and locks without redundant UI persistence",
  "RGB PWA: funded root reuses real AUTOPAY through settings without another funding",
  "RGB PWA: mnemonic page retries interrupted ownership import after reload",
  "RGB PWA: real provider storage failure survives reload and same-database recovery retry",
  "RGB PWA: page restores nonempty issued multiwallet ownership proofs and locks"
]
const completedCases = new Map()
const requiredWalletRuntimeCases = ['Wallet PWA: all feature groups finish without unhandled browser errors']
// Explicit local reruns use the same browser, WASM and node fixture. The
// ordinary Go gate remains the complete required-case ledger above.
const focusedUsageCases = process.argv[2] === '--usage-cases' ? process.argv.slice(3) : null
const walletCaseFlag = process.argv.indexOf('--wallet-cases')
const focusedWalletCases = walletCaseFlag >= 0 ? process.argv.slice(walletCaseFlag + 1) : null
if (focusedWalletCases) {
  assert.ok(posFixture && focusedWalletCases.length, 'wallet rerun requires the existing wallet fixture and exact cases')
  const known = sdkWASM ? requiredSDKWASMCases : [...requiredPwaPosCases, ...optionalPwaEscapeCases, ...requiredPwaL1ProtocolCases, ...requiredPwaWalletBasicCases, ...requiredPwaMintCases,
    ...requiredPwaMiningCases, ...requiredPwaNodeCases, ...requiredPwaIntegrationCases, ...requiredPwaToolsCases]
  assert.ok(focusedWalletCases.every(name => known.includes(name)), 'unknown wallet case')
  assert.equal(new Set(focusedWalletCases).size, focusedWalletCases.length)
}
if (focusedUsageCases) {
  assert.ok(focusedUsageCases.length > 0, 'focused rerun needs at least one case')
  assert.equal(new Set(focusedUsageCases).size, focusedUsageCases.length)
  assert.ok(focusedUsageCases.every(name => name.startsWith('usage: ') && [...requiredAccountCases, 'usage: temporary Guardian renews paid hosting without changing its own recovery', 'usage: original AUTOPAY resumes after prebroadcast exit and lost acknowledgement'].includes(name)), 'unknown usage case')
}
const verdicts = []
const pageErrors = []
const password = 'pwa-e2e-local-password'
const newPassword = 'pwa-e2e-new-password'
const questions = [
  { id: 'book', prompt: 'book phrase', answer: 'moonlight over the old bridge', confirmation: 'moonlight over the old bridge' },
  { id: 'note', prompt: 'private note', answer: 'yellow bicycle beside the river', confirmation: 'yellow bicycle beside the river' },
  { id: 'family', prompt: 'family phrase', answer: 'meet under the tree at six', confirmation: 'meet under the tree at six' },
]
const answers = questions.slice(0, 2).map(q => ({ question_id: q.id, answer: q.answer }))
// Parent Go gates buffer child stdout. Mirror only nonsensitive case progress
// to a private temporary log so a long cold-recovery case remains observable.
const caseProgressPath = join(tmpdir(), `sat20wallet-pwa-e2e-progress-${process.pid}.jsonl`)
const recordProgress = (name, status, duration_ms) => appendFileSync(caseProgressPath,
  JSON.stringify({ case: name, status, duration_ms, at: new Date().toISOString() }) + '\n', { mode: 0o600 })

async function check(name, action) {
  if (focusedUsageCases && !focusedUsageCases.includes(name)) return
  if (focusedWalletCases && !focusedWalletCases.includes(name)) return
  const startedAt = Date.now()
  const started_at = new Date(startedAt).toISOString()
  recordProgress(name, 'running', 0)
  console.log(JSON.stringify({ case: name, status: 'running', started_at, duration_ms: 0 }))
  try {
    await action()
  } catch (error) {
    completedCases.set(name, 'fail')
    recordProgress(name, 'fail', Date.now() - startedAt)
    console.error(JSON.stringify({ case: name, status: 'fail', started_at, duration_ms: Date.now() - startedAt, message: error.message }))
    for (const context of browser.contexts()) {
      for (const page of context.pages()) {
        if (page.isClosed()) continue
        // A blocked WASM callback can also block page evaluation. Keep the
        // original failure and let the remaining cases and cleanup proceed.
        let diagnosticTimer
        const state = await Promise.race([
          page.evaluate(() => ({
            headings: [...document.querySelectorAll('h1,h2')].map(el => el.textContent),
            alerts: [...document.querySelectorAll('[role="alert"]')].map(el => el.textContent),
          })).catch(() => null),
          new Promise(resolve => { diagnosticTimer = setTimeout(() => resolve(null), 2000) }),
        ]).finally(() => clearTimeout(diagnosticTimer))
        console.error(JSON.stringify({ url: page.url(), state }))
      }
    }
    throw error
  }
  completedCases.set(name, 'pass')
  recordProgress(name, 'pass', Date.now() - startedAt)
  verdicts.push(name)
  console.log(JSON.stringify({ case: name, status: 'pass', started_at, duration_ms: Date.now() - startedAt }))
}
async function ready(page) {
  await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__), undefined, { timeout: 90000 })
  await page.evaluate(async () => {
    const verify = window.__SAT20_PWA_VERIFY__
    assertNetwork(verify.useWalletStore().network)
    window.accountE2E = (await import('/utils/accountManagement.ts')).default
    function assertNetwork(network) {
      if (network !== 'testnet') throw new Error(`unexpected fixture network: ${network}`)
    }
  })
  const continueButton = page.getByRole('button', { name: 'Continue in browser', exact: true })
  if (await continueButton.isVisible()) await continueButton.click()
}
async function device(sharedContext) {
  const context = sharedContext ?? await browser.newContext()
  context.setDefaultTimeout(30000)
  context.setDefaultNavigationTimeout(90000)
  // A test-only endpoint module lets normal PWA startup use the fixture. No
  // remote service is contacted and each device has independent SDK and UI IndexedDB databases.
  if (!sharedContext) await context.route('**/config/wasm.ts*', route => route.fulfill({
    contentType: 'text/javascript',
    body: `import { walletStorage } from '/lib/walletStorage.ts';
      await walletStorage.initializeState();
      await walletStorage.setValue('network', 'testnet');
      export const logLevel = 2; export const getConfig = () => (${JSON.stringify(config)});`,
  }))
  if (!sharedContext) await context.route('**/*', route => {
    const url = new URL(route.request().url())
    if (['127.0.0.1', 'localhost'].includes(url.hostname)) return route.fallback()
    return route.abort('blockedbyclient')
  })
  if (!sharedContext && walletRuntimeManifest) {
    await context.route('**/generated/wasm-integrity.ts*', route => route.fulfill({
      contentType: 'text/javascript',
      body: `export const WASM_INTEGRITY_MANIFEST = ${JSON.stringify(walletRuntimeManifest)};`,
    }))
    for (const asset of walletRuntimeAssets) {
      await context.route(`**/${asset.path}*`, route => route.fulfill({
        contentType: asset.mimeTypes[0], body: asset.bytes,
      }))
    }
  }
  if (!sharedContext && posFixture) {
    // Asset lists use the PWA HTTP clients as well as WASM. Forward their
    // existing requests to the same SDK fixture; never fabricate an RPC body.
    await context.route('https://apiprd.ordx.market/**', async route => {
      const requested = new URL(route.request().url())
      let endpoint
      let suffix
      if (requested.pathname.startsWith('/btc/testnet/')) {
        endpoint = config.IndexerL1
        suffix = requested.pathname.slice('/btc/testnet/'.length)
      } else if (requested.pathname.startsWith('/satsnet/testnet/')) {
        endpoint = config.IndexerL2
        suffix = requested.pathname.slice('/satsnet/testnet/'.length)
      } else if (requested.pathname === '/testnet/ordx/GetRecommendedFees' || requested.pathname === '/ordx/GetBTCPrice') {
        endpoint = config.IndexerL1
        suffix = requested.pathname.replace(/^\/(?:testnet\/)?/, '')
      } else return route.abort('blockedbyclient')
      const proxy = String(endpoint.Proxy || 'testnet').replace(/^\/+|\/+$/g, '')
      const target = `${endpoint.Scheme}://${endpoint.Host}/${proxy}/${suffix}${requested.search}`
      const response = await route.fetch({ url: target, maxRedirects: 0, timeout: 30000 })
      const serving = new URL(server.resolvedUrls.local[0])
      const origin = new URL(route.request().headers().origin || serving.origin)
      assert.ok(origin.protocol === serving.protocol && origin.port === serving.port &&
        ['localhost', '127.0.0.1'].includes(origin.hostname), 'unexpected PWA request origin')
      await route.fulfill({ response, headers: { ...response.headers(), 'access-control-allow-origin': origin.origin } })
    })
  }
  const page = await context.newPage()
  page.on('pageerror', error => {
    pageErrors.push(error.message)
    console.error(`[PWA page error] ${error.message}`)
  })
  await page.goto(server.resolvedUrls.local[0], { waitUntil: 'domcontentloaded', timeout: 90000 })
  await ready(page)
  return page
}
async function walletCall(page, method, ...args) {
  return page.evaluate(async ({ method, args }) => {
    const [error, result] = await window.sat20[method](...args)
    if (error) throw new Error(`${method}: ${error.message}`)
    return result
  }, { method, args })
}
async function accountCall(page, method, ...args) {
  return page.evaluate(({ method, args }) => window.accountE2E[method](...args), { method, args })
}
async function unlock(page, credential = password) {
  await page.evaluate(async credential => {
    const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().unlockWallet(credential)
    if (error) throw error
  }, credential)
}
async function catalog(page) {
  return (await walletCall(page, 'getWalletCatalog')).wallets
}

async function runIndexedDBStorageGate() {
  // Run the Go KVDB regressions against Chromium's real IndexedDB, using the
  // same Playwright/Vite runner as the account acceptance suite.
  const page = await browser.newPage()
  page.on('console', message => console.log(message.text()))
  page.on('pageerror', error => pageErrors.push(error.message))
  await page.route('**/__sdk_storage_tests__', route => route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>SDK storage tests</title>' }))
  await page.goto(`${server.resolvedUrls.local[0]}__sdk_storage_tests__`)
  await page.addScriptTag({ path: join(dirname(storageWasm), 'wasm_exec.js') })
  await page.route('**/__sdk_storage_tests__.wasm', route => route.fulfill({ contentType: 'application/wasm', body: readFileSync(storageWasm) }))
  const code = await page.evaluate(async () => {
    const go = new Go()
    go.argv = ['storage.test', '-test.v', '-test.timeout=2m']
    let exitCode
    go.exit = code => { exitCode = code }
    const bytes = await (await fetch('/__sdk_storage_tests__.wasm')).arrayBuffer()
    const { instance } = await WebAssembly.instantiate(bytes, go.importObject)
    await go.run(instance)
    return exitCode
  })
  assert.equal(code, 0, 'Go WASM IndexedDB tests failed')
  assert.deepEqual(pageErrors, [], 'storage tests produced unhandled page errors')
}

async function runAccountGate() {
  const owner = await device()
  let root
  let rootMnemonic
  await check('first wallet initializes account; PWA identity matches SDK', async () => {
    const [error, mnemonic] = await owner.evaluate(async credential => {
      const [error, mnemonic] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(credential)
      return [error?.message, mnemonic]
    }, password)
    assert.equal(error, undefined)
    rootMnemonic = mnemonic
    root = (await catalog(owner))[0]
    const status = await accountCall(owner, 'status')
    assert.equal(status.active, true)
    assert.equal(String(status.root_wallet_id), String(root.id))
    assert.equal(status.account_id, root.accounts[0].account_id)
  })
  for (const [method, args] of [
    ['updateWalletName', [String(root.id), 'Rejected name']],
    ['ensureAccount', [String(root.id), 1, 'Rejected account']],
    ['updateAccountMetadata', [String(root.id), 0, 'Rejected metadata']],
    ['createWallet', [password]],
    ['importWallet', ['comfort very add tuition senior run eight snap burst appear exile dutch', password]],
  ]) {
    await check(`IndexedDB profile failure leaves ${method} unchanged`, async () => {
      const observed = await owner.evaluate(async ({ method, args }) => {
        const digest = async () => {
          const db = await new Promise((resolve, reject) => {
            const request = indexedDB.open('sat20-wallet-sdk', 1)
            request.onsuccess = () => resolve(request.result)
            request.onerror = () => reject(request.error)
          })
          try {
            const records = await new Promise((resolve, reject) => {
              const transaction = db.transaction('kv', 'readonly')
              const store = transaction.objectStore('kv')
              const keys = store.getAllKeys()
              const values = store.getAll()
              transaction.oncomplete = () => resolve(keys.result.flatMap((key, index) =>
                String(key).includes('wallet-id-') || String(key).endsWith('account-management-profile-v2')
                  ? [[key, values.result[index]]] : []))
              transaction.onabort = () => reject(transaction.error)
            })
            const hash = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify(records)))
            return [...new Uint8Array(hash)].map(byte => byte.toString(16).padStart(2, '0')).join('')
          } finally { db.close() }
        }
        const before = await digest()
        const prototype = IDBObjectStore.prototype
        const put = prototype.put
        let injected = false
        prototype.put = function(value, key) {
          if (this.name === 'kv' && String(key).endsWith('account-management-profile-v2')) {
            injected = true
            throw new DOMException('injected account profile failure', 'QuotaExceededError')
          }
          return put.call(this, value, key)
        }
        let failure
        try {
          const [error] = await window.sat20[method](...args)
          failure = error?.message
        } finally { prototype.put = put }
        return { injected, failure, unchanged: before === await digest() }
      }, { method, args })
      assert.equal(observed.injected, true)
      assert.match(observed.failure, /injected account profile failure/)
      assert.equal(observed.unchanged, true, 'failed API changed durable wallet/profile records')
      assert.deepEqual(await catalog(owner), [root], 'failed API changed the live catalog')
    })
  }
  await check('IndexedDB failed operations remain unchanged after page restart', async () => {
    await owner.reload()
    await ready(owner)
    await unlock(owner)
    assert.deepEqual(await catalog(owner), [root])
  })
  await check('preflight rejects wrong password without changing catalog', async () => {
    await assert.rejects(accountCall(owner, 'preflight', 'incorrect', []))
    assert.deepEqual(await catalog(owner), [root])
    assert.equal((await accountCall(owner, 'preflight', password, [])).account_id, root.accounts[0].account_id)
  })
  await check('storage choice can be resumed and canceled', async () => {
    const options = (await accountCall(owner, 'getStorageOptions')).options
    assert.ok(options.some(o => o.id === 'temporary' && o.available))
    await accountCall(owner, 'confirmStorage', 'temporary')
    assert.ok((await accountCall(owner, 'resumePendingStorageAuthorization')).id)
    await accountCall(owner, 'cancelPendingStorageAuthorization')
    assert.equal(await accountCall(owner, 'resumePendingStorageAuthorization'), null)
    await assert.rejects(accountCall(owner, 'confirmStorage', 'paid', 99))
  })
  await check('locked account writes and requests interrupted by lock cannot dispatch', async () => {
    const result = await owner.evaluate(async credential => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      // The facade awaits its real operation log before dispatching. Lock in
      // that interval, then verify no storage authorization was created.
      const pending = window.accountE2E.confirmStorage('temporary').then(() => '', error => error.message)
      await wallet.lockWallet()
      const interrupted = await pending
      const funding = await window.accountE2E.fundAutopay().then(() => '', error => error.message)
      const confirmation = await window.accountE2E.confirmStorage('temporary').then(() => '', error => error.message)
      const [error] = await wallet.unlockWallet(credential)
      if (error) throw error
      return { interrupted, funding, confirmation, resumed: await window.accountE2E.resumePendingStorageAuthorization() }
    }, password)
    assert.match(result.interrupted, /session changed|unlocked/)
    assert.match(result.funding, /unlocked/)
    assert.match(result.confirmation, /unlocked/)
    assert.equal(result.resumed, null)
  })
  let two
  await check('2of2 setup uses real WASM recovery session and rehearsal', async () => {
    await accountCall(owner, 'confirmStorage', 'temporary')
    two = await accountCall(owner, 'createRecovery', { password, wallets: [], recovery_mode: '2of2', questions })
    await assert.rejects(accountCall(owner, 'rehearse', two.session_id, answers, 'invalid-share', password))
    assert.equal((await accountCall(owner, 'status')).recovery_configured, false)
    assert.equal((await accountCall(owner, 'rehearse', two.session_id, answers, two.user_share, password)).verified, true)
    assert.equal((await accountCall(owner, 'status')).recovery_configured, true)
    await assert.rejects(accountCall(owner, 'rehearse', two.session_id, answers, two.user_share, password))
  })
  await check('PWA adds and renames subaccount and preserves root identity', async () => {
    await owner.evaluate(async () => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      await wallet.addAccount('Savings', 1)
      await wallet.updateAccountName(1, 'Travel')
      await wallet.updateWalletName(wallet.walletId, 'PWA Root')
    })
    root = (await catalog(owner))[0]
    assert.equal(root.name, 'PWA Root')
    assert.equal(root.accounts[1].name, 'Travel')
    assert.equal((await accountCall(owner, 'status')).account_id, root.accounts[0].account_id)
  })
  await check('root wallet cannot be deleted', async () => {
    const failure = await owner.evaluate(async id => {
      const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().deleteWallet(id)
      return error?.message
    }, String(root.id))
    assert.match(failure, /Root wallet cannot be deleted/)
    await assert.rejects(walletCall(owner, 'deleteWallet', String(root.id)))
    assert.deepEqual(await catalog(owner), [root])
  })
  const recovery = await device()
  let loaded
  await check('recovery cannot commit before preview; wrong knowledge is read only', async () => {
    loaded = await accountCall(recovery, 'loadRecovery', two.locator)
    await assert.rejects(accountCall(recovery, 'commitRecovery', loaded.session_id, newPassword))
    await assert.rejects(accountCall(recovery, 'recoverKnowledge', loaded.session_id, answers.map(a => ({ ...a, answer: 'incorrect' }))))
    assert.deepEqual(await catalog(recovery), [])
    await accountCall(recovery, 'recoverKnowledge', loaded.session_id, answers)
    await accountCall(recovery, 'setUserShare', loaded.session_id, two.user_share)
  })
  await check('preview reads latest state; commit restores with new local password', async () => {
    // Background synchronization must finish the metadata edits above. Poll
    // the real preview, never substitute a decoded SDK state in the browser.
    let preview
    for (let i = 0; i < 60; i++) {
      preview = await accountCall(recovery, 'previewRecovery', loaded.session_id)
      if (preview.summary.wallets[0].name === 'PWA Root') break
      await new Promise(resolve => setTimeout(resolve, 500))
    }
    assert.equal(preview.summary.wallets[0].name, 'PWA Root')
    const committed = await accountCall(recovery, 'commitRecovery', loaded.session_id, newPassword)
    assert.equal(committed.account_id, root.accounts[0].account_id)
    await assert.rejects(accountCall(recovery, 'commitRecovery', loaded.session_id, newPassword))
    await recovery.reload()
    await ready(recovery)
    await assert.rejects(walletCall(recovery, 'unlockWallet', password))
    await unlock(recovery, newPassword)
    assert.equal((await catalog(recovery))[0].accounts[1].name, 'Travel')
  })
  await check('aborted recovery session cannot be reused', async () => {
    const session = await accountCall(recovery, 'loadRecovery', two.locator)
    await accountCall(recovery, 'abortSession', session.session_id)
    await assert.rejects(accountCall(recovery, 'recoverKnowledge', session.session_id, answers))
  })
  await check('invalid locator and user share leave an empty device recoverable', async () => {
    const empty = await device()
    try {
      await assert.rejects(accountCall(empty, 'loadRecovery', 'not-an-account-locator'))
      assert.deepEqual(await catalog(empty), [])
      const session = await accountCall(empty, 'loadRecovery', two.locator)
      await accountCall(empty, 'recoverKnowledge', session.session_id, answers)
      await assert.rejects(accountCall(empty, 'setUserShare', session.session_id, 'not-a-recovery-share'))
      await assert.rejects(accountCall(empty, 'previewRecovery', session.session_id))
      await assert.rejects(accountCall(empty, 'commitRecovery', session.session_id, newPassword))
      assert.deepEqual(await catalog(empty), [])
      assert.equal((await accountCall(empty, 'status')).active, false)
      // Rejected input must not poison the session or require clearing local browser storage.
      await accountCall(empty, 'setUserShare', session.session_id, two.user_share)
      await accountCall(empty, 'previewRecovery', session.session_id)
      await accountCall(empty, 'commitRecovery', session.session_id, newPassword)
      assert.equal((await catalog(empty))[0].fingerprint, root.fingerprint)
    } finally {
      await empty.context().close()
    }
  })
  await check('manager release discards recovery preview and shares', async () => {
    const sessionDevice = await device()
    const session = await accountCall(sessionDevice, 'loadRecovery', two.locator)
    await accountCall(sessionDevice, 'recoverKnowledge', session.session_id, answers)
    await accountCall(sessionDevice, 'setUserShare', session.session_id, two.user_share)
    await accountCall(sessionDevice, 'previewRecovery', session.session_id)
    await walletCall(sessionDevice, 'release')
    await walletCall(sessionDevice, 'init', config, 2)
    await assert.rejects(accountCall(sessionDevice, 'commitRecovery', session.session_id, newPassword))
    await assert.rejects(accountCall(sessionDevice, 'previewRecovery', session.session_id))
    assert.deepEqual(await catalog(sessionDevice), [])
    await sessionDevice.context().close()
  })
  const imported = await device()
  await check('PWA root mnemonic import discovers latest account without recovery shares', async () => {
    const error = await imported.evaluate(async ({ mnemonic, credential }) => {
      const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().importWallet(mnemonic, credential)
      return error?.message
    }, { mnemonic: rootMnemonic, credential: newPassword })
    assert.equal(error, undefined)
    assert.equal((await accountCall(imported, 'status')).account_id, root.accounts[0].account_id)
    assert.equal((await catalog(imported))[0].accounts[1].name, 'Travel')
    await imported.reload()
    await ready(imported)
    await unlock(imported, newPassword)
    assert.equal((await catalog(imported))[0].name, 'PWA Root')
  })
  await check('remote selected-wallet deletion refreshes PWA selection address and public key', async () => {
    await owner.evaluate(async credential => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      const [error] = await wallet.createWallet(credential)
      if (error) throw error
      await wallet.addAccount('Child savings', 1)
    }, password)
    const child = (await catalog(owner)).find(wallet => wallet.fingerprint !== root.fingerprint)
    await assert.rejects(walletCall(owner, 'updateWalletName', String(child.id), 'PWA Root'))
    await assert.rejects(walletCall(owner, 'ensureAccount', String(child.id), 1024, 'Too many'))
    for (const index of [-1, 0.5, 2 ** 32, 2 ** 32 - 1]) {
      await assert.rejects(walletCall(owner, 'updateAccountMetadata', String(child.id), index, 'Invalid index'))
    }
    await recovery.waitForFunction(fingerprint => {
      const catalog = window.__SAT20_PWA_VERIFY__.useWalletStore().wallets
      return catalog.some(wallet => wallet.fingerprint === fingerprint && wallet.accounts.length === 2)
    }, child.fingerprint, { timeout: 90000 })
    await recovery.evaluate(async fingerprint => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      await wallet.switchWallet(wallet.wallets.find(item => item.fingerprint === fingerprint).id)
      await wallet.switchToAccount(1)
    }, child.fingerprint)
    await owner.evaluate(async id => {
      const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().deleteWallet(id)
      if (error) throw error
    }, String(child.id))
    await recovery.waitForFunction(fingerprint => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      return wallet.wallets.length === 1 && wallet.wallet?.fingerprint === fingerprint && wallet.accountIndex === 0
        && wallet.address === wallet.wallet.accounts[0].address && wallet.publicKey === wallet.wallet.accounts[0].pubKey
    }, root.fingerprint, { timeout: 90000 })
    const identity = await recovery.evaluate(() => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      return { address: wallet.address, publicKey: wallet.publicKey }
    })
    assert.equal(identity.address, (await walletCall(recovery, 'getWalletAddress', 0)).address)
    assert.equal(identity.publicKey, (await walletCall(recovery, 'getWalletPubkey', 0)).pubKey)
    await recovery.reload()
    await ready(recovery)
    await unlock(recovery, newPassword)
    assert.equal(await recovery.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().accountIndex), 0)
  })
  await check('recovery page cancel aborts its WASM session without a reload', async () => {
    const canceled = await device()
    await canceled.evaluate(async () => {
      const load = window.accountE2E.loadRecovery.bind(window.accountE2E)
      window.accountE2E.loadRecovery = async locator => {
        const result = await load(locator)
        window.canceledRecoverySession = result.session_id
        return result
      }
      const { default: router } = await import('/router/index.ts')
      return router.push('/restore-account')
    })
    await canceled.getByPlaceholder('粘贴 sat20account1:...').fill(two.locator)
    await canceled.getByRole('button', { name: '加载加密账户备份', exact: true }).click()
    await canceled.getByText('回答私人知识问题', { exact: true }).waitFor()
    const id = await canceled.evaluate(() => window.canceledRecoverySession)
    await canceled.getByRole('button', { name: '取消', exact: true }).click()
    await canceled.waitForFunction(() => new URL(location.href).hash !== '#/restore-account')
    await assert.rejects(accountCall(canceled, 'recoverKnowledge', id, answers))
    await canceled.context().close()
  })
  const uiRecovery = await device()
  await check('PWA recovery page completes locator knowledge share preview and password steps', async () => {
    await uiRecovery.goto(`${server.resolvedUrls.local[0]}#/restore-account`, { waitUntil: 'domcontentloaded', timeout: 90000 })
    await ready(uiRecovery)
    await uiRecovery.getByPlaceholder('粘贴 sat20account1:...').fill(two.locator)
    await uiRecovery.getByRole('button', { name: '加载加密账户备份', exact: true }).click()
    for (const question of questions.slice(0, 2)) {
      await uiRecovery.getByText(question.prompt, { exact: true }).locator('..').locator('input').fill(question.answer)
    }
    await uiRecovery.getByRole('button', { name: '恢复加密分片', exact: true }).click()
    await uiRecovery.getByPlaceholder('可选：粘贴 sat20share1:...').fill(two.user_share)
    await uiRecovery.getByRole('button', { name: '使用用户分片', exact: true }).click()
    await uiRecovery.getByRole('button', { name: '预览恢复内容', exact: true }).click()
    await uiRecovery.getByText('PWA Root', { exact: true }).waitFor()
    await uiRecovery.getByPlaceholder('设置新的本地钱包密码').fill(newPassword)
    await uiRecovery.getByPlaceholder('再次输入密码').fill(newPassword)
    await Promise.all([
      uiRecovery.waitForEvent('domcontentloaded', { timeout: 90000 }),
      uiRecovery.getByRole('button', { name: '恢复全部钱包', exact: true }).click(),
    ])
    assert.equal(new URL(uiRecovery.url()).hash, '#/unlock')
    await ready(uiRecovery)
    await unlock(uiRecovery, newPassword)
    assert.equal((await catalog(uiRecovery))[0].accounts[1].name, 'Travel')
    assert.equal((await accountCall(uiRecovery, 'status')).account_id, root.accounts[0].account_id)
  })
  const guardian = await device()
  await guardian.evaluate(async credential => {
    const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(credential)
    if (error) throw error
  }, password)
  let three
  await check('independent Guardian accepts setup and owner verifies receipt', async () => {
    const identity = await accountCall(guardian, 'guardianIdentity', password)
    const contact = JSON.parse(identity.contact)
    assert.notEqual(contact.mailbox_id, root.accounts[0].account_id)
    await accountCall(owner, 'confirmStorage', 'temporary')
    three = await accountCall(owner, 'createRecovery', { password, wallets: [], recovery_mode: '2of3', questions, guardian: contact })
    await accountCall(guardian, 'confirmStorage', 'temporary')
    await assert.rejects(accountCall(guardian, 'acceptGuardianSetup', password, '{}'))
    const receipt = await accountCall(guardian, 'acceptGuardianSetup', password, three.guardian_setup)
    await assert.rejects(accountCall(owner, 'checkGuardianSetup', three.session_id, '{}'))
    const verified = await accountCall(owner, 'checkGuardianSetup', three.session_id, receipt.receipt)
    three.locator = verified.locator
  })
  await check('2of3 rehearsal requires actual Guardian response', async () => {
    await assert.rejects(accountCall(owner, 'rehearse', three.session_id, answers, '', password))
    const request = await accountCall(owner, 'createGuardianRequest', three.session_id)
    await assert.rejects(accountCall(guardian, 'createGuardianResponse', 'incorrect', request.request))
    const response = await accountCall(guardian, 'createGuardianResponse', password, request.request)
    await accountCall(owner, 'consumeGuardianResponse', three.session_id, response.response)
    assert.equal((await accountCall(owner, 'rehearse', three.session_id, answers, '', password)).verified, true)
  })
  const guardianRecovery = await device()
  await check('2of3 recovery request response preview commit across devices', async () => {
    const session = await accountCall(guardianRecovery, 'loadRecovery', three.locator)
    await accountCall(guardianRecovery, 'recoverKnowledge', session.session_id, answers)
    const request = await accountCall(guardianRecovery, 'createGuardianRequest', session.session_id)
    const response = await accountCall(guardian, 'createGuardianResponse', password, request.request)
    await accountCall(guardianRecovery, 'consumeGuardianResponse', session.session_id, response.response)
    const preview = await accountCall(guardianRecovery, 'previewRecovery', session.session_id)
    assert.equal(preview.summary.account_id, root.accounts[0].account_id)
    const committed = await accountCall(guardianRecovery, 'commitRecovery', session.session_id, newPassword)
    assert.equal(committed.account_id, root.accounts[0].account_id)
    assert.equal((await catalog(guardianRecovery))[0].name, 'PWA Root')
  })
  await check('2of3 knowledge and user share recover without Guardian', async () => {
    const pairDevice = await device()
    const session = await accountCall(pairDevice, 'loadRecovery', three.locator)
    await accountCall(pairDevice, 'recoverKnowledge', session.session_id, answers)
    await accountCall(pairDevice, 'setUserShare', session.session_id, three.user_share)
    await accountCall(pairDevice, 'previewRecovery', session.session_id)
    assert.equal((await accountCall(pairDevice, 'commitRecovery', session.session_id, newPassword)).account_id, root.accounts[0].account_id)
    await pairDevice.context().close()
  })
  await check('2of3 PWA page recovers with user share and Guardian without knowledge', async () => {
    const pairDevice = await device()
    await pairDevice.goto(`${server.resolvedUrls.local[0]}#/restore-account`, { waitUntil: 'domcontentloaded' })
    await ready(pairDevice)
    await pairDevice.getByPlaceholder('粘贴 sat20account1:...').fill(three.locator)
    await pairDevice.getByRole('button', { name: '加载加密账户备份', exact: true }).click()
    await pairDevice.getByRole('button', { name: '使用用户分片和 Guardian', exact: true }).click()
    await pairDevice.getByPlaceholder('可选：粘贴 sat20share1:...').fill(three.user_share)
    await pairDevice.getByRole('button', { name: '使用用户分片', exact: true }).click()
    assert.equal(await pairDevice.getByRole('button', { name: '预览恢复内容', exact: true }).isDisabled(), true)
    await pairDevice.getByRole('button', { name: '生成 Guardian 恢复请求', exact: true }).click()
    const request = await pairDevice.locator('textarea[readonly]').inputValue()
    const response = await accountCall(guardian, 'createGuardianResponse', password, request)
    await pairDevice.getByPlaceholder('粘贴 Guardian 返回的加密响应').fill(response.response)
    await pairDevice.getByRole('button', { name: '使用 Guardian 响应', exact: true }).click()
    await pairDevice.getByRole('button', { name: '预览恢复内容', exact: true }).click()
    await pairDevice.getByPlaceholder('设置新的本地钱包密码').fill(newPassword)
    await pairDevice.getByPlaceholder('再次输入密码').fill(newPassword)
    await Promise.all([
      pairDevice.waitForEvent('domcontentloaded', { timeout: 90000 }),
      pairDevice.getByRole('button', { name: '恢复全部钱包', exact: true }).click(),
    ])
    await ready(pairDevice)
    await unlock(pairDevice, newPassword)
    assert.equal((await catalog(pairDevice))[0].accounts[1].name, 'Travel')
    await pairDevice.context().close()
  })
  await check('PWA password change survives page reload and retains selection', async () => {
    await walletCall(owner, 'changePassword', password, newPassword)
    await owner.reload()
    await ready(owner)
    await assert.rejects(walletCall(owner, 'unlockWallet', password))
    await unlock(owner, newPassword)
    assert.equal((await accountCall(owner, 'status')).account_id, root.accounts[0].account_id)
    assert.equal((await catalog(owner))[0].name, 'PWA Root')
  })
  await check('PWA offline reload unlocks persisted account and retains local wallet data', async () => {
    const before = await catalog(owner)
    const identity = (await accountCall(owner, 'status')).account_id
    const endpoints = nodeAPIOrigins.map(origin => `${origin}/**`)
    const unavailable = route => route.abort('failed')
    for (const endpoint of endpoints) await owner.context().route(endpoint, unavailable)
    try {
      // Keep PWA assets available while indexer and CoreNode APIs are unreachable.
      // Reload discards the WASM Manager; encrypted wallet data and the DKVS
      // replica remain in this device's SDK IndexedDB.
      await owner.reload()
      await ready(owner)
      await assert.rejects(walletCall(owner, 'unlockWallet', password))
      await unlock(owner, newPassword)
      assert.deepEqual(await catalog(owner), before)
      const status = await accountCall(owner, 'status')
      assert.equal(status.account_id, identity)
      assert.equal(status.recovery_configured, true)
      assert.equal((await accountCall(owner, 'preflight', newPassword, [])).account_id, identity)
    } finally {
      for (const endpoint of endpoints) await owner.context().unroute(endpoint, unavailable)
    }
    assert.equal((await accountCall(owner, 'status')).account_id, identity)
  })
  await check('network password prompt rejects wrong password and cancel preserves account', async () => {
    const before = await catalog(owner)
    const switching = owner.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().setNetwork('mainnet'))
    const dialog = owner.getByRole('dialog')
    await dialog.locator('input[type="password"]').fill('incorrect')
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await dialog.getByRole('alert').waitFor()
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    assert.equal(await switching, false)
    assert.deepEqual(await catalog(owner), before)
    assert.equal(await owner.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().network), 'testnet')
    assert.equal((await accountCall(owner, 'status')).account_id, root.accounts[0].account_id)
  })
  // Keep this strict regression last, after completing the other replicas.
  await check('locked remote deletion followed by offline unlock refreshes PWA identity from local SDK', async () => {
    // Earlier recovery cases have their own replicas. Finish those devices so
    // this boundary is exercised by exactly the owner and the locked replica.
    for (const page of [imported, uiRecovery, guardianRecovery]) await page.context().close()
    await owner.evaluate(async credential => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      const [error] = await wallet.createWallet(credential)
      if (error) throw error
      await wallet.addAccount('Locked deletion savings', 1)
    }, newPassword)
    const child = (await catalog(owner)).find(wallet => wallet.fingerprint !== root.fingerprint)
    assert.ok(child, 'fixture must create a non-root wallet')
    await recovery.waitForFunction(fingerprint => {
      return window.__SAT20_PWA_VERIFY__.useWalletStore().wallets
        .some(wallet => wallet.fingerprint === fingerprint && wallet.accounts.length === 2)
    }, child.fingerprint, { timeout: 90000 })
    await recovery.evaluate(async fingerprint => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      await wallet.switchWallet(wallet.wallets.find(item => item.fingerprint === fingerprint).id)
      await wallet.switchToAccount(1)
    }, child.fingerprint)
    const beforeLock = await recovery.evaluate(async () => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      const session = await import('/lib/walletSession.ts')
      return { walletId: wallet.walletId, accountIndex: wallet.accountIndex, runtimeRunning: session.isWalletRuntimeUnlocked() }
    })
    assert.equal(beforeLock.accountIndex, 1)
    assert.equal(beforeLock.runtimeRunning, true, 'exercise lock with a running SDK')
    // A stale UI selection can remain in the catalog while the SDK has already
    // selected another wallet. Unlock must adopt its authenticated current ID,
    // rather than treating existence of the old catalog entry as confirmation.
    const replicaRoot = (await catalog(recovery)).find(wallet => wallet.fingerprint === root.fingerprint)
    await walletCall(recovery, 'switchWallet', String(replicaRoot.id), '')
    await recovery.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().lockWallet())
    await unlock(recovery, newPassword)
    const adopted = await recovery.evaluate(() => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      return { id: wallet.walletId, index: wallet.accountIndex, count: wallet.wallets.length,
        address: wallet.address, pubKey: wallet.publicKey }
    })
    assert.equal(adopted.id, String(replicaRoot.id))
    assert.equal(adopted.index, 0)
    assert.equal(adopted.count, 2, 'adoption must work while the previous wallet still exists')
    assert.equal(adopted.address, replicaRoot.accounts[0].address)
    assert.equal(adopted.pubKey, replicaRoot.accounts[0].pub_key)
    await recovery.evaluate(async fingerprint => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      await wallet.switchWallet(wallet.wallets.find(item => item.fingerprint === fingerprint).id)
      await wallet.switchToAccount(1)
    }, child.fingerprint)
    await recovery.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().lockWallet())
    await owner.evaluate(async id => {
      const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().deleteWallet(id)
      if (error) throw error
    }, String(child.id))
    // Local rebase can remove a wallet before the corresponding PUT/ACK is
    // complete. Cut APIs only after the owner confirms the deletion and the
    // replica adopts that confirmed revision with no pending local overlay.
    // waitForFunction treats a returned Promise as truthy. Poll the awaited
    // result so offline starts only after both real SDKs confirm the deletion.
    // The SDK omits pending_changes when zero; its public TS type is optional.
    await expect.poll(() => owner.evaluate(async () => {
      const status = await window.accountE2E.status()
      return (status.pending_changes ?? 0) === 0 && !status.managed_data_dirty
    }), { timeout: 90000 }).toBe(true)
    const deletionRevision = (await accountCall(owner, 'status')).state_seq
    await expect.poll(() => recovery.evaluate(async ({ fingerprint, revision }) => {
      const [error, result] = await window.sat20.getWalletCatalog()
      const status = await window.accountE2E.status()
      return !error && status.state_seq >= revision &&
        (status.pending_changes ?? 0) === 0 && !status.managed_data_dirty &&
        result.wallets.length === 1 && !result.wallets.some(wallet => wallet.fingerprint === fingerprint)
    }, { fingerprint: child.fingerprint, revision: deletionRevision }), { timeout: 90000 }).toBe(true)
    assert.equal(await recovery.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), true)
    // Let the deletion event finish while locked, then take only the node APIs
    // offline. Unlock must reconcile persisted local state without another RPC.
    await new Promise(resolve => setTimeout(resolve, 1500))
    const endpoints = nodeAPIOrigins.map(origin => `${origin}/**`)
    const unavailable = route => route.abort('failed')
    for (const endpoint of endpoints) await recovery.context().route(endpoint, unavailable)
    try {
      await unlock(recovery, newPassword)
      const observed = await recovery.evaluate(async () => {
        const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
        const [catalogError, sdkCatalog] = await window.sat20.getWalletCatalog()
        const [addressError, sdkAddress] = await window.sat20.getWalletAddress(0)
        const [keyError, sdkKey] = await window.sat20.getWalletPubkey(0)
        if (catalogError || addressError || keyError) throw catalogError || addressError || keyError
        return { locked: wallet.locked, pwaWalletId: wallet.walletId, sdkRootId: String(sdkCatalog.wallets[0].id),
          pwaWalletCount: wallet.wallets.length, sdkWalletCount: sdkCatalog.wallets.length, accountIndex: wallet.accountIndex,
          rootFingerprint: sdkCatalog.wallets[0].fingerprint,
          addressMatches: wallet.address === sdkAddress.address, publicKeyMatches: wallet.publicKey === sdkKey.pubKey }
      })
      console.log(JSON.stringify({ case: 'locked deletion offline unlock', observation: observed }))
      assert.equal(observed.locked, false)
      assert.equal(observed.sdkWalletCount, 1)
      assert.equal(observed.rootFingerprint, root.fingerprint)
      assert.equal(observed.pwaWalletId, observed.sdkRootId, 'unlock retained a deleted selected wallet')
      assert.equal(observed.pwaWalletCount, observed.sdkWalletCount)
      assert.equal(observed.accountIndex, 0)
      assert.equal(observed.addressMatches, true)
      assert.equal(observed.publicKeyMatches, true)
    } finally {
      for (const endpoint of endpoints) await recovery.context().unroute(endpoint, unavailable)
    }
  })
  try {
    await runPwaBiometricCases({ check, device, ready, walletCall }, { basicWallet: {
      mnemonic: rootMnemonic, password, address: (await walletCall(owner, 'validateMnemonic', rootMnemonic, '')).address,
    } })
  } catch (error) {
    console.error(JSON.stringify({ suite: 'runPwaBiometricCases', status: 'fail', message: error.message }))
    process.exitCode = 1
  }
  await runPwaUsageCases({ check, device, ready, unlock, catalog, walletCall, accountCall,
    password, newPassword, questions, answers, rootMnemonic, nodeAPIOrigins, material: three })
  assert.deepEqual(pageErrors, [], 'PWA must not produce unhandled page errors')
  console.log(JSON.stringify({ passed: true, cases: verdicts.length, backend: 'real local SatoshiNet', browser: 'real PWA and WASM' }))
}

try {
  await server.listen()
  browser = await chromium.launch({ headless: true, ...(executable ? { executablePath: executable } : {}) })
  if (storageWasm) await runIndexedDBStorageGate()
  else if (sdkWASM) {
    await runSDKWASMCases({ check, device, pageErrors }, posFixture)
  }
  else if (posFixture) {
    const helpers = { check, device, ready, unlock, catalog, walletCall, accountCall, nodeAPIOrigins, integrationDapp, selectedCases: focusedWalletCases }
    // Independent feature groups use fresh browser stores and continue after
    // another group fails; the required-case ledger still fails the whole gate.
    for (const [run, names] of [[runPwaPosCases, [...requiredPwaPosCases, ...optionalPwaEscapeCases]], [runPwaL1ProtocolCases, requiredPwaL1ProtocolCases], [runPwaWalletBasicCases, requiredPwaWalletBasicCases],
      [runPwaIntegrationCases, requiredPwaIntegrationCases], [runPwaToolsCases, requiredPwaToolsCases],
      [runPwaMintCases, requiredPwaMintCases], [runPwaMiningCases, requiredPwaMiningCases],
      [runPwaNodeCases, requiredPwaNodeCases]]) {
      if (focusedWalletCases && !names.some(name => focusedWalletCases.includes(name))) continue
      try { await run(helpers, posFixture) }
      catch (error) {
        console.error(JSON.stringify({ suite: run.name, status: 'fail', message: error.message }))
        if (focusedWalletCases) throw error
        process.exitCode = 1
      }
    }
    if (focusedWalletCases) assert.deepEqual(pageErrors, [], 'focused wallet rerun must not produce unhandled page errors')
    await check(requiredWalletRuntimeCases[0], async () => {
      assert.deepEqual(pageErrors, [], 'Wallet PWA must not produce unhandled page errors')
    })
  }
  else if (rgbFixture) {
    await runRgbPwaCases({ check, device, ready, unlock, catalog, walletCall, accountCall }, rgbFixture)
    assert.deepEqual(pageErrors, [], 'RGB recovery must not produce unhandled page errors')
  }
  else if (focusedUsageCases && process.env.SAT20_PWA_AUTOPAY_REVIEW) {
    await runAutopayPwaReviewCases({ check, device, ready, unlock, catalog, walletCall, accountCall, questions, answers }, JSON.parse(process.env.SAT20_PWA_AUTOPAY_REVIEW))
    assert.deepEqual(pageErrors, [], 'paid Guardian review must not produce unhandled page errors')
  }
  else if (focusedUsageCases) {
    await runPwaUsageCases({ check, device, ready, unlock, catalog, walletCall, accountCall,
      password, newPassword, questions, answers, nodeAPIOrigins })
    assert.deepEqual(pageErrors, [], 'focused account rerun must not produce unhandled page errors')
  }
  else await runAccountGate()
} finally {
  const required = storageWasm ? [] : sdkWASM ? focusedWalletCases ?? requiredSDKWASMCases : posFixture ? focusedWalletCases ?? [...requiredPwaPosCases, ...requiredPwaL1ProtocolCases, ...requiredPwaWalletBasicCases, ...requiredPwaIntegrationCases, ...requiredPwaToolsCases, ...requiredPwaMintCases, ...requiredPwaMiningCases, ...requiredPwaNodeCases, ...requiredWalletRuntimeCases] : rgbFixture ? requiredRGBCases : focusedUsageCases ?? requiredAccountCases
  for (const name of required) {
    if (!completedCases.has(name)) console.log(JSON.stringify({ case: name, status: 'not-run' }))
  }
  const complete = required.every(name => completedCases.get(name) === 'pass')
  const summary = { required: required.length, pass: [...completedCases.values()].filter(status => status === 'pass').length,
    fail: [...completedCases.values()].filter(status => status === 'fail').length,
    not_run: required.filter(name => !completedCases.has(name)).length }
  if (required.length) console.log(JSON.stringify({ caseSummary: summary, scope: focusedUsageCases || focusedWalletCases ? 'focused-rerun' : 'full-gate' }))
  await browser?.close()
  await server.close()
  await integrationDapp?.close()
  if (required.length && !complete) process.exitCode = 1
  assert.equal(new Set(required).size, required.length, 'required case names must be unique')
  assert.ok([...completedCases.keys()].every(name => required.includes(name)), 'required case registry is incomplete')
}
