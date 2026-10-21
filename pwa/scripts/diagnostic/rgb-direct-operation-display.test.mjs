import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { transformSync } from 'esbuild'

const { code } = transformSync(readFileSync(new URL('../../utils/operationLog.ts', import.meta.url), 'utf8'), {
  loader: 'ts', format: 'cjs', target: 'es2022',
})
const module = { exports: {} }
runInNewContext(code, { module, exports: module.exports })
const project = module.exports.projectSettledRGB11OperationLogs
const ticker = 'a'.repeat(64)
const txid = 'b'.repeat(64)
const record = () => ({ id: 'op', wallet_id: 1, account_index: 0,
  action: 'rgb11_send_address', status: 'running', summary: 'waiting for ACK',
  parameters: { asset: `rgb11:f:${ticker}` },
  history: [{ status: 'running', details: { transfer_id: 'exact-transfer' } }],
})
const transfer = () => ({ transfer_id: 'exact-transfer', direction: 'send', status: 'settled',
  witness_txid: txid, asset: { Name: { Protocol: 'rgb11', Type: 'f', Ticker: ticker } },
})

test('settled exact transfer projects list/detail without mutating stored history', () => {
  const original = record()
  const before = JSON.stringify(original)
  const [displayed] = project([original], [transfer()])
  assert.equal(displayed.status, 'succeeded')
  assert.equal(displayed.txid, txid)
  assert.equal(displayed.result.status_source, 'Current RGB transfer state')
  assert.equal(JSON.stringify(original), before)
  assert.equal(displayed.history, original.history)
})

for (const state of ['awaiting_ack', 'pending', 'rejected', 'cancelled']) {
  test(`${state} cannot claim settlement`, () => {
    const original = record()
    assert.equal(project([original], [{ ...transfer(), status: state }])[0], original)
  })
}
test('missing, wrong contract, wrong direction or duplicate exact IDs preserve the log', () => {
  const original = record()
  for (const transfers of [[], [{ ...transfer(), transfer_id: 'other' }],
    [{ ...transfer(), direction: 'receive' }],
    [{ ...transfer(), asset: { Name: { Protocol: 'rgb11', Type: 'f', Ticker: 'c'.repeat(64) } } }],
    [transfer(), transfer()], [{ ...transfer(), witness_txid: '' }]]) {
    assert.equal(project([original], transfers)[0], original)
  }
})
test('unrelated and already terminal operations keep their original status', () => {
  for (const original of [{ ...record(), action: 'send_sats' }, { ...record(), status: 'failed' },
    { ...record(), history: [] }, { ...record(), parameters: { asset: 'PWA' } }]) {
    assert.equal(project([original], [transfer()])[0], original)
  }
})
