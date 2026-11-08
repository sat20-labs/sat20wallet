import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { parse, compileScript } from '@vue/compiler-sfc'
import * as Vue from 'vue'
import ts from 'typescript'

const management = 'components/setting/ReferrerSetting.vue'
const binding = 'entrypoints/popup/pages/wallet/settings/referrer/bind.vue'
const registration = 'entrypoints/popup/pages/wallet/settings/referrer/index.vue'

function fixture(component, options = {}) {
  const source = readFileSync(new URL(`../../${component}`, import.meta.url), 'utf8')
  const compiled = compileScript(parse(source).descriptor, { id: 'referrer-state' }).content
  const js = ts.transpileModule(compiled, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText
  const wallet = {
    address: Vue.ref('test-public-address'), network: Vue.ref('regtest'),
    publicKey: Vue.ref('test-public-key'), btcFeeRate: Vue.ref(1),
  }
  const calls = { routes: [], writes: 0 }
  const state = {
    names: ['pwaui1104'], bound: { code: 0, referrer: { Name: 'pwaui1104', BindBlock: 24 } },
    ...options,
  }
  const referrers = {
    getLocalBoundReferrer: async () => null,
    getLocalBoundReferrerTxId: async () => null,
    addLocalBoundReferrer: async () => {}, cacheBoundReferrerTxId: async () => {},
    addLocalReferrerName: async () => {},
  }
  const mocks = {
    vue: { ...Vue, onMounted() {}, watch() {} },
    pinia: { storeToRefs: value => value },
    'vue-i18n': { useI18n: () => ({ t: key => key }) },
    'vue-router': { useRouter: () => ({ push: path => calls.routes.push(path) }) },
    '@iconify/vue': { Icon: {} },
    '@/store/wallet': { useWalletStore: () => wallet },
    '@/store/global': { useGlobalStore: () => ({ env: Vue.ref('dev') }) },
    '@/config/wasm': { getConfig: () => ({ Peers: ['', 's@test-public-key@http://test.invalid'] }) },
    '@/utils/browser': { openMempoolLink() {} },
    '@/types': { Network: { REGTEST: 'regtest' } },
    '@/composables/useReferrerManager': { useReferrerManager: () => referrers },
    '@/composables/useNameManager': { useNameManager: () => ({
      nameList: Vue.ref([{ id: 1, name: 'pwaui1104' }]), isLoadingNames: Vue.ref(false),
    }) },
    '@/utils/sat20': { default: {
      getAllRegisteredReferrerName: async () => {
        if (state.registrationError) return [new Error('service unavailable')]
        if (state.registrationReply) return state.registrationReply()
        return [undefined, { names: state.names }]
      },
      registerAsReferrer: async () => { calls.writes++; return [undefined, { txId: 'test-tx' }] },
      bindReferrerForServer: async () => { calls.writes++; return [undefined, { txId: 'test-tx' }] },
    } },
    '@/apis/satnet': { default: { getReferrerByAddress: async () => {
      if (state.bindingError) throw new Error('service unavailable')
      return state.bound
    } } },
  }
  const mod = { exports: {} }
  new Function('require', 'module', 'exports', 'console', js)(name => {
    if (name in mocks) return mocks[name]
    assert.ok(name.startsWith('@/components/'), `Unexpected dependency: ${name}`)
    return {}
  }, mod, mod.exports, { log() {}, error() {}, warn() {} })
  return { ui: mod.exports.default.setup({}, { expose() {} }), calls, state, wallet, source }
}

test('management blocks both existing relationships and does not expose a management bypass', async () => {
  const f = fixture(management)
  assert.equal(f.ui.canBind.value, false)
  assert.equal(f.ui.canRegister.value, false)
  await f.ui.loadAllReferrerInfo()
  assert.equal(f.ui.boundReferrer.value, 'pwaui1104')
  assert.deepEqual(f.ui.referrerNames.value, ['pwaui1104'])
  f.ui.handleBindClick(); f.ui.handleRegisterClick()
  assert.deepEqual(f.calls.routes, [])
  assert.equal(f.calls.writes, 0)
})

test('only verified empty server results enable the corresponding entries', async () => {
  const f = fixture(management, { names: null, bound: { code: -1, msg: 'key not found' } })
  await f.ui.loadAllReferrerInfo()
  assert.equal(f.ui.canBind.value, true)
  assert.equal(f.ui.canRegister.value, true)
  f.ui.handleBindClick(); f.ui.handleRegisterClick()
  assert.deepEqual(f.calls.routes, ['/wallet/setting/referrer/bind', '/wallet/setting/referrer/register'])
  assert.equal(f.calls.writes, 0)
})

test('failed or malformed lookups never enable a new action', async () => {
  for (const options of [
    { bindingError: true, registrationError: true },
    { bound: { code: 0, referrer: null }, names: {} },
  ]) {
    const f = fixture(management, options)
    await f.ui.loadAllReferrerInfo()
    assert.equal(f.ui.canBind.value, false)
    assert.equal(f.ui.canRegister.value, false)
    f.ui.handleBindClick(); f.ui.handleRegisterClick()
    assert.deepEqual(f.calls.routes, [])
  }
})

test('direct binding page has no unbind/cache action and rejects duplicate submission', async () => {
  const f = fixture(binding)
  assert.equal(f.ui.bindingKnown.value, false)
  await f.ui.loadLocalBoundReferrer()
  assert.equal(f.ui.localBoundReferrer.value, 'pwaui1104')
  f.ui.referrerName.value = 'another-name'
  f.ui.onBind(); await f.ui.confirmBind()
  assert.equal(f.ui.showConfirm.value, false)
  assert.equal(f.calls.writes, 0)
  assert.equal('handleUnbind' in f.ui, false)
  assert.doesNotMatch(f.source, /clearLocalCache|handleUnbind|isUnbinding/)
})

test('direct registration page blocks existing and unverified registration', async () => {
  for (const options of [{}, { registrationError: true }]) {
    const f = fixture(registration, options)
    assert.equal(f.ui.canRegister.value, false)
    await f.ui.loadRegistrationStatus()
    f.ui.name.value = 'pwaui1104'
    f.ui.onRegister(); await f.ui.confirmRegister()
    assert.equal(f.ui.canRegister.value, false)
    assert.equal(f.ui.showConfirm.value, false)
    assert.equal(f.calls.writes, 0)
  }
})

test('registration rechecks server state before the final submission', async () => {
  const f = fixture(registration, { names: null })
  await f.ui.loadRegistrationStatus()
  f.ui.name.value = 'pwaui1104'; f.ui.onRegister()
  assert.equal(f.ui.showConfirm.value, true)
  f.state.names = ['pwaui1104']
  await f.ui.confirmRegister()
  assert.equal(f.calls.writes, 0)
  assert.equal(f.ui.canRegister.value, false)
})

test('a successful new registration immediately disables repeat registration', async () => {
  const f = fixture(registration, { names: null })
  await f.ui.loadRegistrationStatus()
  f.ui.name.value = 'pwaui1104'; f.ui.onRegister()
  await f.ui.confirmRegister(); await f.ui.confirmRegister()
  assert.equal(f.calls.writes, 1)
  assert.equal(f.ui.canRegister.value, false)
})

test('a previous account response cannot enable registration for the new account', async () => {
  let finish
  const f = fixture(registration, { registrationReply: () => new Promise(resolve => { finish = resolve }) })
  const pending = f.ui.loadRegistrationStatus()
  f.wallet.address.value = 'another-public-address'
  finish([undefined, { names: null }]); await pending
  assert.equal(f.ui.registrationKnown.value, false)
  assert.equal(f.ui.canRegister.value, false)
})
