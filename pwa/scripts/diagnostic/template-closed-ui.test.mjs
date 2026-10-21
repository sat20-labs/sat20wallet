import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { createRequire } from 'node:module'
import test from 'node:test'

const require = createRequire(new URL('../../package.json', import.meta.url))
const Vue = require('vue')
const ts = require('typescript')
const sourcePath = process.env.SAT20_TOOLS_SOURCE || new URL('../../entrypoints/popup/pages/wallet/Tools.vue', import.meta.url)
const source = fs.readFileSync(sourcePath, 'utf8')

function actualSection(start, end) {
  const from = source.indexOf(start)
  const to = source.indexOf(end, from + start.length)
  assert.ok(from >= 0 && to > from, `SFC section not found: ${start} ... ${end}`)
  return source.slice(from, to)
}

// These are the actual state readers, computed expressions, and action options
// from the frozen SFC; only imports and unrelated component setup are omitted.
const actualExpressions = [
  actualSection('const walkValues =', 'const findContractArray ='),
  actualSection('const contractUiSubtype = computed', 'const assetNameFromUnknown ='),
  actualSection('const isPositiveDecimalValue =', 'const decimalRatio ='),
  actualSection('const contractAssetAInPool = computed', 'const depthValue ='),
  actualSection('const templateInvokeActionsBySubtype:', 'const invokeContractTypeLabel = computed'),
  actualSection('const isInvokeActionDisabled =', 'type ContextRow'),
  `globalThis.actual = { contractStatusLabel, isAmmLiquidityOpen, ammCanSwap, invokeActionOptions, contractActionChips, isInvokeActionDisabled }`,
].join('\n')
const compiled = ts.transpileModule(actualExpressions, {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText

function readActualUI({ subtype = 'amm.tc', closed = false, assetAInPool = '0', assetBInPool = '0' } = {}) {
  const context = vm.createContext({
    computed: Vue.computed,
    contractState: Vue.ref({ state: { closed, assetAInPool, assetBInPool, deployer: 'fixture-owner' } }),
    selectedContract: Vue.ref(null),
    invokeContractType: Vue.ref('template'),
    invokeContractSubtype: Vue.ref(subtype),
    walletStore: { address: 'fixture-owner' },
    t: key => key,
  })
  vm.runInContext(compiled, context)
  const ui = context.actual
  return {
    status: ui.contractStatusLabel.value,
    writeDisabled: ui.isInvokeActionDisabled('swap'),
    liquidityOpen: ui.isAmmLiquidityOpen.value,
    canSwap: ui.ammCanSwap.value,
    actions: Array.from(ui.invokeActionOptions.value),
    enabledActions: Array.from(ui.contractActionChips.value.filter(action => action.enabled).map(action => action.value)),
  }
}

test('closed AMM with an empty pool shows the closed status', () => {
  assert.equal(readActualUI({ closed: true }).status, 'tools.contracts.closed')
})

test('closed AMM with an empty pool offers neither addliq nor close', () => {
  const ui = readActualUI({ closed: true })
  assert.deepEqual(ui.actions.filter(action => ['addliq', 'close'].includes(action)), [])
  assert.deepEqual(ui.enabledActions.filter(action => ['addliq', 'close'].includes(action)), [])
})

test('open AMM with an empty pool retains the liquidity-open label and actions', () => {
  const ui = readActualUI({ closed: false })
  assert.equal(ui.status, 'tools.contracts.liquidityOpen')
  assert.equal(ui.liquidityOpen, true)
  assert.deepEqual(ui.actions, ['addliq', 'close'])
  assert.deepEqual(ui.enabledActions, ['addliq', 'close'])
})

test('open AMM with a populated pool retains tradable status and actions', () => {
  const ui = readActualUI({ closed: false, assetAInPool: '150', assetBInPool: '1509' })
  assert.equal(ui.status, 'tools.contracts.tradable')
  assert.equal(ui.liquidityOpen, false)
  assert.equal(ui.canSwap, true)
  assert.deepEqual(ui.actions, ['default', 'swap', 'addliq', 'removeliq', 'close'])
})


test('closed LimitOrder shows closed and exposes no write actions', () => {
  const ui = readActualUI({ subtype: 'limitorder.tc', closed: true })
  assert.equal(ui.status, 'tools.contracts.closed')
  assert.deepEqual(ui.actions, [])
})

test('open LimitOrder with an empty book retains its actions', () => {
  const ui = readActualUI({ subtype: 'limitorder.tc', closed: false })
  assert.equal(ui.status, 'tools.contracts.orderbookAvailable')
  assert.deepEqual(ui.actions, ['default', 'swap', 'refund', 'close'])
})


test('a closed template cannot start the signing flow', () => {
  assert.equal(readActualUI({ closed: true }).writeDisabled, true)
  assert.equal(readActualUI({ subtype: 'limitorder.tc', closed: true }).writeDisabled, true)
})

function actualReviewGuard(closed) {
  const context = vm.createContext({
    isContractClosed: Vue.ref(closed), compilerPageDisposed: false,
    revision: 1, invokeReviewRevision: 1, context: 'same-review',
    invokeReviewKey: () => 'same-review', t: key => key,
  })
  const guard = actualSection('    const assertContext = () => {', '    const summary = invokeTransactionSummary')
  vm.runInContext(ts.transpileModule(guard + '\nglobalThis.guard = assertContext', {
    compilerOptions: { target: ts.ScriptTarget.ES2022 },
  }).outputText, context)
  return context.guard
}

test('closure while awaiting quote or confirmation blocks the final signing guard', () => {
  assert.throws(actualReviewGuard(true), /tools.errors.actionUnavailable/)
})

test('an unchanged open contract passes the signing review guard', () => {
  assert.doesNotThrow(actualReviewGuard(false))
})
