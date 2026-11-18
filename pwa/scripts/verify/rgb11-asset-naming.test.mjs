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

const keyA = `rgb11:f:${'01'.repeat(32)}`
const keyB = `rgb11:f:${'02'.repeat(32)}`
const item = (asset, ticker = '', name = '', metadata = {}) => ({asset, ticker, name, available:'42', pending:'3', precision:0, ...metadata})
// The SDK owns verification/canonical naming policy. This adapter only decorates
// its RGBAssetView for display and must never change selection/transfer identity.
test('renames change display labels but never selection or transfer keys', () => {
  const first = decorate([item(keyA, 'usdt@alice')])[0]
  const renamed = decorate([item(keyA, 'usdt@bob')])[0]
  assert.equal(first.label, 'usdt@alice')
  assert.equal(renamed.label, 'usdt@bob')
  for (const field of ['id','key','asset_key','asset']) assert.equal(renamed[field], keyA)
  assert.equal(first.id, renamed.id)
  assert.equal(renamed.amount,'42'); assert.equal(renamed.pending_amount,'3')
})
test('identical labels do not merge distinct assets', () => {
  const result = decorate([item(keyA,'USDT','',{contract_id:'rgb:contract-a'}), item(keyB,'USDT','',{contract_id:'rgb:contract-b'})])
  assert.equal(result.length,2); assert.equal(result[0].label,result[1].label)
  assert.notEqual(result[0].key,result[1].key)
  assert.notEqual(result[0].contract_id,result[1].contract_id)
})
test('missing ticker falls back to SDK name then full identity', () => {
  assert.equal(decorate([item(keyA,'','Asset name')])[0].label,'Asset name')
  const result = decorate([item(keyA)])[0]
  assert.equal(result.label,keyA)
  assert.equal(result.ticker,'01'.repeat(32))
  assert.equal(result.protocol,'rgb11'); assert.equal(result.type,'f')
})

test('SDK local and registered naming metadata is preserved without PWA policy', () => {
  for (const metadata of [
    {contract_id:'rgb:contract-a', canonical_name:'', verified:false},
    {contract_id:'rgb:contract-a', canonical_name:'rgb11:f:usdt@alice', verified:true},
  ]) {
    const result = decorate([item(keyA,'USDT','',metadata)])[0]
    for (const field of ['contract_id','canonical_name','verified']) assert.equal(result[field],metadata[field])
    assert.equal(result.key,keyA)
    assert.equal(result.asset_key,keyA)
  }
})
