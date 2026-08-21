import assert from 'node:assert/strict';
import test from 'node:test';

import { formatInvokePollFailure, pollInvokeEvidence } from './invoke-poll.mjs';

const noWait = async () => {};

test('continues after transient transport and not-indexed errors', async () => {
  let attempt = 0;
  const result = await pollInvokeEvidence({
    attempts: 3,
    sleep: noWait,
    delayForAttempt: () => 0,
    getRawTx: async () => {
      attempt += 1;
      if (attempt === 1) throw new TypeError('fetch failed');
      if (attempt === 2) throw new Error('transaction not indexed yet');
      return { code: 0, data: 'raw' };
    },
    getHistory: async () => ({ data: attempt === 3 ? [{ InUtxo: 'tx:0' }] : [] }),
    getStatus: async () => ({ invokeCount: attempt }),
    findHistoryItem: (history) => history.data[0],
    isComplete: (state) => Boolean(state.rawTx && state.historyItem),
  });

  assert.equal(result.completed, true);
  assert.equal(result.attempts, 3);
  assert.equal(result.lastErrors.rawTx, 'transaction not indexed yet');
});

test('bounded failure preserves the last probe error', async () => {
  const result = await pollInvokeEvidence({
    attempts: 2,
    sleep: noWait,
    delayForAttempt: () => 0,
    getRawTx: async () => { throw new TypeError('fetch failed'); },
    getHistory: async () => ({ data: [] }),
    getStatus: async () => ({ invokeCount: 1 }),
    findHistoryItem: () => undefined,
    isComplete: () => false,
  });

  assert.equal(result.completed, false);
  assert.equal(result.attempts, 2);
  assert.match(formatInvokePollFailure('txid', result), /rawTx=fetch failed/);
});
