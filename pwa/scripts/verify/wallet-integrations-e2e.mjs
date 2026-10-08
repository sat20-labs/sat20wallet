import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { createServer } from 'node:http'
import { expect } from '@playwright/test'
import * as secp256k1 from '@bitcoin-js/tiny-secp256k1-asmjs'
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
  const actor = fixture.basicWallet
  assert.ok(actor?.mnemonic && actor.password && actor.address)
  const page = await device()
  const context = page.context()
  let frame
  let publicKey
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
    await check(dappCases[0], async () => {
      await page.evaluate(() => { location.hash = '#/import' })
      await page.getByLabel('Recovery Phrase', { exact: true }).fill(actor.mnemonic)
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
