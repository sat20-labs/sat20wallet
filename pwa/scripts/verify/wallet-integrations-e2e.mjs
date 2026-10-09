import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { createServer } from 'node:http'
import { expect } from '@playwright/test'
import * as secp256k1 from '@bitcoin-js/tiny-secp256k1-asmjs'
import { Psbt, Transaction, payments, networks, initEccLib } from 'bitcoinjs-lib'
initEccLib(secp256k1)
import { requiredPwaRgbCases, runPwaRgbCases } from './wallet-rgb-e2e.mjs'
import { requiredPwaBiometricCases, runPwaBiometricCases } from './wallet-biometric-e2e.mjs'
export { requiredPwaRgbCases, runPwaRgbCases, requiredPwaBiometricCases, runPwaBiometricCases }

const dappCases = [
  'DApp PWA: isolated client cannot read accounts before approval or after cancellation',
  'DApp PWA: explicit account permissions expose the actual wallet identity',
  'DApp PWA: signing requires its separately approved capability',
  'DApp PWA: cancelling message approval returns no signature',
  'DApp PWA: approved domain message verifies against the displayed wallet key',
  'DApp PWA: disconnect revokes the grant and notifies the client',
  'DApp PWA: authorized Bitcoin PSBT signing preserves exact outputs without broadcasting',
  'DApp PWA: separately authorized broadcast confirms the same signed transaction exactly once',
]
export const requiredPwaDappCases = dappCases
export const requiredPwaIntegrationCases = [...dappCases, ...requiredPwaRgbCases, ...requiredPwaBiometricCases]

// This is an ordinary DApp client, served on an ephemeral loopback port. It
// emits production postMessage requests and records only the wallet's replies.
// It neither implements a wallet API nor answers an SDK, STP, or Indexer call.
export async function createPwaIntegrationDapp() {
  const html = `<!doctype html><meta charset="utf-8"><title>PWA acceptance DApp</title>
    <h1>PWA acceptance DApp</h1><p>Requests require approval in the wallet.</p>
    <script>
      const protocol = 'sat20-dapp-connect';
      const walletOrigin = new URL(document.referrer).origin;
      let sequence = 0;
      const responses = Object.create(null), events = [];
      window.pwaAcceptanceClient = {
        responses, events,
        begin(action, params) {
          const now = Date.now();
          const request = { type: 'SAT20_DAPP_REQUEST', protocol,
            requestId: 'pwa-client-' + (++sequence), action, params,
            origin: location.origin, network: 'testnet',
            nonce: crypto.randomUUID().replaceAll('-', ''),
            timestamp: String(now), expiresAt: now + 120000 };
          parent.postMessage(request, walletOrigin);
          return request;
        },
      };
      addEventListener('message', event => {
        if (event.source !== parent || event.origin !== walletOrigin || event.data?.protocol !== protocol) return;
        if (event.data.type === 'SAT20_DAPP_RESPONSE') responses[event.data.requestId] = event.data;
        if (event.data.type === 'SAT20_DAPP_EVENT') events.push(event.data);
      });
      addEventListener('load', () => parent.postMessage({
        type: 'SAT20_DAPP_CLIENT_READY', protocol, origin: location.origin, href: location.href,
      }, walletOrigin));
    </script>`
  const server = createServer((request, response) => {
    if (request.method !== 'GET' || new URL(request.url, 'http://localhost').pathname !== '/swap/') {
      response.writeHead(404).end('Unknown test client resource')
      return
    }
    response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store',
      'Content-Security-Policy': "default-src 'none'; script-src 'unsafe-inline'; frame-ancestors http://127.0.0.1:* http://localhost:*" })
    response.end(html)
  })
  await new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolve)
  })
  const origin = `http://127.0.0.1:${server.address().port}`
  return { origin, url: `${origin}/swap/`, close: () => new Promise((resolve, reject) => {
    server.close(error => error ? reject(error) : resolve())
    server.closeIdleConnections()
  }) }
}

const poll = { timeout: 120000, intervals: [100, 250, 500] }
const permissions = ['accounts:read', 'public-key:read', 'network:read']
const hash = bytes => createHash('sha256').update(bytes).digest()
const varString = value => {
  const bytes = Buffer.from(value, 'utf8')
  assert.ok(bytes.length <= 0xffff)
  const length = bytes.length < 0xfd ? Buffer.from([bytes.length]) : Buffer.alloc(3)
  if (bytes.length >= 0xfd) { length[0] = 0xfd; length.writeUInt16LE(bytes.length, 1) }
  return Buffer.concat([length, bytes])
}

export async function runPwaDappCases(t, fixture) {
  const { check, device, walletCall, integrationDapp } = t
  assert.ok(integrationDapp?.origin && integrationDapp.url, 'runner must create the loopback DApp before starting Vite')
  const actor = fixture.dappWallet
  assert.ok(actor?.mnemonic && actor.password && actor.address)
  const page = await device()
  const context = page.context()
  let frame
  let publicKey
  let signedPsbt, signedTransaction, unsignedPsbt, input, sourceBefore, recipientBefore
  const recipient = fixture.dappRecipient
  const endpoint = fixture.config.IndexerL1
  const base = `${endpoint.Scheme}://${endpoint.Host}/${endpoint.Proxy}`
  const api = async path => {
    const reply = await fetch(base + path, { signal: AbortSignal.timeout(30000) })
    assert.ok(reply.ok)
    const result = await reply.json()
    assert.equal(result.code, 0, result.msg)
    return result.data
  }
  const control = async action => {
    const reply = await fetch(new URL(`/${action}`, fixture.control_url), { method: 'POST',
      headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ wait_anchors: false }),
      signal: AbortSignal.timeout(120000) })
    assert.ok(reply.ok)
    const result = await reply.json()
    assert.ok(!result.error, result.error)
    return result
  }
  const sats = async address => BigInt((await api(`/v3/address/summary/${address}`))
    .find(asset => asset.Name.Protocol === '' && asset.Name.Type === '*').Amount)
  const begin = (action, params) => frame.evaluate(({ action, params }) =>
    window.pwaAcceptanceClient.begin(action, params), { action, params })
  const response = async request => {
    await expect.poll(() => frame.evaluate(id => window.pwaAcceptanceClient.responses[id] ?? null, request.requestId), poll).toBeTruthy()
    const result = await frame.evaluate(id => window.pwaAcceptanceClient.responses[id], request.requestId)
    assert.equal(result.requestId, request.requestId)
    assert.equal(result.protocol, 'sat20-dapp-connect')
    return result
  }
  const direct = async (action, params) => response(await begin(action, params))
  const denied = result => {
    assert.equal(result.success, false)
    assert.equal(result.result, undefined)
    assert.ok(result.error?.message)
  }
  const dialog = title => page.getByRole('dialog').filter({ has: page.getByRole('heading', { name: title, exact: true }) })
  const approveConnection = async capabilities => {
    const request = await begin('requestAccounts', { capabilities, sessionOnly: true })
    const approval = dialog('Connect Wallet')
    await expect(approval).toBeVisible()
    await expect(approval).toContainText(integrationDapp.origin)
    for (const capability of capabilities) await expect(approval.getByText(capability, { exact: true })).toBeVisible()
    await approval.getByRole('button', { name: 'Confirm', exact: true }).click()
    const result = await response(request)
    assert.equal(result.success, true)
    assert.deepEqual(result.result, [actor.address])
    await expect(approval).toBeHidden()
  }
  try {
    // Import and establish the ordinary cross-origin client even during a
    // focused transaction rerun. All business permissions remain UI actions.
      await page.evaluate(() => { location.hash = '#/import' })
      await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(actor.mnemonic)
      await page.getByLabel('New Wallet Password', { exact: true }).fill(actor.password)
      await page.getByLabel('Confirm Password', { exact: true }).fill(actor.password)
      await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
      await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible()
      assert.equal((await walletCall(page, 'getWalletAddress', 0)).address, actor.address)
      publicKey = (await walletCall(page, 'getWalletPubkey', 0)).pubKey
      await page.evaluate(() => { location.hash = '#/wallet/dapp' })
      const iframe = page.locator('iframe[title="SAT20 Market"]')
      await expect(iframe).toBeVisible()
      assert.equal(new URL(await iframe.getAttribute('src')).origin, integrationDapp.origin)
      frame = await iframe.elementHandle().then(element => element.contentFrame())
      assert.ok(frame)
      await frame.waitForFunction(() => Boolean(window.pwaAcceptanceClient))
      await expect.poll(() => frame.evaluate(() => window.pwaAcceptanceClient.events.some(event => event.event === 'ready')), poll).toBe(true)
      assert.notEqual(new URL(page.url()).origin, new URL(frame.url()).origin, 'DApp must be a cross-origin frame')
    await check(dappCases[0], async () => {
      denied(await direct('getAccounts'))
      const cancelled = await begin('requestAccounts', { capabilities: permissions })
      const approval = dialog('Connect Wallet')
      await expect(approval).toContainText(integrationDapp.origin)
      await approval.getByRole('button', { name: 'Cancel', exact: true }).click()
      denied(await response(cancelled))
      denied(await direct('getAccounts'))
    })

    await check(dappCases[1], async () => {
      await approveConnection(permissions)
      for (const [action, expected] of [['getAccounts', [actor.address]], ['getPublicKey', publicKey], ['getNetwork', 'testnet']]) {
        const result = await direct(action)
        assert.equal(result.success, true)
        assert.deepEqual(result.result, expected)
      }
    })

    await check(dappCases[2], async () => {
      const missingPermission = await direct('signMessage', { message: 'Permission must precede signing.' })
      denied(missingPermission)
      assert.match(missingPermission.error.message, /capability is not granted/i)
      await expect(dialog('Sign Message')).toBeHidden()
      await approveConnection([...permissions, 'transaction:sign'])
    })

    await check(dappCases[3], async () => {
      const request = await begin('signMessage', { message: 'Cancel this test message.' })
      const approval = dialog('Sign Message')
      await expect(approval).toContainText('Cancel this test message.')
      await approval.getByRole('button', { name: 'Cancel', exact: true }).click()
      denied(await response(request))
      await expect(approval).toBeHidden()
    })

    await check(dappCases[4], async () => {
      const message = 'PWA acceptance: confirm this exact message.'
      const request = await begin('signMessage', { message })
      const payload = ['SAT20 Wallet Message', 'version:1', 'network:testnet', `origin:${integrationDapp.origin}`,
        `timestamp:${request.timestamp}`, `nonce:${request.nonce}`, `message-length:${Buffer.byteLength(message)}`, '', message].join('\n')
      const approval = dialog('Sign Message')
      await expect(approval).toContainText('SAT20 Wallet Message / v1')
      await expect(approval).toContainText(integrationDapp.origin)
      await expect(approval).toContainText(hash(payload).toString('hex'))
      await expect(approval).toContainText(request.nonce)
      await approval.getByRole('button', { name: 'Confirm', exact: true }).click()
      const result = await response(request)
      assert.equal(result.success, true)
      const signature = Buffer.from(result.result.signature, 'base64')
      assert.equal(signature.length, 65, 'SDK SignWalletMessage must produce a compact ECDSA signature')
      assert.ok(signature[0] >= 31 && signature[0] <= 34, 'compact signature must specify a compressed key')
      const messageHash = text => hash(hash(Buffer.concat([varString('Bitcoin Signed Message:\n'), varString(text)])))
      assert.equal(secp256k1.verify(messageHash(payload), Buffer.from(publicKey, 'hex'), signature.subarray(1)), true,
        'DApp signature does not authenticate the exact displayed wallet message')
      assert.equal(secp256k1.verify(messageHash(payload + '!'), Buffer.from(publicKey, 'hex'), signature.subarray(1)), false)
    })

    await check(dappCases[5], async () => {
      const eventsBefore = await frame.evaluate(() => window.pwaAcceptanceClient.events.length)
      await page.getByRole('button', { name: 'Disconnect DApp', exact: true }).click()
      await expect.poll(() => frame.evaluate(start => window.pwaAcceptanceClient.events.slice(start)
        .some(event => event.event === 'disconnect' && event.payload?.reason === 'user_disconnected'), eventsBefore), poll).toBe(true)
      denied(await direct('getAccounts'))
      denied(await direct('signMessage', { message: 'A revoked DApp cannot sign.' }))
      assert.equal((await walletCall(page, 'getWalletAddress', 0)).address, actor.address)
    })

    await check(dappCases[6], async () => {
      // Earlier capability/message cases intentionally issue several approvals.
      // Let the production 8-approval/10-second window expire before exercising
      // transaction review; keep the wallet's rate limit unchanged.
      await page.waitForTimeout(10_000)
      await approveConnection(permissions)
      sourceBefore = await sats(actor.address); recipientBefore = await sats(recipient.address)
      const outputs = await api(`/v3/address/utxos/${actor.address}`)
      input = outputs.find(output => !(output.Assets || []).length && output.Value >= 10000)
      assert.ok(input, 'DApp fixture has no independent Bitcoin funding')
      assert.match(input.Outpoint, /^[0-9a-f]{64}:\d+$/, 'Indexer UTXO must identify its funding output')
      const [txid, index] = input.Outpoint.split(':')
      const previous = Transaction.fromHex(await api(`/btc/rawtx/${txid}`)).outs[Number(index)]
      assert.equal(previous.value, input.Value)
      const internal = Buffer.from(publicKey, 'hex').subarray(1)
      assert.equal(payments.p2tr({ internalPubkey: internal, network: networks.testnet }).output.toString('hex'), actor.pk_script)
      assert.equal(previous.script.toString('hex'), actor.pk_script)
      const psbt = new Psbt({ network: networks.testnet })
      psbt.addInput({ hash: txid, index: Number(index), sequence: 0xfffffffd,
        witnessUtxo: { script: previous.script, value: previous.value }, tapInternalKey: internal })
      psbt.addOutput({ address: recipient.address, value: 2000 })
      psbt.addOutput({ address: actor.address, value: previous.value - 2000 - 500 })
      unsignedPsbt = psbt.toHex()
      const params = { psbtHex: unsignedPsbt, options: { chain: 'bitcoin' } }
      const before = (await control('snapshot')).l1
      denied(await direct('signPsbt', params))
      await expect(dialog('Sign PSBT')).toBeHidden()
      await approveConnection([...permissions, 'transaction:sign'])
      const cancelled = await begin('signPsbt', params)
      const approval = dialog('Sign PSBT')
      await expect(approval).toContainText(recipient.address)
      await expect(approval.getByRole('button', { name: 'Confirm', exact: true })).toBeEnabled()
      await approval.getByRole('button', { name: 'Cancel', exact: true }).click()
      denied(await response(cancelled))
      const malformed = await begin('signPsbt', { ...params, psbtHex: '70736274ff00' })
      await expect(approval).toBeVisible()
      await expect(approval.getByText('Error loading transaction details. Please check the console.', { exact: true })).toBeVisible()
      await expect(approval.getByRole('button', { name: 'Confirm', exact: true })).toBeDisabled()
      await approval.getByRole('button', { name: 'Cancel', exact: true }).click()
      denied(await response(malformed))
      const requested = await begin('signPsbt', params)
      await expect(approval).toContainText(recipient.address)
      await expect(approval).toContainText(actor.address)
      await approval.getByRole('button', { name: 'Confirm', exact: true }).click()
      const result = await response(requested)
      assert.equal(result.success, true)
      assert.equal(typeof result.result, 'string')
      signedPsbt = result.result
      const parsed = Psbt.fromHex(signedPsbt, { network: networks.testnet })
      assert.deepEqual(parsed.data.globalMap.unsignedTx.toBuffer(), psbt.data.globalMap.unsignedTx.toBuffer(),
        'wallet changed the DApp transaction while signing')
      // SDK SignPsbt finalizes even when returning PSBT rather than raw tx.
      // Require its actual witness instead of attempting to finalize twice.
      assert.equal(parsed.data.inputs.length, 1)
      assert.ok(parsed.data.inputs[0].finalScriptWitness?.length,
        'wallet returned a PSBT without its finalized Taproot witness')
      signedTransaction = parsed.extractTransaction()
      assert.equal(signedTransaction.outs.length, 2)
      assert.equal(signedTransaction.outs[0].script.toString('hex'), recipient.pk_script)
      assert.equal(signedTransaction.outs[0].value, 2000)
      assert.equal(signedTransaction.outs[1].script.toString('hex'), actor.pk_script)
      assert.equal(signedTransaction.outs[1].value, previous.value - 2500)
      const signature = signedTransaction.ins[0].witness[0]
      assert.ok(signature.length === 64 || signature.length === 65)
      const sighash = signature.length === 65 ? signature[64] : Transaction.SIGHASH_DEFAULT
      assert.ok(secp256k1.verifySchnorr(signedTransaction.hashForWitnessV1(0, [previous.script], [previous.value], sighash),
        previous.script.subarray(2), signature.subarray(0, 64)), 'independent verification rejected the actual Taproot signature')
      const finalized = await begin('signPsbt', { ...params, psbtHex: parsed.toHex() })
      await expect(approval).toContainText(recipient.address)
      await expect(approval).toContainText(actor.address)
      await approval.getByRole('button', { name: 'Cancel', exact: true }).click()
      denied(await response(finalized))
      assert.deepEqual((await control('snapshot')).l1, before, 'PSBT signing or cancellation broadcast/spent funds')
      assert.equal(await sats(actor.address), sourceBefore)
      assert.equal(await sats(recipient.address), recipientBefore)
    })

    await check(dappCases[7], async () => {
      await page.waitForTimeout(10_000)
      assert.ok(signedPsbt && signedTransaction, 'broadcast requires the preceding independently verified signing result')
      const params = { psbtHex: signedPsbt, options: { chain: 'bitcoin' } }
      const before = (await control('snapshot')).l1
      denied(await direct('pushPsbt', params))
      await approveConnection([...permissions, 'transaction:sign', 'transaction:broadcast'])
      const approval = page.getByRole('dialog').filter({ hasText: 'Allow this DApp to broadcast this transaction?' })
      const cancelled = await begin('pushPsbt', params)
      await expect(approval).toContainText(integrationDapp.origin)
      await approval.getByRole('button', { name: 'Cancel', exact: true }).click()
      denied(await response(cancelled))
      assert.deepEqual((await control('snapshot')).l1, before)
      const corrupt = Transaction.fromHex(signedTransaction.toHex())
      corrupt.ins[0].witness[0][0] ^= 1
      const invalid = await begin('pushTx', { rawtx: corrupt.toHex(), options: { chain: 'bitcoin' } })
      await expect(approval).toBeVisible()
      await approval.getByRole('button', { name: 'Confirm', exact: true }).click()
      denied(await response(invalid))
      assert.deepEqual((await control('snapshot')).l1, before, 'invalid signature spent or admitted funds')
      const broadcast = await begin('pushPsbt', params)
      await expect(approval).toContainText(integrationDapp.origin)
      await approval.getByRole('button', { name: 'Confirm', exact: true }).click()
      const result = await response(broadcast)
      assert.equal(result.success, true)
      const txid = signedTransaction.getId()
      assert.equal(result.result, txid)
      assert.equal(await api(`/btc/rawtx/${txid}`), signedTransaction.toHex())
      const sent = (await control('snapshot')).l1
      assert.deepEqual(Object.keys(sent.broadcast_count).filter(id => !Object.hasOwn(before.broadcast_count, id)), [txid])
      assert.equal(sent.broadcast_count[txid], 1)
      assert.equal(await sats(recipient.address), recipientBefore, 'pending payment was credited as confirmed')
      await control('confirm-l1')
      assert.equal(await sats(actor.address), sourceBefore - 2500n)
      assert.equal(await sats(recipient.address), recipientBefore + 2000n)
      const beforeReplay = (await control('snapshot')).l1
      // Submit the same bytes through pushTx with another explicit approval.
      // Idempotent rebroadcast must not turn into a second payment.
      const replay = await begin('pushTx', { rawtx: signedTransaction.toHex(), options: { chain: 'bitcoin' } })
      await expect(approval).toBeVisible()
      await approval.getByRole('button', { name: 'Confirm', exact: true }).click()
      const replayResult = await response(replay)
      assert.equal(replayResult.success, true); assert.equal(replayResult.result, txid)
      assert.equal(await sats(actor.address), sourceBefore - 2500n)
      assert.equal(await sats(recipient.address), recipientBefore + 2000n)
      const afterReplay = (await control('snapshot')).l1
      assert.deepEqual(afterReplay.pending_txids, beforeReplay.pending_txids)
      assert.deepEqual(afterReplay.confirmed_txids, beforeReplay.confirmed_txids)
      assert.deepEqual(Object.keys(afterReplay.broadcast_count).sort(), Object.keys(beforeReplay.broadcast_count).sort())
    })
  } finally { await context.close() }
}

export async function runPwaIntegrationCases(t, fixture) {
  const failures = []
  for (const [run, names] of [[runPwaDappCases, requiredPwaDappCases], [runPwaRgbCases, requiredPwaRgbCases], [runPwaBiometricCases, requiredPwaBiometricCases]]) {
    if (t.selectedCases && !names.some(name => t.selectedCases.includes(name))) continue
    try { await run(t, fixture) } catch (error) { failures.push(error) }
  }
  if (failures.length) throw new AggregateError(failures, failures.map(error => error.message).join('\n'))
}
