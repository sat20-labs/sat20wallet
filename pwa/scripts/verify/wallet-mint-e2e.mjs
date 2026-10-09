import assert from 'node:assert/strict'
import { expect } from '@playwright/test'
import { Transaction } from 'bitcoinjs-lib'

// L1 protocol issuance uses the shared local fake indexer. The successful
// ORDX path below still creates and confirms real signed commit/reveal bytes;
// its new ticker and balance are never injected to make assertions pass.
export const requiredPwaMintCases = [
  'Mint PWA: ORDX deployment parameters reach review and cancellation preserves funds',
  'Mint PWA: BRC20 ticker length and self-mint parameters reach review without signing',
  'Mint PWA: Runes normalization and deployment terms survive review cancellation',
  'Mint PWA: absent ORDX BRC20 and Runes tickers cannot enter transaction review',
  'Mint PWA: DID validation and available-name review cancel without reserving or spending',
  'Mint PWA: partial and remainder amounts survive rechecks and reach review',
  'Mint PWA: ORDX deploy and mint are signed indexed confirmed and survive reload',
  'Mint PWA: BRC20 deploy and mint are signed indexed confirmed and survive reload',
  'Mint PWA: DID inscription confirms ownership and duplicate registration is rejected',
]

const poll = { timeout: 120000, intervals: [250, 500, 1000] }

export async function runPwaMintCases(t, fixture) {
  const { check, device, ready, unlock, walletCall } = t
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
  const confirmPendingL1 = async expectedTxIDs => {
    const before = await snapshot()
    assert.deepEqual(before.l1.pending_txids.slice().sort(), expectedTxIDs.slice().sort(),
      'only this inscription commit/reveal package may be confirmed')
    const response = await fetch(new URL('/confirm-l1', controller), {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ wait_anchors: false }), signal: AbortSignal.timeout(120000),
    })
    const result = await response.json()
    assert.ok(response.ok && !result.error, JSON.stringify(result))
    const after = await snapshot()
    assert.deepEqual(after.l1.pending_txids, [])
    for (const txid of expectedTxIDs) assert.ok(after.l1.confirmed_txids.includes(txid), `L1 did not confirm ${txid}`)
    return after
  }
  const observeInscribeCall = async (page, method) => page.evaluate(async method => {
    const sdk = (await import('/utils/sat20.ts')).default
    const original = sdk[method].bind(sdk)
    window.__PWA_MINT_E2E_CALLS__ ||= {}
    sdk[method] = async (...args) => {
      const response = await original(...args)
      const [error, result] = response
      window.__PWA_MINT_E2E_CALLS__[method] = { args, error: error?.message || '', result }
      return response
    }
  }, method)
  const indexerL1 = fixture.config.IndexerL1
  const indexerProxy = String(indexerL1.Proxy || 'testnet').replace(/^\/+|\/+$/g, '')
  const indexerURL = path => `${indexerL1.Scheme}://${indexerL1.Host}/${indexerProxy}/${String(path).replace(/^\/+/, '')}`
  const indexerJSON = async path => {
    const response = await fetch(indexerURL(path))
    assert.equal(response.ok, true, `L1 indexer query ${path} failed with HTTP ${response.status}`)
    return response.json()
  }
  const rawL1Transaction = async txid => {
    const result = await indexerJSON(`btc/rawtx/${txid}`)
    assert.equal(result.code, 0, result.msg)
    return Transaction.fromHex(result.data)
  }
  const assertSignedCommitReveal = async receipt => {
    assert.match(receipt?.commitTxId || '', /^[0-9a-f]{64}$/)
    assert.match(receipt?.revealTxId || '', /^[0-9a-f]{64}$/)
    assert.equal(receipt.txId, receipt.revealTxId)
    const commit = await rawL1Transaction(receipt.commitTxId)
    const reveal = await rawL1Transaction(receipt.revealTxId)
    assert.equal(commit.getId(), receipt.commitTxId)
    assert.equal(reveal.getId(), receipt.revealTxId)
    assert.ok(commit.ins.length > 0 && commit.ins.every(input => input.witness.length > 0), 'commit transaction is not signed')
    assert.ok(reveal.ins.length > 0 && reveal.ins.every(input => input.witness.length > 0), 'reveal transaction is not signed')
    assert.equal(reveal.ins.length, 1)
    assert.ok(reveal.ins[0].hash.equals(Buffer.from(receipt.commitTxId, 'hex').reverse()), 'reveal does not spend its commit transaction')
    assert.equal(reveal.ins[0].index, 0)
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
    try { await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: 90000 }) } catch (error) {
      const diagnostic = await page.evaluate(async () => {
        const { snapshotWasmRuntimeDiagnostics } = await import('/utils/wasmRuntimeDiagnostics.ts')
        return { route: location.hash, visibility: document.visibilityState,
          breadcrumbs: snapshotWasmRuntimeDiagnostics(),
          tabs: [...document.querySelectorAll('[role="tab"]')].map(element => element.textContent),
          alerts: [...document.querySelectorAll('[role="alert"]')].map(element => element.textContent),
          importing: document.querySelector('button[type="submit"]')?.disabled }
      }).catch(() => null)
      error.message += `; mint_startup=${JSON.stringify(diagnostic)}`
      throw error
    }
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

  let successPage
  try {
    await check(requiredPwaMintCases[6], async () => {
      const ticker = 'PWASUCCESS'
      const amount = '20'
      const asset = `ordx:f:${ticker}`
      const page = successPage = await device()
      await openMint(page)
      await assert.rejects(walletCall(page, 'getTickerInfo', asset), 'fresh ORDX ticker must not exist before deployment')
      const beforeDeploy = await snapshot()
      assert.deepEqual(beforeDeploy.l1.pending_txids, [], 'prior PWA group left L1 transactions pending')

      const { deploy, button } = await prepareDeploy(page, 'ordx', ticker)
      await select(page, field(deploy, 'Assets per sat').getByRole('combobox'), '1')
      await observeInscribeCall(page, 'deployTickerOrdx')
      await button.click()
      const deployDialog = page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })
      await expect(deployDialog).toBeVisible()
      await assertRow(deployDialog, 'Protocol', 'ordx')
      await assertRow(deployDialog, 'Ticker', ticker)
      await assertRow(deployDialog, 'Max supply', '12000')
      await assertRow(deployDialog, 'Mint limit', '120')
      await assertRow(deployDialog, 'Assets per sat', '1')
      await deployDialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect(deployDialog).toBeHidden()
      await expect.poll(() => page.evaluate(() => window.__PWA_MINT_E2E_CALLS__?.deployTickerOrdx), poll).toBeTruthy()
      const deployReceipt = await page.evaluate(() => window.__PWA_MINT_E2E_CALLS__?.deployTickerOrdx)
      assert.equal(deployReceipt?.error, '')
      const deployTx = deployReceipt?.result
      assert.match(deployTx?.commitTxId || '', /^[0-9a-f]{64}$/)
      assert.match(deployTx?.revealTxId || '', /^[0-9a-f]{64}$/)
      assert.equal(deployTx.txId, deployTx.revealTxId)
      await expect(deploy.getByText(/^txid: [0-9a-f]{64}$/)).toHaveText(`txid: ${deployTx.revealTxId}`)
      await confirmPendingL1([deployTx.commitTxId, deployTx.revealTxId])
      await expect.poll(async () => {
        try { return await walletCall(page, 'getTickerInfo', asset) } catch { return null }
      }, poll).toBeTruthy()
      const indexedTicker = await walletCall(page, 'getTickerInfo', asset)
      const tickerInfo = JSON.parse(indexedTicker.ticker)
      assert.equal(tickerInfo.name.Ticker, ticker)
      assert.equal(String(tickerInfo.maxSupply), '12000')
      assert.equal(String(tickerInfo.limit), '120')
      assert.equal(Number(tickerInfo.n), 1)
      assert.equal(String(tickerInfo.totalMinted), '0', 'a confirmed deployment must not pre-mint its supply')

      const mint = card(page, 'Mint Asset')
      await select(page, field(mint, 'Protocol').getByRole('combobox'), 'ordx')
      await mint.getByPlaceholder('Ticker name', { exact: true }).fill(ticker)
      await input(mint, 'Amount').fill(amount)
      await mint.getByRole('button', { name: 'Check', exact: true }).click()
      const mintButton = mint.getByRole('button', { name: 'Mint Asset', exact: true })
      await expect(mintButton).toBeEnabled({ timeout: poll.timeout })
      await observeInscribeCall(page, 'mintAssetOrdx')
      await mintButton.click()
      const mintDialog = page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })
      await expect(mintDialog).toBeVisible()
      await assertRow(mintDialog, 'Asset', asset)
      await assertRow(mintDialog, 'Amount', amount)
      await mintDialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect(mintDialog).toBeHidden()
      await expect.poll(() => page.evaluate(() => window.__PWA_MINT_E2E_CALLS__?.mintAssetOrdx), poll).toBeTruthy()
      const mintReceipt = await page.evaluate(() => window.__PWA_MINT_E2E_CALLS__?.mintAssetOrdx)
      assert.equal(mintReceipt?.error, '')
      const mintTx = mintReceipt?.result
      assert.match(mintTx?.commitTxId || '', /^[0-9a-f]{64}$/)
      assert.match(mintTx?.revealTxId || '', /^[0-9a-f]{64}$/)
      assert.equal(mintTx.txId, mintTx.revealTxId)
      await expect(mint.getByText(/^txid: [0-9a-f]{64}$/)).toHaveText(`txid: ${mintTx.revealTxId}`)
      const afterMint = await confirmPendingL1([mintTx.commitTxId, mintTx.revealTxId])

      for (const [txid, expectedParent] of [
        [deployTx.commitTxId, null], [deployTx.revealTxId, deployTx.commitTxId],
        [mintTx.commitTxId, null], [mintTx.revealTxId, mintTx.commitTxId],
      ]) {
        const tx = await rawL1Transaction(txid)
        assert.equal(tx.getId(), txid)
        assert.ok(tx.ins.length > 0 && tx.ins.every(input => input.witness.length > 0), `${txid} is not signed`)
        if (expectedParent) {
          assert.equal(tx.ins.length, 1)
          assert.ok(tx.ins[0].hash.equals(Buffer.from(expectedParent, 'hex').reverse()), 'reveal does not spend its commit transaction')
          assert.equal(tx.ins[0].index, 0)
        }
      }
      assert.ok(afterMint.l1.confirmed_txids.includes(deployTx.commitTxId))
      assert.ok(afterMint.l1.confirmed_txids.includes(deployTx.revealTxId))
      assert.ok(afterMint.l1.confirmed_txids.includes(mintTx.commitTxId))
      assert.ok(afterMint.l1.confirmed_txids.includes(mintTx.revealTxId))
      const balance = async () => {
        const value = await walletCall(page, 'getAssetAmount', actor.address, asset)
        return BigInt(value.availableAmt) + BigInt(value.lockedAmt)
      }
      await expect.poll(balance, poll).toBe(BigInt(amount))

      await page.reload()
      await ready(page)
      await unlock(page, actor.password)
      await expect.poll(async () => (await walletCall(page, 'getWalletAddress', 0)).address, poll).toBe(actor.address)
      await expect.poll(balance, poll).toBe(BigInt(amount))
      const reloadedTicker = JSON.parse((await walletCall(page, 'getTickerInfo', asset)).ticker)
      assert.equal(reloadedTicker.name.Ticker, ticker)
      assert.equal(String(reloadedTicker.maxSupply), '12000')
      assert.equal(String(reloadedTicker.limit), '120')
      assert.equal(String(reloadedTicker.totalMinted), amount, 'confirmed minted supply must equal the real reveal amount')
    })
  } finally {
    if (successPage) await successPage.context().close().catch(() => {})
  }

  let brc20Page
  try {
    await check(requiredPwaMintCases[7], async () => {
      const ticker = 'PWBX'
      const amount = '20'
      const asset = `brc20:f:${ticker}`
      const page = brc20Page = await device()
      await openMint(page)
      await assert.rejects(walletCall(page, 'getTickerInfo', asset), /GetTickerInfo error/i,
        'fresh BRC20 ticker must not exist before deployment')
      assert.deepEqual((await snapshot()).l1.pending_txids, [], 'prior PWA group left L1 transactions pending')

      const { deploy, button } = await prepareDeploy(page, 'brc20', ticker)
      await input(deploy, 'Decimal').fill('0')
      await observeInscribeCall(page, 'deployTickerBrc20')
      await button.click()
      const deployDialog = page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })
      await expect(deployDialog).toBeVisible()
      await assertRow(deployDialog, 'Protocol', 'brc20')
      await assertRow(deployDialog, 'Ticker', ticker)
      await assertRow(deployDialog, 'Max supply', '12000')
      await assertRow(deployDialog, 'Mint limit', '120')
      await assertRow(deployDialog, 'Decimal', '0')
      await assertRow(deployDialog, 'Only deployer can mint', 'Disable')
      await deployDialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect(deployDialog).toBeHidden()
      await expect.poll(() => page.evaluate(() => window.__PWA_MINT_E2E_CALLS__?.deployTickerBrc20), poll).toBeTruthy()
      const deployTx = (await page.evaluate(() => window.__PWA_MINT_E2E_CALLS__?.deployTickerBrc20))?.result
      await assertSignedCommitReveal(deployTx)
      await expect(deploy.getByText(/^txid: [0-9a-f]{64}$/)).toHaveText(`txid: ${deployTx.revealTxId}`)
      await confirmPendingL1([deployTx.commitTxId, deployTx.revealTxId])

      const readTicker = async () => {
        const response = await walletCall(page, 'getTickerInfo', asset)
        return JSON.parse(response.ticker)
      }
      await expect.poll(async () => {
        try { return await readTicker() } catch { return null }
      }, poll).toBeTruthy()
      const deployedTicker = await readTicker()
      assert.equal(deployedTicker.name.Protocol, 'brc20')
      assert.equal(deployedTicker.name.Ticker, ticker)
      assert.equal(String(deployedTicker.maxSupply), '12000')
      assert.equal(String(deployedTicker.limit), '120')
      assert.equal(Number(deployedTicker.divisibility || 0), 0)
      assert.equal(Number(deployedTicker.selfmint || 0), 0)
      assert.equal(String(deployedTicker.totalMinted), '0', 'confirmed BRC20 deployment must not pre-mint its supply')

      const mint = card(page, 'Mint Asset')
      await select(page, field(mint, 'Protocol').getByRole('combobox'), 'brc20')
      await mint.getByPlaceholder('Ticker name', { exact: true }).fill(ticker)
      await input(mint, 'Amount').fill(amount)
      await mint.getByRole('button', { name: 'Check', exact: true }).click()
      const mintButton = mint.getByRole('button', { name: 'Mint Asset', exact: true })
      await expect(mintButton).toBeEnabled({ timeout: poll.timeout })
      await observeInscribeCall(page, 'mintAssetBrc20')
      await mintButton.click()
      const mintDialog = page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })
      await expect(mintDialog).toBeVisible()
      await assertRow(mintDialog, 'Asset', asset)
      await assertRow(mintDialog, 'Amount', amount)
      await mintDialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect(mintDialog).toBeHidden()
      await expect.poll(() => page.evaluate(() => window.__PWA_MINT_E2E_CALLS__?.mintAssetBrc20), poll).toBeTruthy()
      const mintTx = (await page.evaluate(() => window.__PWA_MINT_E2E_CALLS__?.mintAssetBrc20))?.result
      await assertSignedCommitReveal(mintTx)
      await expect(mint.getByText(/^txid: [0-9a-f]{64}$/)).toHaveText(`txid: ${mintTx.revealTxId}`)
      await confirmPendingL1([mintTx.commitTxId, mintTx.revealTxId])

      const balance = async () => {
        // BRC20 mints credit the address ledger; transferable inscription
        // carriers are created only by a later transfer inscription.
        const value = await walletCall(page, 'getAssetSummary', actor.address)
        return value.assets.filter(item => `${item.Name.Protocol}:${item.Name.Type}:${item.Name.Ticker}` === asset)
          .reduce((sum, item) => sum + BigInt(item.Amount), 0n)
      }
      await expect.poll(balance, poll).toBe(BigInt(amount))
      const mintedTicker = await readTicker()
      assert.equal(String(mintedTicker.totalMinted), amount, 'confirmed BRC20 supply must reflect the real mint inscription')
      const carriers = await walletCall(page, 'getAssetAmount', actor.address, asset)
      assert.equal(BigInt(carriers.availableAmt) + BigInt(carriers.lockedAmt), 0n,
        'mint must not fabricate a transferable BRC20 inscription')

      await page.reload()
      await ready(page)
      await unlock(page, actor.password)
      await expect.poll(async () => (await walletCall(page, 'getWalletAddress', 0)).address, poll).toBe(actor.address)
      await expect.poll(balance, poll).toBe(BigInt(amount))
      const reloadedTicker = await readTicker()
      assert.equal(reloadedTicker.name.Ticker, ticker)
      assert.equal(String(reloadedTicker.maxSupply), '12000')
      assert.equal(String(reloadedTicker.limit), '120')
      assert.equal(Number(reloadedTicker.divisibility || 0), 0)
      assert.equal(String(reloadedTicker.totalMinted), amount)
    })
  } finally {
    if (brc20Page) await brc20Page.context().close().catch(() => {})
  }

  let didPage
  try {
    await check(requiredPwaMintCases[8], async () => {
      const nameValue = 'pwafresh.sats'
      const namePath = `ns/name/${encodeURIComponent(nameValue)}`
      const page = didPage = await device()
      await openMint(page)
      const absent = await indexerJSON(namePath)
      assert.equal(absent.code, -1)
      assert.equal(absent.msg, `can't find name ${nameValue}`)
      assert.equal(absent.data, null)
      const preexistingNames = await indexerJSON(`ns/address/${actor.address}?key=name`)
      assert.equal(preexistingNames.code, 0, preexistingNames.msg)
      assert.ok(Array.isArray(preexistingNames.data?.names))
      assert.ok(!preexistingNames.data.names.some(item => item.name === nameValue), 'DID name already belongs to an address')
      assert.deepEqual((await snapshot()).l1.pending_txids, [], 'prior PWA group left L1 transactions pending')

      await input(card(page, 'Deploy ticker'), 'Fee rate').fill('2')
      const did = card(page, 'Mint DID')
      const name = did.getByPlaceholder('Enter name', { exact: true })
      const button = did.getByRole('button', { name: 'Mint DID', exact: true })
      await name.fill('PwaFresh.sats')
      await did.getByRole('button', { name: 'Check', exact: true }).click()
      await expect(button).toBeEnabled()
      await observeInscribeCall(page, 'inscribeName')
      await button.click()
      const dialog = page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })
      await expect(dialog).toBeVisible()
      for (const [label, value] of Object.entries({
        Purpose: 'Mint DID', To: actor.address, Asset: 'sats',
        Amount: 'Calculated when the wallet builds the transaction',
        Network: 'Bitcoin testnet', 'Fee rate': '2', Name: nameValue,
      })) await assertRow(dialog, label, value)
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect(dialog).toBeHidden()
      await expect.poll(() => page.evaluate(() => window.__PWA_MINT_E2E_CALLS__?.inscribeName), poll).toBeTruthy()
      const didTx = (await page.evaluate(() => window.__PWA_MINT_E2E_CALLS__?.inscribeName))?.result
      await assertSignedCommitReveal(didTx)
      await expect(did.getByText(/^txid: [0-9a-f]{64}$/)).toHaveText(`txid: ${didTx.revealTxId}`)
      const confirmed = await confirmPendingL1([didTx.commitTxId, didTx.revealTxId])
      assert.ok(confirmed.l1.confirmed_txids.includes(didTx.revealTxId))

      await expect.poll(async () => {
        const response = await indexerJSON(namePath)
        return response.code === 0 ? response : null
      }, poll).toBeTruthy()
      const ownership = await indexerJSON(namePath)
      assert.equal(ownership.code, 0, ownership.msg)
      assert.equal(ownership.data.name, nameValue)
      assert.equal(ownership.data.address, actor.address)
      assert.equal(ownership.data.inscriptionAddress, actor.address)
      assert.equal(ownership.data.inscriptionId, `${didTx.revealTxId}i0`)
      assert.equal(ownership.data.utxo, `${didTx.revealTxId}:0`)
      assert.ok(Number.isSafeInteger(Number(ownership.data.height)) && Number(ownership.data.height) > 0)

      const addressNames = await indexerJSON(`ns/address/${actor.address}?key=name`)
      assert.equal(addressNames.code, 0, addressNames.msg)
      assert.ok(addressNames.data.total >= 1)
      assert.ok(addressNames.data.names.some(item => item.name === nameValue
        && item.address === actor.address && item.inscriptionId === `${didTx.revealTxId}i0`),
      'confirmed DID ownership is missing from the address name index')

      await page.reload()
      await ready(page)
      await unlock(page, actor.password)
      await visit(page, '/wallet/tools')
      const mintTab = page.getByRole('tab', { name: 'Mint', exact: true })
      await mintTab.click()
      await expect(mintTab).toHaveAttribute('aria-selected', 'true')
      const afterConfirm = await evidence(page)
      await observeInscribeCall(page, 'inscribeName')
      const reloadedDid = card(page, 'Mint DID')
      const reloadedName = reloadedDid.getByPlaceholder('Enter name', { exact: true })
      const reloadedButton = reloadedDid.getByRole('button', { name: 'Mint DID', exact: true })
      await reloadedName.fill(nameValue)
      await reloadedDid.getByRole('button', { name: 'Check', exact: true }).click()
      await expect(page.getByText('Name already exists', { exact: true })).toBeVisible()
      await expect(reloadedButton).toBeDisabled()
      await expect(page.getByRole('dialog', { name: 'Confirm Transaction', exact: true })).toBeHidden()
      assert.equal(await page.evaluate(() => window.__PWA_MINT_E2E_CALLS__?.inscribeName), undefined,
        'duplicate DID validation must reject before signing')
      await unchanged(page, afterConfirm)
    })
  } finally {
    if (didPage) await didPage.context().close().catch(() => {})
  }

  if (failures.length) throw new AggregateError(failures, 'PWA mint review/cancellation cases failed')
}
