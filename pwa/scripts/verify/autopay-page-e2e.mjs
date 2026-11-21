import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { expect } from '@playwright/test'

const messages = JSON.parse(readFileSync(new URL('../../locales/en.json', import.meta.url), 'utf8')).accountManagement
export const autopayPageCase = 'RGB PWA: AutopayFundingConfirmationMatchesEffectiveRate'

// Retains the existing real signed transaction/contract assertions in the Go
// parent while driving only the new AccountView/PaymentQuote UI surface.
export async function runAutopayPageCase(t, fixture) {
  const { device, walletCall, accountCall } = t
  const page = await device()
  const evidence = []
  let capture = false
  let broadcasts = 0
  const fundingRoute = async route => {
    if (route.request().method() !== 'POST' || !/\/btc\/tx$/.test(new URL(route.request().url()).pathname)) return route.fallback()
    broadcasts++
    const body = route.request().postDataJSON()
    const response = await route.fetch()
    const result = await response.json()
    if (capture) {
      assert.equal(result.code, 0)
      evidence.push({ raw_tx: body.signedTxHex, transaction_id: result.data })
    }
    await route.fulfill({ response })
  }
  try {
    await t.check(autopayPageCase, async () => {
      await page.evaluate(async ({ mnemonic, password }) => {
        const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().importWallet(mnemonic, password)
        if (error) throw error
        location.hash = '#/wallet/setting/account-management'
      }, { mnemonic: fixture.maintenance_mnemonic, password: fixture.password })
      // A temporary account does not discover unrelated on-chain delegates.
      // Establish this payer's own paid backup through the current SDK first.
      assert.equal((await accountCall(page, 'getAccountView')).autopay, null)
      const ids = (await walletCall(page, 'getWalletCatalog')).wallets.map(wallet => Number(wallet.id))
      await accountCall(page, 'preflightRecovery', fixture.password, ids)
      const paid = (await accountCall(page, 'getStorageOptions', 100)).find(option => option.id === 'paid')
      assert.equal(paid.available, true)
      let storage = await accountCall(page, 'confirmStorage', paid)
      if (storage.status === 'pending') {
        const txid = storage.tx_id
        assert.match(txid, /^[0-9a-f]{64}$/)
        await expect.poll(async () => {
          storage = await accountCall(page, 'confirmStorage', paid)
          if (storage.tx_id) assert.equal(storage.tx_id, txid, 'continue the original storage payment')
          return storage.status
        }, { timeout: 90000 }).toBe('ready')
      }
      assert.equal(storage.status, 'ready')
      const questions = t.questions.map(q => ({ question: { id: q.id, prompt: q.prompt,
        case_sensitive: false, ignore_punctuation: true }, answer: q.answer, confirmation: q.confirmation }))
      const created = await accountCall(page, 'createRecovery', { password: fixture.password,
        wallet_ids: ids, mode: '2of2', questions, guardian_contact: '' })
      const recoveryFile = Buffer.from(created.files.find(file => file.name === 'user-share.txt').data, 'base64').toString('utf8')
      await accountCall(page, 'activateRecovery', created.session_id, { password: fixture.password,
        recovery_file: recoveryFile, factors: { answers: t.questions.map(q => ({ question_id: q.id, text: q.answer })),
          guardian_response: '' } })
      await expect.poll(async () => (await accountCall(page, 'getAccountView')).backup_state,
        { timeout: 90000 }).toBe('backed_up')
      const initial = (await accountCall(page, 'getAccountView')).autopay
      assert.ok(initial?.quote)
      const required = Number(paid.payment.rate_per_block)
      assert.ok(required > 0)
      const endpoint = fixture.config.IndexerL2
      const base = `${endpoint.Scheme}://${endpoint.Host}/${endpoint.Proxy}`
      const read = async path => {
        const response = await fetch(base + path, { signal: AbortSignal.timeout(30000) })
        assert.equal(response.ok, true)
        const result = await response.json(); assert.equal(result.code, 0)
        return result.data
      }
      const contractState = async () => {
        let result = await read(`/v3/contracts/${initial.quote.contract}/state`)
        while (result.data) result = result.data
        return result.state || result
      }
      const configure = async (action, rate) => {
        const result = await walletCall(page, 'invokeUnifiedContract', {
          ContractType: 'template', SubType: 'autopay.tc', ContractAddress: initial.quote.contract,
          Action: action, Param: JSON.stringify(action === 'config' ? { amountPerBlock: String(rate), blobKeyLimit: initial.quote.module_limit } : {}),
          ParamEncoding: 'json', Assets: [],
        })
        assert.match(result.txid, /^[0-9a-f]{64}$/)
        await expect.poll(async () => {
          const current = (await contractState()).delegates?.[initial.quote.source_address]
          return action === 'cancel' ? Number(current?.balance || 0) === 0 : current?.amountPerBlock === String(rate)
        }, { timeout: 90000 }).toBe(true)
      }
      const open = async quote => {
        // Re-entering the page performs its normal AccountView read. It does
        // not confirm storage, fund, or invent a quote in the PWA.
        await page.evaluate(() => { location.hash = '#/wallet' })
        await expect(page.getByRole('heading', { name: messages.ui.managementTitle, exact: true })).toHaveCount(0)
        await page.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
        const rendered = () => page.evaluate(async () => {
          const { useAccountManagementStore } = await import('/store/accountManagement.ts')
          const state = useAccountManagementStore()
          return { loading: state.loading, quote: state.status?.autopay?.quote ?? null }
        })
        const before = await rendered()
        console.log(JSON.stringify({ event: 'autopay-page-read', loading: before.loading,
          expected_amount: quote.amount.amount, displayed_amount: before.quote?.amount.amount,
          expected_rate: quote.rate_per_block, displayed_rate: before.quote?.rate_per_block }))
        // The button can still belong to the previous cached view while this
        // page's normal read is in flight. Observe it; do not force a refresh.
        await expect.poll(async () => {
          const state = await rendered()
          return state.loading ? null : state.quote
        }, { timeout: 30000 }).toEqual(quote)
        await page.getByRole('button', { name: messages.dkvsPay, exact: true }).click()
        const dialog = page.getByRole('dialog', { name: messages.dkvsPay, exact: true })
        await expect(dialog).toBeVisible()
        const fields = dialog.locator('dl dd')
        await expect(fields.nth(0)).toContainText(quote.source_address)
        for (const [index, value] of [[1, quote.network], [2, quote.contract], [3, `${quote.amount.amount} ${quote.amount.asset}`], [4, String(quote.funding_blocks)], [5, quote.rate_per_block], [6, String(quote.module_limit)]]) {
          await expect(fields.nth(index)).toHaveText(value)
        }
        return dialog
      }
      await page.context().route('**/*', fundingRoute)
      for (const [index, rate] of [required / 2, required, required * 2].entries()) {
        if (index > 0) await configure('cancel')
        await configure('config', rate)
        let payment = (await accountCall(page, 'getAccountView')).autopay
        assert.equal(payment.can_fund, true)
        assert.equal(payment.quote.rate_per_block, String(Math.max(rate, required)))
        let dialog = await open(payment.quote)
        const beforeCancel = broadcasts
        await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
        assert.equal(broadcasts, beforeCancel)
        if (index === 0) {
          dialog = await open(payment.quote)
          await configure('cancel'); await configure('config', required * 2)
          const beforeChanged = broadcasts
          await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
          await expect(page.getByRole('alert').filter({ hasText: /参数已变化|quote.*changed/i })).toBeVisible()
          assert.equal(broadcasts, beforeChanged)
          await configure('config', rate)
        }
        payment = (await accountCall(page, 'getAccountView')).autopay
        dialog = await open(payment.quote)
        const tip = await read('/bestheight')
        const before = broadcasts
        capture = true
        await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
        await expect.poll(() => evidence.length, { timeout: 90000 }).toBe(index + 1)
        capture = false
        assert.equal(broadcasts, before + 1)
        Object.assign(evidence[index], { rate: payment.quote.rate_per_block, amount: payment.quote.amount.amount,
          asset: payment.quote.amount.asset, contract: payment.quote.contract, payer: payment.quote.source_address, quote_height: tip.height })
        await expect.poll(async () => (await accountCall(page, 'getAccountView')).autopay.status, { timeout: 90000 }).toBe('ready')
      }
      console.log(JSON.stringify({ autopay_funding_confirmation_transactions: evidence }))
    })
  } finally { await page.context().unroute('**/*', fundingRoute); await page.context().close() }
}
