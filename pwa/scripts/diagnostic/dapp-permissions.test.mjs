import assert from 'node:assert/strict'
import fs from 'node:fs'
import test from 'node:test'
import { BoundedApprovalQueue } from '../../lib/approval-queue.ts'
import {
  grantMatchesScope,
  parseDappGrantEnvelope,
  removeMatchingDappGrants,
} from '../../lib/dapp-grant-model.ts'
import { getAllowedDappOrigins } from '../../lib/dapp-origin-policy.ts'
import { DappRequestGuard } from '../../lib/dapp-request-guard.ts'

const grant = {
  origin: 'https://app.example',
  createdAt: 100,
  expiresAt: 200,
  network: 'testnet',
  walletFingerprint: 'a'.repeat(64),
  accountIndex: 1,
  identityGeneration: 4,
  capabilities: ['accounts:read'],
  sessionOnly: false,
}

test('DappGrant schema and identity scope fail closed', () => {
  assert.equal(parseDappGrantEnvelope('{broken'), null)
  assert.equal(parseDappGrantEnvelope(JSON.stringify({ version: 1, grants: [grant] })), null)
  assert.equal(parseDappGrantEnvelope(JSON.stringify({ version: 2, grants: [grant] })), null)
  assert.ok(parseDappGrantEnvelope(JSON.stringify({ version: 3, grants: [grant] })))
  const scope = { network: 'testnet', walletFingerprint: 'a'.repeat(64), accountIndex: 1, identityGeneration: 4 }
  assert.equal(grantMatchesScope(grant, grant.origin, scope, 'accounts:read', 150), true)
  assert.equal(grantMatchesScope(grant, grant.origin, { ...scope, network: 'mainnet' }, 'accounts:read', 150), false)
  assert.equal(grantMatchesScope(grant, grant.origin, { ...scope, walletFingerprint: 'b'.repeat(64) }, 'accounts:read', 150), false)
  assert.equal(grantMatchesScope(grant, grant.origin, { ...scope, accountIndex: 2 }, 'accounts:read', 150), false)
  assert.equal(grantMatchesScope(grant, grant.origin, { ...scope, identityGeneration: 5 }, 'accounts:read', 150), false)
  assert.equal(grantMatchesScope(grant, grant.origin, scope, 'public-key:read', 150), false)
  assert.equal(grantMatchesScope(grant, grant.origin, scope, 'accounts:read', 200), false)
  assert.deepEqual(removeMatchingDappGrants([grant], grant.origin, scope), [])
  assert.deepEqual(removeMatchingDappGrants([grant], grant.origin, { ...scope, accountIndex: 2 }), [grant])
})

test('production origin policy never implicitly trusts localhost', () => {
  const production = getAllowedDappOrigins({
    development: false,
    test: false,
    configured: 'http://localhost:3006,https://custom.example',
    currentProtocol: 'http:',
    currentHostname: 'localhost',
  })
  assert.equal(production.has('http://localhost:3006'), false)
  assert.equal(production.has('https://custom.example'), true)
  const development = getAllowedDappOrigins({
    development: true,
    test: false,
    configured: 'http://localhost:3999',
    currentProtocol: 'http:',
    currentHostname: 'localhost',
  })
  assert.equal(development.has('http://localhost:3999'), true)
})

test('approval queue keeps concurrent request results bound to visible id', async () => {
  let now = 1_000
  const queue = new BoundedApprovalQueue({ now: () => now, maxSize: 2, maxPerOrigin: 2 })
  const first = queue.enqueue('first', 'https://a.example', { value: 1 })
  const second = queue.enqueue('second', 'https://a.example', { value: 2 })
  assert.equal(queue.current?.id, 'first')
  assert.throws(() => queue.confirm('second', 'wrong'), /does not match/)
  queue.confirm('first', 'first-result')
  assert.equal(await first, 'first-result')
  assert.equal(queue.current?.id, 'second')
  queue.confirm('second', 'second-result')
  assert.equal(await second, 'second-result')

  const sameA = queue.enqueue('https://a.example\0shared', 'https://a.example', {})
  const sameB = queue.enqueue('https://b.example\0shared', 'https://b.example', {})
  queue.confirm('https://a.example\0shared', 'a')
  queue.confirm('https://b.example\0shared', 'b')
  assert.equal(await sameA, 'a')
  assert.equal(await sameB, 'b')

  const one = queue.enqueue('one', 'https://a.example', {})
  const two = queue.enqueue('two', 'https://a.example', {})
  await assert.rejects(queue.enqueue('three', 'https://a.example', {}), /queue is full/)
  queue.reject('one')
  queue.reject('two')
  await assert.rejects(one, /User rejected/)
  await assert.rejects(two, /User rejected/)

  const expiring = queue.enqueue('expiring', 'https://b.example', {}, now + 10)
  now += 11
  queue.expire()
  await assert.rejects(expiring, /expired/)

  const rateQueue = new BoundedApprovalQueue({ now: () => now, maxRequestsPerWindow: 1 })
  const rateFirst = rateQueue.enqueue('rate-one', 'https://rate.example', {})
  rateQueue.reject('rate-one')
  await assert.rejects(rateFirst, /User rejected/)
  await assert.rejects(rateQueue.enqueue('rate-two', 'https://rate.example', {}), /rate limit/)
})

test('request guard enforces duplicate, per-origin limit, and expiry', () => {
  let now = 1_000
  const guard = new DappRequestGuard({ now: () => now, maxActive: 2, maxActivePerOrigin: 1 })
  const finish = guard.begin('https://a.example', 'one', 'nonce', now + 100)
  assert.throws(() => guard.begin('https://a.example', 'two', 'nonce', now + 100), /queue is full/)
  finish()
  assert.throws(() => guard.begin('https://a.example', 'one', 'nonce', now + 100), /Duplicate/)
  assert.throws(() => guard.begin('https://a.example', 'expired', 'nonce', now), /expiry/)
})

test('Market and approval routing contain no implicit account disclosure', () => {
  const market = fs.readFileSync(new URL('../../entrypoints/popup/pages/wallet/DappMarket.vue', import.meta.url), 'utf8')
  const policy = fs.readFileSync(new URL('../../lib/dapp-policy.ts', import.meta.url), 'utf8')
  const grants = fs.readFileSync(new URL('../../lib/authorized-origins.ts', import.meta.url), 'utf8')
  const webViewBridge = fs.readFileSync(new URL('../../composables/useWebViewBridge.ts', import.meta.url), 'utf8')
  assert.doesNotMatch(market, /addAuthorizedOrigin|authorizeEmbeddedOrigin/)
  assert.doesNotMatch(market, /announceReady\([^)]*,\s*\{[\s\S]*accounts:/)
  assert.doesNotMatch(market, /return ['"]\*['"]/)
  assert.match(policy, /BIND_REFERRER_FOR_SERVER\]: policy\('identity:write', 'fail-closed'\)/)
	assert.match(policy, /SIGN_DATA\]: policy\('transaction:sign', 'fail-closed'\)/)
  assert.match(grants, /export const revokeDappGrant/)
  assert.doesNotMatch(grants, /local:authorized_origins/)
  assert.doesNotMatch(webViewBridge, /ACTIONS_REQUIRING_ORIGIN_AUTH/)
  assert.match(webViewBridge, /getDappActionPolicy/)
  assert.match(webViewBridge, /isDappCapabilityGranted/)
})

test('account approval card imports every rendered Lucide icon', () => {
  const accountCard = fs.readFileSync(new URL('../../components/wallet/AccountCard.vue', import.meta.url), 'utf8')
  assert.match(accountCard, /import\s*\{[^}]*\bChevronRight\b[^}]*\bUser2\b[^}]*\}\s*from\s*['"]lucide-vue-next['"]/)
})
