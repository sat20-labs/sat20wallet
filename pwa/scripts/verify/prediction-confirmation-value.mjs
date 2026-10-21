import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { computed } from 'vue'

// Exercise the real approval component's derived review fields, without signing.
const source = readFileSync(process.env.PWA_APPROVAL_SOURCE || 'components/approve/ApproveInvokeUnifiedContract.vue', 'utf8')
const start = source.indexOf('const req = computed(')
const end = source.indexOf('\nconst walletLockedMessage', start)
assert.ok(start >= 0 && end > start, 'approval review section must be found')
const code = ts.transpileModule(source.slice(start, end) + '\nglobalThis.review = { betAssetName, betAmount, formattedRequest };', {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None },
}).outputText
const native = { ContractType: 'agent', SubType: 'prediction', ContractAddress: 'contract', Action: 'bet', Value: 1000 }
const cases = [
  ['native_value_visible', native, '::', 1000],
  ['token_control', { ...native, Value: undefined, Assets: [{ AssetName: 'ordx:f:pwapos', Amount: '200' }] }, 'ordx:f:pwapos', '200'],
  ['unrelated_call_control', { ...native, ContractType: 'template', SubType: 'autopay.tc', Action: 'funding' }, '', ''],
  ['zero_value_not_a_bet', { ...native, Value: 0 }, '', ''],
  ['unsafe_value_not_a_bet', { ...native, Value: Number.MAX_SAFE_INTEGER + 1 }, '', ''],
]
let failed = 0
for (const [name, request, asset, amount] of cases) {
  try {
    const context = { props: { data: { req: request } }, computed, JSON, Number, String }
    vm.runInNewContext(code, context)
    assert.equal(context.review.betAssetName.value, asset)
    assert.equal(context.review.betAmount.value, amount)
    assert.equal(context.review.formattedRequest.value, JSON.stringify(request, null, 2), 'review cannot alter the submitted request')
    console.log(`PASS ${name}`)
  } catch (error) {
    failed++
    console.log(`FAIL ${name}: ${error.message}`)
  }
}
console.log(JSON.stringify({ passed: cases.length - failed, failed, total: cases.length }))
process.exitCode = failed ? 1 : 0
