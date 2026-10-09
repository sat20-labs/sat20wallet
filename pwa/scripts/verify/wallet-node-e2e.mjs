import assert from 'node:assert/strict'
import { expect } from '@playwright/test'
import { address as bitcoinAddress, networks, Transaction } from 'bitcoinjs-lib'

export const requiredPwaNodeCases = [
  'Node PWA: Core stake signs real L1 funding and appears in real node indexing',
  'Node PWA: Miner stake signs real L1 funding and appears in real node indexing',
  'Node PWA: Core cannot unstake while its independent Miner child is active',
  'Node PWA: already-staked independent Miner safely unstake through signed local action',
  'Node PWA: independent Core without child miners safely unstake through signed local action',
]

// Run last: these UI actions genuinely add eligible candidates to the network.
// The earlier POS checks intentionally verify the original three-node slots;
// this group only checks canonical stake/Anchor/indexer evidence thereafter.
export async function runPwaNodeCases(t, fixture) {
  const { check, device, ready, unlock, walletCall } = t
  const poll = { timeout: 180000, intervals: [250, 500, 1000, 2000] }
  const errors = []
  const control = async (action, body = {}) => {
    const response = await fetch(new URL(`/${action}`, fixture.control_url), {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
      signal: AbortSignal.timeout(360000),
    })
    const result = await response.json()
    assert.equal(response.ok, true, `${action}: ${JSON.stringify(result)}`)
    assert.ok(!result.error, result.error)
    return result
  }
  const readMiner = async (page, publicKey) => page.evaluate(async pubkey => {
    const api = (await import('/apis/satnet.ts')).default
    return api.getMinerInfo({ pubkey, network: 'testnet' })
  }, publicKey)
  const readLocalAction = async (page, reservationID) => page.evaluate(async id => {
    const [error, response] = await (await import('/utils/stp.ts')).default.allReservations()
    if (error) throw error
    const item = response.reservations.find(entry => String(entry.reservation_id) === String(id))
    if (!item?.json) return null
    return { outerStatus: item.status, action: JSON.parse(item.json) }
  }, reservationID)
  const readOperationLog = async (page, reservationID) => page.evaluate(async id => {
    const [error, records] = await (await import('/utils/operationLog.ts')).getOperationLogs()
    if (error) throw error
    return records.find(record => record.action === 'miner_unstake' &&
      record.parameters?.action === 'unstakeminer' && String(record.reservation_id) === String(id)) || null
  }, reservationID)
  const totalAsset = async (page, address, asset) => {
    const amount = await walletCall(page, 'getAssetAmount', address, asset)
    return BigInt(amount.availableAmt) + BigInt(amount.lockedAmt)
  }
  const waitForUnstake = async (page, reservationID) => {
    const deadline = Date.now() + 15 * 60 * 1000
    let lastMineAt = 0
    while (Date.now() < deadline) {
      const log = await readOperationLog(page, reservationID)
      if (log?.status === 'failed') throw new Error(`MinerUnstake failed: ${JSON.stringify(log)}`)
      if (log?.status === 'succeeded') return log

      const localAction = await readLocalAction(page, reservationID)
      const action = localAction?.action
      if (action?.TxId) {
        if (action.IsL1Tx) {
          const snapshot = await control('snapshot')
          if (snapshot.l1.pending_txids.includes(action.TxId)) {
            assert.deepEqual(snapshot.l1.pending_txids, [action.TxId], 'unstake must not confirm unrelated L1 transactions')
            const confirmed = await control('confirm-l1', { wait_anchors: false })
            assert.ok(confirmed.l1.confirmed_txids.includes(action.TxId), `unstake L1 transaction did not confirm: ${action.TxId}`)
          }
        } else if (Date.now() - lastMineAt >= 3000) {
          // Advance the existing isolated SatoshiNet fixture so the local
          // action monitor can observe its current L2 transaction.
          await control('mine')
          lastMineAt = Date.now()
        }
      }
      await new Promise(resolve => setTimeout(resolve, 750))
    }
    const finalLog = await readOperationLog(page, reservationID)
    const finalAction = await readLocalAction(page, reservationID).catch(error => ({ readError: error.message }))
    throw new Error(`MinerUnstake did not complete within 15 minutes: ${JSON.stringify({ finalLog, finalAction })}`)
  }
  const openStakeWallet = async actor => {
    assert.ok(actor?.mnemonic && actor.password && actor.address)
    const page = await device()
    await page.evaluate(() => { location.hash = '#/import' })
    await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(actor.mnemonic)
    await page.getByLabel('New Wallet Password', { exact: true }).fill(actor.password)
    await page.getByLabel('Confirm Password', { exact: true }).fill(actor.password)
    await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
    await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: 90000 })
    assert.equal((await walletCall(page, 'getWalletAddress', 0)).address, actor.address)
    return page
  }
  // The full wallet suite activates POS v2 in its earlier POS group. A
  // focused Node run must reach the same real fixture boundary before staking.
  const start = await control('snapshot')
  const activated = start.height < fixture.activation_height ? await control('activate') : start
  assert.ok(activated.height >= fixture.activation_height, 'Node cases require the activated POS-v2 chain')
  for (const [i, type, actor, isCore] of [
    [0, 'Core', fixture.coreStakeWallet, true],
    [1, 'Miner', fixture.minerStakeWallet, false],
  ]) {
    const page = await device()
    try {
      await check(requiredPwaNodeCases[i], async () => {
        assert.ok(actor?.mnemonic && actor.password && actor.address)
        await page.evaluate(() => { location.hash = '#/import' })
        await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(actor.mnemonic)
        await page.getByLabel('New Wallet Password', { exact: true }).fill(actor.password)
        await page.getByLabel('Confirm Password', { exact: true }).fill(actor.password)
        await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
        await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: 90000 })
        const publicKey = (await walletCall(page, 'getWalletPubkey', 0)).pubKey
        assert.equal((await walletCall(page, 'getWalletAddress', 0)).address, actor.address)
        const peer = fixture.config.Peers.find(value => value.startsWith(isCore ? 'b@' : 's@')).split('@')[1]
        const channel = (await walletCall(page, 'getChannelAddrByPeerPubkey', peer)).channelAddr
        const script = bitcoinAddress.toOutputScript(channel, networks.testnet).toString('hex')
        const before = await walletCall(page, 'getAssetAmount', actor.address, fixture.stake.asset)
        const beforeSnapshot = await control('snapshot')
        assert.equal(beforeSnapshot.l1.pending_txids.length, 0, 'previous groups left unconfirmed L1 operations')
        assert.ok(BigInt(before.availableAmt) >= BigInt(fixture.stake.amount))
        await page.evaluate(() => { location.hash = '#/wallet/setting/node' })
        await page.getByRole('button', { name: isCore ? 'become Core Node' : 'become Miner', exact: true }).click()
        const dialog = page.getByRole('dialog')
        await expect(dialog).toContainText(isCore ? 'become a core node' : 'become a regular mining node')
        await page.evaluate(async () => {
          const sdk = (await import('/utils/sat20.ts')).default
          const stake = sdk.stakeToBeMiner.bind(sdk)
          window.stakeReceiptReady = false
          window.stakeCalls = []
          const held = new Promise(resolve => { window.releaseStakeReceipt = resolve })
          sdk.stakeToBeMiner = async (...args) => {
            window.stakeCalls.push(args[0])
            const result = await stake(...args)
            window.stakeReceiptError = result[0]?.message || ''
            window.stakeReceiptReady = true
            await held
            return result
          }
        })
        try {
          await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
          await expect.poll(() => page.evaluate(() => window.stakeReceiptReady), { timeout: 60000 }).toBe(true)
          const core = page.getByRole('button', { name: 'become Core Node', exact: true })
          const miner = page.getByRole('button', { name: 'become Miner', exact: true })
          await expect(core).toBeDisabled(); await expect(miner).toBeDisabled()
          await (isCore ? miner : core).dispatchEvent('click')
          await expect(page.getByRole('dialog')).toHaveCount(0)
          assert.deepEqual(await page.evaluate(() => window.stakeCalls), [isCore])
          await page.evaluate(() => window.releaseStakeReceipt())
          const failure = await page.evaluate(() => window.stakeReceiptError)
          assert.equal(failure, '', 'real stake request failed after the UI busy guards were verified')
          await expect(page.getByText('操作成功，节点质押已提交！', { exact: true })).toBeVisible({ timeout: 90000 })
        } finally { await page.evaluate(() => window.releaseStakeReceipt?.()).catch(() => {}) }
        const readPending = () => page.evaluate(async key =>
          (await import('/lib/nodeStakeStorage.ts')).nodeStakeStorage.getNodeStakeData(key), publicKey)
        await expect.poll(readPending, poll).toBeTruthy()
        const saved = await readPending()
        assert.match(saved.txId, /^[0-9a-f]{64}$/)
        assert.equal(saved.isCore, isCore)
        assert.equal(saved.assetName, fixture.stake.asset)
        assert.equal(String(saved.amt), fixture.stake.amount)
        assert.ok(Number(saved.resvId) >= 0)
        await expect.poll(async () => (await control('snapshot')).l1.pending_txids.includes(saved.txId), poll).toBe(true)
        const l1 = fixture.config.IndexerL1
        const endpoint = `${l1.Scheme}://${l1.Host}/${String(l1.Proxy || 'testnet').replace(/^\/+|\/+$/g, '')}`
        const response = await fetch(`${endpoint}/btc/rawtx/${saved.txId}`)
        assert.equal(response.ok, true)
        const raw = await response.json()
        assert.equal(raw.code, 0, raw.msg)
        const funding = Transaction.fromHex(raw.data)
        assert.equal(funding.getId(), saved.txId)
        assert.ok(funding.ins.every(input => input.witness.length > 0), 'stake funding is not signed')
        const outIndex = funding.outs.findIndex(output => output.script.toString('hex') === script)
        assert.ok(outIndex >= 0, 'stake funds were not sent to the reviewed peer channel')
        const fundingPoint = `${saved.txId}:${outIndex}`
        const l1OutputResponse = await fetch(`${endpoint}/v3/utxo/info/${fundingPoint}`)
        const l1Output = await l1OutputResponse.json()
        assert.equal(l1Output.code, 0, l1Output.msg)
        const stake = l1Output.data.Assets.find(asset => `${asset.Name.Protocol}:${asset.Name.Type}:${asset.Name.Ticker}` === fixture.stake.asset)
        assert.ok(stake, 'signed funding output has no actual indexed stake asset')
        assert.equal(String(stake.Amount), fixture.stake.amount)
        await page.reload()
        await ready(page)
        await unlock(page, actor.password)
        assert.deepEqual(await readPending(), saved, 'pending stake record changed on reload')
        const confirmed = await control('confirm-l1')
        const anchor = confirmed.anchors.find(item => item.funding_outpoint === fundingPoint)
        assert.ok(anchor && anchor.height >= fixture.activation_height, 'stake did not become a real POS-v2 Anchor')
        assert.ok(anchor.outputs.some(output => output.pk_script === script))
        const readMiner = () => page.evaluate(async pubkey => {
          const api = (await import('/apis/satnet.ts')).default
          return api.getMinerInfo({ pubkey, network: 'testnet' })
        }, publicKey)
        await expect.poll(async () => (await readMiner()).data?.AnchorTxId, poll).toBe(anchor.txid)
        const indexedResponse = await readMiner()
        assert.equal(indexedResponse.code, 0, indexedResponse.msg)
        const indexed = indexedResponse.data
        assert.equal(indexed.isCoreNode, isCore)
        assert.equal(indexed.ServerNode, peer)
        assert.equal(indexed.AscendUtxo, fundingPoint)
        assert.equal(indexed.ChannelAddr, channel)
        if (indexed.AssetName !== fixture.stake.asset || String(indexed.AssetAmt) !== fixture.stake.amount) {
          console.log(JSON.stringify({ nodeStakeIndexMismatch: { type, indexed, expected: fixture.stake,
            funding: { outpoint: fundingPoint, asset: stake }, anchor } }))
        }
        assert.equal(indexed.AssetName, fixture.stake.asset)
        assert.equal(String(indexed.AssetAmt), fixture.stake.amount)
        await page.evaluate(() => { location.hash = '#/wallet/setting' })
        await page.getByRole('button', { name: /Node Settings/ }).click()
        await expect(page.getByRole('link', { name: channel, exact: true })).toBeVisible()
        await expect(page.getByRole('button', { name: 'Select Node Type', exact: true })).toBeDisabled()
        await expect.poll(readPending, poll).toBeNull()
        const after = await walletCall(page, 'getAssetAmount', actor.address, fixture.stake.asset)
        assert.equal(BigInt(before.availableAmt) + BigInt(before.lockedAmt) - BigInt(after.availableAmt) - BigInt(after.lockedAmt), BigInt(fixture.stake.amount))
        assert.equal((await walletCall(page, 'getWalletAddress', 0)).address, actor.address)
      })
    } catch (error) {
      if (t.selectedCases) throw error
      errors.push(new Error(`${type} stake: ${error.message}`, { cause: error }))
    }
    finally { await page.context().close() }
  }
  if (errors.length) throw new AggregateError(errors, 'Node stake prerequisites failed; dependent unstake cases were not run')

  for (const [caseIndex, actor, shouldBeCore] of [
    [2, fixture.coreNodeWallet, true],
    [3, fixture.minerStakeWallet, false],
    [4, fixture.coreStakeWallet, true],
  ]) {
    let page
    try {
      await check(requiredPwaNodeCases[caseIndex], async () => {
        page = await openStakeWallet(actor)
        const publicKey = (await walletCall(page, 'getWalletPubkey', 0)).pubKey
        const status = await readMiner(page, publicKey)
        assert.equal(status.code, 0, status.msg)
        assert.equal(status.data.isCoreNode, shouldBeCore)

        if (caseIndex === 2) {
          // The Miner stakes to the running Core process, rather than to the
          // independent Core candidate used by the first stake case.
          assert.ok(status.data.childCount > 0, 'Core must still have its independent staked Miner child')
          const before = await control('snapshot')
          assert.deepEqual(before.l1.pending_txids, [])
          await assert.rejects(walletCall(page, 'minerUnstake', '1'), /core node still has child miners/i)
          const after = await control('snapshot')
          assert.deepEqual(after.l1.broadcast_count, before.l1.broadcast_count)
          assert.deepEqual(after.l1.pending_txids, before.l1.pending_txids)
          assert.deepEqual(after.mempool_txids.filter(txid => !before.mempool_txids.includes(txid)), [],
            'Core unstake rejection broadcast to SatoshiNet')
          return
        }

        if (caseIndex === 3) {
          assert.equal(status.data.childCount, 0, 'independent Miner must not own child nodes')
        } else {
          assert.equal(status.data.childCount, 0, 'independent Core candidate must not own child miners')
        }

        const beforeSnapshot = await control('snapshot')
        assert.deepEqual(beforeSnapshot.l1.pending_txids, [], 'previous node action left L1 transactions pending')
        const beforeBalance = await totalAsset(page, actor.address, fixture.stake.asset)
        const receipt = await walletCall(page, 'minerUnstake', '1')
        const reservationID = Number(receipt.resvId)
        assert.ok(Number.isSafeInteger(reservationID) && reservationID >= 0,
          'unstake must create a tracked local action')
        assert.match(receipt.txId, /^[0-9a-f]{64}$/)
        const completion = await waitForUnstake(page, receipt.resvId)
        assert.equal(completion.status, 'succeeded')
        assert.equal(completion.action, 'miner_unstake')
        assert.equal(completion.reservation_type, 'localaction')
        assert.equal(String(completion.reservation_id), String(receipt.resvId))

        await expect.poll(async () => {
          const result = await readMiner(page, publicKey)
          if (result.code === -1 && result.msg === 'not found') {
            assert.equal(result.data, null)
            return null
          }
          assert.equal(result.code, 0, result.msg)
          assert.ok(result.data, 'successful miner lookup must contain its indexed role')
          return result.data
        }, { timeout: 15 * 60 * 1000, intervals: [500, 1000, 2000] }).toBeNull()
        assert.equal(await totalAsset(page, actor.address, fixture.stake.asset),
          beforeBalance + BigInt(fixture.stake.amount), 'unstake did not return the exact indexed stake amount')
        const finalSnapshot = await control('snapshot')
        assert.deepEqual(finalSnapshot.l1.pending_txids, [], 'unstake left an unconfirmed L1 transaction')
        assert.equal((await walletCall(page, 'getWalletAddress', 0)).address, actor.address)
      })
    } catch (error) {
      if (t.selectedCases) throw error
      errors.push(new Error(`${shouldBeCore ? 'Core' : 'Miner'} unstake: ${error.message}`, { cause: error }))
      break
    } finally { if (page) await page.context().close().catch(() => {}) }
  }

  if (errors.length) throw new AggregateError(errors, 'Node PWA acceptance failed')
}
