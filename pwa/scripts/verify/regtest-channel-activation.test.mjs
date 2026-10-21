import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const source = readFileSync(new URL('./pos-pwa-e2e.mjs', import.meta.url), 'utf8')
const body = source.match(/const confirmFunding = ([\s\S]*?)\n  const assertSignedAnchor/)
assert.ok(body, 'exercise the real confirmation helper, including its default arguments')
const funding = { txid: 'a'.repeat(64), confirmed: [] }
const confirm = async (fixture, fields = {}, explicitV2) => {
  const anchor = { funding_outpoint: `${funding.txid}:0`, txid: 'b'.repeat(64), height: 171,
    version: 1, lock_time: 0, sequence: 0xfffffffe, ...fields }
  const snapshot = { height: 171, l1: { pending_txids: [], confirmed_txids: [funding.txid] },
    anchors: [anchor], mempool_txids: [] }
  const build = new Function('fixture', 'control', 'assertNodes', 'txidPattern', 'assert', 'console', `return ${body[1]}`)
  const run = build(fixture, async () => snapshot, () => {}, /^[0-9a-f]{64}$/, assert, { log() {} })
  return run(funding, 2, explicitV2)
}
const active = { activation_height: 32, pos_v2_active: true }

test('ordinary opening and BTC splicing respect the attached chain already past activation', async () => {
  const result = await confirm(active)
  assert.equal(result.anchor.height, 171)
})
test('activated-chain confirmations retain all v2 transaction checks', async () => {
  for (const fields of [{ height: 31 }, { version: 2 }, { lock_time: 1 }, { sequence: 0xffffffff }]) {
    await assert.rejects(confirm(active, fields))
  }
})
test('explicit v2 before activation still fails', async () => {
  await assert.rejects(confirm({ activation_height: 32 }, { height: 31 }, true), /before activation/)
})
test('legacy and unscheduled fixtures keep their original boundaries', async () => {
  await confirm({ activation_height: 32 }, { height: 20 })
  await assert.rejects(confirm({ activation_height: 32 }), /legacy case/)
  await confirm({ activation_height: 0, pos_activation_scheduled: false, config: { Chain: 'regtest' } })
})
