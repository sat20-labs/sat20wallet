import assert from 'node:assert/strict'
import test from 'node:test'
import { BoundedApprovalQueue } from '../../lib/approval-queue.ts'
import { toAssetAmountString, toDecimalString } from '../../lib/strict-integers.ts'

test('divisible asset amounts retain exact decimal precision', () => {
  for (const amount of ['0.5', '0.000000000000000001', '9007199254740993.12345678', '12.00']) {
    assert.equal(toAssetAmountString(amount, 'amount'), amount)
  }
  for (const amount of ['-1', '1e3', '1.', 'NaN', 0.1, Number.MAX_SAFE_INTEGER + 1]) {
    assert.throws(() => toAssetAmountString(amount, 'amount'))
  }
  assert.equal(toDecimalString('12', 'feeRate'), '12')
  assert.throws(() => toDecimalString('0.5', 'count'))
})

test('approved network switch survives cancellation of pending identity requests', async () => {
  const queue = new BoundedApprovalQueue()
  const approved = queue.enqueue('switch', 'https://app.example', { network: 'testnet' })
  const pending = queue.enqueue('sign', 'https://app.example', {})
  const rejected = assert.rejects(pending, /identity/)
  let finish
  queue.execute('switch', async () => {
    queue.rejectAll(new Error('identity changed'))
    return new Promise(resolve => { finish = resolve })
  })
  await Promise.resolve()
  assert.equal(queue.current, null)
  assert.throws(() => queue.execute('switch', async () => 'duplicate'), /does not match/)
  finish('testnet')
  assert.equal(await approved, 'testnet')
  await rejected
})

test('failed or expired network switches cannot report success', async () => {
  let now = 100
  const queue = new BoundedApprovalQueue({ now: () => now })
  const failed = queue.enqueue('failed', 'https://app.example', {})
  queue.execute('failed', async () => { throw new Error('switch failed') })
  await assert.rejects(failed, /switch failed/)
  const expired = queue.enqueue('expired', 'https://app.example', {}, now + 1)
  now += 2
  let executed = false
  assert.throws(() => queue.execute('expired', async () => { executed = true }), /does not match/)
  await assert.rejects(expired, /expired/)
  assert.equal(executed, false)
})
