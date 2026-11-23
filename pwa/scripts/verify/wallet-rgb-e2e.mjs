import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { expect } from '@playwright/test'
import { loadEnv } from 'vite'
import { setupAccount } from './account-fixture-e2e.mjs'
import { waitForAccountWrite } from './account-management-usage-e2e.mjs'

const messages = JSON.parse(readFileSync(new URL('../../locales/en.json', import.meta.url), 'utf8'))
const tr = messages.rgb11Transfer
const inv = messages.rgb11Invoice
const pwaRoot = fileURLToPath(new URL('../../', import.meta.url))
const explorerBase = loadEnv('regtest', pwaRoot, 'VITE_').VITE_SAT20_REGTEST_L1_MEMPOOL_URL
export const requiredPwaRgbCases = [
  'RGB PWA: NIA manual batch review receipt cancellation and pending resume',
  'RGB PWA: IFA issue and inflation through actual forms',
  'RGB PWA: UDA direct transfer pending status and confirmed receipt',
]
const poll = { timeout: 180000, intervals: [500, 1000, 2000] }

export async function runPwaRgbCases(t, fixture) {
  const { walletCall, accountCall } = t
  const actors = fixture.rgbWallets || { sender: fixture.basicWallet, recipient: fixture.recipient }
  const pages = []
  const button = (scope, name) => scope.getByRole('button', { name, exact: true })
  const dialog = (page, name) => page.getByRole('dialog', { name, exact: true })
  const card = (page, asset) => page.locator(`[data-asset-key=${JSON.stringify(asset.asset)}]`)
  const checkBitcoinReference = async (scope, reference) => {
    await expect(scope.getByText(reference, { exact: true })).toBeVisible()
    const link = scope.getByRole('link', { name: reference, exact: true })
    if (explorerBase) {
      await expect(link).toHaveAttribute('href', `${explorerBase.replace(/\/+$/, '')}/tx/${reference.split(':')[0]}`)
    } else {
      await expect(link).toHaveCount(0)
    }
  }
  const view = page => walletCall(page, 'getRGB11View')
  const observe = async (page, method, action, success = true) => {
    await page.evaluate(method => {
      window.__rgbPageResult = null
      const original = window.sat20wallet_wasm[method]
      window.sat20wallet_wasm[method] = async (...args) => {
        window.sat20wallet_wasm[method] = original
        window.__rgbPageArgs = args
        const result = await original(...args)
        window.__rgbPageResult = result
        return result
      }
    }, method)
    await action()
    await page.waitForFunction(() => window.__rgbPageResult !== null, undefined, { timeout: 180000 })
    const result = await page.evaluate(() => { const result = window.__rgbPageResult; delete window.__rgbPageResult; return result })
    if (success) assert.equal(result.code, 0, `${method}: ${result.msg}`)
    else assert.notEqual(result.code, 0, `${method} must reject invalid input`)
    return result.data
  }
  const fresh = async actor => {
    const page = await t.device(); pages.push(page)
    await page.evaluate(async actor => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      const [error] = await store.importWallet(actor.mnemonic, actor.password)
      if (error) throw error
      location.hash = '#/wallet'
    }, actor)
    await waitForAccountWrite(page, accountCall)
    const index = Number(actor.account_index || 0)
    if (index !== 0) {
      await page.evaluate(index => window.__SAT20_PWA_VERIFY__.useWalletStore().switchToAccount(index), index)
      await waitForAccountWrite(page, accountCall)
    }
    assert.equal(await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().address),
      actor.address, 'RGB fixture scope does not match the selected wallet address')
    await button(page, 'RGB').click()
    await expect(button(page, tr.issue)).toBeVisible()
    return page
  }
  const close = async form => { await form.getByRole('button', { name: 'Close', exact: true }).last().click(); await expect(form).not.toBeVisible() }
  const mine = async () => {
    const response = await fetch(new URL('/confirm-l1', fixture.control_url), {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ wait_anchors: false, empty_blocks: 1 }), signal: AbortSignal.timeout(240000),
    })
    assert.equal(response.ok, true)
    assert.equal(Boolean((await response.json()).error), false)
  }
  const refreshed = async page => {
    await walletCall(page, 'refreshRGB11State')
    await page.evaluate(() => window.dispatchEvent(new Event('sat20:wallet-data-updated')))
  }
  const balance = async (page, asset, expected) => {
    await expect.poll(async () => {
      await refreshed(page)
      return (await view(page)).assets.find(item => item.asset === asset.asset)?.available
    }, poll).toBe(expected)
  }
  const issue = async (page, kind, ticker) => {
    await button(page, tr.issue).click()
    const form = dialog(page, tr.issueTitle)
    await form.locator('select').selectOption(kind)
    await form.getByRole('textbox', { name: /^Asset symbol\b/ }).fill(ticker)
    await form.getByRole('textbox', { name: /^Display name\b/ }).fill(`PWA ${kind} acceptance`)
    await form.locator('input[type=number]').fill(kind === 'UDA' ? '0' : '2')
    if (kind !== 'UDA') await form.getByPlaceholder('1000', { exact: true }).fill('100')
    if (kind === 'IFA') await form.getByPlaceholder('9000', { exact: true }).fill('20')
    const asset = await observe(page, 'issueRGB11Asset', () => button(form, tr.issue).click())
    await expect(form.getByText(asset.contract_id, { exact: true })).toBeVisible()
    const pending = page.waitForEvent('download')
    await button(form, tr.downloadContract).click()
    const download = await pending
    await expect(form.getByText(messages.common.fileDownloadStarted.replace('{filename}', download.suggestedFilename()), { exact: true })).toBeVisible()
    const contract = readFileSync(await download.path())
    await close(form)
    await expect(card(page, asset)).toBeVisible()
    return { asset, contract }
  }
  const importContract = async (page, issued) => {
    await button(page, tr.import).click()
    const form = dialog(page, tr.importTitle)
    await form.locator('textarea').fill('invalid contract')
    await observe(page, 'importRGB11Contract', () => button(form, tr.import).click(), false)
    await expect(form.locator('p.text-red-400')).toBeVisible()
    await form.locator('#rgb11-contract-file').setInputFiles({ name: 'pwa-contract.rgb', mimeType: 'application/octet-stream', buffer: issued.contract })
    const asset = await observe(page, 'importRGB11Contract', () => button(form, tr.import).click())
    assert.equal(asset.contract_id, issued.asset.contract_id)
    await expect(form.getByRole('status').filter({ hasText: tr.imported })).toBeVisible()
    assert.equal(asset.available, '0')
    await close(form)
    await expect(card(page, asset)).toBeVisible()
  }
  const receive = async (page, asset, amount, mode = 'witness', transport = 'manual') => {
    await button(card(page, asset), messages.l1AssetsTabs.receive).click()
    const form = dialog(page, inv.title)
    await form.locator('input').first().fill(amount)
    await form.locator('select:has(option[value="witness"])').selectOption(mode)
    await form.locator('select:has(option[value="manual"])').selectOption(transport)
    if (transport === 'proxy') {
      assert.ok(fixture.rgbProxy, 'the real RGB relay must be provided')
      await expect(form.locator('input').nth(1)).toBeVisible()
      await form.locator('input').nth(1).fill('')
      await observe(page, 'createRGB11Invoice', () => button(form, inv.generate).click(), false)
      await expect(form.getByRole('alert')).toBeVisible()
      await form.locator('input').nth(1).fill(fixture.rgbProxy)
    } else await expect(form.locator('input')).toHaveCount(1)
    const result = await observe(page, 'createRGB11Invoice', () => button(form, inv.generate).click())
    const input = await page.evaluate(() => JSON.parse(window.__rgbPageArgs[0]))
    assert.equal(input.receive_mode, mode)
    assert.equal(input.transport, transport)
    if (transport === 'proxy') assert.equal(input.proxy_endpoint, fixture.rgbProxy)
    assert.equal(result.receive_mode, mode)
    assert.equal(result.transport, transport)
    await expect(form.locator('textarea[readonly]').first()).toHaveValue(result.invoice)
    await close(form)
    return result
  }
  const receivePackage = async (page, asset, request, data, keepOpen = false) => {
    await button(card(page, asset), messages.l1AssetsTabs.receive).click()
    const form = dialog(page, inv.title)
    await form.locator('select:has(option[value=' + JSON.stringify(request.id) + '])').selectOption(request.id)
    await expect(form.locator('textarea[readonly]').first()).toHaveValue(request.invoice)
    await expect(form.getByText(inv.validateFilesHelp, { exact: true })).toBeVisible()
    await form.locator('input[type=file]').setInputFiles({ name: 'pwa-transfer.asc', mimeType: 'text/plain', buffer: Buffer.from(data, 'base64') })
    const result = await observe(page, 'prepareRGB11Consignment', () => button(form, tr.prepareReceive).click())
    assert.equal(Boolean(result.receipt), true)
    await expect(form.locator('textarea[readonly]').last()).toHaveValue(result.receipt)
    await expect(form.getByText(inv.shareReceiptHelp, { exact: true })).toBeVisible()
    if (!keepOpen) await close(form)
    return result
  }
  const checkRecipients = async (form, prepared) => {
    for (const [index, recipient] of prepared.recipients.entries()) {
      const row = form.getByRole('listitem').nth(index)
      await expect(row.getByText(recipient.amount.amount, { exact: true })).toBeVisible()
      await expect(row.getByText(recipient.amount.asset, { exact: true })).toBeVisible()
      await expect(row.getByText(recipient.destination, { exact: true })).toBeVisible()
    }
  }
  const prepare = async (owner, asset, requests, inflate = false) => {
    await button(card(owner, asset), inflate ? tr.inflate : messages.l1AssetsTabs.send).click()
    const form = dialog(owner, inflate ? tr.inflate : tr.sendTitle)
    if (!inflate) {
      await form.locator('select').selectOption('invoice')
      const guide = form.getByText(inv.guide.title, { exact: true })
      await guide.click()
      await expect(form.getByText(inv.guide.step3, { exact: true })).toBeVisible()
      await guide.click()
    }
    await form.locator('textarea').fill(requests.map(item => item.invoice).join('\n'))
    const prepared = await observe(owner, inflate ? 'prepareRGB11Inflation' : 'prepareRGB11Transfer', () => button(form, tr.prepare).click())
    await checkRecipients(form, prepared)
    assert.equal(prepared.needs_receipts, true)
    return { form, prepared }
  }
  const cancel = async (page, form) => {
    await button(form, tr.cancelTransfer).click()
    await expect(form.getByText(tr.taskCancelConfirm, { exact: true })).toBeVisible()
    await button(form, tr.keepTransfer).click()
    await expect(form.getByText(tr.taskCancelConfirm, { exact: true })).toHaveCount(0)
    await button(form, tr.cancelTransfer).click()
    const result = await observe(page, 'cancelRGB11Transfer', () => button(form, tr.confirmCancel).click())
    assert.equal(result.status, 'cancelled')
    assert.equal(result.can_confirm, false)
    await expect(form).not.toBeVisible()
  }
  const manual = async (owner, recipient, asset, amounts, inflate = false) => {
    const requests = []
    for (const amount of amounts) requests.push(await receive(recipient, asset, amount))
    let { form, prepared } = await prepare(owner, asset, requests, inflate)
    // Close and continue from the actual pending list. Preparing again would
    // create a different intent and would miss the UI recovery boundary.
    await button(form, tr.close).first().click()
    await expect(form).not.toBeVisible()
    await refreshed(owner)
    const pending = owner.locator('div.rounded.border.border-zinc-700').filter({ has: owner.getByText(prepared.id, { exact: true }) })
    await expect(pending.getByText(tr.taskReceiptHint, { exact: true })).toBeVisible()
    await button(pending, tr.retryTask).click()
    form = dialog(owner, tr.sendTitle)
    await form.getByText(tr.transferDetails, { exact: true }).click()
    await expect(form.getByText(prepared.id, { exact: true })).toBeVisible()
    await checkRecipients(form, prepared)
    const files = await observe(owner, 'exportRGB11Transfer', () => button(form, tr.downloadConsignment).click())
    const receipts = []
    for (const [index, request] of requests.entries()) {
      const file = files.find(item => item.recipient_index === index && item.file.name.endsWith('.asc'))
      assert.ok(file, 'each recipient needs its own consignment')
      receipts.push((await receivePackage(recipient, asset, request, file.file.data, index === requests.length - 1)).receipt)
    }
    const broadcastButton = button(form, inv.confirmAndBroadcast)
    await expect(broadcastButton).toBeDisabled()
    for (const [index, receipt] of receipts.entries()) await form.locator('textarea').nth(index).fill(receipt)
    await form.locator('textarea').first().fill('invalid receipt')
    await observe(owner, 'confirmRGB11Transfer', () => broadcastButton.click(), false)
    await expect(form.getByRole('alert')).toHaveText(tr.summaryMismatch)
    await form.locator('textarea').first().fill(receipts[0])
    const broadcast = await observe(owner, 'confirmRGB11Transfer', () => broadcastButton.click())
    assert.match(broadcast.tx_id, /^[0-9a-f]{64}$/)
    assert.notEqual(broadcast.status, 'completed', 'broadcast is not confirmed receipt')
    await close(form)
    await mine()
    // Keep the last invoice open to verify that the SDK's chain update reaches
    // the dialog without a user action.
    const openReceive = dialog(recipient, inv.title)
    const completed = messages.walletComponents.states_completed
    await expect(openReceive.getByRole('status').filter({ hasText: completed })).toBeVisible({ timeout: poll.timeout })
    await close(openReceive)
    for (const request of requests) {
      await button(card(recipient, asset), messages.l1AssetsTabs.receive).click()
      const receiveForm = dialog(recipient, inv.title)
      await receiveForm.locator('select:has(option[value=' + JSON.stringify(request.id) + '])').selectOption(request.id)
      await expect(receiveForm.locator('textarea[readonly]').first()).toHaveValue(request.invoice)
      await expect(receiveForm.getByRole('status').filter({ hasText: completed })).toBeVisible()
      await close(receiveForm)
    }
    return broadcast
  }
  let sender, receiver
  try {
    sender = await fresh(actors.sender); receiver = await fresh(actors.recipient)
    const cases = [async () => {
      const issued = await issue(sender, 'NIA', 'PWANIA')
      const details = dialog(sender, messages.rgb11Contract.title)
      const metadata = await observe(sender, 'getTickerInfo', () =>
        button(card(sender, issued.asset), `${tr.contractId}: ${issued.asset.contract_id}`).click())
      const { genesis_outpoint: outpoint } = JSON.parse(Buffer.from(JSON.parse(metadata.ticker).content, 'base64').toString())
      assert.match(outpoint, /^[0-9a-f]{64}:\d+$/)
      await expect(details.getByText(messages.rgb11Contract.genesisOutput, { exact: true })).toBeVisible()
      await checkBitcoinReference(details, outpoint)
      await close(details)
      await importContract(receiver, issued)
      await receive(receiver, issued.asset, '1', 'blind', 'proxy')
      const request = await receive(receiver, issued.asset, '1')
      const other = await issue(sender, 'NIA', 'PWAOTHER')
      const canceled = await prepare(sender, other.asset, [request])
      assert.equal(canceled.prepared.recipients[0].amount.asset, issued.asset.asset)
      await expect(canceled.form.getByText(other.asset.contract_id, { exact: true })).toHaveCount(0)
      await expect(canceled.form.getByText(/PWAOTHER/i)).toHaveCount(0)
      await cancel(sender, canceled.form)
      const sent = await manual(sender, receiver, issued.asset, ['1', '2'])
      await balance(receiver, issued.asset, '3')
      await balance(sender, issued.asset, '97')
      await sender.evaluate(() => { location.hash = '#/wallet/setting/operation-logs' })
      const logRow = sender.locator('details').filter({ has: sender.getByText(sent.id, { exact: true }) })
      await expect(logRow.locator('summary')).toContainText(messages.walletComponents.states_completed)
      const deleteAll = messages.pwaUi.logs.deleteAll
      await button(sender, deleteAll).click()
      await button(sender.getByRole('alertdialog'), deleteAll).click()
      await expect(logRow).toHaveCount(0)
      await sender.evaluate(() => { location.hash = '#/wallet' })
      await button(sender, 'RGB').click()
      await sender.getByText(tr.completedFiles, { exact: true }).click()
      const archiveRow = sender.locator('div.rounded.border.border-zinc-700').filter({ has: sender.getByText(sent.tx_id, { exact: true }) })
      await button(archiveRow, tr.viewTransfer).click()
      const saved = dialog(sender, tr.sendTitle)
      const files = await observe(sender, 'exportRGB11Transfer', () => button(saved, tr.downloadConsignment).click())
      assert.deepEqual(files.filter(item => item.file.name.endsWith('.rgb') && item.file.data).map(item => item.recipient_index), [0, 1])
      await close(saved)
    }, async () => {
      const issued = await issue(sender, 'IFA', 'PWAIFA')
      await importContract(receiver, issued)
      await manual(sender, receiver, issued.asset, ['10'], true)
      await balance(receiver, issued.asset, '10')
      await balance(sender, issued.asset, '100')
    }, async () => {
      const pair = fixture.directWallets
      assert.ok(pair, 'Direct needs independent root wallet actors')
      for (const actor of Object.values(pair)) assert.equal(Number(actor.account_index || 0), 0)
      sender = await fresh(pair.sender); receiver = await fresh(pair.recipient)
      for (const [page, actor] of [[sender, pair.sender], [receiver, pair.recipient]]) {
        await setupAccount({ ...t, catalog: async p => (await walletCall(p, 'getWalletCatalog')).wallets }, page, actor.password)
      }
      const issued = await issue(sender, 'UDA', 'PWAUDA')
      await importContract(receiver, issued)
      await refreshed(receiver) // SDK registers its own Direct receive endpoint.
      await button(card(sender, issued.asset), messages.l1AssetsTabs.send).click()
      const form = dialog(sender, tr.sendTitle)
      await form.locator('textarea').fill(pair.recipient.address)
      await form.locator('input').fill('1')
      await receiver.context().setOffline(true)
      const locksBeforePreview = await walletCall(sender, 'getAllLockedUtxo', pair.sender.address)
      const abandoned = await observe(sender, 'prepareRGB11Transfer', () => button(form, tr.prepare).click())
      assert.equal(abandoned.status, 'preview')
      assert.equal((await view(sender)).transfers.some(item => item.id === abandoned.id), false, 'Direct preview must not create a saved transfer')
      assert.deepEqual(await walletCall(sender, 'getAllLockedUtxo', pair.sender.address), locksBeforePreview, 'Direct preview must not reserve inputs')
      await observe(sender, 'cancelRGB11Transfer', () => close(form))
      assert.equal((await view(sender)).transfers.some(item => item.id === abandoned.id), false, 'Closing a preview must not save a cancelled transfer')
      assert.deepEqual(await walletCall(sender, 'getAllLockedUtxo', pair.sender.address), locksBeforePreview)
      await button(card(sender, issued.asset), messages.l1AssetsTabs.send).click()
      await form.locator('textarea').fill(pair.recipient.address)
      await form.locator('input').fill('1')
      const prepared = await observe(sender, 'prepareRGB11Transfer', () => button(form, tr.prepare).click())
      assert.equal(prepared.status, 'preview')
      // A normal delivery outage can happen after confirmation saved the exact
      // candidate. Observe the real SDK and only interrupt this page's transport.
      let deliveryFailures = 0
      const unavailable = route => { deliveryFailures++; return route.abort('failed') }
      await sender.route('**/message/service', unavailable)
      try {
        await observe(sender, 'confirmRGB11Transfer', () => button(form, messages.common.confirm).click(), false)
        assert.ok(deliveryFailures > 0, 'the actual Direct delivery request must hit the outage')
        const saved = (await view(sender)).transfers.find(item => item.id === prepared.id)
        assert.ok(saved && saved.status !== 'preview', 'the failed confirmation must already have saved this candidate')
        assert.equal(saved.can_cancel, false)
        assert.equal(saved.can_confirm, true)
        await expect(form.getByText(tr.preview, { exact: true })).toHaveCount(0)
        await form.getByText(tr.transferDetails, { exact: true }).click()
        await expect(form.getByText(prepared.id, { exact: true })).toBeVisible()
        await expect(button(form, messages.common.cancel)).toHaveCount(0)
        await expect(button(form, messages.common.confirm)).toBeEnabled()
        await expect(form.getByRole('alert')).toBeVisible()
      } finally { await sender.unroute('**/message/service', unavailable) }
      const delivered = await observe(sender, 'confirmRGB11Transfer', () => button(form, messages.common.confirm).click())
      assert.equal(delivered.id, prepared.id, 'retry must continue the original candidate')
      assert.ok(['awaiting_ack', 'awaiting_persistence'].includes(delivered.status))
      await expect(form.getByText(messages.walletComponents[`states_${delivered.status}`], { exact: true })).toBeVisible()
      assert.equal((await view(receiver)).assets.find(a => a.asset === issued.asset.asset).available, '0')
      await close(form)
      await refreshed(sender)
      // Inspect the real pending transaction's protection locks. The RGB page
      // is read-only; visiting it must not release a carrier or fee input.
      const pendingLocks = await walletCall(sender, 'getAllLockedUtxo', pair.sender.address)
      const rgbLocks = Object.entries(pendingLocks).filter(([, raw]) => {
        const lock = JSON.parse(raw)
        return ['rgb', 'pending-rgb'].includes(lock.reason) || lock.reservation_previous_reason === 'rgb'
      })
      assert.ok(rgbLocks.some(([, raw]) => JSON.parse(raw).reason === 'pending-rgb'))
      await sender.evaluate(() => { location.hash = '#/wallet/setting/utxo' })
      await expect(sender.getByRole('tab', { name: 'RGB', exact: true })).toBeVisible()
      await sender.getByRole('tab', { name: 'RGB', exact: true }).click()
      for (const [outpoint] of rgbLocks) {
        await expect(sender.locator(`tr[data-outpoint=${JSON.stringify(outpoint)}]`)).toBeVisible()
      }
      await expect(button(sender, 'Unlock')).toHaveCount(0)
      await expect(sender.getByPlaceholder('Enter UTXO (e.g. txid:vout)', { exact: true })).toHaveCount(0)
      const lockView = await view(sender)
      assert.ok(lockView.locks.some(row => row.scope === 'current' && row.transfer_ids.includes(prepared.id)),
        'pending RGB locks must identify their existing transfer, including its fee inputs')
      await sender.getByRole('tab', { name: 'BTC', exact: true }).click()
      for (const [outpoint] of rgbLocks) await expect(sender.locator(`tr[data-outpoint=${JSON.stringify(outpoint)}]`)).toHaveCount(0)
      assert.deepEqual(await walletCall(sender, 'getAllLockedUtxo', pair.sender.address), pendingLocks,
        'read-only RGB lock inspection changed protection locks')
      await sender.evaluate(() => { location.hash = '#/wallet' })
      await button(sender, 'RGB').click()
      const task = sender.locator('div.rounded.border.border-zinc-700').filter({ has: sender.getByText(prepared.id, { exact: true }) })
      await expect(task).toHaveCount(0)
      await sender.evaluate(() => { location.hash = '#/wallet/setting/operation-logs' })
      const operation = sender.locator('details').filter({ has: sender.getByText(prepared.id, { exact: true }) })
      await expect(operation.locator('summary')).toContainText(messages.walletComponents[`states_${delivered.status}`])
      await operation.locator('summary').click()
      await expect(operation.getByText(prepared.id, { exact: true })).toBeVisible()
      await expect(button(operation, messages.common.confirm)).toHaveCount(0)
      const broadcastRoute = '**/v3/bitcoin/tx/broadcast'
      let broadcastRejected = false
      const denyBroadcast = async route => {
        await route.fulfill({ status: 503, body: 'Bitcoin broadcast unavailable' })
        broadcastRejected = true
      }
      await sender.route(broadcastRoute, denyBroadcast)
      await receiver.context().setOffline(false)
      // Restoring browser transport does not restore the SDK's online grant.
      // Observe its real background recovery before the original RGB refresh.
      let reconnected
      try {
        await expect.poll(async () => {
          const state = await accountCall(receiver, 'getAccountView')
          reconnected = { state: state.state, error_code: state.error_code, modules: state.modules }
          return state.state === 'ready' && state.modules?.some(module => module.name === 'rgb11' && module.can_write)
        }, { timeout: 90000, message: 'reconnected recipient must regain SDK write qualification' }).toBe(true)
      } catch (error) {
        console.error(JSON.stringify({ rgbRecipientReconnect: reconnected }))
        throw error
      }
      try {
        await refreshed(receiver)
        await expect.poll(() => broadcastRejected, poll).toBe(true)
        await expect.poll(async () => (await view(sender)).transfers.find(item => item.id === prepared.id)?.status, poll)
          .toBe('broadcast-attempted')
        await button(sender, messages.common.refresh).click()
        await expect(operation.locator('summary')).toContainText(messages.walletComponents.states_broadcast_attempted)
        if (await operation.getAttribute('open') === null) await operation.locator('summary').click()
        await button(operation, tr.viewTransfer).click()
        const original = dialog(sender, tr.sendTitle)
        await expect(original.getByRole('status')).toHaveText(messages.walletComponents.states_broadcast_attempted)
        await original.getByText(tr.transferDetails, { exact: true }).click()
        await expect(original.getByText(prepared.id, { exact: true })).toBeVisible()
        await close(original)
      } finally {
        await sender.unroute(broadcastRoute, denyBroadcast)
      }
      await refreshed(sender)
      let transfer
      // ACK persistence lets the SDK advance the already approved transaction.
      // The UI observes that original action; it must not confirm a second time.
      await expect.poll(async () => {
        transfer = (await view(sender)).transfers.find(item => item.id === prepared.id)
        return transfer?.status
      }, poll).toBe('pending_chain')
      assert.equal(transfer.tx_id, prepared.tx_id)
      await button(sender, messages.common.refresh).click()
      await expect(operation).toHaveCount(1)
      await expect(operation.locator('summary')).toContainText(messages.walletComponents.states_pending_chain)
      if (await operation.getAttribute('open') === null) await operation.locator('summary').click()
      await expect(operation.getByText(tr.taskChainHint, { exact: true })).toBeVisible()
      await checkBitcoinReference(operation, prepared.tx_id)
      await mine()
      await expect.poll(async () => {
        await refreshed(sender)
        await button(sender, messages.common.refresh).click()
        return operation.locator('summary').textContent()
      }, poll).toContain(messages.walletComponents.states_completed)
      await balance(receiver, issued.asset, '1'); await balance(sender, issued.asset, '0')
    }]
    for (const [index, run] of cases.entries()) {
      if (t.selectedCases && !t.selectedCases.includes(requiredPwaRgbCases[index])) continue
      await t.check(requiredPwaRgbCases[index], run)
    }
  } catch (error) { await t.preserveBrowserFailureState?.(); throw error }
  finally { for (const page of pages) if (!page.isClosed()) await page.context().close() }
}
