import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import ts from 'typescript'
import { transformSync } from 'esbuild'
import { runInNewContext } from 'node:vm'
import { tryit } from 'radash'
import { webcrypto } from 'node:crypto'

const component = new URL('../../components/wallet/RGB11SendDialog.vue', import.meta.url)

const loadSend = async (mocks) => {
  mocks = { walletRequestSessionGuard: () => () => {}, ...mocks }
  const source = await readFile(component, 'utf8')
  const script = source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)?.[1]
  assert.ok(script, 'script setup block missing')
  const file = ts.createSourceFile('dialog.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  let initializer = ''
  file.forEachChild((node) => {
    if (!ts.isVariableStatement(node)) return
    for (const declaration of node.declarationList.declarations) {
      if (declaration.name.getText(file) === 'sendByAddress') initializer = declaration.initializer?.getText(file) || ''
    }
  })
  assert.ok(initializer, 'sendByAddress missing')
  const js = ts.transpileModule(`globalThis.send = ${initializer}`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText
  return Function(...Object.keys(mocks), `${js}; return globalThis.send`)(...Object.values(mocks))
}

test('Direct retry keeps transfer ID', async () => {
  let prepares = 0
  const delivered = []
  const transferId = { value: '' }
  const send = await loadSend({
    loading: { value: false }, message: { value: '' }, success: { value: false },
    temporaryDelivery: { value: false }, transferId,
    assetContractID: { value: 'rgb:contract' }, assetName: { value: 'rgb11:f:test' },
    amountRaw: { value: '1' }, receiverAddress: { value: 'tb1qreceiver' }, btcFeeRate: { value: 2 },
    beginNamedPwaOperation: async () => null,
    updateOperationLog: async () => {}, finishPwaOperation: async () => {},
    completeBroadcast: async () => {}, emit: () => {}, t: (key) => key,
    rgb11Address: {
      prepareTransfer: async () => {
        prepares++
        return [null, { transfer: JSON.stringify({ state: { transfer_id: 'transfer-1' } }) }]
      },
      deliverAndBroadcast: async ({ transfer_id }) => {
        delivered.push(transfer_id)
        return [null, { awaiting_ack: true }]
      },
    },
  })

  await send()
  await send()
  assert.equal(prepares, 1)
  assert.deepEqual(delivered, ['transfer-1', 'transfer-1'])
  assert.equal(transferId.value, 'transfer-1')
})

async function sessionFixture(logs = {}, native = {}) {
  const load = async (relative, deps = {}, globals = {}) => {
    const module = { exports: {} }
    const source = await readFile(new URL(relative, import.meta.url), 'utf8')
    runInNewContext(transformSync(source, { loader: 'ts', format: 'cjs', target: 'es2022' }).code, {
      module, exports: module.exports, Error, crypto: webcrypto,
      require: name => { assert.ok(name in deps, `missing ${name}`); return deps[name] }, ...globals,
    })
    return module.exports
  }
  const identity = await load('../../lib/identity-boundary.ts')
  const session = await load('../../lib/walletSession.ts', { './identity-boundary': identity })
  session.setWalletSessionUnlocked(true)
  const lock = () => { identity.beginWalletIdentityTransition(); session.setWalletSessionUnlocked(false) }
  const facade = (await load('../../utils/rgb11Address.ts', {
    radash: { tryit }, '@/lib/walletSession': session,
    '@/utils/pwaVersionPolicy': { beginVersionDispatch: () => () => {} },
    '@/utils/pwaOperationLog': { beginPwaWalletOperation: async () => null, finishPwaOperation: async () => {}, ...logs },
  }, { sat20wallet_wasm: native })).default
  return { session, lock, facade }
}

for (const pause of ['begin_log', 'prepared', 'update_log']) {
  test(`Direct send stops after session invalidation at ${pause}`, async () => {
    const { session, lock } = await sessionFixture()
    let release, entered
    const blocked = new Promise(resolve => { release = resolve })
    const atPause = new Promise(resolve => { entered = resolve })
    const wait = async () => { entered(); await blocked }
    let prepares = 0, deliveries = 0
    const loading = { value: false }, message = { value: '' }
    const send = await loadSend({
      walletRequestSessionGuard: session.walletRequestSessionGuard,
      loading, message, success: { value: false }, temporaryDelivery: { value: false }, transferId: { value: '' },
      assetContractID: { value: 'rgb:contract' }, assetName: { value: 'rgb11:f:test' },
      amountRaw: { value: '1' }, receiverAddress: { value: 'tb1qreceiver' }, btcFeeRate: { value: 2 },
      beginNamedPwaOperation: async () => { if (pause === 'begin_log') await wait(); return { id: 'op' } },
      updateOperationLog: async () => { if (pause === 'update_log') await wait() },
      finishPwaOperation: async () => {}, completeBroadcast: async () => {}, emit() {}, t: key => key,
      rgb11Address: {
        prepareTransfer: async () => {
          prepares++
          if (pause === 'prepared') await wait()
          return [null, { transfer: JSON.stringify({ state: { transfer_id: 'transfer-1' } }) }]
        },
        deliverAndBroadcast: async () => { deliveries++; return [null, { awaiting_ack: true }] },
      },
    })
    const pending = send()
    await atPause
    lock()
    // Unlocking again must not authorize an earlier action's continuation.
    session.setWalletSessionUnlocked(true)
    release()
    await pending
    assert.equal(prepares, pause === 'begin_log' ? 0 : 1)
    assert.equal(deliveries, 0, 'stale send must not dispatch a new delivery')
    assert.match(message.value, /session changed/i)
    assert.equal(loading.value, false)
  })
}

for (const pause of ['locked', 'begin_log', 'native', 'finish_log']) {
  test(`RGB address facade enforces the session boundary at ${pause}`, async () => {
    let fixture, calls = 0
    fixture = await sessionFixture({
      beginPwaWalletOperation: async () => { if (pause === 'begin_log') fixture.lock(); return { id: 'op' } },
      finishPwaOperation: async (_op, failure) => { if (!failure && pause === 'finish_log') fixture.lock() },
    }, { prepareRGB11AddressTransfer: async () => {
      calls++
      if (pause === 'native') fixture.lock()
      return { code: 0, data: { transfer: 'prepared' } }
    } })
    if (pause === 'locked') fixture.lock()
    const [error, result] = await fixture.facade.prepareTransfer({})
    assert.equal(calls, ['locked', 'begin_log'].includes(pause) ? 0 : 1)
    assert.match(error?.message || '', /session|unlocked/i)
    assert.equal(result, undefined, 'invalidated requests must not expose a successful result')
  })
}

test('RGB background mailbox sync also refuses a locked frontend session', async () => {
  let calls = 0
  const fixture = await sessionFixture({}, { syncRGB11AddressMailbox: async () => { calls++; return { code: 0 } } })
  fixture.lock()
  const [error] = await fixture.facade.syncMailboxInBackground({})
  assert.match(error?.message || '', /session|unlocked/i)
  assert.equal(calls, 0)
})

// Automatic continuation belongs to the durable-task scheduler. The complete
// single-user-action assertion is in rgb11-direct-chain.test.mjs, including ACK
// mailbox sync, Pinia/query state, timer dispatch and the original witness.
test('Direct awaiting ACK preserves the original transfer and notifies the task view', async () => {
  let prepares = 0
  let deliveries = 0
  const completed = []
  const events = []
  const transferId = { value: '' }
  const loading = { value: false }
  const message = { value: '' }
  const send = await loadSend({
    loading, message, success: { value: false },
    temporaryDelivery: { value: false }, transferId,
    assetContractID: { value: 'rgb:contract' }, assetName: { value: 'rgb11:f:test' },
    amountRaw: { value: '1' }, receiverAddress: { value: 'tb1qreceiver' }, btcFeeRate: { value: 2 },
    beginNamedPwaOperation: async () => null,
    updateOperationLog: async () => {}, finishPwaOperation: async () => {},
    completeBroadcast: async (...args) => { completed.push(args) },
    emit: (event) => events.push(event), t: (key) => key,
    rgb11Address: {
      prepareTransfer: async () => {
        prepares++
        return [null, { transfer: JSON.stringify({ state: { transfer_id: 'transfer-1' } }) }]
      },
      deliverAndBroadcast: async ({ transfer_id }) => {
        assert.equal(transfer_id, 'transfer-1')
        deliveries++
        return [null, { awaiting_ack: true }]
      },
    },
  })
  await send()
  assert.equal(prepares, 1)
  assert.equal(deliveries, 1, 'the dialog leaves ACK continuation to the task scheduler')
  assert.equal(transferId.value, 'transfer-1')
  assert.equal(message.value, 'rgb11Transfer.addressAckPending')
  assert.equal(loading.value, false)
  assert.deepEqual(events, ['completed'], 'the durable task view must be refreshed')
  assert.deepEqual(completed, [], 'waiting for ACK is not a broadcast result')
})

test('A completed Direct send starts a fresh transfer for the same asset', async () => {
  let prepares = 0
  const delivered = []
  const completed = []
  const transferId = { value: '' }
  const receiverAddress = { value: 'tb1qfirst' }
  const amountRaw = { value: '1' }
  const send = await loadSend({
    loading: { value: false }, message: { value: '' }, success: { value: false },
    temporaryDelivery: { value: false }, transferId,
    assetContractID: { value: 'rgb:contract' }, assetName: { value: 'rgb11:f:test' },
    amountRaw, receiverAddress, btcFeeRate: { value: 2 },
    beginNamedPwaOperation: async () => null,
    updateOperationLog: async () => {}, finishPwaOperation: async () => {},
    completeBroadcast: async (txid, _messageKey, _operation, id) => { completed.push({ txid, id }) },
    emit: () => {}, t: (key) => key,
    rgb11Address: {
      prepareTransfer: async () => {
        prepares++
        return [null, { transfer: JSON.stringify({ state: { transfer_id: `transfer-${prepares}` } }) }]
      },
      deliverAndBroadcast: async ({ transfer_id }) => {
        delivered.push(transfer_id)
        return [null, { broadcast: true, txid: `tx-${delivered.length}` }]
      },
    },
  })

  await send()
  assert.equal(transferId.value, '', 'broadcasted transfer must no longer be retried')
  assert.equal(receiverAddress.value, '', 'the recipient field must be ready for a new send')
  assert.equal(amountRaw.value, '', 'the amount field must be ready for a new send')

  receiverAddress.value = 'tb1qsecond'
  amountRaw.value = '2'
  await send()
  assert.equal(prepares, 2)
  assert.deepEqual(delivered, ['transfer-1', 'transfer-2'])
  assert.deepEqual(completed, [{ txid: 'tx-1', id: 'transfer-1' }, { txid: 'tx-2', id: 'transfer-2' }])
})
