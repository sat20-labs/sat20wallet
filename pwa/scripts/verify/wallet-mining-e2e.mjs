import assert from 'node:assert/strict'
import { expect } from '@playwright/test'
import { payments, script, opcodes, networks } from 'bitcoinjs-lib'

export const requiredPwaMiningCases = [
  'Mining PWA: real WASM worker submits independently verified fake-L1 proof of work',
  'Mining PWA: stopping halts the worker and reload preserves its configuration',
]

export async function runPwaMiningCases(t, fixture) {
  const { check, device, ready, walletCall } = t
  const actor = fixture.basicWallet
  const page = await device()
  const context = page.context()
  const poll = { timeout: 120000, intervals: [250, 500, 1000] }
  const l1 = fixture.config.IndexerL1
  const proxy = String(l1.Proxy || 'testnet').replace(/^\/+|\/+$/g, '')
  const evidenceURL = `${l1.Scheme}://${l1.Host}/${proxy}/btc/lucky/info`
  let rewardAddress
  let found
  let stopped = false
  const evidence = async () => {
    const response = await fetch(evidenceURL)
    assert.equal(response.ok, true)
    const result = await response.json()
    assert.equal(result.code, 0, result.msg)
    return result.data
  }
  try {
    await check(requiredPwaMiningCases[0], async () => {
      await page.evaluate(() => { location.hash = '#/import' })
      await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(actor.mnemonic)
      await page.getByLabel('New Wallet Password', { exact: true }).fill(actor.password)
      await page.getByLabel('Confirm Password', { exact: true }).fill(actor.password)
      await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
      await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible()
      await page.evaluate(() => { location.hash = '#/wallet/btc-lucky-mining' })
      await expect(page.getByText('BTC Lucky Mining', { exact: true })).toBeVisible()
      const initial = await walletCall(page, 'getBTCLuckyMiningStatus')
      assert.equal(initial.running, false)
      rewardAddress = initial.rewardAddress
      assert.match(rewardAddress, /^tb1q[0-9a-z]+$/)
      const identityResponse = await fetch(`${l1.Scheme}://${l1.Host}/${proxy}/v3/indexer/pubkey`, {
        headers: { pubkey: (await walletCall(page, 'getWalletPubkey', 0)).pubKey },
      })
      assert.equal(identityResponse.ok, true)
      const identity = await identityResponse.json()
      assert.equal(identity.code, 0, identity.msg)
      assert.ok(identity.pubkey || identity.PubKey, 'L1 indexer must publish its service identity')
      const keys = [(await walletCall(page, 'getWalletPubkey', 0)).pubKey, identity.pubkey || identity.PubKey]
        .map(key => Buffer.from(key, 'hex')).sort(Buffer.compare)
      const redeem = script.compile([opcodes.OP_2, ...keys, opcodes.OP_2, opcodes.OP_CHECKMULTISIG])
      const expectedReward = payments.p2wsh({ redeem: { output: redeem }, network: networks.testnet }).address
      assert.equal(rewardAddress, expectedReward, 'mining reward must derive from the wallet and L1 service keys')
      await expect(page.getByText(rewardAddress, { exact: true })).toBeVisible()
      await page.getByRole('combobox').click()
      await page.getByRole('option', { name: '1', exact: true }).click()
      await page.locator('input[type="number"]').fill('100')
      await page.getByRole('checkbox').check()
      await page.getByRole('button', { name: '启动', exact: true }).click()
      await expect(page.getByRole('button', { name: '启动', exact: true })).toBeDisabled()
      await expect(page.getByRole('button', { name: '停止', exact: true })).toBeEnabled()
      await expect.poll(async () => {
        const status = await walletCall(page, 'getBTCLuckyMiningStatus')
        return status.foundBlocks?.some(record => record.submitted && record.rewardAddress === rewardAddress)
      }, poll).toBe(true)
      const running = await walletCall(page, 'getBTCLuckyMiningStatus')
      assert.equal(running.running, true)
      assert.equal(running.jobs, 1)
      assert.equal(running.lowPriority, true)
      assert.equal(running.lowPrioritySleep, '100ms')
      assert.equal(running.rewardAddress, rewardAddress)
      assert.ok(running.jobId)
      assert.match(running.bestShare, /^[0-9a-f]{64}$/)
      // With the test target, records can rotate faster than the page's refresh
      // interval. Stop through the UI before comparing a stable record set.
      await page.getByRole('button', { name: '停止', exact: true }).click()
      await expect(page.getByRole('button', { name: '启动', exact: true })).toBeEnabled()
      await expect.poll(async () => (await walletCall(page, 'getBTCLuckyMiningStatus')).running, poll).toBe(false)
      stopped = true
      const finalStatus = await walletCall(page, 'getBTCLuckyMiningStatus')
      found = finalStatus.foundBlocks.find(record => record.submitted && record.rewardAddress === rewardAddress)
      assert.ok(found)
      assert.equal(found.submitResult, 'pwa-fake-l1-pow-verified')
      const l1Evidence = await evidence()
      assert.ok(l1Evidence.jobCount > 0)
      assert.ok(l1Evidence.foundBlocks.some(record => record.blockHash === found.blockHash &&
        record.coinbaseTxid === found.coinbaseTxid && record.rewardAddress === rewardAddress && record.submitted),
      'WASM success lacks independently checked L1 proof-of-work evidence')
      await expect(page.getByText(found.blockHash, { exact: true })).toBeVisible()
    })

    await check(requiredPwaMiningCases[1], async () => {
      await expect(page.getByRole('button', { name: '启动', exact: true })).toBeEnabled()
      await expect(page.getByRole('button', { name: '停止', exact: true })).toBeDisabled()
      const afterStop = await evidence()
      // A real API round trip and page reload must not start another worker.
      await page.reload()
      await ready(page)
      await expect.poll(() => new URL(page.url()).hash.startsWith('#/unlock'), poll).toBe(true)
      const unlockURL = new URL(new URL(page.url()).hash.slice(1), new URL(page.url()).origin)
      const destination = unlockURL.searchParams.get('redirect') || '/wallet'
      await page.locator('form input[type="password"]').fill(actor.password)
      await page.locator('form button[type="submit"]').click()
      await expect.poll(() => new URL(page.url()).hash, poll).toBe('#' + destination)
      await page.evaluate(() => { location.hash = '#/wallet/btc-lucky-mining' })
      await expect(page.locator('input[type="number"]')).toHaveValue('100')
      await expect(page.getByRole('checkbox')).toBeChecked()
      await expect(page.getByRole('combobox')).toContainText('1')
      const persisted = await page.evaluate(() => JSON.parse(localStorage.getItem('btcLuckyMiningConfig')))
      assert.deepEqual(persisted, { jobs: '1', lowPriority: true, lowPrioritySleepMs: 100 })
      const status = await walletCall(page, 'getBTCLuckyMiningStatus')
      assert.equal(status.running, false)
      assert.equal(status.rewardAddress, rewardAddress)
      assert.equal((await evidence()).jobCount, afterStop.jobCount)
      assert.equal((await walletCall(page, 'getWalletAddress', 0)).address, actor.address)
    })
  } finally {
    if (!stopped && !page.isClosed()) {
      const stop = page.getByRole('button', { name: '停止', exact: true })
      if (await stop.isEnabled().catch(() => false)) await stop.click().catch(() => {})
    }
    await context.close()
  }
}
