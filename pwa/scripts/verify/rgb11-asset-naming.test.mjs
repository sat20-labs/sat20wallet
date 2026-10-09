import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'

// Execute the actual production decorator. Reactive/UI dependencies are not
// invoked by the pure mapping function, so no browser, wallet, or network is
// started by these tests.
const source = await readFile(new URL('../../composables/hooks/useRgb11Assets.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  fileName: 'useRgb11Assets.ts',
}).outputText
const exports = {}
vm.runInNewContext(compiled, { exports, require: () => ({}) }, { filename: 'useRgb11Assets.js' })
const decorate = exports.decorateRGB11AssetItems
assert.equal(typeof decorate, 'function')

const contractA = 'rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E'
const contractB = 'rgb:k0vsa6zj-CLYfnru-63unuJv-qZ2IVJ5-zlENzlF-MkiJNuw'
const keyA = '01'.repeat(32)
const keyB = '02'.repeat(32)
const item = (ticker) => ({ protocol: 'rgb11', type: 'f', ticker, amount: '42', precision: 0 })
const info = (key, contract, label, extra = {}) => ({
  name: { Protocol: 'rgb11', Type: 'f', Ticker: key },
  asset_key: `rgb11:f:${key}`,
  contract_id: contract,
  ticker: label,
  naming_status: 'local-address',
  canonical_name: '',
  verified: false,
  ...extra,
})

test('local renames change labels but never selection or transfer keys', () => {
  const first = decorate([item(keyA)], { ticker_infos: [info(keyA, contractA, 'usdt@123456789012')] })[0]
  const renamed = decorate([item(keyA)], { ticker_infos: [info(keyA, contractA, 'usdt@alice')] })[0]
  assert.equal(first.label, 'usdt@123456789012')
  assert.equal(renamed.label, 'usdt@alice')
  assert.equal(first.key, renamed.key)
  assert.equal(first.id, renamed.id)
  assert.equal(renamed.key, `rgb11:f:${keyA}`)
  assert.equal(renamed.contract_id, contractA)
  assert.equal(renamed.amount, '42')
  assert.equal(renamed.canonical_name, '')
  assert.equal(renamed.verified, false)
  assert.equal(Object.hasOwn(renamed, 'fingerprint'), false)
})

test('identical local labels on different contracts do not merge assets', () => {
  const result = decorate([item(keyA), item(keyB)], { ticker_infos: [
    info(keyA, contractA, 'usdt@alice'), info(keyB, contractB, 'usdt@alice'),
  ] })
  assert.equal(result[0].label, result[1].label)
  assert.notEqual(result[0].key, result[1].key)
  assert.notEqual(result[0].contract_id, result[1].contract_id)
})

test('local metadata alone cannot set the verified badge', () => {
  const result = decorate([item(keyA)], { ticker_infos: [info(keyA, contractA, 'usdt@alice', { verified: true })] })[0]
  assert.equal(result.verified, false)
  assert.equal(result.canonical_name, '')
})

test('registered names remain separate from the complete contract key', () => {
  const canonical = 'rgb11:f:usdt_2@alice'
  const result = decorate([item(keyA)], { ticker_infos: [info(keyA, contractA, 'usdt_2@alice', {
    canonical_name: canonical, verified: true,
  })] })[0]
  assert.equal(result.canonical_name, canonical)
  assert.equal(result.key, `rgb11:f:${keyA}`)
  assert.equal(result.contract_id, contractA)
  assert.equal(result.verified, true)
})

test('missing origin uses full identity instead of an invented provider', () => {
  const result = decorate([item(keyA)], { ticker_infos: [info(keyA, contractA, '')] })[0]
  assert.equal(result.label, contractA)
  assert.equal(result.canonical_name, '')
  const missing = decorate([item(keyA)], { ticker_infos: [] })[0]
  assert.equal(missing.label, `rgb11:f:${keyA}`)
  assert.equal(missing.verified, false)
})
