import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { transformSync } from 'esbuild'

test('paid account confirmation logs the SDK transaction_id without recovery material', async () => {
  const source = readFileSync(new URL('../../utils/pwaOperationLog.ts', import.meta.url), 'utf8')
  const { code } = transformSync(source, { loader: 'ts', format: 'cjs', target: 'es2022' })
  const updates = []
  const module = { exports: {} }
  runInNewContext(code, {
    module,
    exports: module.exports,
    require: (name) => {
      if (name === '@/utils/operationLog') return { beginOperationLog: async () => [null, 'op-1'], updateOperationLog: async (...args) => updates.push(args) }
      if (name === '@/lib/walletStorage') return { walletStorage: { getValue: () => 'testnet' } }
      throw new Error(`Unexpected import: ${name}`)
    },
    console: { warn: () => {} },
  })

  const txid = 'a'.repeat(64)
  await module.exports.finishPwaOperation(
    { id: 'op-1', title: 'Account storage', successMessage: 'done' },
    null,
    { transaction_id: txid, storage_authorization_id: 'secret-id', recovery_share: 'secret-share' },
  )
  assert.equal(updates.length, 1)
  assert.equal(updates[0][1].txid, txid)
  assert.equal(updates[0][1].result.transaction_id, txid)
  assert.equal(JSON.stringify(updates[0][1]).includes('secret-id'), false)
  assert.equal(JSON.stringify(updates[0][1]).includes('secret-share'), false)
})
