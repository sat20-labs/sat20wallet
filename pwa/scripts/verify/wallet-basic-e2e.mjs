import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
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
]

const poll = { timeout: 120000, intervals: [250, 500, 1000] }

export async function runPwaWalletBasicCases(t, fixture) {
  const { check, device, ready, walletCall } = t
  const actor = fixture.basicWallet
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
  let page
  let originalIdentity

  try {
    await check(requiredPwaWalletBasicCases[0], async () => {
      const created = await fresh()
      const password = 'Pwa-create-only-2026!'
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
      assert.equal(before.network, 'testnet')
      assert.equal((await walletCall(created, 'getWalletCatalog')).wallets.length, 1)
      assert.equal((await walletCall(created, 'getWalletAddress', 0)).address, before.address)
      await reloadUnlock(created, password)
      assert.deepEqual(await identity(created), before, 'reload changed the newly created wallet')
      await created.context().close()
    })

    await check(requiredPwaWalletBasicCases[1], async () => {
      page = await fresh()
      await visit(page, '/import')
      await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(actor.mnemonic)
      await page.getByLabel('New Wallet Password', { exact: true }).fill(actor.password)
      await page.getByLabel('Confirm Password', { exact: true }).fill(actor.password)
      await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
      await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible()
      originalIdentity = await identity(page)
      assert.equal(originalIdentity.address, actor.address)
      assert.equal(originalIdentity.network, 'testnet')
      assert.equal(originalIdentity.locked, false)
      assert.equal((await walletCall(page, 'getWalletAddress', 0)).address, actor.address)
      assert.equal((await walletCall(page, 'getWalletPubkey', 0)).pubKey, originalIdentity.publicKey)
      await expect.poll(async () => {
        const amount = await walletCall(page, 'getAssetAmount', actor.address, '::')
        return BigInt(String(amount.availableAmt)) > 0n
      }, poll).toBe(true)
      const totalSats = await page.evaluate(async address => {
        const api = (await import('/apis/ordx.ts')).default
        const result = await api.getAddressSummary({ address, network: 'testnet' })
        if (result.code !== 0) throw new Error(result.msg)
        const total = result.data.find(asset => asset.Name.Protocol === '' && asset.Name.Type === '*')
        if (!total) throw new Error('L1 summary omitted the native total')
        return Number(total.Amount)
      }, actor.address)
      assert.ok(Number.isSafeInteger(totalSats) && totalSats > 0)
      await expect(walletBalance(page)).toHaveText((totalSats / 1e8).toFixed(8) + ' tBTC')
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
      await expect(page.getByText('No locked UTXOs', { exact: true })).toBeVisible()
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
      const amount = await walletCall(page, 'getAssetAmount', actor.address, '::')
      await visit(page, '/wallet/setting/referrer/register')
      await expect(page.getByRole('heading', { name: 'Register as Referrer', exact: true })).toBeVisible()
      await expect(page.getByText('当前地址没有可用的名字，请先注册名字后再进行推荐人注册。', { exact: true })).toBeVisible()
      await page.getByRole('button', { name: 'Register as Referrer', exact: true }).click()
      await expect(page.getByText('请填写完整信息', { exact: true })).toBeVisible()
      await expect(page.getByRole('dialog')).toHaveCount(0)
      assert.deepEqual(await walletCall(page, 'getAssetAmount', actor.address, '::'), amount)
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
  } finally {
    for (const context of contexts) await context.close()
  }
}
