import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { transformSync } from 'esbuild'
import * as vue from 'vue'
import { tryit } from 'radash'
import { BoundedApprovalQueue } from '../../lib/approval-queue.ts'
import * as grantModel from '../../lib/dapp-grant-model.ts'

const storeSource = readFileSync(new URL('../../store/wallet.ts', import.meta.url), 'utf8')
const wrapperSource = readFileSync(new URL('../../utils/sat20.ts', import.meta.url), 'utf8')
const sessionSource = readFileSync(new URL('../../lib/walletSession.ts', import.meta.url), 'utf8')
const createSource = readFileSync(new URL('../../entrypoints/popup/pages/Create.vue', import.meta.url), 'utf8')
const importSource = readFileSync(new URL('../../entrypoints/popup/pages/Import.vue', import.meta.url), 'utf8')
const unlockSource = readFileSync(new URL('../../entrypoints/popup/pages/Unlock.vue', import.meta.url), 'utf8')
const validationSource = readFileSync(new URL('../../utils/validation.ts', import.meta.url), 'utf8')
const englishLocale = JSON.parse(readFileSync(new URL('../../locales/en.json', import.meta.url), 'utf8'))
const chineseLocale = JSON.parse(readFileSync(new URL('../../locales/zh.json', import.meta.url), 'utf8'))

test('wallet password is not exposed as Pinia state', () => {
  assert.doesNotMatch(storeSource, /const\s+password\s*=\s*ref\s*\(/)
  const returnedStore = storeSource.slice(storeSource.lastIndexOf('\n  return {'))
  assert.doesNotMatch(returnedStore, /^\s*password,\s*$/m)
	assert.doesNotMatch(returnedStore, /^\s*(getSessionPassword|setPassword),\s*$/m)
	assert.doesNotMatch(sessionSource, /walletSessionPassword|SessionPassword/)
	assert.doesNotMatch(`${createSource}\n${importSource}\n${unlockSource}`, /hashPassword|utils\/crypto/)
	assert.match(createSource, /createWallet\(values\.password\)/)
	assert.match(importSource, /importWallet\([\s\S]*values\.password/)
	assert.match(unlockSource, /unlockWallet\(values\.password\)/)
	assert.doesNotMatch(sessionSource, /from\s+['"]pinia|from\s+['"].*walletStorage|\bdefineStore\s*\(|\bref\s*\(/)
})

function loadModule(path, dependencies = {}, globals = {}) {
  const source = readFileSync(new URL(path, import.meta.url), 'utf8')
  return loadSource(source, dependencies, globals)
}

function loadSource(source, dependencies = {}, globals = {}) {
  const code = transformSync(source, { loader: 'ts', format: 'cjs', target: 'es2022' }).code
  const module = { exports: {} }
  runInNewContext(code, {
    module, exports: module.exports, Error, crypto, TextEncoder, structuredClone,
    setTimeout, clearTimeout, console: { log() {}, warn() {}, error() {} },
    require(name) {
      if (!(name in dependencies)) throw new Error(`Unexpected dependency: ${name}`)
      return dependencies[name]
    },
    ...globals,
  })
  return module.exports
}

function walletSessionFixture() {
  const calls = [], handlers = {}, listeners = []
  const state = { locked: false, hasWallet: true, walletId: '1', accountIndex: 0,
    rootAccountId: 'root-account', address: 'address', pubkey: 'pubkey', network: 'testnet', chain: 'btc',
    accountRecovery: { status: 'found', code: 'recovered', rootAccountId: 'root-account', network: 'testnet', env: 'prd' },
    wallets: [{ id: '1', name: 'Wallet 1', fingerprint: 'wallet-fingerprint', accounts: [{
      index: 0, name: 'Account 1', address: 'address', pubKey: 'pubkey', accountId: 'root-account',
    }] }] }
  const walletStorage = {
    getValue: (key) => state[key], getState: () => state,
    initializeState: async () => {}, subscribe: (fn) => listeners.push(fn),
    async setValue(key, value) {
      if (key === 'locked' && handlers.persistLocked) await handlers.persistLocked(value)
      const old = state[key]
      state[key] = value
      for (const listener of listeners) listener(key, value, old)
    },
    async batchUpdate(values) {
      for (const [key, value] of Object.entries(values)) await this.setValue(key, value)
    },
  }
  const identity = loadModule('../../lib/identity-boundary.ts')
  const session = loadModule('../../lib/walletSession.ts', { './identity-boundary': identity })
  session.setWalletRuntimeUnlocked(true)
  session.setWalletSessionUnlocked(true)
  const passwordPrompt = loadModule('../../lib/walletPasswordPrompt.ts', {
    './identity-boundary': identity, './walletSession': session,
  })
  const grants = loadModule('../../lib/authorized-origins.ts', {
    './storage-adapter': { Storage: { get: async () => ({ value: null }), set: async () => {} } },
    './walletStorage': { walletStorage }, './identity-boundary': identity,
    './dapp-grant-model': grantModel,
  })
  const wasm = new Proxy({}, { get: (_target, name) => async (...args) => {
    calls.push([name, ...args])
    if (handlers[name]) return handlers[name](...args)
    if (name === 'unlockWallet' && args[0] !== 'correct-password') {
      return { code: -1, msg: 'password is incorrect' }
    }
    const data = name === 'getWalletCatalog' ? { wallets: [{
      id: '1', name: 'Wallet 1', fingerprint: 'wallet-fingerprint', accounts: [{
        index: 0, name: 'Account 1', did: '', address: 'address', pub_key: 'pubkey', account_id: 'root-account',
      }],
    }] }
      : name === 'recoverAccountManagementFromCurrentWallet'
        ? { status: 'found', code: 'recovered', walletId: '1', accountId: 'root-account' }
      : name === 'getWalletAddress' ? { address: 'address' }
        : name === 'getWalletPubkey' ? { pubKey: 'pubkey' }
          : { walletId: '1', signature: 'signed', psbt: 'signed' }
    return { code: 0, data }
  } })
  const logs = { beginPwaWalletOperation: async () => {}, finishPwaOperation: async () => {} }
  const dependencies = {
    radash: { tryit }, '@/lib/walletSession': session, '@/utils/pwaOperationLog': logs,
  }
  const walletManager = loadModule('../../utils/sat20.ts', dependencies, { sat20wallet_wasm: wasm })
  const stp = loadModule('../../utils/stp.ts', dependencies, { sat20wallet_wasm: wasm })
  const messageTypes = loadModule('../../types/message.ts')
  const typeEnums = loadModule('../../types/index.ts')
  const approve = loadModule('../../store/approve.ts', {
    vue, '@/types/message': messageTypes, '@/types': typeEnums,
    '@/lib/approval-queue': { BoundedApprovalQueue }, '@/lib/identity-boundary': identity,
    '@/lib/walletSession': session,
  }).useApproveStore()
  const store = loadModule('../../store/wallet.ts', {
    pinia: { defineStore: (_name, factory) => factory }, vue,
    '@/lib/walletStorage': { walletStorage }, '@/types': { Chain: { BTC: 'btc', SATNET: 'satnet' } },
    '@/utils/sat20': walletManager, '@/utils/stp': stp,
    './channel': { useChannelStore: () => ({ invalidateCurrentChannel() {}, getCurrentChannel: async () => {} }) },
    '@/lib/utils': { sendNetworkChangedEvent() {}, sendAccountsChangedEvent() {} },
    '@/config/wasm': { getConfig: (_env, network) => ({ network }) }, '@/lib/accountSwitchChannel': { awaitAccountChannelRefresh: (fn) => fn() },
    '@/lib/walletSession': session, '@/lib/identity-boundary': identity, '@/lib/authorized-origins': grants,
    '@/lib/walletPasswordPrompt': passwordPrompt,
    '@/utils/accountManagement': { __esModule: true, default: {
      status: async () => ({ active: true, account_id: 'root-account' }),
    } },
  }).useWalletStore()
  return { store, session, identity, grants, approve, calls, handlers, logs, state, passwordPrompt,
    wallet: walletManager.default, stp: stp.default }
}

test('UI lock keeps the runtime, clears credentials/grants and rejects approvals', async () => {
  const h = walletSessionFixture()
  const scope = await h.grants.getCurrentDappScope()
  const grant = { ...scope, origin: 'https://app.example', createdAt: Date.now(),
    expiresAt: Date.now() + 60_000, capabilities: ['accounts:read'], sessionOnly: true }
  await h.grants.createDappGrant(grant)
  const pending = h.approve.showApprove({ action: 'signMessage', metadata: { origin: grant.origin } })
  const cancelled = assert.rejects(pending, /pending approvals were cancelled/)
  await h.store.lockWallet()
  await cancelled
  assert.equal(h.session.isWalletRuntimeUnlocked(), true)
  assert.equal(h.session.isWalletSessionUnlocked(), false)
  assert.equal(h.state.locked, true)
  assert.equal(h.approve.queueSize.value, 0)
  assert.equal(h.approve.isVisible.value, false)
  assert.equal(await h.grants.getMatchingDappGrant(grant.origin, scope, 'accounts:read'), null)
  assert.throws(() => h.identity.assertWalletIdentityReady(scope.identityGeneration))
  await assert.rejects(h.approve.showApprove({ action: 'signMessage' }), /must be unlocked/)
  assert.equal(h.calls.length, 0, 'UI lock must not call SDK lock/release/stop')
  const [error] = await h.store.unlockWallet('wrong-password')
  assert.match(error.message, /password is incorrect/)
  assert.equal(h.state.locked, true)
  assert.equal(h.session.isWalletSessionUnlocked(), false)
})

test('100 UI lock/unlock cycles only re-authenticate the existing runtime', async () => {
  const h = walletSessionFixture()
  for (let i = 0; i < 100; i++) {
    const oldGeneration = h.identity.assertWalletIdentityReady()
    await h.store.lockWallet()
    const [error, result] = await h.store.unlockWallet('correct-password')
    assert.equal(error, undefined)
    assert.equal(result.walletId, '1')
    assert.equal(h.session.isWalletRuntimeUnlocked(), true)
    assert.equal(h.session.isWalletSessionUnlocked(), true)
    assert.equal(h.state.locked, false)
    assert.ok(h.identity.assertWalletIdentityReady() > oldGeneration)
    assert.throws(() => h.identity.assertWalletIdentityReady(oldGeneration), /generation changed/)
  }
  assert.deepEqual(h.calls.map(([name]) => name), Array(100).fill('unlockWallet'))
})

test('locked PWA facades refuse signing, export and channel mutation but allow explicit release', async () => {
  const h = walletSessionFixture()
  await h.store.lockWallet()
  for (const [facade, method, args] of [
    [h.wallet, 'signMessage', ['message']], [h.wallet, 'signData', ['data']],
    [h.wallet, 'signPsbt', ['aabb', false]], [h.wallet, 'signPsbt_SatsNet', ['aabb', false]],
    [h.wallet, 'signPsbts', [['aabb']]], [h.wallet, 'getMnemonice', ['1', 'correct-password']],
    [h.wallet, 'sendAssets', ['address', 'asset', '1', '1']],
    [h.stp, 'commitmentExport', ['channel']], [h.stp, 'sweepBuild', ['channel']],
    [h.stp, 'openChannel', ['1', '1']],
  ]) {
    const [error, result] = await facade[method](...args)
    assert.match(error.message, /must be unlocked/, method)
    assert.equal(result, undefined)
  }
  assert.equal(h.calls.length, 0)
  assert.equal((await h.wallet.release())[0], undefined)
  assert.equal(h.calls[0][0], 'release')
})

test('lock during operation logging prevents a signature from being requested', async () => {
  const h = walletSessionFixture()
  let resume
  h.logs.beginPwaWalletOperation = () => new Promise(resolve => { resume = resolve })
  const pending = h.wallet.signMessage('message')
  await h.store.lockWallet()
  resume()
  const [error, result] = await pending
  assert.match(error.message, /session changed/)
  assert.equal(result, undefined)
  assert.equal(h.calls.length, 0)
})

test('lock invalidates in-flight signatures and unlock responses even after re-unlock', async () => {
  for (const method of ['signMessage', 'unlockWallet']) {
    const h = walletSessionFixture()
    let resume
    h.handlers[method] = () => new Promise(resolve => { resume = resolve })
    const pending = method === 'unlockWallet'
      ? h.store.unlockWallet('correct-password') : h.wallet.signMessage('message')
    await new Promise(resolve => setImmediate(resolve))
    await h.store.lockWallet()
    delete h.handlers[method]
    assert.equal((await h.store.unlockWallet('correct-password'))[0], undefined)
    resume({ code: 0, data: { walletId: '1', signature: 'stale' } })
    const [error, result] = await pending
    assert.match(error.message, /session changed/)
    assert.equal(result, undefined)
    assert.equal(h.state.locked, false)
  }
})

test('cold unlock still initializes the account and already-unlocked errors are not authorization', async () => {
  const h = walletSessionFixture()
  await h.store.lockWallet()
  h.session.setWalletRuntimeUnlocked(false)
  assert.equal((await h.store.unlockWallet('correct-password'))[0], undefined)
  assert.equal(h.state.locked, false)
  h.identity.assertWalletIdentityReady()
  assert.ok(h.calls.some(([name]) => name === 'switchAccount'))
  for (const handler of [
    () => ({ code: -1, msg: 'wallet has been unlocked' }),
    () => { throw new Error('wallet has been unlocked') },
  ]) {
    h.handlers.unlockWallet = handler
    const [error, result] = await h.wallet.unlockWallet('wrong-password')
    assert.match(error.message, /wallet has been unlocked/)
    assert.equal(result, undefined)
  }
  assert.doesNotMatch(wrapperSource, /_isAlreadyUnlocked|async lockWallet/)
})

test('lock during an unlock storage write cannot reopen the UI', async () => {
  const h = walletSessionFixture()
  await h.store.lockWallet()
  let resume
  h.handlers.persistLocked = (locked) => locked ? undefined : new Promise(resolve => { resume = resolve })
  const pending = h.store.unlockWallet('correct-password')
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(typeof resume, 'function')
  await h.store.lockWallet()
  resume()
  const [error, result] = await pending
  assert.match(error.message, /session changed/)
  assert.equal(result, undefined)
  assert.equal(h.state.locked, true)
  assert.equal(h.session.isWalletSessionUnlocked(), false)
})

test('PWA network switching recreates the manager', () => {
	assert.doesNotMatch(storeSource, /walletManager\.switchChain\s*\(/)
	const setNetworkBody = storeSource.match(/const setNetwork = async \(value: Network\) => \{([\s\S]*?)\n  const setChain/)?.[1] || ''
	const release = setNetworkBody.indexOf('walletManager.release()')
	const init = setNetworkBody.indexOf('walletManager.init(targetConfig, logLevel)')
	assert.ok(release >= 0, 'setNetwork must release the old manager')
	assert.ok(init > release, 'setNetwork must initialize the target manager after release')
})

test('wallet password copy matches the approved six-character minimum', () => {
	assert.match(validationSource, /passwordSchema\s*=\s*z\.string\(\)\s*\.min\(6,/)
	assert.equal(englishLocale.create.passwordDescription, 'Password must be at least 6 characters')
	assert.equal(chineseLocale.create.passwordDescription, '密码必须至少包含 6 个字符')
})

function passwordDialogFixture(h) {
  const source = readFileSync(new URL('../../components/common/WalletPasswordDialog.vue', import.meta.url), 'utf8')
    .split('<script setup lang="ts">')[1].split('</script>')[0]
  const cleanup = []
  const route = vue.reactive({ fullPath: '/wallet' })
  const dialog = loadSource(`${source}\nexport { open, busy, error, passwordInput, confirm, finish }`, {
    vue: { ...vue, onBeforeUnmount: (fn) => cleanup.push(fn) },
    'vue-router': { useRoute: () => route }, 'vue-i18n': { useI18n: () => ({ t: (key) => key }) },
    '@/lib/walletPasswordPrompt': h.passwordPrompt, '@/lib/identity-boundary': h.identity,
    '@/lib/credential-rate-limit': loadModule('../../lib/credential-rate-limit.ts'),
    '@/utils/sat20': { __esModule: true, default: h.wallet },
  })
  dialog.passwordInput.value = { value: '' }
  return { ...dialog, route, dispose: () => cleanup.forEach(fn => fn()) }
}

test('session authorization starts locked and never retains a password', async () => {
  const identity = loadModule('../../lib/identity-boundary.ts')
  const session = loadModule('../../lib/walletSession.ts', { './identity-boundary': identity })
  assert.equal(session.isWalletSessionUnlocked(), false)
  assert.equal(session.isWalletRuntimeUnlocked(), false)
  assert.throws(() => session.walletRequestSessionGuard('signMessage'), /must be unlocked/)
  assert.ok(Object.keys(session).every(key => !/password/i.test(key)))
  const router = readFileSync(new URL('../../router/index.ts', import.meta.url), 'utf8')
  assert.match(router, /!isWalletSessionUnlocked\(\).*\|\| !isWalletRuntimeUnlocked\(\)/)
  assert.doesNotMatch(router, /unlockWallet\(|SessionPassword/)
})

test('password dialog verifies once, clears input, and asks again for the next operation', async () => {
  const h = walletSessionFixture(), dialog = passwordDialogFixture(h)
  try {
    for (let i = 0; i < 2; i++) {
      let executed = false
      const result = h.passwordPrompt.withWalletPassword(async password => {
        assert.equal(password, 'correct-password')
        executed = true
        return 'done'
      })
      assert.equal(dialog.open.value, true)
      dialog.passwordInput.value.value = 'wrong-password'
      await dialog.confirm()
      assert.equal(dialog.passwordInput.value.value, '')
      assert.equal(dialog.open.value, true)
      assert.equal(executed, false)
      dialog.passwordInput.value.value = 'correct-password'
      await dialog.confirm()
      assert.equal(await result, 'done')
      assert.equal(dialog.passwordInput.value.value, '')
      assert.equal(dialog.open.value, false)
    }
    assert.deepEqual(h.calls.map(([name]) => name), Array(4).fill('unlockWallet'))
  } finally { dialog.dispose() }
})

test('cancel, navigation, UI lock and unmount discard the prompt without executing', async () => {
  for (const stop of ['cancel', 'navigation', 'lock', 'unmount']) {
    const h = walletSessionFixture(), dialog = passwordDialogFixture(h)
    let calls = 0
    const result = h.passwordPrompt.withWalletPassword(async () => { calls++ })
    dialog.passwordInput.value.value = 'not retained'
    if (stop === 'cancel') dialog.finish()
    if (stop === 'navigation') { dialog.route.fullPath = '/wallet/settings'; await vue.nextTick() }
    if (stop === 'lock') await h.store.lockWallet()
    if (stop === 'unmount') dialog.dispose()
    assert.equal(await result, undefined)
    assert.equal(calls, 0)
    assert.equal(dialog.passwordInput.value.value, '')
    dialog.dispose()
  }
})

test('a successful password response after lock cannot run the waiting operation', async () => {
  const h = walletSessionFixture(), dialog = passwordDialogFixture(h)
  let resume, calls = 0
  h.handlers.unlockWallet = () => new Promise(resolve => { resume = resolve })
  const result = h.passwordPrompt.withWalletPassword(async () => { calls++ })
  dialog.passwordInput.value.value = 'correct-password'
  const checking = dialog.confirm()
  await new Promise(resolve => setImmediate(resolve))
  await h.store.lockWallet()
  resume({ code: 0, data: { walletId: '1' } })
  await checking
  assert.equal(await result, undefined)
  assert.equal(calls, 0)
  assert.equal(h.session.isWalletSessionUnlocked(), false)
  dialog.dispose()
})

test('network cancellation leaves the manager intact; switching and rollback share only an operation-local password', async () => {
  const h = walletSessionFixture(), dialog = passwordDialogFixture(h)
  assert.equal(vue.isProxy(h.store.wallets.value), true)
  const canceled = h.store.setNetwork('mainnet')
  dialog.finish()
  assert.equal(await canceled, false)
  assert.equal(h.calls.length, 0)
  for (const fail of [true, false]) {
    h.calls.length = 0
    h.handlers.init = config => config.network === 'mainnet' && fail
      ? { code: -1, msg: 'target init failed' } : { code: 0 }
    const changed = h.store.setNetwork('mainnet')
    const outcome = fail ? assert.rejects(changed, /target init failed/) : changed
    dialog.passwordInput.value.value = 'correct-password'
    await dialog.confirm()
    await outcome
    const names = h.calls.map(([name]) => name)
    assert.equal(names[0], 'unlockWallet', 'verify before releasing the existing manager')
    assert.ok(names.includes('release'))
	if (!fail) assert.ok(names.includes('recoverAccountManagementFromCurrentWallet'))
    assert.ok(h.calls.filter(([name]) => name === 'switchWallet').every(call => call[2] === ''))
    assert.equal(h.state.network, fail ? 'testnet' : 'mainnet')
	assert.equal(h.state.rootAccountId, 'root-account')
    assert.equal(h.state.walletId, '1')
    assert.equal(h.state.accountIndex, 0)
    assert.equal(h.session.isWalletSessionUnlocked(), true)
  }
  dialog.dispose()
})

test('network topology mismatch rolls back the manager before publishing target state', async () => {
  const h = walletSessionFixture(), dialog = passwordDialogFixture(h)
  const before = structuredClone(h.state)
  let activeNetwork = 'testnet'
  h.handlers.init = config => {
    activeNetwork = config.network
    return { code: 0 }
  }
  h.handlers.getWalletCatalog = () => ({ code: 0, data: { wallets: [{
    id: '1', name: 'Wallet 1', fingerprint: 'wallet-fingerprint', accounts: activeNetwork === 'mainnet' ? [
      { index: 0, name: 'Account 1', did: '', address: 'address', pub_key: 'pubkey', account_id: 'root-account' },
      { index: 1, name: 'Account 2', did: '', address: 'address-1', pub_key: 'pubkey-1', account_id: 'sub-account' },
    ] : [
      { index: 0, name: 'Account 1', did: '', address: 'address', pub_key: 'pubkey', account_id: 'root-account' },
    ],
  }] } })
  const changed = h.store.setNetwork('mainnet')
  const rejected = assert.rejects(changed, /Wallet topology changed/)
  dialog.passwordInput.value.value = 'correct-password'
  await dialog.confirm()
  await rejected
  assert.deepEqual(h.state, before)
  const initializedNetworks = h.calls
    .filter(([name]) => name === 'init')
    .map(([, config]) => config.network)
  assert.deepEqual(initializedNetworks, ['mainnet', 'testnet'])
  dialog.dispose()
})

test('ordinary wallet/account switching and signing do not request a password', async () => {
  const h = walletSessionFixture()
  h.passwordPrompt.registerWalletPasswordPrompt(() => { throw new Error('unexpected password prompt') })
  h.store.wallets.value = [{ id: '2', accounts: [{ index: 1 }] }]
  await h.store.switchWallet('2')
  await h.store.switchToAccount(0)
  assert.equal((await h.wallet.signMessage('message'))[0], undefined)
  assert.ok(h.calls.filter(([name]) => name === 'switchWallet').every(call => call[2] === ''))
})
