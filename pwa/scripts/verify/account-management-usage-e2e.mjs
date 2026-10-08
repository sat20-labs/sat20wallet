import assert from 'node:assert/strict'
import { isDeepStrictEqual } from 'node:util'
import { appendFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { expect } from '@playwright/test'

// Reuse real PWA root discovery and background sync across separate devices.
async function sameModeReconfiguration(t, page, credential, mode, questions, mnemonic) {
  const { device, ready, unlock, accountCall, catalog } = t
  const settled = async p => expect.poll(async () => {
    const status = await accountCall(p, 'status')
    return !status.pending_changes && !status.managed_data_dirty
  }, { timeout: 90000 }).toBe(true)
  const importRoot = async p => {
    await p.evaluate(async ({ mnemonic, credential }) => {
      const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().importWallet(mnemonic, credential)
      if (error) throw error
    }, { mnemonic, credential })
    await settled(p)
  }
  const peer = await device()
  const recovered = await device()
  let phase = 'initial peer import'
  let targetLocator = ''
  try {
    const before = await accountCall(page, 'status')
    await importRoot(peer)
    assert.equal((await accountCall(peer, 'status')).package_id, before.package_id)
    phase = 'source before reconfiguration'
    await settled(page)
    if (mode === 'paid') await accountCall(page, 'reusePaidStorage', 100)
    else await accountCall(page, 'confirmStorage', 'temporary')
    const material = await accountCall(page, 'createRecovery', { password: credential, wallets: [], recovery_mode: '2of2', questions })
    targetLocator = material.locator
    phase = 'reconfiguration rehearsal'
    await accountCall(page, 'rehearse', material.session_id,
      questions.slice(0,2).map(({ id,answer }) => ({ question_id:id,answer })), material.user_share,credential)
    phase = 'source after activation'
    await settled(page)
    phase = 'paired configuration adoption'
    // Adoption includes both the recovery configuration and its paired state
    // and data. Seeing the new locator alone can precede that final commit.
    await expect.poll(async () => {
      const states = await Promise.all([page, peer].map(p => accountCall(p, 'status')))
      return states.every(s => s.public_locator === material.locator &&
        !s.pending_changes && !s.managed_data_dirty) &&
        states[0].state_seq === states[1].state_seq &&
        states[0].managed_data_revision === states[1].managed_data_revision
    }, { timeout: 90000 }).toBe(true)
    const current = await accountCall(page, 'status')
    assert.notEqual(current.package_id,before.package_id)
    assert.equal(current.storage_mode,mode)
    for (let round=0; round<3; round++) {
      for (const p of [peer,page]) {
        phase = `cold unlock round ${round + 1} ${p === peer ? 'peer' : 'source'}`
        await p.reload(); await ready(p)
        // A cold unlock can authenticate while a remote-apply marker still
        // guards provider import. Retry the same visible unlock operation;
        // permanent corruption or any other error must still fail the case.
        await expect.poll(async () => {
          try { await unlock(p, credential); return true }
          catch (error) {
            if (!/account-managed data import is incomplete/.test(error.message)) throw error
            return false
          }
        }, { timeout: 90000, intervals: [1000, 2000, 5000] }).toBe(true)
        phase = `cold version stability round ${round + 1} ${p === peer ? 'peer' : 'source'}`
        await expect.poll(async () => {
          const s = await accountCall(p, 'status')
          return s.public_locator === material.locator && !s.pending_changes && !s.managed_data_dirty &&
            s.state_seq === current.state_seq && s.managed_data_revision === current.managed_data_revision
        }, { timeout: 90000 }).toBe(true)
        const status = await accountCall(p,'status')
        assert.equal(status.package_id,current.package_id)
        assert.equal(status.storage_mode,mode)
        assert.equal(status.state_seq,current.state_seq,'idle devices keep republishing account state')
        assert.equal(status.managed_data_revision,current.managed_data_revision)
      }
    }
    phase = 'new device root recovery'
    await importRoot(recovered)
    const final = await accountCall(recovered,'status')
    assert.equal(final.package_id,current.package_id)
    assert.equal(final.public_locator,material.locator)
    assert.equal(final.storage_mode,mode)
    const identities = wallets => wallets.map(({fingerprint,name,accounts}) => ({fingerprint,name,accounts})).sort((a,b) => a.fingerprint.localeCompare(b.fingerprint))
    assert.deepEqual(identities(await catalog(recovered)),identities(await catalog(page)))
  } catch (error) {
    const states = await Promise.all([page, peer].map(async p => {
      try {
        const s = await accountCall(p, 'status')
        return { device: p === page ? 'source' : 'peer', target_locator: s.public_locator === targetLocator,
          package_id: s.package_id, state_seq: s.state_seq, managed_data_revision: s.managed_data_revision,
          pending_changes: s.pending_changes, managed_data_dirty: s.managed_data_dirty,
          last_sync_error: s.last_sync_error, storage_mode: s.storage_mode }
      } catch (readError) { return { device: p === page ? 'source' : 'peer', status_error: readError.message } }
    }))
    throw new Error(`same-mode ${phase}: ${error.message}; public status ${JSON.stringify(states)}`, { cause: error })
  } finally { await peer.context().close(); await recovered.context().close() }
}

// Additional business cases share the existing runner, real WASM and node
// fixture. Faults intercept only the failing browser boundary, never replace
// the account/crypto/RGB implementation or its successful responses.
export async function runPwaUsageCases(t) {
  const { check, device, ready, unlock, catalog, walletCall, accountCall, password, newPassword, questions, answers } = t
  const importMnemonic = t.rootMnemonic || 'abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about'
  const failures = []
  const scenario = async (name, action) => {
    try { await check(name, action) } catch (error) { failures.push({ name, message: error.message }) }
  }
  const fresh = async () => {
    const page = await device()
    await page.evaluate(async credential => {
      const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(credential)
      if (error) throw error
    }, password)
    return page
  }
  const using = async action => {
    const page = await fresh()
    try { await action(page) } catch (error) {
      const view = await page.evaluate(() => ({
        headings: [...document.querySelectorAll('h1,h2')].map(element => element.textContent),
        alerts: [...document.querySelectorAll('[role="alert"]')].map(element => element.textContent),
      })).catch(() => null)
      console.error(JSON.stringify({ usageFailureView: view, url: page.url() }))
      throw error
    } finally { await page.context().close() }
  }
  const setup = async page => {
    await accountCall(page, 'confirmStorage', 'temporary')
    await settled(page)
    const material = await accountCall(page, 'createRecovery', { password, wallets: [], recovery_mode: '2of2', questions })
    // Match the visible setup page's bounded retry of this same session after
    // a safe CAS refusal; never replace the material or suppress other errors.
    for (let attempt = 0; attempt < 4; attempt++) {
      try {
        const result = await accountCall(page, 'rehearse', material.session_id, answers, material.user_share, password)
        assert.equal(result.verified, true)
        break
      } catch (error) {
        if (!/publish account activation state: dkvs write conflict/.test(error.message) || attempt === 3) throw error
        assert.equal((await accountCall(page, 'status')).recovery_configured, false, 'CAS refusal falsely configured recovery')
        console.log(JSON.stringify({ activationUserRetry: attempt + 1, outcome: 'conflict', entry: 'SDK setup helper' }))
        await settled(page)
      }
    }
    await settled(page)
    return material
  }
  const settled = async page => {
    let lastStatus = null
    try {
      await expect.poll(async () => {
        const status = await accountCall(page, 'status')
        lastStatus = Object.fromEntries([
          'pending_changes', 'managed_data_dirty', 'state_seq', 'managed_data_revision',
          'last_dkvs_sync_error_code', 'last_dkvs_sync_error', 'last_dkvs_sync_error_at',
        ].filter(key => status[key] !== undefined).map(key => [key, status[key]]))
        return (status.pending_changes ?? 0) === 0 && !status.managed_data_dirty
      }, { timeout: 90000 }).toBe(true)
    } catch (error) {
      console.error(JSON.stringify({ accountSyncWaitFailure: { last_status: lastStatus } }))
      throw error
    }
  }
  const restore = async (page, material) => {
    const session = await accountCall(page, 'loadRecovery', material.locator)
    await accountCall(page, 'recoverKnowledge', session.session_id, answers)
    await accountCall(page, 'setUserShare', session.session_id, material.user_share)
    await accountCall(page, 'previewRecovery', session.session_id)
    await accountCall(page, 'commitRecovery', session.session_id, password)
    await page.reload(); await ready(page); await unlock(page, password)
  }
  const settings = async page => {
    await page.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
    await page.getByRole('button', { name: '检查账户', exact: true }).waitFor()
  }
  const confirmPassword = async (page, credential = password) => {
    const dialog = page.getByRole('dialog')
    await dialog.locator('input[type="password"]').fill(credential)
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await dialog.waitFor({ state: 'hidden' })
  }
  const preflightUI = async page => {
    await settings(page)
    await page.getByRole('button', { name: '检查账户', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await dialog.locator('input[type="password"]').fill('incorrect')
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await dialog.getByRole('alert').waitFor()
    await confirmPassword(page)
    await page.getByRole('button', { name: '确认存储方式', exact: true }).waitFor()
  }
  const temporaryUI = async page => {
    const options = (await accountCall(page, 'getStorageOptions')).options
    await page.getByRole('button', { name: new RegExp(`^${options.find(option => option.id === 'temporary').title}`) }).click()
    await page.getByRole('button', { name: '确认存储方式', exact: true }).click()
    await page.getByRole('button', { name: '创建并保存加密账户备份', exact: true }).waitFor()
  }
  const fillQuestions = async page => {
    for (let i = 0; i < 3; i++) {
      await page.getByPlaceholder('答案', { exact: true }).nth(i).fill(questions[i].answer)
      await page.getByPlaceholder('再次输入答案', { exact: true }).nth(i).fill(questions[i].confirmation)
    }
  }
  const assertNoSecrets = async (page, secrets) => {
    const exposed = await page.evaluate(async values => {
      const containers = [...Object.values(localStorage), ...Object.values(sessionStorage)]
      const db = await new Promise((resolve, reject) => {
        const request = indexedDB.open('sat20-wallet-pwa', 1)
        request.onsuccess = () => resolve(request.result)
        request.onerror = () => reject(request.error)
      })
      try {
        const valuesInDB = await new Promise((resolve, reject) => {
          const tx = db.transaction('wallet-state', 'readonly')
          const request = tx.objectStore('wallet-state').getAll()
          tx.oncomplete = () => resolve(request.result)
          tx.onabort = () => reject(tx.error)
        })
        containers.push(...valuesInDB.map(String))
        const logs = await window.sat20wallet_operation_log.getOperationLogs()
        if (logs.code !== 0) throw new Error('read operation logs: ' + logs.msg)
        containers.push(logs.data.logs)
      } finally { db.close() }
      return values.map((value, index) => containers.some(item => item.includes(value)) ? index : -1).filter(index => index >= 0)
    }, secrets)
    assert.deepEqual(exposed, [], 'recovery secrets were persisted in PWA state')
  }

  await scenario('usage: background catalog refresh cannot invalidate a subaccount switch', () => using(async page => {
    const observed = await page.evaluate(async () => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      await store.addAccount('Switch refresh regression', 1)
      await store.switchToAccount(0)
      const api = window.sat20wallet_wasm
      const switchAccount = api.switchAccount
      let refreshes = 0
      api.switchAccount = async (...args) => {
        const result = await switchAccount(...args)
        // Hold only delivery of the real SDK result. Catalog refresh is the
        // same public read used by the actual wallet-data callback.
        await store.syncWalletCatalog()
        refreshes++
        return result
      }
      try {
        await store.switchToAccount(1)
        await store.syncWalletCatalog()
        const { getWalletIdentityState } = await import('/lib/identity-boundary.ts')
        return { refreshes, index: store.accountIndex, address: store.address,
          pubKey: store.publicKey, locked: store.locked, phase: getWalletIdentityState().phase }
      } finally { api.switchAccount = switchAccount }
    })
    assert.equal(observed.refreshes, 1)
    assert.equal(observed.index, 1)
    assert.equal(observed.locked, false)
    assert.equal(observed.phase, 'READY')
    assert.equal(observed.address, (await walletCall(page, 'getWalletAddress', 1)).address)
    assert.equal(observed.pubKey, (await walletCall(page, 'getWalletPubkey', 1)).pubKey)
  }))

  const didSettings = async page => {
    await page.evaluate(() => { location.hash = '#/wallet/setting' })
    await page.getByRole('button', { name: '设置地址 DID', exact: true }).click()
    await page.getByRole('heading', { name: '设置地址 DID', exact: true }).waitFor()
  }
  const saveDID = async (page, did) => {
    await page.getByLabel('地址 DID（可选）', { exact: true }).fill(did)
    await page.getByRole('button', { name: '保存 DID', exact: true }).click()
    await expect(page.getByText('DID 已保存', { exact: true })).toBeVisible()
  }

  await scenario('usage: independent address DID entry persists changes and clearing through recovery', () => using(async page => {
    await didSettings(page)
    await saveDID(page, 'alice.btc')
    await saveDID(page, 'bob.btc')
    const before = await catalog(page)
    assert.equal(before[0].accounts[0].did, 'bob.btc')
    await page.reload(); await ready(page); await unlock(page)
    await didSettings(page)
    await expect(page.getByLabel('地址 DID（可选）', { exact: true })).toHaveValue('bob.btc')
    await settings(page)
    await expect(page.getByRole('textbox', { name: '子账户 0 的已保存 DID', exact: true })).toHaveValue('bob.btc')
    assert.equal(await page.getByRole('textbox', { name: '子账户 0 的已保存 DID', exact: true }).getAttribute('readonly'), '')
    const material = await setup(page)
    const restored = await device()
    try {
      await restore(restored, material)
      assert.equal((await catalog(restored))[0].accounts[0].did, 'bob.btc')
      assert.equal((await catalog(restored))[0].accounts[0].name, before[0].accounts[0].name)
      await didSettings(restored)
      await restored.getByRole('button', { name: '清空 DID', exact: true }).click()
      await expect(restored.getByLabel('地址 DID（可选）', { exact: true })).toHaveValue('')
      await settled(restored)
      await restored.reload(); await ready(restored); await unlock(restored)
      assert.equal((await catalog(restored))[0].accounts[0].did || '', '')
      const cleared = await device()
      try {
        // The original recovery locator must load the latest managed state,
        // including an explicit empty DID; do not substitute draft metadata.
        await restore(cleared, material)
        assert.equal((await catalog(cleared))[0].accounts[0].did || '', '')
      } finally { await cleared.context().close() }
    } finally { await restored.context().close() }
  }))

  await scenario('usage: address DID persistence failure leaves catalog and recovery value unchanged', () => using(async page => {
    await didSettings(page)
    await saveDID(page, 'saved.btc')
    const before = await catalog(page)
    await page.evaluate(() => {
      const put = IDBObjectStore.prototype.put
      window.__restoreDIDPut = () => { IDBObjectStore.prototype.put = put }
      IDBObjectStore.prototype.put = function(value, key) {
        if (this.name === 'kv' && String(key).endsWith('account-management-profile-v2')) {
          throw new DOMException('DID profile persistence failure', 'QuotaExceededError')
        }
        return put.call(this, value, key)
      }
    })
    try {
      await page.getByLabel('地址 DID（可选）', { exact: true }).fill('rejected.btc')
      await page.getByRole('button', { name: '保存 DID', exact: true }).click()
      await expect(page.getByRole('alert')).toContainText('DID profile persistence failure')
      assert.deepEqual(await catalog(page), before)
    } finally { await page.evaluate(() => window.__restoreDIDPut()) }
    await page.reload(); await ready(page); await unlock(page)
    await didSettings(page)
    await expect(page.getByLabel('地址 DID（可选）', { exact: true })).toHaveValue('saved.btc')
  }))

  await scenario('usage: remote deletion consumes stale local edits after reload and publishes unrelated changes', () => using(async page => {
    await page.evaluate(async credential => {
      const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(credential)
      if (error) throw error
    }, password)
    const material = await setup(page)
    const replica = await device()
    const routes = t.nodeAPIOrigins.map(origin => `${origin}/**`)
    const unavailable = route => route.abort('failed')
    try {
      await restore(replica, material)
      const before = await catalog(page)
      const rootID = (await accountCall(page, 'status')).root_wallet_id
      const child = before.find(wallet => String(wallet.id) !== String(rootID))
      const replicaChild = (await catalog(replica)).find(wallet => wallet.fingerprint === child.fingerprint)
      for (const route of routes) await replica.context().route(route, unavailable)
      await replica.evaluate(async ({ childID, rootID }) => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        await store.switchWallet(childID)
        await store.updateWalletName(childID, 'Stale renamed child')
        await store.addAccount('Stale child account', 1)
        await store.updateAccountDID(1, 'stale.btc')
        await store.updateWalletName(rootID, 'Local independent root')
      }, { childID: String(replicaChild.id), rootID: String((await accountCall(replica, 'status')).root_wallet_id) })
      const localPending = (await accountCall(replica, 'status')).pending_changes
      assert.ok(localPending > 0)
      await replica.reload(); await ready(replica); await unlock(replica)
      await page.evaluate(async ({ childID, rootID }) => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        const [error] = await store.deleteWallet(childID)
        if (error) throw error
        await store.switchWallet(rootID)
        await store.updateAccountName(0, 'Remote independent account')
      }, { childID: String(child.id), rootID: String(rootID) })
      await settled(page)
      for (const route of routes) await replica.context().unroute(route, unavailable)
      await settled(replica)
      const semantic = value => value.map(wallet => ({ fingerprint: wallet.fingerprint, name: wallet.name,
        accounts: wallet.accounts.map(account => ({ index: account.index, name: account.name, did: account.did || '' })) }))
      await expect.poll(async () => semantic(await catalog(page)), { timeout: 90000 }).toEqual(semantic(await catalog(replica)))
      const final = await catalog(replica)
      assert.equal(final.length, 1)
      assert.equal(final[0].name, 'Local independent root')
      assert.equal(final[0].accounts[0].name, 'Remote independent account')
      assert.equal(final.some(wallet => wallet.fingerprint === child.fingerprint), false)
      await replica.reload(); await ready(replica); await unlock(replica)
      assert.equal((await accountCall(replica, 'status')).pending_changes || 0, 0)
      assert.equal((await catalog(replica)).length, 1)
      const displayed = await replica.evaluate(() => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        return { wallets: store.wallets.length, walletID: String(store.walletId),
          account: Number(store.accountIndex), address: store.address, pubkey: store.publicKey }
      })
      const root = (await catalog(replica))[0]
      assert.equal(displayed.wallets, 1)
      assert.equal(displayed.walletID, String(root.id))
      assert.equal(displayed.account, 0)
      assert.equal(displayed.address, root.accounts[0].address)
      assert.equal(displayed.pubkey, root.accounts[0].pub_key)
    } finally {
      for (const route of routes) await replica.context().unroute(route, unavailable)
      await replica.context().close()
    }
  }))

  await scenario('usage: committed creation and import pages recover from a catalog read failure without replay', async () => {
    for (const entry of ['create', 'import', 'manager']) {
      const page = entry === 'manager' ? await fresh() : await device()
      try {
        const before = await catalog(page)
        await page.evaluate(async entry => {
          const sdk = (await import('/utils/sat20.ts')).default
          const read = sdk.getWalletCatalog.bind(sdk)
          let armed = false
          window.committedPageFaults = 0
          window.committedPageSubmissions = 0
          sdk.getWalletCatalog = async (...args) => {
            if (armed) {
              armed = false; window.committedPageFaults++
              return [new Error('usage post-commit catalog read failure'), undefined]
            }
            return read(...args)
          }
          const method = entry === 'import' ? 'importWallet' : 'createWallet'
          const submit = sdk[method].bind(sdk)
          sdk[method] = async (...args) => {
            window.committedPageSubmissions++
            const result = await submit(...args)
            if (!result[0] && result[1]) armed = true
            return result
          }
          location.hash = entry === 'manager' ? '#/wallet/manager' : `#/${entry}`
        }, entry)
        if (entry === 'import') {
          await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(importMnemonic)
          await page.getByLabel('New Wallet Password', { exact: true }).fill(password)
          await page.getByLabel('Confirm Password', { exact: true }).fill(password)
          await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
          await expect(page.getByRole('button', { name: 'Reload wallet', exact: true })).toBeVisible({ timeout: 90000 })
          await expect(page.getByRole('button', { name: 'Import Wallet', exact: true })).toBeDisabled()
        } else if (entry === 'create') {
          await page.locator('input[type="password"]').nth(0).fill(password)
          await page.locator('input[type="password"]').nth(1).fill(password)
          await page.locator('button[type="submit"]').click()
        } else {
          await page.getByRole('button', { name: 'Create Wallet', exact: true }).click()
          await confirmPassword(page)
        }
        if (entry !== 'import') await expect(page.getByRole('button', { name: 'I saved my phrase, reload wallet', exact: true })).toBeVisible({ timeout: 90000 })
        const committed = await catalog(page)
        const added = committed.filter(wallet => !before.some(old => String(old.id) === String(wallet.id)))
        assert.equal(added.length, 1)
        const mnemonic = entry === 'import' ? importMnemonic : (await page.locator('.grid.grid-cols-3 .blur-sm').allTextContents()).map(word => word.trim()).join(' ')
        assert.equal((await walletCall(page, 'validateMnemonic', mnemonic, '')).fingerprint, added[0].fingerprint)
        assert.equal(await page.evaluate(() => window.committedPageFaults), 1)
        assert.equal(await page.evaluate(() => window.committedPageSubmissions), 1)
        await page.getByRole('button', { name: entry === 'import' ? 'Reload wallet' : 'I saved my phrase, reload wallet', exact: true }).click()
        await ready(page)
        await page.locator('input[type="password"]').fill(password)
        await page.locator('form button[type="submit"]').click()
        await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: 90000 })
        assert.deepEqual(await catalog(page), committed)
      } finally { await page.context().close() }
    }
  })

  await scenario('usage: maintenance rehearsal obeys the one minute inactivity lock and discards its session', () => using(async page => {
    await setup(page)
    await page.evaluate(async () => {
      const global = (await import('/store/global.ts')).useGlobalStore()
      await global.setAutoLockTime('1')
      const load = window.accountE2E.loadRecovery.bind(window.accountE2E)
      window.accountE2E.loadRecovery = async (...args) => {
        const result = await load(...args); window.inactivitySession = result.session_id; return result
      }
      location.hash = '#/wallet/setting/account-management'
    })
    await page.getByRole('button', { name: '只读恢复演练', exact: true }).click()
    await page.getByRole('button', { name: '加载加密账户备份', exact: true }).click()
    await expect(page.locator('input[type="password"]').first()).toBeVisible()
    const session = await page.evaluate(() => window.inactivitySession)
    assert.ok(session)
    const idleStarted = Date.now()
    // Observe the real timer without clicks, fake time or a direct lock call.
    await expect.poll(() => new URL(page.url()).hash, { timeout: 80000, intervals: [1000] }).toMatch(/^#\/unlock/)
    assert.ok(Date.now() - idleStarted >= 55000, 'inactivity lock fired before the configured minute')
    assert.equal(await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), true)
    await unlock(page)
    await assert.rejects(accountCall(page, 'previewRecovery', session))
    await expect(page.getByPlaceholder('可选：粘贴 sat20share1:...')).toHaveCount(0)
  }))

  await scenario('usage: SDK creation page delivers mnemonic without redundant PWA persistence', async () => {
    const page = await device()
    const before = await catalog(page)
    assert.equal(before.length, 0, 'the initial create page requires an empty wallet device')
    await page.evaluate(() => {
      const put = IDBObjectStore.prototype.put
      window.usageRedundantWrites = 0
      window.restoreUsagePut = () => { IDBObjectStore.prototype.put = put }
      IDBObjectStore.prototype.put = function(value, key) {
        if (this.name === 'wallet-state' && String(key).endsWith('wallet_state_snapshot_v1')) {
          window.usageRedundantWrites++
          throw new DOMException('usage denied redundant UI persistence', 'QuotaExceededError')
        }
        return put.call(this, value, key)
      }
      location.hash = '#/create'
    })
    try {
      await page.locator('input[type="password"]').nth(0).fill(password)
      await page.locator('input[type="password"]').nth(1).fill(password)
      await page.locator('button[type="submit"]').click()
      await expect(page.getByRole('button', { name: 'Copy to clipboard', exact: true })).toBeVisible({ timeout: 120000 })
      assert.equal(await page.evaluate(() => window.usageRedundantWrites), 0)
      const committed = await catalog(page)
      assert.equal(committed.length, before.length + 1)
      const words = await page.locator('.grid.grid-cols-3 .blur-sm').allTextContents()
      assert.ok([12, 24].includes(words.length), 'creation did not deliver the actual recovery phrase')
      const mnemonic = words.map(word => word.trim()).join(' ')
      const derived = await walletCall(page, 'validateMnemonic', mnemonic, '')
      const added = committed.filter(wallet => !before.some(old => String(old.id) === String(wallet.id)))
      assert.equal(added.length, 1, 'creation must add exactly one wallet')
      assert.equal(derived.fingerprint, added[0].fingerprint, 'displayed mnemonic must belong to the newly committed wallet')
      await page.reload(); await ready(page); await unlock(page)
      const displayed = await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().wallets.map(wallet => wallet.fingerprint).sort())
      assert.deepEqual(displayed, committed.map(wallet => wallet.fingerprint).sort())
      assert.deepEqual(await catalog(page), committed)
    } finally {
      await page.evaluate(() => window.restoreUsagePut?.()).catch(() => {})
      await page.context().close()
    }
  })

  await scenario('usage: import page and cold selection use SDK without redundant PWA persistence', async () => {
    const page = await device()
    try {
      await page.evaluate(() => {
        const put = IDBObjectStore.prototype.put
        window.usageRedundantWrites = 0
        IDBObjectStore.prototype.put = function(value, key) {
          if (this.name === 'wallet-state' && String(key).endsWith('wallet_state_snapshot_v1')) {
            window.usageRedundantWrites++
            throw new DOMException('usage denied redundant UI persistence', 'QuotaExceededError')
          }
          return put.call(this, value, key)
        }
        location.hash = '#/import'
      })
      await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(importMnemonic)
      await page.getByLabel('New Wallet Password', { exact: true }).fill(password)
      await page.getByLabel('Confirm Password', { exact: true }).fill(password)
      await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
      await expect.poll(() => new URL(page.url()).hash, { timeout: 90000 }).toBe('#/wallet')
      assert.equal(await page.evaluate(() => window.usageRedundantWrites), 0)
      await page.evaluate(async () => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        await store.addAccount('Persistent selection', 1)
        await store.switchToAccount(1)
      })
      const before = await page.evaluate(() => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        return { walletId: store.walletId, index: store.accountIndex, address: store.address, pubkey: store.publicKey }
      })
      await page.reload(); await ready(page); await unlock(page)
      const after = await page.evaluate(() => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        return { walletId: store.walletId, index: store.accountIndex, address: store.address, pubkey: store.publicKey }
      })
      assert.deepEqual(after, before)
      const saved = await page.evaluate(async () => {
        const { Storage } = await import('/lib/storage-adapter.ts')
        const raw = (await Storage.get({ key: 'local:wallet_state_snapshot_v1' })).value
        return raw ? Object.keys(JSON.parse(raw).state) : []
      })
      for (const key of ['wallets','walletId','accountIndex','address','pubkey','locked','rootAccountId','hasWallet']) assert.equal(saved.includes(key), false)
    } finally { await page.context().close() }
  })

  await scenario('usage: IndexedDB open failure recovers on explicit retry', () => using(async page => {
    const result = await page.evaluate(async () => {
      const { Storage } = await import(`/lib/storage-adapter.ts?open-retry=${Date.now()}`)
      const open = IDBFactory.prototype.open
      IDBFactory.prototype.open = function(...args) {
        throw new DOMException('usage injected open failure', 'UnknownError')
      }
      let failure
      try { await Storage.get({ key: 'local:wallet_state_snapshot_v1' }) }
      catch (error) { failure = error.message }
      finally { IDBFactory.prototype.open = open }
      const retried = await Storage.get({ key: 'local:wallet_state_snapshot_v1' })
      return { failure, retried: typeof retried === 'object' }
    })
    assert.match(result.failure, /usage injected open failure/)
    assert.equal(result.retried, true)
  }))

  await scenario('usage: PWA snapshot read failure preserves durable and displayed account state', () => using(async page => {
    const result = await page.evaluate(async () => {
      const { walletStorage } = await import('/lib/walletStorage.ts')
      const { Storage } = await import('/lib/storage-adapter.ts')
      const key = 'local:wallet_state_snapshot_v1'
      const before = await Storage.get({ key })
      const displayed = JSON.stringify(walletStorage.getState())
      const get = IDBObjectStore.prototype.get
      let injected = false
      IDBObjectStore.prototype.get = function(key) {
        if (this.name === 'wallet-state' && String(key).endsWith('wallet_state_snapshot_v1')) {
          injected = true
          throw new DOMException('usage injected snapshot read failure', 'UnknownError')
        }
        return get.call(this, key)
      }
      let failure
      try { await walletStorage.setValue('hideBalance', !walletStorage.getValue('hideBalance')) }
      catch (error) { failure = error.message }
      finally { IDBObjectStore.prototype.get = get }
      return { injected, failure, sameDisplay: JSON.stringify(walletStorage.getState()) === displayed,
        sameDurable: (await Storage.get({ key })).value === before.value }
    })
    assert.equal(result.injected, true)
    assert.match(result.failure, /usage injected snapshot read failure/)
    assert.equal(result.sameDisplay, true)
    assert.equal(result.sameDurable, true)
    await page.reload(); await ready(page); await unlock(page)
    assert.equal((await catalog(page)).length, 1)
  }))

  await scenario('usage: cold startup SDK status read failure preserves selection and retries from page', () => using(async page => {
    const before = await catalog(page)
    const readStatus = async () => {
      const db = await new Promise((resolve, reject) => {
        const request = indexedDB.open('sat20-wallet-sdk', 1)
        request.onsuccess = () => resolve(request.result)
        request.onerror = () => reject(request.error)
      })
      try {
        return await new Promise((resolve, reject) => {
          const tx = db.transaction('kv', 'readonly')
          const get = tx.objectStore('kv').get('wallet-status')
          tx.oncomplete = () => resolve(JSON.stringify(get.result))
          tx.onabort = () => reject(tx.error)
        })
      } finally { db.close() }
    }
    const status = await page.evaluate(readStatus)
    assert.ok(status, 'fixture must have persisted SDK selection')
    await page.context().addInitScript(() => {
      const get = IDBObjectStore.prototype.get
      IDBObjectStore.prototype.get = function(key) {
        if (sessionStorage.getItem('usage-startup-status-fault') === 'armed' && this.name === 'kv' && String(key) === 'wallet-status') {
          sessionStorage.setItem('usage-startup-status-hit', 'yes')
          throw new DOMException('usage injected cold SDK status read failure', 'UnknownError')
        }
        return get.call(this, key)
      }
    })
    await page.evaluate(() => sessionStorage.setItem('usage-startup-status-fault', 'armed'))
    await page.reload()
    await page.locator('#app [role="alert"]').waitFor({ timeout: 90000 })
    assert.equal(await page.evaluate(() => sessionStorage.getItem('usage-startup-status-hit')), 'yes')
    assert.equal(await page.evaluate(() => Boolean(window.__SAT20_PWA_VERIFY__)), false)
    await page.evaluate(() => sessionStorage.removeItem('usage-startup-status-fault'))
    assert.equal(await page.evaluate(readStatus), status, 'failed startup overwrote SDK selection')
    await Promise.all([page.waitForEvent('domcontentloaded'), page.locator('#app button').click()])
    await ready(page); await unlock(page)
    assert.deepEqual(await catalog(page), before)
  }))

  await scenario('usage: browser password transaction failure preserves every wallet and original password', () => using(async page => {
    await page.evaluate(async credential => {
      const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(credential)
      if (error) throw error
    }, password)
    const before = await catalog(page)
    const failure = await page.evaluate(async ({ old, next }) => {
      const put = IDBObjectStore.prototype.put
      let hits = 0
      IDBObjectStore.prototype.put = function(value, key) {
        if (this.name === 'kv' && String(key).includes('wallet-id-') && ++hits === 2) {
          throw new DOMException('usage injected password transaction failure', 'QuotaExceededError')
        }
        return put.call(this, value, key)
      }
      try { const [error] = await window.sat20.changePassword(old, next); return { hits, error: error?.message } }
      finally { IDBObjectStore.prototype.put = put }
    }, { old: password, next: newPassword })
    assert.equal(failure.hits, 2)
    assert.match(failure.error, /usage injected password transaction failure/)
    await page.reload(); await ready(page)
    await assert.rejects(walletCall(page, 'unlockWallet', newPassword))
    await unlock(page)
    assert.deepEqual(await catalog(page), before)
    for (const wallet of before) {
      await page.evaluate(async id => window.__SAT20_PWA_VERIFY__.useWalletStore().switchWallet(id), String(wallet.id))
      await unlock(page)
    }
  }))

  await scenario('usage: newly created account publishes first recovery package after initial sync settles', () => using(setup))
  await scenario('usage: same temporary mode reconfiguration converges across cold devices and root recovery', () => using(async page => {
    await setup(page)
    const root = (await catalog(page))[0]
    const { mnemonic } = await walletCall(page,'getMnemonice',String(root.id),password)
    await sameModeReconfiguration(t,page,password,'temporary',questions,mnemonic)
  }))

  await scenario('usage: account page refreshes background backup failure and success without navigation', () => using(async page => {
    await setup(page)
    await page.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
    await page.getByRole('heading', { name: '维护恢复配置', exact: true }).waitFor()
    await page.bringToFront()
    const card = page.getByRole('alert', { name: '账户备份状态', exact: true })
    await expect(card).toContainText('待同步变更：0')
    // Pending can finish before the background worker clears an earlier CAS
    // error. Establish a healthy baseline before injecting this outage.
    await expect.poll(async () => (await accountCall(page, 'status')).last_dkvs_sync_error || '', { timeout: 90000 }).toBe('')
    await expect(card).not.toContainText('DKVS 同步异常', { timeout: 45000 })
    const location = page.url()
    const unavailable = route => route.abort('failed')
    const routes = t.nodeAPIOrigins.map(origin => `${origin}/**`)
    try {
      for (const route of routes) await page.context().route(route, unavailable)
      await page.evaluate(async () => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        await store.updateWalletName(store.walletId, 'Background backup status')
      })
      await expect.poll(async () => Boolean((await accountCall(page, 'status')).last_dkvs_sync_error), { timeout: 90000 }).toBe(true)
      await expect(card).toContainText('DKVS 同步异常', { timeout: 45000 })
      await expect(card).not.toContainText('待同步变更：0')
      for (const route of routes) await page.context().unroute(route, unavailable)
      await settled(page)
      await expect(card).toContainText('待同步变更：0', { timeout: 45000 })
      await expect(card).not.toContainText('必要数据正在等待同步')
      await expect(card).not.toContainText('DKVS 同步异常')
      assert.equal(page.url(), location)
    } finally { for (const route of routes) await page.context().unroute(route, unavailable) }
  }))

  await scenario('usage: offline catalog edits survive reload and converge with another device', async () => {
    const page = await device()
    const material = t.material
    try {
      await restore(page, material)
      await page.evaluate(async credential => {
        const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(credential)
        if (error) throw error
      }, password)
      await settled(page)
      const before = await catalog(page)
      const status = await accountCall(page, 'status')
      const root = before.find(wallet => String(wallet.id) === String(status.root_wallet_id))
      const child = before.find(wallet => wallet.fingerprint !== root.fingerprint)
      const replica = await device()
      const unavailable = route => route.abort('failed')
      const routes = t.nodeAPIOrigins.map(origin => `${origin}/**`)
      try {
        await restore(replica, material)
        for (const route of routes) await page.context().route(route, unavailable)
        await page.evaluate(async ({ rootID, childID }) => {
          const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
          await store.updateWalletName(rootID, 'Offline durable root')
          await store.switchWallet(rootID)
          await store.addAccount('Offline savings', 2)
          const [error] = await store.deleteWallet(childID)
          if (error) throw error
        }, { rootID: String(root.id), childID: String(child.id) })
        assert.ok((await accountCall(page, 'status')).pending_changes > 0)
        const offline = await catalog(page)
        await page.reload(); await ready(page); await unlock(page)
        assert.deepEqual(await catalog(page), offline)
        await replica.evaluate(async () => {
          const root = (await window.sat20.getWalletCatalog())[1].wallets[0]
          const [error] = await window.sat20.updateAccountMetadata(String(root.id), 0, 'Online independent edit', 'did:online')
          if (error) throw error
        })
        await settled(replica)
        for (const route of routes) await page.context().unroute(route, unavailable)
        await settled(page)
        await expect.poll(async () => (await catalog(replica)).map(wallet => ({ fingerprint: wallet.fingerprint, name: wallet.name, accounts: wallet.accounts })), { timeout: 90000 })
          .toEqual((await catalog(page)).map(wallet => ({ fingerprint: wallet.fingerprint, name: wallet.name, accounts: wallet.accounts })))
        const final = await catalog(page)
        assert.equal(final.length, 1)
        assert.equal(final[0].name, 'Offline durable root')
        assert.equal(final[0].accounts[0].name, 'Online independent edit')
        assert.equal(final[0].accounts.find(account => account.index === 2).name, 'Offline savings')
        assert.equal(final.some(wallet => wallet.fingerprint === child.fingerprint), false)
      } finally {
        for (const route of routes) await page.context().unroute(route, unavailable)
        await replica.context().close()
      }
    } finally { await page.context().close() }
  })

  await scenario('usage: shared IndexedDB tabs invalidate pending writes when another tab locks', () => using(async page => {
    const second = await device(page.context())
    // Authentication alone keeps the same SDK selection and does not require
    // a peer reload. The lock below is the identity transition under test.
    await unlock(second)
    const held = second.evaluate(async () => {
      const api = window.accountE2E
      const original = api.confirmStorage.bind(api)
      // Hold the real operation before dispatch through the existing facade.
      const { walletRequestSessionGuard } = await import('/lib/walletSession.ts')
      const checkSession = walletRequestSessionGuard('account.confirmStorage')
      window.releaseUsageRequest = null
      await new Promise(resolve => { window.releaseUsageRequest = resolve })
      try { checkSession(); await original('temporary'); return { rejected: false } }
      catch { return { rejected: true } }
    }).catch(error => ({ rejected: /context|closed|navigation|destroyed/i.test(error.message) }))
    await second.waitForFunction(() => typeof window.releaseUsageRequest === 'function')
    // BroadcastChannel delivery should quiesce the second page before reload.
    const reloaded = second.waitForEvent('domcontentloaded', { timeout: 90000 })
    await Promise.all([page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().lockWallet()), reloaded])
    const result = await held
    assert.equal(result.rejected, true)
    await ready(second)
    assert.equal(await second.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), true)
    assert.equal(await accountCall(second, 'resumePendingStorageAuthorization'), null)
    await assert.rejects(accountCall(second, 'confirmStorage', 'temporary'), /unlocked|session/i)
    assert.equal((await catalog(second)).length, 1)
  }))

  await scenario('usage: shared IndexedDB subaccount switch discards another tab previous-identity response', () => using(async page => {
    await page.evaluate(async () => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      await store.addAccount('Shared tab savings', 1)
      await store.switchToAccount(0)
    })
    const second = await device(page.context())
    const held = second.evaluate(async () => {
      const { walletRequestSessionGuard } = await import('/lib/walletSession.ts')
      const checkSession = walletRequestSessionGuard('account.status')
      const previous = await window.accountE2E.status()
      await new Promise(resolve => { window.releaseUsageRequest = resolve })
      try { checkSession(); return { accepted: true, previous } }
      catch { return { accepted: false } }
    }).catch(error => ({ accepted: !/context|closed|navigation|destroyed/i.test(error.message) }))
    await second.waitForFunction(() => typeof window.releaseUsageRequest === 'function')
    const reloaded = second.waitForEvent('domcontentloaded', { timeout: 90000 })
    await Promise.all([page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().switchToAccount(1)), reloaded])
    assert.equal((await held).accepted, false, 'old subaccount response was accepted after the other tab switched')
    await ready(second); await unlock(second)
    const selected = await second.evaluate(() => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      return { index: store.accountIndex, address: store.address, pubKey: store.publicKey }
    })
    assert.equal(selected.index, 1)
    assert.equal(selected.address, (await walletCall(second, 'getWalletAddress', 1)).address)
    assert.equal(selected.pubKey, (await walletCall(second, 'getWalletPubkey', 1)).pubKey)
  }))

  await scenario('usage: shared IndexedDB wallet switch survives another tab rebuilding before commit', () => using(async page => {
    const rootId = await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().walletId)
    const childId = await page.evaluate(async ({ credential, rootId }) => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      const [error] = await store.createWallet(credential)
      if (error) throw error
      const childId = store.walletId
      await store.switchWallet(rootId)
      return childId
    }, { credential: password, rootId })
    assert.notEqual(childId, rootId)
    const second = await device(page.context())
    const rebuilt = second.waitForEvent('domcontentloaded', { timeout: 90000 }).then(() => null, error => error)
    // Keep the real SDK switch pending before its durable commit. The peer's
    // existing cross-tab reload then rebuilds from the previous selection.
    const switching = page.evaluate(async id => {
      const api = window.sat20wallet_wasm
      const original = api.switchWallet
      api.switchWallet = async (...args) => {
        await new Promise(resolve => { window.releaseUsageWalletSwitch = resolve })
        return original(...args)
      }
      try { await window.__SAT20_PWA_VERIFY__.useWalletStore().switchWallet(id) }
      finally { api.switchWallet = original }
    }, childId).then(() => null, error => error)
    try {
      await page.waitForFunction(() => typeof window.releaseUsageWalletSwitch === 'function')
      assert.equal(await rebuilt, null, 'peer did not reload during the pending wallet switch')
      await ready(second)
      await page.evaluate(() => window.releaseUsageWalletSwitch())
      assert.equal(await switching, null, 'real SDK wallet switch failed')
      const selected = await second.evaluate(async credential => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        const [error, result] = await store.unlockWallet(credential)
        if (error) throw error
        return { displayed: store.walletId, manager: result.walletId, address: store.address }
      }, password)
      assert.equal(selected.displayed, childId, 'peer overwrote the committed wallet selection')
      assert.equal(selected.manager, childId, 'peer SDK unlocked the previous wallet')
      assert.equal(selected.address, (await walletCall(second, 'getWalletAddress', 0)).address)
    } finally {
      await page.evaluate(() => window.releaseUsageWalletSwitch?.()).catch(() => {})
      await switching.catch(() => {})
    }
  }))

  await scenario('usage: shared IndexedDB password change cannot authorize an old-password tab', () => using(async page => {
    const guardianIdentity = await accountCall(page, 'guardianIdentity', password)
    const second = await device(page.context())
    await unlock(second)
    await walletCall(page, 'changePassword', password, newPassword)
    // Another Manager must not authenticate using its stale cached ciphertext.
    const acceptedOld = await walletCall(second, 'unlockWallet', password).then(() => true, () => false)
    await second.reload(); await ready(second)
    await assert.rejects(walletCall(second, 'unlockWallet', password))
    await unlock(second, newPassword)
    assert.equal((await catalog(second)).length, 1)
    assert.equal(acceptedOld, false, 'second tab authenticated with the replaced password')
    await assert.rejects(accountCall(second, 'guardianIdentity', password))
    assert.deepEqual(await accountCall(second, 'guardianIdentity', newPassword), guardianIdentity)
  }))

  const realTwoFactorSetupUI = async (page, failStatus = false) => {
    await preflightUI(page)
    await page.getByRole('button', { name: '2/2 增强安全', exact: true }).click()
    await temporaryUI(page)
    await fillQuestions(page)
    await page.getByPlaceholder('再次输入答案', { exact: true }).first().fill('mismatched answer')
    await page.getByRole('button', { name: '创建并保存加密账户备份', exact: true }).click()
    await confirmPassword(page)
    await page.getByRole('alert').filter({ hasText: '两次答案一致' }).waitFor()
    assert.equal((await accountCall(page, 'status')).recovery_configured, false)
    await fillQuestions(page)
    await page.getByRole('button', { name: '创建并保存加密账户备份', exact: true }).click()
    await confirmPassword(page)
    await page.getByRole('button', { name: '进入恢复演练', exact: true }).waitFor()
    const share = await page.locator('textarea[readonly]').nth(1).inputValue()
    await page.getByRole('button', { name: '进入恢复演练', exact: true }).click()
    for (let i = 0; i < 2; i++) await page.getByPlaceholder(`问题 ${i + 1} 的答案`, { exact: true }).fill(questions[i].answer)
    await page.getByPlaceholder('重新粘贴用户分片', { exact: true }).fill(share)
    if (failStatus) await page.evaluate(() => {
      const rehearse = window.accountE2E.rehearse.bind(window.accountE2E)
      const status = window.accountE2E.status.bind(window.accountE2E)
      window.setupCompletionFault = { armed: false, hits: 0, verified: 0 }
      window.accountE2E.rehearse = async (...args) => {
        const result = await rehearse(...args)
        if (result.verified) { window.setupCompletionFault.verified++; window.setupCompletionFault.armed = true }
        return result
      }
      window.accountE2E.status = async () => {
        if (window.setupCompletionFault.armed) {
          window.setupCompletionFault.armed = false; window.setupCompletionFault.hits++
          throw new Error('usage injected final configuration status read failure')
        }
        return status()
      }
    })
    await page.getByRole('button', { name: '执行恢复演练', exact: true }).click()
    await confirmPassword(page)
    // First publication and active-scope refresh can advance the captured
    // baseline more than once. Exercise bounded user retries of the same
    // real session; every CAS refusal must leave recovery unconfigured.
    for (let attempt = 0; attempt < 4; attempt++) {
      let rehearsalOutcome
      await expect.poll(async () => {
        if (await page.getByRole('heading', { name: '账户恢复已配置', exact: true }).isVisible()) {
          rehearsalOutcome = 'configured'
        } else if (await page.getByRole('alert').filter({ hasText: 'publish account activation state: dkvs write conflict' }).isVisible()) {
          rehearsalOutcome = 'conflict'
        } else rehearsalOutcome = 'waiting'
        return rehearsalOutcome
      }, { timeout: 90000 }).not.toBe('waiting')
      if (rehearsalOutcome === 'configured') break
      assert.equal((await accountCall(page, 'status')).recovery_configured, false, 'CAS refusal falsely marked recovery configured')
      console.log(JSON.stringify({ activationUserRetry: attempt + 1, outcome: rehearsalOutcome }))
      assert.ok(attempt < 3, 'account activation did not converge within three user retries')
      await expect.poll(async () => {
        const status = await accountCall(page, 'status')
        return status.state_seq > 0 && status.managed_data_revision > 0 &&
          !status.managed_data_dirty && (status.pending_changes ?? 0) === 0
      }, { timeout: 90000 }).toBe(true)
      await page.getByRole('button', { name: '执行恢复演练', exact: true }).click()
      await confirmPassword(page)
    }
    await page.getByRole('heading', { name: '账户恢复已配置', exact: true }).waitFor()
    if (failStatus) {
      assert.deepEqual(await page.evaluate(() => window.setupCompletionFault), { armed: false, hits: 1, verified: 1 })
      await page.getByRole('status').filter({ hasText: 'usage injected final configuration status read failure' }).waitFor()
      assert.equal(await page.getByRole('button', { name: '执行恢复演练', exact: true }).count(), 0)
      await page.getByRole('button', { name: '重试读取备份状态', exact: true }).click()
      await expect(page.getByText('usage injected final configuration status read failure', { exact: true })).toHaveCount(0)
      assert.equal(await page.evaluate(() => window.setupCompletionFault.verified), 1, 'display refresh replayed consumed activation')
    }
    assert.equal((await accountCall(page, 'status')).recovery_configured, true)
    await assertNoSecrets(page, [...questions.map(q => q.answer), share, password])
  }
  await scenario('usage: real 2of2 setup page validates answers and completes rehearsal', () => using(page => realTwoFactorSetupUI(page)))
  await scenario('usage: verified setup survives final status read failure without replaying rehearsal', () => using(page => realTwoFactorSetupUI(page, true)))

  await scenario('usage: configured account exposes maintenance and cancels without replacing recovery', () => using(async page => {
    const material = await setup(page)
    const before = await accountCall(page, 'status')
    const beforeCatalog = await catalog(page)
    await page.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
    await page.getByRole('button', { name: '重新配置恢复或升级存储', exact: true }).click()
    await page.getByRole('button', { name: '检查账户', exact: true }).click()
    await confirmPassword(page)
    await page.getByRole('button', { name: '2/2 增强安全', exact: true }).click()
    await temporaryUI(page)
    await page.getByRole('button', { name: '取消本次设置', exact: true }).click()
    await page.getByRole('button', { name: '返回当前配置', exact: true }).click()
    await page.getByRole('button', { name: '复制当前公开恢复码', exact: true }).waitFor()
    const current = await accountCall(page, 'status')
    assert.equal(current.package_id, before.package_id)
    assert.equal(current.public_locator, material.locator)
    assert.deepEqual(await catalog(page), beforeCatalog)
    assert.equal(await accountCall(page, 'resumePendingStorageAuthorization'), null)
    await page.reload(); await ready(page); await unlock(page)
    await page.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
    await page.getByRole('button', { name: '重新配置恢复或升级存储', exact: true }).click()
    await page.getByRole('button', { name: '检查账户', exact: true }).waitFor()
    assert.equal((await accountCall(page, 'status')).public_locator, material.locator)
  }))

  await scenario('usage: configured recovery rehearsal previews real backup without committing or changing wallet', () => using(async page => {
    const material = await setup(page)
    const beforeCatalog = await catalog(page)
    const before = await accountCall(page, 'status')
    const selection = await page.evaluate(() => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      return { walletId: store.walletId, accountIndex: store.accountIndex, address: store.address, rootAccountId: store.rootAccountId }
    })
    await page.evaluate(() => {
      window.maintenanceCommits = 0
      const commit = window.accountE2E.commitRecovery.bind(window.accountE2E)
      window.accountE2E.commitRecovery = async (...args) => { window.maintenanceCommits++; return commit(...args) }
      const load = window.accountE2E.loadRecovery.bind(window.accountE2E)
      window.accountE2E.loadRecovery = async (...args) => {
        const result = await load(...args); window.maintenanceSession = result.session_id; return result
      }
      location.hash = '#/wallet/setting/account-management'
    })
    await page.getByRole('button', { name: '只读恢复演练', exact: true }).click()
    await page.getByRole('heading', { name: '只读恢复演练', exact: true }).waitFor()
    await expect(page.getByPlaceholder('粘贴 sat20account1:...')).toHaveValue(material.locator)
    await page.getByRole('button', { name: '加载加密账户备份', exact: true }).click()
    const inputs = page.locator('input[type="password"]')
    await inputs.nth(0).fill('incorrect private answer')
    await inputs.nth(1).fill('another incorrect answer')
    await page.getByRole('button', { name: '恢复加密分片', exact: true }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await inputs.nth(0).fill(questions[0].answer)
    await inputs.nth(1).fill(questions[1].answer)
    await page.getByRole('button', { name: '恢复加密分片', exact: true }).click()
    await page.getByPlaceholder('可选：粘贴 sat20share1:...').fill(material.user_share)
    await page.getByRole('button', { name: '使用用户分片', exact: true }).click()
    await expect(page.getByRole('button', { name: '预览恢复内容', exact: true })).toBeEnabled()
    await page.getByRole('button', { name: '预览恢复内容', exact: true }).click()
    await page.getByRole('heading', { name: '恢复材料验证通过', exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '恢复全部钱包', exact: true }).count(), 0)
    assert.equal(await page.getByPlaceholder('设置新的本地钱包密码').count(), 0)
    const session = await page.evaluate(() => window.maintenanceSession)
    await page.getByRole('button', { name: '完成演练并返回', exact: true }).click()
    await page.getByRole('button', { name: '只读恢复演练', exact: true }).waitFor()
    await assert.rejects(accountCall(page, 'previewRecovery', session))
    assert.equal(await page.evaluate(() => window.maintenanceCommits), 0)
    assert.deepEqual(await catalog(page), beforeCatalog)
    const current = await accountCall(page, 'status')
    assert.equal(current.package_id, before.package_id)
    assert.equal(current.public_locator, before.public_locator)
    assert.equal(current.last_rehearsal_at, before.last_rehearsal_at)
    assert.deepEqual(await page.evaluate(() => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      return { walletId: store.walletId, accountIndex: store.accountIndex, address: store.address, rootAccountId: store.rootAccountId }
    }), selection)
    await assertNoSecrets(page, [...questions.map(q => q.answer), material.user_share, password])
  }))

  await scenario('usage: real 2of3 settings and independent Guardian pages complete setup and rehearsal', () => using(async page => {
    const guardian = await fresh()
    try {
      await settings(guardian)
      await guardian.getByRole('button', { name: '生成我的联系信息并发送给好友', exact: true }).click()
      await confirmPassword(guardian)
      const contact = await guardian.locator('textarea[readonly]').first().inputValue()
      assert.ok(JSON.parse(contact).mailbox_id)
      await preflightUI(page)
      await temporaryUI(page)
      await fillQuestions(page)
      const contactInput = page.getByPlaceholder('粘贴好友钱包生成的 Guardian contact JSON')
      await contactInput.fill('{}')
      assert.equal(await page.getByRole('button', { name: '创建并保存加密账户备份', exact: true }).isDisabled(), true)
      await contactInput.fill(contact)
      await page.getByRole('button', { name: '创建并保存加密账户备份', exact: true }).click()
      await confirmPassword(page)
      await page.getByRole('button', { name: '验证 Guardian 密文已保存', exact: true }).waitFor()
      // Only the final, receipt-bound locator can be handed to the user.
      await expect(page.getByRole('button', { name: '复制恢复码', exact: true })).toHaveCount(0)
      await page.context().grantPermissions(['clipboard-read', 'clipboard-write'], { origin: new URL(page.url()).origin })
      await page.getByRole('button', { name: '复制用户分片', exact: true }).click()
      const savedShare = await page.evaluate(() => navigator.clipboard.readText())
      assert.equal(savedShare, await page.getByRole('textbox', { name: '秘密用户分片', exact: true }).inputValue())
      assert.ok(savedShare.startsWith('sat20-share-v1:'))
      const setupJSON = await page.getByRole('textbox', { name: 'Guardian setup', exact: true }).inputValue()
      await guardian.getByPlaceholder('好友发送的 Guardian setup JSON').fill(setupJSON)
      await guardian.locator('select').selectOption('temporary')
      await guardian.getByRole('button', { name: '接受并保存好友分片', exact: true }).click()
      await confirmPassword(guardian)
      await expect.poll(async () => guardian.locator('textarea[readonly]').count()).toBe(2)
      const receipt = await guardian.locator('textarea[readonly]').last().inputValue()
      await page.getByPlaceholder('粘贴 Guardian 返回的 receipt').fill(receipt)
      await page.getByRole('button', { name: '验证 Guardian 密文已保存', exact: true }).click()
      await expect(page.getByRole('heading', { name: '4. 保存恢复材料', exact: true })).toBeVisible()
      await page.getByRole('button', { name: '复制恢复码', exact: true }).click()
      const savedLocator = await page.evaluate(() => navigator.clipboard.readText())
      assert.ok(savedLocator.startsWith('sat20account1:'))
      await page.getByRole('button', { name: '我已保存最终恢复码，进入演练', exact: true }).click()
      await page.getByPlaceholder('重新粘贴已保存的最终恢复码').fill('incorrect saved locator')
      await page.getByRole('button', { name: '生成 Guardian 演练请求', exact: true }).click()
      await page.getByRole('alert').filter({ hasText: '请粘贴本次保存的最终恢复码' }).waitFor()
      assert.equal((await accountCall(page, 'status')).recovery_configured, false)
      await page.getByPlaceholder('重新粘贴已保存的最终恢复码').fill(savedLocator)
      await page.getByRole('button', { name: '生成 Guardian 演练请求', exact: true }).click()
      const request = await page.locator('textarea[readonly]').first().inputValue()
      await guardian.getByPlaceholder('好友发送的 Guardian 恢复请求 JSON').fill(request)
      await guardian.getByRole('button', { name: '生成加密恢复响应', exact: true }).click()
      await confirmPassword(guardian)
      await expect.poll(async () => guardian.locator('textarea[readonly]').count()).toBe(3)
      const response = await guardian.locator('textarea[readonly]').last().inputValue()
      await page.getByPlaceholder('粘贴 Guardian 返回的加密响应').fill(response)
      await page.getByRole('button', { name: '验证 Guardian 响应', exact: true }).click()
      for (let i = 0; i < 2; i++) await page.getByPlaceholder(`问题 ${i + 1} 的答案`, { exact: true }).fill(questions[i].answer)
      await page.getByRole('button', { name: '执行恢复演练', exact: true }).click()
      await confirmPassword(page)
      await page.getByRole('heading', { name: '账户恢复已配置', exact: true }).waitFor()
      const status = await accountCall(page, 'status')
      assert.equal(status.recovery_configured, true)
      assert.equal(status.recovery_mode, '2of3')
      await page.getByRole('button', { name: '复制当前公开恢复码', exact: true }).click()
      assert.equal(await page.evaluate(() => navigator.clipboard.readText()), savedLocator)
      const expectedCatalog = await catalog(page)
      await assertNoSecrets(page, [...questions.map(q => q.answer), savedShare, password])
      await assertNoSecrets(guardian, [...questions.map(q => q.answer), password])
      // Close the original device before either recovery. Never replace the
      // clipboard material with an SDK/session locator in this test.
      await page.context().close()
      for (const combination of ['knowledge+guardian', 'share+guardian']) {
        const recovery = await device()
        try {
          await recovery.evaluate(() => { location.hash = '#/restore-account' })
          await recovery.getByPlaceholder('粘贴 sat20account1:...').fill(savedLocator)
          await recovery.getByRole('button', { name: '加载加密账户备份', exact: true }).click()
          if (combination === 'knowledge+guardian') {
            for (let i = 0; i < 2; i++) await recovery.locator('section input[type="password"]').nth(i).fill(questions[i].answer)
            await recovery.getByRole('button', { name: '恢复加密分片', exact: true }).click()
          } else {
            await recovery.getByRole('button', { name: '使用用户分片和 Guardian', exact: true }).click()
            await recovery.getByPlaceholder('可选：粘贴 sat20share1:...').fill(savedShare)
            await recovery.getByRole('button', { name: '使用用户分片', exact: true }).click()
          }
          await recovery.getByRole('button', { name: '生成 Guardian 恢复请求', exact: true }).click()
          const recoveryRequest = await recovery.locator('textarea[readonly]').first().inputValue()
          const previousResponse = await guardian.locator('textarea[readonly]').last().inputValue()
          await guardian.getByPlaceholder('好友发送的 Guardian 恢复请求 JSON').fill(recoveryRequest)
          await guardian.getByRole('button', { name: '生成加密恢复响应', exact: true }).click()
          await confirmPassword(guardian)
          await expect.poll(() => guardian.locator('textarea[readonly]').last().inputValue()).not.toBe(previousResponse)
          await recovery.getByPlaceholder('粘贴 Guardian 返回的加密响应').fill(await guardian.locator('textarea[readonly]').last().inputValue())
          await recovery.getByRole('button', { name: '使用 Guardian 响应', exact: true }).click()
          await recovery.getByRole('button', { name: '预览恢复内容', exact: true }).click()
          await recovery.getByPlaceholder('设置新的本地钱包密码').fill(newPassword)
          await recovery.getByPlaceholder('再次输入密码').fill(newPassword)
          await Promise.all([recovery.waitForEvent('domcontentloaded'), recovery.getByRole('button', { name: '恢复全部钱包', exact: true }).click()])
          await ready(recovery); await unlock(recovery, newPassword)
          const normalize = wallets => wallets.map(({ fingerprint, name, accounts }) => ({ fingerprint, name, accounts }))
          assert.deepEqual(normalize(await catalog(recovery)), normalize(expectedCatalog), combination)
          await assertNoSecrets(recovery, [...questions.map(q => q.answer), savedShare, newPassword])
        } finally { await recovery.context().close() }
      }
    } finally { await guardian.context().close() }
  }))

  await scenario('usage: committed recovery with lost response survives reload without duplicate wallets', async () => {
    const page = await device()
    try {
      const session = await accountCall(page, 'loadRecovery', t.material.locator)
      await accountCall(page, 'recoverKnowledge', session.session_id, answers)
      await accountCall(page, 'setUserShare', session.session_id, t.material.user_share)
      await accountCall(page, 'previewRecovery', session.session_id)
      await page.evaluate(() => {
        const original = window.accountE2E.commitRecovery.bind(window.accountE2E)
        window.accountE2E.commitRecovery = async (...args) => {
          await original(...args)
          throw new Error('usage simulated loss of committed recovery response')
        }
      })
      await assert.rejects(accountCall(page, 'commitRecovery', session.session_id, password), /usage simulated loss/)
      const committed = await catalog(page)
      assert.ok(committed.length > 0)
      await page.reload(); await ready(page); await unlock(page)
      assert.deepEqual(await catalog(page), committed)
      const store = await page.evaluate(() => {
        const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
        return { count: wallet.wallets.length, root: wallet.rootAccountId, index: wallet.accountIndex }
      })
      assert.equal(store.count, committed.length)
      assert.equal(store.root, (await accountCall(page, 'status')).account_id)
      assert.equal(store.index, 0)
      await assert.rejects(accountCall(page, 'commitRecovery', session.session_id, password))
      await assertNoSecrets(page, [...questions.map(q => q.answer), t.material.user_share, password])
    } finally { await page.context().close() }
  })

  await scenario('usage: recovery commits once and reloads SDK data without redundant UI persistence', async () => {
    const page = await device()
    let commits = 0
    let uiWrites = 0
    await page.exposeBinding('__usageRecoveryTrace', (_, kind) => {
      if (kind === 'commit') commits++
      if (kind === 'ui-write') uiWrites++
    })
    try {
      await page.evaluate(() => { location.hash = '#/restore-account' })
      await page.getByPlaceholder('粘贴 sat20account1:...').fill(t.material.locator)
      await page.getByRole('button', { name: '加载加密账户备份', exact: true }).click()
      for (let i = 0; i < 2; i++) await page.locator('section input[type="password"]').nth(i).fill(questions[i].answer)
      await page.getByRole('button', { name: '恢复加密分片', exact: true }).click()
      await page.getByPlaceholder('可选：粘贴 sat20share1:...').fill(t.material.user_share)
      await page.getByRole('button', { name: '使用用户分片', exact: true }).click()
      await page.getByRole('button', { name: '预览恢复内容', exact: true }).click()
      await page.getByPlaceholder('设置新的本地钱包密码').fill(newPassword)
      await page.getByPlaceholder('再次输入密码').fill(newPassword)
      await page.evaluate(() => {
        const commit = window.accountE2E.commitRecovery.bind(window.accountE2E)
        window.accountE2E.commitRecovery = async (...args) => {
          await window.__usageRecoveryTrace('commit')
          return commit(...args)
        }
        const put = IDBObjectStore.prototype.put
        IDBObjectStore.prototype.put = function(value, key) {
          if (this.name === 'wallet-state' && String(key).endsWith('wallet_state_snapshot_v1')) {
            void window.__usageRecoveryTrace('ui-write')
            IDBObjectStore.prototype.put = put
            throw new DOMException('usage injected restored UI snapshot failure', 'QuotaExceededError')
          }
          return put.call(this, value, key)
        }
      })
      await page.getByRole('button', { name: '恢复全部钱包', exact: true }).click()
      await expect.poll(() => new URL(page.url()).hash, { timeout: 90000 }).toBe('#/unlock')
      await expect(page.getByRole('button', { name: '恢复全部钱包', exact: true })).toHaveCount(0)
      await ready(page); await unlock(page, newPassword)
      const committed = await catalog(page)
      assert.ok(committed.length > 0)
      await page.reload(); await ready(page); await unlock(page, newPassword)
      assert.deepEqual(await catalog(page), committed)
      assert.equal(commits, 1, 'recovery was submitted again after the SDK commit')
      assert.equal(uiWrites, 0, 'recovery still writes a duplicate PWA wallet snapshot')
      await assertNoSecrets(page, [...questions.map(q => q.answer), t.material.user_share, newPassword])
    } finally { await page.context().close() }
  })

  await scenario('usage: own Guardian contact from an earlier page is rejected by UI and high-level WASM', () => using(async page => {
    const own = JSON.parse((await accountCall(page, 'guardianIdentity', password)).contact)
    await settings(page)
    await page.evaluate(() => { location.hash = '#/wallet/setting' })
    await preflightUI(page); await temporaryUI(page)
    await fillQuestions(page)
    await page.getByPlaceholder('粘贴好友钱包生成的 Guardian contact JSON').fill(JSON.stringify(own))
    await expect(page.getByText('不能使用自己的 Guardian 联系信息', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: '创建并保存加密账户备份', exact: true })).toBeDisabled()
    await assert.rejects(accountCall(page, 'createRecovery', { password, wallets: [], recovery_mode: '2of3', questions, guardian: own }), /independent account/)
    await assert.rejects(accountCall(page, 'createRecovery', { password, wallets: [], recovery_mode: '2of3', questions,
      guardian: { ...own, network: 'mainnet' } }), /network does not match/)
    assert.equal((await accountCall(page, 'status')).recovery_configured, false)
    await accountCall(page, 'cancelPendingStorageAuthorization')
  }))

  await scenario('usage: Guardian paid confirmation uses 100 records and root identity after a 200 record child-wallet quote', () => using(async page => {
    let broadcasts = 0
    await page.context().route('**/*', route => {
      if (/broadcast|sendrawtransaction|\/btc\/txs?(?:\?|$)/i.test(route.request().url())) { broadcasts++; return route.abort('blockedbyclient') }
      return route.fallback()
    })
    await page.evaluate(async credential => {
      const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(credential)
      if (error) throw error
    }, password)
    const status = await accountCall(page, 'status')
    await preflightUI(page)
    const paid = (await accountCall(page, 'getStorageOptions')).options.find(option => option.id === 'paid')
    await page.getByRole('button', { name: paid.title, exact: false }).click()
    await page.locator('#autopay-record-count').fill('200')
    await page.getByRole('button', { name: '确认存储方式', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('200', { exact: true })).toBeVisible()
    await expect(dialog).toContainText(status.account_id)
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    await page.getByPlaceholder('好友发送的 Guardian setup JSON').fill(t.material.guardian_setup)
    await page.locator('select').selectOption('paid')
    await page.getByRole('button', { name: '接受并保存好友分片', exact: true }).click()
    await dialog.locator('input[type="password"]').fill(password)
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await expect(dialog.getByText('100', { exact: true })).toBeVisible()
    await expect(dialog.getByText('200', { exact: true })).toHaveCount(0)
    const rate = Math.max(100 * Number(paid.full_record_fee_per_block), Number(paid.minimum_amount_per_block || 0))
    await expect(dialog).toContainText(`${rate.toLocaleString(undefined, { maximumFractionDigits: 8 })} ${paid.fee_asset}`)
    await expect(dialog).toContainText(status.account_id)
    await expect(dialog).toContainText(paid.contract_address)
    await expect(dialog).toContainText('testnet')
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    assert.equal(broadcasts, 0, 'confirmation cancellation broadcast a funding transaction')
  }))

  await scenario('usage: paid setup confirmation rejects invalid count and cancellation never pays', () => using(async page => {
    await preflightUI(page)
    const options = (await accountCall(page, 'getStorageOptions')).options
    const paid = options.find(option => option.id === 'paid')
    assert.ok(paid?.available, 'real fixture must expose the paid option')
    await page.getByRole('button', { name: paid.title, exact: false }).click()
    await page.evaluate(() => {
      const confirm = window.accountE2E.confirmStorage.bind(window.accountE2E)
      window.recordCountPaidRequests = 0
      window.accountE2E.confirmStorage = (...args) => { window.recordCountPaidRequests++; return confirm(...args) }
    })
    for (const value of ['99', '100.5', '9007199254740992']) {
      await page.locator('#autopay-record-count').fill(value)
      await expect(page.getByRole('button', { name: '确认存储方式', exact: true })).toBeDisabled()
      await expect(page.getByRole('dialog')).toHaveCount(0)
      assert.equal(await page.evaluate(() => window.recordCountPaidRequests), 0, 'invalid count dispatched paid storage')
    }
    await page.locator('#autopay-record-count').fill('100')
    await page.getByRole('button', { name: '确认存储方式', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText(paid.contract_address)
    await expect(dialog).toContainText(paid.fee_asset)
    await expect(dialog).toContainText('testnet')
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    assert.equal(await page.evaluate(() => window.recordCountPaidRequests), 0, 'cancellation dispatched paid storage')
    assert.equal(await accountCall(page, 'resumePendingStorageAuthorization'), null)
    assert.equal((await accountCall(page, 'status')).recovery_configured, false)
  }))

  await scenario('usage: leaving a loaded recovery page aborts session and clears temporary material', async () => {
    const page = await device()
    try {
      await page.evaluate(() => { location.hash = '#/restore-account' })
      await page.getByPlaceholder('粘贴 sat20account1:...').fill(t.material.locator)
      await page.evaluate(() => {
        const original = window.accountE2E.loadRecovery.bind(window.accountE2E)
        window.accountE2E.loadRecovery = async locator => {
          const result = await original(locator)
          window.usageLoadedSession = result.session_id
          return result
        }
      })
      await page.getByRole('button', { name: '加载加密账户备份', exact: true }).click()
      for (let i = 0; i < 2; i++) await page.locator('section input[type="password"]').nth(i).fill(questions[i].answer)
      await page.getByRole('button', { name: '恢复加密分片', exact: true }).click()
      await page.getByPlaceholder('可选：粘贴 sat20share1:...').fill(t.material.user_share)
      await page.getByRole('button', { name: '使用用户分片', exact: true }).click()
      await page.getByRole('button', { name: '预览恢复内容', exact: true }).click()
      const session = await page.evaluate(() => window.usageLoadedSession)
      await page.evaluate(() => { location.hash = '#/' })
      await page.getByRole('heading', { name: '确认恢复账户', exact: true }).waitFor({ state: 'hidden' })
      await assert.rejects(accountCall(page, 'commitRecovery', session, password))
      assert.deepEqual(await catalog(page), [])
      await assertNoSecrets(page, [...questions.map(q => q.answer), t.material.user_share, password])
      await page.reload(); await ready(page)
      await assert.rejects(accountCall(page, 'previewRecovery', session))
    } finally { await page.context().close() }
  })
  assert.deepEqual(failures, [], 'PWA usage regressions failed; see individual verdicts')
}

export async function runRgbPwaCases(t, fixture) {
  const { check, device, ready, unlock, catalog, walletCall, accountCall } = t
  const failures = []
  const navigateRecovery = async page => {
    await page.evaluate(() => { location.hash = '#/restore-account' })
    await page.getByPlaceholder('粘贴 sat20account1:...').fill(fixture.locator)
    await page.getByRole('button', { name: '加载加密账户备份', exact: true }).click()
    for (let i = 0; i < fixture.answers.length; i++) await page.locator('section input[type="password"]').nth(i).fill(fixture.answers[i].answer)
    await page.getByRole('button', { name: '恢复加密分片', exact: true }).click()
    await page.getByPlaceholder('可选：粘贴 sat20share1:...').fill(fixture.user_share)
    await page.getByRole('button', { name: '使用用户分片', exact: true }).click()
    await page.getByRole('button', { name: '预览恢复内容', exact: true }).click()
    await page.getByPlaceholder('设置新的本地钱包密码').fill(fixture.password)
    await page.getByPlaceholder('再次输入密码').fill(fixture.password)
  }
  const unlockRecovered = async (page, phase = 'recovery unlock') => {
    // A healthy R+1 remote apply can overlap the fresh page's first unlock.
    // Exercise the real public retry; never open a session while import remains.
    let lastError
    try { await expect.poll(async () => {
      const result = await page.evaluate(async credential => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        const [error] = await store.unlockWallet(credential)
        return { error: error?.message, locked: store.locked }
      }, fixture.password)
      lastError = result.error
      if (!result.error) return true
      assert.match(result.error, /account-managed data import is incomplete/)
      assert.equal(result.locked, true, 'incomplete background import opened PWA session')
      return false
    }, { timeout: 90000, message: phase }).toBe(true) } catch (error) {
      const status = await accountCall(page, 'status').catch(() => ({}))
      const marker = await page.evaluate(async key => {
        const db = await new Promise((resolve, reject) => {
          const request = indexedDB.open('sat20-wallet-sdk', 1)
          request.onsuccess = () => resolve(request.result)
          request.onerror = () => reject(request.error)
        })
        try {
          const value = await new Promise((resolve, reject) => {
            const tx = db.transaction('kv', 'readonly')
            const request = tx.objectStore('kv').get(key)
            tx.oncomplete = () => resolve(request.result)
            tx.onabort = () => reject(tx.error)
          })
          if (!value) return null
          const parsed = JSON.parse(atob(value))
          return { origin: parsed.origin, stage: parsed.stage, state_revision: parsed.target_state_revision,
            data_revision: parsed.target_data_revision }
        } finally { db.close() }
      }, fixture.pending_channel.import_marker_key).catch(() => ({ unreadable: true }))
      let diagnostic = JSON.stringify({ phase, last_unlock_error: lastError, marker,
        last_sync_error: status.last_dkvs_sync_error, state_seq: status.state_seq,
        data_revision: status.managed_data_revision, data_dirty: status.managed_data_dirty })
      for (const secret of [fixture.password, fixture.root_mnemonic, fixture.user_share,
        ...fixture.answers.map(answer => answer.answer)]) diagnostic = diagnostic.replaceAll(secret, '[redacted]')
      throw new Error(`${phase}: ${error.message}; diagnostic=${diagnostic}`)
    }
  }
  const commit = async page => {
    await Promise.all([
      page.waitForEvent('domcontentloaded', { timeout: 90000 }),
      page.getByRole('button', { name: '恢复全部钱包', exact: true }).click(),
    ])
    await ready(page); await unlockRecovered(page)
  }
  const verify = async (page, expectedScopes = fixture.scopes, allowLocalHistory = false) => {
    const wallets = await catalog(page)
    assert.equal(wallets.length, fixture.wallet_count)
    const observed = []
    for (const expected of expectedScopes) {
      const wallet = wallets.find(item => item.fingerprint === expected.fingerprint)
      assert.ok(wallet)
      await page.evaluate(async ({ id, index }) => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        await store.switchWallet(id)
        await store.switchToAccount(index)
      }, { id: String(wallet.id), index: expected.index })
      const state = JSON.parse((await walletCall(page, 'getRGB11State')).state)
      const stable = value => Object.fromEntries(['assets', 'proofs'].map(key => [key,
        [...(value[key] ?? [])].sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)))]))
      const actual = stable(state)
      const reference = stable(expected.state)
      if (allowLocalHistory) {
        // Same-device storage includes local spending history that a fresh
        // backup restore intentionally does not import. Every backed-up asset
        // and proof must still match; retain the complete local baseline for
        // the strict before/after comparison below.
        for (const key of ['assets', 'proofs']) {
          for (const record of reference[key]) {
            assert.ok(actual[key].some(item => isDeepStrictEqual(item, record)),
              `local device lost backed-up RGB ${key}`)
          }
        }
      } else {
        assert.deepEqual(actual, reference, 'browser RGB assets/proofs differ from expected ownership')
      }
      const address = wallet.accounts.find(account => account.index === expected.index).address
      const locks = await walletCall(page, 'getAllLockedUtxo', address)
      for (const [point, reason] of Object.entries(expected.locks)) {
        assert.ok(locks[point], `RGB allocation lost its spending lock: ${point}`)
        assert.equal(JSON.parse(locks[point]).reason, reason)
      }
      observed.push({ fingerprint: expected.fingerprint, index: expected.index, state: actual,
        locks: Object.fromEntries(Object.entries(locks).map(([point, value]) => [point, JSON.parse(value).reason])) })
    }
    return observed
  }
  const bitcoinRoute = async route => {
    const path = new URL(route.request().url()).pathname
    const body = route.request().postDataJSON() ?? {}
    const facts = fixture.bitcoin_evidence
    let data
    if (path.endsWith('/tip')) data = facts.tip
    else if (path.endsWith('/utxos/status')) data = body.outpoints.map(point => facts.utxos[point] ?? { outpoint: point, exists: false, unspent: false })
    else if (path.endsWith('/outspends/batch')) data = body.outpoints.map(point => facts.outspends[point] ?? { outpoint: point, exists: false, spent: false })
    else if (path.endsWith('/rawtx/batch')) data = body.txids.map(id => facts.raw[id] ?? { txid: id, error: 'controlled transaction not found' })
    else if (path.endsWith('/tx/status/batch')) data = body.txids.map(id => facts.status[id] ?? { txid: id, exists: false, in_mempool: false, confirmed: false })
    else return route.fallback()
    return route.fulfill({ contentType: 'application/json', json: { code: 0, msg: 'ok', data } })
  }
  for (const mode of ['success', 'recovery-retry', 'mnemonic-retry']) {
    const fault = mode !== 'success'
    const mnemonic = mode === 'mnemonic-retry'
    const name = mnemonic ? 'RGB PWA: mnemonic page retries interrupted ownership import after reload'
      : fault ? 'RGB PWA: real provider storage failure survives reload and same-database recovery retry'
      : 'RGB PWA: page restores nonempty issued multiwallet ownership proofs and locks'
    const page = await device()
    let broadcasts = 0
    await page.context().route('**/*', route => {
      if (/broadcast|sendrawtransaction|\/btc\/txs?(?:\?|$)/i.test(route.request().url())) { broadcasts++; return route.abort('blockedbyclient') }
      return route.fallback()
    })
    await page.context().route('**/v3/bitcoin/**', bitcoinRoute)
    try {
      await check(name, async () => {
        const fillMnemonic = async () => {
          await page.evaluate(() => { location.hash = '#/import' })
          await page.locator('form textarea').fill(fixture.root_mnemonic)
          await page.locator('form input[type="password"]').nth(0).fill(fixture.password)
          await page.locator('form input[type="password"]').nth(1).fill(fixture.password)
        }
        if (mnemonic) await fillMnemonic()
        else await navigateRecovery(page)
        if (fault) {
          await page.evaluate(mnemonic => {
            const put = IDBObjectStore.prototype.put
            window.usageRgbFailure = false
            window.usageRgbCommitOutcome = null
            const owner = mnemonic ? window.__SAT20_PWA_VERIFY__.useWalletStore() : window.accountE2E
            const method = mnemonic ? 'importWallet' : 'commitRecovery'
            const commitRecovery = owner[method].bind(owner)
            owner[method] = async (...args) => {
              try {
                const result = await commitRecovery(...args)
                window.usageRgbCommitOutcome = mnemonic && result[0]
                  ? { failed: true, message: result[0].message } : { failed: false }
                return result
              } catch (error) {
                window.usageRgbCommitOutcome = { failed: true, message: error.message }
                throw error
              }
            }
            IDBObjectStore.prototype.put = function(value, key) {
              if (!window.usageRgbFailure && this.name === 'kv' && String(key).startsWith('rgb11-') && String(key).includes('-proof-')) {
                window.usageRgbFailure = true
                throw new DOMException('usage injected real RGB proof persistence failure', 'QuotaExceededError')
              }
              return put.call(this, value, key)
            }
            window.restoreUsageRgbFault = () => { IDBObjectStore.prototype.put = put }
          }, mnemonic)
          if (mnemonic) await page.locator('form button[type="submit"]').click()
          else await page.getByRole('button', { name: '恢复全部钱包', exact: true }).click()
          await expect.poll(() => page.evaluate(() => Boolean(window.usageRgbCommitOutcome)), { timeout: 90000 }).toBe(true)
          const faultResult = await page.evaluate(() => ({ injected: window.usageRgbFailure, result: window.usageRgbCommitOutcome }))
          assert.equal(faultResult.injected, true, 'real recovered ownership proof write was not faulted')
          assert.equal(faultResult.result.failed, true, 'RGB provider storage failure was swallowed')
          assert.match(faultResult.result.message, /usage injected real RGB proof persistence failure/)
          if (!mnemonic) await page.getByRole('alert').filter({ hasText: 'usage injected real RGB proof persistence failure' }).waitFor()
          await page.evaluate(() => window.restoreUsageRgbFault())
          const wallets = await catalog(page)
          assert.equal(wallets.length, fixture.wallet_count, 'must exercise failure after the account transaction')
          // Public facade remains locked during recovery; the trusted host SDK
          // boundary must independently reject destructive catalog changes.
          const rejected = await page.evaluate(async id => {
            const response = await window.sat20wallet_wasm.updateWalletName(id, 'Forbidden incomplete RGB import')
            return { code: response.code, message: response.msg }
          }, String(wallets[0].id))
          assert.notEqual(rejected.code, 0)
          assert.match(rejected.message, /account-managed data import is incomplete/)
          // Independent device A advances the real remote state while B is
          // interrupted at the original committed proof import target.
          if (!mnemonic) {
            const peer = await device()
            try {
              await peer.context().route('**/v3/bitcoin/**', bitcoinRoute)
              await peer.context().route('**/*', route => /broadcast|sendrawtransaction/i.test(route.request().url())
                ? route.abort('blockedbyclient') : route.fallback())
              await navigateRecovery(peer); await commit(peer)
              const root = (await catalog(peer))[0]
              await peer.evaluate(async id => {
                await window.__SAT20_PWA_VERIFY__.useWalletStore().updateWalletName(id, 'Independent device newer state')
              }, String(root.id))
              await expect.poll(async () => (await accountCall(peer, 'status')).pending_changes ?? 0,
                { timeout: 90000 }).toBe(0)
            } finally { await peer.context().close() }
          }
          await page.reload(); await ready(page)
          await page.evaluate(() => { location.hash = '#/unlock' })
          const unlockFailure = await page.evaluate(async credential => {
            const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
            const [error] = await store.unlockWallet(credential)
            return { message: error?.message, locked: store.locked }
          }, fixture.password)
          assert.match(unlockFailure.message, /account-managed data import is incomplete/)
          assert.equal(unlockFailure.locked, true, 'incomplete ownership must not open PWA session')
          await page.getByRole('button', { name: mnemonic ? '继续助记词恢复' : '继续账户恢复', exact: true }).click()
          if (mnemonic) await fillMnemonic()
          else await navigateRecovery(page)
        }
        if (mnemonic) {
          await page.locator('form button[type="submit"]').click()
          await expect.poll(() => page.evaluate(() => location.hash), { timeout: 90000 }).toBe('#/wallet')
          await unlockRecovered(page)
        } else await commit(page)
        if (mode === 'recovery-retry') {
          await expect.poll(async () => {
            await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().syncWalletCatalog())
            return (await catalog(page)).some(wallet => wallet.name === 'Independent device newer state')
          }, { timeout: 90000 }).toBe(true)
        }
        await verify(page)
        await page.reload(); await ready(page); await unlockRecovered(page)
        await verify(page)
        assert.equal(broadcasts, 0, 'restoration attempted a transaction broadcast')
      })
    } catch (error) { failures.push({ name, message: error.message }) }
    finally { await page.context().close() }
  }
  const channelPage = await device()
  let channelBroadcasts = 0
  await channelPage.context().route('**/v3/bitcoin/**', bitcoinRoute)
  await channelPage.context().route('**/*', route => {
    if (/broadcast|sendrawtransaction|\/btc\/txs?(?:\?|$)/i.test(route.request().url())) {
      channelBroadcasts++; return route.abort('blockedbyclient')
    }
    return route.fallback()
  })
  try {
    await check('RGB PWA: cold remote import retry keeps unfinished local channel visible', async () => {
      const readLocal = async key => {
        const db = await new Promise((resolve, reject) => {
          const request = indexedDB.open('sat20-wallet-sdk', 1)
          request.onsuccess = () => resolve(request.result)
          request.onerror = () => reject(request.error)
        })
        try {
          return await new Promise((resolve, reject) => {
            const tx = db.transaction('kv', 'readonly')
            const request = tx.objectStore('kv').get(key)
            tx.oncomplete = () => resolve(request.result ?? null)
            tx.onabort = () => reject(tx.error)
          })
        } finally { db.close() }
      }
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
      const localRGBBeforeFault = await verify(channelPage, fixture.scopes, true)
      await channelPage.evaluate(async id => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        await store.switchWallet(id)
        await store.switchToAccount(0)
      }, String(fixture.pending_channel.wallet_id))
      await channelPage.evaluate(() => sessionStorage.setItem('usage-channel-import-fault', 'armed'))
      const installFault = markerKey => {
        const put = IDBObjectStore.prototype.put
        IDBObjectStore.prototype.put = function(value, key) {
          // Do not assume a catalog-only update rewrites an ownership proof
          // or imports a provider when a newer local data generation exists.
          // Fault the first actual post-commit import stage write; the atomic
          // local-commit marker must still succeed.
          if (sessionStorage.getItem('usage-channel-import-fault') === 'armed' &&
              this.name === 'kv' && String(key) === markerKey &&
              JSON.parse(atob(value)).stage !== 'local-commit') {
            sessionStorage.setItem('usage-channel-import-fault-hit', 'yes')
            throw new DOMException('usage pending channel post-commit import stage write failure', 'QuotaExceededError')
          }
          return put.call(this, value, key)
        }
      }
      await channelPage.evaluate(installFault, fixture.pending_channel.import_marker_key)
      await channelPage.context().addInitScript(installFault, fixture.pending_channel.import_marker_key)
      const peer = await device()
      let peerRoot
      const caseFailures = []
      try {
        await peer.context().route('**/v3/bitcoin/**', bitcoinRoute)
        await navigateRecovery(peer); await commit(peer)
        peerRoot = (await catalog(peer))[0]
        await peer.evaluate(async id => {
          await window.__SAT20_PWA_VERIFY__.useWalletStore().updateWalletName(id, 'Pending channel recovery state')
        }, String(peerRoot.id))
        await expect.poll(async () => (await accountCall(peer, 'status')).pending_changes ?? 0,
          { timeout: 90000 }).toBe(0)
        await expect.poll(async () => {
          const value = await channelPage.evaluate(readLocal, fixture.pending_channel.import_marker_key)
          return value ? JSON.parse(Buffer.from(value, 'base64').toString()).origin : null
        }, { timeout: 90000 }).toBe('remote-apply')
        await expect.poll(() => channelPage.evaluate(() => sessionStorage.getItem('usage-channel-import-fault-hit')),
          { timeout: 90000 }).toBe('yes')
        await channelPage.reload(); await ready(channelPage)
        const rejected = await channelPage.evaluate(async password => {
          const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
          const [error] = await store.unlockWallet(password)
          return { error: error?.message, locked: store.locked }
        }, fixture.password)
        assert.match(rejected.error, /account-managed data import is incomplete/)
        assert.equal(rejected.locked, true)
        await channelPage.evaluate(() => sessionStorage.removeItem('usage-channel-import-fault'))
        await unlockRecovered(channelPage, 'same-device cold retry after removing injected fault')
        await expect.poll(() => channelPage.evaluate(readLocal, fixture.pending_channel.import_marker_key),
          { timeout: 90000 }).toBe(null)
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
        for (const [key, value] of Object.entries(fixture.pending_channel.records)) {
          assert.equal(await channelPage.evaluate(readLocal, key), value, 'authentication rewrote unfinished channel work')
        }
        const localRGBAfterRetry = await verify(channelPage, localRGBBeforeFault)
        assert.deepEqual(localRGBAfterRetry, localRGBBeforeFault,
          'remote import retry changed same-device RGB assets, proofs or spending locks')
        assert.equal(channelBroadcasts, 0, 'channel recovery attempted a new transaction broadcast')
      } catch (error) {
        caseFailures.push(error)
      } finally {
        try {
          // Restore only this peer's metadata change, including when the cold
          // retry stays red. Keep later independent recovery cases synchronized.
          if (peerRoot) {
            await peer.evaluate(async ({ id, name }) => {
              await window.__SAT20_PWA_VERIFY__.useWalletStore().updateWalletName(id, name)
            }, { id: String(peerRoot.id), name: peerRoot.name })
            await expect.poll(async () => (await accountCall(peer, 'status')).pending_changes ?? 0,
              { timeout: 90000 }).toBe(0)
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
  const snapshotPage = await device()
  let rgbCommits = 0
  let rgbUiWrites = 0
  await snapshotPage.exposeBinding('__rgbRecoveryTrace', (_, kind) => {
    if (kind === 'commit') rgbCommits++
    if (kind === 'ui-write') rgbUiWrites++
  })
  try {
    await snapshotPage.context().route('**/v3/bitcoin/**', bitcoinRoute)
    await check('RGB PWA: single SDK recovery preserves ownership proofs and locks without redundant UI persistence', async () => {
      await navigateRecovery(snapshotPage)
      const replacementPassword = fixture.password + '-ui-snapshot'
      await snapshotPage.getByPlaceholder('设置新的本地钱包密码').fill(replacementPassword)
      await snapshotPage.getByPlaceholder('再次输入密码').fill(replacementPassword)
      await snapshotPage.evaluate(() => {
        const put = IDBObjectStore.prototype.put
        const commit = window.accountE2E.commitRecovery.bind(window.accountE2E)
        window.accountE2E.commitRecovery = async (...args) => {
          await window.__rgbRecoveryTrace('commit')
          return commit(...args)
        }
        IDBObjectStore.prototype.put = function(value, key) {
          if (this.name === 'wallet-state' && String(key).endsWith('wallet_state_snapshot_v1')) {
            void window.__rgbRecoveryTrace('ui-write')
            IDBObjectStore.prototype.put = put
            throw new DOMException('RGB recovered UI snapshot failure', 'QuotaExceededError')
          }
          return put.call(this, value, key)
        }
      })
      await snapshotPage.getByRole('button', { name: '恢复全部钱包', exact: true }).click()
      await expect.poll(() => new URL(snapshotPage.url()).hash, { timeout: 90000 }).toBe('#/unlock')
      await expect(snapshotPage.getByRole('button', { name: '恢复全部钱包', exact: true })).toHaveCount(0)
      // The SDK owns the restored catalog and public identity.
      await ready(snapshotPage)
      await expect.poll(() => new URL(snapshotPage.url()).hash).toBe('#/unlock')
      await snapshotPage.locator('input[type="password"]').fill(replacementPassword)
      await snapshotPage.locator('form button[type="submit"]').click()
      await expect.poll(() => snapshotPage.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), { timeout: 90000 }).toBe(false)
      await verify(snapshotPage)
      assert.equal(rgbCommits, 1, 'RGB recovery was submitted again after the SDK commit')
      assert.equal(rgbUiWrites, 0, 'RGB recovery writes a duplicate PWA wallet snapshot')
    })
  } catch (error) { failures.push({ name: 'RGB recovered UI snapshot', message: error.message }) }
  finally { await snapshotPage.context().close() }

  try { await runRgbPaidCases(t, fixture) }
  catch (error) { failures.push({ name: 'paid settings', message: error.message }) }
  assert.deepEqual(failures, [], 'RGB browser recovery regressions failed; see individual verdicts')
  console.log(JSON.stringify({ passed: true, cases: 9, backend: 'real RGB issuance and paid DKVS', browser: 'real PWA and WASM' }))
}

export async function runRgbPaidCases(t, fixture) {
  const { check, device, ready, unlock, catalog, walletCall, accountCall } = t
  const paidPage = await device()
  let paidBroadcasts = 0
  const rejectReuseBroadcast = route => {
    if (/broadcast|sendrawtransaction|\/btc\/txs?(?:\?|$)/i.test(route.request().url())) { paidBroadcasts++; return route.abort('blockedbyclient') }
    return route.fallback()
  }
  // These ledger steps share a payer/delegate and form one ordered journey.
  // A failure invalidates the remaining steps; never recover by paying again
  // or deleting pending receipts merely to prepare the next scenario.
  try {
    await paidPage.context().route('**/*', rejectReuseBroadcast)
    await check('RGB PWA: funded root reuses real AUTOPAY through settings without another funding', async () => {
      assert.ok(fixture.maintenance_mnemonic, 'configured temporary maintenance payer is required')
      // Use the PWA import flow, including root discovery/initialization.
      // Raw SDK import bypasses the PWA's root discovery.
      await paidPage.evaluate(async ({ mnemonic, password }) => {
        const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().importWallet(mnemonic, password)
        if (error) throw error
      }, { mnemonic: fixture.maintenance_mnemonic, password: fixture.password })
      await paidPage.reload(); await ready(paidPage); await unlock(paidPage, fixture.password)
      const root = (await catalog(paidPage))[0]
      await walletCall(paidPage, 'ensureAccount', String(root.id), 1, 'Upgrade savings', 'did:upgrade:1')
      const ids = ['book-page', 'private-phrase', 'private-note']
      const questions = ids.map((id, index) => ({ id, prompt: `Private upgrade question ${index + 1}`,
        answer: fixture.answers[index % fixture.answers.length].answer,
        confirmation: fixture.answers[index % fixture.answers.length].answer }))
      // Begin with an actually configured temporary account, rather than
      // setting a Vue step or manufacturing a configured status response.
      await expect.poll(async () => {
        const status = await accountCall(paidPage, 'status')
        return !status.pending_changes && !status.managed_data_dirty
      }, { timeout: 90000 }).toBe(true)
      await accountCall(paidPage, 'confirmStorage', 'temporary')
      const temporary = await accountCall(paidPage, 'createRecovery', { password: fixture.password,
        wallets: [], recovery_mode: '2of2', questions })
      await accountCall(paidPage, 'rehearse', temporary.session_id,
        questions.slice(0, 2).map(({ id, answer }) => ({ question_id: id, answer })), temporary.user_share, fixture.password)
      const configured = await accountCall(paidPage, 'status')
      assert.equal(configured.active, true)
      assert.equal(configured.recovery_configured, true)
      assert.equal(configured.storage_mode, 'temporary')
      const beforeCatalog = await catalog(paidPage)
      await paidPage.reload(); await ready(paidPage); await unlock(paidPage, fixture.password)
      await paidPage.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
      await paidPage.getByRole('heading', { name: '维护恢复配置', exact: true }).waitFor()
      await expect(paidPage.getByRole('textbox', { name: '当前公开恢复码', exact: true })).toHaveValue(temporary.locator)
      await paidPage.getByRole('button', { name: '重新配置恢复或升级存储', exact: true }).click()
      await paidPage.getByRole('button', { name: '检查账户', exact: true }).click()
      const dialog = paidPage.getByRole('dialog')
      await dialog.locator('input[type="password"]').fill(fixture.password)
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await dialog.waitFor({ state: 'hidden' })
      await paidPage.getByRole('button', { name: '2/2 增强安全', exact: true }).click()
      const paid = (await accountCall(paidPage, 'getStorageOptions')).options.find(option => option.id === 'paid')
      await paidPage.getByRole('button', { name: paid.title, exact: false }).click()
      await paidPage.getByRole('button', { name: '仅复用现有 AUTOPAY 授权（不充值）', exact: true }).click()
      await paidPage.getByRole('button', { name: '创建并保存加密账户备份', exact: true }).waitFor()
      const authorization = await accountCall(paidPage, 'resumePendingStorageAuthorization')
      assert.equal(authorization.mode, 'paid')
      assert.equal(Boolean(authorization.transaction_id), false)
      await paidPage.getByRole('button', { name: '取消本次设置', exact: true }).click()
      assert.equal(await accountCall(paidPage, 'resumePendingStorageAuthorization'), null)
      assert.equal((await accountCall(paidPage, 'status')).public_locator, temporary.locator)
      await paidPage.getByRole('button', { name: '仅复用现有 AUTOPAY 授权（不充值）', exact: true }).click()
      for (let i = 0; i < 3; i++) {
        await paidPage.getByPlaceholder('答案', { exact: true }).nth(i).fill(questions[i].answer)
        await paidPage.getByPlaceholder('再次输入答案', { exact: true }).nth(i).fill(questions[i].confirmation)
      }
      await expect.poll(async () => {
        const status = await accountCall(paidPage, 'status')
        return !status.pending_changes && !status.managed_data_dirty
      }, { timeout: 90000 }).toBe(true)
      await paidPage.getByRole('button', { name: '创建并保存加密账户备份', exact: true }).click()
      await dialog.locator('input[type="password"]').fill(fixture.password)
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await dialog.waitFor({ state: 'hidden' })
      const finalLocator = await paidPage.getByRole('textbox', { name: '最终公开恢复码', exact: true }).inputValue()
      const userShare = await paidPage.getByRole('textbox', { name: '秘密用户分片', exact: true }).inputValue()
      await paidPage.getByRole('button', { name: '进入恢复演练', exact: true }).click()
      for (let i = 0; i < 2; i++) await paidPage.getByPlaceholder(`问题 ${i + 1} 的答案`, { exact: true }).fill(questions[i].answer)
      await paidPage.getByPlaceholder('重新粘贴用户分片', { exact: true }).fill(userShare)
      await paidPage.getByRole('button', { name: '执行恢复演练', exact: true }).click()
      await dialog.locator('input[type="password"]').fill(fixture.password)
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await dialog.waitFor({ state: 'hidden' })
      await paidPage.getByRole('heading', { name: '账户恢复已配置', exact: true }).waitFor()
      assert.equal((await accountCall(paidPage, 'status')).storage_mode, 'paid')
      await paidPage.reload(); await ready(paidPage); await unlock(paidPage, fixture.password)
      await paidPage.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
      await expect(paidPage.getByRole('textbox', { name: '当前公开恢复码', exact: true })).toHaveValue(finalLocator)
      assert.equal((await accountCall(paidPage, 'status')).storage_mode, 'paid')
      assert.deepEqual(await catalog(paidPage), beforeCatalog, 'paid upgrade changed wallet or DID metadata')
      assert.equal(paidBroadcasts, 0, 'AUTOPAY reuse attempted another funding transaction')
    })

    await check('RGB PWA: same paid mode reconfiguration converges across cold devices and root recovery', async () => {
      const ids = ['book-page','private-phrase','private-note']
      const questions = ids.map((id,index) => ({ id,prompt:`Private current question ${index+1}`,
        answer:fixture.answers[index%fixture.answers.length].answer,
        confirmation:fixture.answers[index%fixture.answers.length].answer }))
      await sameModeReconfiguration(t,paidPage,fixture.password,'paid',questions,fixture.maintenance_mnemonic)
      assert.equal(paidBroadcasts,0,'same-mode reconfiguration unexpectedly funded AUTOPAY')
    })
    await paidPage.context().unroute('**/*', rejectReuseBroadcast)
    await check('RGB PWA: AutopayFundingConfirmationMatchesEffectiveRate', async () => {
      // Cold reload/unlock in the preceding maintenance case leaves the
      // router on /unlock. Re-enter the visible management page before using
      // its controls; SDK readiness alone does not establish a UI route.
      await paidPage.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
      await paidPage.getByRole('heading', { name: '维护恢复配置', exact: true }).waitFor()
      const initial = await accountCall(paidPage, 'autopayStatus')
      const publicDiagnostic = data => appendFileSync(
        join(tmpdir(), `sat20wallet-pwa-e2e-funding-diagnostics-${process.pid}.jsonl`),
        JSON.stringify({ at: new Date().toISOString(), ...data }) + '\n', { mode: 0o600 })
      assert.ok(initial.required && initial.ready)
      const required = Number(initial.required_amount_per_block)
      assert.ok(Number.isFinite(required) && required > 0)
      const evidence = []
      let captureFunding = false
      let broadcasts = 0
      const routeFunding = async route => {
        if (route.request().method() !== 'POST' || !/\/btc\/tx$/.test(new URL(route.request().url()).pathname)) return route.fallback()
        const body = route.request().postDataJSON()
        broadcasts++
        const response = await route.fetch()
        const result = await response.json()
        if (captureFunding) {
          assert.equal(result.code, 0, 'real funding submission failed')
          evidence.push({ raw_tx: body.signedTxHex, transaction_id: result.data })
        }
        await route.fulfill({ response })
      }
      // Every state transition is a real contract call on the isolated node.
      // Cancel returns the delegate's remaining fee balance; no fixture minting
      // or fabricated contract/status/transaction responses are used.
      const configure = async (action, rate) => {
        const result = await walletCall(paidPage, 'invokeUnifiedContract', {
          ContractType: 'template', SubType: 'autopay.tc', ContractAddress: initial.contract_address,
          Action: action, Param: JSON.stringify(action === 'config' ? { amountPerBlock: String(rate), blobKeyLimit: 1 } : {}),
          ParamEncoding: 'json', Assets: [],
        })
        assert.ok(result.txid)
        const selection = await walletCall(paidPage, 'getWalletCatalog')
        assert.equal(selection.current_account_index, 0, 'fixture contract action requires the root subaccount')
        let observed
        publicDiagnostic({ action, requested_rate: rate, txid: result.txid, selection: { wallet: selection.current_wallet_id, account: selection.current_account_index } })
        try {
          await expect.poll(async () => {
            observed = await accountCall(paidPage, 'autopayStatus')
            return action === 'cancel' ? Number(observed.balance || 0) === 0 : observed.amount_per_block === String(rate)
          }, { timeout: 90000, message: `real ${action} rate=${rate ?? ''} transaction=${result.txid}` }).toBe(true)
        } catch (error) {
          publicDiagnostic({ action, requested_rate: rate, observed_rate: observed?.amount_per_block, balance: observed?.balance, reason: observed?.reason, current_block: observed?.current_block })
          throw new Error(`real ${action} did not settle: ${error.message}; status ${JSON.stringify({
            payer: observed?.payer, rate: observed?.amount_per_block, required_rate: observed?.required_amount_per_block,
            balance: observed?.balance, current_block: observed?.current_block, reason: observed?.reason,
            funding_pending: observed?.funding_pending, funding_error: observed?.funding_error, selection: { wallet: selection.current_wallet_id, account: selection.current_account_index } })}`, { cause: error })
        }
      }
      const openFunding = async () => {
        // The first reused delegate has no funding receipt, so its tracker
        // query button does not exist. Exercise the normal visible-page read.
        await paidPage.bringToFront()
        await paidPage.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
        await paidPage.getByRole('button', { name: '充值 AUTOPAY', exact: true }).click()
        const dialog = paidPage.getByRole('dialog')
        await expect(dialog).toBeVisible()
        return dialog
      }
      await paidPage.context().route('**/*', routeFunding)
      try {
        for (const [index, rate] of [required / 2, required, required * 2].entries()) {
          if (index > 0) await configure('cancel')
          await configure('config', rate)
          let quote = await accountCall(paidPage, 'autopayStatus')
          assert.equal(quote.can_fund, true)
          assert.equal(quote.effective_amount_per_block, String(Math.max(rate, required)))
          if (index > 0) assert.ok(Number(quote.balance || 0) < rate, 'equal/higher-rate fixture must have insufficient balance')
          const beforeCancel = broadcasts
          let dialog = await openFunding()
          await expect(dialog.getByText(`${quote.effective_amount_per_block} ${quote.fee_asset}`, { exact: true })).toBeVisible()
          await expect(dialog).toContainText(quote.recommended_funding_amount)
          await expect(dialog).toContainText(quote.contract_address)
          const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true })
          await cancel.scrollIntoViewIfNeeded()
          await expect(cancel).toBeInViewport({ ratio: 1 })
          await cancel.click()
          await expect(paidPage.getByRole('button', { name: '充值 AUTOPAY', exact: true })).toBeEnabled()
          assert.equal(broadcasts, beforeCancel, 'cancel confirmation broadcast a payment')
          if (index === 0) {
            dialog = await openFunding()
            await configure('cancel')
            await configure('config', required * 2)
            // The open dialog retains the quotation actually being approved.
            await expect(dialog.getByText(`${quote.effective_amount_per_block} ${quote.fee_asset}`, { exact: true })).toBeVisible()
            const beforeChangedQuote = broadcasts
            await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
            await expect(paidPage.getByRole('alert').filter({ hasText: '充值参数已变化' })).toBeVisible()
            assert.equal(broadcasts, beforeChangedQuote, 'changed quote broadcast without re-confirmation')
            await configure('config', rate)
          }
          quote = await accountCall(paidPage, 'autopayStatus')
          dialog = await openFunding()
          await expect(dialog.getByText(`${quote.effective_amount_per_block} ${quote.fee_asset}`, { exact: true })).toBeVisible()
          const beforeFunding = broadcasts
          captureFunding = true
          await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
          await expect.poll(() => evidence.length, { timeout: 90000 }).toBe(index + 1)
          await expect(paidPage.getByRole('button', { name: '查询充值状态', exact: true })).toBeEnabled({ timeout: 160000 })
          captureFunding = false
          assert.equal(broadcasts, beforeFunding + 1, 'confirmation did not submit exactly one payment')
          Object.assign(evidence[index], { rate: quote.effective_amount_per_block,
            amount: quote.recommended_funding_amount, asset: quote.fee_asset, contract: quote.contract_address,
            payer: quote.payer, quote_height: quote.current_block })
          await expect.poll(async () => (await accountCall(paidPage, 'autopayStatus')).ready, { timeout: 90000 }).toBe(true)
        }
        // Restore the original delegate economics for the existing 200-record
        // timeout scenario which follows, reusing the returned fee balance.
        await configure('cancel')
        await configure('config', required)
        const quote = await accountCall(paidPage, 'autopayStatus')
        const result = await accountCall(paidPage, 'fundAutopay', quote)
        assert.ok(result.transaction_id)
        await expect.poll(async () => (await accountCall(paidPage, 'autopayStatus')).ready, { timeout: 90000 }).toBe(true)
        // The native parent decodes the actual signed SatoshiNet wire transactions
        // and checks their Config payload and contract funding asset/amount.
        console.log(JSON.stringify({ autopay_funding_confirmation_transactions: evidence }))
      } finally {
        captureFunding = false
        await paidPage.context().unroute('**/*', routeFunding)
        if (await paidPage.getByRole('dialog').isVisible()) await paidPage.keyboard.press('Escape')
      }
    })

    await check('RGB PWA: submitted AUTOPAY funding survives timeout reload and repeated confirmation', async () => {
      const initial = await accountCall(paidPage, 'autopayStatus')
      assert.equal(initial.ready, true, 'timeout scenario requires a ready delegate')
      assert.notEqual(initial.funding_pending, true, 'timeout scenario must not reuse an unresolved prior payment')
      assert.equal(initial.amount_per_block, initial.required_amount_per_block, 'timeout scenario requires the restored 100-record fee rate')
      let submissions = 0
      const submittedBodies = []
      let delayState = false
      let delayHistory = false
      let delayedQueries = 0
      const fundingRoute = async route => {
        const url = new URL(route.request().url())
        if (route.request().method() === 'POST' && /\/btc\/tx$/.test(url.pathname)) {
          submissions++
          submittedBodies.push(route.request().postData())
          // Submit to the actual temporary node; never manufacture a TXID.
          const response = await route.fetch()
          await route.fulfill({ response })
          delayState = true
          delayHistory = true
          return
        }
        if ((delayState && /\/v3\/contracts\/[^/]+\/state$/.test(url.pathname)) ||
            (delayHistory && /\/v3\/contracts\/[^/]+\/history$/.test(url.pathname))) {
          delayedQueries++
          return route.abort('failed')
        }
        return route.fallback()
      }
      await paidPage.context().route('**/*', fundingRoute)
      const confirmPaid = async () => {
        await paidPage.getByRole('button', { name: '确认存储方式', exact: true }).click()
        await paidPage.getByRole('dialog').getByRole('button', { name: 'Confirm', exact: true }).click()
        await paidPage.getByRole('dialog').waitFor({ state: 'hidden' })
      }
      const select200 = async () => {
        await paidPage.getByRole('button', { name: '重新配置恢复或升级存储', exact: true }).click()
        await paidPage.getByRole('button', { name: '检查账户', exact: true }).click()
        const dialog = paidPage.getByRole('dialog')
        await dialog.locator('input[type="password"]').fill(fixture.password)
        await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
        await dialog.waitFor({ state: 'hidden' })
        const paid = (await accountCall(paidPage, 'getStorageOptions')).options.find(option => option.id === 'paid')
        await paidPage.getByRole('button', { name: paid.title, exact: false }).click()
        await paidPage.getByLabel('持久记录条数', { exact: true }).fill('200')
      }
      try {
        await select200()
        await paidPage.evaluate(() => {
          const put = IDBObjectStore.prototype.put
          window.__restoreFundingPut = () => { IDBObjectStore.prototype.put = put }
          IDBObjectStore.prototype.put = function(value, key) {
            if (this.name === 'kv' && String(key).includes('oplog-r-')) {
              throw new DOMException('funding log persistence failure', 'QuotaExceededError')
            }
            return put.call(this, value, key)
          }
        })
        try {
          await confirmPaid()
          await expect(paidPage.getByRole('alert').filter({ hasText: 'funding log persistence failure' })).toBeVisible()
          assert.equal(submissions, 0, 'a required log failure must prevent broadcasting')
        } finally { await paidPage.evaluate(() => window.__restoreFundingPut()) }
        await confirmPaid()
        await expect.poll(() => submissions, { timeout: 90000 }).toBe(1)
        // This is the production two-minute wait, with contract state and execution-history
        // queries delayed after a real submission. No SDK clock override.
        const tracker = paidPage.getByRole('region', { name: 'AUTOPAY 充值追踪' })
        await expect(tracker).toContainText('原充值交易等待提交或合约确认', { timeout: 160000 })
        await expect(paidPage.getByRole('button', { name: '确认存储方式', exact: true })).toBeEnabled({ timeout: 160000 })
        const trackingText = await tracker.innerText()
        const txid = trackingText.match(/[0-9a-f]{64}/)?.[0]
        assert.ok(txid, 'the rendered receipt must retain the actual signed transaction ID')
        assert.ok(delayedQueries > 0)
        assert.equal(await accountCall(paidPage, 'resumePendingStorageAuthorization'), null,
          'pending payment cannot authorize paid recovery storage')
        await paidPage.reload(); await ready(paidPage); await unlock(paidPage, fixture.password)
        await paidPage.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
        await expect(paidPage.getByRole('region', { name: 'AUTOPAY 充值追踪' })).toContainText(txid)
        await paidPage.getByRole('button', { name: '继续提交原交易', exact: true }).click()
        await expect.poll(() => submissions, { timeout: 90000 }).toBe(2)
        assert.equal(submittedBodies[1], submittedBodies[0], 'continuation created a different signed transaction')
        const logs = await paidPage.evaluate(async () => {
          const response = await window.sat20wallet_operation_log.getOperationLogs()
          if (response.code !== 0) throw new Error(response.msg)
          return JSON.parse(response.data.logs)
        })
        const receipt = logs.find(log => log.action === 'account_autopay_fund' && log.txid === txid)
        assert.ok(receipt)
        assert.equal(receipt.status, 'pending')
        assert.equal(receipt.parameters.amount_per_block, '20')
        assert.equal(receipt.parameters.amount, '20000')
        const clear = await paidPage.evaluate(() => window.sat20wallet_operation_log.deleteAllOperationLogs())
        assert.notEqual(clear.code, 0, 'clearing logs must not erase the unresolved payment receipt')
        // Read the actual history before claiming it proves this transaction.
        // A block Result can aggregate several invokes and expose only one
        // source reference. An unrelated Result cannot complete our receipt.
        delayHistory = false
        const history = []
        for (let start = 0; ;) {
          const response = await walletCall(paidPage, 'queryContract', {
            ContractType: 'template', Query: 'history', Contract: receipt.parameters.contract, Start: start, Limit: 100,
          })
          const batch = JSON.parse(response.result)
          history.push(...batch.data)
          start += batch.data.length
          if (!batch.data.length || start >= batch.total) break
        }
        assert.equal(history.filter(item => item.txid === txid && item.kind === 'invoke' && item.height > 0).length, 1,
          'real node history must contain exactly one confirmed original invocation')
        const originalResult = history.find(item => item.kind === 'result' && item.height > 0 &&
          (item.txid === txid || item.details?.result_for_txid === txid))
        console.log(JSON.stringify({ funding_history_evidence: { original_invocation_count: 1,
          original_result_linked: Boolean(originalResult), original_result_status: originalResult?.status } }))
        if (originalResult) {
          assert.equal(originalResult.status, 'success', 'original execution did not succeed')
          await expect.poll(async () => !(await accountCall(paidPage, 'autopayStatus')).funding_pending,
            { timeout: 90000, message: 'independently proven original result completes the receipt' }).toBe(true)
        } else {
          assert.equal((await accountCall(paidPage, 'autopayStatus')).funding_pending, true,
            'unrelated aggregate history falsely completed the original receipt')
        }
        const historical = await accountCall(paidPage, 'autopayStatus')
        assert.equal(historical.ready, false)
        assert.equal(historical.can_fund, false, 'unavailable current state cannot authorize a new payment')
        assert.equal(historical.funding_transaction_id, txid)
        assert.equal(submissions, 2, 'status and history queries broadcast another transaction')
        delayState = false
        await paidPage.getByRole('button', { name: '查询充值状态', exact: true }).click()
        await expect.poll(async () => {
          await paidPage.getByRole('button', { name: '查询充值状态', exact: true }).click()
          return (await accountCall(paidPage, 'autopayStatus')).ready
        }, { timeout: 90000 }).toBe(true)
        await expect(paidPage.getByRole('region', { name: 'AUTOPAY 充值追踪' })).toContainText('合约查询确认 AUTOPAY 支付已就绪')
        await expect(paidPage.getByRole('region', { name: 'AUTOPAY 充值追踪' })).toContainText(txid)
        const completed = await paidPage.evaluate(async receiptTxID => {
          const response = await window.sat20wallet_operation_log.getOperationLogs()
          if (response.code !== 0) throw new Error(response.msg)
          const matches = JSON.parse(response.data.logs).filter(log => log.action === 'account_autopay_fund' && log.txid === receiptTxID)
          if (matches.length !== 1) throw new Error('original funding must retain exactly one operation log')
          return matches[0]
        }, txid)
        assert.equal(completed.status, 'succeeded', 'confirmed original transaction and ready contract did not finish the receipt')
        // Reload returned to the maintenance entry. Use its visible controls
        // again before confirming the 200-record authorization.
        await select200()
        await confirmPaid()
        await paidPage.getByRole('button', { name: '创建并保存加密账户备份', exact: true }).waitFor()
        assert.equal(submissions, 2, 'ready contract must reuse the original funding')
        await paidPage.getByRole('button', { name: '取消本次设置', exact: true }).click()
      } finally { await paidPage.context().unroute('**/*', fundingRoute) }
    })
  } finally { await paidPage.context().close() }
}

export async function runAutopayPwaReviewCases(t, fixture) {
  const { check, device, ready, accountCall, walletCall, questions, answers } = t
  const g = await device(), owner = await device(), password = fixture.password
  const poll = { timeout: 90000, intervals: [250, 500, 1000] }
  const mine = async () => {
    const r = await fetch(new URL('/mine', fixture.control_url), { method: 'POST', signal: AbortSignal.timeout(90000) })
    assert.equal(r.ok, true); assert.equal((await r.json()).mined, true)
  }
  const management = async () => {
    await g.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
    await g.getByRole('heading', { name: '维护恢复配置', exact: true }).waitFor()
  }
  const cold = async () => {
    await g.reload(); await ready(g)
    await g.locator('input[type="password"]').fill(password)
    await g.locator('form button[type="submit"]').click()
    await expect.poll(() => g.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), poll).toBe(false)
    await management()
  }
  const passwordDialog = async () => {
    const d = g.getByRole('dialog')
    await d.locator('input[type="password"]').fill(password)
    await d.getByRole('button', { name: 'Confirm', exact: true }).click()
  }
  const cancel = async () => {
    const r = await walletCall(g, 'invokeUnifiedContract', { ContractType: 'template', SubType: 'autopay.tc', ContractAddress: fixture.contract,
      Action: 'cancel', Param: '{}', ParamEncoding: 'json', Assets: [] })
    assert.ok(r.txid); await mine()
    await expect.poll(async () => (await accountCall(g, 'autopayStatus')).can_fund, poll).toBe(true)
    await cold()
    await expect(g.getByRole('button', { name: '充值 AUTOPAY', exact: true })).toBeVisible()
  }
  const fund = async () => {
    await g.getByRole('button', { name: '充值 AUTOPAY', exact: true }).click()
    await g.getByRole('dialog').getByRole('button', { name: 'Confirm', exact: true }).click()
  }
  try {
    await check('usage: temporary Guardian renews paid hosting without changing its own recovery', async () => {
      await g.evaluate(async f => {
        const [e] = await window.__SAT20_PWA_VERIFY__.useWalletStore().importWallet(f.mnemonic, f.password); if (e) throw e
      }, fixture)
      await owner.evaluate(async p => {
        const [e] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(p); if (e) throw e
      }, password)
      await expect.poll(async () => { const s = await accountCall(g, 'status'); return !s.pending_changes && !s.managed_data_dirty }, poll).toBe(true)
      await accountCall(g, 'confirmStorage', 'temporary')
      const own = await accountCall(g, 'createRecovery', { password, wallets: [], recovery_mode: '2of2', questions })
      for (let attempt = 0; attempt < 4; attempt++) {
        try { await accountCall(g, 'rehearse', own.session_id, answers, own.user_share, password); break }
        catch (error) {
          if (!/publish account activation state: dkvs write conflict/.test(error.message) || attempt === 3) throw error
          assert.equal((await accountCall(g, 'status')).recovery_configured, false)
          await expect.poll(async () => { const s = await accountCall(g, 'status'); return !s.pending_changes && !s.managed_data_dirty }, poll).toBe(true)
        }
      }
      const before = await accountCall(g, 'status'); assert.equal(before.storage_mode, 'temporary')
      await management()
      await g.getByRole('button', { name: '生成我的联系信息并发送给好友', exact: true }).click(); await passwordDialog()
      await expect.poll(() => g.locator('textarea[readonly]').count(), poll).toBe(2)
      const contact = JSON.parse(await g.locator('textarea[readonly]').nth(1).inputValue())
      await accountCall(owner, 'confirmStorage', 'temporary')
      const material = await accountCall(owner, 'createRecovery', { password, wallets: [], recovery_mode: '2of3', questions, guardian: contact })
      await g.getByPlaceholder('好友发送的 Guardian setup JSON').fill(material.guardian_setup)
      await g.locator('select').selectOption('paid')
      await g.getByRole('button', { name: '接受并保存好友分片', exact: true }).click(); await passwordDialog()
      await expect(g.getByRole('dialog')).toContainText('100')
      await g.getByRole('dialog').getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect.poll(() => g.getByRole('button', { name: '接受并保存好友分片', exact: true }).isEnabled(), poll).toBe(true)
      await expect.poll(() => g.locator('textarea[readonly]').count(), poll).toBe(3)
      const receipt = await g.locator('textarea[readonly]').last().inputValue()
      await accountCall(owner, 'checkGuardianSetup', material.session_id, receipt)
      const request = (await accountCall(owner, 'createGuardianRequest', material.session_id)).request
      await cancel(); await fund(); await mine()
      await expect.poll(async () => (await accountCall(g, 'autopayStatus')).ready, poll).toBe(true)
      await cold()
      const after = await accountCall(g, 'status')
      for (const field of ['storage_mode', 'package_id', 'public_locator']) assert.equal(after[field], before[field])
      await g.getByPlaceholder('好友发送的 Guardian 恢复请求 JSON').fill(request)
      await g.getByRole('button', { name: '生成加密恢复响应', exact: true }).click(); await passwordDialog()
      await expect.poll(() => g.locator('textarea[readonly]').count(), poll).toBe(2)
      const response = await g.locator('textarea[readonly]').last().inputValue()
      await accountCall(owner, 'consumeGuardianResponse', material.session_id, response)
    })
    await check('usage: original AUTOPAY resumes after prebroadcast exit and lost acknowledgement', async () => {
      for (const mode of ['before-broadcast', 'acknowledgement-lost']) {
        await cancel()
        const attempts = []; let hide = true, delivered = 0
        const interrupt = async route => {
          const path = new URL(route.request().url()).pathname
          if (hide && path.includes('/btc/tx/simpleinfo/')) return route.abort('blockedbyclient')
          if (route.request().method() !== 'POST' || !path.endsWith('/btc/tx')) return route.fallback()
          attempts.push(route.request().postDataJSON().signedTxHex)
          if (attempts.length === 1 && mode === 'before-broadcast') return route.abort('blockedbyclient')
          const response = await route.fetch(); delivered++; assert.equal((await response.json()).code, 0)
          if (attempts.length === 1) return route.abort('blockedbyclient')
          hide = false; await mine(); await route.fulfill({ response })
        }
        await g.context().route('**/*', interrupt)
        try {
          await fund()
          await expect.poll(async () => (await accountCall(g, 'autopayStatus')).funding_pending, poll).toBe(true)
          const pending = await accountCall(g, 'autopayStatus')
          assert.match(pending.funding_transaction_id, /^[0-9a-f]{64}$/)
          assert.equal(attempts.length, 1); assert.equal(delivered, mode === 'before-broadcast' ? 0 : 1)
          await cold()
          await expect(g.getByText(`交易号：${pending.funding_transaction_id}`, { exact: true })).toBeVisible()
          await g.getByRole('button', { name: '查询充值状态', exact: true }).click()
          await expect(g.getByRole('button', { name: '继续提交原交易', exact: true })).toBeEnabled()
          assert.equal(attempts.length, 1)
          await g.getByRole('button', { name: '继续提交原交易', exact: true }).click()
          await expect.poll(() => attempts.length, poll).toBe(2)
          assert.equal(attempts[1], attempts[0], 'cold continuation changed original signed bytes')
          await expect.poll(async () => !(await accountCall(g, 'autopayStatus')).funding_pending, poll).toBe(true)
          const final = await accountCall(g, 'autopayStatus')
          assert.equal(final.funding_transaction_id, pending.funding_transaction_id); assert.equal(final.ready, true)
          const logs = await g.evaluate(async txid => {
            const r = await window.sat20wallet_operation_log.getOperationLogs(); if (r.code !== 0) throw new Error(r.msg)
            return JSON.parse(r.data.logs).filter(l => l.action === 'account_autopay_fund' && l.txid === txid)
          }, pending.funding_transaction_id)
          assert.equal(logs.length, 1); assert.equal(logs[0].status, 'succeeded')
        } finally { await g.context().unroute('**/*', interrupt) }
      }
    })
  } finally { await g.context().close(); await owner.context().close() }
}
