import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import ts from 'typescript'

const component = new URL('../../components/wallet/RGB11SendDialog.vue', import.meta.url)

const loadSend = async (mocks) => {
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
