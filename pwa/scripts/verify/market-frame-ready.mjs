import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'
const timers = new Map(); let nextTimer = 0
const setTimeoutFake = (fn, ms) => { timers.set(++nextTimer, { fn, ms }); return nextTimer }
const clearTimeoutFake = (id) => timers.delete(id)
const transpile = (source) => ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const exports = {}
vm.runInNewContext(transpile(await readFile(new URL('../../utils/marketFrameReady.ts', import.meta.url), 'utf8')), { exports, URL, setTimeout: setTimeoutFake, clearTimeout: clearTimeoutFake })
const { MarketFrameReady } = exports
const origin = 'https://test-satsnet.ordx.market', href = origin + '/swap/?network=testnet'
const states = [], gate = new MarketFrameReady((s) => states.push(s)), frame = {}, old = {}
const event = (source = frame, overrides = {}, data = {}) => ({ source, origin, data: { protocol: 'sat20-dapp-connect', type: 'SAT20_DAPP_CLIENT_READY', origin, href, ...data }, ...overrides })
const allowed = (o) => o === origin
let generation = gate.begin(href); gate.bind(generation, frame)
assert.equal([...timers.values()][0].ms, 30000)
for (const e of [event(old), event(frame, { origin: 'https://evil.example' }), event(frame, {}, { origin: 'wrong' }), event(frame, {}, { href: 'https://evil.example/' }), event(frame, {}, { type: 'connect' }), event(frame, {}, { protocol: 'wrong' })]) assert.equal(gate.ready(e, allowed), false)
assert.equal(gate.ready(event(), () => false), false)
assert.equal(states.at(-1), 'loading')
const expired = [...timers.values()][0].fn; expired(); assert.equal(states.at(-1), 'timeout')
assert.equal(gate.ready(event(), allowed), true); assert.equal(states.at(-1), 'ready'); assert.equal(timers.size, 0)
generation = gate.begin(href); gate.bind(generation - 1, old)
assert.equal(gate.ready(event(old), allowed), false)
gate.bind(generation, frame); expired(); assert.equal(states.at(-1), 'loading')
gate.error(generation - 1); assert.equal(states.at(-1), 'loading')
gate.error(generation); assert.equal(states.at(-1), 'error'); assert.equal(timers.size, 0)
gate.begin(href); gate.dispose(); assert.equal(timers.size, 0); assert.equal(gate.ready(event(), allowed), false)

// Execute the actual component script with UI/bridge dependencies replaced, not mirrored handlers.
const vue = await readFile(new URL('../../entrypoints/popup/pages/wallet/DappMarket.vue', import.meta.url), 'utf8')
const script = vue.split('<script setup lang="ts">')[1].split('</script>')[0]
const ast = ts.createSourceFile('component.ts', script, ts.ScriptTarget.Latest, true)
const printer = ts.createPrinter()
const body = ast.statements.filter((s) => !ts.isImportDeclaration(s)).map((s) => printer.printNode(ts.EmitHint.Unspecified, s, ast)).join('\n').replaceAll('import.meta.env', 'environment')
const mounted = [], unmounted = [], watchers = []
const identitySinks = [], bridgeEvents = []
let pendingIdentityDisconnect = false
const registerReadyDappIdentitySink = (sinkOrigin, sink) => {
  const registration = { origin: sinkOrigin, sink, active: true }
  identitySinks.push(registration)
  return () => { registration.active = false }
}
const consumePendingDappIdentityDisconnect = () => {
  const pending = pendingIdentityDisconnect
  pendingIdentityDisconnect = false
  return pending
}
const assertBridgeDisconnect = () => {
  const [type, eventOrigin, payload] = bridgeEvents.at(-1)
  assert.equal(type, 'disconnect')
  assert.equal(eventOrigin, origin)
  assert.equal(payload.reason, 'wallet_scope_changed')
}
const context = { URL, console, MarketFrameReady, environment: { DEV: false }, SAT20_DAPP_PROTOCOL: 'sat20-dapp-connect', Network: { TESTNET: 'testnet' }, navigator: { onLine: true }, window: { addEventListener() {}, removeEventListener() {} },
 ref: (value) => ({ value }), computed: (get) => ({ get value() { return get() } }), watch: (source, fn) => watchers.push([source, fn]), onMounted: (fn) => mounted.push(fn), onBeforeUnmount: (fn) => unmounted.push(fn), useRouter: () => ({ push() {} }), useWalletStore: () => ({ network: 'testnet' }), usePwaDappBridge: () => ({ pendingRequests: { value: 0 }, isAllowedOrigin: allowed, announceReady() {}, announceEvent: (...args) => bridgeEvents.push(args), start() {}, stop() {} }), registerReadyDappIdentitySink, consumePendingDappIdentityDisconnect, getCurrentDappScope: async () => ({}), revokeDappGrant: async () => {} }
vm.createContext(context)
vm.runInContext(transpile(body + '\n;globalThis.ui = {frameRef, loading, loadError, readyTimedOut, handleLoad, handleError, handleClientReady, reload, loadHome, updateOnlineState, isOnline};'), context)
const ui = context.ui
const mountFrame = () => { const f = { contentWindow: {} }; ui.frameRef.value = f; watchers[0][1](f); return f }
let f = mountFrame(); mounted.forEach((fn) => fn())
ui.handleLoad({ target: f }); assert.equal(ui.loading.value, true)
;[...timers.values()][0].fn(); assert.equal(ui.readyTimedOut.value, true)
ui.handleLoad({ target: f }); assert.equal(ui.loadError.value, true)
await ui.handleClientReady(event(f.contentWindow)); assert.equal(ui.loading.value, false); assert.equal(ui.loadError.value, false)
assert.equal(identitySinks.length, 1); assert.equal(identitySinks[0].origin, origin); assert.equal(identitySinks[0].active, true)
identitySinks[0].sink({ reason: 'wallet_scope_changed' }); assertBridgeDisconnect()
const previous = f; ui.reload(); f = mountFrame()
assert.equal(identitySinks[0].active, false)
await ui.handleClientReady(event(previous.contentWindow)); assert.equal(ui.loading.value, true)
ui.handleError({ target: previous }); assert.equal(ui.loadError.value, false)
ui.handleError({ target: f }); assert.equal(ui.loadError.value, true)
ui.handleLoad({ target: f }); assert.equal(ui.loadError.value, true)
pendingIdentityDisconnect = true
await ui.handleClientReady(event(f.contentWindow)); assert.equal(ui.loadError.value, false)
assertBridgeDisconnect()
context.navigator.onLine = false; ui.updateOnlineState(); assert.equal(ui.isOnline.value, false)
ui.loadHome(); unmounted.forEach((fn) => fn()); assert.equal(timers.size, 0)
console.log('Market frame readiness lifecycle and component checks passed')
