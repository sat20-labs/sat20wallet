import assert from 'node:assert/strict'
import fs from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { transformSync } from 'esbuild'
import {
  buildWalletMessagePayload,
  walletMessageDigest,
} from '../../lib/wallet-message-domain.ts'
import {
  assertWalletIdentityReady,
  beginWalletIdentityTransition,
  completeWalletIdentityTransition,
  getWalletIdentityState,
  setWalletIdentityPhase,
} from '../../lib/identity-boundary.ts'
import { toBoundedNumber, toDecimalString } from '../../lib/strict-integers.ts'

const baseMessage = {
  network: 'testnet',
  origin: 'https://app.example',
  timestamp: '1000',
  nonce: 'abcdefghijklmnop',
  message: 'approve order 7',
}

test('wallet message domain binds network, origin, timestamp, and nonce', async () => {
  const base = buildWalletMessagePayload(baseMessage, 1000)
  const variants = [
    { ...baseMessage, network: 'mainnet' },
    { ...baseMessage, origin: 'https://other.example' },
    { ...baseMessage, nonce: 'ponmlkjihgfedcba' },
    { ...baseMessage, timestamp: '1001' },
  ]
  const digest = await walletMessageDigest(base)
  for (const variant of variants) {
    assert.notEqual(await walletMessageDigest(buildWalletMessagePayload(variant, Number(variant.timestamp))), digest)
  }
  assert.throws(() => buildWalletMessagePayload({ ...baseMessage, origin: 'https://app.example/path' }, 1000), /origin/)
  assert.throws(() => buildWalletMessagePayload({ ...baseMessage, timestamp: '9007199254740992' }, 1000), /range/)
})

test('identity generation rejects switching and stale approvals', () => {
  const oldGeneration = assertWalletIdentityReady()
  const generation = beginWalletIdentityTransition()
  assert.equal(generation, oldGeneration + 1)
  assert.equal(getWalletIdentityState().phase, 'QUIESCING')
  assert.throws(() => assertWalletIdentityReady(), /retry after switching/)
  setWalletIdentityPhase(generation, 'REBUILDING')
  setWalletIdentityPhase(generation, 'VERIFYING')
  completeWalletIdentityTransition(generation)
  assert.equal(assertWalletIdentityReady(generation), generation)
  assert.throws(() => assertWalletIdentityReady(oldGeneration), /generation changed/)
})

test('transaction integer conversion rejects unsafe or out-of-range values', () => {
  assert.equal(toDecimalString(123n, 'amount'), '123')
  assert.equal(toDecimalString('900719925474099312345', 'amount'), '900719925474099312345')
  assert.throws(() => toDecimalString(Number.MAX_SAFE_INTEGER + 1, 'amount'), /safe integer/)
  assert.equal(toBoundedNumber('1000', 'count', 1000), 1000)
  assert.throws(() => toBoundedNumber('1001', 'count', 1000), /exceeds/)
})

const psbtComponent = fs.readFileSync(new URL('../../components/approve/SignPsbt.vue', import.meta.url), 'utf8')
const psbtScript = psbtComponent.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*$/gm, '')
const psbtCode = transformSync(psbtScript, { loader: 'ts', target: 'es2022' }).code
const txDetails = { inputs: [{ Outpoint: 'input:0', Value: 1000 }], outputs: [{ Value: 900 }] }

function psbtApproval(chain = 'btc', overrides = {}) {
  const calls = [], events = [], notices = []
  const props = { data: { psbtHex: 'aabb', options: { chain } }, metadata: { identityGeneration: getWalletIdentityState().generation } }
  let load, cleanup
  const walletManager = Object.fromEntries([
    'getTxAssetInfoFromPsbt', 'getTxAssetInfoFromPsbt_SatsNet', 'signPsbt', 'signPsbt_SatsNet',
  ].map((method) => [method, async (...args) => {
    calls.push([method, ...args])
    if (overrides[method]) return overrides[method](...args)
    return [undefined, method.startsWith('get') ? txDetails : { psbt: 'signed' }]
  }]))
  const approval = runInNewContext(`${psbtCode}\n({ confirm, canSign, isLoading, parseError, parsedInputs })`, {
    defineProps: () => props,
    defineEmits: () => (...args) => events.push(args),
    ref: (value) => ({ value }),
    computed: (fn) => ({ get value() { return fn() } }),
    watch: (_source, callback) => { load = callback },
    useWalletStore: () => ({ network: { value: 'testnet' } }),
    storeToRefs: (store) => store,
    useToast: () => ({ toast: (notice) => notices.push(notice) }),
    walletManager,
    assertWalletIdentityReady,
    console: { log() {}, warn() {}, error() {} },
  })
  return { ...approval, props, calls, events, notices, load: () => {
    cleanup?.()
    return load(undefined, undefined, (fn) => { cleanup = fn })
  } }
}

test('PSBT approval uses the original detail and signing APIs on both chains', async () => {
  assert.match(psbtComponent, /:confirm-disabled="!canSign"/)
  assert.match(psbtComponent, /:loading="isLoading"/)
  for (const chain of ['btc', 'sat20', 'satnet', 'satsnet']) {
    const approval = psbtApproval(chain)
    assert.equal(approval.canSign.value, false)
    await approval.load()
    assert.equal(approval.canSign.value, true)
    await approval.confirm()
    const suffix = chain === 'btc' ? '' : '_SatsNet'
    assert.deepEqual(approval.calls, [
      [`getTxAssetInfoFromPsbt${suffix}`, 'aabb', 'testnet'],
      [`signPsbt${suffix}`, 'aabb', false],
    ])
    assert.deepEqual(approval.events, [['confirm', 'signed']])
  }
})

test('PSBT approval refuses loading, missing, and malformed transaction details', async () => {
  let respond
  const loading = psbtApproval('btc', { getTxAssetInfoFromPsbt: () => new Promise((resolve) => { respond = resolve }) })
  const pending = loading.load()
  await loading.confirm()
  assert.equal(loading.canSign.value, false)
  assert.equal(loading.calls.length, 1)
  respond([undefined, txDetails])
  await pending
  assert.equal(loading.canSign.value, true)

  for (const response of [
    [new Error('lookup failed')], [undefined, undefined],
    [undefined, { ...txDetails, inputs: '{' }],
    [undefined, { ...txDetails, inputs: '{}' }],
    [undefined, { ...txDetails, inputs: [] }],
    [undefined, { inputs: txDetails.inputs }],
  ]) {
    const approval = psbtApproval('btc', { getTxAssetInfoFromPsbt: () => response })
    await approval.load()
    assert.equal(approval.parseError.value, true)
    assert.equal(approval.canSign.value, false)
    await approval.confirm()
    assert.equal(approval.calls.length, 1)
    assert.equal(approval.events.length, 0)
  }
})

test('PSBT approval refuses a stale wallet identity before signing', async () => {
  const approval = psbtApproval()
  await approval.load()
  const generation = beginWalletIdentityTransition()
  completeWalletIdentityTransition(generation)
  await approval.confirm()
  assert.equal(approval.calls.length, 1)
  assert.equal(approval.events.length, 0)
  assert.equal(approval.parseError.value, true)
})

test('PSBT approval does not return a signature after an identity switch', async () => {
  let respond
  const approval = psbtApproval('btc', { signPsbt: () => new Promise((resolve) => { respond = resolve }) })
  await approval.load()
  const signing = approval.confirm()
  await approval.confirm()
  assert.equal(approval.calls.length, 2, 'a second click must not sign while busy')
  const generation = beginWalletIdentityTransition()
  completeWalletIdentityTransition(generation)
  respond([undefined, { psbt: 'signed' }])
  await signing
  assert.equal(approval.events.length, 0)
  assert.equal(approval.parseError.value, true)
})

test('PSBT approval ignores detail responses from a replaced request', async () => {
  const responses = []
  const approval = psbtApproval('btc', { getTxAssetInfoFromPsbt: () => new Promise((resolve) => responses.push(resolve)) })
  const old = approval.load()
  approval.props.data.psbtHex = 'ccdd'
  const current = approval.load()
  responses.shift()([undefined, txDetails])
  await old
  assert.equal(approval.isLoading.value, true)
  assert.equal(approval.canSign.value, false)
  assert.equal(approval.parsedInputs.value.length, 0)
  responses.shift()([undefined, txDetails])
  await current
  await approval.confirm()
  assert.deepEqual(approval.calls.at(-1), ['signPsbt', 'ccdd', false])
})

test('both PWA DApp bridges domain-wrap messages and reject raw signData', () => {
  const pwaBridge = fs.readFileSync(new URL('../../composables/usePwaDappBridge.ts', import.meta.url), 'utf8')
  const legacyBridge = fs.readFileSync(new URL('../../composables/webview-bridge/handlers/transaction-handlers.ts', import.meta.url), 'utf8')
  for (const source of [pwaBridge, legacyBridge]) {
    assert.match(source, /buildWalletMessagePayload/)
    assert.match(source, /Raw DApp signData is disabled/)
  }
})
