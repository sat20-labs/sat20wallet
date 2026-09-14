import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { parse, compileScript } from '@vue/compiler-sfc'
import * as Vue from 'vue'
import ts from 'typescript'

// Compile the real entry, including its template. Only external services/UI are doubles.
const require = createRequire(import.meta.url)
const root = new URL('../../', import.meta.url)
const transpile = source => ts.transpileModule(source, { compilerOptions: {
  target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS,
} }).outputText
const evaluate = (source, resolve) => {
  const module = { exports: {} }
  new Function('require', 'module', 'exports', transpile(source))(resolve, module, module.exports)
  return module.exports
}
const amount = evaluate(readFileSync(new URL('utils/sendAmount.ts', root), 'utf8'), require)
const context = evaluate(readFileSync(new URL('lib/assetContext.ts', root), 'utf8'), require)
const { descriptor } = parse(readFileSync(new URL('components/asset/BalanceSummary.vue', root), 'utf8'))
const compiled = compileScript(descriptor, { id: 'balance-summary-test', inlineTemplate: true })
const native = { id: '::', key: '::', protocol: '', type: '*', label: 'sats', amount: '1000' }
const fixture = async (chain = 'bitcoin') => {
  const wallet = Vue.reactive({ address: 'test-address', network: 'testnet', btcFeeRate: 1, walletId: 'wallet-b', accountIndex: 0 })
  const global = Vue.reactive({ env: 'test', hideBalance: false })
  const balances = Vue.reactive({ totalSats: 1000, plainList: [{ ...native }] })
  const channel = Vue.reactive({ totalSats: 0, plainList: [], channel: null })
  const data = Vue.ref(), isError = Vue.ref(false)
  const calls = [], errors = [], buttons = []
  let dialog, query
  const mocks = {
    vue: Vue,
    pinia: { storeToRefs: Vue.toRefs },
    '@/store': { useL1Store: () => balances, useL2Store: () => balances, useWalletStore: () => wallet,
      useChannelStore: () => channel, useTranscendingModeStore: () => Vue.reactive({ selectedTranscendingMode: 'poolswap' }) },
    '@/store/global': { useGlobalStore: () => global },
    '@/lib/assetContext': context, '@/utils/sendAmount': amount,
    '@/composables/useAssetActions': { useAssetActions: () => ({
      loading: Vue.ref(false), handleError: e => errors.push(e),
      l1Send: async p => calls.push(['l1', p]), l2Send: async p => calls.push(['l2', p]),
    }) },
    '@/components/ui/toast-new': { useToast: () => ({ toast: e => errors.push(e) }) },
    'vue-i18n': { useI18n: () => ({ t: s => s }) },
    '@tanstack/vue-query': { useQuery: q => { query = q; return { data, isError, refetch: async () => ({ data: data.value }) } } },
    '@/utils/sat20': { default: {} }, '@/utils/browser': { openLink: async () => {} },
    '@/utils': { generateMempoolUrl: () => '' }, '@/types/index': { Chain: {} },
    '@/components/ui/button': { Button: { setup(_, { attrs, slots }) {
      buttons.push(attrs); return () => Vue.h('button', {}, slots.default?.())
    } } },
    '@iconify/vue': { Icon: () => null },
    '@/components/wallet/AssetOperationDialog.vue': { default: { setup(_, { attrs }) {
      return () => { dialog = { ...attrs }; return null }
    } } },
  }
  const component = evaluate(compiled.content, id => mocks[id] || (id.endsWith('.vue') ? { default: () => null } : require(id))).default
  const renderer = Vue.createRenderer({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setText() {}, setElementText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const app = renderer.createApp(component, { selectedChain: chain, mempoolUrl: '' })
  app.config.globalProperties.$t = s => s
  app.mount({})
  const ready = async value => {
    isError.value = false
    data.value = { contextKey: query.queryKey[1].value, balance: { availableAmt: value, lockedAmt: '0' } }
    await Vue.nextTick()
  }
  const open = async () => { await buttons[1].onClick(); await Vue.nextTick() }
  const enter = async text => {
    dialog['onUpdate:amount'](text); dialog['onUpdate:address']('recipient'); await Vue.nextTick()
  }
  return { ready, open, enter, wallet, balances, isError, data, calls, errors,
    // This stub checks the native entry contract; send-review.mjs mounts the real dialog.
    get dialog() { return dialog }, error: () => amount.validateSendAmount(dialog.amount, dialog['asset-key'] === '::' && dialog['asset-type'] === '*' ? 0 : dialog['amount-precision'], dialog['max-amount']),
    confirm: async () => { await dialog.onConfirm(); await Vue.nextTick() }, close: () => app.unmount() }
}

const f = await fixture()
await f.ready('1000'); await f.open(); await f.enter('330')
console.log('actual SFC gate:', JSON.stringify({ amount: f.dialog.amount, precision: f.dialog['amount-precision'] ?? 'undefined', balance: f.dialog['max-amount'], error: f.error() }))
assert.equal(f.error(), null, 'loaded native 330 / 1000 sats must reach review')
assert.equal(f.dialog['amount-precision'], undefined, 'native entry does not pass precision')
assert.equal(f.calls.length, 0, 'opening review does not dispatch')
f.dialog['onUpdate:open'](false); await Vue.nextTick()
assert.equal(f.calls.length, 0, 'cancel does not dispatch')
await f.open(); await f.enter('330')
await f.ready('329'); await f.confirm(); assert.equal(f.calls.length, 0, 'balance drop blocks final dispatch')
await f.ready('1000'); const oldResult = { ...f.data.value }
f.wallet.accountIndex++; await Vue.nextTick()
assert.equal(f.error(), 'unavailable', 'new scope has no loaded balance')
f.data.value = oldResult; await Vue.nextTick()
assert.equal(f.error(), 'unavailable', 'late old-scope response is rejected')
await f.ready('1000'); await f.confirm(); assert.equal(f.calls.length, 0, 'old dialog cannot send in new scope')
await f.open(); await f.enter('330'); await f.confirm()
assert.deepEqual(f.calls, [['l1', { toAddress: 'recipient', asset_name: '::', amt: '330' }]])
f.close()

const g = await fixture('satoshinet')
await g.open(); assert.ok(g.errors.includes('assetOperationDialog.amountErrors.unavailable'))
await g.ready('1000'); await g.open(); await g.enter('330'); assert.equal(g.error(), null)
assert.equal(g.dialog['max-amount'], '990', 'L2 recipient maximum reserves the fixed network fee')
assert.equal(g.dialog['network-fee'], '10'); assert.equal(g.dialog['total-spend'], '340')
g.dialog['onUpdate:open'](false); await Vue.nextTick()
g.isError.value = true; await Vue.nextTick(); await g.open(); await g.enter('330')
assert.equal(g.error(), null, 'a background query error retains the last exact same-scope snapshot')
await g.ready('9007199254740993'); await g.enter('9007199254740983'); assert.equal(g.error(), null)
await g.ready(9007199254740992); await g.confirm(); assert.equal(g.calls.length, 0, 'unsafe numeric refresh blocks final dispatch')
await g.ready('1e3'); await g.confirm(); assert.equal(g.calls.length, 0, 'non-integer refresh blocks final dispatch')
await g.ready('1000')
for (const [value, expected] of [['1e3', 'decimal'], ['1.5', 'precision'], ['1001', 'balance']]) {
  await g.enter(value); assert.equal(g.error(), expected); await g.confirm(); assert.equal(g.calls.length, 0)
}
await g.enter('330'); await g.confirm()
assert.deepEqual(g.calls, [['l2', { toAddress: 'recipient', asset_name: '::', amt: '330' }]])
g.close()
const invalid = await fixture()
invalid.balances.plainList = [{ ...native, id: 'ordx:f:test', key: 'ordx:f:test', protocol: 'ordx', type: 'f' }]
await invalid.ready('1000'); await invalid.open()
assert.equal(invalid.dialog.open, false, 'a nonnative snapshot cannot enter the sats send path')
assert.equal(invalid.calls.length, 0)
invalid.close()
assert.equal(amount.validateSendAmount('1', undefined, '1000'), 'unavailable', 'unknown asset precision stays closed')
console.log('BalanceSummary real SFC send wiring/context/exact balance/dispatch checks passed (mock dispatch only)')
