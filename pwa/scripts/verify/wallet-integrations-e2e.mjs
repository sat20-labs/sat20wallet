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
export async function createPwaIntegrationDapp(network = 'testnet', port = 0) {
  assert.ok(['testnet', 'regtest'].includes(network))
  const html = `<!doctype html><meta charset="utf-8"><title>PWA acceptance DApp</title>
    <main>
      <h1>PWA acceptance DApp</h1>
      <p id="dapp-status" role="status">Requests require approval in the wallet.</p>
      <fieldset>
        <legend>Wallet permissions</legend>
        <button id="connect-read" type="button">Connect wallet (read access)</button>
        <button id="connect-signing" type="button">Request signing permission</button>
        <button id="connect-broadcast" type="button">Request broadcast permission</button>
      </fieldset>
      <fieldset>
        <legend>Message signing</legend>
        <label for="message-to-sign">Public message</label>
        <textarea id="message-to-sign" rows="3">PWA acceptance: confirm this exact message.</textarea>
        <button id="sign-message" type="button">Sign message</button>
      </fieldset>
      <fieldset>
        <legend>Bitcoin PSBT</legend>
        <label for="unsigned-psbt">Unsigned PSBT hex</label>
        <textarea id="unsigned-psbt" rows="5" autocomplete="off" spellcheck="false"></textarea>
        <button id="sign-psbt" type="button">Sign PSBT</button>
        <label for="signed-psbt">Signed PSBT hex to broadcast</label>
        <textarea id="signed-psbt" rows="5" autocomplete="off" spellcheck="false"></textarea>
        <button id="broadcast-psbt" type="button">Broadcast signed PSBT</button>
      </fieldset>
      <fieldset>
        <legend>Legacy STP Transcend deployment</legend>
        <p>Requests wallet approval to deploy plain-sat transcend.tc. No existing channel is specified. Confirm the actual fee in the wallet.</p>
        <button id="connect-contract" type="button">Request contract permission</button>
        <button id="deploy-legacy-transcend" type="button">Deploy legacy Transcend contract (wallet approval)</button>
      </fieldset>
      <fieldset>
        <legend>Disconnect</legend>
        <p>DApp clients cannot revoke their own grant. Use the wallet’s Disconnect DApp control to revoke access.</p>
        <button id="disconnect-help" type="button">Disconnect / revoke in wallet</button>
      </fieldset>
      <pre id="dapp-response" aria-live="polite"></pre>
    </main>
    <script>
      const protocol = 'sat20-dapp-connect';
      const walletOrigin = new URL(document.referrer).origin;
      let sequence = 0;
      const responses = Object.create(null), events = [], pending = new Map();
      const status = document.getElementById('dapp-status');
      const responseOutput = document.getElementById('dapp-response');
      const readCapabilities = ['accounts:read', 'public-key:read', 'network:read'];
      const send = (action, params) => {
        const request = window.pwaAcceptanceClient.begin(action, params);
        pending.set(request.requestId, action);
        status.textContent = 'Waiting for wallet approval: ' + action;
        return request;
      };
      window.pwaAcceptanceClient = {
        responses, events,
        begin(action, params) {
          const now = Date.now();
          const request = { type: 'SAT20_DAPP_REQUEST', protocol,
            requestId: 'pwa-client-' + (++sequence), action, params,
            origin: location.origin, network: ${JSON.stringify(network)},
            nonce: crypto.randomUUID().replaceAll('-', ''),
            timestamp: String(now), expiresAt: now + 120000 };
          parent.postMessage(request, walletOrigin);
          return request;
        },
      };
      addEventListener('message', event => {
        if (event.source !== parent || event.origin !== walletOrigin || event.data?.protocol !== protocol) return;
        if (event.data.type === 'SAT20_DAPP_RESPONSE') {
          const response = event.data;
          responses[response.requestId] = response;
          const action = pending.get(response.requestId);
          pending.delete(response.requestId);
          if (action === 'signPsbt' && response.success && typeof response.result === 'string') {
            document.getElementById('signed-psbt').value = response.result;
          }
          status.textContent = response.success ? 'Wallet approved: ' + (action || 'request') : 'Wallet rejected: ' + (action || 'request');
          responseOutput.textContent = JSON.stringify({ action, requestId: response.requestId,
            success: response.success, result: response.result, error: response.error }, null, 2);
        }
        if (event.data.type === 'SAT20_DAPP_EVENT') {
          events.push(event.data);
          if (event.data.event === 'disconnect') status.textContent = 'Wallet revoked this DApp connection.';
        }
      });
      document.getElementById('connect-read').addEventListener('click', () =>
        send('requestAccounts', { capabilities: readCapabilities, sessionOnly: true }));
      document.getElementById('connect-signing').addEventListener('click', () =>
        send('requestAccounts', { capabilities: [...readCapabilities, 'transaction:sign'], sessionOnly: true }));
      document.getElementById('connect-broadcast').addEventListener('click', () =>
        send('requestAccounts', { capabilities: [...readCapabilities, 'transaction:sign', 'transaction:broadcast'], sessionOnly: true }));
      document.getElementById('sign-message').addEventListener('click', () => {
        const message = document.getElementById('message-to-sign').value.trim();
        if (!message) { status.textContent = 'Enter a public message to sign.'; return; }
        send('signMessage', { message });
      });
      document.getElementById('sign-psbt').addEventListener('click', () => {
        const psbtHex = document.getElementById('unsigned-psbt').value.trim();
        if (!psbtHex) { status.textContent = 'Enter an unsigned PSBT hex first.'; return; }
        send('signPsbt', { psbtHex, options: { chain: 'bitcoin' } });
      });
      document.getElementById('broadcast-psbt').addEventListener('click', () => {
        const psbtHex = document.getElementById('signed-psbt').value.trim();
        if (!psbtHex) { status.textContent = 'Sign or enter a PSBT before requesting broadcast.'; return; }
        send('pushPsbt', { psbtHex, options: { chain: 'bitcoin' } });
      });
      document.getElementById('connect-contract').addEventListener('click', () =>
        send('requestAccounts', { capabilities: [...readCapabilities, 'contract:write'], sessionOnly: true }));
      document.getElementById('deploy-legacy-transcend').addEventListener('click', () =>
        send('deployContract_Remote', ['transcend.tc', '{"contractType":"transcend.tc","assetName":{"Protocol":"","Type":"","Ticker":""},"startBlock":0,"endBlock":0}', 1, false]));
      document.getElementById('disconnect-help').addEventListener('click', () => {
        status.textContent = 'Use the wallet’s Disconnect DApp button to revoke this origin; this client has no revoke action.';
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
      'Content-Security-Policy': "default-src 'none'; script-src 'unsafe-inline'; frame-ancestors http://127.0.0.1:* http://localhost:* http://pwa.localhost:5179 http://cold.localhost:5179 http://recovery.localhost:5179" })
    response.end(html)
  })
  await new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(port, '127.0.0.1', resolve)
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
  const bitcoinNetwork = fixture.config.Chain === 'regtest' ? networks.regtest : networks.testnet
  const acceptedBroadcasts = Object.create(null)
  const broadcastObservations = []
  if (fixture.realBitcoinControl) context.on('response', reply => {
    const request = reply.request()
    if (request.method() !== 'POST' || ![`${base}/btc/tx`, `${base}/btc/txs`, `${base}/v3/bitcoin/tx/broadcast`].includes(request.url())) return
    broadcastObservations.push((async () => {
      const result = await reply.json()
      if (result.code !== 0 || result.data?.accepted === false || result.data?.error) return
      const body = request.postDataJSON()
      for (const raw of body.signedTxsHex ?? body.SignedTxsHex ?? [body.signedTxHex ?? body.SignedTxHex ?? body.raw_tx]) {
        const txid = Transaction.fromHex(raw).getId()
        acceptedBroadcasts[txid] = (acceptedBroadcasts[txid] ?? 0) + 1
      }
    })().catch(error => ({ error })))
  })
  const api = async (path, body) => {
    const reply = await fetch(base + path, { signal: AbortSignal.timeout(30000), ...(body === undefined ? {} : {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
    }) })
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
    if (fixture.realBitcoinControl) {
      const observations = await Promise.all(broadcastObservations)
      for (const observation of observations) if (observation?.error) throw observation.error
      result.l1.broadcast_count = { ...acceptedBroadcasts }
    }
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
      if (fixture.config.Chain === 'regtest') {
        await expect.poll(() => page.evaluate(() => window.__observedRootRecovery ?? null), { timeout: 120000 }).toBeTruthy()
        const recovery = await page.evaluate(() => window.__observedRootRecovery)
        if (recovery.error) {
          let message = recovery.error
          for (const secret of [actor.mnemonic, actor.password]) message = message.replaceAll(secret, '[redacted]')
          throw new Error(`real regtest root recovery rejected: ${message}`)
        }
      }
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
      for (const [action, expected] of [['getAccounts', [actor.address]], ['getPublicKey', publicKey], ['getNetwork', fixture.config.Chain]]) {
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
      const payload = ['SAT20 Wallet Message', 'version:1', `network:${fixture.config.Chain}`, `origin:${integrationDapp.origin}`,
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
      let outputs
      if (fixture.realBitcoinControl) {
        const [group] = await api('/v3/bitcoin/utxos/by-scripts', { scripts: [actor.pk_script] })
        assert.ok(group && !group.error)
        const locked = await walletCall(page, 'getAllLockedUtxo', actor.address)
        const rgb = JSON.parse((await walletCall(page, 'getRGB11State')).state)
        const carrierPoints = new Set(rgb.proofs.map(proof => proof.outpoint))
        const plain = []
        for (const coin of group.utxos) {
          if (coin.confirmations < 1 || coin.value < 10000 || Object.hasOwn(locked, coin.outpoint) || carrierPoints.has(coin.outpoint)) continue
          const assets = await api(`/v3/utxo/info/${coin.outpoint}`)
          if (!assets || (assets.Assets || []).length) continue
          plain.push({ Outpoint: coin.outpoint, Value: coin.value, Assets: [] })
        }
        outputs = plain
      } else outputs = await api(`/v3/address/utxos/${actor.address}`)
      input = outputs.find(output => !(output.Assets || []).length && output.Value >= 10000)
      assert.ok(input, 'DApp fixture has no independent Bitcoin funding')
      assert.match(input.Outpoint, /^[0-9a-f]{64}:\d+$/, 'Indexer UTXO must identify its funding output')
      const [txid, index] = input.Outpoint.split(':')
      const previous = Transaction.fromHex(await api(`/btc/rawtx/${txid}`)).outs[Number(index)]
      assert.equal(previous.value, input.Value)
      const internal = Buffer.from(publicKey, 'hex').subarray(1)
      assert.equal(payments.p2tr({ internalPubkey: internal, network: bitcoinNetwork }).output.toString('hex'), actor.pk_script)
      assert.equal(previous.script.toString('hex'), actor.pk_script)
      const psbt = new Psbt({ network: bitcoinNetwork })
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
      const parsed = Psbt.fromHex(signedPsbt, { network: bitcoinNetwork })
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
      if (fixture.dappResumeTxID) {
        // Continue the original accepted payment after the test-only HTTP observer failed.
        // Earlier authorization, malformed-signature rejection and signing assertions remain recorded PASS.
        const txid = fixture.dappResumeTxID
        const raw = await api(`/btc/rawtx/${txid}`)
        const transaction = Transaction.fromHex(raw)
        assert.equal(transaction.getId(), txid)
        assert.equal(transaction.ins.length, 1)
        assert.equal(transaction.outs.length, 2)
        assert.equal(transaction.outs[0].script.toString('hex'), recipient.pk_script)
        assert.equal(transaction.outs[0].value, 2000)
        assert.equal(transaction.outs[1].script.toString('hex'), actor.pk_script)
        const parentID = Buffer.from(transaction.ins[0].hash).reverse().toString('hex')
        const parent = Transaction.fromHex(await api(`/btc/rawtx/${parentID}`))
        const spent = parent.outs[transaction.ins[0].index]
        assert.equal(spent.script.toString('hex'), actor.pk_script)
        assert.equal(transaction.outs[1].value, spent.value - 2500)
        let [status] = await api('/v3/bitcoin/tx/status/batch', { txids: [txid] })
        assert.ok(status.exists)
        if (!status.confirmed) {
          assert.equal(status.in_mempool, true)
          await control('confirm-l1')
          ;[status] = await api('/v3/bitcoin/tx/status/batch', { txids: [txid] })
        }
        assert.equal(status.confirmed, true)
        assert.ok(status.confirmations >= 1)
        const [outspend] = await api('/v3/bitcoin/outspends/batch', { outpoints: [`${parentID}:${transaction.ins[0].index}`] })
        assert.equal(outspend.spent, true)
        const [paid] = await api('/v3/bitcoin/utxos/status', { outpoints: [`${txid}:0`] })
        assert.equal(paid.exists, true); assert.equal(paid.unspent, true)
        assert.equal(paid.value, 2000); assert.equal(paid.pk_script, recipient.pk_script)
        assert.ok(paid.confirmations >= 1)
        const senderBalance = await sats(actor.address), receiverBalance = await sats(recipient.address)
        const beforeReplay = (await control('snapshot')).l1
        await approveConnection([...permissions, 'transaction:sign', 'transaction:broadcast'])
        const approval = page.getByRole('dialog').filter({ hasText: 'Allow this DApp to broadcast this transaction?' })
        const replay = await begin('pushTx', { rawtx: raw, options: { chain: 'bitcoin' } })
        await expect(approval).toContainText(integrationDapp.origin)
        await approval.getByRole('button', { name: 'Confirm', exact: true }).click()
        const replayResult = await response(replay)
        assert.equal(replayResult.success, true); assert.equal(replayResult.result, txid)
        assert.equal(await sats(actor.address), senderBalance)
        assert.equal(await sats(recipient.address), receiverBalance)
        const afterReplay = (await control('snapshot')).l1
        assert.deepEqual(afterReplay.pending_txids, beforeReplay.pending_txids)
        assert.deepEqual(afterReplay.confirmed_txids, beforeReplay.confirmed_txids)
        assert.deepEqual(afterReplay.broadcast_count, { [txid]: 1 }, 'the single replay response must identify only the original transaction')
        console.log(JSON.stringify({ observation: 'original DApp payment resumed', txid, confirmations: status.confirmations, paid: paid.value, fee: 500, replay: replayResult.result }))
        return
      }
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
