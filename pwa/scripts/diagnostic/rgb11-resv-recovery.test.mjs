import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { transformSync } from 'esbuild'
import ts from 'typescript'

const source = path => readFileSync(new URL(path, import.meta.url), 'utf8')
const script = source('../../components/wallet/RGB11InvoiceDialog.vue').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
const ast = ts.createSourceFile('dialog.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
let restoreSource
let parseFreshSelectionMarkerInitializer
ast.forEachChild(node => {
  if (ts.isVariableStatement(node)) {
    for (const declaration of node.declarationList.declarations) {
      if (ts.isIdentifier(declaration.name) && declaration.name.text === 'parseFreshSelectionMarker' && declaration.initializer) {
        parseFreshSelectionMarkerInitializer = declaration.initializer.getText(ast)
      }
    }
  }
  if (ts.isExpressionStatement(node) && ts.isCallExpression(node.expression)) {
    const call = node.expression
    if (call.expression.getText(ast) === 'watch' && call.arguments[0]?.getText(ast) === 'scopeKey') restoreSource = call.arguments[1].getText(ast)
  }
})
assert.ok(restoreSource)
assert.ok(parseFreshSelectionMarkerInitializer)
function load(code, globals = {}) {
  const module = { exports: {} }
  runInNewContext(transformSync(code, { loader: 'ts', format: 'cjs', target: 'es2022' }).code,
    { module, exports: module.exports, crypto, atob, TextEncoder, ...globals })
  return module.exports
}
const parseFreshSelectionMarker = load(
  `export const parseFreshSelectionMarker = ${parseFreshSelectionMarkerInitializer}`,
).parseFreshSelectionMarker
const helpers = load(source('../../utils/rgb11Oob.ts'))
for (const status of ['created', 'awaiting_broadcast', 'pending', 'settled', 'expired']) {
  test(`SDK reservation restores ${status} without PWA business records`, async () => {
    const ref = value => ({ value })
    const key = 'selected-account-and-contract'
    const transfer = { transfer_id: 'transfer-one', status, witness_txid: 'tx-one', consignment_hash: 'consignment-hash',
      asset: { Amount: { Value: '250', Precision: 2 } }, output_outpoints: ['tx-one:1'] }
    const reservations = [
      { request_id: 'other', direction: 'receive', contract_id: 'rgb:other', invoice: 'wrong-invoice' },
      { request_id: 'request-one', direction: 'receive', contract_id: 'rgb:fixture', invoice: 'sdk-invoice',
        amount_raw: '250', mode: 'blind', transport_mode: 'out-of-band', expiry: 2000000000, status,
        ...(status !== 'created' ? { transfer } : {}) },
    ]
    const globals = { ...helpers, props: { asset: { precision: 2 } }, t: value => value,
      assetContractID: ref('rgb:fixture'), scopeKey: ref(key),
      parseFreshSelectionMarker,
      walletManager: { getRGB11State: async () => [null, { state: JSON.stringify({ reservations,
        ticker_infos: [{ content: { contract_id: 'rgb:fixture', schema_id: 'schema-one' } }] }) }] },
      Storage: { get: async () => ({ value: null }), remove: async () => {} },
    }
    for (const name of ['invoice', 'requestId', 'transferPackage', 'amount', 'phase', 'summary', 'packageHash',
      'expiresAt', 'sdkTerminalStatus', 'errorMessage', 'restoring', 'restoreFailed', 'receiveMode', 'transportMode']) globals[name] = ref('')
    const restore = load(`export const restore = ${restoreSource}`, globals).restore
    await restore(key)
    assert.equal(globals.restoreFailed.value, false)
    assert.equal(globals.requestId.value, 'request-one')
    assert.equal(globals.invoice.value, 'sdk-invoice')
    assert.equal(globals.amount.value, '2.50')
    if (status === 'awaiting_broadcast') {
      assert.equal(globals.phase.value, 'prepared')
      assert.equal(globals.summary.value.witness_txid, 'tx-one')
      assert.equal(globals.summary.value.schema_id, 'schema-one')
      assert.equal(globals.packageHash.value, 'consignment-hash')
    } else if (status === 'pending' || status === 'settled') assert.equal(globals.phase.value, 'accepted')
  })
}
