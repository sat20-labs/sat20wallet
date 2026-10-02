import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { transformSync } from 'esbuild'
import { tryit } from 'radash'
import ts from 'typescript'

function load(source, deps = {}, globals = {}) {
  const module = { exports: {} }
  runInNewContext(transformSync(source, { loader: 'ts', format: 'cjs', target: 'es2022' }).code, {
    module, exports: module.exports, Error, crypto, console: { error() {} },
    require(name) { assert.ok(name in deps, `missing ${name}`); return deps[name] }, ...globals,
  })
  return module.exports
}
const source = path => readFileSync(new URL(path, import.meta.url), 'utf8')
const script = source('../../components/wallet/RGB11InvoiceDialog.vue').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
const ast = ts.createSourceFile('dialog.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
let generateSource
ast.forEachChild(node => {
  if (ts.isVariableStatement(node)) for (const d of node.declarationList.declarations) {
    if (d.name.getText(ast) === 'generateInvoice') generateSource = d.initializer.getText(ast)
  }
})
assert.ok(generateSource)

for (const stage of ['none', 'before_dispatch', 'native_pending', 'finish_log']) {
  test(`receive handle survives session change: ${stage}`, async () => {
    const identity = load(source('../../lib/identity-boundary.ts'))
    const session = load(source('../../lib/walletSession.ts'), { './identity-boundary': identity })
    session.setWalletSessionUnlocked(true)
    const saved = new Map(), nativeRequests = new Map()
    const key = 'rgb11:receive:v1:["test","testnet","root","wallet",1,"rgb:fixture"]'
    const ref = value => ({ value })
    const scopeKey = ref(key)
    const lock = () => { identity.beginWalletIdentityTransition(); session.setWalletSessionUnlocked(false); scopeKey.value = '' }
    let entered, resume
    const atNative = new Promise(resolve => { entered = resolve })
    const nativeResult = new Promise(resolve => { resume = resolve })
    let calls = 0
    const logs = {
      beginPwaWalletOperation: async () => { if (stage === 'before_dispatch') lock() },
      finishPwaOperation: async (_op, failure) => { if (!failure && stage === 'finish_log') lock() },
    }
    const walletManager = load(source('../../utils/sat20.ts'), {
      radash: { tryit }, '@/lib/walletSession': session, '@/utils/pwaOperationLog': logs,
      '@/utils/pwaVersionPolicy': { beginVersionDispatch: () => () => {} },
      '@/utils/walletAwaitTrace': { beginWalletAwaitTrace: () => () => {} },
    }, { sat20wallet_wasm: { createRGB11Invoice: async () => {
      calls++
      // Only the external WASM boundary is mocked. The actual SDK persists
      // receive+reservation before success; the wrapper and UI below are real.
      nativeRequests.set('request-one', { reserved: true })
      entered()
      return nativeResult
    } } }).default
    const globals = {
      restoring: ref(false), restoreFailed: ref(false), scopeKey, requestId: ref(''),
      assetContractID: ref('rgb:fixture'), errorMessage: ref(''), t: x => x,
      props: { asset: { ticker: 'fixture', precision: 0 } }, amount: ref('5'), decimalToRaw: () => '5',
      proxyEndpoint: ref('rpc://fixture.invalid'), proxyStorageKey: () => 'proxy-key',
      transportMode: ref('rgb-json-rpc'), loading: ref(false), receiveMode: ref('blind'), walletManager,
      Storage: { set: async ({ key, value }) => saved.set(key, value) },
      phase: ref('created'), sdkTerminalStatus: ref(''), expiresAt: ref(0), summary: ref(null),
      packageHash: ref(''), transferPackage: ref(''), rgb11ReceivePackages: new Map(), invoice: ref(''),
    }
    const generate = load(`export const generate = ${generateSource}`, {}, globals).generate
    const pending = generate()
    if (stage !== 'before_dispatch') {
      await atNative
      if (stage === 'native_pending') lock()
      resume({ code: 0, data: { request_id: 'request-one', invoice: 'invoice-one' } })
    }
    await pending
    assert.equal(calls, stage === 'before_dispatch' ? 0 : 1)
    if (stage === 'before_dispatch') {
      assert.equal(nativeRequests.size, 0)
      assert.equal(saved.has(key), false)
      return
    }
    assert.equal(nativeRequests.size, 1)
    if (stage !== 'none') assert.equal(globals.invoice.value, '', 'locked UI must not expose invoice')
    if (stage === 'none') assert.equal(saved.get(`${key}:selection`), 'request-one')
    // Session changes may suppress the WASM result. Recovery now lists SDK
    // reservations and no longer requires an independently persisted UI handle.
    assert.equal(nativeRequests.has('request-one'), true)
    assert.equal(saved.has(key), false, 'PWA must not persist an invoice/lifecycle duplicate')
  })
}


// Red/green regression: starting a fresh request must not hide a request that
// the SDK durably creates before the PWA receives the return value.
let startNewSource, restoreScopeSource, freshSelectionMarkerSource, parseFreshSelectionMarkerSource
ast.forEachChild(node => {
  if (ts.isVariableStatement(node)) for (const d of node.declarationList.declarations) {
    const name = d.name.getText(ast)
    if (name === 'startNewRequest') startNewSource = d.initializer?.getText(ast)
    if (name === 'freshSelectionMarker') freshSelectionMarkerSource = d.initializer?.getText(ast)
    if (name === 'parseFreshSelectionMarker') parseFreshSelectionMarkerSource = d.initializer?.getText(ast)
  }
  if (!ts.isExpressionStatement(node) || !ts.isCallExpression(node.expression)) return
  const call = node.expression
  if (call.expression.getText(ast) !== 'watch' || call.arguments.length < 2) return
  if (call.arguments[0].getText(ast) === 'scopeKey') restoreScopeSource = call.arguments[1].getText(ast)
})
assert.ok(startNewSource, 'startNewRequest missing')
assert.ok(restoreScopeSource, 'scope recovery watcher missing')
assert.ok(freshSelectionMarkerSource, 'freshSelectionMarker missing')
assert.ok(parseFreshSelectionMarkerSource, 'parseFreshSelectionMarker missing')

test('fresh selection recovers SDK request created after the reset marker', async () => {
  const key = 'rgb11:receive:v1:["test","testnet","root","wallet",1,"rgb:fixture"]'
  const selectionKey = `${key}:selection`
  const ref = value => ({ value })
  const storage = new Map([[selectionKey, 'old-request']])
  const oldRequest = {
    direction: 'receive', request_id: 'old-request', contract_id: 'rgb:fixture',
    invoice: 'invoice-old', amount_raw: '1', mode: 'witness',
    transport_mode: 'out-of-band', status: 'pending', expiry: 4_000_000_000,
    created_at: 100,
  }
  const newRequest = {
    direction: 'receive', request_id: 'new-request', contract_id: 'rgb:fixture',
    invoice: 'invoice-new', amount_raw: '2', mode: 'witness',
    transport_mode: 'out-of-band', status: 'created', expiry: 4_000_000_000,
    created_at: 101,
  }
  let sdkState = { reservations: [oldRequest], transfers: [], ticker_infos: [] }
  const walletManager = {
    getRGB11State: async () => [undefined, { state: JSON.stringify(sdkState) }],
  }
  const Storage = {
    get: async ({ key }) => ({ value: storage.has(key) ? storage.get(key) : null }),
    set: async ({ key, value }) => { storage.set(key, value) },
    remove: async ({ key }) => { storage.delete(key) },
  }
  const markerHelpers = load(`
    export const freshSelectionMarker = ${freshSelectionMarkerSource}
    export const parseFreshSelectionMarker = ${parseFreshSelectionMarkerSource}
  `)
  const globals = {
    ...markerHelpers,
    scopeKey: ref(key), requestId: ref('old-request'), invoice: ref('invoice-old'),
    transferPackage: ref(''), amount: ref('1'), phase: ref('accepted'), summary: ref(null),
    packageHash: ref(''), expiresAt: ref(4_000_000_000), sdkTerminalStatus: ref('pending'),
    errorMessage: ref(''), restoring: ref(false), restoreFailed: ref(false), loading: ref(false),
    receiveMode: ref('witness'), transportMode: ref('out-of-band'),
    canStartNewRequest: ref(true), rgb11ReceivePackages: new Map(),
    Storage, walletManager, assetContractID: ref('rgb:fixture'),
    props: { asset: { precision: 0 } }, t: x => x,
    ensureScope: candidate => { if (candidate !== key) throw new Error('scope changed') },
    rgb11Schema: () => '', rgb11Amount: () => ({ amount_raw: '0' }),
    sha256Text: async value => `hash:${value}`,
  }
  const startNew = load(`export const startNew = ${startNewSource}`, {}, globals).startNew
  const restoreScope = load(`export const restoreScope = ${restoreScopeSource}`, {}, globals).restoreScope

  await startNew()
  assert.equal(globals.requestId.value, '')
  sdkState = { reservations: [oldRequest, newRequest], transfers: [], ticker_infos: [] }

  await restoreScope(key)
  assert.equal(globals.requestId.value, 'new-request',
    'a fresh SDK request created after reset must be recovered instead of hidden by the fresh-selection marker')
  assert.equal(globals.invoice.value, 'invoice-new')
})
