import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, reactive, ref, watch } from 'vue'

// Execute the actual confirmation orchestration with read/write boundaries mocked.
const source = readFileSync('entrypoints/popup/pages/wallet/Tools.vue', 'utf8')
const start = source.indexOf('const invokeReviewContext =')
const end = source.indexOf('\nconst checkDeployTicker', start)
assert.ok(start > 0 && end > start)
const code = ts.transpileModule(source.slice(start, end) + '\nglobalThis.run = invokeSmartContract;', {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None },
}).outputText
function fixture({ fee = '150', feeError, simulation, autopay = false } = {}) {
  const calls = [], errors = [], summaries = []
  let resolveConfirm
  const req = ref({ ContractType: autopay ? 'template' : 'evm', ContractAddress: 'contract', Action: 'call', Param: '{"calldataHex":"0xd09de08a"}', GasLimit: 100000 })
  const context = {
    console, JSON, String, Error, watch,
    env: ref('test'), network: ref('testnet'),
    walletStore: reactive({ rootAccountId: 'root', walletId: 'w1', accountIndex: 0, address: 'source', wallets: [{ id: 'w1', name: 'Wallet 1' }] }),
    invokeContractAddress: ref('contract'), invokeContractType: ref(autopay ? 'template' : 'evm'),
    invokeContractSubtype: ref(''), invokeAction: ref('call'), evmCallJsonText: ref('{}'),
    compilerPageDisposed: false, isInvokingContract: ref(false), contractInvokeResult: ref(''),
    contractUiSubtype: ref(autopay ? 'autopay.tc' : ''),
    isEVMCallInvoke: computed(() => !autopay),
    walkValues: () => undefined, contractLookupPayload: () => ({}),
    t: key => key, currentWalletAddress: () => 'source',
    buildUnifiedInvokeRequest: () => JSON.parse(JSON.stringify(req.value)),
    isInvokeActionDisabled: () => false,
    invokeTransactionSummary: () => ({ details: [{ label: 'Gas limit', value: '100000' }] }),
    validatedEVMCallPayloadForSubmit: () => ({ calldataHex: '0xd09de08a', argumentsText: '[]', gasLimit: 100000 }),
    estimateEVMGasForPayload: async () => simulation ? await simulation : { gasUsed: 23100, gasLimit: 100000 },
    confirmToolTransaction: summary => { summaries.push(summary); return new Promise(resolve => { resolveConfirm = resolve }) },
    resolveTxConfirm: value => resolveConfirm?.(value),
    onBeforeUnmount: () => {},
    sat20: {
      getFeeForInvokeUnifiedContract: async () => [feeError, { fee }],
      invokeUnifiedContract: async request => { calls.push(request); return [undefined, { txid: 'tx' }] },
    },
    showError: (_, error) => errors.push(error.message), showSuccess: () => {},
  }
  vm.runInNewContext(code, context)
  return { context, req, calls, errors, summaries, confirm: value => resolveConfirm(value) }
}
const settle = async () => { for (let i = 0; i < 8; i++) await Promise.resolve() }
for (const action of ['confirm', 'cancel', 'identity', 'request']) {
  const f = fixture(); const pending = f.context.run(); await settle()
  const rows = f.summaries[0].details
  for (const value of ['Wallet 1 (w1)', '0', 'source', '[]', '0xd09de08a', '23100', '150']) {
    assert.ok(rows.some(row => row.value === value), `missing ${value}`)
  }
  assert.equal(f.calls.length, 0)
  if (action === 'identity') f.context.walletStore.accountIndex++
  else if (action === 'request') f.req.value.GasLimit++
  else f.confirm(action === 'confirm')
  await pending
  assert.equal(f.calls.length, action === 'confirm' ? 1 : 0)
}
for (const fee of ['', 'NaN', '-1']) {
  const f = fixture({ fee }); await f.context.run()
  assert.equal(f.calls.length, 0); assert.equal(f.summaries.length, 0); assert.equal(f.errors.length, 1)
}
const rejected = fixture({ feeError: new Error('fee unavailable') }); await rejected.context.run()
assert.equal(rejected.calls.length, 0); assert.deepEqual(rejected.errors, ['fee unavailable'])
let finishSimulation
const stale = fixture({ simulation: new Promise(resolve => { finishSimulation = resolve }) })
const staleRun = stale.context.run()
stale.context.network.value = 'mainnet'; stale.context.network.value = 'testnet'
finishSimulation({ gasUsed: 23100, gasLimit: 100000 }); await staleRun
assert.equal(stale.calls.length, 0); assert.equal(stale.summaries.length, 0); assert.equal(stale.errors.length, 1)
const autopay = fixture({ autopay: true }); const autopayRun = autopay.context.run(); await settle()
assert.ok(autopay.summaries[0].details.some(row => row.value === 'tools.txConfirm.autopayOperatingGas'))
assert.ok(autopay.summaries[0].details.some(row => row.value === '150'))
autopay.confirm(false); await autopayRun
console.log('contract invoke confirmation: PASS (preview fields, cancel, context invalidation, stale simulation, fee failures, AUTOPAY payer)')
