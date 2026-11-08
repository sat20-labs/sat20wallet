import { createI18n } from 'vue-i18n'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { transformSync } from 'esbuild'
import { reactive, ref, watch } from 'vue'

const messages = JSON.parse(readFileSync(new URL('../../locales/en.json', import.meta.url), 'utf8'))
const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: messages } })

const { code } = transformSync(readFileSync(new URL('../../utils/operationLog.ts', import.meta.url), 'utf8'), {
  loader: 'ts', format: 'cjs', target: 'es2022',
})
const module = { exports: {} }
runInNewContext(code, { module, exports: module.exports })
const project = module.exports.projectSettledRGB11OperationLogs
const projectStatus = module.exports.projectRGB11OperationStatus
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

test('current failed-operation status exposes only exact public lifecycle fields', () => {
  const original = { ...record(), status: 'failed', history: [
    ...record().history, { status: 'failed', details: { error: 'invalid dkvs fee proof' } },
  ] }
  const current = { ...transfer(), status: 'prepared', ack_status: 'pending',
    delivery_record_key: '/mail/public-key', delivery_record_hash: 'c'.repeat(64),
    delivery_acknowledged: false, invoice: 'private-invoice', proof: 'private-proof',
    seal_disclosure: 'private-seal', signed_psbt: 'private-psbt',
  }
  const before = JSON.stringify([original, current])
  const snapshot = projectStatus(original, [current], { walletId: '1', accountIndex: 0 })
  assert.deepEqual(JSON.parse(JSON.stringify(snapshot)), {
    transfer_id: 'exact-transfer', status: 'prepared', ack_status: 'pending',
    delivery_record_key: '/mail/public-key', delivery_record_hash: 'c'.repeat(64),
    delivery_acknowledged: false, witness_txid: txid, reject_reason: '',
    logged_error: 'invalid dkvs fee proof',
  })
  assert.equal(JSON.stringify(snapshot).includes('private-'), false)
  assert.equal(JSON.stringify([original, current]), before)
})

test('current status fails closed for another identity, asset or ambiguous transfer', () => {
  const identity = { walletId: 1, accountIndex: 0 }
  const original = record()
  for (const [operation, transfers, scope] of [
    [original, [transfer()], { ...identity, walletId: 2 }],
    [original, [transfer()], { ...identity, accountIndex: 1 }],
    [{ ...original, wallet_id: undefined }, [transfer()], identity],
    [{ ...original, history: [...original.history, { details: { transfer_id: 'different-transfer' } }] }, [transfer()], identity],
    [original, [{ ...transfer(), asset: { Name: { Protocol: 'rgb11', Type: 'f', Ticker: 'c'.repeat(64) } } }], identity],
    [original, [{ ...transfer(), direction: 'receive' }], identity],
    [original, [transfer(), transfer()], identity],
    [original, [], identity],
  ]) assert.equal(projectStatus(operation, transfers, scope), null)
})

test('SDK root JSON omits zero account_index without granting another account access', () => {
  const rootJSON = { ...record() }
  delete rootJSON.account_index
  assert.ok(projectStatus(rootJSON, [transfer()], { walletId: 1, accountIndex: 0 }))
  assert.equal(projectStatus(rootJSON, [transfer()], { walletId: 1, accountIndex: 1 }), null)
  assert.equal(projectStatus(rootJSON, [transfer()], { walletId: 2, accountIndex: 0 }), null)
  assert.equal(projectStatus(rootJSON, [transfer()], { walletId: 1, accountIndex: undefined }), null)
})

test('missing ACK evidence and historical broadcast attempts remain unknown', () => {
  for (const status of ['prepared', 'broadcast-attempted', 'settled']) {
    const snapshot = projectStatus(record(), [{ ...transfer(), status }], { walletId: 1, accountIndex: 0 })
    assert.equal(snapshot.status, status)
    assert.equal(snapshot.delivery_acknowledged, null)
    assert.equal(snapshot.delivery_record_key, '')
    assert.equal(Object.hasOwn(snapshot, 'ever_broadcast_attempted'), false)
    assert.equal(Object.hasOwn(snapshot, 'broadcast'), false)
  }
})

function loadStatusPage(getState) {
  const source = readFileSync(new URL('../../entrypoints/popup/pages/wallet/settings/OperationLogs.vue', import.meta.url), 'utf8')
  const script = source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)?.[1]
  assert.ok(script)
  const { code } = transformSync(`${script}\nexport { detail, detailVisible, rgbStatus, rgbStatusError, rgbStatusLoading, readRGBTaskStatus, closeDetail };`, {
    loader: 'ts', format: 'cjs', target: 'es2022',
  })
  const instance = { exports: {} }
  const wallet = reactive({ walletId: '1', accountIndex: 0, rootAccountId: 'root',
    address: 'original-address', network: 'regtest', locked: false })
  const native = new Proxy({ getRGB11State: getState }, {
    get(target, key) {
      assert.equal(key, 'getRGB11State', `unexpected wallet operation: ${String(key)}`)
      return target[key]
    },
  })
  runInNewContext(code, {
    module: instance, exports: instance.exports,
    require(name) {
      if (name === 'vue') return { ref, watch, onMounted: () => {}, onUnmounted: () => {} }
      if (name === 'vue-i18n') return { useI18n: () => i18n.global }
      if (name === '@/utils/sat20') return { __esModule: true, default: native }
      if (name === '@/store') return { useWalletStore: () => wallet, useGlobalStore: () => ({ env: 'test' }) }
      if (name === '@/utils/operationLog') return module.exports
      throw new Error(`Unexpected import: ${name}`)
    },
  })
  const page = instance.exports
  page.detail.value = { ...record(), status: 'failed' }
  page.detailVisible.value = true
  return { page, wallet }
}

test('manual status read calls only the existing getter and never persists the log', async () => {
  let calls = 0
  const { page } = loadStatusPage(async () => {
    calls++
    return [undefined, { state: JSON.stringify({ transfers: [{ ...transfer(), status: 'prepared' }],
      proofs: [{ seal_disclosure: 'private-seal' }], invoice: 'private-invoice' }) }]
  })
  assert.equal(calls, 0)
  const stored = JSON.stringify(page.detail.value)
  await page.readRGBTaskStatus()
  assert.equal(calls, 1)
  assert.equal(page.rgbStatus.value.status, 'prepared')
  assert.equal(JSON.stringify(page.rgbStatus.value).includes('private-'), false)
  assert.equal(JSON.stringify(page.detail.value), stored)
  assert.equal(page.rgbStatusLoading.value, false)
})

test('closing detail or switching identity discards an in-flight status response', async () => {
  for (const change of ['close', 'switch']) {
    let release
    const waiting = new Promise(resolve => { release = resolve })
    const { page, wallet } = loadStatusPage(() => waiting)
    const request = page.readRGBTaskStatus()
    if (change === 'close') page.closeDetail()
    else wallet.accountIndex = 1
    release([undefined, { state: JSON.stringify({ transfers: [transfer()] }) }])
    await request
    assert.equal(page.rgbStatus.value, null)
    assert.equal(page.rgbStatusError.value, '')
    assert.equal(page.rgbStatusLoading.value, false)
  }
})

test('failed read clears the old snapshot and does not expose the raw getter error', async () => {
  const { page } = loadStatusPage(async () => [new Error('private-invoice'), undefined])
  page.rgbStatus.value = { status: 'settled' }
  await page.readRGBTaskStatus()
  assert.equal(page.rgbStatus.value, null)
  assert.equal(page.rgbStatusError.value, 'Unable to read current RGB task status. The stored log is unchanged.')
  assert.equal(page.rgbStatusError.value.includes('private-invoice'), false)
})

test('identity change removes an already displayed snapshot without another getter call', async () => {
  let calls = 0
  const { page, wallet } = loadStatusPage(async () => {
    calls++
    return [undefined, { state: JSON.stringify({ transfers: [transfer()] }) }]
  })
  await page.readRGBTaskStatus()
  assert.ok(page.rgbStatus.value)
  wallet.accountIndex = 1
  assert.equal(page.rgbStatus.value, null)
  assert.equal(page.rgbStatusError.value, '')
  assert.equal(calls, 1)
})
