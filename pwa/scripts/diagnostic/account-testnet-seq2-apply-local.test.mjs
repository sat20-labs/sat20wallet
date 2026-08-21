import assert from 'node:assert/strict'
import test from 'node:test'

import { classifyLocalPatchHash } from './account-testnet-seq2-apply-local-state.mjs'

test('local account repair patch has explicit idempotent hash states', () => {
  assert.equal(classifyLocalPatchHash('aa', 'aa', 'bb'), 'apply')
  assert.equal(classifyLocalPatchHash('BB', 'aa', 'bb'), 'already_applied')
  assert.equal(classifyLocalPatchHash('cc', 'aa', 'bb'), 'third_hash')
  assert.equal(classifyLocalPatchHash('', 'aa', 'bb'), 'invalid')
})
