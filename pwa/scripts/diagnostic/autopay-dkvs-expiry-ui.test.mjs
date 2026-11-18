import assert from 'node:assert/strict'
import fs from 'node:fs'
import test from 'node:test'

const read = path => fs.readFileSync(new URL(path, import.meta.url), 'utf8')
const statusType = read('../../utils/accountManagement.ts')
const accountPage = read('../../entrypoints/popup/pages/wallet/settings/account-management/Index.vue')
const walletReminder = read('../../components/account/AccountAutopayReminder.vue')
const toolsPage = read('../../entrypoints/popup/pages/wallet/Tools.vue')
const zh = JSON.parse(read('../../locales/zh.json'))
const en = JSON.parse(read('../../locales/en.json'))

test('SDK PaymentView exposes an estimate separately from payment transaction identity', () => {
  const views = read('../../utils/walletViews.ts')
  assert.match(views, /interface PaymentView[\s\S]*expiry_height: number[\s\S]*quote: PaymentQuote \| null[\s\S]*tx_id: string/)
  assert.match(statusType, /fundAutopay\(confirmed: PaymentQuote\)/)
})
test('account and reminder label expiry as estimated, not actual paid-through proof', () => {
  assert.match(accountPage, /state\.autopay\.expiry_height/)
  assert.match(walletReminder, /autopay\?\.expiry_height/)
  for (const source of [accountPage,walletReminder]) {
    assert.match(source, /accountManagement\.dkvsExpiryEstimate/)
    assert.doesNotMatch(source, /accountManagement\.dkvsPaidHeight/)
  }
  assert.match(zh.accountManagement.dkvsExpiryEstimate,/预计.*聪网高度/)
  assert.match(en.accountManagement.dkvsExpiryEstimate,/Estimated.*SatoshiNet height/)
})
test('funding requires available SDK quote and explicit confirmation', () => {
  assert.match(accountPage, /state\.autopay\.can_fund && state\.autopay\.quote/)
  assert.match(accountPage, /@click="askFunding"/)
  assert.match(accountPage, /@click="confirmPayment"/)
  assert.match(accountPage, /if \(quote\) \{ payment\.value = quote; paymentAction = action \}/)
  assert.match(accountPage, /requestPayment\(quote, async \(\) => \{ await accountSDK\.fundAutopay\(quote\)/)
  assert.match(accountPage, /const confirmPayment = \(\) => run\(async \(\) => \{ const action = paymentAction; cancelPayment\(\); if \(action\) await action\(\)/)
  // Actual unchanged-quote, changed-quote refusal and one-broadcast behavior
  // remains exercised by runAutopayPageCase, not claimed by this source guard.
})
test('wallet reminder identifies DKVS funding without making a payment', () => {
  assert.match(walletReminder,/accountManagement\.dkvsPaymentNeedsAttention/)
  assert.match(walletReminder,/accountManagement\.dkvsPay/)
  assert.doesNotMatch(walletReminder,/accountSDK\.fundAutopay/)
})

test('Tools AUTOPAY funding remains the operator GAS top-up action', () => {
  assert.match(toolsPage, /gasFundingAmount/)
  assert.match(toolsPage, /autopay\.tc/)
  assert.match(toolsPage, /operatingGas|操作 GAS|运行 GAS|运营 GAS/)
})
