import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const pwa = fileURLToPath(new URL('../../', import.meta.url))
const require = createRequire(`${pwa}/package.json`)
const { parse, compileScript } = require('@vue/compiler-sfc')
const Vue = require('vue')
const ts = require('typescript')
const sourcePath = `${pwa}/entrypoints/popup/pages/wallet/settings/node.vue`
const source = readFileSync(sourcePath, 'utf8')
const compiled = compileScript(parse(source).descriptor, { id: 'node-unstake-behavior-test' }).content
const transpiled = ts.transpileModule(compiled, { compilerOptions: {
  target: ts.ScriptTarget.ES2022,
  module: ts.ModuleKind.CommonJS,
} }).outputText

function fixture({ minerInfo = { data: { ServerNode: 'core' } }, pending = false, fetching = false, error = null, unstake } = {}) {
  const queryData = Vue.ref(minerInfo)
  const isPending = Vue.ref(pending)
  const isFetching = Vue.ref(fetching)
  const queryError = Vue.ref(error)
  const store = Vue.reactive({ btcFeeRate: 2, network: 'regtest', publicKey: 'test-pubkey' })
  const calls = []
  const localStakeRecord = { pubkey: 'test-pubkey', asset: 'DOGCOIN', amount: '1000' }
  const storageCalls = []
  const walletManager = {
    minerUnstake: async (...args) => {
      calls.push(args)
      return unstake ? unstake(...args) : [null, { txId: 'tx-unstake', resvId: 'resv-unstake' }]
    },
    stakeToBeMiner: async () => { throw new Error('unexpected stake call') },
  }
  const generic = {}
  const mocks = {
    vue: Vue,
    '@tanstack/vue-query': { useQuery: () => ({ data: queryData, isPending, isFetching, error: queryError }) },
    '@/apis': { satnetApi: { getMinerInfo: async () => minerInfo } },
    '@/utils/sat20': { default: walletManager },
    '@/components/layout/LayoutSecond.vue': { default: generic },
    '@/components/ui/card': Object.fromEntries(['Card', 'CardHeader', 'CardTitle', 'CardDescription', 'CardContent', 'CardFooter'].map(k => [k, generic])),
    '@iconify/vue': { Icon: generic },
    '@/components/ui/button': { Button: generic },
    '@/components/ui/alert': { Alert: generic, AlertTitle: generic, AlertDescription: generic },
    '@/components/ui/accordion': { Accordion: generic, AccordionItem: generic, AccordionTrigger: generic, AccordionContent: generic },
    '@/components/ui/dialog': { Dialog: generic, DialogContent: generic, DialogHeader: generic, DialogTitle: generic, DialogDescription: generic, DialogFooter: generic },
    '@/store/wallet': { useWalletStore: () => store },
    pinia: { storeToRefs: Vue.toRefs },
    '@/utils': { hideAddress: x => x, generateMempoolUrl: () => '' },
    '@/lib/nodeStakeStorage': { nodeStakeStorage: {
      saveNodeStakeData: async (...args) => storageCalls.push(['save', ...args]),
      deleteNodeStakeData: async (...args) => storageCalls.push(['delete', ...args]),
      getNodeStakeData: async () => localStakeRecord,
    } },
  }
  const mod = { exports: {} }
  new Function('require', 'module', 'exports', transpiled)(id => {
    if (Object.hasOwn(mocks, id)) return mocks[id]
    return require(id)
  }, mod, mod.exports)
  const component = mod.exports.default
  const bindings = component.setup({}, { expose() {} })
  return { bindings, calls, storageCalls, localStakeRecord, queryData, isPending, isFetching, queryError }
}

test('unstaked state refuses opening and submission', async () => {
  const f = fixture({ minerInfo: { data: {} } })
  f.bindings.onUnstake()
  await f.bindings.confirmUnstake()
  assert.equal(f.bindings.showConfirm.value, false)
  assert.equal(f.calls.length, 0)
})

test('pending, fetching, and error query states refuse the action', async t => {
  for (const options of [
    { pending: true },
    { fetching: true },
    { error: new Error('query failed') },
  ]) await t.test(JSON.stringify({ pending: !!options.pending, fetching: !!options.fetching, error: !!options.error }), async () => {
    const f = fixture(options)
    f.bindings.onUnstake()
    await f.bindings.confirmUnstake()
    assert.equal(f.bindings.showConfirm.value, false)
    assert.equal(f.calls.length, 0)
  })
})

test('cached empty miner info with background fetch cannot open stake or unstake', () => {
  const f = fixture({ minerInfo: {}, fetching: true })
  f.bindings.onStake(false)
  f.bindings.onUnstake()
  assert.equal(f.bindings.showConfirm.value, false)
  assert.equal(f.calls.length, 0)
})

test('opening confirmation and cancelling do not call the SDK', () => {
  const f = fixture()
  f.bindings.onUnstake()
  assert.equal(f.bindings.showConfirm.value, true)
  assert.equal(f.bindings.pendingAction.value, 'unstake')
  assert.equal(f.calls.length, 0)
  assert.match(source, /@click="showConfirm\s*=\s*false"/)
  f.bindings.showConfirm.value = false // the exact template cancel expression above
  assert.equal(f.calls.length, 0)
  assert.equal(f.storageCalls.length, 0)
})

test('successful request calls minerUnstake once and reports only submission/reservation state', async () => {
  const f = fixture()
  await f.bindings.confirmUnstake()
  assert.deepEqual(f.calls, [['2']])
  assert.equal(f.bindings.unstakeSubmitted.value, true)
  assert.equal(f.bindings.resultSuccess.value, true)
  assert.equal(f.bindings.resultMsg.value, '取消质押已提交，请在操作日志中查看最终结果。')
  assert.equal(f.bindings.txId.value, 'tx-unstake')
  assert.equal(f.bindings.resvId.value, 'resv-unstake')
  assert.equal(f.bindings.isStaked.value, true)
  assert.deepEqual(f.localStakeRecord, { pubkey: 'test-pubkey', asset: 'DOGCOIN', amount: '1000' })
  assert.equal(f.storageCalls.length, 0)
  assert.equal(f.bindings.isLoading.value, false)
})

test('a pending request blocks a concurrent second submission', async () => {
  let resolve
  const f = fixture({ unstake: () => new Promise(r => { resolve = r }) })
  const first = f.bindings.confirmUnstake()
  const second = f.bindings.confirmUnstake()
  await second
  assert.equal(f.calls.length, 1)
  assert.equal(f.bindings.isLoading.value, true)
  resolve([null, { txId: 'tx-once', resvId: 'resv-once' }])
  await first
  assert.equal(f.bindings.isLoading.value, false)
  assert.equal(f.calls.length, 1)
})

test('SDK error clears loading and allows a successful retry', async () => {
  let attempt = 0
  const f = fixture({ unstake: async () => ++attempt === 1
    ? [new Error('temporary failure'), undefined]
    : [null, { txId: 'tx-retry', resvId: 'resv-retry' }] })
  await f.bindings.confirmUnstake()
  assert.equal(f.bindings.isLoading.value, false)
  assert.match(f.bindings.resultMsg.value, /temporary failure/)
  assert.equal(f.bindings.unstakeSubmitted.value, false)
  await f.bindings.confirmUnstake()
  assert.equal(f.calls.length, 2)
  assert.equal(f.bindings.isLoading.value, false)
  assert.equal(f.bindings.unstakeSubmitted.value, true)
  assert.equal(f.bindings.resvId.value, 'resv-retry')
})
