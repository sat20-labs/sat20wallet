import assert from 'node:assert/strict'

export function assertBroadcastCounts(counts) {
  assert.ok(counts !== null && typeof counts === 'object' && !Array.isArray(counts),
    'snapshot broadcast_count must be a per-txid map')
  for (const count of Object.values(counts)) {
    assert.ok(Number.isSafeInteger(count) && count >= 0, 'each txid broadcast count must be a nonnegative safe integer')
  }
  return { ...counts }
}

const transactionSet = snapshot => new Set([
  ...(snapshot.l1.pending_txids ?? []), ...(snapshot.l1.confirmed_txids ?? []),
])
export function assertSnapshotTransactionDelta(before, after, allowedAdded = [], broadcastDelta = 0, message = 'unexpected L1 transaction change', realBitcoinControl = false) {
  const beforeTxIDs = transactionSet(before)
  const afterTxIDs = transactionSet(after)
  assert.deepEqual([...beforeTxIDs].filter(txid => !afterTxIDs.has(txid)).sort(), [],
    `${message}: existing pending or confirmed transactions disappeared`)
  assert.deepEqual([...afterTxIDs].filter(txid => !beforeTxIDs.has(txid)).sort(), [...allowedAdded].sort(),
    `${message}: new pending or confirmed transactions differ`)
  // bitcoind snapshots have no submission counter. Preserve their existing
  // exact transaction-set checks; a fake-L1 snapshot must always supply a map.
  if (realBitcoinControl && before.l1.broadcast_count === undefined) {
    assert.equal(after.l1.broadcast_count, undefined, 'real Bitcoin snapshot counter schema changed')
    return
  }
  const countsBefore = assertBroadcastCounts(before.l1.broadcast_count)
  const countsAfter = assertBroadcastCounts(after.l1.broadcast_count)
  if (broadcastDelta === null) return // The signed initial package has its own transaction evidence.
  const expected = { ...countsBefore }
  if (broadcastDelta !== 0) {
    assert.equal(broadcastDelta, 1, 'only one specified sweep broadcast is supported')
    assert.equal(allowedAdded.length, 1, 'a sweep requires exactly one specified transaction')
    const txid = allowedAdded[0]
    assert.equal(countsBefore[txid] ?? 0, 0, 'the specified sweep must not have been broadcast before')
    expected[txid] = 1
  }
  assert.deepEqual(countsAfter, expected, `${message}: per-txid broadcast counters differ`)
}
