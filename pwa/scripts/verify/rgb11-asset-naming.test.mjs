import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'

// Execute the actual production decorator. Reactive/UI dependencies are not
// invoked by the pure mapping function, so no browser, wallet, or network is
// started by these tests.
const load = async (path, require = () => ({})) => {
  const source = await readFile(new URL(path, import.meta.url), 'utf8')
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const exports = {}
  vm.runInNewContext(compiled, { exports, require }, { filename: path })
  return exports
}
const walletViews = await load('../../utils/walletViews.ts')
const { decorateRGB11AssetItems: decorate } = await load('../../composables/hooks/useRgb11Assets.ts',
  path => path === '@/utils/walletViews' ? walletViews : {})
assert.equal(typeof decorate, 'function')

const keyA = 'rgb11:f:usdt@0101'
const keyB = 'rgb11:f:usdt@0202'
const item = (asset, ticker = '', name = '', metadata = {}) => ({asset, ticker, name, available:'42', pending:'3', precision:0, ...metadata})
// The SDK owns verification/canonical naming policy. This adapter only decorates
// its RGBAssetView for display and must never change selection/transfer identity.
test('the canonical asset name is also the transfer key', () => {
  const metadata = {canonical_name:'rgb11:f:usdt@0101'}
  const first = decorate([item(keyA, 'usdt@alice', '', metadata)])[0]
  const renamed = decorate([item(keyA, 'usdt@bob', '', metadata)])[0]
  assert.equal(first.label, metadata.canonical_name)
  assert.equal(renamed.label, metadata.canonical_name)
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
test('missing canonical name displays a description or the protocol label', () => {
  assert.equal(decorate([item(keyA,'','Asset name')])[0].label,'Asset name')
  const result = decorate([item(keyA)])[0]
  assert.equal(result.label,'RGB')
  assert.equal(result.ticker,'usdt@0101')
  assert.equal(result.protocol,'rgb11'); assert.equal(result.type,'f')
  assert.equal(decorate([item(`rgb11:f:${'01'.repeat(32)}`)]).length, 0)
})

test('SDK local and registered naming metadata is preserved without PWA policy', () => {
  for (const metadata of [
    {contract_id:'rgb:contract-a', canonical_name:'', verified:false},
    {contract_id:'rgb:contract-a', canonical_name:'rgb11:f:usdt@0101', verified:true},
  ]) {
    const result = decorate([item(keyA,'USDT','',metadata)])[0]
    for (const field of ['contract_id','canonical_name','verified']) assert.equal(result[field],metadata[field])
    assert.equal(result.key,keyA)
    assert.equal(result.asset_key,keyA)
  }
})
