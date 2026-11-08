import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, ref } from 'vue'

const source = readFileSync('entrypoints/popup/pages/wallet/settings/account-management/Index.vue', 'utf8')
const quoteStart = source.indexOf('const quoteForRecordCount =')
const quoteEnd = source.indexOf('\nconst autopayQuote =', quoteStart)
assert.ok(quoteStart >= 0 && quoteEnd > quoteStart, 'real record-count quote helper must be found')
const quoteCode = source.slice(quoteStart, quoteEnd)
const rowsCode = source.slice(source.indexOf('const paidConfirmRows ='), source.indexOf('const resolvePaidConfirmation ='))
const fundingCode = source.slice(source.indexOf('const fundAutopay ='), source.indexOf('const createRecovery ='))
const compile = code => ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
for (const language of ['en', 'zh']) {
  const locale = JSON.parse(readFileSync(`locales/${language}.json`, 'utf8'))
  const t = (key, values = {}) => key.split('.').reduce((value, part) => value?.[part], locale)?.replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? '')) ?? key
  let confirmed = false, writes = 0, refreshes = 0
  let changeStatusDuringConfirmation = false
  const fundingStatus = {
    can_fund: true,
    ready: false,
    required: true,
    payer: 'root-payer',
    contract_address: 'autopay-contract',
    fee_asset: 'brc20:f:sgas',
    recommended_funding_amount: '10000',
    recommended_funding_blocks: 1000,
    amount_per_block: '10',
    effective_amount_per_block: '10',
  }
  const submittedSnapshots = []
  const displayedFundingRows = () => {
    const rows = context.rows.value
    const value = label => rows.find(row => row.label === t(label))?.value
    assert.equal(value('accountManagement.fundingPrincipal'), '10000')
    assert.equal(value('tools.txConfirm.asset'), 'brc20:f:sgas')
    assert.equal(value('accountManagement.fundingSourceWallet'), 'Funding root (99)')
    assert.equal(value('tools.txConfirm.account'), t('accountManagement.fundingSourceAccount'))
    assert.equal(value('accountManagement.ui.rootIdentity'), 'root-account-id')
    assert.equal(value('tools.txConfirm.sourceAddress'), 'root-payer')
    assert.equal(value('tools.txConfirm.to'), 'autopay-contract')
    assert.equal(value('accountManagement.perBlock'), '10 brc20:f:sgas')
    assert.equal(value('accountManagement.fundingPeriod'), t('accountManagement.fundingBlocks', { count: 1000 }))
    assert.equal(value('tools.txConfirm.operatingGas'), t('accountManagement.fundingOperatingGas'))
    assert.match(value('tools.txConfirm.operatingGas'), /200 GAS/)
    assert.ok(!rows.some(row => row.value === '10200' || row.value === '10001' || row.value === 'selected-non-root'))
    return rows
  }
  const context = {
    computed, t,
    paidStorageOption: ref({ fee_asset: 'brc20:f:sgas', estimated_cost: '10000', full_record_fee_per_block: '0.1', minimum_amount_per_block: '0', contract_address: 'autopay-contract' }),
    autopayQuote: ref({ initialCost: '10000', amountPerBlock: '10' }),
    paidConfirmContext: ref('funding'),
    fundingConfirmation: ref(null),
    preflight: ref({ account_id: 'root-account-id' }),
    savedState: ref({ root_wallet_id: 99, account_id: 'saved-root-account-id' }),
    wallets: ref([{ id: 23, name: 'Selected non-root' }, { id: 99, name: 'Funding root' }]),
    walletStore: { walletId: 23, accountIndex: 5, address: 'selected-non-root', network: 'testnet', rootAccountId: 'wallet-root-id' },
    autopayStatus: ref({ ...fundingStatus }),
    normalizedRecordCount: ref(100),
    autopayFundingResult: ref(null),
    run: task => task(), requestPaidConfirmation: async () => {
      assert.deepEqual(JSON.parse(JSON.stringify(context.fundingConfirmation.value)), fundingStatus)
      displayedFundingRows()
      if (changeStatusDuringConfirmation) {
        context.autopayStatus.value = { ...context.autopayStatus.value, payer: 'changed-payer', contract_address: 'changed-contract', fee_asset: 'brc20:f:changed', recommended_funding_amount: '10001', effective_amount_per_block: '11' }
        displayedFundingRows()
      }
      return confirmed
    },
    accountSDK: { fundAutopay: async (...args) => {
      assert.equal(args.length, 1)
      submittedSnapshots.push(JSON.parse(JSON.stringify(args[0])))
      writes++
      return { transaction_id: 'fund-tx' }
    } },
    refreshAutopayStatus: async () => { refreshes++ },
  }
  vm.runInNewContext(compile(quoteCode + rowsCode + fundingCode + '\nglobalThis.rows = paidConfirmRows; globalThis.fund = fundAutopay;'), context)
  await context.fund(); assert.equal(writes, 0) // Cancel remains write-free.
  assert.equal(submittedSnapshots.length, 0)
  assert.equal(refreshes, 2)
  confirmed = true; changeStatusDuringConfirmation = true
  await context.fund(); assert.equal(writes, 1); assert.equal(refreshes, 4)
  assert.deepEqual(submittedSnapshots, [fundingStatus], 'confirmed SDK input must be the complete snapshot shown, even if live status changes while the dialog is open')
  assert.equal(context.fundingConfirmation.value, null)
  context.autopayStatus.value = { ...context.autopayStatus.value, ready: true, can_fund: false }
  await assert.rejects(context.fund(), /AUTOPAY/); assert.equal(writes, 1); assert.equal(refreshes, 5)
  for (const mode of ['account', 'guardian']) {
    context.paidConfirmContext.value = mode
    assert.equal(context.rows.value.find(row => row.label === t('tools.txConfirm.amount'))?.value, (10_000).toLocaleString(undefined, { maximumFractionDigits: 8 }))
    assert.ok(!context.rows.value.some(row => row.label === t('tools.txConfirm.operatingGas')))
    assert.equal(context.rows.value.find(row => row.label === t('accountManagement.fundingSourceWallet'))?.value, 'Funding root (99)')
    assert.equal(context.rows.value.find(row => row.label === t('accountManagement.ui.rootIdentity'))?.value, 'root-account-id')
    assert.ok(!context.rows.value.some(row => row.label === t('tools.txConfirm.sourceAddress')))
  }
}
assert.match(source, /v-if="autopayStatus\?\.required && !autopayStatus\.ready"/)
assert.match(source, /v-if="autopayStatus\.can_fund"/)
console.log('AUTOPAY funding confirmation: PASS (root payer, frozen funding snapshot, principal, reserve GAS, Cancel, can_fund guard, root identity in other confirmation modes, EN/ZH)')
