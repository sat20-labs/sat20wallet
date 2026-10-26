import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync, existsSync } from 'node:fs'

// Exercise the actual browser consumer functions without starting a browser.
const source = name => readFileSync(new URL(name, import.meta.url), 'utf8')
const helperURL = new URL('./l1-snapshot-assertions.mjs', import.meta.url)
const helper = existsSync(helperURL) ? await import(helperURL) : {}
const basic = source('./wallet-basic-e2e.mjs')
const snapshotBody = basic.slice(basic.indexOf('  const snapshot = async () => {'), basic.indexOf('  const realSummary ='))
const pos = source('./pos-pwa-e2e.mjs')
const deltaBody = pos.slice(pos.indexOf('  const l1TransactionSet ='), pos.indexOf('\n\n  try {', pos.indexOf('  const l1TransactionSet =')))
const consumer = counts => new Function('assert', 'fetch', 'fixture', 'assertBroadcastCounts',
  `${snapshotBody}; return snapshot()`)(assert, async () => ({ ok: true, json: async () => ({ l1: { broadcast_count: counts } }) }),
  { control_url: 'http://127.0.0.1:1' }, helper.assertBroadcastCounts)
const delta = new Function('assert', 'assertSnapshotTransactionDelta', 'fixture',
  `${deltaBody}; return assertL1TransactionDelta`)(assert, helper.assertSnapshotTransactionDelta, {})
const snapshot = (counts, pending = ['old'], confirmed = []) => ({ l1: { broadcast_count: counts, pending_txids: pending, confirmed_txids: confirmed } })
test('component consumer accepts per-txid integer counters', async () => {
  assert.deepEqual(await consumer({ old: 2 }), { old: 2 })
})
test('component consumer rejects invalid counters', async () => {
  for (const value of [4, null, [], { old: -1 }, { old: 0.5 }, { old: '1' }]) await assert.rejects(() => consumer(value))
})
test('same map and pending-to-confirmed movement cause no broadcast', () => {
  delta(snapshot({ old: 2 }), snapshot({ old: 2 }, [], ['old']), [], 0)
})
test('repeated old txid is rejected', () => {
  assert.throws(() => delta(snapshot({ old: 2 }), snapshot({ old: 3 }), [], 0))
})
test('wrong txid replacement with equal total is rejected', () => {
  assert.throws(() => delta(snapshot({ old: 2 }), snapshot({ wrong: 2 }), [], 0))
})
test('one specified sweep broadcast is accepted', () => {
  delta(snapshot({ old: 2 }), snapshot({ old: 2, sweep: 1 }, ['old', 'sweep']), ['sweep'], 1)
})
test('duplicate sweep or unexpected transaction is rejected', () => {
  assert.throws(() => delta(snapshot({ old: 2 }), snapshot({ old: 2, sweep: 2 }, ['old', 'sweep']), ['sweep'], 1))
  assert.throws(() => delta(snapshot({ old: 2 }), snapshot({ old: 2, wrong: 1 }, ['old', 'wrong']), ['sweep'], 1))
})
test('removed existing transaction is rejected even with unchanged counters', () => {
  assert.throws(() => delta(snapshot({ old: 2 }), snapshot({ old: 2 }, []), [], 0))
})

test('missing fake-L1 counters fail; explicit real Bitcoin snapshots retain exact transaction checks', () => {
  assert.throws(() => delta(snapshot(undefined), snapshot(undefined), [], 0))
  helper.assertSnapshotTransactionDelta(snapshot(undefined), snapshot(undefined, [], ['old']), [], 0, 'real', true)
  assert.throws(() => helper.assertSnapshotTransactionDelta(snapshot(undefined), snapshot(undefined, ['wrong']), [], 0, 'real', true))
})
