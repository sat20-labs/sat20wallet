import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, ref } from 'vue'

const source = readFileSync('entrypoints/popup/pages/wallet/settings/account-management/Index.vue', 'utf8')
const rowsCode = source.slice(source.indexOf('const paidConfirmRows ='), source.indexOf('const resolvePaidConfirmation ='))
const fundingCode = source.slice(source.indexOf('const fundAutopay ='), source.indexOf('const createRecovery ='))
const compile = code => ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
for (const language of ['en', 'zh']) {
  const locale = JSON.parse(readFileSync(`locales/${language}.json`, 'utf8'))
  const t = (key, values = {}) => key.split('.').reduce((value, part) => value?.[part], locale)?.replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? '')) ?? key
  let confirmed = false, writes = 0, refreshes = 0
  const context = {
    computed, t,
    paidStorageOption: ref({ fee_asset: 'brc20:f:sgas', estimated_cost: '10000' }),
    autopayQuote: ref({ initialCost: '10000', amountPerBlock: '10' }),
    paidConfirmContext: ref('funding'),
    savedState: ref({ root_wallet_id: 99 }),
    wallets: ref([{ id: 23, name: 'Selected non-root' }, { id: 99, name: 'Funding root' }]),
    walletStore: { walletId: 23, accountIndex: 5, address: 'selected-non-root', network: 'testnet' },
    autopayStatus: ref({ can_fund: true, ready: false, payer: 'root-payer', contract_address: 'autopay-contract', fee_asset: 'brc20:f:sgas', recommended_funding_amount: '10000', recommended_funding_blocks: 1000, amount_per_block: '10' }),
    normalizedRecordCount: ref(100),
    autopayFundingResult: ref(null),
    run: task => task(), requestPaidConfirmation: async () => confirmed,
    accountSDK: { fundAutopay: async (...args) => { assert.equal(args.length, 0); writes++; return { transaction_id: 'fund-tx' } } },
    refreshAutopayStatus: async () => { refreshes++ },
  }
  vm.runInNewContext(compile(rowsCode + fundingCode + '\nglobalThis.rows = paidConfirmRows; globalThis.fund = fundAutopay;'), context)
  const rows = context.rows.value
  const value = label => rows.find(row => row.label === t(label))?.value
  assert.equal(value('accountManagement.fundingPrincipal'), '10000')
  assert.equal(value('tools.txConfirm.asset'), 'brc20:f:sgas')
  assert.equal(value('accountManagement.fundingSourceWallet'), 'Funding root (99)')
  assert.equal(value('tools.txConfirm.account'), t('accountManagement.fundingSourceAccount'))
  assert.equal(value('tools.txConfirm.sourceAddress'), 'root-payer')
  assert.equal(value('tools.txConfirm.operatingGas'), t('accountManagement.fundingOperatingGas'))
  assert.match(value('tools.txConfirm.operatingGas'), /200 GAS/)
  assert.ok(!rows.some(row => row.value === '10200' || row.value === 'selected-non-root'))
  await context.fund(); assert.equal(writes, 0) // Cancel remains write-free.
  confirmed = true; await context.fund(); assert.equal(writes, 1); assert.equal(refreshes, 1)
  context.autopayStatus.value = { ...context.autopayStatus.value, ready: true, can_fund: false }
  await assert.rejects(context.fund(), /AUTOPAY/); assert.equal(writes, 1)
  for (const mode of ['account', 'guardian']) {
    context.paidConfirmContext.value = mode
    assert.equal(context.rows.value.find(row => row.label === t('tools.txConfirm.amount'))?.value, '10000')
    assert.ok(!context.rows.value.some(row => row.label === t('tools.txConfirm.operatingGas')))
    assert.ok(!context.rows.value.some(row => row.label === t('accountManagement.fundingSourceWallet')))
  }
}
assert.match(source, /savedState\.storage_mode === 'paid' && autopayStatus && !autopayStatus\.ready/)
assert.match(source, /v-if="autopayStatus\.can_fund"/)
console.log('AUTOPAY funding confirmation: PASS (root payer, unchanged principal, reserve GAS, Cancel, can_fund guard, unchanged SDK call, other confirmation modes)')
