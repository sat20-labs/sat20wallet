import assert from 'node:assert/strict'
import { expect } from '@playwright/test'
import { address as bitcoinAddress, networks, Transaction, initEccLib } from 'bitcoinjs-lib'
import * as secp256k1 from '@bitcoin-js/tiny-secp256k1-asmjs'
initEccLib(secp256k1)

// Launched by the existing PWA runner and SDK virtual network. Only the L1
// indexer is a test ledger. Every wallet operation below starts with a UI click;
// SDK reads and the loopback Go controller supply independent chain evidence.
export const requiredPwaPosCases = [
  'POS PWA: opening confirms the signed Anchor after pending reload',
  'POS PWA: BTC splicing-in survives closing and reopening the page',
  'POS PWA: a drained activation preserves the existing private channel',
  'POS PWA: v2 asset splicing-in preserves quantities and signed outpoints',
  'POS PWA: v2 asset deposit reaches the wallet through the public channel',
  'POS PWA: v2 BTC deposit reaches the wallet through the public channel',
  'POS PWA: scheduled producer rotation preserves wallet state',
  'POS PWA: offline Miner substitution confirms a PWA deposit',
  'POS PWA: a restored Miner and normal Core restart preserve wallet state',
  'Funds PWA: Bitcoin Send confirms the requested BTC at the recipient',
  'Funds PWA: Bitcoin Send confirms the requested ORDX at the recipient',
  'Funds PWA: Bitcoin advanced Send creates two reviewed BTC outputs',
  'Funds PWA: SatoshiNet Send confirms the requested BTC at the recipient',
  'Funds PWA: SatoshiNet Send confirms the requested ORDX at the recipient',
  'Funds PWA: private BTC unlock and lock restore the channel balance',
  'Funds PWA: private ORDX unlock and lock restore the channel balance',
  'Funds PWA: private BTC splicing-out returns confirmed Bitcoin funds',
  'Funds PWA: private ORDX splicing-out returns confirmed Bitcoin assets',
  'Funds PWA: public BTC withdrawal returns confirmed Bitcoin funds',
  'Funds PWA: public ORDX withdrawal returns confirmed Bitcoin assets',
  'Escape PWA: current commitment inputs and outputs match the SDK safety snapshot',
  'Escape PWA: cancelling cooperative close preserves the channel and funds',
  'Escape PWA: force-close safety confirmation displays evidence and cancels safely',
  'Escape PWA: cooperative close returns confirmed BTC and ORDX to Bitcoin',
  'Funds PWA: Bitcoin advanced ORDX Send creates two reviewed asset outputs',
]
// Destructive channel closure uses the same fixture in a separate selected
// run, since cooperative-close and force-close cannot share one channel.
export const optionalPwaEscapeCases = [
  'Escape PWA: confirmed force close waits for CSV and returns exact wallet funds after reload',
]

const pollOptions = { timeout: 180000, intervals: [250, 500, 1000, 2000] }
const txidPattern = /^[0-9a-f]{64}$/
const asInteger = value => {
  assert.ok(typeof value === 'string' || typeof value === 'number', 'expected an exact integer amount')
  if (typeof value === 'number') assert.ok(Number.isSafeInteger(value), 'unsafe numeric amount')
  assert.match(String(value), /^\d+$/, 'invalid integer amount')
  return BigInt(value)
}
const assetKey = name => `${name.Protocol}:${name.Type}:${name.Ticker}`
const channelAmount = (channel, key) => (channel.localbalanceL1 || [])
  .filter(asset => assetKey(asset.Name) === key)
  .reduce((sum, asset) => sum + asInteger(asset.Amount), 0n)
const channelBalances = channel => (channel.localbalanceL1 || [])
  .map(asset => ({ key: assetKey(asset.Name), amount: String(asset.Amount), binding: asset.BindingSat }))
  .sort((a, b) => `${a.key}:${a.binding}`.localeCompare(`${b.key}:${b.binding}`))
const btcText = sats => `${(Number(sats) / 1e8).toFixed(8)} tBTC`
const deanchorInputs = channel => Object.fromEntries(['localDeAnchorTx', 'remoteDeAnchorTx'].map(side => {
  const inputs = channel[side]?.TxIn
  assert.ok(inputs?.length, `${side} must exist before the L1 funding confirms`)
  return [side, inputs.map(input => `${input.PreviousOutPoint.Hash}:${input.PreviousOutPoint.Index}`).sort()]
}))

export async function runPwaPosCases(t, fixture) {
  const { check, device, ready, walletCall } = t
  assert.equal(fixture.config.Chain, 'testnet')
  const controller = new URL(fixture.control_url)
  assert.equal(controller.protocol, 'http:')
  assert.ok(['127.0.0.1', 'localhost'].includes(controller.hostname), 'controller must be loopback')
  assert.equal(fixture.asset.key, `ordx:f:${fixture.asset.ticker}`)
  assert.equal(fixture.asset.type, 'ORDX')
  const amounts = fixture.amounts
  for (const name of ['opening', 'splicing_btc', 'splicing_asset', 'deposit_btc', 'deposit_asset']) {
    assert.ok(asInteger(amounts[name]) > 0n, `${name} must be positive`)
  }
  const walletScript = bitcoinAddress.toOutputScript(fixture.address, networks.testnet).toString('hex')
  assert.notEqual(fixture.recipient.address, fixture.address, 'transfers require an independent recipient')
  const recipientScript = bitcoinAddress.toOutputScript(fixture.recipient.address, networks.testnet).toString('hex')
  assert.equal(recipientScript, fixture.recipient.pk_script)
  let page = await device()
  const context = page.context()
  let channelId
  let latestDeposit

  const control = async (action, body = {}) => {
    const response = await fetch(new URL(`/${action}`, controller), {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body), signal: AbortSignal.timeout(240000),
    })
    const text = await response.text()
    // A freshly submitted real L2 transaction may not have propagated yet.
    // Only this read endpoint permits 404; failed controller actions still fail.
    if (action === 'transaction' && response.status === 404) return null
    assert.ok(response.ok, `${action}: HTTP ${response.status}: ${text}`)
    const result = JSON.parse(text)
    assert.ok(!result.error, `${action}: ${result.error}`)
    return result
  }
  const stpRead = async (method, ...args) => {
    assert.ok(['getCurrentChannel', 'getChannelStatus', 'previewOpenChannel', 'allReservations',
      'safetySnapshot', 'getCommitTxAssetInfo', 'commitmentExport', 'forceClosePlan', 'sweepBuild', 'punishStatus'].includes(method), 'test may only read STP state')
    const read = page.evaluate(async ({ method, args }) => {
      const stp = (await import('/utils/stp.ts')).default
      const [error, result] = await stp[method](...args)
      if (error) throw new Error(`${method}: ${error.message}`)
      return result
    }, { method, args })
    // Bound even a blocked WASM callback; browser-side timers cannot run when
    // its event loop is stuck. A timeout remains a failure of this case.
    let timeout
    try {
      return await Promise.race([read, new Promise((_, reject) => {
        timeout = setTimeout(() => reject(new Error(`${method}: browser SDK read timed out after 30s`)), 30000)
      })])
    } finally {
      clearTimeout(timeout)
    }
  }
  const readChannel = async () => JSON.parse((await stpRead('getCurrentChannel')).json)
  const readL2Amount = async key => {
    const result = await walletCall(page, 'getAssetAmount_SatsNet', fixture.address, key)
    return asInteger(result.availableAmt) + asInteger(result.lockedAmt)
  }
  const readL2Total = async () => {
    const summary = await page.evaluate(async address => {
      const api = (await import('/apis/satnet.ts')).default
      return api.getAddressSummary({ address, network: 'testnet' })
    }, fixture.address)
    assert.equal(summary.code, 0, summary.msg)
    assert.ok(Array.isArray(summary.data))
    const totals = summary.data.filter(asset => asset.Name.Protocol === '' && asset.Name.Type === '*')
    assert.ok(totals.length <= 1, 'duplicate native total row')
    return totals.length ? asInteger(totals[0].Amount) : 0n
  }
  const activePanel = () => page.locator('[role="tabpanel"][data-state="active"]')
  const balancePanel = () => page.getByText('TOTAL BALANCE', { exact: true }).locator('..')
  const showTab = async name => {
    const tab = page.getByRole('tab', { name, exact: true })
    await tab.click()
    await expect(tab).toHaveAttribute('aria-selected', 'true')
    await expect(activePanel()).toHaveCount(1)
  }
  const selectMode = async mode => {
    // HomeHeader's last button opens its Transcending Mode chooser. All choices
    // and business actions use actual rendered controls; no mode store mutation.
    await page.locator('header.mb-6').getByRole('button').last().click()
    await expect(page.getByRole('heading', { name: 'Transcending Mode', exact: true })).toBeVisible()
    await page.getByRole('button', { name: mode === 'lightning' ? 'Lightning' : 'Poolswap', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Transcending Mode', exact: true })).toBeHidden()
  }
  const assetRow = () => activePanel().locator('div.bg-muted.border')
    .filter({ has: page.getByText(fixture.asset.ticker.toUpperCase(), { exact: true }) })
  const assertAssetVisible = async amount => {
    await activePanel().getByRole('button', { name: fixture.asset.type, exact: true }).click()
    await expect(assetRow()).toHaveCount(1)
    // Fixture quantities stay below 1000 so this is an exact visible amount,
    // rather than only a rounded K/M suffix. SDK and chain amounts are exact.
    if (amount < 1000n) await expect(assetRow().locator('div.text-sm.font-semibold').first()).toHaveText(String(amount))
  }
  const assertNodes = (snapshot, expected = 3) => {
    assert.match(snapshot.hash, txidPattern)
    assert.equal(snapshot.nodes.length, expected, 'unexpected count of live fixture nodes')
    assert.equal(new Set(snapshot.nodes.map(node => node.role)).size, expected)
    for (const node of snapshot.nodes) {
      assert.equal(node.height, snapshot.height, `${node.role} canonical height differs`)
      assert.equal(node.hash, snapshot.hash, `${node.role} canonical hash differs`)
      assert.equal(node.api_height, snapshot.height, `${node.role} index API has not caught up`)
    }
  }
  const assertIdentity = async () => {
    const identity = await page.evaluate(() => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      return { address: wallet.address, network: wallet.network, locked: wallet.locked }
    })
    assert.deepEqual(identity, { address: fixture.address, network: 'testnet', locked: false })
  }
  const unlockThroughUI = async () => {
    await expect.poll(() => new URL(page.url()).hash.startsWith('#/unlock'), pollOptions).toBe(true)
    await page.locator('form input[type="password"]').fill(fixture.password)
    await page.locator('form button[type="submit"]').click()
    await expect.poll(() => page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), pollOptions).toBe(false)
    await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible()
    await assertIdentity()
  }
  const reopen = async closePage => {
    if (closePage) {
      await page.close()
      page = await device(context)
    } else {
      await page.reload()
      await ready(page)
    }
    await unlockThroughUI()
  }
  const readyChannelUI = async () => {
    await expect.poll(async () => (await readChannel()).status, pollOptions).toBe(16)
    const channel = await readChannel()
    assert.equal(channel.channelId, channelId)
    await showTab('Channel')
    await expect.poll(() => page.evaluate(async () => {
      const store = (await import('/store/channel.ts')).useChannelStore()
      return store.channel?.status
    }), pollOptions).toBe(16)
    await expect(activePanel().getByText('Channel is opening', { exact: true })).toBeHidden()
    await expect(activePanel().getByText('Splicing in', { exact: true })).toBeHidden()
    await expect(balancePanel().getByRole('heading', { level: 2 })).toHaveText(btcText(channelAmount(channel, '::')))
    return channel
  }
  const pendingFunding = async before => {
    let snapshot
    await expect.poll(async () => {
      snapshot = await control('snapshot')
      return snapshot.l1.pending_txids.length
    }, pollOptions).toBe(1)
    const txid = snapshot.l1.pending_txids[0]
    assert.match(txid, txidPattern)
    assert.ok(!before.l1.confirmed_txids.includes(txid), 'new UI operation reused already confirmed funding')
    assert.deepEqual([...snapshot.l1.confirmed_txids].sort(), [...before.l1.confirmed_txids].sort(), 'L1 confirmed without the test confirmation boundary')
    return { txid, confirmed: [...before.l1.confirmed_txids].sort() }
  }
  const assertSamePending = async funding => {
    const snapshot = await control('snapshot')
    assert.deepEqual([...snapshot.l1.pending_txids].sort(), [funding.txid], 'page recovery changed or duplicated funding')
    assert.deepEqual([...snapshot.l1.confirmed_txids].sort(), funding.confirmed)
  }
  const confirmFunding = async (funding, expectedNodes = 3, v2 = false) => {
    const snapshot = await control('confirm-l1')
    assertNodes(snapshot, expectedNodes)
    assert.deepEqual(snapshot.l1.pending_txids, [])
    const added = snapshot.l1.confirmed_txids.filter(txid => !funding.confirmed.includes(txid))
    assert.deepEqual(added, [funding.txid], 'confirmation must consume only the operation just submitted')
    const anchors = snapshot.anchors.filter(anchor => anchor.funding_outpoint.startsWith(`${funding.txid}:`))
    assert.equal(anchors.length, 1, 'funding must have exactly one actual confirmed Anchor')
    const anchor = anchors[0]
    assert.match(anchor.txid, txidPattern)
    assert.ok(anchor.height > 0 && anchor.height <= snapshot.height)
    assert.ok(!snapshot.mempool_txids.includes(anchor.txid), 'Anchor remains unconfirmed in the mempool')
    if (v2) {
      assert.ok(anchor.height >= fixture.activation_height, 'v2 case ran before activation')
      assert.equal(anchor.version, 1)
      assert.equal(anchor.lock_time, 0)
      assert.equal(anchor.sequence, 0xfffffffe)
    } else assert.ok(anchor.height < fixture.activation_height, 'legacy case accidentally crossed activation')
    console.log(JSON.stringify({ posFunding: funding.txid, anchor: anchor.txid, height: anchor.height }))
    return { snapshot, anchor }
  }
  const assertSignedAnchor = (signed, anchor, current) => {
    const outpoint = `${anchor.txid}:0`
    for (const side of ['localDeAnchorTx', 'remoteDeAnchorTx']) {
      assert.ok(signed[side].includes(outpoint), `${side} was signed for a different Anchor txid`)
      assert.ok(deanchorInputs(current)[side].includes(outpoint), `${side} lost the confirmed Anchor reference`)
    }
  }
  const openAssetOperation = async (operation, token, tab = 'Bitcoin') => {
    await showTab(tab)
    if (token) {
      await activePanel().getByRole('button', { name: fixture.asset.type, exact: true }).click()
      await expect(assetRow()).toHaveCount(1)
      await assetRow().getByRole('button', { name: operation, exact: true }).click()
    } else await balancePanel().getByRole('button', { name: operation, exact: true }).click()
    const dialog = page.getByRole('dialog')
    const titles = {
      Deposit: 'Deposit Asset', Withdraw: 'Withdraw Asset', Send: 'Send Asset',
      Lock: 'Lock Asset', Unlock: 'Unlock Asset', 'Splicing in': 'Splicing In', 'Splicing out': 'Splicing Out',
    }
    await expect(dialog.getByRole('heading', { name: titles[operation], exact: true })).toBeVisible()
    return dialog
  }
  const clickAssetOperation = async (operation, amount, token, tab = 'Bitcoin') => {
    const dialog = await openAssetOperation(operation, token, tab)
    if (operation === 'Send') await dialog.getByRole('button', { name: 'Normal', exact: true }).click()
    await dialog.getByPlaceholder('Enter amount', { exact: true }).fill(String(amount))
    await expect(dialog.getByPlaceholder('Enter amount', { exact: true })).toHaveValue(String(amount))
    if (token) await expect(dialog).toContainText(fixture.asset.ticker.toUpperCase())
    if (operation === 'Send') await dialog.getByPlaceholder('Enter address', { exact: true }).fill(fixture.recipient.address)
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await expect(dialog.getByRole('heading', { name: 'Please Confirm', exact: true })).toBeVisible()
    if (operation === 'Send') {
      const review = dialog.locator('dl')
      await expect(review.getByText(String(amount) + ' ' + (token ? fixture.asset.ticker.toUpperCase() : 'sats'), { exact: true })).toBeVisible()
      await expect(review.getByText(fixture.recipient.address, { exact: true })).toBeVisible()
      await expect(review.getByText(tab, { exact: true })).toBeVisible()
      await expect(review.getByText('testnet', { exact: true })).toBeVisible()
    }
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await expect(dialog).toBeHidden()
  }
  const deposit = async (token, expectedNodes = 3) => {
    await selectMode('poolswap')
    const key = token ? fixture.asset.key : '::'
    const beforeAmount = await readL2Amount(key)
    const beforeTotal = await readL2Total()
    const before = await control('snapshot')
    assert.deepEqual(before.l1.pending_txids, [])
    await clickAssetOperation('Deposit', token ? amounts.deposit_asset : amounts.deposit_btc, token)
    const funding = await pendingFunding(before)
    const confirmed = await confirmFunding(funding, expectedNodes, true)
    const outputs = confirmed.anchor.outputs.filter(output => output.pk_script.toLowerCase() === walletScript)
    assert.ok(outputs.length, 'public-channel Anchor has no output to the PWA wallet')
    const credited = token
      ? outputs.flatMap(output => output.assets || []).filter(asset => assetKey(asset.Name) === key)
        .reduce((sum, asset) => {
          assert.equal(asset.Amount?.Precision, 0, 'fixture Anchor asset precision must be zero')
          assert.equal(typeof asset.Amount.Value, 'string', 'Anchor asset value must be an exact string')
          return sum + asInteger(asset.Amount.Value)
        }, 0n)
      : outputs.filter(output => !(output.assets || []).length).reduce((sum, output) => sum + asInteger(output.value), 0n)
    const expectedCredit = asInteger(token ? amounts.deposit_asset : amounts.deposit_btc)
    assert.equal(credited, expectedCredit, 'deposit credit must equal the user-confirmed amount; deposit service fee is zero')
    const creditedSats = outputs.reduce((sum, output) => sum + asInteger(output.value), 0n)
    await expect.poll(() => readL2Amount(key), pollOptions).toBe(beforeAmount + credited)
    await expect.poll(readL2Total, pollOptions).toBe(beforeTotal + creditedSats)
    await showTab('SatoshiNet')
    await expect.poll(() => page.evaluate(async () => {
      const store = (await import('/store/l2.ts')).useL2Store()
      return String(store.totalSats)
    }), pollOptions).toBe(String(beforeTotal + creditedSats))
    await expect(balancePanel().getByRole('heading', { level: 2 })).toHaveText(btcText(beforeTotal + creditedSats))
    if (token) {
      await expect.poll(() => page.evaluate(async key => {
        const store = (await import('/store/l2.ts')).useL2Store()
        return store.sat20List.filter(asset => asset.key === key)
          .reduce((sum, asset) => sum + BigInt(String(asset.amount)), 0n).toString()
      }, key), pollOptions).toBe(String(beforeAmount + credited))
      await assertAssetVisible(beforeAmount + credited)
    }
    latestDeposit = { funding, anchor: confirmed.anchor, key, amount: beforeAmount + credited }
    return confirmed
  }

  const readSummary = async (chain, address) => {
    const response = await page.evaluate(async ({ chain, address }) => {
      const api = chain === 'Bitcoin'
        ? (await import('/apis/ordx.ts')).default : (await import('/apis/satnet.ts')).default
      return api.getAddressSummary({ address, network: 'testnet' })
    }, { chain, address })
    assert.equal(response.code, 0, response.msg)
    assert.ok(Array.isArray(response.data))
    return response.data
  }
  const summaryAmount = (summary, key) => summary
    .filter(asset => key === '::'
      ? asset.Name.Protocol === '' && asset.Name.Type === '*' : assetKey(asset.Name) === key)
    .reduce((sum, asset) => sum + asInteger(asset.Amount), 0n)
  const addressAmount = async (chain, address, key) => summaryAmount(await readSummary(chain, address), key)
  const assertWalletBalanceUI = async (chain, token = false) => {
    const summary = await readSummary(chain, fixture.address)
    const total = summaryAmount(summary, '::')
    // Switching tabs triggers the same refresh as a returning wallet user.
    await showTab(chain === 'Bitcoin' ? 'SatoshiNet' : 'Bitcoin')
    await showTab(chain)
    await expect(balancePanel().getByRole('heading', { level: 2 })).toHaveText(btcText(total), { timeout: 90000 })
    if (token) await assertAssetVisible(summaryAmount(summary, fixture.asset.key))
  }
  const operationLogs = async () => {
    const response = await page.evaluate(() => window.sat20wallet_operation_log.getOperationLogs())
    assert.equal(response.code, 0, response.msg)
    const logs = JSON.parse(response.data.logs)
    assert.ok(Array.isArray(logs))
    return logs
  }
  const newOperationTx = async (action, beforeLogs) => {
    const oldIDs = new Set(beforeLogs.map(log => log.id))
    let receipt
    await expect.poll(async () => {
      const added = (await operationLogs()).filter(log => log.action === action && !oldIDs.has(log.id))
      assert.ok(added.length <= 1, 'one UI confirmation created multiple operation receipts')
      receipt = added[0]
      if (receipt?.status === 'failed') throw new Error(JSON.stringify(receipt))
      return receipt?.status === 'succeeded' && txidPattern.test(receipt.txid || '')
    }, pollOptions).toBe(true)
    return receipt.txid
  }
  const readL1Transaction = async txid => {
    const response = await page.evaluate(async txid => {
      const api = (await import('/apis/ordx.ts')).default
      return api.getTxRaw({ txid, network: 'testnet' })
    }, txid)
    assert.equal(response.code, 0, response.msg)
    assert.match(response.data, /^[0-9a-f]+$/i)
    const tx = Transaction.fromHex(response.data)
    assert.equal(tx.getId(), txid)
    assert.ok(tx.ins.length > 0 && tx.ins.every(input => input.witness.length > 0), 'L1 transfer lacks signed inputs')
    return tx
  }
  const confirmL1Payment = async funding => {
    const snapshot = await control('confirm-l1', { wait_anchors: false })
    assert.ok(snapshot.l1.confirmed_txids.includes(funding.txid))
    assert.deepEqual(snapshot.l1.pending_txids, [])
    assert.deepEqual(snapshot.l1.confirmed_txids.filter(txid => !funding.confirmed.includes(txid)), [funding.txid])
    return readL1Transaction(funding.txid)
  }
  const confirmedL2Transaction = async txid => {
    assert.match(txid, txidPattern)
    let tx
    await expect.poll(async () => {
      tx = await control('transaction', { txid })
      return tx !== null
    }, pollOptions).toBe(true)
    if (!tx.confirmations) await control('mine')
    await expect.poll(async () => {
      tx = await control('transaction', { txid })
      return (tx?.confirmations || 0) >= 1
    }, pollOptions).toBe(true)
    assert.equal(tx.txid, txid)
    assert.match(tx.blockhash, txidPattern)
    assert.ok(Array.isArray(tx.vout) && tx.vout.length > 0)
    return tx
  }
  const rpcOutputs = tx => tx.vout.map(output => {
    assert.equal(typeof output.value, 'number')
    const sats = Math.round(output.value * 1e8)
    assert.ok(Math.abs(output.value * 1e8 - sats) < 0.000001, 'RPC BTC value contains fractional sats')
    return { value: asInteger(sats), script: output.scriptPubKey.hex, assets: output.Assets || [] }
  })
  const splitThroughUI = async (token, amount) => {
    const dialog = await openAssetOperation('Send', token)
    const advanced = dialog.getByRole('button', { name: 'Advanced', exact: true })
    // This is a normal availability requirement, including ORDX. A disabled
    // control is a failure; the test never enables it or bypasses the UI.
    await expect(advanced).toBeEnabled()
    await advanced.click()
    await dialog.getByPlaceholder('Enter amount', { exact: true }).fill(String(amount))
    await dialog.getByPlaceholder('Enter repeat times', { exact: true }).fill('2')
    await dialog.getByPlaceholder('Enter address', { exact: true }).fill(fixture.recipient.address)
    await dialog.getByRole('button', { name: 'Review split', exact: true }).click()
    const review = dialog.locator('section[aria-live="polite"]')
    await expect(review.getByRole('heading', { name: 'Confirm Asset Split', exact: true })).toBeVisible()
    await expect(review.getByText(fixture.recipient.address, { exact: true })).toBeVisible()
    await expect(review.getByText('Bitcoin / testnet', { exact: true })).toBeVisible()
    await expect(review.getByText(token ? fixture.asset.key : 'BTC (sats)', { exact: true })).toBeVisible()
    await expect(review.locator('dd').filter({ hasText: new RegExp('^' + String(amount) + (token ? '' : ' sats') + '$') })).toHaveCount(1)
    await expect(review.locator('dd').filter({ hasText: new RegExp('^' + String(amount * 2n) + (token ? '' : ' sats') + '$') })).toHaveCount(1)
    await review.getByRole('button', { name: 'Confirm', exact: true }).click()
  }
  const sendL1 = async (token, split = false) => {
    const key = token ? fixture.asset.key : '::'
    const amount = token ? 400n : (split ? 1000n : 2000n)
    const count = split ? 2n : 1n
    const before = await control('snapshot')
    assert.deepEqual(before.l1.pending_txids, [])
    const sourceBefore = await addressAmount('Bitcoin', fixture.address, key)
    const recipientBefore = await addressAmount('Bitcoin', fixture.recipient.address, key)
    const recipientSatsBefore = await addressAmount('Bitcoin', fixture.recipient.address, '::')
    const logs = await operationLogs()
    if (split) await splitThroughUI(token, amount)
    else await clickAssetOperation('Send', amount, token)
    const funding = await pendingFunding(before)
    const txid = await newOperationTx(split ? 'split_send_l1' : 'send_asset_l1', logs)
    assert.equal(txid, funding.txid)
    const tx = await confirmL1Payment(funding)
    const paid = tx.outs.filter(output => output.script.toString('hex') === recipientScript)
    assert.equal(paid.length, Number(count), 'recipient output count differs from the UI review')
    // PWAPOS has binding ratio 1, and these amounts exceed L1 dust.
    for (const output of paid) assert.equal(asInteger(output.value), amount)
    const creditSats = paid.reduce((sum, output) => sum + asInteger(output.value), 0n)
    await expect.poll(() => addressAmount('Bitcoin', fixture.recipient.address, key), pollOptions).toBe(recipientBefore + amount * count)
    assert.equal(await addressAmount('Bitcoin', fixture.recipient.address, '::'), recipientSatsBefore + creditSats)
    const sourceAfter = await addressAmount('Bitcoin', fixture.address, key)
    if (token) assert.equal(sourceAfter, sourceBefore - amount * count)
    else assert.ok(sourceAfter < sourceBefore - amount * count, 'sender did not pay the requested amount and a network fee')
    console.log(JSON.stringify({ pwaTransfer: txid, chain: 'Bitcoin', asset: key, amount: String(amount), outputs: Number(count) }))
    if (split) await reopen(false)
    await assertWalletBalanceUI('Bitcoin', token)
  }
  const sendL2 = async token => {
    const key = token ? fixture.asset.key : '::'
    const amount = token ? 50n : 2000n
    const sourceBefore = await addressAmount('SatoshiNet', fixture.address, key)
    const sourceSatsBefore = await readL2Total()
    const recipientBefore = await addressAmount('SatoshiNet', fixture.recipient.address, key)
    const recipientSatsBefore = await addressAmount('SatoshiNet', fixture.recipient.address, '::')
    const logs = await operationLogs()
    await clickAssetOperation('Send', amount, token, 'SatoshiNet')
    const txid = await newOperationTx('send_asset_l2', logs)
    const tx = await confirmedL2Transaction(txid)
    const paid = rpcOutputs(tx).filter(output => output.script === recipientScript)
    assert.equal(paid.length, 1)
    const credit = token ? paid[0].assets.filter(asset => assetKey(asset.Name) === key)
      .reduce((sum, asset) => sum + asInteger(asset.Amount), 0n) : paid[0].value
    assert.equal(credit, amount)
    await expect.poll(() => addressAmount('SatoshiNet', fixture.recipient.address, key), pollOptions).toBe(recipientBefore + amount)
    await expect.poll(() => addressAmount('SatoshiNet', fixture.recipient.address, '::'), pollOptions).toBe(recipientSatsBefore + paid[0].value)
    await expect.poll(readL2Total, pollOptions).toBe(sourceSatsBefore - paid[0].value - 10n)
    if (token) assert.equal(await addressAmount('SatoshiNet', fixture.address, key), sourceBefore - amount)
    console.log(JSON.stringify({ pwaTransfer: txid, chain: 'SatoshiNet', asset: key, amount: String(amount) }))
    await assertWalletBalanceUI('SatoshiNet', token)
  }
  const privateRoundTrip = async token => {
    await selectMode('lightning')
    const key = token ? fixture.asset.key : '::'
    const amount = token ? 100n : 4000n
    const before = await readChannel()
    const sourceBefore = await readL2Amount(key)
    const satsBefore = await readL2Total()
    // Unlock first gives the peer an equivalent balance, so locking it back
    // exercises the ordinary funded channel without an unrelated expansion.
    await clickAssetOperation('Unlock', amount, token, 'Channel')
    await expect.poll(async () => channelAmount(await readChannel(), key), pollOptions).toBe(channelAmount(before, key) - amount)
    const unlocked = await readyChannelUI()
    assert.notEqual(unlocked.lastPaymentId, before.lastPaymentId)
    await confirmedL2Transaction(unlocked.lastPaymentId)
    await expect.poll(() => readL2Amount(key), pollOptions).toBe(sourceBefore + amount - (token ? 0n : 10n))
    await expect.poll(readL2Total, pollOptions).toBe(satsBefore + amount - 10n)
    await assertWalletBalanceUI('SatoshiNet', token)
    await clickAssetOperation('Lock', amount, token, 'SatoshiNet')
    await expect.poll(async () => channelAmount(await readChannel(), key), pollOptions).toBe(channelAmount(before, key))
    const locked = await readyChannelUI()
    assert.notEqual(locked.lastPaymentId, unlocked.lastPaymentId)
    await confirmedL2Transaction(locked.lastPaymentId)
    assert.deepEqual(channelBalances(locked), channelBalances(before))
    await expect.poll(() => readL2Amount(key), pollOptions).toBe(sourceBefore - (token ? 0n : 20n))
    await expect.poll(readL2Total, pollOptions).toBe(satsBefore - 20n)
    await assertWalletBalanceUI('SatoshiNet', token)
  }
  const splicingOut = async token => {
    await selectMode('lightning')
    const key = token ? fixture.asset.key : '::'
    const amount = token ? 400n : 10000n
    const prior = await readChannel()
    const before = await control('snapshot')
    const l1Before = await addressAmount('Bitcoin', fixture.address, key)
    await clickAssetOperation('Splicing out', amount, token, 'Channel')
    const funding = await pendingFunding(before)
    const tx = await confirmL1Payment(funding)
    assert.ok(tx.outs.some(output => output.script.toString('hex') === walletScript && asInteger(output.value) === amount),
      'splicing-out has no exact requested output to the wallet')
    const current = await readyChannelUI()
    assert.equal(channelAmount(current, key), channelAmount(prior, key) - amount)
    if (token) await expect.poll(() => addressAmount('Bitcoin', fixture.address, key), pollOptions).toBe(l1Before + amount)
    else {
      const received = (await addressAmount('Bitcoin', fixture.address, key)) - l1Before
      assert.ok(received > 0n && received < amount, 'splicing-out net credit must account for the separate L1 fee')
    }
    await assertWalletBalanceUI('Bitcoin', token)
  }
  const withdraw = async token => {
    await selectMode('poolswap')
    const key = token ? fixture.asset.key : '::'
    const amount = token ? 400n : 4000n
    const before = await control('snapshot')
    const l1Before = await addressAmount('Bitcoin', fixture.address, key)
    const l1SatsBefore = await addressAmount('Bitcoin', fixture.address, '::')
    const l2Before = await readL2Amount(key)
    const feeRate = asInteger(await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().btcFeeRate))
    await clickAssetOperation('Withdraw', amount, token, 'SatoshiNet')
    const funding = await pendingFunding(before)
    const tx = await confirmL1Payment(funding)
    const paid = tx.outs.filter(output => output.script.toString('hex') === walletScript)
    assert.ok(paid.length, 'withdrawal never produced a Bitcoin payment to the wallet')
    const creditSats = paid.reduce((sum, output) => sum + asInteger(output.value), 0n)
    assert.ok(creditSats > 0n)
    await expect.poll(() => addressAmount('Bitcoin', fixture.address, '::'), pollOptions).toBe(l1SatsBefore + creditSats)
    if (token) {
      await expect.poll(() => addressAmount('Bitcoin', fixture.address, key), pollOptions).toBe(l1Before + amount)
      await expect.poll(() => readL2Amount(key), pollOptions).toBe(l2Before - amount)
    } else {
      assert.equal(creditSats, amount, 'public BTC withdrawal must pay the complete requested amount; fees are charged separately')
      // The published withdrawal quote reserves a 3-input/5-output 2-of-2
      // transaction: ceil((4*(10+3*41+5*43)+2+3*222)/4) = 515 vbytes.
      // It is charged in addition to 2000 service sats and 10 L2 network sats.
      const expectedDebit = amount + 2000n + 515n * feeRate + 10n
      await expect.poll(() => readL2Amount(key), pollOptions).toBe(l2Before - expectedDebit)
    }
    await assertWalletBalanceUI('Bitcoin', token)
    await assertWalletBalanceUI('SatoshiNet', token)
  }
  const openEscape = async () => {
    await page.evaluate(() => { location.hash = '#/wallet' })
    await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible()
    await selectMode('lightning')
    await page.evaluate(() => { location.hash = '#/wallet/setting' })
    const heading = page.getByRole('heading', { name: 'Unilateral Withdrawal', exact: true })
    await expect(heading).toBeVisible()
    await heading.click()
    await expect(page.getByRole('heading', { name: 'Current Commitment Transaction', exact: true })).toBeVisible()
  }
  const safetySnapshot = async () => {
    const result = await stpRead('safetySnapshot', channelId)
    const snapshot = typeof result.json === 'string' ? JSON.parse(result.json) : result
    assert.equal(snapshot.channel_id, channelId)
    assert.equal(snapshot.raw_status, 16)
    assert.equal(snapshot.local_commitment_present, true)
    assert.equal(snapshot.remote_commitment_present, true)
    assert.match(snapshot.local_commitment_txid, txidPattern)
    assert.match(snapshot.remote_commitment_txid, txidPattern)
    return snapshot
  }
  const requestCooperativeClose = async current => {
    await page.getByRole('button', { name: 'Cooperative Close', exact: true }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog.getByRole('heading', { name: 'Confirm Cooperative Close', exact: true })).toBeVisible()
    await expect(dialog.getByText(channelId, { exact: true })).toBeVisible()
    await expect(dialog.getByText('Bitcoin testnet4 / SatoshiNet testnet', { exact: true })).toBeVisible()
    await expect(dialog.getByText(String(current.capacity) + ' sats', { exact: true })).toBeVisible()
    await expect(dialog.getByText('Cooperative (force = false)', { exact: true })).toBeVisible()
    for (const balance of current.localbalanceL1) {
      const unit = assetKey(balance.Name) === '::' ? 'sats' : balance.Name.Ticker
      await expect(dialog).toContainText(String(balance.Amount) + ' ' + unit)
    }
    return dialog
  }
  const assertCloseCancelled = async (before, current) => {
    assert.equal(await stpRead('getChannelStatus', channelId), 16)
    const after = await readChannel()
    assert.equal(after.commitHeight, current.commitHeight)
    assert.deepEqual(channelBalances(after), channelBalances(current))
    assert.deepEqual(deanchorInputs(after), deanchorInputs(current))
    const snapshot = await control('snapshot')
    assert.deepEqual(snapshot.l1.broadcast_count, before.l1.broadcast_count)
    assert.deepEqual(snapshot.l1.pending_txids, before.l1.pending_txids)
    assert.deepEqual(snapshot.l1.confirmed_txids, before.l1.confirmed_txids)
  }

  try {
    // Test wallet import is fixture setup. Every movement of its funds below
    // goes through the same controls a PWA user operates.
    await page.evaluate(async ({ mnemonic, password }) => {
      const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().importWallet(mnemonic, password)
      if (error) throw error
      location.hash = '#/wallet'
    }, { mnemonic: fixture.mnemonic, password: fixture.password })
    await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible()
    await assertIdentity()
    await selectMode('lightning')

    await check(requiredPwaPosCases[0], async () => {
      const before = await control('snapshot')
      assert.deepEqual(before.l1.pending_txids, [])
      await showTab('Channel')
      await activePanel().getByRole('button', { name: 'Open', exact: true }).click()
      await activePanel().locator('input').fill(String(amounts.opening))
      await activePanel().getByRole('button', { name: 'Confirm', exact: true }).click()
      const dialog = page.getByRole('dialog')
      await expect(dialog.getByRole('heading', { name: 'Confirm Channel Opening', exact: true })).toBeVisible()
      const feeRate = await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().btcFeeRate)
      const preview = await stpRead('previewOpenChannel', String(feeRate), String(amounts.opening))
      assert.equal(preview.valid, true)
      await expect(dialog).toContainText(`${amounts.opening} sats`)
      await expect(dialog).toContainText(`${preview.channelCapacity} sats`)
      await expect(dialog.getByRole('button', { name: 'Open Channel', exact: true })).toBeEnabled()
      await dialog.getByRole('button', { name: 'Open Channel', exact: true }).click()
      const funding = await pendingFunding(before)
      await expect(activePanel().getByText('Channel is opening', { exact: true })).toBeVisible()
      const pending = await readChannel()
      channelId = pending.channelId
      assert.ok(channelId)
      const signed = deanchorInputs(pending)
      await reopen(false)
      await showTab('Channel')
      await expect(activePanel().getByText('Channel is opening', { exact: true })).toBeVisible()
      assert.equal((await readChannel()).channelId, channelId)
      assert.deepEqual(deanchorInputs(await readChannel()), signed)
      await assertSamePending(funding)
      const { anchor } = await confirmFunding(funding)
      const channel = await readyChannelUI()
      assert.equal(channel.capacity, preview.channelCapacity)
      assertSignedAnchor(signed, anchor, channel)
    })

    await check(requiredPwaPosCases[1], async () => {
      let stage = 'peer-ready'
      const setStage = value => {
        stage = value
        console.log(JSON.stringify({ splicingInStage: stage }))
      }
      setStage(stage)
      try {
        const prior = await readChannel()
        // The controlled Anchor confirmation waits for chain/indexer state;
        // Core's STP monitor can finish opening on its next tick. Establish
        // that independent prerequisite before testing pending-page recovery.
        const corePeer = fixture.config.Peers.find(peer => peer.startsWith('s@'))
        assert.ok(corePeer, 'fixture must identify its Core STP peer')
        const coreURL = new URL(corePeer.slice(corePeer.lastIndexOf('@') + 1) + '/')
        assert.equal(coreURL.protocol, 'http:')
        assert.equal(coreURL.hostname, '127.0.0.1', 'Core read must stay in the isolated network')
        await expect.poll(async () => {
          const response = await fetch(new URL(`info/channel/${encodeURIComponent(channelId)}`, coreURL), {
            signal: AbortSignal.timeout(10000),
          })
          assert.ok(response.ok, `Core channel read: HTTP ${response.status}`)
          const result = await response.json()
          if (result.code !== 0) return false
          assert.equal(result.channel?.channelId, channelId)
          return result.channel.status === 16
        }, pollOptions).toBe(true)
        setStage('submit')
        const before = await control('snapshot')
        await clickAssetOperation('Splicing in', amounts.splicing_btc, false)
        setStage('submitted')
        const funding = await pendingFunding(before)
        setStage('funding-pending')
        await showTab('Channel')
        await expect(activePanel().getByText('Splicing in', { exact: true })).toBeVisible()
        setStage('pending-visible')
        const signed = deanchorInputs(await readChannel())
        setStage('reopen')
        await reopen(true)
        setStage('reopened')
        await showTab('Channel')
        await expect(activePanel().getByText('Splicing in', { exact: true })).toBeVisible()
        assert.equal((await readChannel()).channelId, channelId)
        assert.deepEqual(deanchorInputs(await readChannel()), signed)
        await assertSamePending(funding)
        const { anchor } = await confirmFunding(funding)
        const channel = await readyChannelUI()
        assert.equal(channelAmount(channel, '::') - channelAmount(prior, '::'), asInteger(amounts.splicing_btc))
        assertSignedAnchor(signed, anchor, channel)
      } catch (error) {
        console.error(JSON.stringify({ splicingInFailureStarted: { stage, message: error.message } }))
        let sdkReadError
        const sdkChannel = await readChannel().catch(error => {
          sdkReadError = error.message
          return null
        })
        const reservations = await stpRead('allReservations').then(result => result.reservations
          .filter(item => item.type === 'splicing').map(item => {
            const stored = JSON.parse(item.json)
            return { id: item.reservation_id, status: item.status, channelId: stored.ChannelId,
              walletId: stored.WalletId, hasSplicingTx: Boolean(stored.SplicingTx),
              hasAnchorTx: Boolean(stored.AnchorTx) }
          })).catch(error => ({ error: error.message }))
        let displayTimeout
        const displayRead = page.evaluate(async () => {
          const store = (await import('/store/channel.ts')).useChannelStore()
          const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
          return { status: store.channel?.status, channelId: store.channel?.channelId,
            pendingSplicing: store.channel?.pendingSplicing, locked: wallet.locked,
            walletId: String(wallet.walletId), accountIndex: wallet.accountIndex, address: wallet.address,
            activeTab: document.querySelector('[role="tab"][aria-selected="true"]')?.textContent,
            panel: document.querySelector('[role="tabpanel"][data-state="active"]')?.textContent }
        })
        let display
        try {
          display = await Promise.race([displayRead, new Promise((_, reject) => {
            displayTimeout = setTimeout(() => reject(new Error('display read timed out after 10s')), 10000)
          })]).catch(error => ({ error: error.message }))
        } finally {
          clearTimeout(displayTimeout)
        }
        console.error(JSON.stringify({ splicingInFailure: { stage, sdkReadError,
          sdkStatus: sdkChannel?.status, reservations, display } }))
        throw error
      }
    })

    await check(requiredPwaPosCases[2], async () => {
      const before = await readChannel()
      const pending = await control('snapshot')
      assert.deepEqual(pending.l1.pending_txids, [])
      assert.deepEqual(pending.mempool_txids, [])
      const activated = await control('activate')
      assertNodes(activated)
      assert.ok(activated.height >= fixture.activation_height + 1)
      await reopen(false)
      const channel = await readyChannelUI()
      assert.equal(channel.capacity, before.capacity)
      assert.deepEqual(channelBalances(channel), channelBalances(before))
      assert.deepEqual(deanchorInputs(channel), deanchorInputs(before))
    })

    await check(requiredPwaPosCases[3], async () => {
      const prior = await readChannel()
      const before = await control('snapshot')
      await clickAssetOperation('Splicing in', amounts.splicing_asset, true)
      const funding = await pendingFunding(before)
      const signed = deanchorInputs(await readChannel())
      const { anchor } = await confirmFunding(funding, 3, true)
      const channel = await readyChannelUI()
      const expected = channelAmount(prior, fixture.asset.key) + asInteger(amounts.splicing_asset)
      assert.equal(channelAmount(channel, fixture.asset.key), expected)
      assertSignedAnchor(signed, anchor, channel)
      await assertAssetVisible(expected)
    })

    await check(requiredPwaPosCases[4], async () => {
      await deposit(true)
      // A returning user must see the same confirmed credit. Reopen uses the
      // normal PWA unlock and refresh paths, never an injected balance.
      const expected = latestDeposit.amount
      const total = await readL2Total()
      const broadcasts = (await control('snapshot')).l1.broadcast_count
      await reopen(true)
      await selectMode('poolswap')
      await showTab('SatoshiNet')
      await expect.poll(() => readL2Amount(fixture.asset.key), pollOptions).toBe(expected)
      await expect.poll(readL2Total, pollOptions).toBe(total)
      await assertAssetVisible(expected)
      assert.deepEqual((await control('snapshot')).l1.broadcast_count, broadcasts, 'Deposit reload must not replay its L1 broadcast')
    })
    await check(requiredPwaPosCases[5], async () => { await deposit(false) })

    await check(requiredPwaPosCases[6], async () => {
      const before = await readL2Amount('::')
      const rotated = await control('rotation')
      assertNodes(rotated)
      // Go verifies each actual producer, approval and reward outpoint. The PWA
      // remains attached to that same network and must retain its confirmed funds.
      await showTab('Bitcoin')
      await showTab('SatoshiNet')
      assert.equal(await readL2Amount('::'), before)
      const confirmed = rotated.anchors.find(anchor => anchor.txid === latestDeposit.anchor.txid)
      assert.ok(confirmed, 'rotation lost the PWA confirmed Anchor')
    })

    await check(requiredPwaPosCases[7], async () => {
      const offline = await control('miner-offline')
      assertNodes(offline, 2)
      await deposit(false, 2)
    })

    await check(requiredPwaPosCases[8], async () => {
      const beforeAmount = await readL2Amount('::')
      const beforeChannel = await readChannel()
      const restored = await control('restore-miner')
      assertNodes(restored)
      assert.ok(restored.anchors.some(anchor => anchor.txid === latestDeposit.anchor.txid))
      const restarted = await control('restart-core')
      assertNodes(restarted)
      await reopen(true)
      assert.equal(await readL2Amount('::'), beforeAmount)
      await selectMode('lightning')
      const channel = await readyChannelUI()
      assert.equal(channel.capacity, beforeChannel.capacity)
      assert.deepEqual(channelBalances(channel), channelBalances(beforeChannel))
      assert.deepEqual(deanchorInputs(channel), deanchorInputs(beforeChannel))
      await showTab('SatoshiNet')
      await expect.poll(() => readL2Amount('::'), pollOptions).toBe(beforeAmount)
      const final = await control('snapshot')
      assert.deepEqual(final.l1.pending_txids, [])
      assertNodes(final)
    })

    // These cases follow activation and recovery, so their ordinary transactions
    // cannot advance the chain past H before the POS boundary assertions run.
    await check(requiredPwaPosCases[9], async () => { await sendL1(false) })
    await check(requiredPwaPosCases[10], async () => { await sendL1(true) })
    await check(requiredPwaPosCases[11], async () => { await sendL1(false, true) })
    await check(requiredPwaPosCases[12], async () => { await sendL2(false) })
    await check(requiredPwaPosCases[13], async () => { await sendL2(true) })
    await check(requiredPwaPosCases[14], async () => { await privateRoundTrip(false) })
    await check(requiredPwaPosCases[15], async () => { await privateRoundTrip(true) })
    await check(requiredPwaPosCases[16], async () => { await splicingOut(false) })
    await check(requiredPwaPosCases[17], async () => { await splicingOut(true) })
    await check(requiredPwaPosCases[18], async () => { await withdraw(false) })
    await check(requiredPwaPosCases[19], async () => { await withdraw(true) })

    await check(requiredPwaPosCases[20], async () => {
      await openEscape()
      const current = await readChannel()
      const safety = await safetySnapshot()
      assert.equal(safety.commit_height, current.commitHeight)
      const info = await stpRead('getCommitTxAssetInfo', channelId)
      const inputs = JSON.parse(info.inputs)
      const outputs = JSON.parse(info.outputs)
      assert.ok(inputs.length && outputs.length)
      assert.ok(outputs.every(output => output.Outpoint.startsWith(safety.local_commitment_txid + ':')))
      for (const [title, expected] of [['Inputs', inputs], ['Outputs', outputs]]) {
        const table = page.getByRole('heading', { name: title, exact: true }).locator('..').locator('table')
        await expect(table.locator('tbody tr')).toHaveCount(expected.length)
        for (let i = 0; i < expected.length; i++) {
          const output = expected[i]
          const row = table.locator('tbody tr').nth(i)
          await expect(row.getByRole('link')).toHaveAttribute('href', new RegExp('/tx/' + output.Outpoint.split(':')[0] + '$'))
          await expect(row.locator('td').nth(1)).toHaveText(String(output.Value))
          for (const asset of output.Assets || []) await expect(row).toContainText(asset.Name.Ticker + ': ' + asset.Amount)
        }
      }
    })
    await check(requiredPwaPosCases[21], async () => {
      const before = await control('snapshot')
      const current = await readChannel()
      const dialog = await requestCooperativeClose(current)
      await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
      await expect(dialog).toBeHidden()
      await assertCloseCancelled(before, current)
    })
    await check(requiredPwaPosCases[22], async () => {
      const before = await control('snapshot')
      const current = await readChannel()
      const safety = await safetySnapshot()
      await page.getByRole('button', { name: 'Force Close', exact: true }).click()
      const dialog = page.getByRole('alertdialog')
      await expect(dialog.getByRole('heading', { name: 'Confirm Force Close', exact: true })).toBeVisible()
      for (const [label, value] of [
        ['Channel', safety.channel_id], ['Status', safety.status], ['Commit height', safety.commit_height],
        ['CSV delay', safety.csv_delay], ['Local commitment', safety.local_commitment_txid],
        ['Remote commitment', safety.remote_commitment_txid], ['Punish coverage', safety.punish_coverage.status],
      ]) await expect(dialog.getByText(label + ': ' + value, { exact: true })).toBeVisible()
      await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
      await expect(dialog).toBeHidden()
      await assertCloseCancelled(before, current)
    })
    if (t.selectedCases?.includes(optionalPwaEscapeCases[0])) await check(optionalPwaEscapeCases[0], async () => {
      await openEscape()
      const current = await readChannel()
      const before = await control('snapshot')
      const beforeAmount = await addressAmount('Bitcoin', fixture.address, '::')
      const planValue = await stpRead('forceClosePlan', channelId)
      const plan = JSON.parse(planValue.json)
      assert.equal(plan.channel_id, channelId)
      assert.equal(plan.commit_height, current.commitHeight)
      assert.ok(Number.isInteger(plan.csv_delay) && plan.csv_delay >= 2 && plan.csv_delay <= 256)
      const commit = Transaction.fromHex(plan.commit_tx_hex)
      assert.equal(commit.getId(), plan.commit_txid)
      assert.ok((current.localbalanceL1 || []).every(asset => assetKey(asset.Name) === '::' || asInteger(asset.Amount) === 0n),
        'this independent CSV drill requires its opening/BTC-only channel')
      const endpoint = fixture.config.IndexerL1
      const rawL1 = async id => {
        const response = await fetch(`${endpoint.Scheme}://${endpoint.Host}/${endpoint.Proxy}/btc/rawtx/${id}`)
        assert.equal(response.ok, true)
        const raw = await response.json(); assert.equal(raw.code, 0, raw.msg)
        return Transaction.fromHex(raw.data)
      }
      let expectedGross = channelAmount(current, '::')
      for (const point of current.stubUtxos || []) {
        const [id, index] = point.split(':')
        expectedGross += asInteger((await rawL1(id)).outs[Number(index)].value)
      }
      let commitInputs = 0n
      for (const input of commit.ins) {
        commitInputs += asInteger((await rawL1(Buffer.from(input.hash).reverse().toString('hex'))).outs[input.index].value)
      }
      const commitFee = commitInputs - commit.outs.reduce((sum, output) => sum + asInteger(output.value), 0n)
      assert.ok(commitFee > 0n)
      const packageTxs = [...(plan.prev_tx_hex || []), plan.commit_tx_hex, ...(plan.next_tx_hex || [])].map(raw => Transaction.fromHex(raw))
      const expectedIDs = packageTxs.map(tx => tx.getId())
      assert.equal(new Set(expectedIDs).size, expectedIDs.length)
      // Observe a real SDK channel event, never invoke a callback in the test.
      await page.evaluate(() => {
        window.__channelE2EEvents = []
        const ret = window.sat20wallet_wasm.registerCallback((event, value) => window.__channelE2EEvents.push({ event, value }))
        if (ret.code !== 0) throw new Error(ret.msg)
      })
      await page.getByRole('button', { name: 'Force Close', exact: true }).click()
      const dialog = page.getByRole('alertdialog')
      await expect(dialog.getByRole('heading', { name: 'Confirm Force Close', exact: true })).toBeVisible()
      await expect(dialog.getByText(`Local commitment: ${plan.commit_txid}`, { exact: true })).toBeVisible()
      await dialog.getByRole('button', { name: 'Force Close', exact: true }).click()
      await expect(dialog).toBeHidden()
      await expect.poll(async () => (await control('snapshot')).l1.pending_txids.includes(plan.commit_txid), pollOptions).toBe(true)
      const pending = await control('snapshot')
      for (const id of expectedIDs) assert.ok(pending.l1.pending_txids.includes(id) || before.l1.confirmed_txids.includes(id))
      await reopen(true)
      await expect.poll(() => stpRead('getChannelStatus', channelId), pollOptions).toBe(12)
      await page.evaluate(() => {
        window.__channelE2EEvents = []
        const ret = window.sat20wallet_wasm.registerCallback((event, value) => window.__channelE2EEvents.push({ event, value }))
        if (ret.code !== 0) throw new Error(ret.msg)
      })
      const released = await page.evaluate(async () => await window.sat20wallet_wasm.release())
      assert.equal(released.code, 0, released.msg)
      const oldEvents = await page.evaluate(() => window.__channelE2EEvents)
      const confirmed = await control('confirm-l1', { wait_anchors: false })
      const closeHeight = confirmed.l1.height
      await control('mine')
      assert.deepEqual(await page.evaluate(() => window.__channelE2EEvents), oldEvents,
        'the released manager must not dispatch a channel callback after chain confirmation')
      const initialized = await page.evaluate(async config => await window.sat20wallet_wasm.init(config, 2), fixture.config)
      assert.equal(initialized.code, 0, initialized.msg)
      await page.evaluate(() => {
        window.__channelE2EEvents = []
        const ret = window.sat20wallet_wasm.registerCallback((event, value) => window.__channelE2EEvents.push({ event, value }))
        if (ret.code !== 0) throw new Error(ret.msg)
      })
      const unlocked = await page.evaluate(async password => await window.sat20wallet_wasm.unlockWallet(password), fixture.password)
      assert.equal(unlocked.code, 0, unlocked.msg)
      await expect.poll(() => stpRead('getChannelStatus', channelId), pollOptions).toBe(13)
      assert.equal(await addressAmount('Bitcoin', fixture.address, '::'), beforeAmount, 'CSV funds must remain unavailable after commitment confirmation')
      await expect.poll(() => page.evaluate(txid => window.__channelE2EEvents.some(item => item.event === 'channelclosedforcely' && item.value === txid), plan.commit_txid), pollOptions).toBe(true)
      const premature = JSON.parse((await stpRead('sweepBuild', channelId, plan.commit_txid, closeHeight, false)).json)
      assert.equal(premature.signed, true); assert.equal(premature.verified, true)
      const l1Endpoint = fixture.config.IndexerL1
      const testSweep = async raw => {
        const dryRun = await fetch(`${l1Endpoint.Scheme}://${l1Endpoint.Host}/${l1Endpoint.Proxy}/btc/tx/test`, {
          method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ signedTxs: [raw] }),
        })
        assert.equal(dryRun.ok, true)
        const admission = await dryRun.json(); assert.equal(admission.code, 0, admission.msg)
        assert.equal(admission.data.length, 1)
        return admission.data[0]
      }
      const refused = await testSweep(premature.sweep_tx_hex)
      assert.equal(refused.allowed, false, 'an otherwise signed premature sweep must fail relative block maturity')
      assert.match(refused['reject-reason'], /non-BIP68-final/)
      const broadcasts = (await control('snapshot')).l1.broadcast_count
      const directBroadcast = await page.evaluate(async ({ channelId, txid, height }) =>
        await window.sat20wallet_wasm.sweepBuild(channelId, txid, height, true), { channelId, txid: plan.commit_txid, height: closeHeight })
      assert.notEqual(directBroadcast.code, 0, 'only the existing monitor owns sweep broadcasting')
      assert.deepEqual((await control('snapshot')).l1.broadcast_count, broadcasts)
      // At this tip the next candidate block is exactly the BIP68 boundary.
      // Preflight may admit it, while the SDK monitor retains its own later
      // automatic broadcast threshold. Neither read may mutate the ledger.
      const boundary = await control('confirm-l1', { wait_anchors: false, empty_blocks: plan.csv_delay - 1 })
      assert.equal(boundary.l1.height + 1, closeHeight + plan.csv_delay)
      const boundarySweep = JSON.parse((await stpRead('sweepBuild', channelId, plan.commit_txid, boundary.l1.height, false)).json)
      assert.equal((await testSweep(boundarySweep.sweep_tx_hex)).allowed, true, 'the exact relative block boundary must be admissible')
      assert.equal(await stpRead('getChannelStatus', channelId), 13)
      assert.deepEqual((await control('snapshot')).l1.broadcast_count, broadcasts)
      const mature = await control('confirm-l1', { wait_anchors: false, empty_blocks: 2 })
      assert.equal(mature.l1.height, closeHeight + plan.csv_delay + 1)
      await expect.poll(() => stpRead('getChannelStatus', channelId), pollOptions).toBe(14)
      const sweepValue = await stpRead('sweepBuild', channelId, plan.commit_txid, mature.l1.height, false)
      const sweep = JSON.parse(sweepValue.json)
      assert.equal(sweep.signed, true); assert.equal(sweep.verified, true); assert.equal(sweep.broadcastable, true)
      assert.equal(sweep.commit_txid, plan.commit_txid)
      const tx = Transaction.fromHex(sweep.sweep_tx_hex)
      assert.equal(tx.getId(), sweep.sweep_txid)
      assert.ok(tx.ins.some(input => Buffer.from(input.hash).reverse().toString('hex') === plan.commit_txid))
      assert.ok(tx.ins.every(input => input.witness.length > 0))
      let inputSats = 0n
      for (const input of tx.ins) {
        const id = Buffer.from(input.hash).reverse().toString('hex')
        assert.equal(id, plan.commit_txid, 'this funded BTC sweep must not debit unrelated wallet inputs')
        inputSats += asInteger((await rawL1(id)).outs[input.index].value)
      }
      const outputSats = tx.outs.reduce((sum, output) => sum + asInteger(output.value), 0n)
      assert.equal(inputSats - outputSats, asInteger(sweep.fee), 'signed sweep fee must equal independently decoded inputs minus outputs')
      const credit = tx.outs.filter(output => output.script.toString('hex') === walletScript).reduce((sum, output) => sum + asInteger(output.value), 0n)
      assert.ok(credit > 0n)
      assert.equal(credit, expectedGross - commitFee - asInteger(sweep.fee),
        'final return must preserve the pre-close wallet entitlement after exact commitment and sweep fees')
      const feeRate = asInteger(await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().btcFeeRate))
      assert.ok(asInteger(sweep.fee) >= BigInt(tx.virtualSize()) * feeRate)
      await control('confirm-l1', { wait_anchors: false })
      await expect.poll(() => stpRead('getChannelStatus', channelId), pollOptions).toBe(-1)
      await expect.poll(() => page.evaluate(txid => window.__channelE2EEvents.some(item => item.event === 'channelswept' && item.value === txid), sweep.sweep_txid), pollOptions).toBe(true)
      await expect.poll(() => addressAmount('Bitcoin', fixture.address, '::'), pollOptions).toBe(beforeAmount + credit)
      const finalBroadcasts = (await control('snapshot')).l1.broadcast_count
      await reopen(true)
      assert.equal(await addressAmount('Bitcoin', fixture.address, '::'), beforeAmount + credit)
      assert.deepEqual((await control('snapshot')).l1.broadcast_count, finalBroadcasts, 'completed force close must not replay after reload')
      await assertWalletBalanceUI('Bitcoin')
    })
    await check(requiredPwaPosCases[23], async () => {
      await openEscape()
      const current = await readChannel()
      const feeRate = asInteger(await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().btcFeeRate))
      assert.ok(Array.isArray(current.remotebalanceL1))
      assert.ok(current.remotebalanceL1.every(allocation => asInteger(allocation.Amount) === 0n),
        'this round-trip fixture must restore all channel equity to its initiator before close')
      const endpoint = fixture.config.IndexerL1
      const l1 = `${endpoint.Scheme}://${endpoint.Host}/${String(endpoint.Proxy || 'testnet').replace(/^\/+|\/+$/g, '')}`
      // Read the complete pool before signing: token carriers can also hold
      // plain sats above the amount bound to tokens, including dust padding.
      const poolPoints = [current.chanPoint, ...current.fundingUtxos, ...current.stubUtxos]
      assert.equal(new Set(poolPoints).size, poolPoints.length)
      let expectedGross = 0n
      for (const point of poolPoints) {
        const response = await fetch(`${l1}/v3/utxo/info/${point}`)
        assert.equal(response.ok, true)
        const output = await response.json()
        assert.equal(output.code, 0, output.msg)
        expectedGross += asInteger(output.data.Value ?? output.data.value)
      }
      const before = await control('snapshot')
      assert.deepEqual(before.l1.pending_txids, [])
      const l1SatsBefore = await addressAmount('Bitcoin', fixture.address, '::')
      const l1AssetBefore = await addressAmount('Bitcoin', fixture.address, fixture.asset.key)
      const l2SatsBefore = await readL2Total()
      const dialog = await requestCooperativeClose(current)
      await dialog.getByRole('button', { name: 'Confirm Close', exact: true }).click()
      await expect(dialog).toBeHidden()
      const closing = await pendingFunding(before)
      const tx = await confirmL1Payment(closing)
      const spent = new Set(tx.ins.map(input => Buffer.from(input.hash).reverse().toString('hex') + ':' + input.index))
      assert.equal(spent.size, tx.ins.length, 'close transaction repeats an input')
      assert.deepEqual([...spent].sort(), [...poolPoints].sort(), 'close must spend exactly the independently valued channel pool')
      const returned = tx.outs.filter(output => output.script.toString('hex') === walletScript)
      assert.ok(returned.length, 'cooperative close has no return output to the wallet')
      const returnedSats = returned.reduce((sum, output) => sum + asInteger(output.value), 0n)
      // Channel closes reserve two maximal 73-byte signatures per input.
      // Fee is derived from the confirmed rate and transaction structure,
      // never from the refund amount under test.
      assert.ok(tx.ins.length < 253 && tx.outs.length < 253)
      const strippedSize = 10 + tx.ins.length * 41 + tx.outs.reduce((sum, output) => sum + 9 + output.script.length, 0)
      const closingVSize = BigInt(Math.ceil((strippedSize * 4 + 2 + tx.ins.length * 222) / 4))
      const expectedReturn = expectedGross - closingVSize * feeRate
      assert.equal(returnedSats, expectedReturn, 'cooperative close must return the complete pre-close local BTC equity minus the quoted network fee')
      await expect.poll(() => stpRead('getChannelStatus', channelId), pollOptions).toBe(0)
      await expect.poll(() => addressAmount('Bitcoin', fixture.address, '::'), pollOptions).toBe(l1SatsBefore + expectedReturn)
      await expect.poll(() => addressAmount('Bitcoin', fixture.address, fixture.asset.key), pollOptions)
        .toBe(l1AssetBefore + channelAmount(current, fixture.asset.key))
      // Existing spendable L2 deposits are independent of the private channel.
      assert.equal(await readL2Total(), l2SatsBefore)
      await page.evaluate(() => { location.hash = '#/wallet' })
      await showTab('Channel')
      await expect(activePanel().getByRole('button', { name: 'Open', exact: true })).toBeVisible()
      await assertWalletBalanceUI('Bitcoin', true)
      console.log(JSON.stringify({ pwaCooperativeClose: closing.txid, returnedSats: String(returnedSats), inputs: [...spent] }))
    })

    // Keep the known ORDX Advanced availability regression last. Its happy path
    // remains fully specified and will continue once the production UI is fixed.
    await check(requiredPwaPosCases[24], async () => { await sendL1(true, true) })
  } finally {
    await context.close()
  }
}
