import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { chromium } from '@playwright/test'
import { createServer } from 'vite'

// Launched by sdk/e2e against its real temporary CoreNode. Inject only endpoint
// configuration; wallet, account, crypto, storage and RPC implementations are real.
const config = JSON.parse(process.env.SAT20_ACCOUNT_E2E_CONFIG || 'null')
assert.ok(config?.IndexerL2?.Host, 'launch via SAT20_RUN_PWA_E2E=1 go test ./e2e -run ^TestSDKAccountPWAConnectedBrowser$')
assert.equal(config.Chain, 'testnet')
for (const endpoint of [config.IndexerL1, config.IndexerL2]) {
  assert.ok(['127.0.0.1', 'localhost'].includes(new URL(`${endpoint.Scheme}://${endpoint.Host}`).hostname), 'fixture endpoints must be loopback')
}
const executable = process.env.SAT20_BROWSER_EXECUTABLE || [
  '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge',
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
].find(existsSync)
const server = await createServer({ server: { host: '127.0.0.1', port: 0, open: false } })
let browser
const verdicts = []
const password = 'pwa-e2e-local-password'
const newPassword = 'pwa-e2e-new-password'
const questions = [
  { id: 'book', prompt: 'book phrase', answer: 'moonlight over the old bridge', confirmation: 'moonlight over the old bridge' },
  { id: 'note', prompt: 'private note', answer: 'yellow bicycle beside the river', confirmation: 'yellow bicycle beside the river' },
  { id: 'family', prompt: 'family phrase', answer: 'meet under the tree at six', confirmation: 'meet under the tree at six' },
]
const answers = questions.slice(0, 2).map(q => ({ question_id: q.id, answer: q.answer }))

async function check(name, action) {
  console.log(JSON.stringify({ case: name, status: 'running' }))
  try {
    await action()
  } catch (error) {
    for (const context of browser.contexts()) {
      for (const page of context.pages()) {
        if (page.isClosed()) continue
        const state = await page.evaluate(() => ({
          headings: [...document.querySelectorAll('h1,h2')].map(el => el.textContent),
          alerts: [...document.querySelectorAll('[role="alert"]')].map(el => el.textContent),
        })).catch(() => null)
        console.error(JSON.stringify({ url: page.url(), state }))
      }
    }
    throw error
  }
  verdicts.push(name)
  console.log(JSON.stringify({ case: name, status: 'pass' }))
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
async function device() {
  const context = await browser.newContext()
  context.setDefaultTimeout(30000)
  context.setDefaultNavigationTimeout(90000)
  // A test-only endpoint module lets normal PWA startup use the fixture. No
  // remote service is contacted and each device has its own real IndexedDB.
  await context.route('**/config/wasm.ts*', route => route.fulfill({
    contentType: 'text/javascript',
    body: `import { walletStorage } from '/lib/walletStorage.ts';
      await walletStorage.initializeState();
      await walletStorage.setValue('network', 'testnet');
      export const logLevel = 2; export const getConfig = () => (${JSON.stringify(config)});`,
  }))
  await context.route('**/*', route => {
    const url = new URL(route.request().url())
    if (['127.0.0.1', 'localhost'].includes(url.hostname)) return route.fallback()
    return route.abort('blockedbyclient')
  })
  const page = await context.newPage()
  page.on('pageerror', error => console.error(`[PWA page error] ${error.message}`))
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

try {
  await server.listen()
  browser = await chromium.launch({ headless: true, ...(executable ? { executablePath: executable } : {}) })
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
    const endpoints = [...new Set([config.IndexerL1, config.IndexerL2].map(endpoint =>
      `${endpoint.Scheme}://${endpoint.Host}/**`))]
    const unavailable = route => route.abort('failed')
    for (const endpoint of endpoints) await owner.context().route(endpoint, unavailable)
    try {
      // Keep PWA assets available while both real node APIs are unreachable.
      // Reload discards the WASM Manager; encrypted wallet data and the DKVS
      // replica remain in this device's actual IndexedDB.
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
  console.log(JSON.stringify({ passed: true, cases: verdicts.length, backend: 'real local SatoshiNet', browser: 'real PWA and WASM' }))
} finally {
  await browser?.close()
  await server.close()
}
