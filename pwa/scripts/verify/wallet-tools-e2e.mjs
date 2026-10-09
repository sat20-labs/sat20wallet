import assert from 'node:assert/strict'
import { expect } from '@playwright/test'

// Real Tools clicks, bundled solc, WASM signatures and canonical contract
// execution. This suite runs after POS and before the final node-stake cases.
export const requiredPwaToolsCases = [
  'Tools PWA: deploy and fund an Exchange template through wallet pages',
  'Tools PWA: Faucet sends sats and receives actual contract-settled GAS',
  'Tools PWA: template close is signed through the page and settles remaining GAS',
  'Tools PWA: bundled Solidity compiler deploys the current SDK probe on chain',
  'Tools PWA: EVM call changes canonical state and appears in page history',
  'Tools PWA: Agent configuration rejects an unsafe source before signing',
  'Tools PWA: direct WASM reconciles Exchange index queries and deployed service status',
  'Tools PWA: direct WASM rejects an unknown template and invoke without broadcasting',
]

const poll = { timeout: 180000, intervals: [250, 500, 1000, 2000] }
const txidPattern = /^[0-9a-f]{64}$/
const assetKey = name => `${name.Protocol}:${name.Type}:${name.Ticker}`
const integer = value => {
  assert.match(String(value), /^\d+$/)
  return BigInt(String(value))
}
const findField = (value, key) => {
  if (!value || typeof value !== 'object') return undefined
  if (Object.hasOwn(value, key)) return value[key]
  for (const nested of Object.values(value)) {
    const result = findField(nested, key)
    if (result !== undefined) return result
  }
}

export async function runPwaToolsCases(t, fixture) {
  const { check, device, walletCall } = t
  const actor = fixture.basicWallet
  assert.ok(actor?.mnemonic && fixture.recipient?.mnemonic)
  assert.ok(fixture.tools?.evmSource && fixture.tools.gasAsset)
  assert.equal(fixture.config.Chain, 'testnet')
  const controller = new URL(fixture.control_url)
  assert.equal(controller.protocol, 'http:')
  assert.ok(['127.0.0.1', 'localhost'].includes(controller.hostname))
  const gas = fixture.tools.gasAsset
  const gasTicker = gas.split(':')[2]
  const pages = []
  let page
  let buyer
  let exchange
  let exchangeInventory
  let evm
  const failures = []

  const control = async (action, body = {}) => {
    const response = await fetch(new URL(`/${action}`, controller), {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
      signal: AbortSignal.timeout(240000),
    })
    if (action === 'transaction' && response.status === 404) return null
    const text = await response.text()
    assert.ok(response.ok, `${action}: HTTP ${response.status}: ${text}`)
    const result = JSON.parse(text)
    assert.ok(!result.error, `${action}: ${result.error}`)
    return result
  }
  const visit = async (target, path) => {
    await target.evaluate(path => { location.hash = `#${path}` }, path)
    await expect.poll(() => new URL(target.url()).hash, poll).toBe(`#${path}`)
  }
  const importWallet = async identity => {
    const target = await device()
    pages.push(target)
    await visit(target, '/import')
    await target.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(identity.mnemonic)
    await target.getByLabel('New Wallet Password', { exact: true }).fill(identity.password)
    await target.getByLabel('Confirm Password', { exact: true }).fill(identity.password)
    await target.getByRole('button', { name: 'Import Wallet', exact: true }).click()
    await expect(target.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: 90000 })
    await expect.poll(() => target.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().address), poll).toBe(identity.address)
    return target
  }
  const panel = () => page.locator('[role="tabpanel"][data-state="active"]')
  const field = label => panel().locator('label').filter({ hasText: new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`) }).locator('..')
  const fill = (label, value, tag = 'input') => field(label).locator(tag).fill(String(value))
  const select = async (locator, choice) => {
    await locator.click()
    await page.getByRole('option', { name: choice, exact: true }).click()
  }
  const toolsTab = async name => {
    await visit(page, '/wallet/tools')
    const tab = page.getByRole('tab', { name, exact: true })
    await tab.click()
    await expect(tab).toHaveAttribute('aria-selected', 'true')
  }
  const deployForm = async (type, schema) => {
    // Navigating away clears the previous Tools detail/form state through the
    // normal Vue route lifecycle, including the compiler worker.
    await visit(page, '/wallet')
    await toolsTab('Smart Contracts')
    await panel().getByRole('button', { name: /^Deploy Smart Contract/ }).click()
    await panel().getByRole('button', { name: 'Load', exact: true }).click()
    await expect(panel().getByRole('button', { name: 'Load', exact: true })).toBeEnabled()
    await select(field('Contract type').getByRole('combobox'), type)
    await expect(panel().getByRole('combobox').nth(1)).toBeVisible()
    await select(panel().getByRole('combobox').nth(1), schema)
  }
  const assetField = async (label, key) => {
    const [protocol, , ticker] = key.split(':')
    await select(field(label).getByRole('combobox'), key === '::' ? 'sats' : protocol)
    if (key !== '::') await field(label).locator('input').fill(ticker)
    await field(label).getByRole('button', { name: 'Check', exact: true }).click()
  }
  const confirmTools = async requiredText => {
    const dialog = page.getByRole('dialog')
    try { await expect(dialog).toBeVisible() } catch (error) {
      const failures = (await panel().locator('pre').allTextContents()).flatMap(text => {
        try { const value = JSON.parse(text); return value.stage === 'error' ? [value] : [] }
        catch { return [] }
      })
      const runtime = await page.evaluate(async () => {
        const { snapshotWasmRuntimeDiagnostics } = await import('/utils/wasmRuntimeDiagnostics.ts')
        return { route: location.hash, breadcrumbs: snapshotWasmRuntimeDiagnostics() }
      }).catch(() => null)
      error.message += `; tools_review_errors=${JSON.stringify(failures)}; tools_runtime=${JSON.stringify(runtime)}`
      throw error
    }
    for (const text of requiredText) await expect(dialog).toContainText(text)
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await expect(dialog).toBeHidden()
  }
  const submitDeploy = async name => {
    await panel().getByRole('button', { name: 'Deploy Smart Contract', exact: true }).click()
    await confirmTools([name, 'SatoshiNet', 'testnet'])
    let result
    await expect.poll(async () => {
      for (const text of await panel().locator('pre').allTextContents()) {
        try {
          const value = JSON.parse(text)
          if (value.txid || value.stage === 'error') { result = value; return true }
        } catch { /* The compiler config and bytecode preview are not results. */ }
      }
      return false
    }, poll).toBe(true)
    assert.notEqual(result.stage, 'error', JSON.stringify(result))
    assert.match(result.txid, txidPattern)
    assert.ok(result.contractAddress, 'deployment must expose its actual contract address')
    await confirmedWork(result.txid)
    return result
  }
  const confirmedWork = async txid => {
    assert.match(txid, txidPattern)
    let transaction
    await expect.poll(async () => {
      transaction = await control('transaction', { txid })
      return Boolean(transaction?.confirmations)
    }, poll).toBe(true)
    assert.equal(transaction.txid, txid)
    assert.match(transaction.blockhash, txidPattern)
    assert.ok(transaction.results?.length, 'broadcast/confirmation alone is not contract execution')
    for (const result of transaction.results) {
      assert.ok(result.confirmations > 0)
      assert.equal(result.blockhash, transaction.blockhash)
      assert.ok(result.vin.some(input => input.txid === txid), 'Result does not consume this signed work transaction')
      const outcomes = (result.contractOps || []).filter(op => op.kind === 'result')
      assert.ok(outcomes.length, 'canonical Result lacks decoded execution status')
      for (const outcome of outcomes) assert.equal(outcome.status?.toLowerCase(), 'success', JSON.stringify(outcome))
    }
    console.log(JSON.stringify({ pwaContractWork: txid, block: transaction.blockhash, results: transaction.results.map(result => result.txid) }))
    return transaction
  }
  const contractRead = async (method, address) => {
    assert.ok(['getContract', 'getContractState', 'getContractHistory'].includes(method))
    const result = await page.evaluate(async ({ method, address }) => {
      const api = (await import('/apis/smartcontract.ts')).default
      return api[method]({ network: 'testnet', contract: address, limit: 100 })
    }, { method, address })
    assert.equal(result.code, 0, result.msg)
    return result.data
  }
  const contractQuery = async request => {
    const result = await walletCall(page, 'queryContract', request)
    const response = JSON.parse(result.result)
    // The SDK unwraps info/state data; list/history retain the REST envelope.
    if (request.Query === 'info' || request.Query === 'state') return response
    assert.equal(response.code, 0, response.msg)
    return response.data
  }
  const amount = async (target, address, key) => {
    const result = await walletCall(target, 'getAssetAmount_SatsNet', address, key)
    return integer(result.availableAmt) + integer(result.lockedAmt)
  }
  const gasInOutputs = outputs => outputs
    .flatMap(output => output.Assets || [])
    .filter(asset => assetKey(asset.Name) === gas)
    .reduce((sum, asset) => sum + integer(asset.Amount), 0n)
  const gasOutputs = (work, pkScript) => gasInOutputs(work.results.flatMap(result => result.vout)
    .filter(output => output.scriptPubKey.hex === pkScript))
  const contractFunding = (work, address) => work.vout.filter(output => output.contract?.contract === address)
  const actorGasSpent = async work => {
    let total = 0n
    for (const input of work.vin) {
      const previous = await control('transaction', { txid: input.txid })
      assert.ok(previous, 'signed work references a missing previous transaction')
      const output = previous.vout.find(output => output.n === input.vout)
      assert.ok(output, 'signed work references a missing previous output')
      assert.equal(output.scriptPubKey.hex, actor.pk_script, 'close spent an unexpected actor input')
      total += gasInOutputs([output])
    }
    return total
  }
  const openContract = async address => {
    await visit(page, '/wallet')
    await toolsTab('Smart Contracts')
    await panel().getByRole('button', { name: /^Invoke Smart Contract/ }).click()
    const search = panel().getByPlaceholder('Enter a contract address for exact search', { exact: true })
    await search.fill(address)
    await search.locator('..').getByRole('button').click()
    await expect(panel().getByText('Real-time Status', { exact: true })).toBeVisible()
    await expect(field('Contract address').locator('input')).toHaveValue(address)
  }
  const submitInvoke = async action => {
    await select(field('Action').getByRole('combobox'), action)
    await panel().getByRole('button', { name: 'Sign and Broadcast', exact: true }).click()
    await confirmTools([actor.address, action, 'testnet'])
    const receipt = panel().locator('p').filter({ hasText: /^txid: [0-9a-f]{64}$/ })
    await expect(receipt).toHaveCount(1, { timeout: 120000 })
    const txid = (await receipt.textContent()).trim().slice(6)
    return confirmedWork(txid)
  }
  const historyThroughUI = async (address, txid, kind) => {
    await panel().getByRole('button', { name: 'Back to Contracts', exact: true }).click()
    await panel().getByRole('button', { name: 'Query History', exact: true }).click()
    const historyView = panel().locator('span').filter({ hasText: /^Query History$/ }).locator('../..').locator('pre')
    await expect(historyView).toHaveCount(1)
    await expect(historyView).toContainText(txid)
    const history = await contractRead('getContractHistory', address)
    const record = history.find(item => item.txid === txid && item.contract === address)
    assert.ok(record, 'independent canonical history lacks this transaction')
    if (kind) {
      assert.equal(record.kind, kind)
      if (kind === 'result') assert.equal(record.status, 'success')
    }
  }
  const sendGasThroughUI = async (address, quantity) => {
    await visit(page, '/wallet')
    await page.getByRole('tab', { name: 'SatoshiNet', exact: true }).click()
    const kind = { ordx: 'ORDX', runes: 'Runes', brc20: 'BRC20' }[gas.split(':')[0]]
    assert.ok(kind, `unsupported fixture gas asset ${gas}`)
    await panel().getByRole('button', { name: kind, exact: true }).click()
    const row = panel().locator('div.bg-muted.border').filter({ has: page.getByText(gasTicker.toUpperCase(), { exact: true }) })
    await expect(row).toHaveCount(1)
    await row.getByRole('button', { name: 'Send', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: 'Send Asset', exact: true })).toBeVisible()
    await dialog.getByRole('button', { name: 'Normal', exact: true }).click()
    await dialog.getByPlaceholder('Enter amount', { exact: true }).fill(String(quantity))
    await dialog.getByPlaceholder('Enter address', { exact: true }).fill(address)
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await expect(dialog.getByRole('heading', { name: 'Please Confirm', exact: true })).toBeVisible()
    await expect(dialog).toContainText(address)
    await expect(dialog).toContainText(String(quantity))
    const before = await logs()
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await expect(dialog).toBeHidden()
    let receipt
    await expect.poll(async () => {
      const oldIDs = new Set(before.map(log => log.id))
      const added = (await logs()).filter(log => !oldIDs.has(log.id) && log.action === 'send_asset_l2')
      assert.ok(added.length <= 1)
      receipt = added[0]
      if (receipt?.status === 'failed') throw new Error(JSON.stringify(receipt))
      return receipt?.status === 'succeeded'
    }, poll).toBe(true)
    return confirmedWork(receipt.txid)
  }
  const logs = async () => {
    const response = await page.evaluate(() => window.sat20wallet_operation_log.getOperationLogs())
    assert.equal(response.code, 0, response.msg)
    return JSON.parse(response.data.logs)
  }
  const ensureExchange = async (fresh = false) => {
    if (!page) page = await importWallet(actor)
    if (exchange && !fresh) return
    await deployForm('Template', 'Exchange')
    await assetField('Asset A', gas)
    await assetField('Asset B', '::')
    await select(field('Price mode').getByRole('combobox'), 'By sold Asset A')
    if (await field('Threshold').count() === 0) await panel().getByRole('button', { name: 'Add Price steps', exact: true }).click()
    await fill('Threshold', '0')
    await fill('Asset B per Asset A', '0.001')
    exchange = await submitDeploy('Exchange')
  }

  try {
    try {
    await check(requiredPwaToolsCases[0], async () => {
      page = await importWallet(actor)
      await expect.poll(() => amount(page, actor.address, gas), poll).toBeGreaterThan(10000000n)
      await deployForm('Template', 'Exchange')
      await assetField('Asset A', gas)
      await assetField('Asset B', '::')
      await select(field('Price mode').getByRole('combobox'), 'By sold Asset A')
      if (await field('Threshold').count() === 0) await panel().getByRole('button', { name: 'Add Price steps', exact: true }).click()
      await fill('Threshold', '0')
      await fill('Asset B per Asset A', '0.001')
      exchange = await submitDeploy('Exchange')
      await openContract(exchange.contractAddress)
      await historyThroughUI(exchange.contractAddress, exchange.txid)
      const before = await amount(page, exchange.contractAddress, gas)
      const snapshot = await control('snapshot')
      assert.ok(snapshot.height < 100000, 'fixed 50 GAS Result fee assumes the first gas-price epoch')
      const funding = await sendGasThroughUI(exchange.contractAddress, 5000000n)
      assert.equal(gasInOutputs(contractFunding(funding, exchange.contractAddress)), 5000000n,
        'UI supply quantity must equal the signed contract funding output')
      // At this height ResultBaseGas=50,000 and 1,000 execution units cost
      // one GAS. Exchange treats GAS as its business asset and subtracts
      // that 50-GAS Result fee before crediting the funding inventory.
      exchangeInventory = before + 5000000n - 50n
      await expect.poll(() => amount(page, exchange.contractAddress, gas), poll).toBe(exchangeInventory)
    })

    await check(requiredPwaToolsCases[1], async () => {
      buyer = await importWallet(fixture.recipient)
      const deployerPage = page
      page = buyer
      await toolsTab('Faucet')
      await fill('Contract address', exchange.contractAddress)
      await fill('Sats amount', '1000')
      const before = await amount(page, fixture.recipient.address, gas)
      assert.equal(await amount(page, exchange.contractAddress, gas), exchangeInventory)
      await panel().getByRole('button', { name: 'Send sats', exact: true }).click()
      await expect(page.getByRole('dialog')).toContainText(exchange.contractAddress)
      await page.getByRole('dialog').getByRole('button', { name: 'Cancel', exact: true }).click()
      assert.equal(await amount(page, fixture.recipient.address, gas), before)
      await panel().getByRole('button', { name: 'Send sats', exact: true }).click()
      await confirmTools([exchange.contractAddress, '1000', 'testnet'])
      const receipt = panel().locator('p').filter({ hasText: /^txid: [0-9a-f]{64}$/ })
      await expect(receipt).toHaveCount(1, { timeout: 120000 })
      const work = await confirmedWork((await receipt.textContent()).trim().slice(6))
      const funding = contractFunding(work, exchange.contractAddress)
      assert.equal(funding.length, 1)
      assert.equal(funding[0].contract.value, 1000, 'Faucet changed the requested sats amount')
      assert.equal(gasInOutputs(funding), 0n, 'Faucet is a plain-sats default invoke')
      const settled = gasOutputs(work, fixture.recipient.pk_script)
      // A default invoke without GAS retains 1% of native input: 1000-10
      // sats buy exactly 990,000 GAS at the configured 0.001 sats/GAS.
      assert.equal(settled, 990000n, 'Faucet settlement must honor price and default-invoke retention')
      await expect.poll(() => amount(page, fixture.recipient.address, gas), poll).toBe(before + 990000n)
      exchangeInventory -= 990000n
      await expect.poll(() => amount(page, exchange.contractAddress, gas), poll).toBe(exchangeInventory)
      await openContract(exchange.contractAddress)
      // Plain-sats funding has no explicit invoke op. Mainline indexes its
      // canonical Result; confirmedWork already proves it consumes this work.
      assert.equal((work.contractOps || []).filter(op => op.kind === 'invoke').length, 0)
      for (const result of work.results) {
        await historyThroughUI(exchange.contractAddress, result.txid, 'result')
        await openContract(exchange.contractAddress)
      }
      page = deployerPage
    })

    await check(requiredPwaToolsCases[2], async () => {
      await openContract(exchange.contractAddress)
      const before = await amount(page, actor.address, gas)
      assert.equal(await amount(page, exchange.contractAddress, gas), exchangeInventory)
      const work = await submitInvoke('close')
      const returned = gasOutputs(work, actor.pk_script)
      const closeFunding = gasInOutputs(contractFunding(work, exchange.contractAddress))
      assert.ok(closeFunding >= 50n, 'close must fund its Result fee')
      assert.equal(returned, exchangeInventory + closeFunding - 50n,
        'close must return the exact inventory and unused call funding after one Result fee')
      await expect.poll(() => amount(page, exchange.contractAddress, gas), poll).toBe(0n)
      const spent = await actorGasSpent(work)
      const change = gasInOutputs(work.vout.filter(output => output.scriptPubKey.hex === actor.pk_script))
      await expect.poll(() => amount(page, actor.address, gas), poll).toBe(before - spent + change + returned)
      await historyThroughUI(exchange.contractAddress, work.txid)
    })
    } catch (error) { failures.push(error) }

    try {
    await check(requiredPwaToolsCases[3], async () => {
      page = await importWallet(actor)
      await deployForm('EVM', 'Standard EVM AMM')
      await fill('Solidity source', fixture.tools.evmSource, 'textarea')
      await fill('Contract name', fixture.tools.evmContractName)
      await fill('Constructor args JSON', JSON.stringify([gas, fixture.asset.key]))
      await panel().getByRole('button', { name: 'Generate init code', exact: true }).click()
      await expect(panel().getByText(`Compiled contract: ${fixture.tools.evmContractName}`, { exact: true })).toBeVisible({ timeout: 120000 })
      await expect(panel().getByText(/^Browser compiler version: 0\.8\./)).toBeVisible()
      await expect(field('Init code hex').locator('textarea')).toHaveValue(/^0x[0-9a-f]+$/i)
      evm = await submitDeploy('Standard EVM AMM')
      assert.equal(evm.sourceVerification?.verified, false, 'local compiler output must not claim node source verification')
      await expect.poll(async () => findField(await contractRead('getContractState', evm.contractAddress), 'custom')?.counter, poll).toBe(0)
      await openContract(evm.contractAddress)
      await historyThroughUI(evm.contractAddress, evm.txid)
    })

    await check(requiredPwaToolsCases[4], async () => {
      await openContract(evm.contractAddress)
      await select(field('Action').getByRole('combobox'), 'call')
      await fill('Call JSON', JSON.stringify({ function: 'inc()', args: [], sats: '0', funding: [] }), 'textarea')
      await panel().getByRole('button', { name: 'Generate calldata', exact: true }).click()
      await expect(panel().getByText('Calldata is generated from the current JSON.', { exact: true })).toBeVisible()
      await expect(field('Calldata preview').locator('pre')).toHaveText(/^0x[0-9a-f]{8}$/i)
      // Do not reselect the action after generation: changing the action resets
      // the form and correctly invalidates the reviewed calldata.
      await panel().getByRole('button', { name: 'Sign and Broadcast', exact: true }).click()
      await confirmTools([actor.address, evm.contractAddress, 'inc()', 'testnet'])
      const receipt = panel().locator('p').filter({ hasText: /^txid: [0-9a-f]{64}$/ })
      await expect(receipt).toHaveCount(1, { timeout: 120000 })
      const work = await confirmedWork((await receipt.textContent()).trim().slice(6))
      await expect.poll(async () => findField(await contractRead('getContractState', evm.contractAddress), 'custom')?.counter, poll).toBe(1)
      const stateResponse = page.waitForResponse(response => response.url().includes(`/v3/contracts/${evm.contractAddress}/state`))
      await panel().getByText('Real-time Status', { exact: true }).locator('..').getByRole('button').click()
      const refreshed = await (await stateResponse).json()
      assert.equal(refreshed.code, 0)
      assert.equal(findField(refreshed.data, 'custom')?.counter, 1, 'UI refresh did not query the committed EVM state')
      await historyThroughUI(evm.contractAddress, work.txid)
    })
    } catch (error) { failures.push(error) }

    try {
    await check(requiredPwaToolsCases[5], async () => {
      // This is explicitly the form/signing boundary. A successful prediction
      // deployment/resolution requires a real oracle; no model response is
      // mocked and this case does not claim that oracle path is accepted.
      page = await importWallet(actor)
      await deployForm('Agent', 'Prediction')
      const snapshot = await control('snapshot')
      await fill('Title', 'Isolated PWA prediction form')
      await fill('Description', 'At the event height, outcome A means the published value is one; outcome B means zero.', 'textarea')
      await select(field('Time type').getByRole('combobox'), 'Block height')
      await fill('Event time', snapshot.height + 30)
      await fill('Bet deadline', snapshot.height + 20)
      await fill('Confirm after', snapshot.height + 40)
      await fill('Source URL', 'file:///pwa-test-source.html')
      await assetField('Bet asset', '::')
      await fill('Minimum bet unit', '1000')
      const outcomes = field('Display text').locator('input')
      await expect(outcomes).toHaveCount(2)
      await outcomes.nth(0).fill('Value one')
      await outcomes.nth(1).fill('Value zero')
      const beforeGas = await amount(page, actor.address, gas)
      const beforeLogs = await logs()
      await panel().getByRole('button', { name: 'Deploy Smart Contract', exact: true }).click()
      const error = panel().locator('pre').filter({ hasText: 'Prediction source URL must use http or https' })
      await expect(error).toHaveCount(1)
      const result = JSON.parse(await error.textContent())
      assert.equal(result.stage, 'error')
      assert.equal(result.txid, undefined)
      await expect(page.getByRole('dialog')).toHaveCount(0)
      assert.equal(await amount(page, actor.address, gas), beforeGas)
      const previous = new Set(beforeLogs.map(log => log.id))
      const deployments = (await logs()).filter(log => !previous.has(log.id) && /deploy.*contract|contract.*deploy/.test(log.action))
      assert.deepEqual(deployments, [], 'invalid source reached a signing/broadcast operation')
      const after = await control('snapshot')
      assert.deepEqual(after.l1.broadcast_count, snapshot.l1.broadcast_count)
    })
    } catch (error) { failures.push(error) }

    try {
    await check(requiredPwaToolsCases[6], async () => {
      // The earlier close case consumed its Exchange. Deploy a fresh one
      // through the real form for both full and focused query acceptance.
      await ensureExchange(true)

      const before = await control('snapshot')
      const supported = await walletCall(page, 'getSupportedContracts')
      assert.ok(Array.isArray(supported.contractContents) && supported.contractContents.length)
      const serverQuery = async path => {
        const response = await fetch(`${fixture.coreSTPURL}${path}`)
        assert.equal(response.ok, true, `Core STP ${path}: HTTP ${response.status}`)
        const data = await response.json()
        assert.equal(data.code, 0, data.msg)
        return data
      }
      assert.deepEqual(supported.contractContents, (await serverQuery('/info/contracts/support')).contracts,
        'WASM service catalog must match the actual Core STP catalog')

      const exchangeContent = await walletCall(page, 'buildUnifiedContractContent', 'template', 'exchange.tc', JSON.stringify({
        assetAName: gas,
        assetBName: '::',
        priceMode: 'sold_a',
        steps: [{ threshold: '0', bPerA: '0.001' }],
      }))
      assert.equal(exchangeContent.contentEncoding, 'base64')
      const estimateRequest = {
        ContractType: 'template', SubType: 'exchange.tc', DeployNonce: 987654321,
        ContractContent: exchangeContent.content, ContentEncoding: exchangeContent.contentEncoding,
      }
      const estimate = await walletCall(page, 'estimateDeployUnifiedContract', estimateRequest)
      const repeatedEstimate = await walletCall(page, 'estimateDeployUnifiedContract', estimateRequest)
      assert.deepEqual(repeatedEstimate, estimate, 'same deployment request did not produce a stable estimate')
      assert.equal(estimate.contractType, 'template')
      assert.equal(estimate.caller, actor.address)
      assert.match(estimate.contractAddress, /^\w+$/)
      assert.ok(BigInt(estimate.gasAssetAmount) >= BigInt(estimate.gasFeeAmount))
      assert.equal(estimate.txid, '', 'estimate must not broadcast a transaction')

      const invokeParams = await walletCall(page, 'getParamForInvokeUnifiedContract', 'template', 'exchange.tc', 'exchange')
      const invokeTemplate = JSON.parse(invokeParams.parameter)
      assert.equal(invokeTemplate.action, 'exchange')
      assert.equal(JSON.parse(invokeTemplate.param).minOutA, '')
      const feeRequest = { ContractType: 'template', SubType: 'exchange.tc',
        ContractAddress: exchange.contractAddress, Action: 'exchange', Param: JSON.stringify({ minOutA: '0' }) }
      const invokeFee = await walletCall(page, 'getFeeForInvokeUnifiedContract', feeRequest)
      assert.ok(BigInt(invokeFee.fee) > 0n)
      assert.deepEqual(await walletCall(page, 'getFeeForInvokeUnifiedContract', feeRequest), invokeFee)

      const directList = await contractQuery({ ContractType: 'template', Query: 'list', Start: 0, Limit: 100 })
      const restListResponse = await page.evaluate(async () => {
        const api = (await import('/apis/smartcontract.ts')).default
        return api.getContracts({ network: 'testnet', start: 0, limit: 100 })
      })
      assert.equal(restListResponse.code, 0, restListResponse.msg)
      assert.deepEqual(directList, restListResponse.data, 'WASM contract list disagrees with canonical indexer API')
      assert.ok(JSON.stringify(directList).includes(exchange.contractAddress), 'deployed Exchange is absent from direct WASM list query')

      const directInfo = await contractQuery({ ContractType: 'template', Query: 'info', Contract: exchange.contractAddress })
      assert.deepEqual(directInfo, await contractRead('getContract', exchange.contractAddress))
      const directState = await contractQuery({ ContractType: 'template', Query: 'state', Contract: exchange.contractAddress })
      assert.deepEqual(directState, await contractRead('getContractState', exchange.contractAddress))
      const directHistory = await contractQuery({
        ContractType: 'template', Query: 'history', Contract: exchange.contractAddress, Start: 0, Limit: 100,
      })
      const restHistory = await contractRead('getContractHistory', exchange.contractAddress)
      assert.deepEqual(directHistory, restHistory, 'WASM history disagrees with canonical indexer history')
      assert.ok(JSON.stringify(directHistory).includes(exchange.txid), 'deployment transaction is absent from contract history')

      const serverContracts = await walletCall(page, 'getDeployedContractsInServer')
      assert.ok(Array.isArray(serverContracts.contractURLs) && serverContracts.contractURLs.length,
        'Core has no deployed service contract to inspect')
      const serverURL = fixture.publicContracts['::']
      assert.ok(serverURL && serverContracts.contractURLs.includes(serverURL), 'the confirmed fixture service contract is missing')
      const status = await walletCall(page, 'getDeployedContractStatus', serverURL)
      assert.ok(typeof status.contractStatus === 'string' && status.contractStatus.length > 0)
      const parsedStatus = JSON.parse(status.contractStatus)
      assert.ok(parsedStatus && typeof parsedStatus === 'object', 'Core contract status is not valid JSON')
      assert.deepEqual(parsedStatus, JSON.parse((await serverQuery(`/info/contract/${encodeURIComponent(serverURL)}`)).status))
      const serverHistory = await walletCall(page, 'getContractInvokeHistoryInServer', serverURL, 0, 100)
      assert.ok(typeof serverHistory.history === 'string' && serverHistory.history.length > 0)
      const history = JSON.parse(serverHistory.history)
      assert.deepEqual(history, JSON.parse((await serverQuery(`/info/contract/history/${encodeURIComponent(serverURL)}?start=0&limit=100`)).status))
      const addressHistory = await walletCall(page, 'getContractInvokeHistoryByAddressInServer', serverURL, actor.address, 0, 100)
      assert.deepEqual(JSON.parse(addressHistory.history), JSON.parse((await serverQuery(`/info/contract/userhistory/${encodeURIComponent(serverURL)}/${actor.address}?start=0&limit=100`)).status))
      // Analytics is a public WASM export without a PWA convenience wrapper.
      const analytics = await page.evaluate(async url => {
        const response = await window.sat20wallet_wasm.getDeployedContractAnalytics(url)
        if (response.code !== 0) throw new Error(response.msg)
        return response.data
      }, serverURL)
      assert.deepEqual(JSON.parse(analytics.Analytics), JSON.parse((await serverQuery(`/info/contract/analytics/${encodeURIComponent(serverURL)}`)).status))
      const addresses = await walletCall(page, 'getAllAddressInContract', serverURL, 0, 100)
      assert.equal(addresses.addresses, (await serverQuery(`/info/contract/alluser/${encodeURIComponent(serverURL)}?start=0&limit=100`)).status)
      const addressStatus = await walletCall(page, 'getAddressStatusInContract', serverURL, actor.address)
      assert.deepEqual(JSON.parse(addressStatus.status), JSON.parse((await serverQuery(`/info/contract/user/${encodeURIComponent(serverURL)}/${actor.address}`)).status))

      const after = await control('snapshot')
      assert.deepEqual(after.l1.broadcast_count, before.l1.broadcast_count, 'read/estimate APIs broadcast to L1')
      assert.deepEqual(after.l1.pending_txids.filter(txid => !before.l1.pending_txids.includes(txid)), [])
      assert.deepEqual(after.mempool_txids.filter(txid => !before.mempool_txids.includes(txid)), [],
        'read/estimate APIs broadcast to SatoshiNet')
    })
    } catch (error) { failures.push(error) }

    try {
    await check(requiredPwaToolsCases[7], async () => {
      await ensureExchange()
      const before = await control('snapshot')
      await assert.rejects(walletCall(page, 'estimateDeployUnifiedContract', {
        ContractType: 'template', SubType: 'not-a-template.tc', DeployNonce: 987654322,
        ContractContent: 'e30=', ContentEncoding: 'base64',
      }), /template|contract|unknown|unsupported/i)
      await assert.rejects(walletCall(page, 'getParamForInvokeUnifiedContract', 'template', 'not-a-template.tc', 'exchange'),
        /template|contract|unknown|unsupported/i)
      await assert.rejects(walletCall(page, 'invokeUnifiedContract', {
        ContractType: 'template', SubType: 'exchange.tc', ContractAddress: exchange.contractAddress,
        Action: 'not-a-supported-action',
      }), /action|template|contract|unsupported/i)
      await assert.rejects(walletCall(page, 'queryContract', { Query: 'unsupported' }), /unsupported contract query/i)
      const after = await control('snapshot')
      assert.deepEqual(after.l1.broadcast_count, before.l1.broadcast_count)
      assert.deepEqual(after.l1.pending_txids.filter(txid => !before.l1.pending_txids.includes(txid)), [])
      assert.deepEqual(after.mempool_txids.filter(txid => !before.mempool_txids.includes(txid)), [],
        'invalid contract requests broadcast to SatoshiNet')
    })
    } catch (error) { failures.push(error) }
    if (failures.length) throw new AggregateError(failures, 'Tools acceptance groups failed')
  } finally {
    for (const target of pages) await target.context().close().catch(() => {})
  }
}
