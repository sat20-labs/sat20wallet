import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { expect } from '@playwright/test'
import { Transaction } from 'bitcoinjs-lib'

export const requiredPwaRgbCases = [
  'RGB PWA: issue a real NIA contract and protect its confirmed carrier',
  'RGB PWA: export and import a standard contract without crediting the recipient',
  'RGB PWA: an out-of-band witness invoice survives authenticated reload',
  'RGB PWA: prepare an unsigned transfer before receiver validation',
  'RGB PWA: receiver validation persists the same package and grants no spendable balance',
  'RGB PWA: restored sender matches the receiver summary before broadcasting the actual transaction',
  'RGB PWA: confirmed receive conserves RGB supply and persists the actual carrier proofs',
  'RGB PWA: cancelling an unbroadcast transfer releases its reservations and preserves carrier protection',
  'RGB PWA: issue a real IFA with separately committed inflation rights',
  'RGB PWA: issue a real indivisible UDA',
  'RGB PWA: transfer the unique UDA to another wallet and persist its carrier proof',
]

const poll = { timeout: 180000, intervals: [250, 500, 1000, 2000] }
const assetKey = name => `${name.Protocol}:${name.Type}:${name.Ticker}`
const sum = (assets, key) => (assets || []).filter(asset => assetKey(asset.Name) === key).reduce((total, asset) => {
  assert.equal(asset.Amount.Precision, 0, 'fixture RGB assets use atomic unit precision')
  assert.match(String(asset.Amount.Value), /^\d+$/)
  return total + BigInt(asset.Amount.Value)
}, 0n)
const sha256 = value => createHash('sha256').update(value).digest('hex')
const sameTxIDs = (before, after) => assert.deepEqual(after.l1.broadcast_count, before.l1.broadcast_count,
  'an operation that must remain unbroadcast submitted an L1 transaction')

export async function runPwaRgbCases(t, fixture) {
  const failures = []
  for (const [udaOnly, names] of [[false, requiredPwaRgbCases.slice(0, 10)], [true, requiredPwaRgbCases.slice(10)]]) {
    if (t.selectedCases && !names.some(name => t.selectedCases.includes(name))) continue
    try { await runPwaRgbCaseGroup(t, fixture, udaOnly) } catch (error) { failures.push(error) }
  }
  if (failures.length) throw new AggregateError(failures, failures.map(error => error.message).join('\n'))
}

async function runPwaRgbCaseGroup(t, fixture, udaOnly) {
  const { check, device, ready, walletCall, accountCall, questions, answers } = t
  const senderActor = udaOnly ? fixture.udaWallets.sender : (fixture.rgbWallets?.sender ?? fixture.basicWallet)
  const receiverActor = udaOnly ? fixture.udaWallets.recipient : (fixture.rgbWallets?.recipient ?? fixture.recipient)
  for (const actor of [senderActor, receiverActor]) assert.ok(actor?.mnemonic && actor.address && actor.password)
  assert.notEqual(senderActor.address, receiverActor.address)
  const endpoint = fixture.config.IndexerL1
  const l1 = `${endpoint.Scheme}://${endpoint.Host}/${String(endpoint.Proxy || 'testnet').replace(/^\/+|\/+$/g, '')}`
  const contexts = []
  const broadcastCount = Object.create(null)
  const broadcastErrors = []
  const issuanceRequests = []
  let sender, receiver, contractId, canonicalName, armor, contractFile, invoice, requestId, transferId, witnessTxid, consignment, summary
  let beforePrepare, receiveAlreadySettled = false
  const state = async page => {
    const result = await walletCall(page, 'getRGB11State')
    assert.equal(typeof result.state, 'string')
    return JSON.parse(result.state)
  }
  const locks = async (page, actor) => Object.fromEntries(Object.entries(await walletCall(page, 'getAllLockedUtxo', actor.address))
    .map(([point, value]) => [point, typeof value === 'string' ? JSON.parse(value) : value]))
  const control = async (action, body = {}) => {
    const response = await fetch(new URL(`/${action}`, fixture.control_url), { method: 'POST',
      headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: AbortSignal.timeout(240000) })
    const text = await response.text()
    assert.ok(response.ok, `${action}: ${response.status}: ${text}`)
    const result = JSON.parse(text)
    assert.ok(!result.error, `${action}: ${result.error}`)
    if (fixture.realBitcoinControl) {
      assert.deepEqual(broadcastErrors, [], 'real L1 broadcast observation must decode every request')
      result.l1.broadcast_count = { ...broadcastCount }
    }
    return result
  }
  const evidence = async (path, body) => {
    const response = await fetch(`${l1}${path}`, body === undefined ? { signal: AbortSignal.timeout(30000) } : {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: AbortSignal.timeout(30000),
    })
    assert.ok(response.ok, `${path}: HTTP ${response.status}`)
    const result = await response.json()
    assert.equal(result.code, 0, `${path}: ${result.msg}`)
    return result.data
  }
  const dialog = (page, title) => page.getByRole('dialog').filter({ has: page.getByRole('heading', { name: title, exact: true }) })
  const visitRGB = async page => {
    await page.evaluate(() => { location.hash = '#/wallet' })
    await page.getByRole('tab', { name: 'Bitcoin', exact: true }).click()
    await page.getByRole('button', { name: 'RGB', exact: true }).click()
    await expect(page.getByText('Consistency: ok', { exact: true })).toBeVisible({ timeout: 120000 })
  }
  const refreshRGB = async page => {
    const typeButton = page.getByRole('button', { name: 'RGB', exact: true })
    // The asset-type navigation's only following button is its refresh action.
    await typeButton.locator('../..').getByRole('button').last().click()
  }
  const assetCard = (page, id = contractId) => page.locator('div.flex.min-w-0.overflow-hidden')
    .filter({ has: page.locator(`[title=${JSON.stringify(id)}]`) })
  const fresh = async actor => {
    const page = await device()
    contexts.push(page.context())
    if (fixture.realBitcoinControl) {
      page.context().on('response', response => {
        if (/\/v3\/(?:address\/asset|bitcoin\/utxos\/status)/.test(response.url())) issuanceRequests.push({ url: response.url(), status: response.status() })
      })
      page.context().on('requestfailed', request => {
        if (/\/v3\/(?:address\/asset|bitcoin\/utxos\/status)/.test(request.url())) issuanceRequests.push({ url: request.url(), error: request.failure()?.errorText })
      })
    }
    if (fixture.realBitcoinControl) page.context().on('request', request => {
      const url = new URL(request.url())
      if (request.method() !== 'POST' || ![`${l1}/btc/tx`, `${l1}/btc/txs`, `${l1}/v3/bitcoin/tx/broadcast`].includes(url.href)) return
      try {
        const body = request.postDataJSON()
        const raws = body.signedTxsHex ?? [body.signedTxHex ?? body.raw_tx]
        for (const raw of raws) {
          const txid = Transaction.fromHex(raw).getId()
          broadcastCount[txid] = (broadcastCount[txid] ?? 0) + 1
        }
      } catch (error) { broadcastErrors.push(error.message) }
    })
    page.on('console', message => {
      let value = message.text()
      if (!/^(?:fatal error:|panic:|goroutine \d|TypeError:|\[Vue warn\]|\s*(?:runtime\.|sync\.|github\.com\/sat20-labs\/|\/Users\/.*\.go:))/.test(value)
        && !/RGB11 (?:chain reconciliation .*|L1 monitor tick) failed:/.test(value)
        && !/activation_baseline_mismatch phase=local_builder_compare/.test(value)) return
      for (const fixtureActor of [senderActor, receiverActor]) {
        for (const secret of [fixtureActor.mnemonic, fixtureActor.password]) value = value.replaceAll(secret, '[redacted]')
      }
      console.error(`[RGB runtime] ${value}`)
    })
    await page.evaluate(() => { location.hash = '#/import' })
    await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(actor.mnemonic)
    await page.getByLabel('New Wallet Password', { exact: true }).fill(actor.password)
    await page.getByLabel('Confirm Password', { exact: true }).fill(actor.password)
    const started = Date.now()
    await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
    try { await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: 90000 }) } catch (error) {
      const diagnostic = await page.evaluate(async () => {
        const { snapshotWasmRuntimeDiagnostics } = await import('/utils/wasmRuntimeDiagnostics.ts')
        return { route: location.hash, visibility: document.visibilityState,
          breadcrumbs: snapshotWasmRuntimeDiagnostics(),
          rootRecovery: window.__observedRootRecovery ?? null,
          tabs: [...document.querySelectorAll('[role="tab"]')].map(element => element.textContent),
          dialogs: [...document.querySelectorAll('[role="dialog"]')].map(element => element.getAttribute('data-state')) }
      })
      error.message += `; import_start=${started}; startup=${JSON.stringify(diagnostic)}`
      throw error
    }
    const accountIndex = Number(actor.account_index ?? 0)
    assert.ok(Number.isInteger(accountIndex) && accountIndex >= 0 && accountIndex <= 3)
    await page.evaluate(async index => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      await store.switchToAccount(index)
      if (Number(store.accountIndex) !== index) throw new Error('RGB fixture account selection did not complete')
    }, accountIndex)
    assert.equal((await walletCall(page, 'getWalletAddress', accountIndex)).address, actor.address)
    assert.deepEqual(await page.evaluate(() => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      return { index: Number(store.accountIndex), address: store.address }
    }), { index: accountIndex, address: actor.address })
    // RGB mutations require an actual configured recovery account. Complete
    // the same public setup/rehearsal as the account suite before using it.
    if (!(await accountCall(page, 'status')).recovery_configured) {
      await accountCall(page, 'preflight', actor.password, [])
      await accountCall(page, 'confirmStorage', 'temporary')
      const material = await accountCall(page, 'createRecovery', {
        password: actor.password, wallets: [], recovery_mode: '2of2', questions,
      })
      const rehearsal = await accountCall(page, 'rehearse', material.session_id,
        answers, material.user_share, actor.password)
      assert.equal(rehearsal.verified, true)
    }
    await expect.poll(async () => {
      const status = await accountCall(page, 'status')
      return status.recovery_configured && !status.pending_changes && !status.managed_data_dirty
    }, poll).toBe(true)
    await visitRGB(page)
    return page
  }
  const reload = async (page, actor) => {
    await page.reload()
    await ready(page)
    await expect.poll(() => new URL(page.url()).hash.startsWith('#/unlock'), poll).toBe(true)
    await page.locator('form input[type="password"]').fill(actor.password)
    await page.evaluate(async () => {
      const { resetWasmRuntimeDiagnostics } = await import('/utils/wasmRuntimeDiagnostics.ts')
      resetWasmRuntimeDiagnostics()
    })
    const started = Date.now()
    await page.locator('form button[type="submit"]').click()
    try { await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: 90000 }) } catch (error) {
      const diagnostic = await page.evaluate(async () => {
        const { snapshotWasmRuntimeDiagnostics } = await import('/utils/wasmRuntimeDiagnostics.ts')
        return { route: location.hash, breadcrumbs: snapshotWasmRuntimeDiagnostics(),
          tabs: [...document.querySelectorAll('[role="tab"]')].map(element => ({
            label: element.textContent, hidden: !!element.closest('[aria-hidden="true"], [inert]'),
            display: getComputedStyle(element).display, boxes: element.getClientRects().length,
          })), dialogs: [...document.querySelectorAll('[role="dialog"]')].map(element => ({
            state: element.getAttribute('data-state'), open: getComputedStyle(element).display !== 'none',
          })) }
      })
      error.message += `; reload_start=${started}; startup=${JSON.stringify(diagnostic)}`
      throw error
    }
    console.log(`[RGB startup timing] ${JSON.stringify({ elapsed_ms: Date.now() - started,
      breadcrumbs: await page.evaluate(async () => {
        const { snapshotWasmRuntimeDiagnostics } = await import('/utils/wasmRuntimeDiagnostics.ts')
        return snapshotWasmRuntimeDiagnostics()
      }) })}`)
    const accountIndex = Number(actor.account_index ?? 0)
    assert.deepEqual(await page.evaluate(() => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      return { index: Number(store.accountIndex), address: store.address }
    }), { index: accountIndex, address: actor.address }, 'authenticated reload must preserve the selected RGB actor')
    assert.equal((await walletCall(page, 'getWalletAddress', accountIndex)).address, actor.address)
    await visitRGB(page)
  }
  const downloadBytes = async (page, button) => {
    const waiting = page.waitForEvent('download')
    await button.click()
    const download = await waiting
    assert.equal(await download.failure(), null)
    const stream = await download.createReadStream()
    assert.ok(stream)
    const chunks = []
    for await (const chunk of stream) chunks.push(chunk)
    const bytes = Buffer.concat(chunks)
    assert.ok(bytes.length > 0)
    return { bytes, filename: download.suggestedFilename() }
  }
  const issue = async (schema, ticker, supply, inflation) => {
    if (!sender) sender = await fresh(senderActor)
    await sender.getByRole('button', { name: 'Issue RGB Asset', exact: true }).click()
    const form = dialog(sender, 'Issue RGB Asset')
    await form.locator('select').selectOption(schema)
    await form.getByPlaceholder('USDT', { exact: true }).fill(ticker)
    await form.getByPlaceholder('Tether USD', { exact: true }).fill(`PWA ${schema} acceptance`)
    await form.locator('input[type="number"]').fill('0')
    if (schema !== 'UDA') await form.getByPlaceholder('1000', { exact: true }).fill(supply)
    if (schema === 'IFA') await form.getByPlaceholder('9000', { exact: true }).fill(inflation)
    await form.getByRole('button', { name: 'Issue RGB Asset', exact: true }).click()
    await expect(form.locator('p.text-red-400, p.text-emerald-400')).toBeVisible({ timeout: 180000 })
    if (await form.locator('p.text-red-400').isVisible()) {
      const current = await state(sender)
      const plain = await evidence(`/v3/address/asset/${senderActor.address}/::`)
      const locked = await locks(sender, senderActor)
      console.error(JSON.stringify({ observation: 'RGB issue rejected', schema, requests: issuanceRequests.slice(-30),
        error: await form.locator('p.text-red-400').innerText(),
        candidates: plain.map(row => ({ outpoint: row.Outpoint, value: row.Value, locked: locked[row.Outpoint] ?? null })),
        outputs: (current.outputs ?? []).map(row => ({ outpoint: row.OutPointStr, assets: row.Assets })),
        proofs: (current.proofs ?? []).map(row => ({ outpoint: row.outpoint, status: row.status, asset: row.asset_name, confirmations: row.confirmations })),
        transfers: (current.transfers ?? []).map(row => ({ id: row.transfer_id, status: row.status, txid: row.witness_txid })) }))
      throw new Error(`RGB ${schema} issuance rejected: ${await form.locator('p.text-red-400').innerText()}`)
    }
    await expect(form.getByText('Asset issued. The contract consignment is shown for other wallets to import.', { exact: true })).toBeVisible()
    const id = (await form.getByText('RGB Contract ID', { exact: true }).locator('..').locator('p').last().textContent()).trim()
    const key = (await form.getByText('Full asset name', { exact: true }).locator('..').locator('p').last().textContent()).trim()
    assert.match(id, /^rgb:/)
    assert.match(key, schema === 'UDA' ? /^rgb11:o:[0-9a-f]{64}$/ : /^rgb11:f:[0-9a-f]{64}$/)
    const contractArmor = await form.locator('textarea[readonly]').inputValue()
    assert.match(contractArmor, /-----BEGIN RGB CONSIGNMENT-----/)
    const standard = await downloadBytes(sender, form.getByRole('button', { name: 'Download Standard Contract File (.rgb)', exact: true }))
    assert.match(standard.filename, /\.rgb$/)
    const issued = await state(sender)
    if (issued.consistency_status !== 'ok') {
      console.error(JSON.stringify({ observation: 'RGB issue consistency failure', schema, contract_id: id,
        asset: key, consistency: issued.consistency_status, sync: issued.sync_status,
        proofs: (issued.proofs ?? []).map(proof => ({ outpoint: proof.outpoint, asset: proof.asset_name,
          status: proof.status, confirmations: proof.confirmations, witness_txid: proof.witness_txid,
          policy_status: proof.policy_status, policy_reason: proof.policy_reason })),
        transfers: (issued.transfers ?? []).map(transfer => ({ id: transfer.transfer_id,
          direction: transfer.direction, asset: transfer.asset?.Name, status: transfer.status,
          witness_txid: transfer.witness_txid, reject_reason: transfer.reject_reason,
          input_outpoints: transfer.input_outpoints, output_outpoints: transfer.output_outpoints })) }))
      // One diagnostic refresh after failure; the assertion below still uses
      // the original failed snapshot. This must never become retry-to-pass.
      const diagnostic = await sender.evaluate(async () => {
        const [error, result] = await window.sat20.refreshRGB11State()
        return { error: error?.message ?? null, result: result?.result ?? null }
      }).catch(error => ({ error: error.message, result: null }))
      console.error(JSON.stringify({ observation: 'RGB failed issuance diagnostic refresh', schema, ...diagnostic }))
    }
    assert.equal(issued.consistency_status, 'ok')
    assert.equal(sum(issued.available_assets, key), BigInt(schema === 'UDA' ? '1' : supply))
    const info = issued.ticker_infos.find(info => info.contract_id === id)
    assert.ok(info)
    assert.equal(info.asset_key, key)
    assert.equal(info.canonical_name ?? '', '')
    assert.equal(info.verified, false)
    assert.equal(info.ticker, `${ticker.toLowerCase()}@${info.genesis_address.slice(-12)}`)
    await form.getByRole('button', { name: 'Close', exact: true }).first().click()
    await expect(assetCard(sender, id)).toBeVisible()
    return { id, key, contractArmor, standard, issued }
  }
  const openReceive = async () => {
    await assetCard(receiver).getByRole('button', { name: 'Receive', exact: true }).click()
    const receive = dialog(receiver, 'Receive RGB Asset')
    await expect(receive).toBeVisible()
    return receive
  }
  const prepareSend = async (invoiceText, amount) => {
    await sender.bringToFront()
    const card = assetCard(sender)
    const sendButton = card.getByRole('button', { name: 'Send', exact: true })
    try {
      await sendButton.click()
    } catch (error) {
      // Observe the exact sender gate; the receiver dialog is a different page.
      // Keep the original failure even if a blocked WASM callback prevents reads.
      let timer
      try {
        const diagnostic = await Promise.race([
          (async () => {
            const ui = await sender.evaluate(async asset => {
              const { useRGB11Store } = await import('/store/rgb11.ts')
              const store = useRGB11Store()
              const reads = (window.__observedAssetReads || []).filter(read => read.method === 'getRGB11State').slice(-8)
              return { state: JSON.parse(JSON.stringify(store.state)), error: store.error,
                reads: reads.map(read => ({ ...read, rgb: read.rgb ? { ...read.rgb,
                  assets: read.rgb.assets.filter(item => item.asset === asset),
                  available: read.rgb.available.filter(item => item.asset === asset),
                  pending: read.rgb.pending.filter(item => item.asset === asset) } : undefined })) }
            }, canonicalName)
            return { asset: canonicalName, ui: { initialized: ui.state.initialized,
              consistency: ui.state.consistency_status, sync: ui.state.sync_status,
              total: String(sum(ui.state.assets, canonicalName)),
              available: String(sum(ui.state.available_assets, canonicalName)),
              pending: String(sum(ui.state.pending_assets, canonicalName)) }, asset_reads: ui.reads,
              ui_error: ui.error, card: await card.innerText(), disabled: await sendButton.isDisabled() }
          })(),
          new Promise(resolve => { timer = setTimeout(() => resolve({ read_timed_out: true }), 3000) }),
        ])
        console.error(JSON.stringify({ observation: 'RGB sender Send gate failure', diagnostic }))
      } catch (diagnosticError) {
        console.error(JSON.stringify({ observation: 'RGB sender Send gate read failed', message: diagnosticError.message }))
      } finally { clearTimeout(timer) }
      throw error
    }
    const send = dialog(sender, 'Send RGB Asset')
    await send.getByRole('button', { name: 'Invoice', exact: true }).click()
    await send.getByPlaceholder('1', { exact: true }).fill(amount)
    await send.locator('textarea').first().fill(invoiceText)
    await send.getByRole('button', { name: 'Prepare Transfer', exact: true }).click()
    await expect(send.getByRole('button', { name: 'Download armored consignment for PWA', exact: true })).toBeVisible({ timeout: 180000 })
    return send
  }

  try {
    if (udaOnly) {
      await check(requiredPwaRgbCases[10], async () => {
        if (fixture.rgbReadbackWitness) {
          sender = await fresh(senderActor); receiver = await fresh(receiverActor)
          const original = (await state(sender)).transfers.find(item => item.direction === 'send' && item.witness_txid === fixture.rgbReadbackWitness)
          assert.ok(original, 'identify original confirmed UDA transfer')
          canonicalName = assetKey(original.asset.Name)
          const samples = []
          const observe = async label => {
            const snapshot = await state(receiver)
            const proof = snapshot.proofs.find(item => item.witness_txid === fixture.rgbReadbackWitness && assetKey(item.asset_name) === canonicalName)
            samples.push({ label, initialized: snapshot.initialized, sync: snapshot.sync_status,
              consistency: snapshot.consistency_status, amount: String(sum(snapshot.available_assets, canonicalName)), total: String(sum(snapshot.assets, canonicalName)), pending: String(sum(snapshot.pending_assets, canonicalName)),
              proof: proof ? { outpoint: proof.outpoint, status: proof.status, confirmations: proof.confirmations, validation_hash: proof.validation_hash } : null })
            console.log(JSON.stringify({ observation: 'original UDA readback', ...samples.at(-1) }))
            return snapshot
          }
          await observe('fresh-dkvs-import')
          await reload(receiver, receiverActor)
          await observe('immediate-unlock')
          for (const delay of [250, 1000, 3000]) { await receiver.waitForTimeout(delay); await observe(`after-${delay}ms`) }
          const unverified = await state(receiver)
          assert.equal(sum(unverified.assets, canonicalName), 1n, 'persisted ownership must survive unlock')
          await refreshRGB(receiver)
          await expect.poll(async () => sum((await state(receiver)).available_assets, canonicalName), poll).toBe(1n)
          await observe('after-explicit-chain-refresh')
          const restored = await state(receiver)
          assert.equal(sum((await state(sender)).assets, canonicalName), 0n)
          assert.equal(sum(restored.available_assets, canonicalName), 1n)
          const proof = restored.proofs.find(item => item.witness_txid === fixture.rgbReadbackWitness && assetKey(item.asset_name) === canonicalName)
          assert.ok(proof); assert.equal(proof.status, 'settled'); assert.ok(proof.confirmations >= 1)
          assert.equal((await locks(receiver, receiverActor))[proof.outpoint]?.reason, 'rgb')
          const [output] = await evidence('/v3/bitcoin/utxos/status', { outpoints: [proof.outpoint] })
          assert.ok(output.exists && output.unspent && output.confirmations >= 1)
          await expect(assetCard(receiver, (await state(receiver)).ticker_infos.find(item => item.asset_key === canonicalName).contract_id)).toContainText('Available: 1')
          return
        }
        const unique = await issue('UDA', 'PWAUDAT', '1')
        contractId = unique.id; canonicalName = unique.key
        assert.match(canonicalName, /^rgb11:o:[0-9a-f]{64}$/)
        const originalProofs = unique.issued.proofs.filter(proof => assetKey(proof.asset_name) === canonicalName)
        assert.ok(originalProofs.length)
        receiver = await fresh(receiverActor)
        await receiver.getByRole('button', { name: 'Import RGB Contract', exact: true }).click()
        const form = dialog(receiver, 'Import RGB Contract')
        await form.locator('#rgb11-contract-file').setInputFiles({ name: unique.standard.filename,
          mimeType: 'application/octet-stream', buffer: unique.standard.bytes })
        await form.getByRole('button', { name: 'Import RGB Contract', exact: true }).click()
        await expect(form.getByText('Imported; 0 wallet allocation(s) projected.', { exact: true })).toBeVisible({ timeout: 180000 })
        await form.getByRole('button', { name: 'Close', exact: true }).click()
        assert.equal(sum((await state(receiver)).assets, canonicalName), 0n)
        const receive = await openReceive()
        await receive.locator('select').nth(0).selectOption('out-of-band')
        await receive.locator('select').nth(1).selectOption('witness')
        await receive.getByPlaceholder('Enter amount to receive', { exact: true }).fill('1')
        // Observe the real UI-created request; standard witness invoices use an
        // independent receive key rather than the wallet's primary address script.
        await receiver.evaluate(async () => {
          const { default: manager } = await import('/utils/sat20.ts')
          const create = manager.createRGB11Invoice.bind(manager)
          manager.createRGB11Invoice = async (...args) => {
            const note = phase => window.__SAT20_E2E_NOTE__?.('createRGB11Invoice', phase, {
              same_global_instance: manager === window.sat20,
              same_verify_instance: manager === window.__SAT20_PWA_VERIFY__?.sat20,
            })
            note('enter')
            try {
              const result = await create(...args)
              note(result[0] ? 'returned-error' : 'returned')
              if (!result[0]) window.__rgbUDAReceiveRequest = result[1]
              return result
            } catch (error) { note('threw'); throw error }
          }
        })
        await receive.getByRole('button', { name: 'Generate Invoice', exact: true }).click()
        await expect(receive.locator('textarea[readonly]')).toBeVisible({ timeout: 180000 })
        const uniqueInvoice = await receive.locator('textarea[readonly]').inputValue()
        const uniqueRequest = await receiver.evaluate(() => window.__rgbUDAReceiveRequest)
        assert.equal(uniqueRequest?.invoice, uniqueInvoice)
        const uniqueReservation = (await state(receiver)).reservations.find(item => item.invoice === uniqueInvoice)
        assert.equal(uniqueReservation?.contract_id, unique.id)
        assert.equal(uniqueRequest.request_id, uniqueReservation?.request_id)
        const requestedScript = Buffer.from(uniqueRequest.witness_script, 'base64').toString('hex')
        assert.match(requestedScript, /^5120[0-9a-f]{64}$/)
        assert.notEqual(requestedScript, receiverActor.pk_script)
        beforePrepare = await control('snapshot')
        const send = await prepareSend(uniqueInvoice, '1')
        const transfer = (await state(sender)).transfers.find(item => item.direction === 'send' && item.invoice === uniqueInvoice)
        assert.ok(transfer?.transfer_id)
        const packageFile = await downloadBytes(sender, send.getByRole('button', { name: 'Download armored consignment for PWA', exact: true }))
        assert.equal(sha256(packageFile.bytes), transfer.consignment_hash)
        await expect(send.getByRole('button', { name: 'Confirm receiver validation and broadcast', exact: true })).toBeDisabled()
        sameTxIDs(beforePrepare, await control('snapshot'))
        await receive.locator('#rgb11-consignment-file').setInputFiles({ name: 'pwa-uda.asc',
          mimeType: 'text/plain', buffer: packageFile.bytes })
        await receive.getByRole('button', { name: 'Validate before broadcast', exact: true }).click()
        await expect(receive.locator('textarea[readonly]')).toHaveCount(2, { timeout: 180000 })
        const recipientSummary = JSON.parse(await receive.locator('textarea[readonly]').nth(1).inputValue())
        assert.equal(recipientSummary.contract_id, unique.id)
        assert.equal(recipientSummary.amount_raw, '1'); assert.equal(recipientSummary.precision, 0)
        assert.equal(recipientSummary.witness_txid, transfer.witness_txid)
        assert.equal(recipientSummary.consignment_hash, transfer.consignment_hash)
        assert.equal(recipientSummary.invoice_hash, sha256(uniqueInvoice))
        assert.equal(sum((await state(receiver)).available_assets, canonicalName), 0n)
        const broadcast = send.getByRole('button', { name: 'Confirm receiver validation and broadcast', exact: true })
        await send.locator('#rgb-summary-0').fill(JSON.stringify({ ...recipientSummary, amount_raw: '2' }))
        await send.getByRole('button', { name: 'Match summary fields', exact: true }).click()
        await expect(send.getByText('Validation summary does not match this transfer, recipient, amount or consignment.', { exact: true })).toBeVisible()
        await expect(send.locator('input[type="checkbox"]')).toBeDisabled()
        await expect(broadcast).toBeDisabled()
        sameTxIDs(beforePrepare, await control('snapshot'))
        await send.locator('#rgb-summary-0').fill(JSON.stringify(recipientSummary))
        await send.getByRole('button', { name: 'Match summary fields', exact: true }).click()
        await send.locator('input[type="checkbox"]').check()
        await broadcast.click()
        await expect(send.getByText(`Transaction broadcast: ${transfer.witness_txid}`, { exact: true })).toBeVisible({ timeout: 180000 })
        const submitted = await control('snapshot')
        assert.equal(submitted.l1.broadcast_count[transfer.witness_txid], 1)
        const actual = Transaction.fromHex(await evidence(`/btc/rawtx/${transfer.witness_txid}`))
        assert.equal(actual.getId(), transfer.witness_txid)
        assert.ok(actual.ins.every(input => input.witness.length))
        const actualInputs = actual.ins.map(input => `${Buffer.from(input.hash).reverse().toString('hex')}:${input.index}`)
        assert.deepEqual([...actualInputs].sort(), [...transfer.input_outpoints].sort())
        assert.ok(originalProofs.some(proof => actualInputs.includes(proof.outpoint)), 'UDA transfer did not consume its original carrier')
        const [recipientTxid, recipientIndex] = recipientSummary.recipient_outpoint.split(':')
        assert.equal(recipientTxid, actual.getId())
        assert.equal(actual.outs[Number(recipientIndex)].script.toString('hex'), requestedScript)
        await control('confirm-l1', { wait_anchors: false })
        await receive.getByRole('button', { name: 'Complete receive after broadcast', exact: true }).click()
        await expect(receive.locator('p.text-red-400, p.text-emerald-400')).toBeVisible({ timeout: 180000 })
        assert.equal((await receive.locator('p.text-red-400').allTextContents()).join(' '), '',
          `original UDA receive failed; witness=${transfer.witness_txid}; request=${uniqueRequest.request_id}`)
        await expect(receive.getByText('Consignment accepted. Notify the sender through the external channel so it can record the out-of-band ACK.', { exact: true }))
          .toBeVisible({ timeout: 180000 })
        await sender.keyboard.press('Escape'); await receiver.keyboard.press('Escape')
        await refreshRGB(sender)
        await expect.poll(async () => sum((await state(receiver)).available_assets, canonicalName), poll).toBe(1n)
        await expect.poll(async () => sum((await state(sender)).assets, canonicalName), poll).toBe(0n)
        const proof = (await state(receiver)).proofs.find(item => item.outpoint === recipientSummary.recipient_outpoint && assetKey(item.asset_name) === canonicalName)
        assert.equal(proof?.status, 'settled'); assert.ok(proof.confirmations >= 1)
        assert.equal((await locks(receiver, receiverActor))[proof.outpoint]?.reason, 'rgb')
        const [status] = await evidence('/v3/bitcoin/utxos/status', { outpoints: [proof.outpoint] })
        assert.ok(status.exists && status.unspent && status.confirmations >= 1)
        await reload(sender, senderActor); await reload(receiver, receiverActor)
        await refreshRGB(receiver)
        await expect.poll(async () => sum((await state(receiver)).available_assets, canonicalName), poll).toBe(1n)
        assert.equal(sum((await state(sender)).assets, canonicalName), 0n)
        assert.equal(sum((await state(receiver)).available_assets, canonicalName), 1n)
        assert.ok((await state(receiver)).proofs.some(saved => saved.outpoint === proof.outpoint && saved.validation_hash === proof.validation_hash))
        assert.equal((await locks(receiver, receiverActor))[proof.outpoint]?.reason, 'rgb')
        await expect(assetCard(receiver)).toContainText('Available: 1')
        await expect(assetCard(receiver).getByRole('button', { name: 'Send', exact: true })).toBeEnabled()
        const sentState = await state(sender), receivedState = await state(receiver)
        assert.equal(sum(sentState.assets, canonicalName) + sum(receivedState.assets, canonicalName), 1n)
      })
      return
    }
    const resumeExisting = fixture.resumeRGBWitnessTxID
    if (resumeExisting) {
      assert.ok(fixture.realBitcoinControl, 'continuation requires actual chain evidence')
      sender = await fresh(senderActor)
      receiver = await fresh(receiverActor)
      const candidates = (await state(sender)).transfers.filter(item => item.direction === 'send' && item.witness_txid === resumeExisting)
      assert.equal(candidates.length, 1, 'must identify exactly the original transfer')
      const original = candidates[0]
      transferId = original.transfer_id; witnessTxid = original.witness_txid
      canonicalName = assetKey(original.asset.Name); invoice = original.invoice
      const info = (await state(sender)).ticker_infos.find(item => item.asset_key === canonicalName)
      assert.ok(info?.contract_id); contractId = info.contract_id
      // Prepared-only Resume API cannot reload an already broadcast transfer.
      // The fixture reads this exact immutable object from a DKVS-recovered SDK DB.
      consignment = fixture.resumeRGBConsignment
      assert.ok(consignment)
      assert.equal(sha256(consignment), original.consignment_hash)
      const receiverState = await state(receiver)
      const histories = receiverState.transfers.filter(item => item.invoice === invoice && item.direction === 'receive' && item.witness_txid === witnessTxid)
      assert.equal(histories.length, 1)
      assert.equal(histories[0].consignment_hash, original.consignment_hash)
      receiveAlreadySettled = histories[0].status === 'settled'
      if (receiveAlreadySettled) {
        assert.equal(histories[0].output_outpoints.length, 1)
        summary = { recipient_outpoint: histories[0].output_outpoints[0] }
        requestId = histories[0].receive_request_id
        assert.equal(sum(receiverState.available_assets, canonicalName), 25n)
      } else {
        const requests = receiverState.reservations.filter(item => item.invoice === invoice)
        assert.equal(requests.length, 1); requestId = requests[0].request_id
        const receive = await openReceive()
        await expect(receive.locator('textarea[readonly]')).toHaveCount(2, { timeout: 180000 })
        assert.equal(await receive.locator('textarea[readonly]').first().inputValue(), invoice)
        summary = JSON.parse(await receive.locator('textarea[readonly]').nth(1).inputValue())
        assert.equal(summary.witness_txid, witnessTxid); assert.equal(summary.contract_id, contractId)
        assert.equal(summary.consignment_hash, sha256(consignment)); assert.equal(summary.invoice_hash, sha256(invoice))
        assert.equal(summary.amount_raw, '25'); assert.equal(summary.precision, 0)
        await receive.locator('#rgb11-consignment-file').setInputFiles({ name: 'original-pwa-rgb.asc', mimeType: 'text/plain', buffer: Buffer.from(consignment) })
        await expect(receive.getByRole('button', { name: 'Complete receive after broadcast', exact: true })).toBeEnabled()
      }
      beforePrepare = await control('snapshot')
    } else {
    await check(requiredPwaRgbCases[0], async () => {
      sender = await fresh(senderActor)
      const issued = await issue('NIA', 'PWARGB', '1000')
      contractId = issued.id; canonicalName = issued.key; armor = issued.contractArmor; contractFile = issued.standard
      const ownProofs = issued.issued.proofs.filter(proof => assetKey(proof.asset_name) === canonicalName && proof.status === 'settled')
      assert.ok(ownProofs.length)
      const locked = await locks(sender, senderActor)
      for (const proof of ownProofs) {
        assert.ok(proof.confirmations >= 1)
        assert.equal(locked[proof.outpoint]?.reason, 'rgb')
        const [status] = await evidence('/v3/bitcoin/utxos/status', { outpoints: [proof.outpoint] })
        assert.equal(status.exists, true); assert.equal(status.unspent, true); assert.ok(status.confirmations >= 1)
        const [txid, vout] = proof.outpoint.split(':')
        const raw = Transaction.fromHex(await evidence(`/btc/rawtx/${txid}`))
        assert.equal(raw.getId(), txid)
        assert.equal(raw.outs[Number(vout)].script.toString('hex'), status.pk_script)
      }
      // Garbage selection must honor the same carrier protection as normal
      // sends, including when the caller explicitly names the carrier input.
      const before = await control('snapshot')
      const beforeAmount = await walletCall(sender, 'getAssetAmount', senderActor.address, '::')
      for (const proof of ownProofs) {
        const refused = await sender.evaluate(async ({ address, point }) =>
          window.sat20wallet_wasm.sendGarbage(address, [point], 0, '1'),
        { address: receiverActor.address, point: proof.outpoint })
        assert.ok(refused && typeof refused.code === 'number', 'carrier refusal must return a WASM envelope')
        assert.notEqual(refused.code, 0, 'garbage send accepted a confirmed RGB carrier')
      }
      const after = await control('snapshot')
      sameTxIDs(before, after)
      assert.deepEqual(after.l1.pending_txids, before.l1.pending_txids)
      assert.deepEqual(await walletCall(sender, 'getAssetAmount', senderActor.address, '::'), beforeAmount)
      assert.deepEqual(await locks(sender, senderActor), locked)
      assert.equal(sum((await state(sender)).available_assets, canonicalName), 1000n)
      for (const proof of ownProofs) {
        const [status] = await evidence('/v3/bitcoin/utxos/status', { outpoints: [proof.outpoint] })
        assert.equal(status.unspent, true, 'refused garbage send spent a protected carrier')
      }
    })

    await check(requiredPwaRgbCases[1], async () => {
      receiver = await fresh(receiverActor)
      await receiver.getByRole('button', { name: 'Import RGB Contract', exact: true }).click()
      const form = dialog(receiver, 'Import RGB Contract')
      await form.locator('#rgb11-contract-file').setInputFiles({ name: contractFile.filename,
        mimeType: 'application/octet-stream', buffer: contractFile.bytes })
      await form.getByRole('button', { name: 'Import RGB Contract', exact: true }).click()
      await expect(form.getByText('Imported; 0 wallet allocation(s) projected.', { exact: true })).toBeVisible({ timeout: 180000 })
      await receiver.bringToFront()
      await form.getByRole('button', { name: 'Close', exact: true }).click()
      await expect(form).toBeHidden()
      const received = await state(receiver)
      const info = received.ticker_infos.find(info => info.contract_id === contractId)
      assert.ok(info)
      assert.equal(info.asset_key, canonicalName)
      assert.equal(info.canonical_name ?? '', '')
      assert.equal(info.verified, false)
      assert.equal(info.ticker, `pwargb@${info.genesis_address.slice(-12)}`)
      assert.equal(sum(received.assets, canonicalName), 0n)
      assert.equal(sum(received.available_assets, canonicalName), 0n)
      await expect(assetCard(receiver)).toContainText('Available: 0')
      // Also exercise the standard export action in the persisted asset list.
      const exported = await downloadBytes(sender, assetCard(sender).getByRole('button', { name: 'Download Standard Contract File (.rgb)', exact: true }))
      assert.deepEqual(exported.bytes, contractFile.bytes, 'asset-list export differs from the actual issued contract')
    })

    await check(requiredPwaRgbCases[2], async () => {
      const receive = await openReceive()
      await receive.locator('select').nth(0).selectOption('out-of-band')
      await receive.locator('select').nth(1).selectOption('witness')
      await receive.getByPlaceholder('Enter amount to receive', { exact: true }).fill('25')
      await receive.getByRole('button', { name: 'Generate Invoice', exact: true }).click()
      await expect(receive.locator('textarea[readonly]')).toBeVisible({ timeout: 180000 })
      invoice = await receive.locator('textarea[readonly]').inputValue()
      assert.match(invoice, /^rgb:/)
      const reservation = (await state(receiver)).reservations.find(item => item.invoice === invoice)
      assert.ok(reservation?.request_id)
      requestId = reservation.request_id
      assert.equal(reservation.transport_mode, 'out-of-band')
      assert.equal(reservation.amount_raw, '25')
      await reload(receiver, receiverActor)
      const restored = await openReceive()
      await expect(restored.locator('textarea[readonly]')).toHaveValue(invoice, { timeout: poll.timeout })
      assert.equal((await state(receiver)).reservations.filter(item => item.request_id === requestId).length, 1)
    })

    await check(requiredPwaRgbCases[3], async () => {
      beforePrepare = await control('snapshot')
      const send = await prepareSend(invoice, '25')
      const outgoing = (await state(sender)).transfers.filter(item => item.direction === 'send' && item.invoice === invoice)
      assert.equal(outgoing.length, 1)
      const transfer = outgoing[0]
      transferId = transfer.transfer_id; witnessTxid = transfer.witness_txid
      assert.match(witnessTxid, /^[0-9a-f]{64}$/)
      assert.equal(transfer.transport_mode, 'out-of-band')
      assert.ok(transfer.input_outpoints.length)
      const downloaded = await downloadBytes(sender, send.getByRole('button', { name: 'Download armored consignment for PWA', exact: true }))
      consignment = downloaded.bytes.toString('utf8')
      assert.equal(sha256(consignment), transfer.consignment_hash)
      await expect(send.getByRole('button', { name: 'Confirm receiver validation and broadcast', exact: true })).toBeDisabled()
      assert.equal(sum((await state(sender)).available_assets, canonicalName), 0n)
      sameTxIDs(beforePrepare, await control('snapshot'))
    })

    await check(requiredPwaRgbCases[4], async () => {
      let receive = dialog(receiver, 'Receive RGB Asset')
      await receive.locator('#rgb11-consignment-file').setInputFiles({ name: 'pwa-rgb-consignment.asc', mimeType: 'text/plain', buffer: Buffer.from(consignment) })
      await receive.getByRole('button', { name: 'Validate before broadcast', exact: true }).click()
      await expect(receive.locator('textarea[readonly]')).toHaveCount(2, { timeout: 180000 })
      summary = JSON.parse(await receive.locator('textarea[readonly]').nth(1).inputValue())
      assert.equal(summary.stage, 'validated-awaiting-broadcast')
      assert.equal(summary.contract_id, contractId)
      assert.equal(summary.witness_txid, witnessTxid)
      assert.equal(summary.amount_raw, '25')
      assert.equal(summary.precision, 0)
      assert.equal(summary.consignment_hash, sha256(consignment))
      assert.equal(summary.invoice_hash, sha256(invoice))
      assert.equal(sum((await state(receiver)).available_assets, canonicalName), 0n)
      await reload(receiver, receiverActor)
      receive = await openReceive()
      // Opening the dialog starts an asynchronous SDK reservation read. Its
      // visible shell does not yet establish that the persisted request loaded.
      await expect(receive.locator('textarea[readonly]')).toHaveCount(2, { timeout: poll.timeout })
      await expect(receive.locator('textarea[readonly]').first()).toHaveValue(invoice)
      await expect(receive.locator('textarea[readonly]').nth(1)).toHaveValue(JSON.stringify(summary, null, 2))
      await expect(receive.getByRole('button', { name: 'Complete receive after broadcast', exact: true })).toBeDisabled()
      await receive.locator('#rgb11-consignment-file').setInputFiles({ name: 'pwa-rgb-consignment.asc', mimeType: 'text/plain', buffer: Buffer.from(consignment) })
      await expect(receive.getByRole('button', { name: 'Complete receive after broadcast', exact: true })).toBeEnabled()
      sameTxIDs(beforePrepare, await control('snapshot'))
    })

    }

    await check(requiredPwaRgbCases[5], async () => {
      let submitted
      if (resumeExisting) {
        // Original run already restored, matched, signed and broadcast this
        // exact transaction. Validate the cold-restored state without a replay.
        submitted = await control('snapshot')
        const [currentWitness] = await evidence('/v3/bitcoin/tx/status/batch', { txids: [witnessTxid] })
        assert.ok(currentWitness.in_mempool || (currentWitness.confirmed && currentWitness.confirmations >= 1))
        assert.equal(submitted.l1.broadcast_count[witnessTxid] ?? 0, 0, 'continuation must not broadcast again')
        sameTxIDs(beforePrepare, submitted)
      } else {
      await reload(sender, senderActor)
      await sender.getByRole('button', { name: 'Match summary fields', exact: true }).click()
      const send = dialog(sender, 'Send RGB Asset')
      await expect(send).toContainText(transferId)
      const resumed = await downloadBytes(sender, send.getByRole('button', { name: 'Download armored consignment for PWA', exact: true }))
      assert.equal(resumed.bytes.toString('utf8'), consignment)
      const broadcast = send.getByRole('button', { name: 'Confirm receiver validation and broadcast', exact: true })
      await expect(broadcast).toBeDisabled()
      await send.locator('#rgb-summary-0').fill(JSON.stringify(summary))
      await send.getByRole('button', { name: 'Match summary fields', exact: true }).click()
      await expect(send.getByText('Fields match this recipient. Confirm the external message before broadcasting.', { exact: true })).toBeVisible()
      await expect(broadcast).toBeDisabled()
      await send.locator('input[type="checkbox"]').check()
      await broadcast.click()
      await expect(send.getByText(`Transaction broadcast: ${witnessTxid}`, { exact: true })).toBeVisible({ timeout: 180000 })
      submitted = await control('snapshot')
      assert.ok(submitted.l1.pending_txids.includes(witnessTxid))
      assert.equal(submitted.l1.broadcast_count[witnessTxid], 1)
      }
      const raw = Transaction.fromHex(await evidence(`/btc/rawtx/${witnessTxid}`))
      assert.equal(raw.getId(), witnessTxid)
      assert.ok(raw.ins.every(input => input.witness.length > 0), 'actual RGB witness transaction lacks signatures')
      const actualInputs = raw.ins.map(input => `${Buffer.from(input.hash).reverse().toString('hex')}:${input.index}`).sort()
      const prepared = (await state(sender)).transfers.find(item => item.transfer_id === transferId)
      assert.deepEqual(actualInputs, [...prepared.input_outpoints].sort())
      const [pointTxid, pointIndex] = summary.recipient_outpoint.split(':')
      assert.equal(pointTxid, witnessTxid)
      const [output] = await evidence('/v3/bitcoin/utxos/status', { outpoints: [summary.recipient_outpoint] })
      assert.equal(output.exists, true); assert.equal(output.unspent, true); if (!resumeExisting) assert.equal(output.confirmations || 0, 0)
      assert.equal(raw.outs[Number(pointIndex)].script.toString('hex'), output.pk_script)
      assert.equal(sum((await state(receiver)).available_assets, canonicalName), receiveAlreadySettled ? 25n : 0n)
      await sender.keyboard.press('Escape')
    })

    await check(requiredPwaRgbCases[6], async () => {
      const [statusBefore] = await evidence('/v3/bitcoin/tx/status/batch', { txids: [witnessTxid] })
      if (!statusBefore.confirmed) {
        const confirmed = await control('confirm-l1', { wait_anchors: false })
        assert.ok(confirmed.l1.confirmed_txids.includes(witnessTxid))
      }
      const [statusAfter] = await evidence('/v3/bitcoin/tx/status/batch', { txids: [witnessTxid] })
      assert.ok(statusAfter.confirmed && statusAfter.confirmations >= 1)
      if (!receiveAlreadySettled) {
      const receive = dialog(receiver, 'Receive RGB Asset')
      await receive.getByRole('button', { name: 'Complete receive after broadcast', exact: true }).click()
      await expect(receive.locator('p.text-red-400, p.text-emerald-400')).toBeVisible({ timeout: 180000 })
      assert.equal((await receive.locator('p.text-red-400').allTextContents()).join(' '), '',
        `original RGB receive failed; witness=${witnessTxid}; request=${requestId}`)
      await expect(receive.getByText('Consignment accepted. Notify the sender through the external channel so it can record the out-of-band ACK.', { exact: true }))
        .toBeVisible({ timeout: 180000 })
      await receiver.keyboard.press('Escape')
      }
      await refreshRGB(sender)
      await expect.poll(async () => sum((await state(sender)).available_assets, canonicalName), poll).toBe(975n)
      await expect.poll(async () => sum((await state(receiver)).available_assets, canonicalName), poll).toBe(25n)
      const sent = await state(sender), received = await state(receiver)
      assert.equal(sum(sent.assets, canonicalName) + sum(received.assets, canonicalName), 1000n)
      const proof = received.proofs.find(item => item.outpoint === summary.recipient_outpoint && assetKey(item.asset_name) === canonicalName)
      assert.ok(proof)
      assert.equal(proof.status, 'settled'); assert.ok(proof.confirmations >= 1)
      assert.equal((await locks(receiver, receiverActor))[proof.outpoint]?.reason, 'rgb')
      const [output] = await evidence('/v3/bitcoin/utxos/status', { outpoints: [proof.outpoint] })
      assert.equal(output.unspent, true); assert.ok(output.confirmations >= 1)
      await reload(receiver, receiverActor)
      await refreshRGB(receiver)
      await expect.poll(async () => sum((await state(receiver)).available_assets, canonicalName), poll).toBe(25n)
      assert.equal(sum((await state(receiver)).available_assets, canonicalName), 25n)
      assert.equal((await locks(receiver, receiverActor))[proof.outpoint]?.reason, 'rgb')
      await expect(assetCard(receiver)).toContainText('Available: 25')
      await expect(assetCard(sender)).toContainText('Available: 975')
    })

    await check(requiredPwaRgbCases[7], async () => {
      if (fixture.rgbCancellationDiagnostic) {
        const observe = async label => {
          const snapshot = await state(sender)
          const locked = await locks(sender, senderActor)
          console.log(JSON.stringify({ observation: 'original cancel gate', label,
            total: String(sum(snapshot.assets, canonicalName)), available: String(sum(snapshot.available_assets, canonicalName)), pending: String(sum(snapshot.pending_assets, canonicalName)),
            sync: snapshot.sync_status, consistency: snapshot.consistency_status,
            proofs: snapshot.proofs.filter(item => assetKey(item.asset_name) === canonicalName).map(item => ({ outpoint: item.outpoint, status: item.status, confirmations: item.confirmations, validation_hash: item.validation_hash, lock_reason: locked[item.outpoint]?.reason ?? null })),
            transfers: snapshot.transfers.filter(item => assetKey(item.asset.Name) === canonicalName).map(item => ({ id: item.transfer_id, status: item.status, reject_reason: item.reject_reason, min_confirmations: item.min_confirmations, input_outpoints: item.input_outpoints, output_outpoints: item.output_outpoints })),
            reservations: snapshot.reservations.filter(item => item.direction === 'send').map(item => ({ id: item.id, status: item.status, transfer_status: item.transfer?.status })) }))
        }
        const pending = (await state(sender)).transfers.filter(item => item.direction === 'send' && item.status === 'prepared' && assetKey(item.asset.Name) === canonicalName)
        assert.equal(pending.length, 1, 'resume the original unbroadcast cancellation, without preparing a new transfer')
        const before = await control('snapshot')
        await sender.evaluate(async () => {
          const { default: manager } = await import('/utils/sat20.ts')
          const cancel = manager.cancelRGB11OutOfBandTransfer.bind(manager)
          manager.cancelRGB11OutOfBandTransfer = async (...args) => {
            window.__observedRGB11Cancellation = { started: true }
            try {
              const result = await cancel(...args)
              window.__observedRGB11Cancellation = { started: true, finished: true, error: result[0]?.message ?? null, cancelled: result[1]?.cancelled }
              return result
            } catch (error) {
              window.__observedRGB11Cancellation = { started: true, finished: true, thrown: String(error) }
              throw error
            }
          }
        })
        await observe('before-cancel')
        sender.once('dialog', async confirmation => {
          assert.equal(confirmation.type(), 'confirm')
          assert.match(confirmation.message(), /unbroadcast out-of-band/)
          await confirmation.accept()
        })
        await sender.getByRole('button', { name: 'Cancel and release reservation', exact: true }).click()
        await sender.waitForFunction(() => window.__observedRGB11Cancellation?.started, undefined, { timeout: 10000 })
        await sender.waitForFunction(() => window.__observedRGB11Cancellation?.finished, undefined, { timeout: 180000 })
        const result = await sender.evaluate(() => window.__observedRGB11Cancellation)
        console.log(JSON.stringify({ observation: 'original cancel result', ...result }))
        await observe('after-cancel')
        assert.deepEqual(result, { started: true, finished: true, error: null, cancelled: true })
        await refreshRGB(sender)
        await expect.poll(async () => (await state(sender)).transfers.find(item => item.transfer_id === pending[0].transfer_id)?.status, poll).toBe('rejected')
        await expect.poll(async () => sum((await state(sender)).available_assets, canonicalName), poll).toBe(975n)
        assert.equal((await state(sender)).transfers.find(item => item.transfer_id === pending[0].transfer_id)?.reject_reason, 'user-rejected')
        const protectedInputs = await locks(sender, senderActor)
        for (const point of pending[0].input_outpoints) assert.equal(protectedInputs[point]?.reason, 'rgb')
        const outputs = await evidence('/v3/bitcoin/utxos/status', { outpoints: pending[0].input_outpoints })
        assert.ok(outputs.every(output => output.exists && output.unspent))
        sameTxIDs(before, await control('snapshot'))
        return
      }
      // Focused continuation skips the preceding receive case's chain refresh.
      // Establish its real readiness prerequisite rather than re-running it.
      await refreshRGB(sender)
      await expect.poll(async () => sum((await state(sender)).available_assets, canonicalName), poll).toBe(975n)
      const before = await control('snapshot')
      const lockedBefore = await locks(sender, senderActor)
      let receive = await openReceive()
      // Settled receives remain in transfer history, while the resumable request
      // list is empty. The next invoice starts from the actual fresh UI form.
      const prior = (await state(receiver)).transfers.find(item => item.invoice === invoice && item.direction === 'receive')
      assert.equal(prior?.status, 'settled')
      assert.ok(!(await state(receiver)).reservations.some(item => item.request_id === requestId))
      if (await receive.locator('textarea[readonly]').count()) {
        // A failed cancellation run can leave its unused receiver request.
        // Reuse only the exact authenticated amount/contract request; no reset.
        const existingInvoice = await receive.locator('textarea[readonly]').inputValue()
        const unused = (await state(receiver)).reservations.filter(item => item.invoice === existingInvoice)
        assert.equal(unused.length, 1)
        assert.equal(unused[0].contract_id, contractId); assert.equal(unused[0].amount_raw, '5')
        assert.equal(unused[0].transport_mode, 'out-of-band')
      } else {
        await receive.locator('select').nth(0).selectOption('out-of-band')
        await receive.locator('select').nth(1).selectOption('witness')
        await receive.getByPlaceholder('Enter amount to receive', { exact: true }).fill('5')
        await receive.getByRole('button', { name: 'Generate Invoice', exact: true }).click()
        await expect(receive.locator('textarea[readonly]')).toBeVisible({ timeout: 180000 })
      }
      const unusedInvoice = await receive.locator('textarea[readonly]').inputValue()
      assert.notEqual(unusedInvoice, invoice)
      await receiver.keyboard.press('Escape')
      await prepareSend(unusedInvoice, '5')
      const prepared = (await state(sender)).transfers.find(item => item.direction === 'send' && item.invoice === unusedInvoice)
      assert.ok(prepared?.transfer_id)
      assert.equal(sum((await state(sender)).available_assets, canonicalName), 0n)
      await sender.keyboard.press('Escape')
      await refreshRGB(sender)
      sender.once('dialog', async dialog => {
        assert.equal(dialog.type(), 'confirm')
        assert.match(dialog.message(), /unbroadcast out-of-band/)
        await dialog.accept()
      })
      await sender.evaluate(async () => {
        const { default: manager } = await import('/utils/sat20.ts')
        const cancel = manager.cancelRGB11OutOfBandTransfer.bind(manager)
        manager.cancelRGB11OutOfBandTransfer = async (...args) => {
          const note = phase => window.__SAT20_E2E_NOTE__?.('cancelRGB11OutOfBandTransfer', phase, {
            same_global_instance: manager === window.sat20,
            same_verify_instance: manager === window.__SAT20_PWA_VERIFY__?.sat20,
          })
          note('enter')
          try {
            const result = await cancel(...args)
            note(result[0] ? 'returned-error' : 'returned')
            window.__observedRGB11Cancellation = { finished: true, error: result[0]?.message ?? null, cancelled: result[1]?.cancelled }
            return result
          } catch (error) { note('threw'); throw error }
        }
      })
      await sender.getByRole('button', { name: 'Cancel and release reservation', exact: true }).click()
      await sender.waitForFunction(() => window.__observedRGB11Cancellation?.finished, undefined, { timeout: 180000 })
      assert.deepEqual(await sender.evaluate(() => window.__observedRGB11Cancellation), { finished: true, error: null, cancelled: true })
      await refreshRGB(sender)
      await expect.poll(async () => (await state(sender)).transfers.find(item => item.transfer_id === prepared.transfer_id)?.status, poll).toBe('rejected')
      assert.equal((await state(sender)).transfers.find(item => item.transfer_id === prepared.transfer_id)?.reject_reason, 'user-rejected')
      await expect.poll(async () => sum((await state(sender)).available_assets, canonicalName), poll).toBe(975n)
      const lockedAfter = await locks(sender, senderActor)
      for (const point of prepared.input_outpoints) {
        assert.equal(lockedAfter[point]?.reason, lockedBefore[point]?.reason, `cancel changed the original carrier protection at ${point}`)
      }
      const unspent = await evidence('/v3/bitcoin/utxos/status', { outpoints: prepared.input_outpoints })
      assert.ok(unspent.every(output => output.exists && output.unspent))
      sameTxIDs(before, await control('snapshot'))
      const cancelledState = await state(sender)
      console.log(JSON.stringify({ observation: 'RGB after unbroadcast cancellation',
        consistency: cancelledState.consistency_status, sync: cancelledState.sync_status }))
    })

    await check(requiredPwaRgbCases[8], async () => {
      if (sender) {
        const beforeIFA = await state(sender)
        console.log(JSON.stringify({ observation: 'RGB before IFA issuance',
          consistency: beforeIFA.consistency_status, sync: beforeIFA.sync_status }))
      }
      const issued = await issue('IFA', 'PWAIFA', '1000', '9000')
      const controlKey = issued.key.replace(/^rgb11:f:/, 'rgb11:control:')
      assert.equal(sum(issued.issued.available_assets, controlKey), 9000n)
      const controls = issued.issued.proofs.filter(proof => assetKey(proof.asset_name) === controlKey)
      const ordinary = issued.issued.proofs.filter(proof => assetKey(proof.asset_name) === issued.key)
      assert.ok(controls.length && ordinary.length)
      const assertControls = async () => {
        const persisted = await state(sender)
        assert.equal(sum(persisted.available_assets, controlKey), 9000n)
        const locked = await locks(sender, senderActor)
        for (const proof of controls) {
          assert.ok(persisted.proofs.some(saved => saved.outpoint === proof.outpoint && saved.validation_hash === proof.validation_hash && assetKey(saved.asset_name) === controlKey))
          assert.ok(ordinary.every(allocation => allocation.outpoint !== proof.outpoint), 'inflation authority must have a distinct carrier')
          assert.equal(proof.status, 'settled'); assert.ok(proof.confirmations >= 1)
          assert.equal(locked[proof.outpoint]?.reason, 'rgb')
          const [output] = await evidence('/v3/bitcoin/utxos/status', { outpoints: [proof.outpoint] })
          assert.ok(output.exists && output.unspent && output.confirmations >= 1)
        }
      }
      await assertControls()
      await reload(sender, senderActor)
      // Unlock resets RAM verification; verify current chain facts before spendability assertions.
      await refreshRGB(sender)
      await expect.poll(async () => sum((await state(sender)).available_assets, controlKey), poll).toBe(9000n)
      await assertControls()
    })
    await check(requiredPwaRgbCases[9], async () => {
      const unique = await issue('UDA', 'PWAUDA', '1')
      await expect(assetCard(sender, unique.id)).toContainText('Available: 1')
      await expect(assetCard(sender, unique.id).getByRole('button', { name: 'Send', exact: true })).toBeEnabled()
    })
  } finally {
    for (const context of contexts.reverse()) await context.close().catch(() => {})
  }
}
