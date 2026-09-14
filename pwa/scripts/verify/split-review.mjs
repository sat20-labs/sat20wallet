import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import * as V from 'vue'
import * as z from 'zod'
import { useForm as useRealForm, useField } from 'vee-validate'
import { toTypedSchema as realTypedSchema } from '@vee-validate/zod'
const source = readFileSync('entrypoints/popup/pages/wallet/split.vue', 'utf8')
const script = source.split('<script lang="ts" setup>')[1].split('</script>')[0]
const ast = ts.createSourceFile('split.ts', script, ts.ScriptTarget.Latest, true)
const printer = ts.createPrinter()
const body = ast.statements.filter(s => !ts.isImportDeclaration(s)).map(s => printer.printNode(ts.EmitHint.Unspecified, s, ast)).join('\n')
const code = ts.transpileModule(body + '\nglobalThis.api = { form, review, onSubmit, confirmSplit, cancelReview };', { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const en = JSON.parse(readFileSync('locales/en.json', 'utf8'))
const t = key => key.split('.').reduce((v, k) => v?.[k], en) ?? key
function fixture({ validation, valid = true, realFields = false } = {}) {
  const writes = [], disposed = [], notices = [], watchers = [], identityListeners = []
  let generation = 1, ready = true, refreshes = 0
  const wallet = V.reactive({ address: 'source', walletId: 11, wallet: { name: 'Source wallet' }, rootAccountId: 'root-a', accountIndex: 0, network: 'testnet', btcFeeRate: 2 })
  const props = V.reactive({ assetName: '::' }), global = V.reactive({ env: 'prd' })
  const context = {
    ref: V.ref, reactive: V.reactive, computed: V.computed,
    watch: (...args) => { const stop = V.watch(...args); watchers.push(stop); return stop }, onBeforeUnmount: fn => disposed.push(fn),
    useI18n: () => ({ t }), useRoute: () => ({}), useRouter: () => ({ back() {} }), defineProps: () => props,
    useWalletStore: () => wallet, useGlobalStore: () => global, storeToRefs: V.toRefs,
    useL1Assets: () => ({ refreshL1Assets: async () => { refreshes++ } }), useToast: () => ({ toast: item => notices.push(item) }),
    assertWalletIdentityReady: expected => { if (!ready || expected !== undefined && expected !== generation) throw Error('Stale identity'); return generation },
    subscribeWalletIdentity: fn => { identityListeners.push(fn); return () => identityListeners.splice(identityListeners.indexOf(fn), 1) },
    toTypedSchema: schema => realFields ? realTypedSchema(schema) : schema, z,
    useForm: options => {
      if (realFields) return useRealForm(options)
      const values = V.reactive({ ...options.initialValues })
      return { values, setFieldValue: (key, value) => { values[key] = value }, resetForm() {},
        handleSubmit: fn => async () => { const parsed = options.validationSchema.safeParse(values); if (parsed.success) return fn(parsed.data) } }
    },
    walletManager: {
      getAssetAmount: async () => [undefined, { availableAmt: 10000 }],
      validateBitcoinAddress: async () => validation ? await validation : [undefined, { valid }],
      batchSendAssets: async (...args) => { writes.push(args); return [undefined, { txId: 'split-tx' }] },
    },
    console: { log() {}, error() {} }, setTimeout() {},
  }
  vm.runInNewContext(code, context)
  const api = context.api
  for (const [key, value] of Object.entries({ amt: '1000', n: 2, destAddr: 'destination' })) api.form.setFieldValue(key, value)
  return { api, wallet, global, props, writes, notices, get refreshes() { return refreshes },
    transition() { generation++; ready = false; identityListeners.slice().forEach(fn => fn({ generation, phase: 'QUIESCING' })) },
    dispose() { disposed.forEach(fn => fn()); watchers.forEach(fn => fn()) } }
}
const f = fixture(); await f.api.onSubmit()
assert.equal(f.writes.length, 0)
for (const [key, value] of Object.entries({ amt: '1000', n: 2, total: '2000', destAddr: 'destination', feeRate: 2, network: 'testnet', accountIndex: 0, sourceAddress: 'source' })) assert.equal(f.api.review.value[key], value)
assert.ok(Object.isFrozen(f.api.review.value))
f.api.cancelReview(); assert.equal(f.api.review.value, null); assert.equal(f.writes.length, 0)
await f.api.onSubmit(); await Promise.all([f.api.confirmSplit(), f.api.confirmSplit()])
assert.deepEqual(f.writes, [['destination', '::', '1000', 2, 2]])
assert.equal(f.refreshes, 1); assert.equal(f.api.review.value, null); f.dispose()
for (const change of [f => { f.api.form.values.amt = '1001' }, f => { f.api.form.values.n = 3 }, f => { f.api.form.values.destAddr = 'other' }, f => { f.wallet.walletId = 22 }, f => { f.wallet.accountIndex++ }, f => { f.wallet.network = 'mainnet' }, f => { f.wallet.btcFeeRate++ }, f => { f.global.env = 'test' }, f => f.transition()]) {
  const f = fixture(); await f.api.onSubmit(); change(f); assert.equal(f.api.review.value, null)
  await f.api.confirmSplit(); assert.equal(f.writes.length, 0); f.dispose()
}
const closed = fixture(); await closed.api.onSubmit(); closed.dispose(); await closed.api.confirmSplit(); assert.equal(closed.writes.length, 0)
let resolveAddress
const race = fixture({ validation: new Promise(resolve => { resolveAddress = resolve }) })
const preparing = race.api.onSubmit(); race.wallet.network = 'mainnet'; race.wallet.network = 'testnet'
resolveAddress([undefined, { valid: true }]); await preparing; assert.equal(race.api.review.value, null); assert.equal(race.writes.length, 0); race.dispose()
for (const setup of [() => fixture({ valid: false }), () => { const f = fixture(); f.api.form.values.amt = '1.5'; return f }, () => { const f = fixture(); f.wallet.address = null; return f }]) {
  const invalid = setup(); await invalid.api.onSubmit()
  assert.equal(invalid.api.review.value, null); assert.equal(invalid.writes.length, 0); invalid.dispose()
}
assert.match(source, /v-if="!review"/)
assert.match(source, /@click="cancelReview"/)
assert.match(source, /@click="confirmSplit"/)
assert.match(en.splitAsset.feeUnavailable, /have not been quoted/)
assert.match(en.splitAsset.feeUnavailable, /may reduce/)
// Mount real vee-validate fields: Review's v-if unmount must not erase the
// values and trigger fingerprint cancellation. No DOM/browser/wallet needed.
const renderer = V.createRenderer({
  createElement: type => ({ type, children: [] }), createText: text => ({ text }), createComment: text => ({ text }),
  setText() {}, setElementText() {}, patchProp() {}, parentNode: node => node.parent, nextSibling: () => null,
  insert(node, parent) { node.parent = parent; (parent.children ??= []).push(node) },
  remove(node) { if (node.parent) node.parent.children = node.parent.children.filter(child => child !== node) },
})
let lifecycle
const RealField = { props: ['name'], setup(props) { useField(() => props.name); return () => V.h('input') } }
const app = renderer.createApp({ setup() {
  lifecycle = fixture({ realFields: true })
  return () => lifecycle.api.review.value ? V.h('section') : V.h('form', {}, ['assetName', 'amt', 'n', 'destAddr'].map(name => V.h(RealField, { name })))
} })
app.mount({ children: [] })
await V.nextTick()
await lifecycle.api.onSubmit()
for (let i = 0; i < 8; i++) await V.nextTick()
assert.equal(lifecycle.api.review.value?.total, '2000', 'Review survives actual Field unmount')
assert.equal(lifecycle.writes.length, 0)
lifecycle.api.cancelReview()
for (let i = 0; i < 8; i++) await V.nextTick()
assert.equal(lifecycle.api.form.values.amt, '1000')
assert.equal(lifecycle.api.form.values.n, 2)
assert.equal(lifecycle.api.form.values.destAddr, 'destination')
await lifecycle.api.onSubmit()
for (let i = 0; i < 8; i++) await V.nextTick()
assert.equal(lifecycle.api.review.value?.total, '2000', 'Review can reopen after Cancel')
lifecycle.api.form.setFieldValue('amt', '1001')
assert.equal(lifecycle.api.review.value, null, 'Genuine input changes still invalidate Review')
assert.equal(lifecycle.writes.length, 0)
app.unmount(); lifecycle.dispose()
console.log('Split review: PASS (no write before Confirm, Cancel, immutable request, identity/input invalidation, stale address validation, double submit, invalid inputs, L1 refresh)')
console.log('Split real-field lifecycle: PASS (Review retains values through unmount; Cancel restores inputs; edits still invalidate)')
