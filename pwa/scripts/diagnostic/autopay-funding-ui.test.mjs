import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'
import test from 'node:test'

const require = createRequire(new URL('../../package.json', import.meta.url))
const ts = require('typescript')
const source = fs.readFileSync(process.env.SAT20_TOOLS_SOURCE || new URL('../../entrypoints/popup/pages/wallet/Tools.vue', import.meta.url), 'utf8')
const section = (start, end) => source.slice(source.indexOf(start), source.indexOf(end, source.indexOf(start)))
const actions = section('const templateInvokeActionsBySubtype:', 'const agentInvokeActions')
const fields = section('const localInvokeTemplate =', 'const resolveInvokeBalanceAsset =')
const request = section('const buildUnifiedInvokeRequest =', 'const inputTypeForField =')
function actualUI(subtype, action, params = {}) {
  const context = vm.createContext({
    invokeAction: { value: action }, invokeContractType: { value: 'template' },
    contractAssetAName: { value: 'brc20:f:sgas' }, contractUiSubtype: { value: subtype },
    invokeContractSubtype: { value: subtype }, invokeParamWrapperAction: { value: action },
    invokeParams: () => params, t: key => key,
    invokeTextField: (key, label, placeholder, defaultValue, valueKind, extra) => ({ key, label, placeholder, defaultValue, valueKind, ...extra }),
  })
  vm.runInContext(ts.transpileModule(actions + fields + request + '\nglobalThis.result = { actions: templateInvokeActionsBySubtype, fields: localInvokeTemplate, request: buildUnifiedInvokeRequest }', { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText, context)
  return context.result
}
test('operator can select gas-only AUTOPAY funding and the signed request carries the exact entered amount', () => {
  const ui = actualUI('autopay.tc', 'funding', { gasFundingAmount: '5000' })
  assert.ok(ui.actions['autopay.tc']?.includes('funding'), 'AUTOPAY funding action is inaccessible in PWA')
  assert.ok(ui.fields().some(field => field.key === 'gasFundingAmount'), 'operating GAS field is absent')
  const request = JSON.parse(JSON.stringify(ui.request('autopay-address')))
  assert.equal(request.Action, 'funding')
  assert.deepEqual(JSON.parse(request.Param), { gasFundingAmount: '5000' })
  assert.deepEqual(request.Assets, [{ AssetName: 'brc20:f:sgas', Amount: '5000' }])
  assert.equal(JSON.parse(request.Param).amountPerBlock, undefined, 'operator funding must not change delegate settings')
})
test('gas-only funding rejects zero/negative/fractional/empty amount before signing', () => {
  for (const amount of ['0', '-1', '1.5', '']) {
    assert.throws(() => actualUI('autopay.tc', 'funding', { gasFundingAmount: amount }).request('autopay-address'))
  }
})
test('existing Exchange and default invocation requests retain their original meaning', () => {
  const exchange = JSON.parse(JSON.stringify(actualUI('exchange.tc', 'exchange', { minOutA: '10' }).request('exchange-address')))
  assert.deepEqual(JSON.parse(exchange.Param), { minOutA: '10' })
  assert.equal(exchange.Assets, undefined)
  const fallback = JSON.parse(JSON.stringify(actualUI('autopay.tc', 'default').request('autopay-address')))
  assert.equal(fallback.DefaultInvoke, true)
  assert.equal(fallback.Assets, undefined)
})

test('actual AUTOPAY templateName is recognized before the invocation form is selected', () => {
  const normalize = section('const normalizeTemplateSubtype =', 'const walkValues =')
  const context = vm.createContext({})
  vm.runInContext(ts.transpileModule(normalize + '\nglobalThis.normalize = normalizeTemplateSubtype;', { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText, context)
  assert.equal(context.normalize('autopay.tc'), 'autopay.tc')
  assert.equal(context.normalize('exchange.tc'), 'exchange.tc')
  assert.equal(context.normalize('amm.tc'), 'amm.tc')
})
