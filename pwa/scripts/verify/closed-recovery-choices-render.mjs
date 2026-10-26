import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { parse, compileScript } from '@vue/compiler-sfc'
import * as V from 'vue'
import * as P from 'pinia'
import { tryit, parallel } from 'radash'

// Actual source SFCs, Pinia, callback, identity/session guard and STP facade.
// Only platform/UI leaf widgets and the WASM backend are adapters. This is a
// real Vue render/event integration control, not a browser or Bitcoin proof.
const renderer = V.createRenderer({
  createElement: type => ({ type, props: {}, children: [], parent: null }),
  createText: text => ({ type: '#text', text, children: [], parent: null }),
  createComment: text => ({ type: '#comment', text, children: [], parent: null }),
  setText(node, text) { node.text = text },
  setElementText(node, text) { node.text = text; node.children = [] },
  patchProp(node, key, previous, next) { node.props[key] = next },
  parentNode: node => node.parent,
  nextSibling(node) { if (!node.parent) return null; return node.parent.children[node.parent.children.indexOf(node) + 1] || null },
  insert(node, parent, anchor) {
    if (node.parent) { const index = node.parent.children.indexOf(node); if (index >= 0) node.parent.children.splice(index, 1) }
    node.parent = parent
    const index = anchor ? parent.children.indexOf(anchor) : -1
    if (index < 0) parent.children.push(node); else parent.children.splice(index, 0, node)
  },
  remove(node) { if (node.parent) { const index = node.parent.children.indexOf(node); if (index >= 0) node.parent.children.splice(index, 1); node.parent = null } },
})
const textOf = node => node.type === '#comment' ? '' : `${node.text || ''}${node.children.map(textOf).join('')}`
const find = (node, id) => node.props?.['data-testid'] === id ? node : node.children.map(child => find(child, id)).find(Boolean)
const flush = async () => { await new Promise(resolve => setImmediate(resolve)); await V.nextTick(); await new Promise(resolve => setImmediate(resolve)); await V.nextTick() }
const clickNode = async node => {
  assert.ok(node, 'the rendered button must exist')
  assert.equal(Boolean(node.props.disabled), false, 'never invoke a disabled button handler')
  const handlers = Array.isArray(node.props.onClick) ? node.props.onClick : [node.props.onClick]
  for (const handler of handlers) if (typeof handler === 'function') await handler({ preventDefault() {}, stopPropagation() {} })
  await flush()
}
const Wrapper = V.defineComponent({ inheritAttrs: false, setup(_, { attrs, slots }) { return () => V.h('div', attrs, slots.default?.()) } })
const Button = V.defineComponent({ inheritAttrs: false, props: { disabled: Boolean }, setup(props, { attrs, slots }) { return () => V.h('button', { ...attrs, disabled: props.disabled }, slots.default?.()) } })
const Dialog = V.defineComponent({ props: { open: Boolean }, setup(props, { slots }) { return () => props.open ? V.h('div', { 'data-dialog-adapter': true }, slots.default?.()) : null } })
const AssetList = V.defineComponent({ setup() { return () => V.h('div', { 'data-testid': 'l1-assets-page' }, 'L1 assets stay selected') } })

async function fixture({ offline = false, native = false, ready = false, deferQuote = false, deferChannel = false, noLocal = false, deferActions = false, nativeWithdraw = false, optionsWalletId = 11, quoteWalletId = 11, optionsAccountIndex = null, quoteAccountIndex = null } = {}) {
  const pinia = P.createPinia(); P.setActivePinia(pinia)
  const calls = [], notices = [], errors = [], callbacks = { current: null }, cache = new Map()
  const route = V.reactive({ query: { tab: 'l1' } })
  const root = { type: 'root', props: {}, children: [], parent: null }
  const sandbox = vm.createContext({ console: { log() {}, warn() {}, error() {} }, setTimeout, clearTimeout, crypto: globalThis.crypto })
  const useWalletStore = P.defineStore('wallet', () => ({ walletId: V.ref('11'), accountIndex: V.ref(0), network: V.ref('testnet'), btcFeeRate: V.ref(5), address: V.ref('fixture-address') }))
  const emptyStore = name => P.defineStore(name, () => ({ plainList: V.ref([]), sat20List: V.ref([]), runesList: V.ref([]), brc20List: V.ref([]), reset() {} }))
  const useL1Store = emptyStore('l1'), useL2Store = emptyStore('l2')
  const useGlobalStore = P.defineStore('global', () => ({ env: V.ref('prd') }))
  const useTranscendingModeStore = P.defineStore('transcending', () => ({ selectedTranscendingMode: V.ref('lightning') }))
  const wallet = useWalletStore()
  const state = { offline, native, ready, noLocal, deferChannel, parentId: '1'.repeat(64), noticeWalletId: 11, optionsWalletId, quoteWalletId, optionsAccountIndex, quoteAccountIndex, lastQuote: null, acceptedTopups: 0, acceptedWithdrawals: 0, channelRelease: null, quoteRelease: null, actionQueue: [] }
  const channelId = () => `closed-channel-${wallet.accountIndex}`
  const rawQuote = account => ` { "quoteId":"${'2'.repeat(64)}", "walletId":${JSON.stringify(state.quoteWalletId)}, "accountIndex":${state.quoteAccountIndex ?? account}, "channelId":"closed-channel-${account}", "amount":40000, "walletFee":770, "requiredInput":40770, "feeRate":${wallet.btcFeeRate}, "unsignedTx":"exact-unsigned-fixture" } `
  const record = (method, fn) => async (...args) => { calls.push({ method, args, accountAtCall: wallet.accountIndex, generationAtCall: identity.getWalletIdentityState().generation }); return fn(...args) }
  const actionResponse = (method, args, response) => {
    if (!deferActions) return response
    // The backend adapter captures the accepted original owner and exact request.
    // Switching the visible account must neither cancel nor replace this task.
    const owner = Object.freeze({ walletId: wallet.walletId, accountIndex: wallet.accountIndex, generation: identity.getWalletIdentityState().generation })
    const exactArgs = Object.freeze([...args])
    return new Promise((resolve, reject) => {
      const pending = { method, owner, exactArgs, outcome: 'pending',
        resolve() { assert.equal(pending.outcome, 'pending'); pending.outcome = 'resolved'; resolve(response) },
        reject() { assert.equal(pending.outcome, 'pending'); pending.outcome = 'rejected'; reject(new Error(`late ${method} backend failure for account ${owner.accountIndex}`)) },
      }
      state.actionQueue.push(pending)
    })
  }
  sandbox.sat20wallet_wasm = {
    registerCallback: record('registerCallback', callback => { callbacks.current = callback; return { code: 0 } }),
    getCurrentChannel: record('getCurrentChannel', async () => {
      if (state.deferChannel) await new Promise(resolve => { state.channelRelease = resolve })
      return { code: 0, data: state.ready ? { json: JSON.stringify({ channelId: channelId(), status: 16, localbalanceL1: [], capacity: 40000 }) } : undefined }
    }),
    allReservations: record('allReservations', () => ({ code: 0, data: { reservations: [] } })),
    switchAccount: record('switchAccount', account => { wallet.accountIndex = account; return { code: 0 } }),
    previewClosedChannelRecovery: record('previewClosedChannelRecovery', requested => ({ code: 0, data: JSON.stringify({ walletId: state.optionsWalletId, accountIndex: state.optionsAccountIndex ?? wallet.accountIndex, channelId: requested || channelId(), oldParentId: state.noLocal ? '' : state.parentId, hasLocalClosedState: !state.noLocal && !state.ready, canWithdraw: (!state.native || nativeWithdraw) && !state.ready && !state.noLocal, withdrawReason: state.noLocal ? 'Local recovery material is unavailable; connect online to rebuild or restore the required material' : state.native && !nativeWithdraw ? 'RGB/ARC/native withdrawal is not supported by this action' : '', withdrawFeeSats: 500, withdrawTxCount: 1, withdrawTxIds: ['3'.repeat(64)], withdrawPending: false, withdrawComplete: false }) })),
    quoteClosedChannelSingleUTXOTopup: record('quoteClosedChannelSingleUTXOTopup', async () => {
      const account = wallet.accountIndex
      if (deferQuote) await new Promise(resolve => { state.quoteRelease = resolve })
      state.lastQuote = rawQuote(account)
      return state.offline ? { code: -1, msg: 'server is offline; you may explicitly broadcast your local Others' } : { code: 0, data: state.lastQuote }
    }),
    confirmClosedChannelSingleUTXOTopup: record('confirmClosedChannelSingleUTXOTopup', quote => {
      const reviewed = JSON.parse(quote)
      if (reviewed.accountIndex !== wallet.accountIndex || reviewed.channelId !== channelId()) return { code: -1, msg: 'stale account' }
      state.acceptedTopups++
      return actionResponse('confirmClosedChannelSingleUTXOTopup', [quote], { code: 0, data: JSON.stringify({ txId: '4'.repeat(64), phase: 'broadcast' }) })
    }),
    withdrawClosedChannelExact: record('withdrawClosedChannelExact', (requested, parent) => {
      if ((state.native && !nativeWithdraw) || requested !== channelId() || parent !== state.parentId) return { code: -1, msg: 'reviewed parent/account/capability changed' }
      state.acceptedWithdrawals++
      return actionResponse('withdrawClosedChannelExact', [requested, parent], { code: 0, data: { resvId: '88' } })
    }),
  }
  let bundle
  const walletManager = { registerCallback: async callback => { callbacks.current = callback; return [undefined, {}] }, getTickerInfo: async () => [undefined, undefined] }
  function resolveModule(name) {
    if (name === 'vue') return V
    if (name === 'pinia') return P
    if (name === 'radash') return { tryit, parallel }
    if (name === '@/lib/identity-boundary' || name === './identity-boundary') return loadTS('lib/identity-boundary.ts')
    if (name === '@/lib/wire-wallet-id') return loadTS('lib/wire-wallet-id.ts')
    if (name === '@/lib/walletSession') return loadTS('lib/walletSession.ts')
    if (name === '@/utils/stp') return loadTS('utils/stp.ts')
    if (name === '@/utils/sat20') return { default: walletManager, __esModule: true }
    if (name === '@/utils/pwaVersionPolicy') return { beginVersionDispatch: () => () => {} }
    if (name === '@/utils/pwaOperationLog') return { beginPwaWalletOperation: async () => null, finishPwaOperation: async () => {} }
    if (name === '@/store') return bundle
    if (name === '@/store/global') return { useGlobalStore }
    if (name === '@/composables') return { useUnifiedAssets: () => ({ refreshL1Assets: async () => {} }), useL2Assets: () => ({ refreshL2Assets: async () => {} }) }
    if (name === '@/composables/useAssetOperations') return { useAssetOperations: () => ({}) }
    if (name === '@/composables/useNameManager') return { useNameManager: () => ({ currentName: V.ref('Fixture') }) }
    if (name === '@/composables/channelStatus') return { getChannelStatusText: () => 'closed' }
    if (name === '@/components/ui/toast-new') return { useToast: () => ({ toast: notice => notices.push(notice) }) }
    if (name === 'vue-router') return { useRoute: () => route, useRouter: () => ({ push() {}, replace() {} }) }
    if (name === '@/types/index') return { Chain: { BTC: 'btc', SATNET: 'satnet' } }
    if (name === '@/utils') return { hideAddress: value => String(value || ''), generateMempoolUrl: () => 'https://example.invalid/local-fixture' }
    if (name === '@iconify/vue') return { Icon: Wrapper }
    if (name === '@/components/ui/button') return { Button }
    if (name === '@/components/ui/dialog') return { Dialog, DialogContent: Wrapper, DialogHeader: Wrapper, DialogTitle: Wrapper, DialogDescription: Wrapper, DialogFooter: Wrapper }
    if (name === '@/components/wallet/ClosedChannelRecovery.vue') return { default: existsSync('components/wallet/ClosedChannelRecovery.vue') ? loadSFC('components/wallet/ClosedChannelRecovery.vue') : Wrapper, __esModule: true }
    if (name === '@/components/wallet/AssetList.vue') return { default: AssetList, __esModule: true }
    if (name.endsWith('.vue')) return { default: Wrapper, __esModule: true }
    if (name.startsWith('@/components/ui/')) return new Proxy({}, { get: () => Wrapper })
    throw new Error(`unmapped production import in interaction fixture: ${name}`)
  }
  function evaluate(source, filename) {
    const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS, esModuleInterop: true } }).outputText
    const module = { exports: {} }
    vm.runInContext(`(function(exports, require, module) { ${code}\n})`, sandbox, { filename })(module.exports, resolveModule, module)
    return module.exports
  }
  function loadTS(filename) { if (!cache.has(filename)) cache.set(filename, evaluate(readFileSync(filename, 'utf8'), filename)); return cache.get(filename) }
  function loadSFC(filename) {
    if (!cache.has(filename)) {
      const parsed = parse(readFileSync(filename, 'utf8'), { filename })
      assert.equal(parsed.errors.length, 0, `${filename} must parse as a real SFC`)
      const compiled = compileScript(parsed.descriptor, { id: filename.replace(/\W/g, '_'), inlineTemplate: true })
      cache.set(filename, evaluate(compiled.content, filename).default)
    }
    return cache.get(filename)
  }
  const identity = loadTS('lib/identity-boundary.ts')
  const session = loadTS('lib/walletSession.ts'); session.setWalletSessionUnlocked(true)
  const facade = loadTS('utils/stp.ts').default
  const useChannelStore = loadTS('store/channel.ts').useChannelStore
  bundle = { useWalletStore, useChannelStore, useL1Store, useL2Store, useTranscendingModeStore, useGlobalStore }
  const app = renderer.createApp(loadSFC('entrypoints/popup/pages/wallet/index.vue'))
  app.use(pinia); app.config.globalProperties.$t = key => key; app.config.errorHandler = error => errors.push(error); app.config.warnHandler = () => {}
  app.mount(root); await flush()
  const financialCalls = () => calls.filter(call => ['confirmClosedChannelSingleUTXOTopup', 'withdrawClosedChannelExact'].includes(call.method))
  const emitRaw = async payload => {
    assert.equal(typeof callbacks.current, 'function', 'actual wallet page must register the SDK callback')
    await callbacks.current('channelrebuildfundingrequired', payload)
    await flush()
  }
  const emit = async (account = wallet.accountIndex, owner = state.noticeWalletId) => {
    await emitRaw(JSON.stringify({ walletId: owner, accountIndex: account, channelId: `closed-channel-${account}`, message: state.ready ? 'channel READY; asset restoration paused for separate stub approval' : 'rebuild funding requires approval' }))
  }
  const switchAccount = async account => {
    const generation = identity.beginWalletIdentityTransition()
    identity.setWalletIdentityPhase(generation, 'REBUILDING')
    const [switchError] = await facade.switchAccount(account)
    assert.equal(switchError, undefined, 'fixture identity switch must reach the real facade/backend successfully')
    identity.setWalletIdentityPhase(generation, 'VERIFYING')
    identity.completeWalletIdentityTransition(generation)
    await flush()
  }
  return { root, wallet, state, calls, notices, errors, facade, identity, emit, emitRaw, switchAccount, financialCalls,
    channelStore: useChannelStore(), wireWalletIdToStoreId: existsSync('lib/wire-wallet-id.ts') ? loadTS('lib/wire-wallet-id.ts').wireWalletIdToStoreId : null,
    node: id => find(root, id), text: () => textOf(root),
    async click(id) { await clickNode(find(root, id)); assert.deepEqual(errors, [], 'render/event execution must not hide Vue errors') },
    dispose() { app.unmount(); if (typeof P.disposePinia === 'function') P.disposePinia(pinia) },
  }
}

// No heartbeat/server notification is required to expose verified local
// closed-state choices on L1. Startup itself only previews and never opens.
const localStart = await fixture({ offline: true })
assert.ok(localStart.node('closed-recovery-card'), 'offline no-notice startup must expose local recovery outside Tabs')
assert.equal(localStart.node('closed-recovery-dialog'), undefined, 'default local capability must not open a popup')
assert.ok(localStart.calls.some(call => call.method === 'previewClosedChannelRecovery' && call.args[0] === ''), 'startup must use the captured-account default locator through the real facade')
assert.equal(localStart.calls.some(call => call.method === 'quoteClosedChannelSingleUTXOTopup'), false, 'startup capability is not permission to obtain a payment review')
assert.equal(localStart.financialCalls().length, 0)
await localStart.click('recovery-choices-open'); assert.ok(localStart.node('closed-recovery-dialog'))
assert.equal(Boolean(localStart.node('withdraw-choice').props.disabled), false, 'verified local ordinary package remains explicitly selectable offline')
await localStart.click('withdraw-choice'); assert.equal(localStart.financialCalls().length, 0)
await localStart.click('withdraw-confirm'); assert.deepEqual(localStart.financialCalls().map(call => call.method), ['withdrawClosedChannelExact'])
assert.deepEqual(localStart.financialCalls()[0].args, ['closed-channel-0', '1'.repeat(64)])
localStart.dispose()
const missingStart = await fixture({ offline: true, noLocal: true })
assert.match(missingStart.text(), /material.*unavailable|online/i, 'first restoration without a real local package must explain its limitation')
assert.equal(missingStart.node('closed-recovery-dialog'), undefined)
assert.equal(Boolean(missingStart.node('recovery-choices-open')?.props.disabled), true, 'missing local state cannot offer an actionable closed recovery review')
assert.equal(missingStart.financialCalls().length, 0); missingStart.dispose()

const rebuild = await fixture(); await rebuild.emit()
assert.ok(rebuild.node('l1-assets-page'), 'the user remains on L1 rather than requiring a Channel tab')
assert.ok(rebuild.node('closed-recovery-card'), 'closed/null active channel must expose recovery outside Tabs')
assert.ok(rebuild.node('closed-recovery-dialog'), 'matching background event must actually open the recovery choice dialog')
assert.match(rebuild.text(), /40000/); assert.match(rebuild.text(), /770/); assert.match(rebuild.text(), /40770/)
assert.equal(rebuild.financialCalls().length, 0, 'notification/preview must not act')
const rebuildButton = rebuild.node('rebuild-confirm'); await Promise.all([clickNode(rebuildButton), clickNode(rebuildButton)])
assert.equal(rebuild.state.acceptedTopups, 1); assert.equal(rebuild.state.acceptedWithdrawals, 0)
assert.equal(rebuild.financialCalls().length, 1); assert.equal(rebuild.financialCalls()[0].method, 'confirmClosedChannelSingleUTXOTopup')
assert.equal(rebuild.financialCalls()[0].args[0], rebuild.state.lastQuote, 'confirm must forward the exact quoted bytes, including spaces and property order')
rebuild.dispose()

// The PWA store holds string IDs, while all three SDK JSON entry points hold
// numeric IDs. A string disguised as a wire ID, or a rounded/invalid number,
// must never pass the common identity boundary.
const invalidWireIds = ['11', -1, -0, 1.5, NaN, Infinity, 9007199254740992, null, {}]
const boundary = await fixture()
assert.equal(boundary.wireWalletIdToStoreId(0), '0')
assert.equal(boundary.wireWalletIdToStoreId(11), '11')
for (const id of invalidWireIds) assert.equal(boundary.wireWalletIdToStoreId(id), null, `invalid wire wallet ID ${String(id)} must fail closed`)
boundary.dispose()
for (const invalid of ['11', -1, 1.5, NaN, 9007199254740992, null]) {
  const noticeCase = await fixture()
  await noticeCase.emit(0, invalid)
  assert.equal(noticeCase.channelStore.closedRebuildFundingNotice, null, 'invalid numeric SDK notice cannot enter the UI store')
  assert.equal(noticeCase.calls.some(call => call.method === 'getCurrentChannel'), false, 'invalid notice cannot start a channel refresh')
  assert.equal(noticeCase.financialCalls().length, 0); noticeCase.dispose()

  const optionsCase = await fixture({ optionsWalletId: invalid })
  await optionsCase.emit()
  assert.equal(optionsCase.calls.some(call => call.method === 'quoteClosedChannelSingleUTXOTopup'), false, 'invalid SDK options cannot request a funding quote')
  assert.ok(optionsCase.node('closed-recovery-dialog'), 'invalid options may be explained in an error dialog')
  assert.equal(Boolean(optionsCase.node('rebuild-confirm')?.props.disabled), true)
  assert.equal(Boolean(optionsCase.node('withdraw-choice')?.props.disabled), true)
  assert.equal(optionsCase.financialCalls().length, 0); optionsCase.dispose()

  const quoteCase = await fixture({ quoteWalletId: invalid })
  await quoteCase.emit()
  assert.equal(Boolean(quoteCase.node('rebuild-confirm')?.props.disabled), true, 'invalid SDK quote cannot enable rebuild')
  assert.equal(quoteCase.financialCalls().length, 0); quoteCase.dispose()
}
const wrongOptionsAccount = await fixture({ optionsAccountIndex: 1 })
await wrongOptionsAccount.emit()
assert.equal(wrongOptionsAccount.calls.some(call => call.method === 'quoteClosedChannelSingleUTXOTopup'), false)
assert.equal(wrongOptionsAccount.financialCalls().length, 0); wrongOptionsAccount.dispose()
const wrongQuoteAccount = await fixture({ quoteAccountIndex: 1 })
await wrongQuoteAccount.emit()
assert.equal(Boolean(wrongQuoteAccount.node('rebuild-confirm')?.props.disabled), true)
assert.equal(wrongQuoteAccount.financialCalls().length, 0); wrongQuoteAccount.dispose()
const wrongNoticeWallet = await fixture()
await wrongNoticeWallet.emit(0, 22)
assert.equal(wrongNoticeWallet.channelStore.closedRebuildFundingNotice, null)
assert.equal(wrongNoticeWallet.financialCalls().length, 0); wrongNoticeWallet.dispose()
for (const route of ['AB', 'ABA', 'generation-only']) {
  const staleNotice = await fixture({ deferChannel: true })
  const pendingNotice = staleNotice.emit()
  await flush()
  assert.equal(typeof staleNotice.state.channelRelease, 'function', 'notification must be suspended during the channel refresh')
  if (route === 'AB' || route === 'ABA') await staleNotice.switchAccount(1)
  if (route === 'ABA' || route === 'generation-only') await staleNotice.switchAccount(0)
  staleNotice.state.channelRelease(); await pendingNotice
  assert.equal(staleNotice.channelStore.closedRebuildFundingNotice, null, 'stale notification cannot enter the current account after an identity transition')
  assert.equal(staleNotice.node('closed-recovery-dialog'), undefined)
  assert.equal(staleNotice.financialCalls().length, 0); staleNotice.dispose()
}
const mutableNotice = await fixture({ deferChannel: true })
const callbackObject = { walletId: 11, accountIndex: 0, channelId: 'closed-channel-0', message: 'original notice' }
const pendingMutableNotice = mutableNotice.emitRaw(callbackObject)
await flush()
assert.equal(typeof mutableNotice.state.channelRelease, 'function')
callbackObject.walletId = 22; callbackObject.accountIndex = 1; callbackObject.channelId = 'other'; callbackObject.message = 'mutated notice'
mutableNotice.state.channelRelease(); await pendingMutableNotice
assert.equal(mutableNotice.channelStore.closedRebuildFundingNotice?.walletId, '11')
assert.equal(mutableNotice.channelStore.closedRebuildFundingNotice?.accountIndex, 0)
assert.equal(mutableNotice.channelStore.closedRebuildFundingNotice?.channelId, 'closed-channel-0')
assert.equal(mutableNotice.channelStore.closedRebuildFundingNotice?.message, 'original notice')
assert.equal(mutableNotice.financialCalls().length, 0); mutableNotice.dispose()

const withdraw = await fixture({ offline: true }); await withdraw.emit()
assert.ok(withdraw.node('closed-recovery-dialog')); assert.match(withdraw.text(), /offline/i)
assert.equal(Boolean(withdraw.node('rebuild-confirm').props.disabled), true)
assert.equal(withdraw.financialCalls().length, 0, 'offline must remain a suggestion, never automatic withdrawal')
await withdraw.click('withdraw-choice'); assert.ok(withdraw.node('withdraw-review')); assert.match(withdraw.text(), /500/)
assert.equal(withdraw.financialCalls().length, 0, 'choice/review is not permission to broadcast yet')
const withdrawButton = withdraw.node('withdraw-confirm'); await Promise.all([clickNode(withdrawButton), clickNode(withdrawButton)])
assert.equal(withdraw.state.acceptedWithdrawals, 1); assert.equal(withdraw.state.acceptedTopups, 0)
assert.deepEqual(withdraw.financialCalls().map(call => call.method), ['withdrawClosedChannelExact'])
assert.deepEqual(withdraw.financialCalls()[0].args, ['closed-channel-0', '1'.repeat(64)])
withdraw.dispose()

const native = await fixture({ offline: true, native: true }); await native.emit()
assert.equal(Boolean(native.node('withdraw-choice').props.disabled), true); assert.match(native.text(), /RGB\/ARC\/native/)
await native.click('recovery-cancel'); assert.equal(native.financialCalls().length, 0); native.dispose()

const routed = await fixture(); await routed.switchAccount(1); await routed.emit(0)
assert.equal(routed.node('closed-recovery-dialog'), undefined, 'old account notification must not open a dialog')
await routed.emit(1); assert.ok(routed.node('closed-recovery-dialog')); assert.match(routed.text(), /Account: 1/)
await routed.switchAccount(2); assert.equal(routed.node('closed-recovery-dialog'), undefined)
assert.equal(routed.financialCalls().length, 0); routed.dispose()

for (const change of [f => { f.wallet.btcFeeRate++ }, f => { f.wallet.network = 'mainnet' }, f => { f.wallet.walletId = '22' }]) {
  const f = await fixture(); await f.emit(); const stale = f.node('rebuild-confirm'); change(f); await flush()
  assert.equal(f.node('closed-recovery-dialog'), undefined, 'changed context must close old approval')
  await clickNode(stale); assert.equal(f.financialCalls().length, 0); f.dispose()
}
const aba = await fixture({ deferQuote: true }); await aba.emit(); assert.equal(typeof aba.state.quoteRelease, 'function')
await aba.switchAccount(1); await aba.switchAccount(0); aba.state.quoteRelease(); await flush()
assert.equal(aba.node('closed-recovery-dialog'), undefined, 'ABA/stale quote must not reopen approval'); assert.equal(aba.financialCalls().length, 0); aba.dispose()

// Same-account duplicate notifications while a readonly quote is pending
// are coalesced. They must not be used as a reason to add query/reentry.
const sameAccountNotice = await fixture({ deferQuote: true }); await sameAccountNotice.emit(); await sameAccountNotice.emit()
assert.equal(sameAccountNotice.wallet.accountIndex, 0)
assert.equal(sameAccountNotice.calls.filter(call => call.method === 'quoteClosedChannelSingleUTXOTopup').length, 1, 'same-account pending quote notification must stay coalesced')
sameAccountNotice.state.quoteRelease(); await flush()
assert.ok(sameAccountNotice.node('closed-recovery-dialog')); assert.equal(sameAccountNotice.financialCalls().length, 0); sameAccountNotice.dispose()

// A new-account notification must not disappear behind the old account's
// outstanding read-only quote. Both deferred answers remain identity-scoped.
const nextAccount = await fixture({ deferQuote: true }); await nextAccount.emit()
const releaseOldAccount = nextAccount.state.quoteRelease
await nextAccount.switchAccount(1)
assert.equal(nextAccount.wallet.accountIndex, 1, 'this case must really be a different selected account, not duplicate-account dedup')
assert.equal(nextAccount.identity.getWalletIdentityState().phase, 'READY')
assert.ok(nextAccount.calls.some(call => call.method === 'switchAccount' && call.args[0] === 1))
assert.equal(nextAccount.calls.find(call => call.method === 'quoteClosedChannelSingleUTXOTopup').accountAtCall, 0, 'old readonly quote must have started on account0')
await nextAccount.emit(1)
releaseOldAccount(); await flush()
console.log('pending-quote identity evidence', JSON.stringify({ currentAccount: nextAccount.wallet.accountIndex, identity: nextAccount.identity.getWalletIdentityState(), quoteAccounts: nextAccount.calls.filter(call => call.method === 'quoteClosedChannelSingleUTXOTopup').map(call => call.accountAtCall), switchArgs: nextAccount.calls.filter(call => call.method === 'switchAccount').map(call => call.args) }))
assert.equal(nextAccount.calls.filter(call => call.method === 'quoteClosedChannelSingleUTXOTopup').length, 2, 'current-account notification must issue its own readonly review while old quote is pending')
nextAccount.state.quoteRelease(); await flush()
assert.ok(nextAccount.node('closed-recovery-dialog'), 'current-account notification must open the numerical review')
assert.match(nextAccount.text(), /Account: 1/)
assert.equal(nextAccount.financialCalls().length, 0, 'routing a notification is never payment approval')
nextAccount.dispose()

const cancelled = await fixture(); await cancelled.emit(); await cancelled.click('recovery-cancel')
assert.equal(cancelled.node('closed-recovery-dialog'), undefined); assert.equal(cancelled.financialCalls().length, 0); cancelled.dispose()
const closed = await fixture(); await closed.emit(); const staleAfterUnmount = closed.node('rebuild-confirm'); closed.dispose(); await clickNode(staleAfterUnmount)
assert.equal(closed.financialCalls().length, 0, 'unmounted approval must not act')
const ready = await fixture({ ready: true }); await ready.emit()
assert.ok(ready.node('closed-recovery-card')); assert.equal(ready.node('closed-recovery-dialog'), undefined)
assert.equal(ready.calls.some(call => call.method === 'quoteClosedChannelSingleUTXOTopup'), false, 'READY missing-stub notice cannot offer full closed-channel funding')
assert.equal(ready.financialCalls().length, 0); ready.dispose()
console.log('Closed recovery original rendered interaction: PASS (L1/null visibility, actual SDK callback/store/facade, two explicit choices, offline no automatic withdrawal, unsupported native disabled, account/generation/fee/network/ABA/unmount, double confirm)')


// Approved F3 regression controls. These mount the same production wallet SFC,
// invoke its registered SDK callback, and exercise the actual Pinia channel
// store, identity generation guard and STP facade. The WASM backend is the only
// deferred financial boundary here; this is not a browser or SDK funding test.
const actionKinds = ['topup', 'withdrawal']
const actionMethod = kind => kind === 'topup' ? 'confirmClosedChannelSingleUTXOTopup' : 'withdrawClosedChannelExact'
const confirmId = kind => kind === 'topup' ? 'rebuild-confirm' : 'withdraw-confirm'
const actionCaseFilter = process.argv.find(arg => arg.startsWith('--f3-case='))?.slice('--f3-case='.length)
const actionFailures = [], actionResults = []
async function actionCase(label, body) {
  if (actionCaseFilter && !label.includes(actionCaseFilter)) return
  try { await body(); actionResults.push({ label, pass: true }); console.log(`F3 action isolation PASS: ${label}`) }
  catch (error) { const detail = String(error.stack || error); actionFailures.push({ label, detail }); actionResults.push({ label, pass: false }); console.log(`F3 action isolation FAIL: ${label}\n${detail}`) }
}
async function beginAction(f, kind) {
  if (!f.node('closed-recovery-dialog')) await f.click('recovery-choices-open')
  assert.ok(f.node('closed-recovery-dialog'), 'current account must be able to review while an older account action is pending')
  if (kind === 'withdrawal') {
    const before = f.financialCalls().length
    await f.click('withdraw-choice')
    assert.equal(f.financialCalls().length, before, 'withdrawal review still cannot broadcast')
  }
  const button = f.node(confirmId(kind))
  assert.ok(button, 'the current generation must render its own confirmation button')
  const before = f.financialCalls().length
  const completion = f.click(confirmId(kind))
  await flush()
  assert.equal(f.financialCalls().length, before + 1, 'one explicit confirmation dispatches exactly one captured financial request')
  const task = f.state.actionQueue.at(-1)
  assert.equal(task.method, actionMethod(kind))
  assert.equal(task.outcome, 'pending')
  // A stale DOM reference can still deliver a second click before or after a
  // rerender. The current action's synchronous guard must prevent dispatch.
  await clickNode(button)
  assert.equal(f.financialCalls().length, before + 1, 'same-generation pending action must suppress a repeated confirmation')
  return { completion, task, button }
}
async function changeAccount(f, route) {
  const originalGeneration = f.identity.getWalletIdentityState().generation
  if (route === 'AB' || route === 'ABA') await f.switchAccount(1)
  if (route === 'ABA' || route === 'generation-only') await f.switchAccount(0)
  assert.equal(f.wallet.accountIndex, route === 'AB' ? 1 : 0, 'the fixture must actually select the claimed account')
  assert.equal(f.identity.getWalletIdentityState().phase, 'READY')
  assert.ok(f.identity.getWalletIdentityState().generation > originalGeneration, 'ABA and same-account identity replacement must use a new real identity generation')
  const switches = f.calls.filter(call => call.method === 'switchAccount').map(call => call.args[0])
  assert.deepEqual(switches, route === 'AB' ? [1] : route === 'ABA' ? [1, 0] : [0])
}
async function cleanupActions(f) {
  for (const task of f.state.actionQueue) if (task.outcome === 'pending') task.resolve()
  await flush()
  f.dispose()
}
for (const kind of actionKinds) for (const route of ['AB', 'ABA', 'generation-only']) for (const outcome of ['resolve', 'reject']) {
  await actionCase(`${kind} ${route} late ${outcome} cannot leak busy/error/toast`, async () => {
    const f = await fixture({ deferActions: true })
    try {
      await f.emit()
      const old = await beginAction(f, kind)
      const ownerBefore = JSON.stringify(old.task.owner), exactBefore = JSON.stringify(old.task.exactArgs)
      await changeAccount(f, route)
      const busyAfterSwitch = Boolean(f.node('recovery-choices-open')?.props.disabled)
      assert.equal(old.task.outcome, 'pending', 'UI invalidation cannot cancel an already approved original-owner backend task')
      const noticesBefore = f.notices.length
      old.task[outcome](); await old.completion; await flush()
      const evidence = { busyAfterSwitch, busyAfterCompletion: Boolean(f.node('recovery-choices-open')?.props.disabled),
        newNotices: f.notices.length - noticesBefore, staleOutcomeVisible: /Wallet session changed|late .*backend failure|Funding recorded|Withdrawal .*submitted/i.test(f.text()),
        actionCount: f.financialCalls().length, originalOwner: old.task.owner.accountIndex }
      console.log('F3 late-action identity evidence', JSON.stringify({ kind, route, outcome, generation: f.identity.getWalletIdentityState().generation, currentAccount: f.wallet.accountIndex, originalGeneration: old.task.owner.generation, ...evidence }))
      assert.deepEqual(evidence, { busyAfterSwitch: false, busyAfterCompletion: false, newNotices: 0, staleOutcomeVisible: false, actionCount: 1, originalOwner: 0 })
      assert.equal(JSON.stringify(old.task.owner), ownerBefore, 'original SDK-adapter owner remains exact')
      assert.equal(JSON.stringify(old.task.exactArgs), exactBefore, 'original accepted quote/package is never replaced')
      assert.deepEqual(f.errors, [])
    } finally { await cleanupActions(f) }
  })
}
// Completing an older request must not release a newer request's shared busy
// state, including cross-kind top-up/withdrawal replacement after invalidation.
for (const oldKind of actionKinds) for (const newKind of actionKinds) for (const route of ['AB', 'ABA']) for (const outcome of ['resolve', 'reject']) {
  await actionCase(`${oldKind} ${route} late ${outcome} keeps newer ${newKind} busy`, async () => {
    const f = await fixture({ deferActions: true })
    try {
      await f.emit()
      const old = await beginAction(f, oldKind)
      await changeAccount(f, route)
      const next = await beginAction(f, newKind)
      assert.equal(next.task.owner.accountIndex, f.wallet.accountIndex)
      assert.equal(next.task.owner.generation, f.identity.getWalletIdentityState().generation)
      assert.notEqual(next.task.owner.generation, old.task.owner.generation)
      const noticesBefore = f.notices.length
      old.task[outcome](); await old.completion; await flush()
      assert.equal(next.task.outcome, 'pending', 'old completion cannot cancel/settle the approved new request')
      assert.equal(Boolean(f.node('recovery-choices-open')?.props.disabled), true, 'old finally must not clear the current action busy token')
      assert.equal(f.notices.length, noticesBefore, 'old completion cannot toast over a newer action')
      assert.doesNotMatch(f.text(), /Wallet session changed|late .*backend failure/i)
      await clickNode(next.button)
      assert.equal(f.financialCalls().length, 2, 'old finally cannot permit a duplicate current action')
      next.task.resolve(); await next.completion; await flush()
      assert.equal(Boolean(f.node('recovery-choices-open')?.props.disabled), false, 'current completion releases only its own busy state')
      assert.equal(f.notices.at(-1)?.title, newKind === 'topup' ? 'Funding recorded' : 'Withdrawal submitted', 'only current-generation success is visible')
      assert.deepEqual(f.financialCalls().map(call => call.method), [actionMethod(oldKind), actionMethod(newKind)])
      assert.deepEqual(f.errors, [])
    } finally { await cleanupActions(f) }
  })
}
for (const kind of actionKinds) for (const outcome of ['resolve', 'reject']) {
  await actionCase(`${kind} current ${outcome} remains visible and releases busy`, async () => {
    const f = await fixture({ deferActions: true })
    try {
      await f.emit()
      const own = await beginAction(f, kind)
      const noticesBefore = f.notices.length
      assert.equal(Boolean(f.node('recovery-choices-open')?.props.disabled), true)
      own.task[outcome](); await own.completion; await flush()
      assert.equal(Boolean(f.node('recovery-choices-open')?.props.disabled), false)
      assert.equal(f.notices.length, noticesBefore + 1, 'valid current action keeps its success/error notification')
      assert.equal(f.notices.at(-1)?.title, outcome === 'resolve' ? (kind === 'topup' ? 'Funding recorded' : 'Withdrawal submitted') : (kind === 'topup' ? 'Rebuild paused' : 'Withdrawal paused'))
      if (outcome === 'reject') assert.match(f.text(), /late .*backend failure/)
      assert.equal(f.financialCalls().length, 1)
      assert.deepEqual(f.errors, [])
    } finally { await cleanupActions(f) }
  })
}
for (const kind of actionKinds) for (const field of ['btcFeeRate', 'network', 'walletId']) {
  await actionCase(`${kind} pending ${field} context replacement isolates callbacks`, async () => {
    const f = await fixture({ deferActions: true })
    try {
      await f.emit()
      const old = await beginAction(f, kind)
      if (field === 'network') f.wallet.network = 'mainnet'
      else if (field === 'walletId') f.wallet.walletId = '22'
      else f.wallet[field]++
      await flush()
      const busyAfterChange = Boolean(f.node('recovery-choices-open')?.props.disabled)
      const noticesBefore = f.notices.length
      old.task.reject(); await old.completion; await flush()
      assert.equal(busyAfterChange, false, 'changed review context cannot stay blocked behind an older action')
      assert.equal(f.notices.length, noticesBefore)
      assert.doesNotMatch(f.text(), /late .*backend failure/)
      assert.equal(f.financialCalls().length, 1)
    } finally { await cleanupActions(f) }
  })
}
for (const kind of actionKinds) for (const outcome of ['resolve', 'reject']) {
  await actionCase(`${kind} pending unmount late ${outcome} stays silent`, async () => {
    const f = await fixture({ deferActions: true })
    await f.emit()
    const old = await beginAction(f, kind)
    const noticesBefore = f.notices.length
    f.dispose()
    assert.equal(old.task.outcome, 'pending', 'unmount does not cancel an accepted original-owner funding task')
    old.task[outcome](); await old.completion; await flush()
    assert.equal(f.notices.length, noticesBefore)
    assert.equal(f.financialCalls().length, 1)
    assert.deepEqual(f.errors, [])
  })
}
await actionCase('native positive adapter capability still requires both explicit withdrawal choices', async () => {
  const f = await fixture({ offline: true, native: true, nativeWithdraw: true })
  try {
    await f.emit()
    assert.equal(Boolean(f.node('withdraw-choice')?.props.disabled), false)
    assert.equal(f.financialCalls().length, 0)
    await f.click('withdraw-choice')
    assert.ok(f.node('withdraw-review'))
    assert.equal(f.financialCalls().length, 0)
    await f.click('withdraw-confirm')
    assert.equal(f.state.acceptedWithdrawals, 1)
    assert.deepEqual(f.financialCalls()[0].args, ['closed-channel-0', '1'.repeat(64)])
  } finally { f.dispose() }
})
// RGB and ARC share the SDK's protocol-neutral capability fields. These
// labelled adapter controls consume canWithdraw=true and distinct exact parent
// locators through the real mounted page/callback/store/facade. They do not
// validate native transactions or claim a live browser / real-wallet E2E.
for (const protocol of ['RGB', 'ARC']) {
  await actionCase(`${protocol} canWithdraw capability adapter requires both explicit withdrawal choices`, async () => {
    const f = await fixture({ native: true, nativeWithdraw: true })
    try {
      const reviewedParent = (protocol === 'RGB' ? '5' : '6').repeat(64)
      f.state.parentId = reviewedParent
      await f.emit()
      assert.ok(f.node('closed-recovery-dialog'), 'the SDK callback must expose the current account capability review')
      assert.equal(Boolean(f.node('withdraw-choice')?.props.disabled), false, 'the UI must consume the positive SDK withdrawal capability')
      assert.match(f.text(), /Your old signed recovery package does not protect this new money/)
      assert.equal(f.financialCalls().length, 0, 'a native capability callback and quote never authorize a financial action')
      await f.click('withdraw-choice')
      assert.ok(f.node('withdraw-review'))
      assert.match(f.text(), /complete already-signed local recovery package/)
      assert.doesNotMatch(f.text(), /ordinary-asset Others|old Others package/)
      assert.equal(f.financialCalls().length, 0, 'the first explicit native withdrawal choice only opens its review')
      await f.click('withdraw-confirm')
      assert.equal(f.state.acceptedWithdrawals, 1)
      assert.equal(f.state.acceptedTopups, 0)
      assert.deepEqual(f.financialCalls().map(call => call.method), ['withdrawClosedChannelExact'])
      assert.deepEqual(f.financialCalls()[0].args, ['closed-channel-0', reviewedParent])
      assert.equal(f.notices.at(-1)?.title, 'Withdrawal submitted')
      assert.deepEqual(f.errors, [])
    } finally { f.dispose() }
  })
}
assert.ok(actionResults.length > 0, 'the optional --f3-case filter must match at least one action regression')
console.log('F3 action isolation matrix', JSON.stringify(actionResults))
assert.deepEqual(actionFailures, [], 'all current-action account/generation/token isolation controls must pass')
console.log('Closed recovery action isolation: PASS (real mounted Vue/callback/Pinia/facade, account+generation, late resolve/reject, cross-kind token/finally, current double-click, context/unmount, explicit native-capability adapter)')
