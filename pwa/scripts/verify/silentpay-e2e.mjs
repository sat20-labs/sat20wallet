import assert from 'node:assert/strict'
import { expect } from '@playwright/test'
import QRCode from 'qrcode'
import { Transaction } from 'bitcoinjs-lib'

const homeReceiveCase = 'SP PWA: home receive exposes the private receiving method'
const confirmedSendCase = 'SP PWA: reviewed BTC send confirms once and receiver refresh discovers exact funds'
const sharedAddressCase = 'SP PWA: two wallets pay the same shared address and both outputs are found'
export const requiredPwaRegtestSPCases = [homeReceiveCase, confirmedSendCase, sharedAddressCase]

// Four browser groups, with independent entry/recovery and identity leaves so
// one known production defect cannot hide the other authorized checks.
export const requiredPwaSilentPayCases = [
  homeReceiveCase,
  'SP PWA: enable QR clipboard and reload preserve the same subaccount',
  confirmedSendCase,
  'SP PWA: late scan cannot overwrite another selected subaccount',
  'SP PWA: network boundary rejects a public switch during a delayed scan',
  'SP PWA: locking during a delayed scan hides private receive data',
  'SP PWA: interrupted refresh preserves verified funds and retries successfully',
  'SP PWA: invalid SP checksum is rejected without broadcasting',
  'SP PWA: cancelling SP confirmation never broadcasts',
  sharedAddressCase,
]

export async function runPwaSilentPayCases(t, fixture) {
  const { check, device, ready, walletCall, accountCall, unlock } = t
  const spec = fixture.silentPay
  assert.ok(spec?.sender?.mnemonic && spec?.receiver?.mnemonic && spec?.address,
    'SP requires the existing private real-Core fixture')
  const endpoint = fixture.config.IndexerL1
  const base = `${endpoint.Scheme}://${endpoint.Host}/${endpoint.Proxy}`
  const scanURL = `${base}/v1/sp/scan-data`
  const scanPattern = `${scanURL}*`
  const broadcastURL = `${base}/btc/tx`
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
  const bitcoinControl = async (action, body) => {
    const response = await fetch(new URL(action, fixture.control_url), {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body), signal: AbortSignal.timeout(120000),
    })
    assert.ok(response.ok, `Bitcoin control ${action} failed with HTTP ${response.status}`)
    const state = await response.json()
    assert.ok(!state.error, state.error)
    return state.l1
  }
  const opened = []
  const importActor = async actor => {
    const page = await device(); opened.push(page.context())
    if (fixture.realBitcoinControl) {
      // These actors were backed up during group preparation. The import page
      // discovers and restores their account before enabling SP metadata.
      await visit(page, '/import')
      await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(actor.mnemonic)
      await page.getByLabel('New Wallet Password', { exact: true }).fill(actor.password)
      await page.getByLabel('Confirm Password', { exact: true }).fill(actor.password)
      await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
    } else {
      await walletCall(page, 'importWallet', actor.mnemonic, actor.password)
      await unlock(page, actor.password)
      await visit(page, '/wallet')
    }
    await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible(poll)
    await expect.poll(() => page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().address), poll).toBe(actor.address)
    if (fixture.realBitcoinControl && actor === spec.receiver) {
      await expect.poll(async () => {
        const view = await accountCall(page, 'getAccountView')
        return view.state
      }, { ...poll, message: 'receiver must restore its prepared account before enabling private receive' }).toBe('ready')
    }
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
  const readBalance = async page => {
    const i = await identity(page)
    const { balance } = await walletCall(page, 'getSilentPayBalance', i.id, i.index)
    assert.equal(balance.complete, true)
    return balance
  }
  const refresh = async page => {
    const response = page.waitForResponse(r => r.url().split('?')[0] === scanURL, { timeout: 30000 })
    await page.getByRole('button', { name: 'Refresh private BTC balance', exact: true }).click()
    await response
    // A previous complete balance stays visible while a new scan runs. Wait
    // for this scan's UI completion before reading its persisted SDK result.
    await expect(page.getByRole('button', { name: 'Refresh private BTC balance', exact: true })).toBeEnabled(poll)
    await expect(page.getByRole('alert')).toHaveCount(0)
    await expect(page.getByText('SP balance (confirmed snapshot)', { exact: true })).toBeVisible(poll)
    const { balance } = await readBalance(page)
    const sats = Number(balance)
    await expect(page.locator('strong')).toHaveText(`${(sats / 1e8).toFixed(8)} BTC`)
    return sats
  }
  const delayedScan = async page => {
    let reached, release
    const intercepted = new Promise(resolve => { reached = resolve })
    const released = new Promise(resolve => { release = resolve })
    const handler = async route => {
      const response = await route.fetch()
      reached()
      await released
      await route.fulfill({ response }).catch(() => {})
    }
    await page.context().route(scanPattern, handler)
    await page.getByRole('button', { name: 'Refresh private BTC balance', exact: true }).click()
    await Promise.race([intercepted, new Promise((_, reject) => setTimeout(() => reject(new Error('SP scan request was not observed')), 15000))])
    return async () => { release(); await page.context().unroute(scanPattern, handler) }
  }
  const sendForm = async (page, address, amount = spec.amount) => {
    await visit(page, '/wallet?tab=l1')
    await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toHaveAttribute('aria-selected', 'true', poll)
    const panel = page.getByText('TOTAL BALANCE', { exact: true }).locator('..')
    // The imported sender has real confirmed funding. Wait for the home asset
    // query before clicking: the button is visible before native sats load.
    await expect.poll(async () => Number((await panel.locator('h2').innerText()).split(' ')[0]), poll).toBeGreaterThan(0)
    await panel.getByRole('button', { name: 'Send', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await dialog.getByRole('button', { name: 'Normal', exact: true }).click()
    await dialog.getByPlaceholder('Enter amount', { exact: true }).fill(String(amount))
    await dialog.getByPlaceholder('Enter address', { exact: true }).fill(address)
    return dialog
  }
  const pay = async (sender, address, amount) => {
    const dialog = await sendForm(sender, address, amount)
    const raws = []
    const isBroadcast = request => request.method() === 'POST' && request.url() === broadcastURL
    sender.context().on('request', request => {
      if (!isBroadcast(request)) return
      const body = request.postDataJSON()
      raws.push(body.signedTxHex ?? body.SignedTxHex)
    })
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await expect(dialog.locator('dl')).toContainText(address)
    await expect(dialog.locator('dl')).toContainText(`${amount} sats`)
    await expect(dialog.locator('dl')).toContainText('regtest')
    assert.equal(raws.length, 0, 'review alone must not broadcast')
    const broadcast = sender.waitForResponse(r => isBroadcast(r.request()), { timeout: 30000 })
    // Two confirmation clicks still submit exactly one transaction.
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).evaluate(button => { button.click(); button.click() })
    const accepted = await (await broadcast).json()
    assert.equal(accepted.code, 0, 'actual Core broadcast must succeed before mining')
    assert.equal(raws.length, 1)
    const tx = Transaction.fromHex(raws[0])
    const txid = tx.getId()
    assert.equal(accepted.data, txid)
    assert.equal(Number(tx.outs[0].value), Number(amount))
    return { txid, amount: Number(amount), script: Buffer.from(tx.outs[0].script).toString('hex'), raws }
  }
  const confirmPayments = async (before, txids) => {
    const pending = await bitcoinControl('/snapshot', {})
    assert.deepEqual(pending.pending_txids, txids.slice().sort())
    // Leave two 72-block history windows for the SP snapshot to commit.
    const confirmed = await bitcoinControl('/confirm-l1', { wait_anchors: false, empty_blocks: 1 + 2 * 72 })
    assert.deepEqual(confirmed.pending_txids, [])
    assert.deepEqual(confirmed.confirmed_txids.filter(id => !before.confirmed_txids.includes(id)), txids.slice().sort())
    await expect.poll(async () => {
      const response = await fetch(`${base}/v1/sp/status`, { signal: AbortSignal.timeout(5000) })
      assert.ok(response.ok, 'the real indexer must serve SP status')
      const status = await response.json()
      return status.data_ready ? status.base_anchor.height : -1
    }, { ...poll, message: 'committed SP snapshot must cover the confirmed payment block' }).toBeGreaterThan(pending.height)
  }
  const assertPayment = (balance, { txid, amount }) => {
    const matches = balance.outputs.filter(output => output.txid === txid)
    assert.equal(matches.length, 1, 'each payment must be recorded exactly once')
    const [out] = matches
    assert.equal(out.vout, 0)
    assert.equal(out.value, amount)
    assert.equal(out.unspent, true)
  }
  const actions = [
    async () => {
      const page = await importActor(spec.receiver)
      const before = await identity(page)
      const openReceive = () => page.getByText('TOTAL BALANCE', { exact: true }).locator('..').getByRole('button', { name: 'Receive', exact: true }).click()
      await openReceive()
      await expect(page.getByRole('button', { name: 'Private · SP', exact: true }),
        'the actual home receive entry must expose SP without a direct URL').toBeVisible(poll)
      // The existing ordinary receive flow must still display its QR and copy
      // the actual ordinary BTC address before navigating to the SP page.
      await expect(page.locator('canvas')).toBeVisible()
      await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
      await page.getByRole('button', { name: 'Copy Address', exact: true }).click()
      await expect.poll(() => page.evaluate(() => navigator.clipboard.readText()), poll).toBe(spec.receiver.address)
      await page.getByRole('button', { name: '✕', exact: true }).click()

      await page.getByRole('tab', { name: 'SatoshiNet', exact: true }).click()
      await expect(page.getByRole('tab', { name: 'SatoshiNet', exact: true })).toHaveAttribute('aria-selected', 'true', poll)
      await openReceive()
      await expect(page.getByRole('button', { name: 'Copy Address', exact: true })).toBeVisible()
      await expect(page.locator('canvas')).toBeVisible()
      await expect(page.getByRole('button', { name: 'Private · SP', exact: true })).toHaveCount(0)
      await page.getByRole('button', { name: '✕', exact: true }).click()

      await page.getByRole('tab', { name: 'Bitcoin', exact: true }).click()
      await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toHaveAttribute('aria-selected', 'true', poll)
      await openReceive()
      await page.getByRole('button', { name: 'Private · SP', exact: true }).click()
      await expect(page).toHaveURL(/#\/wallet\/receive$/)
      await expect(page.getByRole('button', { name: 'Copy Address', exact: true })).toHaveCount(0)
      await expect(page.getByRole('button', { name: 'Ordinary BTC', exact: true })).toBeVisible()
      // Select the existing page's private receiving method through the UI.
      await page.getByRole('button', { name: 'Private · SP', exact: true }).click()
      const enable = page.getByRole('button', { name: 'Enable private BTC receive for this account', exact: true })
      await expect(enable).toBeVisible(poll)
      await enable.click()
      await expect(page.getByRole('button', { name: 'Refresh private BTC balance', exact: true })).toBeVisible(poll)
      assert.deepEqual(await identity(page), before)
      const account = await walletCall(page, 'getSilentPayAccount', before.id, before.index)
      assert.equal(account.enabled, true)
      assert.equal(account.address, spec.address)
    },
    async () => {
      const page = await importActor(spec.receiver)
      await receive(page)
      await expect(page.getByText('Private BTC balance not scanned yet; zero must not be assumed.', { exact: true })).toBeVisible()
      assert.equal(await page.getByText('SP balance (confirmed snapshot)', { exact: true }).count(), 0)
      const before = await identity(page)
      const account = await walletCall(page, 'getSilentPayAccount', before.id, before.index)
      assert.equal(account.address, spec.address)
      // Independent encoders can choose different legal masks and boost error
      // correction. Every candidate still encodes this exact address, in byte
      // mode, and every rendered module must match a complete candidate.
      const candidates = ['L', 'M', 'Q', 'H'].flatMap(errorCorrectionLevel =>
        Array.from({ length: 8 }, (_, maskPattern) => {
          const qr = QRCode.create([{ data: account.address, mode: 'byte' }], { errorCorrectionLevel, maskPattern }).modules
          return { size: qr.size, data: Array.from(qr.data) }
        }))
      await expect.poll(() => page.locator('canvas').evaluate((canvas, candidates) => {
        const pixels = canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data
        return candidates.some(expected => {
          for (let y = 0; y < expected.size; y++) for (let x = 0; x < expected.size; x++) {
            const px = Math.floor((x + 0.5) * canvas.width / expected.size)
            const py = Math.floor((y + 0.5) * canvas.height / expected.size)
            if ((pixels[(py * canvas.width + px) * 4] < 128) !== Boolean(expected.data[y * expected.size + x])) return false
          }
          return true
        })
      }, candidates), { ...poll, message: 'QR must encode the exact SP address' }).toBe(true)
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
      const realBitcoin = fixture.realBitcoinControl
      const stats = realBitcoin ? await bitcoinControl('/snapshot', {}) : await control('stats')
      if (realBitcoin) assert.deepEqual(stats.pending_txids, [], 'earlier Bitcoin cases must leave no pending transactions')
      const payment = await pay(sender, spec.address, spec.amount)
      if (realBitcoin) {
        await confirmPayments(stats, [payment.txid])
      } else {
        await expect.poll(async () => (await control('stats')).broadcasts, poll).toBe(stats.broadcasts + 1)
        const confirmed = await control('mine')
        assert.equal(confirmed.broadcasts, stats.broadcasts + 1)
      }
      assert.equal(await refresh(receiver), before + payment.amount, 'receiver scan must find the confirmed payment')
      assert.equal(await refresh(receiver), before + payment.amount, 'repeat refresh must not double-credit')
      assertPayment(await readBalance(receiver), payment)
      assert.equal(payment.raws.length, 1, 'the full journey must broadcast only once')
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
      const abort = route => route.abort('connectionfailed')
      await page.context().route(scanPattern, abort)
      await page.getByRole('button', { name: 'Refresh private BTC balance', exact: true }).click()
      await expect(page.getByRole('alert')).toBeVisible(poll)
      await expect(page.locator('strong')).toHaveText(`${(before / 1e8).toFixed(8)} BTC`)
      await expect(page.getByRole('button', { name: 'Refresh private BTC balance', exact: true })).toBeEnabled()
      await page.context().unroute(scanPattern, abort)
      assert.equal(await refresh(page), before)
    },
    async () => {
      const page = await importActor(spec.sender)
      const stats = await control('stats')
      const badChecksum = spec.address.slice(0, -1) + (spec.address.endsWith('q') ? 'p' : 'q')
      const dialog = await sendForm(page, badChecksum)
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
    async () => {
      assert.ok(fixture.realBitcoinControl && spec.secondSender, 'a second funded wallet is required')
      const receiver = await importActor(spec.receiver)
      await receive(receiver)
      const before = await refresh(receiver)
      await receiver.context().grantPermissions(['clipboard-read', 'clipboard-write'])
      await receiver.locator('.w-64 button').click()
      const copiedAddress = () => receiver.evaluate(() => navigator.clipboard.readText())
      await expect.poll(copiedAddress, poll).toBe(spec.address)
      const shared = await copiedAddress()

      const stats = await bitcoinControl('/snapshot', {})
      assert.deepEqual(stats.pending_txids, [])
      const first = await importActor(spec.sender)
      const second = await importActor(spec.secondSender)
      assert.equal(new Set([spec.sender.address, spec.secondSender.address, spec.receiver.address]).size, 3)
      const payments = [await pay(first, shared, 3000), await pay(second, shared, 4500)]
      const [a, b] = payments
      assert.notEqual(a.txid, b.txid)
      assert.notEqual(a.script, b.script, 'each sender must produce its own BIP352 output key')
      await confirmPayments(stats, payments.map(payment => payment.txid))
      const total = a.amount + b.amount
      assert.equal(await refresh(receiver), before + total)
      assert.equal(await refresh(receiver), before + total, 'repeat scan must not duplicate either payment')
      const balance = await readBalance(receiver)
      for (const payment of payments) {
        assertPayment(balance, payment)
        assert.equal(payment.raws.length, 1)
      }
      console.log(JSON.stringify({ event: 'sp-multiple-payments', baseline: before,
        payments: payments.map(({ txid, amount }) => ({ txid, amount })) }))
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
