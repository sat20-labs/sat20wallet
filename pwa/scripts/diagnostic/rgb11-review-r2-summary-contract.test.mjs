// Executes the existing Vue callback and sender matcher with isolated SDK
// response fixtures. This does not build WASM, use a browser, or send a tx.
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createHash, webcrypto } from 'node:crypto'
import test from 'node:test'
import ts from 'typescript'

if (!globalThis.crypto) globalThis.crypto = webcrypto
const componentURL = new URL('../../components/wallet/RGB11InvoiceDialog.vue', import.meta.url)
const source = await readFile(componentURL, 'utf8')
const script = source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)?.[1]
assert.ok(script, 'invoice script setup missing')
const file = ts.createSourceFile('invoice.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const initializers = new Map()
let restoreCallback
file.forEachChild((node) => {
  if (ts.isVariableStatement(node)) {
    for (const declaration of node.declarationList.declarations) {
      initializers.set(declaration.name.getText(file), declaration.initializer?.getText(file))
    }
  }
  if (ts.isExpressionStatement(node) && ts.isCallExpression(node.expression)) {
    const call = node.expression
    if (call.expression.getText(file) === 'watch' && call.arguments[0]?.getText(file) === 'scopeKey') {
      restoreCallback = call.arguments[1].getText(file)
    }
  }
})
const bind = (initializer, mocks) => {
  assert.ok(initializer, 'requested component callback missing')
  const js = ts.transpileModule(`const selected = ${initializer}`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText
  return Function(...Object.keys(mocks), `${js}; return selected`)(...Object.values(mocks))
}
const utilitySource = await readFile(new URL('../../utils/rgb11Oob.ts', import.meta.url), 'utf8')
const utilityJS = ts.transpileModule(utilitySource, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText
const { sha256Text, rgb11Amount, rgb11Schema, matchesRGB11Summary } = await import(
  `data:text/javascript;base64,${Buffer.from(utilityJS).toString('base64')}`,
)
const ref = (value) => ({ value })
const canonical = 'e'.repeat(64)
const witness = 'a'.repeat(64)
const contract = 'rgb:review-r2-contract'
const schema = 'review-r2-schema'
const armor = '-----BEGIN RGB CONSIGNMENT-----\nreview-r2-contract-fixture\n-----END RGB CONSIGNMENT-----\n'
const hash = await sha256Text(armor)
// Identical to the existing SDK rgb11BatchRecipientID rule; no new ID rule.
const childID = (index) => createHash('sha256')
  .update(`SAT20-RGB11-BATCH-RECIPIENT-V1:${canonical}:${index}`).digest('hex')
const receiveState = (index, overrides = {}) => ({
  transfer_id: childID(index), direction: 'receive',
  receive_request_id: `request-${index}`, invoice: `witness-invoice-${index}`,
  consignment_hash: hash, witness_txid: witness, output_outpoints: [`${witness}:${index + 1}`],
  status: 'awaiting_broadcast', transport_mode: 'out-of-band',
  asset: { Amount: { Value: String(1000 + index), Precision: 0 } }, ...overrides,
})
const senderState = (index, localID = childID(index)) => ({
  ...receiveState(index), direction: 'send', transfer_id: localID,
  batch_id: canonical, recipient_vout: index + 1,
})
const receipt = { transfer_id: canonical, consignment_hash: hash, contract_id: contract, schema_id: schema }

const fixture = (index, states, receiptValue = receipt) => {
  const calls = []
  const mocks = {
    scopeKey: ref('review-r2-scope'), phase: ref('created'), requestId: ref(`request-${index}`),
    invoice: ref(`witness-invoice-${index}`), amount: ref(String(1000 + index)),
    assetContractID: ref(contract), props: { asset: { precision: 0 } },
    loading: ref(false), errorMessage: ref(''), summary: ref(null), packageHash: ref(''),
    t: (key) => key, sha256Text, rgb11Amount, rgb11Schema,
    packageInput: async () => ({ consignment: armor, hash }),
    ensureScope: (key) => assert.equal(key, 'review-r2-scope'),
    decimalToRaw: bind(initializers.get('decimalToRaw'), { maxUint64: '18446744073709551615' }),
    persistRequest: async (key) => { calls.push(['persist', key]) },
    walletManager: {
      prepareRGB11Consignment: async (id, raw) => {
        calls.push(['prepare', id]); assert.equal(id, `request-${index}`); assert.equal(raw, armor)
        return [null, { receipt: JSON.stringify(receiptValue) }]
      },
      getRGB11State: async () => [null, { state: JSON.stringify({ transfers: states }) }],
    },
  }
  return { mocks, calls, prepare: bind(initializers.get('preparePackage'), mocks) }
}

for (const walletLayout of ['same-wallet', 'different-wallets']) {
  for (const index of [0, 1]) {
    test(`R2-01 ${walletLayout} batch recipient ${index}: prepare produces sender-verifiable canonical summary`, async () => {
      const states = walletLayout === 'same-wallet' ? [receiveState(0), receiveState(1)] : [receiveState(index)]
      const before = structuredClone(states)
      const { mocks, prepare } = fixture(index, states)
      await prepare()
      assert.equal(mocks.errorMessage.value, '', 'existing preparePackage rejected a valid batch receiver record')
      assert.equal(mocks.phase.value, 'prepared')
      assert.equal(mocks.summary.value.transfer_id, canonical, 'receipt identity remains canonical')
      assert.equal(await matchesRGB11Summary(JSON.stringify(mocks.summary.value), senderState(index), contract, schema, armor), true)
      assert.equal(await matchesRGB11Summary(JSON.stringify(mocks.summary.value), senderState(1 - index), contract, schema, armor), false, 'summary cannot approve another invoice/output')
      assert.deepEqual(states, before, 'summary extraction must not mutate SDK records')
      // Already prepared UI callback is a no-op; restoring a created UI over
      // the same persisted SDK rows selects the same request deterministically.
      await prepare()
      mocks.phase.value = 'created'; mocks.summary.value = null
      await prepare()
      assert.equal(mocks.summary.value.recipient_outpoint, `${witness}:${index + 1}`)
    })
  }
}

test('single/legacy canonical local ID remains compatible', async () => {
  const state = receiveState(0, { transfer_id: canonical })
  const { mocks, prepare } = fixture(0, [state])
  await prepare()
  assert.equal(mocks.errorMessage.value, '')
  assert.equal(mocks.phase.value, 'prepared')
  assert.equal(await matchesRGB11Summary(JSON.stringify(mocks.summary.value), senderState(0, canonical), contract, schema, armor), true)
})

test('wrong receive_request_id rejects even when legacy ID, invoice and object hash match', async () => {
  const { mocks, prepare } = fixture(0, [receiveState(0, { transfer_id: canonical, receive_request_id: 'different-request' })])
  await prepare()
  assert.equal(mocks.errorMessage.value, 'rgb11Transfer.summaryMismatch')
  assert.equal(mocks.phase.value, 'created')
  assert.equal(mocks.summary.value, null)
})

for (const [name, states] of [
  ['wrong consignment hash', [receiveState(0, { transfer_id: canonical, consignment_hash: 'f'.repeat(64) })]],
  ['nonunique matching rows', [receiveState(0, { transfer_id: canonical }), receiveState(0, { transfer_id: canonical })]],
  ['wrong amount', [receiveState(0, { transfer_id: canonical, asset: { Amount: { Value: '999', Precision: 0 } } })]],
  ['missing witness', [receiveState(0, { transfer_id: canonical, witness_txid: '' })]],
  ['nonunique output', [receiveState(0, { transfer_id: canonical, output_outpoints: [`${witness}:1`, `${witness}:2`] })]],
]) {
  test(`receive summary rejects ${name}`, async () => {
    const { mocks, prepare } = fixture(0, states)
    await prepare()
    assert.equal(mocks.errorMessage.value, 'rgb11Transfer.summaryMismatch')
    assert.equal(mocks.summary.value, null)
    assert.equal(mocks.phase.value, 'created')
  })
}

for (const index of [0, 1]) {
  test(`restored batch recipient ${index}: current summary still matches only its sender item`, async () => {
    const { mocks } = fixture(index, [receiveState(index)])
    Object.assign(mocks, {
      transferPackage: ref(''), expiresAt: ref(0), sdkTerminalStatus: ref(''),
      restoring: ref(false), restoreFailed: ref(false), receiveMode: ref('witness'), transportMode: ref('out-of-band'),
      Storage: { get: async () => ({ value: `request-${index}` }), set: async () => {}, remove: async () => {} },
      parseFreshSelectionMarker: () => null, rgb11ReceivePackages: new Map(),
      walletManager: { getRGB11State: async () => [null, { state: JSON.stringify({
        transfers: [receiveState(index)],
        ticker_infos: [{ content: { contract_id: contract, schema_id: schema } }],
        reservations: [{ direction: 'receive', request_id: `request-${index}`, contract_id: contract,
          invoice: `witness-invoice-${index}`, amount_raw: String(1000 + index),
          mode: 'witness', transport_mode: 'out-of-band', status: 'awaiting_broadcast', transfer: receiveState(index) }],
      }) }] },
    })
    await bind(restoreCallback, mocks)('review-r2-scope')
    assert.equal(mocks.restoreFailed.value, false)
    assert.equal(mocks.phase.value, 'prepared')
    assert.equal(mocks.summary.value.transfer_id, childID(index))
    assert.equal(await matchesRGB11Summary(JSON.stringify(mocks.summary.value), senderState(index), contract, schema, armor), true)
    assert.equal(await matchesRGB11Summary(JSON.stringify(mocks.summary.value), senderState(1 - index), contract, schema, armor), false)
  })
}

test('receipt and state cannot agree on a hash for a different submitted object', async () => {
  const otherHash = 'f'.repeat(64)
  const { mocks, prepare } = fixture(0, [receiveState(0, { transfer_id: canonical, consignment_hash: otherHash })],
    { ...receipt, consignment_hash: otherHash })
  await prepare()
  assert.equal(mocks.errorMessage.value, 'rgb11Transfer.summaryMismatch')
  assert.equal(mocks.summary.value, null)
})

const senderSource = await readFile(new URL('../../components/wallet/RGB11SendDialog.vue', import.meta.url), 'utf8')
const senderScript = senderSource.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)?.[1]
assert.ok(senderScript)
const senderFile = ts.createSourceFile('sender.ts', senderScript, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const senderInitializers = new Map()
senderFile.forEachChild((node) => {
  if (!ts.isVariableStatement(node)) return
  for (const declaration of node.declarationList.declarations) {
    senderInitializers.set(declaration.name.getText(senderFile), declaration.initializer?.getText(senderFile))
  }
})

for (const walletLayout of ['same-wallet', 'different-wallets']) {
  test(`${walletLayout}: actual prepare, sender verification/broadcast and receive accept callbacks preserve recipient binding`, async () => {
    const receivers = []
    for (const index of [0, 1]) {
      const receiver = fixture(index, walletLayout === 'same-wallet' ? [receiveState(0), receiveState(1)] : [receiveState(index)])
      await receiver.prepare()
      assert.equal(receiver.mocks.errorMessage.value, '')
      receivers.push(receiver)
    }
    const calls = []
    const items = receivers.map(({ mocks }, index) => ({ transferId: childID(index),
      transportMode: 'out-of-band', state: senderState(index),
      summary: JSON.stringify(mocks.summary.value), matched: '', confirmed: false }))
    const senderMocks = {
      loading: ref(false), message: ref(''), success: ref(false), t: (key) => key,
      transferId: ref(childID(0)), batchItems: ref(items), outOfBand: ref(true),
      scopeKey: ref('scope'), preparedScope: ref('scope'),
      assetContractID: ref(contract), schemaID: ref(schema), armoredConsignment: ref(armor),
      walletStore: { locked: false, isSwitchingWallet: false, isSwitchingAccount: false, isSwitchingNetwork: false },
      allRecipientsConfirmed: { get value() { return items.every((item) => item.confirmed && item.matched === item.summary && !!item.matched) } },
      matchesRGB11Summary,
      beginNamedPwaOperation: async () => 'operation',
      finishPwaOperation: async (_operation, error) => { calls.push(['blocked', error.message]) },
      completeBroadcast: async (txid) => { calls.push(['completed', txid]) },
      walletManager: { broadcastRGB11OutOfBand: async (ids) => {
        calls.push(['broadcast', [...ids]])
        assert.deepEqual(ids, [childID(0), childID(1)])
        return [null, { txid: witness }]
      } },
    }
    const verify = bind(senderInitializers.get('verifyRecipientSummary'), senderMocks)
    const broadcast = bind(senderInitializers.get('broadcastTraditional'), senderMocks)
    for (const item of items) {
      await verify(item)
      assert.equal(item.matched, item.summary)
      item.confirmed = true
    }
    await broadcast()
    assert.deepEqual(calls, [['broadcast', [childID(0), childID(1)]], ['completed', witness]])
    await broadcast()
    assert.equal(calls.filter(([action]) => action === 'broadcast').length, 1, 'broadcast requires fresh recipient confirmations')
    const acceptedIDs = []
    for (const [index, { mocks }] of receivers.entries()) {
      mocks.walletManager.acceptRGB11Consignment = async (requestID, consignment) => {
        assert.equal(requestID, `request-${index}`); assert.equal(consignment, armor)
        acceptedIDs.push(requestID)
        return [null, { receipt: JSON.stringify(receipt) }]
      }
      Object.assign(mocks, { refreshRGB11State: async () => {}, toast: () => {} })
      const accept = bind(initializers.get('acceptPackage'), mocks)
      await accept()
      assert.equal(mocks.phase.value, 'accepted')
      assert.equal(mocks.errorMessage.value, '')
      await accept()
    }
    assert.deepEqual(acceptedIDs, ['request-0', 'request-1'], 'repeat accept UI action cannot cross requests or submit twice')
  })
}
