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

test('AUTOPAY status exposes actual payment height and separate exact-expiry estimates', () => {
  assert.match(statusType, /last_pay_height\?: number/)
  assert.match(statusType, /remaining_payment_blocks\?: number/)
  assert.match(statusType, /estimated_expiry_height\?: number/)
  assert.match(statusType, /estimated_funded_expiry_height\?: number/)
})

test('account management shows the actual paid-through height and marks expiry as expected', () => {
  assert.match(accountPage, /last_pay_height/)
  assert.match(accountPage, /accountManagement\.dkvsPaidHeight/)
  assert.match(accountPage, /estimated_expiry_height/)
  assert.match(accountPage, /accountManagement\.dkvsExpiryEstimate/)
  assert.match(accountPage, /estimated_funded_expiry_height/)
  assert.equal(zh.accountManagement.dkvsPay, '支付 DKVS 存储费用')
  assert.match(zh.accountManagement.dkvsPaidHeight, /已实际支付至聪网高度/)
  assert.match(zh.accountManagement.dkvsExpiryEstimate, /预计.*聪网高度/)
  assert.equal(en.accountManagement.dkvsPay, 'Pay DKVS storage fees')
  assert.match(en.accountManagement.dkvsPaidHeight, /Actually paid through SatoshiNet height/)
  assert.match(en.accountManagement.dkvsExpiryEstimate, /Estimated.*SatoshiNet height/)
})

test('healthy paid storage offers the same confirmed DKVS renewal flow in both locales', () => {
  const healthyAutopayBlock = accountPage.match(
    /v-else-if="autopayStatus\?\.required && autopayStatus\.ready"([\s\S]*?)<\/div>/,
  )?.[1]
  assert.ok(healthyAutopayBlock, 'healthy AUTOPAY status block is missing')
  assert.match(healthyAutopayBlock, /autopayStatus\.can_fund/)
  assert.match(healthyAutopayBlock, /@click="fundAutopay"/)
  assert.match(healthyAutopayBlock, /accountManagement\.dkvsRenew/)
  assert.match(accountPage, /accountSDK\.fundAutopay\(confirmed\)/)
  assert.equal(zh.accountManagement.dkvsRenew, '续费 DKVS 存储')
  assert.equal(en.accountManagement.dkvsRenew, 'Renew DKVS storage')
})

test('wallet reminder identifies DKVS storage payment and keeps its estimate distinct from payment proof', () => {
  assert.match(walletReminder, /accountManagement\.dkvsPaymentNeedsAttention/)
  assert.match(walletReminder, /accountManagement\.dkvsPay/)
  assert.match(walletReminder, /autopay\?\.estimated_expiry_height/)
  assert.match(walletReminder, /accountManagement\.dkvsExpiryEstimate/)
  assert.match(zh.accountManagement.dkvsPaymentNeedsAttention, /DKVS/)
  assert.match(zh.accountManagement.dkvsExpiryEstimate, /预计.*聪网高度/)
  assert.match(en.accountManagement.dkvsPaymentNeedsAttention, /DKVS/)
  assert.match(en.accountManagement.dkvsExpiryEstimate, /Estimated.*SatoshiNet height/)
})

test('Tools AUTOPAY funding remains the operator GAS top-up action', () => {
  assert.match(toolsPage, /gasFundingAmount/)
  assert.match(toolsPage, /autopay\.tc/)
  assert.match(toolsPage, /operatingGas|操作 GAS|运行 GAS|运营 GAS/)
})
