import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { cpus } from 'node:os'
import { expect } from '@playwright/test'
import { script as bitcoinScript } from 'bitcoinjs-lib'
import * as secp256k1 from '@bitcoin-js/tiny-secp256k1-asmjs'
import QRCode from 'qrcode'

// The account/recovery suites remain part of ordinary SDK E2E. This module
// adds the basic wallet pages that those suites do not exercise end to end.
export const requiredPwaWalletBasicCases = [
  'Wallet PWA: create through the page and unlock the same identity after reload',
  'Wallet PWA: import a funded test wallet through the page',
  'Wallet PWA: receive address and clipboard match the active wallet',
  'Wallet PWA: public key and peer channel address match the SDK',
  'Wallet PWA: recovery phrase requires authentication and clears on navigation',
  'Wallet PWA: balance visibility and auto-lock preference survive reload',
  'Wallet PWA: language changes persist without changing wallet identity',
  'Wallet PWA: manually locked Bitcoin UTXO survives reload and can be unlocked',
  'Wallet PWA: signature review signs the displayed payload with the active key',
  'Wallet PWA: cancelling a node stake leaves funds and local stake state unchanged',
  'Wallet PWA: operation logs retain real results and can be cleared without deleting the wallet',
  'Wallet PWA: configured inactivity locks signing until password authentication',
  'Wallet PWA: referrer registration without an owned name cannot broadcast',
  'Wallet PWA: referrer cancellation and unknown-name rejection preserve funds',
  'Wallet PWA: late asset response cannot overwrite the selected account',
]

const poll = { timeout: 120000, intervals: [250, 500, 1000] }

export async function runPwaWalletBasicCases(t, fixture) {
  const { check, device, ready, walletCall } = t
  const actor = fixture.basicWallet
  const network = fixture.config?.Chain ?? 'testnet'
  const btcUnit = network === 'mainnet' ? 'BTC' : network === 'regtest' ? 'rBTC' : 'tBTC'
  assert.ok(actor?.mnemonic && actor.address && actor.password, 'funded basic wallet fixture is required')
  const contexts = []
  const fresh = async () => {
    const page = await device()
    contexts.push(page.context())
    return page
  }
  const visit = async (page, path) => {
    await page.evaluate(path => { location.hash = `#${path}` }, path)
    await expect.poll(() => new URL(page.url()).hash, poll).toBe(`#${path}`)
  }
  const identity = page => page.evaluate(() => {
    const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
    return { address: store.address, publicKey: store.publicKey, walletId: String(store.walletId),
      accountIndex: Number(store.accountIndex), network: store.network, locked: store.locked }
  })
  const unlockPage = async (page, password) => {
    await expect.poll(() => new URL(page.url()).hash.startsWith('#/unlock'), poll).toBe(true)
    const unlockURL = new URL(new URL(page.url()).hash.slice(1), new URL(page.url()).origin)
    const destination = unlockURL.searchParams.get('redirect') || '/wallet'
    await page.locator('form input[type="password"]').fill(password)
    await page.locator('form button[type="submit"]').click()
    await expect.poll(async () => (await identity(page)).locked, poll).toBe(false)
    // Unlock commits identity before its final router redirect. Wait for the
    // requested page before a caller starts navigating to the next test page.
    await expect.poll(() => new URL(page.url()).hash, poll).toBe('#' + destination)
  }
  const reloadUnlock = async (page, password) => {
    await page.reload()
    await ready(page)
    await unlockPage(page, password)
  }
  const settings = async page => {
    await visit(page, '/wallet/setting')
    await expect(page.getByRole('heading', { name: 'System Settings', exact: true })).toBeVisible()
  }
  const walletBalance = page => page.getByText('TOTAL BALANCE', { exact: true }).locator('..').getByRole('heading', { level: 2 })
  const createdPassword = 'Pwa-create-only-2026!'
  let createdPage
  let page
  let originalIdentity

  try {
    await check(requiredPwaWalletBasicCases[0], async () => {
      const created = await fresh()
      createdPage = created
      const password = createdPassword
      await visit(created, '/create')
      await created.getByLabel('Password', { exact: true }).fill(password)
      await created.getByLabel('Confirm Password', { exact: true }).fill(password)
      await created.getByRole('button', { name: 'Continue', exact: true }).click()
      await expect(created.getByRole('heading', { name: 'Save your recovery phrase', exact: true })).toBeVisible()
      const words = await created.locator('.grid.grid-cols-3 > div > span:last-child').allTextContents()
      assert.equal(words.length, 12, 'creation page must expose a complete recovery phrase')
      assert.ok(words.every(word => /^[a-z]+$/.test(word.trim())))
      const derived = await walletCall(created, 'validateMnemonic', words.map(word => word.trim()).join(' '), '')
      const committed = (await walletCall(created, 'getWalletCatalog')).wallets
      assert.equal(committed.length, 1)
      assert.equal(derived.fingerprint, committed[0].fingerprint, 'saved phrase must derive the newly created wallet')
      await created.getByRole('link', { name: "I've saved my recovery phrase", exact: true }).click()
      await expect(created.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible()
      const before = await identity(created)
      assert.equal(before.locked, false)
      assert.equal(before.network, network)
      assert.equal((await walletCall(created, 'getWalletCatalog')).wallets.length, 1)
      assert.equal((await walletCall(created, 'getWalletAddress', 0)).address, before.address)
      await reloadUnlock(created, password)
      assert.deepEqual(await identity(created), before, 'reload changed the newly created wallet')
      // Reuse this already-created identity for the no-owned-name negative
      // case; the funded fixture may legitimately own names on a real chain.
    })

    await check(requiredPwaWalletBasicCases[1], async () => {
      page = await fresh()
      await visit(page, '/import')
      await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(actor.mnemonic)
      await page.getByLabel('New Wallet Password', { exact: true }).fill(actor.password)
      await page.getByLabel('Confirm Password', { exact: true }).fill(actor.password)
      await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
      await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: poll.timeout })
      originalIdentity = await identity(page)
      assert.equal(originalIdentity.address, actor.address)
      assert.equal(originalIdentity.network, network)
      assert.equal(originalIdentity.locked, false)
      assert.equal((await walletCall(page, 'getWalletAddress', 0)).address, actor.address)
      assert.equal((await walletCall(page, 'getWalletPubkey', 0)).pubKey, originalIdentity.publicKey)
      await expect.poll(async () => {
        const amount = await walletCall(page, 'getAssetAmount', actor.address, '::')
        return BigInt(String(amount.availableAmt)) > 0n
      }, poll).toBe(true)
      const totalSats = await page.evaluate(async ({ address, network }) => {
        const api = (await import('/apis/ordx.ts')).default
        const result = await api.getAddressSummary({ address, network })
        if (result.code !== 0) throw new Error(result.msg)
        const total = result.data.find(asset => asset.Name.Protocol === '' && asset.Name.Type === '*')
        if (!total) throw new Error('L1 summary omitted the native total')
        return Number(total.Amount)
      }, { address: actor.address, network })
      assert.ok(Number.isSafeInteger(totalSats) && totalSats > 0)
      const balanceWaitStarted = Date.now()
      await expect(walletBalance(page)).toHaveText((totalSats / 1e8).toFixed(8) + ` ${btcUnit}`, { timeout: poll.timeout })
      console.log(JSON.stringify({ phase: 'wallet-basic-native-balance-ready', wait_ms: Date.now() - balanceWaitStarted }))
    })

    await check(requiredPwaWalletBasicCases[2], async () => {
      await page.context().grantPermissions(['clipboard-read', 'clipboard-write'], { origin: new URL(page.url()).origin })
      await visit(page, '/wallet')
      const actions = page.getByText('TOTAL BALANCE', { exact: true }).locator('..')
      await actions.getByRole('button', { name: 'Receive', exact: true }).click()
      await expect(page.getByRole('heading', { name: 'Receive Address', exact: true })).toBeVisible()
      const canvas = page.locator('canvas').last()
      await expect(canvas).toBeVisible()
      const expectedQR = QRCode.create(actor.address)
      await expect.poll(() => canvas.evaluate((element, expected) => {
        const ctx = element.getContext('2d')
        if (!ctx || element.width !== 256 || element.height !== 256) return false
        const pixels = ctx.getImageData(0, 0, element.width, element.height).data
        const count = expected.size
        let compared = 0
        for (let y = 0; y < count; y++) {
          for (let x = 0; x < count; x++) {
            // ReceiveQRCode decorates the center with a wallet icon. Compare
            // the actual address-encoding modules outside that overlay.
            if (Math.abs(x + 0.5 - count / 2) < count * 0.2 &&
                Math.abs(y + 0.5 - count / 2) < count * 0.2) continue
            const px = Math.floor((x + 4.5) * element.width / (count + 8))
            const py = Math.floor((y + 4.5) * element.height / (count + 8))
            const offset = (py * element.width + px) * 4
            if (pixels[offset + 3] !== 255) return false
            const dark = pixels[offset + 1] < 128
            if (dark !== Boolean(expected.data[y * count + x])) return false
            compared++
          }
        }
        return compared > 100
      }, { size: expectedQR.modules.size, data: Array.from(expectedQR.modules.data) }), poll).toBe(true)
      await page.getByRole('button', { name: 'Copy Address', exact: true }).click()
      await expect.poll(() => page.evaluate(() => navigator.clipboard.readText()), poll).toBe(actor.address)
      await page.getByRole('button', { name: '✕', exact: true }).click()
      await expect(canvas).toBeHidden()
      await visit(page, '/wallet/receive')
      await expect(page.locator('canvas')).toBeVisible()
      assert.deepEqual(await identity(page), originalIdentity)
    })

    await check(requiredPwaWalletBasicCases[3], async () => {
      await visit(page, '/wallet/setting/publickey')
      await expect(page.getByText(originalIdentity.publicKey, { exact: true })).toBeVisible()
      const peerKey = fixture.config.Peers.find(peer => peer.startsWith('s@')).split('@')[1]
      assert.match(peerKey, /^(02|03)[0-9a-f]{64}$/)
      await page.getByLabel('请输入服务节点公钥（hex）', { exact: true }).fill(peerKey)
      await page.getByRole('button', { name: '计算通道公钥', exact: true }).click()
      const expected = await walletCall(page, 'getChannelAddrByPeerPubkey', peerKey)
      assert.ok(expected.channelAddr && expected.peerAddr)
      await expect(page.getByText(expected.channelAddr, { exact: true })).toBeVisible()
      await expect(page.getByText(expected.peerAddr, { exact: true })).toBeVisible()
    })

    await check(requiredPwaWalletBasicCases[4], async () => {
      await visit(page, '/wallet/setting/phrase')
      const words = () => page.locator('.grid.grid-cols-3 > div > span.font-medium')
      await expect(words()).toHaveCount(0)
      await page.getByPlaceholder('Enter password to verify', { exact: true }).fill('incorrect-test-password')
      await page.getByRole('button', { name: 'Verify', exact: true }).click()
      await expect(page.getByText('Verification failed', { exact: true })).toBeVisible()
      await expect(words()).toHaveCount(0)
      await page.getByPlaceholder('Enter password to verify', { exact: true }).fill(actor.password)
      await page.getByRole('button', { name: 'Verify', exact: true }).click()
      await expect(words()).toHaveCount(12)
      assert.equal((await words().allTextContents()).map(word => word.trim()).join(' '), actor.mnemonic)
      await visit(page, '/wallet')
      await visit(page, '/wallet/setting/phrase')
      await expect(words()).toHaveCount(0)
      await expect(page.getByPlaceholder('Enter password to verify', { exact: true })).toHaveValue('')
    })

    await check(requiredPwaWalletBasicCases[5], async () => {
      await settings(page)
      await page.getByRole('button', { name: /Security Options/ }).click()
      const security = page.getByRole('heading', { name: 'Security Options', exact: true }).locator('../..').locator('..')
      await security.getByRole('combobox').click()
      await page.getByRole('option', { name: '15 minutes', exact: true }).click()
      await security.getByRole('button', { name: 'Hide Balance', exact: true }).click()
      await reloadUnlock(page, actor.password)
      await visit(page, '/wallet')
      await expect(walletBalance(page)).toHaveText('••••••••')
      const persisted = await page.evaluate(async () => {
        const { walletStorage } = await import('/lib/walletStorage.ts')
        return { autoLockTime: walletStorage.getValue('autoLockTime'), hideBalance: walletStorage.getValue('hideBalance') }
      })
      assert.deepEqual(persisted, { autoLockTime: '15', hideBalance: true })
      await settings(page)
      await page.getByRole('button', { name: /Security Options/ }).click()
      await page.getByRole('button', { name: 'Show Balance', exact: true }).click()
      await visit(page, '/wallet')
      await expect(walletBalance(page)).not.toHaveText('••••••••')
    })

    await check(requiredPwaWalletBasicCases[6], async () => {
      await settings(page)
      await page.getByRole('button', { name: /Environment Options/ }).click()
      const environment = page.getByRole('heading', { name: 'Environment Options', exact: true }).locator('../..').locator('..')
      await environment.getByRole('combobox').nth(1).click()
      await page.getByRole('option', { name: '中文', exact: true }).click()
      await expect(page.getByRole('heading', { name: 'Environment Options', exact: true })).toBeHidden()
      await reloadUnlock(page, actor.password)
      const language = await page.evaluate(async () => (await import('/lib/walletStorage.ts')).walletStorage.getValue('language'))
      assert.equal(language, 'zh')
      assert.deepEqual(await identity(page), originalIdentity)
      // The environment selector remains second in this section in both
      // languages; use its exact visible selected language to reopen it.
      await visit(page, '/wallet/setting')
      const heading = page.getByRole('heading').filter({ hasText: '环境' })
      await heading.locator('../..').click()
      await page.getByRole('combobox').filter({ hasText: '中文' }).click()
      await page.getByRole('option', { name: 'English', exact: true }).click()
      await expect(page.getByRole('heading', { name: 'Environment Options', exact: true })).toBeVisible()
    })

    await check(requiredPwaWalletBasicCases[7], async () => {
      const selected = await walletCall(page, 'getUtxosWithAsset', actor.address, '1000', '::')
      assert.ok(selected.utxos?.length)
      const outpoint = selected.utxos[0]
      assert.match(outpoint, /^[0-9a-f]{64}:\d+$/)
      const before = await walletCall(page, 'getAllLockedUtxo', actor.address)
      assert.ok(!Object.hasOwn(before, outpoint))
      await visit(page, '/wallet/setting/utxo')
      await page.getByPlaceholder('Enter UTXO (e.g. txid:vout)', { exact: true }).fill(outpoint)
      await page.getByRole('button', { name: 'Lock', exact: true }).click()
      await expect.poll(async () => Object.hasOwn(await walletCall(page, 'getAllLockedUtxo', actor.address), outpoint), poll).toBe(true)
      await expect(page.getByRole('row').filter({ hasText: 'manual' })).toHaveCount(1)
      await reloadUnlock(page, actor.password)
      await visit(page, '/wallet/setting/utxo')
      assert.ok(Object.hasOwn(await walletCall(page, 'getAllLockedUtxo', actor.address), outpoint))
      await page.getByRole('row').filter({ hasText: 'manual' }).getByRole('button', { name: 'Unlock', exact: true }).click()
      await expect.poll(async () => Object.hasOwn(await walletCall(page, 'getAllLockedUtxo', actor.address), outpoint), poll).toBe(false)
      // Existing RGB and other protection locks are legitimate. Removing this
      // manual lock must restore the complete original lock set exactly.
      assert.deepEqual(await walletCall(page, 'getAllLockedUtxo', actor.address), before,
        'manual unlock changed another UTXO protection lock')
      if (Object.keys(before).length === 0) {
        await expect(page.getByText('No locked UTXOs', { exact: true })).toBeVisible()
      } else {
        await expect(page.locator('tbody tr')).toHaveCount(Object.keys(before).length)
        await expect(page.getByText('No locked UTXOs', { exact: true })).toBeHidden()
      }
    })

    await check(requiredPwaWalletBasicCases[8], async () => {
      await visit(page, '/wallet/agent-sign-data')
      const payload = JSON.stringify({ protocol: 'pwa-functional-acceptance', action: 'readiness', address: actor.address, nonce: 'basic-1' })
      await page.getByPlaceholder('Raw protocol JSON', { exact: true }).fill(payload)
      await page.getByRole('button', { name: 'Review signature', exact: true }).click()
      const review = page.getByRole('region', { name: 'Signature confirmation', exact: true })
      await expect(review).toContainText(actor.address)
      await expect(review).toContainText(payload)
      await review.getByRole('button', { name: 'Cancel', exact: true }).click()
      await expect(review).toBeHidden()
      await expect(page.locator('main > pre')).toHaveCount(0)
      await page.getByRole('button', { name: 'Review signature', exact: true }).click()
      await review.getByRole('button', { name: 'Confirm and sign', exact: true }).click()
      const output = page.locator('main > pre')
      await expect(output).toBeVisible()
      const { signature } = JSON.parse(await output.textContent())
      assert.match(signature, /^30[0-9a-f]+$/)
      const compact = bitcoinScript.signature.decode(Buffer.concat([Buffer.from(signature, 'hex'), Buffer.from([1])])).signature
      const digest = createHash('sha256').update(payload).digest()
      assert.equal(secp256k1.verify(digest, Buffer.from(originalIdentity.publicKey, 'hex'), compact), true,
        'displayed payload signature does not verify under the active wallet public key')
    })

    await check(requiredPwaWalletBasicCases[9], async () => {
      const before = await walletCall(page, 'getAssetAmount', actor.address, '::')
      const savedStake = () => page.evaluate(async pubkey => (await import('/lib/nodeStakeStorage.ts')).nodeStakeStorage.getNodeStakeData(pubkey), originalIdentity.publicKey)
      const beforeStake = await savedStake()
      await visit(page, '/wallet/setting/node')
      for (const role of ['become Core Node', 'become Miner']) {
        await page.getByRole('button', { name: role, exact: true }).click()
        const dialog = page.getByRole('dialog')
        await expect(dialog).toContainText('This action will trigger an on-chain staking transaction!')
        await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
        await expect(dialog).toBeHidden()
      }
      assert.deepEqual(await savedStake(), beforeStake)
      assert.deepEqual(await walletCall(page, 'getAssetAmount', actor.address, '::'), before)
    })

    await check(requiredPwaWalletBasicCases[10], async () => {
      await reloadUnlock(page, actor.password)
      await visit(page, '/wallet/setting/operation-logs')
      await expect(page.getByRole('heading', { name: 'Operation logs', exact: true })).toBeVisible()
      const logs = await page.evaluate(async () => {
        const [error, result] = await (await import('/utils/operationLog.ts')).getOperationLogs()
        if (error) throw error
        return result
      })
      assert.ok(logs.length, 'wallet UI actions produced no durable operation logs')
      assert.ok(!JSON.stringify(logs).includes(actor.password), 'operation log leaked the wallet password')
      assert.ok(!JSON.stringify(logs).includes(actor.mnemonic), 'operation log leaked the recovery phrase')
      const log = logs.find(record => record.action === 'sign_data' && record.status === 'succeeded')
      assert.ok(log, 'the real verified signature did not survive reload in operation logs')
      assert.ok(log.history.some(event => event.status === 'succeeded'))
      await page.getByRole('button').filter({ hasText: log.title }).last().click()
      await expect(page.getByRole('heading', { name: log.title, exact: true })).toBeVisible()
      await expect(page.getByRole('heading', { name: 'History', exact: true })).toBeVisible()
      await visit(page, '/wallet')
      await visit(page, '/wallet/setting/operation-logs')
      page.once('dialog', dialog => dialog.accept())
      await page.getByRole('button', { name: 'Delete all', exact: true }).click()
      await expect(page.getByText('No operation logs yet.', { exact: true })).toBeVisible()
      const cleared = await page.evaluate(async () => {
        const [error, result] = await (await import('/utils/operationLog.ts')).getOperationLogs()
        if (error) throw error
        return result
      })
      assert.deepEqual(cleared, [], 'operation log deletion only cleared the page')
      assert.deepEqual(await identity(page), originalIdentity)
      assert.equal((await walletCall(page, 'getWalletCatalog')).wallets.length, 1)
    })

    await check(requiredPwaWalletBasicCases[11], async () => {
      await settings(page)
      await page.getByRole('button', { name: /Security Options/ }).click()
      const security = page.getByRole('heading', { name: 'Security Options', exact: true }).locator('../..').locator('..')
      await security.getByRole('combobox').click()
      await page.getByRole('option', { name: '1 minute', exact: true }).click()
      await visit(page, '/wallet')
      const start = Date.now()
      // Keep real browser and WASM clocks. Polling reads do not dispatch any
      // of App.vue's pointer/key/touch/scroll activity events.
      await expect.poll(async () => (await identity(page)).locked,
        { timeout: 90000, intervals: [1000] }).toBe(true)
      assert.ok(Date.now() - start >= 55000, 'wallet locked before its configured inactivity interval')
      await expect.poll(() => new URL(page.url()).hash.startsWith('#/unlock'), poll).toBe(true)
      await assert.rejects(() => walletCall(page, 'signData', 'locked-wallet-must-not-sign'))
      await unlockPage(page, actor.password)
      assert.deepEqual(await identity(page), originalIdentity)
      await settings(page)
      await page.getByRole('button', { name: /Security Options/ }).click()
      await security.getByRole('combobox').click()
      await page.getByRole('option', { name: '15 minutes', exact: true }).click()
    })

    await check(requiredPwaWalletBasicCases[12], async () => {
      assert.ok(createdPage, 'no-name case requires the wallet created through the page')
      if ((await identity(createdPage)).locked) await unlockPage(createdPage, createdPassword)
      const createdIdentity = await identity(createdPage)
      const names = await createdPage.evaluate(async ({ address, network }) => {
        const response = await (await import('/apis/ordx.ts')).default.getNsListByAddress({ address, network })
        if (response.code !== 0) throw new Error(response.msg || 'name ownership query failed')
        return response.data?.names ?? []
      }, createdIdentity)
      assert.deepEqual(names, [], 'negative fixture must have no owned name')
      const amount = await walletCall(createdPage, 'getAssetAmount', createdIdentity.address, '::')
      await visit(createdPage, '/wallet/setting/referrer/register')
      await expect(createdPage.getByRole('heading', { name: 'Register as Referrer', exact: true })).toBeVisible()
      await expect(createdPage.getByText('当前地址没有可用的名字，请先注册名字后再进行推荐人注册。', { exact: true })).toBeVisible()
      await createdPage.getByRole('button', { name: 'Register as Referrer', exact: true }).click()
      await expect(createdPage.getByText('请填写完整信息', { exact: true })).toBeVisible()
      await expect(createdPage.getByRole('dialog')).toHaveCount(0)
      assert.deepEqual(await walletCall(createdPage, 'getAssetAmount', createdIdentity.address, '::'), amount)
      assert.deepEqual(await identity(createdPage), createdIdentity)
      await createdPage.context().close()
    })

    await check(requiredPwaWalletBasicCases[13], async () => {
      const before = await walletCall(page, 'getAssetAmount_SatsNet', actor.address, '::')
      await visit(page, '/wallet/setting/referrer/bind')
      await page.getByLabel('Referrer Name', { exact: true }).fill('pwa-acceptance-unregistered-name')
      await page.getByRole('button', { name: 'Bind Referrer', exact: true }).click()
      const dialog = page.getByRole('dialog')
      await expect(dialog).toContainText('pwa-acceptance-unregistered-name')
      await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
      assert.deepEqual(await walletCall(page, 'getAssetAmount_SatsNet', actor.address, '::'), before)
      await page.getByRole('button', { name: 'Bind Referrer', exact: true }).click()
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect(page.getByText('Bind Failure', { exact: true })).toBeVisible()
      await expect(page.getByLabel('Referrer Name', { exact: true })).toBeEnabled()
      assert.deepEqual(await walletCall(page, 'getAssetAmount_SatsNet', actor.address, '::'), before)
      assert.deepEqual(await identity(page), originalIdentity)
    })
    await check(requiredPwaWalletBasicCases[14], () => runAssetContextStability(t, fixture, fresh, visit, identity, walletBalance))
  } finally {
    for (const context of contexts) await context.close()
  }
}


// Hold a genuine A response until B has rendered. The observer delegates every
// SDK call unchanged; it only provides a completion barrier after WASM returns.
async function runAssetContextStability(t, fixture, fresh, visit, identity, walletBalance) {
  const { walletCall } = t
  const page = await fresh()
  const endpoint = fixture.config.IndexerL1
  const base = `${endpoint.Scheme}://${endpoint.Host}/${String(endpoint.Proxy || '').replace(/^\/+|\/+$/g, '')}`.replace(/\/$/, '')
  const summaryURL = address => `${base}/v3/address/summary/${encodeURIComponent(address)}`
  const keyOf = name => name.Protocol ? `${name.Protocol}:${name.Type}:${name.Ticker}` : '::'
  const amountOf = asset => String(asset.Amount?.Value ?? asset.Amount ?? '0')
  const metrics = []
  const started = performance.now()
  let releaseHeld, heldRequest, heldHits = 0, totalAHits = 0, heldReady = false, holdStarted, requestStarted, fetchedAt
  let routeHandler
  const released = new Promise(resolve => { releaseHeld = resolve })
  const sample = (phase, start, extra = {}) => metrics.push({ phase, duration_ms: performance.now() - start, ...extra })
  const snapshot = async () => {
    const response = await fetch(new URL('/snapshot', fixture.control_url), {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}', signal: AbortSignal.timeout(30000),
    })
    assert.ok(response.ok, 'test control snapshot must succeed')
    const result = await response.json()
    assert.ok(!result.error, 'test control snapshot must not return an application error')
    return result.l1.broadcast_count
  }
  const realSummary = async address => {
    const response = await fetch(summaryURL(address), { signal: AbortSignal.timeout(30000) })
    assert.ok(response.ok, 'real fixture summary must be available')
    const result = await response.json()
    assert.equal(result.code, 0)
    assert.ok(Array.isArray(result.data))
    return result.data
  }
  const readAssets = () => page.evaluate(async () => {
    const store = (await import('/store/l1.ts')).useL1Store()
    return { totalSats: store.totalSats, sat20Keys: store.sat20List.map(item => item.key).sort(),
      rawKeys: store.assetList.map(item => item.Name.Protocol ? `${item.Name.Protocol}:${item.Name.Type}:${item.Name.Ticker}` : '::').sort() }
  })
  const completions = address => page.evaluate(address => window.__SAT20_ASSET_CONTEXT_PROBE__.completed.filter(item => item.address === address).length, address)
  const waitCompleted = async (address, before, expectedError) => {
    const handle = await page.waitForFunction(({ address, before }) => {
      const completed = window.__SAT20_ASSET_CONTEXT_PROBE__.completed.filter(item => item.address === address)
      return completed.length > before && completed.at(-1)
    }, { address, before }, { timeout: poll.timeout })
    const completion = await handle.jsonValue()
    await handle.dispose()
    if (expectedError) {
      assert.equal(completion.ok, false, 'the held old-identity SDK call must be rejected')
      assert.equal(completion.error_message, expectedError, 'only the exact stale-session rejection is expected')
    } else {
      assert.equal(completion.ok, true, 'the real SDK summary call must succeed')
    }
    return completion
  }
  const refresh = () => page.getByRole('button', { name: 'ORDX', exact: true }).locator('../..').getByRole('button').last().click()
  const logs = () => page.evaluate(async () => {
    const [error, result] = await (await import('/utils/operationLog.ts')).getOperationLogs()
    if (error) throw error
    return result
  })
  let outcome = 'fail'
  try {
    await page.evaluate(async () => {
      const manager = (await import('/utils/sat20.ts')).default
      const original = manager.getAssetSummary
      const probe = window.__SAT20_ASSET_CONTEXT_PROBE__ = { completed: [], active: {}, restore: () => { manager.getAssetSummary = original } }
      manager.getAssetSummary = async function(address) {
        probe.active[address] = (probe.active[address] || 0) + 1
        const started = performance.now()
        try {
          const result = await original.call(this, address)
          probe.completed.push({ address, duration_ms: performance.now() - started, ok: !result[0],
            error_message: result[0]?.message })
          return result
        } finally { probe.active[address]-- }
      }
    })
    // Import through the real page/store. This actor owns the nonempty ORDX
    // fixture; basicWallet is BTC-only and would make the stale-data check weak.
    await visit(page, '/import')
    await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(fixture.mnemonic)
    await page.getByLabel('New Wallet Password', { exact: true }).fill(fixture.password)
    await page.getByLabel('Confirm Password', { exact: true }).fill(fixture.password)
    await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
    await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: poll.timeout })
    assert.equal((await identity(page)).address, fixture.address)
    await page.evaluate(async () => { await window.__SAT20_PWA_VERIFY__.useWalletStore().addAccount('Asset context B', 1) })
    const b = await identity(page)
    assert.equal(b.accountIndex, 1)
    assert.notEqual(b.address, fixture.address)
    assert.equal((await walletCall(page, 'getWalletAddress', 1)).address, b.address)
    const assetsA = await realSummary(fixture.address)
    const assetsB = await realSummary(b.address)
    assert.ok(assetsA.some(asset => keyOf(asset.Name) === fixture.asset.key && BigInt(amountOf(asset)) > 0n), 'A must own the actual nonempty ORDX fixture')
    assert.ok(!assetsB.some(asset => keyOf(asset.Name) === fixture.asset.key), 'B must not own A\'s ORDX fixture')
    const totalB = assetsB.find(asset => asset.Name.Protocol === '' && asset.Name.Type === '*')
    assert.ok(totalB, 'B summary must include its native total')
    const expectedSatsB = Number(amountOf(totalB))
    assert.ok(Number.isSafeInteger(expectedSatsB))
    await page.evaluate(async () => { await window.__SAT20_PWA_VERIFY__.useWalletStore().switchToAccount(0) })
    await page.getByRole('tab', { name: 'Bitcoin', exact: true }).click()
    await page.getByRole('button', { name: 'ORDX', exact: true }).click()
    const ticker = page.getByText(fixture.asset.ticker.toLocaleUpperCase(), { exact: true })
    await expect(ticker).toBeVisible({ timeout: poll.timeout })
    await expect.poll(() => page.evaluate(address => window.__SAT20_ASSET_CONTEXT_PROBE__.active[address] || 0, fixture.address), poll).toBe(0)
    const beforeBroadcasts = await snapshot()
    routeHandler = async route => {
      totalAHits++
      if (heldHits) return route.continue()
      heldHits++
      heldRequest = route.request()
      requestStarted = performance.now()
      const response = await route.fetch()
      assert.ok(response.ok(), 'held A response must come from the real fixture')
      const result = await response.json()
      assert.equal(result.code, 0)
      assert.ok(result.data.some(asset => keyOf(asset.Name) === fixture.asset.key && BigInt(amountOf(asset)) > 0n))
      fetchedAt = performance.now()
      holdStarted = fetchedAt
      heldReady = true
      await released
      await route.fulfill({ response })
    }
    await page.route(summaryURL(fixture.address), routeHandler)
    const triggerStarted = performance.now()
    await refresh()
    await expect.poll(() => heldReady, poll).toBe(true)
    // Bind to the held request object. Invalidation may legitimately issue a
    // second A request; its early response cannot satisfy this barrier.
    const heldResponse = page.waitForResponse(response => response.request() === heldRequest, { timeout: poll.timeout })
    heldResponse.catch(() => {})
    metrics.push({ phase: 'refresh_trigger_to_request', duration_ms: requestStarted - triggerStarted })
    metrics.push({ phase: 'real_A_request_to_response', duration_ms: fetchedAt - requestStarted })
    const bResponse = page.waitForResponse(response => response.url() === summaryURL(b.address), { timeout: poll.timeout })
    bResponse.catch(() => {})
    const bCompleted = await completions(b.address)
    await page.evaluate(async () => { await window.__SAT20_PWA_VERIFY__.useWalletStore().switchToAccount(1) })
    assert.ok((await bResponse).ok())
    await waitCompleted(b.address, bCompleted)
    await expect.poll(() => identity(page), poll).toEqual(b)
    const expectedKeysB = assetsB.map(asset => keyOf(asset.Name)).sort()
    await expect.poll(readAssets, poll).toEqual({ totalSats: expectedSatsB, sat20Keys: [], rawKeys: expectedKeysB })
    const unit = b.network === 'mainnet' ? 'BTC' : b.network === 'regtest' ? 'rBTC' : 'tBTC'
    const expectedBalance = (expectedSatsB / 1e8).toFixed(8) + ` ${unit}`
    await expect(walletBalance(page)).toHaveText(expectedBalance)
    await expect(ticker).toHaveCount(0)
    const bAssets = await readAssets()
    // Switching accounts legitimately creates an operation log. Measure only
    // late read completion and the following read-only refreshes from here.
    const beforeLogs = await logs()
    const releasedAt = performance.now()
    const completedBeforeRelease = await completions(fixture.address)
    metrics.push({ phase: 'injected_hold', duration_ms: releasedAt - holdStarted })
    releaseHeld()
    await (await heldResponse).finished()
    await waitCompleted(fixture.address, completedBeforeRelease, 'Wallet session changed; request is no longer valid')
    await page.waitForFunction(address => (window.__SAT20_ASSET_CONTEXT_PROBE__.active[address] || 0) === 0,
      fixture.address, { timeout: poll.timeout })
    // SDK completion is the data boundary; render turns let Vue apply the real
    // query response before inspecting both Pinia and the displayed asset list.
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    assert.deepEqual(await identity(page), b)
    assert.equal((await walletCall(page, 'getWalletAddress', 1)).address, b.address)
    assert.deepEqual(await readAssets(), bAssets, 'late A data must not replace B assets or total')
    await expect(walletBalance(page)).toHaveText(expectedBalance)
    await expect(ticker).toHaveCount(0)
    sample('release_to_B_render_verified', releasedAt, { counts: { held_hits: heldHits, A_summary_requests: totalAHits } })
    assert.equal(heldHits, 1, 'exactly one genuine stale response must be released')
    await page.unroute(summaryURL(fixture.address), routeHandler)
    routeHandler = undefined
    const warm = []
    for (let index = 0; index < 10; index++) {
      await page.waitForFunction(address => (window.__SAT20_ASSET_CONTEXT_PROBE__.active[address] || 0) === 0,
        b.address, { timeout: poll.timeout })
      const completed = await completions(b.address)
      const started = performance.now()
      await refresh()
      await waitCompleted(b.address, completed)
      await page.waitForFunction(address => (window.__SAT20_ASSET_CONTEXT_PROBE__.active[address] || 0) === 0,
        b.address, { timeout: poll.timeout })
      const calls = await page.evaluate(({ address, completed }) => window.__SAT20_ASSET_CONTEXT_PROBE__.completed
        .filter(item => item.address === address).slice(completed), { address: b.address, completed })
      assert.ok(calls.length > 0 && calls.every(call => call.ok), 'every call in this refresh must finish successfully')
      const duration_ms = Math.max(...calls.map(call => call.duration_ms))
      warm.push(duration_ms)
      const observed = performance.now() - started
      assert.deepEqual(await readAssets(), bAssets)
      await expect(walletBalance(page)).toHaveText(expectedBalance)
      await expect(ticker).toHaveCount(0)
      metrics.push({ phase: 'warm_B_refresh_max_SDK_summary', temperature: 'warm', sample_index: index, duration_ms,
        sdk_durations_ms: calls.map(call => call.duration_ms), counts: { summary_calls: calls.length },
        trigger_to_sdk_observed_ms: observed, trigger_to_B_render_verified_ms: performance.now() - started })
    }
    assert.deepEqual(await snapshot(), beforeBroadcasts, 'refresh and account switching must not broadcast')
    assert.deepEqual(await logs(), beforeLogs, 'late asset reads and refreshes must not create operation logs')
    warm.sort((a, b) => a - b)
    metrics.push({ phase: 'warm_B_refresh_SDK_summary', measurement: 'maximum SDK getAssetSummary duration per sequential refresh',
      samples: 10, unit: 'ms', summary: { min: warm[0], median: (warm[4] + warm[5]) / 2, max: warm[9] },
      counts: { held_hits: heldHits, A_summary_requests: totalAHits, l1_broadcast: 0 },
      workload: { accounts: 2, nonempty_ordx_accounts: 1, sequential_refreshes: 10 } })
    outcome = 'pass'
    return { metrics }
  } catch (error) {
    error.metrics = metrics
    throw error
  } finally {
    releaseHeld()
    if (routeHandler) await page.unroute(summaryURL(fixture.address), routeHandler).catch(() => {})
    await page.evaluate(() => window.__SAT20_ASSET_CONTEXT_PROBE__?.restore()).catch(() => {})
    sample('case_including_wallet_setup', started)
    const environment = { os: process.platform, arch: process.arch, cpu_count: cpus().length, node_version: process.version,
      browser_version: page.context().browser().version(), network: fixture.config.Chain || 'testnet', isolated_fixture: true,
      fixture_kind: 'temporary SatoshiNet and controlled Bitcoin evidence', workers: 1 }
    for (const metric of metrics) { metric.status = outcome; metric.environment = environment }
  }
}
