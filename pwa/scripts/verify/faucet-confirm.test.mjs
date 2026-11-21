import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, reactive, ref } from 'vue'

// Use the Tools page's confirmation flow with controlled contract replies.
const source = readFileSync(new URL('../../entrypoints/popup/pages/wallet/Tools.vue', import.meta.url), 'utf8')
const extract = (start, end) => {
  const from = source.indexOf(start)
  const to = source.indexOf(end, from)
  assert.ok(from >= 0 && to > from)
  return source.slice(from, to)
}
const { outputText } = ts.transpileModule([
  extract('const faucetAddress =', 'const handleRGB11Issued ='),
  extract('const readFaucetStatus =', 'watch([activeTab, env, network]'),
  extract('const faucetReviewKey =', 'const parseOptionalPositiveInteger ='),
  'globalThis.faucet = { faucetAddress, faucetContracts, faucetListState, faucetSelectedUrl, selectFaucet, sendFaucetSats }',
].join('\n'), { compilerOptions: { target: ts.ScriptTarget.ES2022 } })

const contract = { url: 'faucet_asset_faucet.tc', address: 'faucet', asset: 'brc20:f:gas' }
const status = (fields = {}, terms = {}) => [undefined, { contractStatus: JSON.stringify({
  channelAddr: contract.address, status: 100, currentBlock: 20, enableBlock: 10,
  Contract: { contractType: 'faucet.tc', startBlock: 0, endBlock: 0, ...terms },
  ...fields,
}) }]
const unavailable = [new Error('lookup failed'), undefined]

function fixture({ replies = [], confirm = true } = {}) {
  const log = [], errors = [], confirmations = [], transfers = []
  const context = {
    ref, computed, t: key => key,
    env: ref('test'), network: ref('testnet'),
    walletStore: reactive({ rootAccountId: 'root', walletId: 'wallet', accountIndex: 0, address: 'source' }),
    sat20: {
      getDeployedContractStatus: async url => {
        log.push('status')
        assert.equal(url, contract.url)
        return replies.shift()
      },
      sendAssets_SatsNet: async (...args) => {
        log.push('send')
        transfers.push(args)
        return [undefined, { txId: 'tx' }]
      },
    },
    confirmToolTransaction: async payload => {
      log.push('confirm')
      confirmations.push(payload)
      return confirm
    },
    parsePositiveInteger: Number, displayAssetName: value => value, l2NetworkLabel: () => 'testnet',
    showSuccess: () => {}, showError: (_, error) => errors.push(error.message),
  }
  vm.runInNewContext(outputText, context)
  const flow = context.faucet
  flow.faucetContracts.value = [contract]
  flow.faucetListState.value = 'ready'
  flow.selectFaucet(contract)
  return { ...flow, log, errors, confirmations, transfers }
}

test('known faucet verifies twice and sends only after confirmation', async () => {
  const f = fixture({ replies: [status(), status({}, { startBlock: 20, endBlock: 20 })] })
  await f.sendFaucetSats()
  assert.deepEqual(f.log, ['status', 'confirm', 'status', 'send'])
  assert.equal(f.confirmations[0].details[0].value, contract.asset)
  assert.equal(f.confirmations[0].warning, undefined)
  assert.deepEqual(f.transfers, [[contract.address, '::', '1000', '']])
})

for (const [name, reply, error] of [
  ['not enabled', status({ enableBlock: 21 }), 'inactive'],
  ['before start', status({}, { startBlock: 21 }), 'inactive'],
  ['past end', status({}, { endBlock: 19 }), 'inactive'],
  ['closed', status({ status: -1 }), 'inactive'],
  ['lookup failure', unavailable, 'statusUnavailable'],
  ['wrong address', status({ channelAddr: 'other' }), 'statusUnavailable'],
  ['wrong type', status({}, { contractType: 'amm.tc' }), 'statusUnavailable'],
  ['missing height', status({}, { endBlock: undefined }), 'statusUnavailable'],
]) {
  test(`known faucet blocks before confirmation when ${name}`, async () => {
    const f = fixture({ replies: [reply] })
    await f.sendFaucetSats()
    assert.deepEqual(f.log, ['status'])
    assert.deepEqual(f.errors, [`tools.faucet.${error}`])
  })
}

for (const [name, reply, error] of [
  ['starts closing', status({ status: 200 }), 'inactive'],
  ['status becomes unavailable', unavailable, 'statusUnavailable'],
]) {
  test(`known faucet blocks before broadcast when it ${name}`, async () => {
    const f = fixture({ replies: [status(), reply] })
    await f.sendFaucetSats()
    assert.deepEqual(f.log, ['status', 'confirm', 'status'])
    assert.deepEqual(f.errors, [`tools.faucet.${error}`])
  })
}

for (const confirm of [false, true]) {
  test(`an unknown address is an ordinary transfer and the user ${confirm ? 'approves' : 'cancels'}`, async () => {
    const f = fixture({ confirm })
    f.faucetAddress.value = 'unlisted'
    f.faucetSelectedUrl.value = ''
    await f.sendFaucetSats()
    assert.deepEqual(f.log, confirm ? ['confirm', 'send'] : ['confirm'])
    assert.equal(f.confirmations[0].purpose, 'tools.faucet.plainTransfer')
    assert.equal(f.confirmations[0].warning, 'tools.faucet.unverifiedWarning')
    assert.equal(f.confirmations[0].details, undefined)
    assert.equal(f.confirmations[0].to, 'unlisted')
    assert.deepEqual(f.transfers, confirm ? [['unlisted', '::', '1000', '']] : [])
  })
}
