import assert from 'node:assert/strict'
import { expect } from '@playwright/test'
import { Transaction, script as btcScript } from 'bitcoinjs-lib'

export const requiredPwaL1ProtocolCases = [
  'Assets PWA: BRC20 sends an existing transfer inscription and persists exact balances',
  'Assets PWA: BRC20 creates a transfer inscription before sending an untransferable balance',
  'Assets PWA: Runes decimal partial send preserves the reviewed amount and change',
]
const specs = ['brc_transfer', 'brc_balance', 'runes']
const poll = { timeout: 120000, intervals: [250, 500, 1000] }
const keyOf = name => `${name.Protocol}:${name.Type}:${name.Ticker}`
const atoms = (text, precision) => {
  assert.match(String(text), /^\d+(?:\.\d+)?$/)
  const [whole, fraction = ''] = String(text).split('.')
  assert.ok(fraction.length <= precision, 'amount exceeds declared precision')
  return BigInt(whole + fraction.padEnd(precision, '0'))
}

// Decode the actual Runestone edicts independently of the fake indexer's
// allocation model. Body fields use block/transaction deltas and atomic units.
const edicts = tx => {
  const output = tx.outs.find(out => out.script[0] === 0x6a && out.script[1] === 0x5d)
  assert.ok(output, 'Rune send omitted its Runestone')
  const chunks = btcScript.decompile(output.script).slice(2)
  assert.ok(chunks.every(Buffer.isBuffer))
  const bytes = Buffer.concat(chunks), values = []
  let value = 0n, shift = 0n
  for (const byte of bytes) {
    value |= BigInt(byte & 127) << shift
    if (byte & 128) { shift += 7n; assert.ok(shift < 128n) }
    else { values.push(value); value = 0n; shift = 0n }
  }
  assert.equal(shift, 0n)
  let offset = 0
  while (offset < values.length && values[offset] !== 0n) offset += 2
  assert.ok(offset < values.length, 'Rune send omitted edicts')
  const body = values.slice(offset + 1), result = []
  assert.equal(body.length % 4, 0)
  let block = 0n, index = 0n
  for (let i = 0; i < body.length; i += 4) {
    index = body[i] === 0n ? index + body[i + 1] : body[i + 1]
    block += body[i]
    result.push({ id: `${block}:${index}`, amount: body[i + 2], output: Number(body[i + 3]) })
  }
  return result
}

export async function runPwaL1ProtocolCases(t, fixture) {
  const { check, device, ready, walletCall } = t
  const endpoint = fixture.config.IndexerL1
  const base = `${endpoint.Scheme}://${endpoint.Host}/${endpoint.Proxy}`
  const observedBroadcasts = Object.create(null)
  const observationErrors = []
  const api = async (path, body) => {
    const response = await fetch(base + path, { signal: AbortSignal.timeout(30000), ...(body === undefined ? {} : { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) }) })
    assert.ok(response.ok)
    const result = await response.json()
    assert.equal(result.code, 0, `${path}: ${result.msg}`)
    return result.data
  }
  const control = async action => {
    const response = await fetch(new URL(`/${action}`, fixture.control_url), {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ wait_anchors: false }), signal: AbortSignal.timeout(120000),
    })
    assert.ok(response.ok)
    const result = await response.json()
    assert.ok(!result.error, result.error)
    if (fixture.realBitcoinControl) {
      assert.deepEqual(observationErrors, [], 'actual L1 broadcast observation failed')
      result.l1.broadcast_count = { ...observedBroadcasts }
    }
    return result
  }
  const balance = async (actor, key, precision = 0) => (await api(`/v3/address/summary/${actor.address}`))
    .filter(asset => key === '*' ? asset.Name.Type === '*' : keyOf(asset.Name) === key)
    .reduce((sum, asset) => sum + atoms(asset.Amount, precision), 0n)
  const logs = page => page.evaluate(async () => {
    const result = await window.sat20wallet_operation_log.getOperationLogs()
    if (result.code !== 0) throw new Error(result.msg)
    return JSON.parse(result.data.logs)
  })
  const importActor = async (page, actor) => {
    if (fixture.realBitcoinControl) page.context().on('request', request => {
      if (request.method() !== 'POST' || !['/btc/tx', '/btc/txs', '/v3/bitcoin/tx/broadcast'].some(path => request.url() === base + path)) return
      try {
        const body = request.postDataJSON()
        const raws = body.signedTxsHex ?? body.SignedTxsHex ?? [body.signedTxHex ?? body.SignedTxHex ?? body.raw_tx]
        for (const raw of raws) {
          const txid = Transaction.fromHex(raw).getId()
          observedBroadcasts[txid] = (observedBroadcasts[txid] ?? 0) + 1
        }
      } catch (error) { observationErrors.push(error.message) }
    })
    await page.evaluate(() => { location.hash = '#/import' })
    await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(actor.mnemonic)
    await page.getByLabel('New Wallet Password', { exact: true }).fill(actor.password)
    await page.getByLabel('Confirm Password', { exact: true }).fill(actor.password)
    const importedAt = Date.now()
    await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
    if (fixture.config.Chain === 'regtest') {
      await expect.poll(() => page.evaluate(() => window.__observedRootRecovery ?? null), poll).toBeTruthy()
      const recovery = await page.evaluate(() => window.__observedRootRecovery)
      if (recovery.error) {
        let message = recovery.error
        for (const secret of [actor.mnemonic, actor.password]) message = message.replaceAll(secret, '[redacted]')
        throw new Error(`real regtest root recovery rejected: ${message}`)
      }
    }
    await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: poll.timeout })
    console.log(JSON.stringify({ observation: 'L1 asset wallet import readiness', elapsed_ms: Date.now() - importedAt }))
    assert.equal((await walletCall(page, 'getWalletAddress', 0)).address, actor.address)
  }
  const failures = []
  for (const [index, id] of specs.entries()) {
    const name = requiredPwaL1ProtocolCases[index]
    if (t.selectedCases && !t.selectedCases.includes(name)) continue
    const contexts = []
    try {
      await check(name, async () => {
        const spec = fixture.protocolWallets[id], { sender, recipient, asset, precision, label } = spec
        const page = await device(); contexts.push(page.context())
        await importActor(page, sender)
        const amount = atoms(spec.amount, precision), initial = atoms(spec.balance, precision)
        const recipientInitial = atoms(spec.recipient_balance ?? '0', precision)
        if (spec.resume) {
          const { txids, payment_txid, coinbase_txid, sender_before_sats, receiver_before_sats, fee_rate } = spec.resume
          const statuses = await api('/v3/bitcoin/tx/status/batch', { txids })
          assert.equal(statuses.length, 3)
          assert.ok(statuses.every(tx => tx.exists && tx.confirmed && tx.confirmations >= 1))
          const transactions = await Promise.all(txids.map(async txid => Transaction.fromHex(await api(`/btc/rawtx/${txid}`))))
          let fees = 0n
          for (const tx of transactions) {
            assert.ok(tx.ins.every(input => input.witness.length > 0))
            let inputSats = 0n
            for (const input of tx.ins) {
              const parent = Transaction.fromHex(await api(`/btc/rawtx/${Buffer.from(input.hash).reverse().toString('hex')}`))
              inputSats += BigInt(parent.outs[input.index].value)
            }
            const fee = inputSats - tx.outs.reduce((sum, out) => sum + BigInt(out.value), 0n)
            assert.ok(fee >= BigInt(tx.virtualSize()) * BigInt(fee_rate))
            fees += fee
          }
          const payment = transactions.find(tx => tx.getId() === payment_txid)
          assert.ok(payment)
          const paid = payment.outs.filter(out => out.script.toString('hex') === recipient.pk_script)
          assert.equal(paid.length, 1)
          const reveal = transactions.find(tx => tx.getId() !== payment_txid && tx.ins.some(input => input.witness.some(witness => btcScript.decompile(witness)?.some(chunk => Buffer.isBuffer(chunk) && chunk.toString().includes('"transfer"')))))
          assert.ok(reveal)
          const payload = btcScript.decompile(reveal.ins[0].witness.at(-2)).filter(Buffer.isBuffer)
            .map(chunk => { try { return JSON.parse(chunk.toString()) } catch { return null } }).find(value => value?.p === 'brc-20')
          assert.deepEqual(payload, { p: 'brc-20', op: 'transfer', tick: asset.split(':')[2].toLowerCase(), amt: spec.amount })
          assert.ok(txids.includes(Buffer.from(reveal.ins[0].hash).reverse().toString('hex')))
          assert.ok(payment.ins.some(input => Buffer.from(input.hash).reverse().toString('hex') === reveal.getId()))
          const coinbase = Transaction.fromHex(await api(`/btc/rawtx/${coinbase_txid}`))
          assert.equal(coinbase.ins.length, 1)
          assert.equal(Buffer.from(coinbase.ins[0].hash).toString('hex'), '00'.repeat(32))
          assert.equal(coinbase.ins[0].index, 0xffffffff)
          const [rewardStatus] = await api('/v3/bitcoin/tx/status/batch', { txids: [coinbase_txid] })
          assert.equal(rewardStatus.confirmed, true)
          assert.ok(statuses.every(tx => tx.block_hash === rewardStatus.block_hash))
          const reward = coinbase.outs.filter(out => out.script.toString('hex') === sender.pk_script).reduce((sum, out) => sum + BigInt(out.value), 0n)
          const paidSats = paid.reduce((sum, out) => sum + BigInt(out.value), 0n)
          assert.equal(await balance(sender, '*'), BigInt(sender_before_sats) - paidSats - fees + reward)
          assert.equal(await balance(recipient, '*'), BigInt(receiver_before_sats) + paidSats)
          assert.equal(await balance(sender, asset, precision), initial - amount)
          assert.equal(await balance(recipient, asset, precision), amount)
          const received = await api(`/v3/address/asset/${recipient.address}/${asset}?invalid=true`)
          assert.equal(received.reduce((sum, out) => sum + out.Assets.filter(item => keyOf(item.Name) === asset).reduce((value, item) => value + atoms(item.Amount, precision), 0n), 0n), amount)
          assert.deepEqual(await api(`/v3/address/asset/${recipient.address}/${asset}`), [])
          await page.reload(); await ready(page)
          await page.locator('form input[type="password"]').fill(sender.password)
          await page.locator('form button[type="submit"]').click()
          await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: poll.timeout })
          const receiverPage = await device(); contexts.push(receiverPage.context())
          await importActor(receiverPage, recipient)
          for (const [actorPage, actor, expected] of [[page, sender, initial - amount], [receiverPage, recipient, amount]]) {
            const summary = await walletCall(actorPage, 'getAssetSummary', actor.address)
            assert.equal(summary.assets.filter(item => keyOf(item.Name) === asset).reduce((sum, item) => sum + atoms(item.Amount, precision), 0n), expected)
            const active = actorPage.locator('[role="tabpanel"][data-state="active"]')
            await actorPage.getByRole('tab', { name: 'Bitcoin', exact: true }).click()
            await active.getByRole('button', { name: label, exact: true }).click()
            const row = active.locator('div.bg-muted.border').filter({ has: actorPage.getByText(asset.split(':')[2].toUpperCase(), { exact: true }) })
            await expect(row).toBeVisible()
            await expect(row.locator('div.text-sm.font-semibold').first()).toHaveText(expected.toString())
          }
          console.log(JSON.stringify({ observation: 'original confirmed BRC20 payment readback', payment_txid, fee_sats: fees.toString(), coinbase_reward_sats: reward.toString() }))
          assert.deepEqual((await control('snapshot')).l1.broadcast_count, {}, 'readback must not broadcast another payment')
          return
        }
        assert.equal(await balance(sender, asset, precision), initial)
        assert.equal(await balance(recipient, asset, precision), recipientInitial)
        const senderSats = await balance(sender, '*'), recipientSats = await balance(recipient, '*')
        const initialTransfers = await api(`/v3/address/asset/${sender.address}/${asset}`)
        const recipientCarriersBefore = label === 'BRC20'
          ? await api(`/v3/address/asset/${recipient.address}/${asset}?invalid=true`) : []
        if (id === 'runes') {
          assert.ok(initialTransfers.length > 0, 'the actual Rune balance requires indexed carriers')
          assert.equal(initialTransfers.reduce((sum, out) => {
            assert.equal(out.Assets.length, 1, 'only pure Rune carriers may fund this send')
            assert.equal(keyOf(out.Assets[0].Name), asset)
            return sum + atoms(out.Assets[0].Amount, precision)
          }, 0n), initial, 'all actual pure carriers must account for the sender balance')
        } else assert.equal(initialTransfers.length, id === 'brc_balance' ? 0 : 1)
        const panel = page.locator('[role="tabpanel"][data-state="active"]')
        await page.getByRole('tab', { name: 'Bitcoin', exact: true }).click()
        await panel.getByRole('button', { name: label, exact: true }).click()
        const row = panel.locator('div.bg-muted.border').filter({ has: page.getByText(asset.split(':')[2].toUpperCase(), { exact: true }) })
        const assetWaitStarted = Date.now()
        try {
          await expect(row).toHaveCount(1, { timeout: poll.timeout })
        } finally {
          if (fixture.realBitcoinControl) {
            const durationMs = Date.now() - assetWaitStarted
            let diagnosticTimer
            const observed = await Promise.race([
              Promise.all([
                page.evaluate(() => (window.__observedAssetReads || []).slice(-12)).catch(() => []),
                panel.locator('div.bg-muted.border').allTextContents().catch(() => []),
              ]).then(([reads, rendered]) => ({ reads,
                rendered: rendered.slice(0, 12).map(text => text.slice(0, 180)) })),
              new Promise(resolve => { diagnosticTimer = setTimeout(() => resolve({ diagnostics: 'unavailable after 2s' }), 2000) }),
            ]).finally(() => clearTimeout(diagnosticTimer))
            console.log(JSON.stringify({ event: 'asset-row-readiness', asset, durationMs, ...observed }))
          }
        }
        await row.getByRole('button', { name: 'Send', exact: true }).click()
        const dialog = page.getByRole('dialog')
        await dialog.getByPlaceholder('Enter address', { exact: true }).fill(recipient.address)
        if (id === 'runes') {
          await dialog.getByPlaceholder('Enter amount', { exact: true }).fill('12.345')
          await expect(dialog.getByRole('alert')).toBeVisible()
          await expect(dialog.getByRole('button', { name: 'Confirm', exact: true })).toBeDisabled()
        }
        await dialog.getByPlaceholder('Enter amount', { exact: true }).fill(spec.amount)
        const beforeCancel = (await control('snapshot')).l1
        const logsBeforeCancel = await logs(page)
        const locksBeforeCancel = await walletCall(page, 'getAllLockedUtxo', sender.address)
        await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
        await expect(dialog.getByText(recipient.address, { exact: true })).toBeVisible()
        await expect(dialog).toContainText(`${spec.amount}`)
        await expect(dialog).toContainText('sats/vB')
        await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
        assert.deepEqual((await control('snapshot')).l1, beforeCancel, 'review cancellation changed L1 state')
        assert.deepEqual(await logs(page), logsBeforeCancel, 'review cancellation created an operation')
        assert.deepEqual(await walletCall(page, 'getAllLockedUtxo', sender.address), locksBeforeCancel, 'review cancellation reserved inputs')
        const before = (await control('snapshot')).l1, oldLogs = new Set((await logs(page)).map(log => log.id))
        // Cancel returns to the editable form; confirm the same reviewed terms.
        await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
        await expect(dialog.getByText(recipient.address, { exact: true })).toBeVisible()
        const rateText = await dialog.locator('dd').filter({ hasText: /^\d+ sats\/vB$/ }).textContent()
        const rate = BigInt(rateText.trim().split(' ')[0])
        await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
        let receipt
        await expect.poll(async () => {
          const added = (await logs(page)).filter(log => !oldLogs.has(log.id) && log.action === 'send_asset_l1')
          assert.ok(added.length <= 1)
          receipt = added[0]
          if (receipt?.status === 'failed') throw new Error(JSON.stringify(receipt))
          return receipt?.status === 'succeeded'
        }, poll).toBe(true)
        assert.match(receipt.txid, /^[0-9a-f]{64}$/)
        const submitted = (await control('snapshot')).l1
        const ids = Object.keys(submitted.broadcast_count).filter(txid => !Object.hasOwn(before.broadcast_count, txid))
        assert.equal(ids.length, id === 'brc_balance' ? 3 : 1, 'one confirmation created an unexpected transaction count')
        assert.ok(ids.includes(receipt.txid))
        const transactions = await Promise.all(ids.map(async txid => Transaction.fromHex(await api(`/btc/rawtx/${txid}`))))
        for (const tx of transactions) {
          assert.equal(submitted.broadcast_count[tx.getId()], 1)
          assert.ok(tx.ins.every(input => input.witness.length > 0))
        }
        const payment = transactions.find(tx => tx.getId() === receipt.txid)
        const paid = payment.outs.filter(out => out.script.toString('hex') === recipient.pk_script)
        assert.equal(paid.length, 1)
        if (id === 'brc_transfer') {
          const inputs = payment.ins.map(input => `${Buffer.from(input.hash).reverse().toString('hex')}:${input.index}`)
          assert.ok(inputs.includes(spec.seed_outpoint), 'send did not consume the original transfer inscription')
        } else if (id === 'brc_balance') {
          const reveal = transactions.find(tx => tx.ins.some(input => input.witness.some(witness => {
            const chunks = btcScript.decompile(witness)
            return chunks?.some(chunk => Buffer.isBuffer(chunk) && chunk.toString().includes('"transfer"'))
          })))
          assert.ok(reveal, 'no actual transfer inscription reveal')
          const script = reveal.ins[0].witness[reveal.ins[0].witness.length - 2]
          const payloads = btcScript.decompile(script).filter(Buffer.isBuffer)
          const payload = payloads.map(chunk => { try { return JSON.parse(chunk.toString()) } catch { return null } })
            .find(value => value?.p === 'brc-20')
          assert.deepEqual(payload, { p: 'brc-20', op: 'transfer', tick: asset.split(':')[2].toLowerCase(), amt: spec.amount })
          const commitID = Buffer.from(reveal.ins[0].hash).reverse().toString('hex')
          assert.ok(ids.includes(commitID), 'reveal did not spend this confirmation\'s commit')
          assert.ok(payment.ins.some(input => Buffer.from(input.hash).reverse().toString('hex') === reveal.getId()))
        } else {
          const ticker = await api(`/v3/tick/info/${asset}`)
          const recipientIndex = payment.outs.indexOf(paid[0])
          assert.equal(edicts(payment).filter(edict => edict.id === ticker.displayname && edict.output === recipientIndex)
            .reduce((sum, edict) => sum + edict.amount, 0n), amount)
          assert.ok(payment.outs.some(out => out.script.toString('hex') === sender.pk_script), 'Rune send omitted change')
        }
        let fees = 0n
        for (const tx of transactions) {
          let inputSats = 0n
          for (const input of tx.ins) {
            const previous = Transaction.fromHex(await api(`/btc/rawtx/${Buffer.from(input.hash).reverse().toString('hex')}`))
            inputSats += BigInt(previous.outs[input.index].value)
          }
          const fee = inputSats - tx.outs.reduce((sum, out) => sum + BigInt(out.value), 0n)
          assert.ok(fee >= BigInt(tx.virtualSize()) * rate, 'transaction underpays the reviewed network fee rate')
          fees += fee
        }
        assert.equal(await balance(recipient, asset, precision), recipientInitial, 'pending send was credited as confirmed')
        await control('confirm-l1')
        assert.equal(await balance(sender, asset, precision), initial - amount)
        assert.equal(await balance(recipient, asset, precision), recipientInitial + amount)
        const paidSats = paid.reduce((sum, out) => sum + BigInt(out.value), 0n)
        assert.equal(await balance(sender, '*'), senderSats - paidSats - fees)
        assert.equal(await balance(recipient, '*'), recipientSats + paidSats)
        const received = await api(`/v3/address/asset/${recipient.address}/${asset}${label === 'BRC20' ? '?invalid=true' : ''}`)
        if (label === 'BRC20') {
          // invalid=true includes old, consumed BRC20 transfer inscriptions.
          // Holdings are the ledger balances asserted above; carrier evidence
          // must identify this exact payment while preserving the old outputs.
          const points = paid.map(out => `${payment.getId()}:${payment.outs.indexOf(out)}`)
          const added = received.filter(out => points.includes(out.Outpoint))
          assert.deepEqual(added.map(out => out.Outpoint).sort(), points.sort())
          assert.equal(added.reduce((sum, out) => sum + out.Assets.filter(item => keyOf(item.Name) === asset)
            .reduce((total, item) => total + atoms(item.Amount, precision), 0n), 0n), amount)
          const byPoint = (left, right) => left.Outpoint.localeCompare(right.Outpoint)
          assert.deepEqual(received.filter(out => !points.includes(out.Outpoint)).sort(byPoint), recipientCarriersBefore.sort(byPoint),
            'sending must preserve pre-existing recipient carrier evidence')
        } else {
          assert.equal(received.reduce((sum, out) => sum + out.Assets.filter(item => keyOf(item.Name) === asset)
            .reduce((total, item) => total + atoms(item.Amount, precision), 0n), 0n), recipientInitial + amount)
        }
        if (label === 'BRC20') assert.deepEqual(await api(`/v3/address/asset/${recipient.address}/${asset}`), [])
        await page.reload(); await ready(page)
        await page.locator('form input[type="password"]').fill(sender.password)
        await page.locator('form button[type="submit"]').click()
        await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible()
        const receiverPage = await device(); contexts.push(receiverPage.context())
        await importActor(receiverPage, recipient)
        for (const [actorPage, actor, expected] of [[page, sender, initial - amount], [receiverPage, recipient, recipientInitial + amount]]) {
          // BRC20 holdings live in the address ledger; getAssetAmount reads
          // transferable carriers only. Use the wallet's actual summary API.
          const result = await walletCall(actorPage, 'getAssetSummary', actor.address)
          assert.equal(result.assets.filter(item => keyOf(item.Name) === asset)
            .reduce((sum, item) => sum + atoms(item.Amount, precision), 0n), expected)
          const active = actorPage.locator('[role="tabpanel"][data-state="active"]')
          await actorPage.getByRole('tab', { name: 'Bitcoin', exact: true }).click()
          if (expected > 0n) {
            await active.getByRole('button', { name: label, exact: true }).click()
            const visible = active.locator('div.bg-muted.border').filter({ has: actorPage.getByText(asset.split(':')[2].toUpperCase(), { exact: true }) })
            await expect(visible).toBeVisible()
            const digits = expected.toString().padStart(precision + 1, '0')
            const display = precision ? `${digits.slice(0, -precision)}.${digits.slice(-precision)}`.replace(/\.?0+$/, '') : digits
            await expect(visible.locator('div.text-sm.font-semibold').first()).toHaveText(display)
          }
        }
        console.log(JSON.stringify({ runeOrBrcPayment: receipt.txid, asset, amount: spec.amount, sender: sender.address, recipient: recipient.address }))
      })
    } catch (error) {
      failures.push(error)
      if (t.selectedCases) throw error
    }
    finally { for (const context of contexts.reverse()) await context.close().catch(() => {}) }
  }
  if (failures.length) throw new AggregateError(failures, failures.map(error => error.message).join('\n'))
}
