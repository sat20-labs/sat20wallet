import assert from 'node:assert/strict'
import { expect } from '@playwright/test'

// L1 protocol indexing/issuance is deliberately outside the local Bitcoin
// ledger. These cases exercise the real forms, eligibility reads and review
// boundary; they never claim a successful inscription or etching. No commit
// or reveal is signed because each valid transaction review is cancelled.
export const requiredPwaMintCases = [
  'Mint PWA: ORDX deployment parameters reach review and cancellation preserves funds',
  'Mint PWA: BRC20 ticker length and self-mint parameters reach review without signing',
  'Mint PWA: Runes normalization and deployment terms survive review cancellation',
  'Mint PWA: absent ORDX BRC20 and Runes tickers cannot enter transaction review',
  'Mint PWA: DID validation and available-name review cancel without reserving or spending',
  'Mint PWA: partial and remainder amounts survive rechecks and reach review',
]

const poll = { timeout: 120000, intervals: [250, 500, 1000] }

export async function runPwaMintCases(t, fixture) {
  const { check, device, walletCall } = t
  const actor = fixture.basicWallet
  assert.ok(actor?.mnemonic && actor.password && actor.address)
  assert.equal(fixture.config.Chain, 'testnet')
  const controller = new URL(fixture.control_url)
  assert.equal(controller.protocol, 'http:')
  assert.ok(['127.0.0.1', 'localhost'].includes(controller.hostname))
  const snapshot = async () => {
    const response = await fetch(new URL('/snapshot', controller), {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
      signal: AbortSignal.timeout(120000),
    })
    const result = await response.json()
    assert.ok(response.ok && !result.error, JSON.stringify(result))
    return result
  }
  const visit = async (page, path) => {
    await page.evaluate(path => { location.hash = `#${path}` }, path)
    await expect.poll(() => new URL(page.url()).hash, poll).toBe(`#${path}`)
  }
  const openMint = async page => {
    await visit(page, '/import')
    await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(actor.mnemonic)
    await page.getByLabel('New Wallet Password', { exact: true }).fill(actor.password)
    await page.getByLabel('Confirm Password', { exact: true }).fill(actor.password)
    await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
    await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible()
    await expect.poll(() => page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().address), poll).toBe(actor.address)
    // A previous RGB case may have issued assets to this existing fixture
    // identity. Reconcile its carrier locks before taking the no-spend baseline.
    await walletCall(page, 'refreshRGB11State')
    await visit(page, '/wallet/tools')
    const tab = page.getByRole('tab', { name: 'Mint', exact: true })
    await tab.click()
    await expect(tab).toHaveAttribute('aria-selected', 'true')
  }
  const logs = page => page.evaluate(async () => {
    const [error, records] = await (await import('/utils/operationLog.ts')).getOperationLogs()
    if (error) throw error
    return records.map(record => ({ id: record.id, action: record.action, status: record.status }))
      .sort((a, b) => a.id.localeCompare(b.id))
  })
  const evidence = async page => ({
    l1: (await snapshot()).l1,
    amount: await walletCall(page, 'getAssetAmount', actor.address, '::'),
    logs: await logs(page),
  })
  const unchanged = async (page, before) => {
    const after = await evidence(page)
    assert.deepEqual(after.l1.broadcast_count, before.l1.broadcast_count, 'a cancelled or invalid mint broadcast an L1 transaction')
    assert.deepEqual(after.l1.pending_txids, before.l1.pending_txids)
    assert.deepEqual(after.l1.confirmed_txids, before.l1.confirmed_txids)
    assert.deepEqual(after.amount, before.amount, 'review/cancellation reserved or spent wallet funds')
    assert.deepEqual(after.logs, before.logs, 'review/cancellation started a signing operation')
    await expect(page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })).toBeHidden()
    await expect(page.getByText(/^txid: [0-9a-f]{64}$/)).toHaveCount(0)
  }
  const card = (page, title) => page.getByRole('heading', { name: title, exact: true }).locator('../..')
  const field = (scope, label) => scope.locator('label').filter({ hasText: new RegExp(`^${label}$`) }).locator('..')
  const input = (scope, label) => field(scope, label).locator('input')
  const select = async (page, locator, option) => {
    await locator.click()
    await page.getByRole('option', { name: option, exact: true }).click()
  }
  const assertRow = async (dialog, label, expected) => {
    const values = await dialog.getByText(label, { exact: true }).locator('..').locator('span:last-child').allTextContents()
    assert.ok(values.length, `review omitted ${label}`)
    assert.ok(values.every(value => value.trim() === expected), `${label}: ${JSON.stringify(values)} != ${expected}`)
  }
  const cancelReview = async (page, purpose, details) => {
    const dialog = page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })
    await expect(dialog).toBeVisible()
    for (const [label, value] of Object.entries({
      Purpose: purpose, To: actor.address, Asset: 'sats',
      Amount: 'Calculated when the wallet builds the transaction',
      Network: 'Bitcoin testnet', 'Fee rate': '2', ...details,
    })) await assertRow(dialog, label, value)
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    await expect(dialog).toBeHidden()
  }
  const prepareDeploy = async (page, protocol, ticker) => {
    const deploy = card(page, 'Deploy ticker')
    await select(page, field(deploy, 'Protocol').getByRole('combobox'), protocol)
    await input(deploy, 'Fee rate').fill('2')
    await deploy.getByPlaceholder('Ticker name', { exact: true }).fill(ticker)
    await input(deploy, 'Max supply').fill('12000')
    await input(deploy, 'Mint limit').fill('120')
    const button = deploy.getByRole('button', { name: 'Deploy ticker', exact: true })
    await expect(button).toBeDisabled()
    await deploy.getByRole('button', { name: 'Check', exact: true }).click()
    await expect(button).toBeEnabled()
    return { deploy, button }
  }
  // Separate contexts keep a failed validation from hiding other independent
  // protocol entries. All failures remain required failures in the report.
  const failures = []
  const run = async (index, fn) => {
    let page
    try {
      await check(requiredPwaMintCases[index], async () => {
        page = await device()
        await openMint(page)
        const before = await evidence(page)
        await fn(page)
        await unchanged(page, before)
      })
    } catch (error) { failures.push(error) }
    finally { if (page) await page.context().close() }
  }

  await run(0, async page => {
    const { deploy, button } = await prepareDeploy(page, 'ordx', 'PWAPREVIEW')
    await select(page, field(deploy, 'Assets per sat').getByRole('combobox'), '10')
    await button.click()
    await cancelReview(page, 'Deploy ticker', {
      Protocol: 'ordx', Ticker: 'PWAPREVIEW', 'Max supply': '12000', 'Mint limit': '120', 'Assets per sat': '10',
    })
    await expect(button).toBeEnabled()
    await deploy.getByPlaceholder('Ticker name', { exact: true }).fill('PWAPREVIEWB')
    await expect(button).toBeDisabled()
  })

  await run(1, async page => {
    const deploy = card(page, 'Deploy ticker')
    await select(page, field(deploy, 'Protocol').getByRole('combobox'), 'brc20')
    await deploy.getByPlaceholder('Ticker name', { exact: true }).fill('ABC')
    await deploy.getByRole('button', { name: 'Check', exact: true }).click()
    await expect(page.getByText('BRC20 tickers must be 4 or 5 characters. 4-character tickers are not self-mint; 5-character tickers are always self-mint', { exact: true })).toBeVisible()
    await expect(deploy.getByRole('button', { name: 'Deploy ticker', exact: true })).toBeDisabled()
    const { button } = await prepareDeploy(page, 'brc20', 'PWAX')
    await input(deploy, 'Decimal').fill('8')
    await expect(deploy.getByRole('checkbox')).toHaveCount(0)
    await button.click()
    await cancelReview(page, 'Deploy ticker', {
      Protocol: 'brc20', Ticker: 'PWAX', 'Max supply': '12000', 'Mint limit': '120', Decimal: '8', 'Only deployer can mint': 'Disable',
    })
    await expect(button).toBeEnabled()
    await deploy.getByPlaceholder('Ticker name', { exact: true }).fill('PWAXY')
    await expect(button).toBeDisabled()
    await expect(deploy.getByRole('checkbox')).toBeChecked()
    await expect(deploy.getByRole('checkbox')).toBeDisabled()
    await deploy.getByRole('button', { name: 'Check', exact: true }).click()
    await expect(button).toBeEnabled()
    await button.click()
    await cancelReview(page, 'Deploy ticker', {
      Protocol: 'brc20', Ticker: 'PWAXY', 'Max supply': '12000', 'Mint limit': '120', Decimal: '8', 'Only deployer can mint': 'Enable',
    })
    await expect(button).toBeEnabled()
  })

  await run(2, async page => {
    const { deploy, button } = await prepareDeploy(page, 'runes', 'pwa.preview')
    await expect(deploy.getByPlaceholder('Ticker name', { exact: true })).toHaveValue('PWA•PREVIEW')
    await input(deploy, 'Divisibility').fill('2')
    await button.click()
    await cancelReview(page, 'Deploy ticker', {
      Protocol: 'runes', Ticker: 'PWA•PREVIEW', 'Max supply': '12000', 'Mint limit': '120', Divisibility: '2', 'Only deployer can mint': 'Disable',
    })
    await expect(button).toBeEnabled()
    await deploy.getByRole('checkbox').check()
    await expect(input(deploy, 'Mint limit')).toHaveCount(0)
    await button.click()
    const dialog = page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })
    await expect(dialog).toBeVisible()
    await expect(dialog.getByText('Mint limit', { exact: true })).toHaveCount(0)
    await cancelReview(page, 'Deploy ticker', {
      Protocol: 'runes', Ticker: 'PWA•PREVIEW', 'Max supply': '12000', Divisibility: '2', 'Only deployer can mint': 'Enable',
    })
    await expect(button).toBeEnabled()
  })

  await run(3, async page => {
    const mint = card(page, 'Mint Asset')
    for (const protocol of ['ordx', 'brc20', 'runes']) {
      await select(page, field(mint, 'Protocol').getByRole('combobox'), protocol)
      if (protocol === 'runes') {
        await expect(input(mint, 'Amount')).toBeDisabled()
        await expect(mint.getByText('Runes mint amount is defined by the deployment terms.', { exact: true })).toBeVisible()
      } else await input(mint, 'Amount').fill('120')
      await mint.getByPlaceholder('Ticker name', { exact: true }).fill(protocol === 'brc20' ? 'NONE' : 'PWAMISSING')
      // Await removal of the previous error so the next toast cannot satisfy
      // this action's completion assertion before its indexer read finishes.
      const error = page.getByText('Ticker info was not found', { exact: true })
      await expect(error).toBeHidden()
      await mint.getByRole('button', { name: 'Check', exact: true }).click()
      await expect(error).toBeVisible()
      await expect(mint.getByRole('button', { name: 'Mint Asset', exact: true })).toBeDisabled()
      await expect(page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })).toBeHidden()
    }
  })

  await run(4, async page => {
    const did = card(page, 'Mint DID')
    const name = did.getByPlaceholder('Enter name', { exact: true })
    const button = did.getByRole('button', { name: 'Mint DID', exact: true })
    await name.fill('invalid/name')
    await did.getByRole('button', { name: 'Check', exact: true }).click()
    await expect(page.getByText('Name must be valid UTF-8 up to 32 bytes, contain one or two dot-separated parts, and contain no punctuation, separators, or control characters', { exact: true })).toBeVisible()
    await expect(button).toBeDisabled()
    await expect(page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })).toBeHidden()
    await input(card(page, 'Deploy ticker'), 'Fee rate').fill('2')
    await name.fill('PwaPreview.sats')
    await did.getByRole('button', { name: 'Check', exact: true }).click()
    await expect(button).toBeEnabled()
    await button.click()
    await cancelReview(page, 'Mint DID', { Name: 'pwapreview.sats' })
    await expect(button).toBeEnabled()
    // Cancellation must not mark the name as pending. Rechecking the same
    // name must still allow another preview without creating an operation.
    await did.getByRole('button', { name: 'Check', exact: true }).click()
    await expect(button).toBeEnabled()
    await button.click()
    await cancelReview(page, 'Mint DID', { Name: 'pwapreview.sats' })
    await expect(button).toBeEnabled()
    await name.fill('pwapreviewother.sats')
    await expect(button).toBeDisabled()
  })

  await run(5, async page => {
    const mint = card(page, 'Mint Asset')
    await select(page, field(mint, 'Protocol').getByRole('combobox'), 'ordx')
    await input(card(page, 'Deploy ticker'), 'Fee rate').fill('2')
    await mint.getByPlaceholder('Ticker name', { exact: true }).fill('PWAMINT')
    for (const amount of ['100', '200']) {
      await input(mint, 'Amount').fill(amount)
      await mint.getByRole('button', { name: 'Check', exact: true }).click()
      await expect(mint.getByRole('button', { name: 'Mint Asset', exact: true })).toBeEnabled()
      await expect(input(mint, 'Amount')).toHaveValue(amount)
      await mint.getByRole('button', { name: 'Mint Asset', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })
      await expect(dialog).toBeVisible()
      await assertRow(dialog, 'Amount', amount)
      await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
      await expect(input(mint, 'Amount')).toHaveValue(amount)
    }
    await input(mint, 'Amount').fill('201')
    await mint.getByRole('button', { name: 'Check', exact: true }).click()
    await expect(mint.getByRole('button', { name: 'Mint Asset', exact: true })).toBeDisabled()
  })

  if (failures.length) throw new AggregateError(failures, 'PWA mint review/cancellation cases failed')
}
