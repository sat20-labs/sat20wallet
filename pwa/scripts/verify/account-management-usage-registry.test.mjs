import test from 'node:test'
import assert from 'node:assert/strict'
import {
  assertFixedOwnerMnemonicIdentity,
  fixedRegtestAccountUsageCaseNames,
  regtestAccountUsageCaseNames,
  requireFixedAccountManagementOwner,
  runPwaUsageCases,
} from './account-management-usage-e2e.mjs'

const fixedRootProbe = {
  owner: {
    role: 'ordinary-1',
    mnemonic: 'safe-registration-probe-only',
    password: 'safe-registration-probe-only',
    address: 'probe-public-address',
    fingerprint: 'probe-public-fingerprint',
  },
}

const baseRunner = check => ({
  check,
  device: async () => { throw new Error('registration probe must not open a browser') },
  ready: async () => { throw new Error('registration probe must not initialize the PWA') },
  unlock: async () => { throw new Error('registration probe must not unlock a wallet') },
  catalog: async () => { throw new Error('registration probe must not read a wallet') },
  walletCall: async () => { throw new Error('registration probe must not call the wallet SDK') },
  accountCall: async () => { throw new Error('registration probe must not call account-management business logic') },
  password: 'registration-probe-only',
  newPassword: 'registration-probe-only',
  questions: [],
  answers: [],
  rootMnemonic: 'registration-probe-only',
  nodeAPIOrigins: [],
})

test('regtest fixed-root allowlist contains only the two fixed ordinary-1 business cases', () => {
  assert.deepEqual(fixedRegtestAccountUsageCaseNames, [
    'usage: fixed regtest ordinary-1 adds selects renames subaccount and preserves root identity',
    'usage: fixed regtest ordinary-1 renews paid storage through current AUTOPAY',
  ])
  assert.equal(fixedRegtestAccountUsageCaseNames.some(name => /\b2of[23]\b/.test(name)), false)
})

test('registers fixed-root and Guardian regtest UI cases without running handlers', async () => {
  const registered = []
  await runPwaUsageCases({
    ...baseRunner(async (name, handler) => {
      assert.equal(typeof handler, 'function')
      registered.push(name)
    }),
    fixedAccountManagement: fixedRootProbe,
  })

  for (const name of fixedRegtestAccountUsageCaseNames) {
    assert.ok(registered.includes(name), `missing registered UI case: ${name}`)
  }
  const guardianCase = 'usage: real 2of3 settings and independent Guardian pages complete setup and rehearsal'
  assert.deepEqual(regtestAccountUsageCaseNames, [...fixedRegtestAccountUsageCaseNames, guardianCase])
  assert.ok(registered.includes(guardianCase), 'missing independent Guardian setup/rehearsal UI case')
})

test('does not register fixed-root cases without the explicit fixture', async () => {
  const registered = []
  await runPwaUsageCases({
    ...baseRunner(async (name, handler) => { assert.equal(typeof handler, 'function'); registered.push(name) }),
  })
  for (const name of fixedRegtestAccountUsageCaseNames) assert.equal(registered.includes(name), false)
})

test('rejects missing or wrong fixed-owner parameters before registering a case', async () => {
  let registrations = 0
  const check = async () => { registrations++ }
  await assert.rejects(runPwaUsageCases({
    ...baseRunner(check),
    fixedAccountManagement: { owner: { role: 'ordinary-1', password: 'probe-only' } },
  }), /requires mnemonic and password/)
  await assert.rejects(runPwaUsageCases({
    ...baseRunner(check),
    fixedAccountManagement: { owner: { role: 'ordinary-2', mnemonic: 'probe-only', password: 'probe-only' } },
  }), /role must be ordinary-1/)
  assert.equal(registrations, 0, 'invalid fixture registered browser cases before validation')
})

test('checks optional public fixture identity against the mnemonic-derived identity', () => {
  const owner = requireFixedAccountManagementOwner(fixedRootProbe)
  const derived = { address: 'probe-public-address', fingerprint: 'probe-public-fingerprint' }
  assert.doesNotThrow(() => assertFixedOwnerMnemonicIdentity(owner, derived))
  assert.throws(() => assertFixedOwnerMnemonicIdentity(owner, { ...derived, address: 'different-public-address' }),
    /does not match fixture address/)
  assert.throws(() => assertFixedOwnerMnemonicIdentity(owner, { ...derived, fingerprint: 'different-public-fingerprint' }),
    /does not match fixture fingerprint/)
})
