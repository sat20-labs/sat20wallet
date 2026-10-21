import assert from 'node:assert/strict'
import { expect } from '@playwright/test'
import QRCode from 'qrcode'
import { Transaction } from 'bitcoinjs-lib'

// Four browser groups, with independent entry/recovery and identity leaves so
// one known production defect cannot hide the other authorized checks.
export const requiredPwaSilentPayCases = [
  'SP PWA: home receive exposes the private receiving method',
  'SP PWA: enable QR clipboard and reload preserve the same subaccount',
  'SP PWA: reviewed BTC send confirms once and receiver refresh discovers exact funds',
  'SP PWA: late scan cannot overwrite another selected subaccount',
  'SP PWA: network boundary rejects a public switch during a delayed scan',
  'SP PWA: locking during a delayed scan hides private receive data',
  'SP PWA: interrupted refresh preserves verified funds and retries successfully',
  'SP PWA: invalid SP checksum is rejected without broadcasting',
  'SP PWA: cancelling SP confirmation never broadcasts',
]

export async function runPwaSilentPayCases(t, fixture) {
  const { check, device, ready, walletCall, unlock } = t
  const spec = fixture.silentPay
  assert.ok(spec?.sender?.mnemonic && spec?.receiver?.mnemonic && spec?.address,
    'SP requires the existing private real-Core fixture')
  const endpoint = fixture.config.IndexerL1
  const base = `${endpoint.Scheme}://${endpoint.Host}/${endpoint.Proxy}`
  const poll = { timeout: 30000, intervals: [100, 250, 500] }
  const visit = (page, path) => page.evaluate(path => { location.hash = `#${path}` }, path)
  const identity = page => page.evaluate(() => {
    const s = window.__SAT20_PWA_VERIFY__.useWalletStore()
    return { id: String(s.walletId), index: Number(s.accountIndex), network: s.network, locked: s.locked }
  })
  const control = async action => {
    const response = await fetch(`${base}/control/${action}`, { method: 'POST', signal: AbortSignal.timeout(30000) })
    assert.ok(response.ok)
    return response.json()
  }
  const opened = []
  const importActor = async actor => {
    const page = await device(); opened.push(page.context())
    // Import is fixture setup; every SP action below uses the production UI.
    await walletCall(page, 'importWallet', actor.mnemonic, actor.password)
    await unlock(page, actor.password)
    await visit(page, '/wallet')
    await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible(poll)
    return page
  }
  const receive = async page => {
    await visit(page, '/wallet/receive')
    await page.getByRole('button', { name: 'Private · SP', exact: true }).click()
    const enable = page.getByRole('button', { name: 'Enable private BTC receive for this account', exact: true })
    await expect.poll(async () => await enable.isVisible() ||
      await page.getByRole('button', { name: 'Refresh private BTC balance', exact: true }).isVisible(), poll).toBe(true)
    if (await enable.isVisible()) await enable.click()
    await expect(page.getByRole('button', { name: 'Refresh private BTC balance', exact: true })).toBeVisible(poll)
  }
  const refresh = async page => {
    await page.getByRole('button', { name: 'Refresh private BTC balance', exact: true }).click()
    await expect(page.getByText('SP balance (confirmed snapshot)', { exact: true })).toBeVisible(poll)
    const i = await identity(page)
    const result = await walletCall(page, 'getSilentPayBalance', i.id, i.index)
    const sats = Number(result.balance.balance)
    assert.equal(result.balance.complete, true)
    await expect(page.locator('strong')).toHaveText(`${(sats / 1e8).toFixed(8)} BTC`)
    return sats
  }
  const delayedScan = async page => {
    let reached, release
    const intercepted = new Promise(resolve => { reached = resolve })
    const released = new Promise(resolve => { release = resolve })
    const pattern = '**/test/v1/sp/scan-data*'
    const handler = async route => {
      const response = await route.fetch()
      reached()
      await released
      await route.fulfill({ response }).catch(() => {})
    }
    await page.context().route(pattern, handler)
    await page.getByRole('button', { name: 'Refresh private BTC balance', exact: true }).click()
    await Promise.race([intercepted, new Promise((_, reject) => setTimeout(() => reject(new Error('SP scan request was not observed')), 15000))])
    return async () => { release(); await page.context().unroute(pattern, handler) }
  }
  const sendForm = async (page, address) => {
    await visit(page, '/wallet')
    await page.getByRole('tab', { name: 'Bitcoin', exact: true }).click()
    const panel = page.getByText('TOTAL BALANCE', { exact: true }).locator('..')
    await panel.getByRole('button', { name: 'Send', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await dialog.getByRole('button', { name: 'Normal', exact: true }).click()
    await dialog.getByPlaceholder('Enter amount', { exact: true }).fill(String(spec.amount))
    await dialog.getByPlaceholder('Enter address', { exact: true }).fill(address)
    return dialog
  }
  const actions = [
    async () => {
      const page = await importActor(spec.receiver)
      await page.getByText('TOTAL BALANCE', { exact: true }).locator('..').getByRole('button', { name: 'Receive', exact: true }).click()
      await expect(page.getByRole('button', { name: 'Private · SP', exact: true }),
        'the actual home receive entry must expose SP without a direct URL').toBeVisible(poll)
    },
    async () => {
      const page = await importActor(spec.receiver)
      await receive(page)
      await expect(page.getByText('Private BTC balance not scanned yet; zero must not be assumed.', { exact: true })).toBeVisible()
      assert.equal(await page.getByText('SP balance (confirmed snapshot)', { exact: true }).count(), 0)
      const before = await identity(page)
      const account = await walletCall(page, 'getSilentPayAccount', before.id, before.index)
      assert.equal(account.address, spec.address)
      const qr = QRCode.create(account.address, { errorCorrectionLevel: 'H' }).modules
      assert.equal(await page.locator('canvas').evaluate((canvas, expected) => {
        const pixels = canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data
        for (let y = 0; y < expected.size; y++) for (let x = 0; x < expected.size; x++) {
          const px = Math.floor((x + 0.5) * canvas.width / expected.size)
          const py = Math.floor((y + 0.5) * canvas.height / expected.size)
          if ((pixels[(py * canvas.width + px) * 4] < 128) !== Boolean(expected.data[y * expected.size + x])) return false
        }
        return true
      }, { size: qr.size, data: Array.from(qr.data) }), true, 'QR must encode the exact SP address')
      await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
      await page.locator('.w-64 button').click()
      await expect.poll(() => page.evaluate(() => navigator.clipboard.readText()), poll).toBe(account.address)
      await page.reload(); await ready(page); await unlock(page, spec.receiver.password)
      await receive(page)
      assert.deepEqual(await identity(page), before)
      const restored = await walletCall(page, 'getSilentPayAccount', before.id, before.index)
      assert.equal(restored.enabled, true); assert.equal(restored.address, account.address)
    },
    async () => {
      const receiver = await importActor(spec.receiver)
      await receive(receiver)
      const before = await refresh(receiver)
      const sender = await importActor(spec.sender)
      const dialog = await sendForm(sender, spec.address)
      const stats = await control('stats')
      const raws = []
      sender.context().on('request', request => {
        if (request.method() !== 'POST' || request.url() !== `${base}/btc/tx`) return
        const body = request.postDataJSON()
        raws.push(body.signedTxHex ?? body.SignedTxHex)
      })
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect(dialog.locator('dl')).toContainText(spec.address)
      await expect(dialog.locator('dl')).toContainText(`${spec.amount} sats`)
      await expect(dialog.locator('dl')).toContainText('regtest')
      // Dispatch two genuine clicks in the same task; production's submission
      // guard must still permit exactly one signed transaction and broadcast.
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).evaluate(button => { button.click(); button.click() })
      await expect.poll(async () => (await control('stats')).broadcasts, poll).toBe(stats.broadcasts + 1)
      assert.equal(raws.length, 1)
      const tx = Transaction.fromHex(raws[0])
      assert.equal(tx.outs[0].value, Number(spec.amount))
      const confirmed = await control('mine')
      assert.equal(confirmed.broadcasts, stats.broadcasts + 1)
      assert.equal(await refresh(receiver), before + Number(spec.amount))
      assert.equal(await refresh(receiver), before + Number(spec.amount), 'repeat refresh must not double-credit')
      const i = await identity(receiver)
      const balance = await walletCall(receiver, 'getSilentPayBalance', i.id, i.index)
      assert.ok(JSON.stringify(balance).includes(tx.getId()), 'displayed balance must include the real Core-confirmed payment')
    },
    async () => {
      const page = await importActor(spec.receiver)
      await receive(page)
      const release = await delayedScan(page)
      try {
        await page.evaluate(async () => {
          const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
          const [error] = await window.sat20.switchAccount(1)
          if (error) throw error
          await store.setAccountIndex(1)
          await store.syncWalletCatalog()
        })
      } finally { await release() }
      await expect(page.getByRole('button', { name: 'Enable private BTC receive for this account', exact: true })).toBeVisible(poll)
      assert.equal(await page.locator('strong').count(), 0)
      assert.equal(await page.locator('canvas').count(), 0)
    },
    async () => {
      const page = await importActor(spec.receiver)
      await receive(page)
      const release = await delayedScan(page)
      try {
        const rejected = await page.evaluate(async () => {
          const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
          try { await store.setNetwork('mainnet'); return false } catch { return store.network === 'regtest' }
        })
        assert.equal(rejected, true, 'isolated regtest UI must reject a public-network switch')
      } finally { await release() }
      await expect(page.getByRole('button', { name: 'Refresh private BTC balance', exact: true })).toBeEnabled(poll)
      assert.equal((await identity(page)).network, 'regtest')
    },
    async () => {
      const page = await importActor(spec.receiver)
      await receive(page)
      const release = await delayedScan(page)
      try { await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().lockWallet()) }
      finally { await release() }
      assert.equal((await identity(page)).locked, true)
      await expect(page.getByRole('button', { name: 'Refresh private BTC balance', exact: true })).toHaveCount(0)
      await unlock(page, spec.receiver.password)
      await receive(page)
      await refresh(page)
    },
    async () => {
      const page = await importActor(spec.receiver)
      await receive(page)
      const before = await refresh(page)
      assert.ok(before > 0, 'failure test requires a real previously confirmed SP payment')
      const pattern = '**/test/v1/sp/scan-data*'
      const abort = route => route.abort('connectionfailed')
      await page.context().route(pattern, abort)
      await page.getByRole('button', { name: 'Refresh private BTC balance', exact: true }).click()
      await expect(page.getByRole('alert')).toBeVisible(poll)
      await expect(page.locator('strong')).toHaveText(`${(before / 1e8).toFixed(8)} BTC`)
      await expect(page.getByRole('button', { name: 'Refresh private BTC balance', exact: true })).toBeEnabled()
      await page.context().unroute(pattern, abort)
      assert.equal(await refresh(page), before)
    },
    async () => {
      const page = await importActor(spec.sender)
      const stats = await control('stats')
      let dialog = await sendForm(page, spec.address.slice(0, -1) + '!')
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      // This UI delegates checksum validation to the actual SDK submission.
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect(page.getByText(/checksum|invalid.*address|invalid.*character|decode/i).first()).toBeVisible(poll)
      assert.equal((await control('stats')).broadcasts, stats.broadcasts)
    },
    async () => {
      const page = await importActor(spec.sender)
      const stats = await control('stats')
      const dialog = await sendForm(page, spec.address)
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect(dialog.getByRole('heading', { name: 'Please Confirm', exact: true })).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(dialog).toBeHidden()
      assert.equal((await control('stats')).broadcasts, stats.broadcasts)
    },
  ]
  const failures = []
  for (const [index, action] of actions.entries()) {
    const name = requiredPwaSilentPayCases[index]
    if (t.selectedCases && !t.selectedCases.includes(name)) continue
    try { await check(name, action) }
    catch (error) { failures.push(`${name}: ${error.message}`) }
    finally { for (const context of opened.splice(0)) await context.close() }
  }
  assert.deepEqual(failures, [], 'SP browser groups retain all independent production failures')
}
