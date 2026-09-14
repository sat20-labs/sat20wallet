import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { parse, compileScript } from '@vue/compiler-sfc'
import * as V from 'vue'
import ts from 'typescript'
const root = new URL('../../', import.meta.url), require = createRequire(import.meta.url)
const load = p => readFileSync(new URL(p, root), 'utf8')
const evaluate = (source, mocks) => {
  const module = { exports: {} }
  new Function('require', 'module', 'exports', ts.transpileModule(source, { compilerOptions: {
    target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS,
  } }).outputText)(id => mocks[id] || require(id), module, module.exports)
  return module.exports
}
const compile = file => compileScript(parse(load(file)).descriptor, { id: file, inlineTemplate: true }).content
const text = n => n.text || n.children?.map(text).join(' ') || ''
const nodes = n => [n, ...(n.children || []).flatMap(nodes)]
const tick = () => V.nextTick()
const target = 'tb1p' + 'q'.repeat(58) // synthetic public fixture; resolver is a double
async function fixture(direct = false, chain = 'bitcoin') {
  const wallet = V.reactive({ network: 'testnet', btcFeeRate: 1, address: 'source', walletId: 'wb', accountIndex: 0, rootAccountId: 'root' })
  const global = V.reactive({ env: 'test', hideBalance: false })
  const store = V.reactive({ totalSats: 1000, plainList: [{ id: '::', key: '::', protocol: '', type: '*', label: 'sats', amount: '1000' }] })
  const data = V.ref(), calls = [], errors = []
  let query, resolve = async a => ({ isDomain: false, resolvedAddress: a, originalInput: a, domainName: null })
  const element = tag => ({ setup(_, { attrs, slots }) { return () => V.h(tag, { ...attrs }, slots.default?.()) } })
  const english = JSON.parse(load('locales/en.json'))
  const t = key => key.split('.').reduce((o, k) => o?.[k], english) || key
  const mocks = {
    vue: V, pinia: { storeToRefs: V.toRefs },
    '@/store': { useWalletStore: () => wallet, useL1Store: () => store, useL2Store: () => store,
      useChannelStore: () => V.reactive({ channel: null }), useTranscendingModeStore: () => V.reactive({ selectedTranscendingMode: 'poolswap' }) },
    '@/store/global': { useGlobalStore: () => global },
    '@/utils/sendAmount': evaluate(load('utils/sendAmount.ts'), {}),
    '@/lib/assetContext': evaluate(load('lib/assetContext.ts'), {}),
    '@/utils': { validateAndResolveAddress: (...args) => resolve(...args), hideAddress: a => a.slice(0, 8), generateMempoolUrl: () => '' },
    '@/utils/browser': { openLink() {} }, '@/types/index': { Chain: {} }, '@/utils/sat20': { default: {} },
    '@/components/ui/button': { Button: element('button') }, '@/components/ui/input': { Input: element('input') },
    '@/components/ui/label': { Label: element('label') }, '@/components/ui/separator': { Separator: element('hr') },
    '@/components/ui/dialog': Object.fromEntries(['Dialog', 'DialogContent', 'DialogDescription', 'DialogFooter', 'DialogHeader', 'DialogTitle'].map(k => [k, element('div')])),
    '@iconify/vue': { Icon: () => null }, '@/entrypoints/popup/pages/wallet/split.vue': { default: () => null },
    '@/components/wallet/LockWithExpandConfirmDialog.vue': { default: () => null }, '@/components/wallet/ReceiveQRCode.vue': { default: () => null },
    '@/components/ui/toast-new': { useToast: () => ({ toast: e => errors.push(e) }) },
    'vue-i18n': { useI18n: () => ({ t }) },
    '@tanstack/vue-query': { useQuery: q => { query = q; return { data, isError: V.ref(false), refetch: async () => ({ data: data.value }) } } },
    '@/composables/useAssetActions': { useAssetActions: () => ({ loading: V.ref(false), handleError: e => errors.push(e), l1Send: async p => calls.push(p), l2Send: async p => calls.push(p) }) },
  }
  const dialog = evaluate(compile('components/wallet/AssetOperationDialog.vue'), mocks).default
  mocks['@/components/wallet/AssetOperationDialog.vue'] = { default: dialog }
  const props = V.reactive({ open: true, title: 'Send', description: '', amount: '330', address: target, maxAmount: '1000', assetKey: '::', assetType: '*', chain, operationType: 'send',
    networkFee: chain === 'satoshinet' ? '10' : undefined, totalSpend: chain === 'satoshinet' ? '340' : undefined })
  const component = direct ? { setup: () => () => V.h(dialog, { ...props,
    'onUpdate:open': v => { props.open = v }, 'onUpdate:address': v => { props.address = v }, onConfirm: () => calls.push('confirm'),
  }) } : evaluate(compile('components/asset/BalanceSummary.vue'), mocks).default
  const container = { children: [] }
  const renderer = V.createRenderer({
    createElement: tag => ({ tag, children: [], props: {} }), createText: text => ({ text }), createComment: () => ({ text: '' }),
    insert(n, p, anchor) { if (n.parent) n.parent.children.splice(n.parent.children.indexOf(n), 1); n.parent = p; const i = p.children.indexOf(anchor); p.children.splice(i < 0 ? p.children.length : i, 0, n) },
    remove(n) { if (n.parent) n.parent.children.splice(n.parent.children.indexOf(n), 1) },
    setText(n, value) { n.text = value }, setElementText(n, value) { n.children = []; n.text = value },
    patchProp(n, k, _, v) { n.props[k] = v }, parentNode: n => n.parent, nextSibling: n => n.parent?.children[n.parent.children.indexOf(n) + 1],
  })
  const app = renderer.createApp(component, { selectedChain: chain, mempoolUrl: '' })
  app.config.globalProperties.$t = t; app.mount(container)
  const button = label => nodes(container).find(n => n.tag === 'button' && text(n).trim() === label)
  const click = async label => { const b = button(label); assert.ok(b, `button ${label}`); assert.ok(!b.props.disabled, `${label} enabled`); await b.props.onClick(); await tick() }
  if (!direct) {
    data.value = { contextKey: query.queryKey[1].value, balance: { availableAmt: '1000', lockedAmt: '0' } }; await tick()
    await click(t('balanceSummary.Send'))
    const inputs = nodes(container).filter(n => n.tag === 'input')
    inputs[0].props['onUpdate:modelValue']('330'); inputs[1].props['onUpdate:modelValue'](target); await tick()
  }
  return { wallet, props, calls, click, button, t, get reviewText() { return nodes(container).filter(n => n.tag === 'dl').map(text).join(' ') },
    setResolver: fn => { resolve = fn }, close: () => app.unmount() }
}
const f = await fixture()
await f.click(f.t('assetOperationDialog.confirm'))
for (const expected of ['330 sats', target, 'Bitcoin', 'testnet', '1 sats/vB']) assert.ok(f.reviewText.includes(expected), `review displays ${expected}`)
assert.equal(f.calls.length, 0)
await f.click(f.t('assetOperationDialog.cancel')); assert.equal(f.calls.length, 0)
await f.click(f.t('assetOperationDialog.confirm'))
const finalClick = f.button(f.t('assetOperationDialog.confirm')).props.onClick
finalClick(); finalClick(); await tick()
assert.deepEqual(f.calls, [{ toAddress: target, asset_name: '::', amt: '330' }])
f.close()
const l2 = await fixture(false, 'satoshinet')
await l2.click(l2.t('assetOperationDialog.confirm'))
for (const expected of ['330 sats', target, 'SatoshiNet', 'testnet', '10 sats', '340 sats']) assert.ok(l2.reviewText.includes(expected), `L2 review displays ${expected}`)
assert.equal(l2.calls.length, 0); l2.close()
for (const change of [f => { f.props.amount = '331' }, f => { f.props.address = target + 'x' }, f => { f.wallet.network = 'mainnet' }, f => { f.wallet.accountIndex++ }, f => { f.wallet.btcFeeRate = 2 }]) {
  const f = await fixture(true); await f.click(f.t('assetOperationDialog.confirm'))
  change(f); await tick(); assert.equal(f.reviewText, '', 'change returns to form'); assert.equal(f.calls.length, 0); f.close()
}
const race = await fixture(true)
let finish
race.setResolver(() => new Promise(r => { finish = r }))
const pending = race.click(race.t('assetOperationDialog.confirm'))
race.wallet.network = 'mainnet'; finish({ isDomain: false, resolvedAddress: target }); await pending
assert.equal(race.reviewText, ''); assert.equal(race.calls.length, 0); race.close()
const unresolved = await fixture(true)
unresolved.setResolver(async () => null)
await unresolved.click(unresolved.t('assetOperationDialog.confirm')); assert.equal(unresolved.reviewText, ''); assert.equal(unresolved.calls.length, 0); unresolved.close()
const domain = await fixture(true)
domain.props.address = 'fixture.sats'; domain.setResolver(async () => ({ isDomain: true, resolvedAddress: target, domainName: 'fixture.sats' }))
await tick(); await domain.click(domain.t('assetOperationDialog.confirm'))
assert.ok(domain.reviewText.includes(target)); assert.equal(domain.calls.length, 0); domain.close()
const token = await fixture(true)
token.props.assetKey = 'ordx:f:test'; token.props.assetType = 'f'; await tick()
assert.equal(token.button(token.t('assetOperationDialog.confirm')).props.disabled, true, 'unknown token precision stays blocked')
token.props.amountPrecision = 2; token.props.amount = '1.23'; token.props.assetTicker = 'TEST'; await tick()
await token.click(token.t('assetOperationDialog.confirm')); assert.ok(token.reviewText.includes('1.23 TEST')); token.close()
for (const lang of ['en', 'zh']) {
  const locale = JSON.parse(load(`locales/${lang}.json`)).assetOperationDialog
  for (const key of ['reviewChain', 'reviewNetwork', 'reviewFeeRate', 'reviewNetworkFee', 'reviewTotalSpend']) assert.ok(locale[key])
}
console.log('Real BalanceSummary + AssetOperationDialog review fields/cancel/single dispatch/context/precision checks passed')
