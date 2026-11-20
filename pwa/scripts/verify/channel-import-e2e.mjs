import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { expect } from '@playwright/test'
const ui = JSON.parse(readFileSync(new URL('../../locales/en.json', import.meta.url), 'utf8')).accountManagement.ui

// Retained same-device local channel boundary. RGB proof/business journeys run
// separately through the new SDK APIs; this case only tests catalog import.
export async function runChannelImportCase(t, fixture) {
  const { check, device, ready, unlock, catalog, walletCall, accountCall } = t
  const waitForPeerCatalog = async page => expect.poll(async () => {
    const state = await accountCall(page, 'getAccountView')
    return state.state === 'ready' && state.backup_state === 'backed_up'
  }, { timeout: 90000 }).toBe(true)
  const navigateRecovery = async page => {
    await page.evaluate(() => { location.hash = '#/restore-account' })
    await page.getByPlaceholder(ui.pasteLocator).fill(fixture.locator)
    await page.getByRole('button', { name: ui.loadBackup, exact: true }).click()
    for (let i = 0; i < fixture.answers.length; i++) await page.locator('section input[type=password]').nth(i).fill(fixture.answers[i].answer)
    await page.getByPlaceholder(ui.pasteUserShare).fill(fixture.user_share)
    await page.getByRole('button', { name: ui.previewRecovery, exact: true }).click()
    await page.locator('#restore-local-password').fill(fixture.password)
    await page.locator('#restore-confirm-password').fill(fixture.password)
  }
  const unlockRecovered = async page => { await unlock(page, fixture.password); await waitForPeerCatalog(page) }
  const commit = async page => {
    await Promise.all([page.waitForEvent('domcontentloaded'), page.getByRole('button', { name: ui.restoreAll, exact: true }).click()])
    await ready(page); await unlockRecovered(page)
  }
  const bitcoinRoute = async route => {
    const path = new URL(route.request().url()).pathname
    const body = route.request().postDataJSON() ?? {}
    const facts = fixture.bitcoin_evidence
    let data
    if (path.endsWith('/tip')) data = facts.tip
    else if (path.endsWith('/utxos/status')) data = body.outpoints.map(point => facts.utxos[point] ?? { outpoint: point, exists: false, unspent: false })
    else if (path.endsWith('/outspends/batch')) data = body.outpoints.map(point => {
      const outspend = facts.outspends[point] ?? { outpoint: point, exists: false, spent: false }
      const spender = facts.status[outspend.spending_tx]
      assert.ok(!outspend.spending_tx || (spender?.in_mempool && !spender.confirmed),
        `controlled Bitcoin outspend leaked non-mempool spender identity: ${point}`)
      return outspend
    })
    else if (path.endsWith('/rawtx/batch')) data = body.txids.map(id => facts.raw[id] ?? { txid: id, error: 'controlled transaction not found' })
    else if (path.endsWith('/tx/status/batch')) data = body.txids.map(id => facts.status[id] ?? { txid: id, exists: false, in_mempool: false, confirmed: false })
    else return route.fallback()
    return route.fulfill({ contentType: 'application/json', json: { code: 0, msg: 'ok', data } })
  }
  const registerGenesisRoute = async context => {
    const indexer = fixture.config.IndexerL1
    const genesisURL = new URL(`${indexer.Scheme}://${indexer.Host}/${indexer.Proxy}/btc/block/blockhash/0`).href
    await context.route(genesisURL, route => {
      if (route.request().method() !== 'GET') return route.fallback()
      return route.fulfill({ contentType: 'application/json', json: { code: 0, msg: 'ok', data: fixture.bitcoin_evidence.genesis_hash } })
    })
  }
  const channelPage = await device()
  await registerGenesisRoute(channelPage.context())
  let channelBroadcasts = 0
  await channelPage.context().route('**/v3/bitcoin/**', bitcoinRoute)
  await channelPage.context().route('**/*', route => {
    if (/broadcast|sendrawtransaction|\/btc\/txs?(?:\?|$)/i.test(route.request().url())) {
      channelBroadcasts++; return route.abort('blockedbyclient')
    }
    return route.fallback()
  })
  const failures = []
  try {
    await check('RGB PWA: cold remote import retry keeps unfinished local channel visible', async () => {
      const checkLocalRows = async ({ keys, remember = false, expected }) => {
        const db = await new Promise((resolve, reject) => {
          const request = indexedDB.open('sat20-wallet-sdk', 1)
          request.onsuccess = () => resolve(request.result)
          request.onerror = () => reject(request.error)
        })
        try {
          const rows = await new Promise((resolve, reject) => {
            const tx = db.transaction('kv', 'readonly')
            const store = tx.objectStore('kv')
            const requests = keys.map(key => store.get(key))
            tx.oncomplete = () => resolve(requests.map(request => request.result))
            tx.onabort = () => reject(tx.error)
          })
          if (remember) window.__usageCatalogRows = rows
          const reference = expected ?? window.__usageCatalogRows
          return rows.length > 0 && rows.every((row, i) => typeof row === 'string' && row === reference[i])
        } finally { db.close() }
      }
      const peer = await device()
      await registerGenesisRoute(peer.context())
      let peerRoot
      let peerRenamed = false
      const caseFailures = []
      try {
        // Establish a known catalog generation. A wallet rename updates the
        // wallets module and leaves this device's RGB proof receipts intact.
        await peer.context().route('**/v3/bitcoin/**', bitcoinRoute)
        await navigateRecovery(peer); await commit(peer)
        peerRoot = (await catalog(peer))[0]
        await waitForPeerCatalog(peer)
        await peer.evaluate(async id => {
          await window.__SAT20_PWA_VERIFY__.useWalletStore().updateWalletName(id, 'Pending channel healthy baseline')
        }, String(peerRoot.id))
        peerRenamed = true
        await expect.poll(async () => (await accountCall(peer, 'getAccountView')).backup_state,
          { timeout: 90000 }).toBe('backed_up')
        // This is one device's complete encoded DB, including its local wallet
        // IDs and unfinished channel. It is not a channel transferred by account
        // recovery. The other cases exercise actual backup restoration pages.
        // Close the empty Manager before replacing its database. Otherwise its
        // checkpoint worker can overwrite the handed-off selection before reload.
        await walletCall(channelPage, 'release')
        await channelPage.evaluate(async records => {
          const db = await new Promise((resolve, reject) => {
            const request = indexedDB.open('sat20-wallet-sdk', 1)
            request.onsuccess = () => resolve(request.result)
            request.onerror = () => reject(request.error)
          })
          try {
            await new Promise((resolve, reject) => {
              const tx = db.transaction('kv', 'readwrite')
              tx.oncomplete = resolve
              tx.onabort = () => reject(tx.error)
              for (const [key, value] of Object.entries(records)) tx.objectStore('kv').put(value, key)
            })
          } finally { db.close() }
        }, fixture.pending_channel.device_records)

        await channelPage.reload(); await ready(channelPage)

        await unlockRecovered(channelPage, 'same-device healthy cold startup before injected fault')

        assert.equal(String((await catalog(channelPage))[0].id), String(fixture.pending_channel.wallet_id))
        const healthyChannel = await channelPage.evaluate(async () => {
          const { default: stp } = await import('/utils/stp.ts')
          const [error, result] = await stp.getCurrentChannel()
          if (error) throw error
          return result.channelId
        })
        assert.equal(healthyChannel, fixture.pending_channel.id, 'local device fixture cannot restore its channel before the fault')
        await expect.poll(async () => (await catalog(channelPage)).some(wallet => wallet.name === 'Pending channel healthy baseline'),
          { timeout: 90000 }).toBe(true)
        await channelPage.evaluate(async id => {
          const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
          await store.switchWallet(id)
          await store.switchToAccount(0)
        }, String(fixture.pending_channel.wallet_id))
        const localCatalog = await catalog(channelPage)
        const renamedWallet = localCatalog.find(wallet => wallet.fingerprint === peerRoot.fingerprint)
        assert.equal(renamedWallet?.name, 'Pending channel healthy baseline')
        const catalogKeys = localCatalog.map(wallet => `wallet-id-${wallet.id}`)
        assert.ok(await channelPage.evaluate(checkLocalRows, { keys: catalogKeys, remember: true }),
          'local catalog rows are missing before the interrupted import')
        await channelPage.evaluate(walletKey => {
          const put = IDBObjectStore.prototype.put
          const withStatus = new WeakSet()
          IDBObjectStore.prototype.put = function(value, key) {
            const request = put.call(this, value, key)
            const tx = this.transaction
            if (tx.db.name !== 'sat20-wallet-sdk' || this.name !== 'kv') return request
            if (key === 'wallet-status') withStatus.add(tx)
            if (key === walletKey) request.addEventListener('success', () => {
              // Remote catalog import queues the wallet rows and status together.
              if (!withStatus.has(tx)) return
              tx.addEventListener('abort', () => { window.__usageCatalogAborted = true }, { once: true })
              tx.abort()
            }, { once: true })
            return request
          }
        }, `wallet-id-${renamedWallet.id}`)
        await waitForPeerCatalog(peer)
        await peer.evaluate(async id => {
          await window.__SAT20_PWA_VERIFY__.useWalletStore().updateWalletName(id, 'Pending channel recovery state')
        }, String(peerRoot.id))
        await expect.poll(async () => (await accountCall(peer, 'getAccountView')).backup_state,
          { timeout: 90000 }).toBe('backed_up')
        await expect.poll(() => channelPage.evaluate(() => window.__usageCatalogAborted === true),
          { timeout: 90000 }).toBe(true)
        await expect.poll(async () => (await accountCall(channelPage, 'getAccountView')).error_code,
          { timeout: 90000 }).toBe('ACCOUNT_DB_WRITE_FAILED')
        assert.ok(await channelPage.evaluate(checkLocalRows, { keys: catalogKeys }),
          'aborted remote catalog transaction changed durable wallet rows')
        // Reload drops the page-local fault and starts a fresh Manager. It must
        // finish the interrupted wallets/global import from its durable copy.
        await channelPage.reload(); await ready(channelPage)
        await unlockRecovered(channelPage, 'same-device cold retry after removing injected fault')
        await expect.poll(async () => {
          const wallets = await catalog(channelPage)
          const view = await accountCall(channelPage, 'getAccountView')
          return wallets.some(wallet => String(wallet.id) === String(renamedWallet.id) &&
            wallet.name === 'Pending channel recovery state') &&
            view.state === 'ready' && view.backup_state === 'backed_up' && !view.error_code
        }, { timeout: 90000 }).toBe(true)
        const channel = await channelPage.evaluate(async () => {
          const { default: stp } = await import('/utils/stp.ts')
          const [error, result] = await stp.getCurrentChannel()
          if (error) throw error
          return result
        })
        assert.equal(channel.channelId, fixture.pending_channel.id, 'successful retry lost the unfinished local channel')
        const visible = await channelPage.evaluate(async () => {
          const { useChannelStore } = await import('/store/channel.ts')
          const store = useChannelStore()
          await store.getCurrentChannel()
          return store.channel?.channelId
        })
        assert.equal(visible, fixture.pending_channel.id, 'PWA channel store lost the unfinished local channel')
        const channels = await channelPage.evaluate(async () => {
          const { default: stp } = await import('/utils/stp.ts')
          const [error, result] = await stp.getAllChannels()
          if (error) throw error
          return JSON.parse(result.channels)
        })
        assert.ok(channels.some(item => item.ChannelId === fixture.pending_channel.id || item.channelId === fixture.pending_channel.id))
        const records = fixture.pending_channel.records
        assert.ok(await channelPage.evaluate(checkLocalRows, {
          keys: Object.keys(records), expected: Object.values(records),
        }), 'authentication rewrote unfinished channel work')

        assert.equal(channelBroadcasts, 0, 'channel recovery attempted a new transaction broadcast')
      } catch (error) {
        caseFailures.push(error)
      } finally {
        try {
          // Restore only this peer's metadata change, including when the cold
          // retry stays red. Keep later independent recovery cases synchronized.
          if (peerRenamed) {
            await waitForPeerCatalog(peer)
            await peer.evaluate(async ({ id, name }) => {
              await window.__SAT20_PWA_VERIFY__.useWalletStore().updateWalletName(id, name)
            }, { id: String(peerRoot.id), name: peerRoot.name })
            await expect.poll(async () => (await accountCall(peer, 'getAccountView')).backup_state,
              { timeout: 90000 }).toBe('backed_up')
          }
        } catch (error) {
          caseFailures.push(new Error(`restore peer metadata: ${error.message}`))
        } finally { await peer.context().close() }
      }
      if (caseFailures.length === 1) throw caseFailures[0]
      if (caseFailures.length > 1) throw new AggregateError(caseFailures, caseFailures.map(error => error.message).join('; '))
    })
  } catch (error) { failures.push({ name: 'pending local channel after remote import', message: error.message }) }
  finally { await channelPage.context().close() }
  assert.deepEqual(failures, [], 'channel import boundary failed')
}
