import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { isDeepStrictEqual } from 'node:util'
import { expect } from '@playwright/test'
import { setupAccount, restoreAccount, waitAccountBackup, recoveryAnswers, recoveryShare } from './account-fixture-e2e.mjs'
import { runChannelImportCase } from './channel-import-e2e.mjs'
const accountMessages = JSON.parse(readFileSync(new URL('../../locales/en.json', import.meta.url), 'utf8')).accountManagement
export function installWalletStartProbe() {
  if (window.__walletStartProbe) return
  const probe = { wasm: 0, dbOpen: 0, dbWrite: 0, storageWrite: 0 }
  Object.defineProperty(window, '__walletStartProbe', { value: probe, configurable: true })
  const wrap = (owner, key, field) => {
    if (!owner) return
    const descriptor = Object.getOwnPropertyDescriptor(owner, key)
    if (!descriptor || typeof descriptor.value !== 'function' || descriptor.value.__walletStartProbeWrapped) return
    const original = descriptor.value
    const wrapped = function (...args) {
      probe[field]++
      return Reflect.apply(original, this, args)
    }
    Object.defineProperty(wrapped, '__walletStartProbeWrapped', { value: true })
    Object.defineProperty(owner, key, { ...descriptor, value: wrapped })
  }
  wrap(WebAssembly, 'instantiate', 'wasm')
  wrap(WebAssembly, 'instantiateStreaming', 'wasm')
  wrap(IDBFactory.prototype, 'open', 'dbOpen')
  wrap(IDBFactory.prototype, 'deleteDatabase', 'dbWrite')
  for (const method of ['put', 'add', 'delete', 'clear']) wrap(IDBObjectStore.prototype, method, 'dbWrite')
  if (typeof IDBCursor !== 'undefined') wrap(IDBCursor.prototype, 'update', 'dbWrite')
  for (const method of ['setItem', 'removeItem', 'clear']) wrap(Storage.prototype, method, 'storageWrite')
}

export async function waitForAccountWrite(page, accountCall) {
  let observed
  try {
    await expect.poll(async () => {
      const view = await accountCall(page, 'getAccountView')
      observed = { state: view.state, error_code: view.error_code }
      return view.state === 'local' || view.state === 'ready'
    }, { timeout: 90000, message: 'business preparation requires a verified account write qualification' }).toBe(true)
  } catch (error) {
    console.error(JSON.stringify({ accountWriteWaitFailure: observed }))
    throw error
  }
}

export const runtimeAccountCases = ["usage: background catalog refresh cannot invalidate a subaccount switch", "usage: independent address DID entry persists changes and clearing through recovery", "usage: address DID persistence failure leaves catalog and recovery value unchanged", "usage: committed creation and import pages recover from a catalog read failure without replay", "usage: SDK creation page delivers mnemonic without redundant PWA persistence", "usage: import page and cold selection use SDK without redundant PWA persistence", "usage: IndexedDB open failure recovers on explicit retry", "usage: PWA snapshot read failure preserves durable and displayed account state", "usage: cold startup SDK status read failure preserves selection and retries from page", "usage: browser password transaction failure preserves every wallet and original password", "usage: same-context PWA pages serialize Manager lifetime", "usage: refresh serializes PWA Manager startup", "usage: active Manager rejects a pending account write after same-page lock", "usage: same-page subaccount switch discards a stale account response", "usage: committed wallet selection survives single Manager handoff", "usage: password changes apply on single Manager handoff", 'usage: maintenance rehearsal locks after inactivity and discards its session', 'usage: committed recovery response loss does not duplicate wallets', 'usage: background backup failure and recovery remain visible on the account page']

export async function runPwaUsageCases(t) {
  const { check, device, waitingDevice, ready, unlock, catalog, walletCall, accountCall, password, newPassword } = t
  const failures = []
  const scenario = async (name, action) => {
    if (t.focusedUsageCases && !t.focusedUsageCases.includes(name)) return
    try { await check(name, action) } catch (error) { failures.push({ name, message: error.message }) }
  }
  const createLocal = page => page.evaluate(async password => {
    const [error, mnemonic] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(password)
    if (error) throw error
    return mnemonic
  }, password)
  const fresh = async () => {
    const page = await device()
    await createLocal(page)
    await waitForAccountWrite(page, accountCall)
    return page
  }
  const using = async action => {
    const page = await fresh()
    try { await action(page) }
    catch (error) { await t.preserveBrowserFailureState?.(); throw error }
    finally { await page.context().close() }
  }
  const setup = page => setupAccount(t, page)
  const restore = (page, material) => restoreAccount(t, page, material)
  const settled = page => waitAccountBackup(page, accountCall)
  const confirmPassword = async page => {
    const dialog = page.getByRole('dialog', { name: 'Confirm wallet password', exact: true })
    await dialog.locator('input[type=password]').fill(password)
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await expect(dialog).not.toBeVisible()
  }

  await scenario('usage: maintenance rehearsal locks after inactivity and discards its session', () => using(async page => {
    const material = await setup(page)
    await page.evaluate(async () => {
      await (await import('/store/global.ts')).useGlobalStore().setAutoLockTime('1')
      const load = window.sat20account_wasm.loadRecovery
      window.sat20account_wasm.loadRecovery = async (...args) => {
        const result = await load(...args); window.idleSession = result.data?.session_id; return result
      }
      location.hash = '#/wallet/setting/account-management'
    })
    await page.getByRole('button', { name: accountMessages.ui.rehearsalTitle, exact: true }).click()
    await page.getByPlaceholder(accountMessages.ui.pasteLocator).fill(material.locator)
    const began = Date.now()
    await page.getByRole('button', { name: accountMessages.ui.loadBackup, exact: true }).click()
    await page.waitForFunction(() => Boolean(window.idleSession))
    const session = await page.evaluate(() => window.idleSession)
    await expect.poll(() => new URL(page.url()).hash, { timeout: 80000, intervals: [1000] }).toMatch(/^#\/unlock/)
    assert.ok(Date.now() - began >= 55000)
    assert.equal(await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), true)
    await page.locator('form input[type=password]').fill(password)
    await page.locator('form button[type=submit]').click()
    await page.waitForURL('**/#/restore-account?mode=rehearsal')
    await assert.rejects(accountCall(page, 'previewRecovery', session, { answers: recoveryAnswers, user_share: recoveryShare(material) }), /session|expired/i)
    await expect(page.getByPlaceholder(accountMessages.ui.pasteUserShare)).toHaveCount(0)
    await page.goBack()
    await page.waitForURL('**/#/wallet/setting/account-management')
    await expect(page.getByRole('heading', { name: accountMessages.ui.managementTitle, exact: true })).toBeVisible()
  }))

  await scenario('usage: committed recovery response loss does not duplicate wallets', async () => {
    const owner = await fresh()
    const page = await device()
    try {
      const material = await setup(owner)
      await page.evaluate(() => { location.hash = '#/restore-account' })
      await page.getByPlaceholder(accountMessages.ui.pasteLocator).fill(material.locator)
      await page.getByRole('button', { name: accountMessages.ui.loadBackup, exact: true }).click()
      for (const [index, answer] of recoveryAnswers.entries()) await page.locator('section input[type=password]').nth(index).fill(answer.text)
      await page.getByPlaceholder(accountMessages.ui.pasteUserShare).fill(recoveryShare(material))
      await page.getByRole('button', { name: accountMessages.ui.previewRecovery, exact: true }).click()
      await page.locator('#restore-local-password').fill(password)
      await page.locator('#restore-confirm-password').fill(password)
      await page.evaluate(() => {
        window.completedRestores = 0
        window.recoveryResults = []
        const original = window.sat20account_wasm.commitRecovery
        window.sat20account_wasm.commitRecovery = async (...args) => {
          const result = await original(...args)
          window.recoveryResults.push({ code: result.code, msg: result.msg })
          if (result.code === 0) { window.completedRestores++; throw new Error('test lost committed recovery response') }
          return result
        }
      })
      await page.getByRole('button', { name: accountMessages.ui.restoreAll, exact: true }).click()
      await expect(page.getByRole('alert')).toContainText('test lost committed recovery response')
      const committed = await catalog(page)
      assert.ok(committed.length)
      await page.getByRole('button', { name: accountMessages.ui.restoreAll, exact: true }).click()
      await page.waitForFunction(() => window.recoveryResults.length === 2)
      const replay = await page.evaluate(() => window.recoveryResults[1])
      assert.notEqual(replay.code, 0)
      await expect(page.getByRole('alert')).toContainText(accountMessages.ui.sessionExpired)
      await expect(page.locator('#restore-local-password')).toHaveCount(0)
      assert.equal(await page.evaluate(() => window.completedRestores), 1)
      await page.reload(); await ready(page); await unlock(page)
      assert.deepEqual(await catalog(page), committed)
      assert.equal(await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), false)
    } finally { await page.context().close(); await owner.context().close() }
  })

  await scenario('usage: background backup failure and recovery remain visible on the account page', () => using(async page => {
    await setup(page)
    await page.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
    const status = page.locator('section').first()
    const backedUp = accountMessages.ui.backupState.replace('{value}', accountMessages.backupState.backed_up)
    await expect(status).toContainText(backedUp)
    const beforeURL = page.url()
    let failures = 0
    const unavailable = async route => {
      if (route.request().method() === 'POST' && new URL(route.request().url()).pathname.endsWith('/records/batch-cas')) {
        failures++; return route.abort('failed')
      }
      return route.fallback()
    }
    await page.context().route('**/*', unavailable)
    let publishedCatalog
    try {
      await page.evaluate(async () => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        await store.updateWalletName(store.walletId, 'Offline backup remains local')
      })
      publishedCatalog = await catalog(page)
      assert.equal(publishedCatalog[0].name, 'Offline backup remains local')
      await expect.poll(() => failures, { timeout: 90000 }).toBeGreaterThan(0)
      let failed
      await expect.poll(async () => {
        failed = await accountCall(page, 'getAccountView')
        return Boolean(failed.error_code || failed.modules.some(module => module.error_code))
      }, { timeout: 90000 }).toBe(true)
      const code = failed.error_code || failed.modules.find(module => module.error_code).error_code
      await expect(status).toContainText(code, { timeout: 45000 })
      await expect(status).not.toContainText(backedUp)
      // A lost transport response is not a confirmed ACK cleanup failure.
      // New-write eligibility may pause or recover; committed data stays readable.
      assert.deepEqual(await catalog(page), publishedCatalog)
    } finally { await page.context().unroute('**/*', unavailable) }
    await status.getByRole('button', { name: accountMessages.readiness.retryNow, exact: true }).click()
    await settled(page)
    await expect(status).toContainText(backedUp, { timeout: 45000 })
    assert.deepEqual(await catalog(page), publishedCatalog)
    assert.equal(page.url(), beforeURL)

    // Now keep this test's whole browser context offline, including receiving.
    // Preload UI modules while still online.
    const walletMessages = JSON.parse(readFileSync(new URL('../../locales/en.json', import.meta.url), 'utf8')).walletManager
    await page.evaluate(() => { location.hash = '#/wallet/manager' })
    await expect(page.getByRole('heading', {name: walletMessages.title, exact: true})).toBeVisible()
    await page.evaluate(async () => {
      const { Storage } = await import('/lib/storage-adapter.ts')
      const { useAccountManagementStore } = await import('/store/accountManagement.ts')
      window.__offlineReadSnapshot = async () => (await Storage.get({key:'local:wallet_state_snapshot_v1'})).value
      window.__offlineAccountState = useAccountManagementStore()
      window.__offlineRenameObservation = null
      const progress = {rejects:0, events:0, rejectsAtEvent:0}
      window.__offlineProgress = progress
      const originalFetch = window.fetch
      window.fetch = async function (...args) {
        try { return await Reflect.apply(originalFetch, this, args) }
        catch (error) {
          const input = args[0]
          const url = typeof input === 'string' || input instanceof URL ? String(input) : input.url
          if (new URL(url, location.href).pathname.includes('/dkvs/')) progress.rejects++
          throw error
        }
      }
      const onStatus = () => { progress.events++; progress.rejectsAtEvent = progress.rejects }
      window.addEventListener('sat20:account-status-updated', onStatus)
      window.__offlineRestoreProgress = () => {
        window.fetch = originalFetch
        window.removeEventListener('sat20:account-status-updated', onStatus)
      }
      const original = window.sat20wallet_wasm.updateWalletName
      window.__offlineRestoreRenameObserver = () => { window.sat20wallet_wasm.updateWalletName = original }
      window.sat20wallet_wasm.updateWalletName = async (...args) => {
        try {
          const result = await original(...args)
          window.__offlineRenameObservation = {code:result?.code, rejected:result?.code !== 0}
          return result
        } catch (error) {
          window.__offlineRenameObservation = {code:null, rejected:true}
          throw error
        }
      }
      location.hash = '#/wallet/setting/account-management'
    })
    await expect(status).toContainText(backedUp)
    const committedCatalog = await catalog(page)
    const committedView = await accountCall(page, 'getAccountView')
    const committedModules = committedView.modules
    assert.ok(committedModules.length > 0)
    assert.equal(await page.evaluate(() => window.__offlineAccountState.retryFailed),false,'prior single-request fault must have recovered')
    // This is the PWA preference snapshot only, not an SDK module DB snapshot.
    const snapshotBefore = await page.evaluate(() => window.__offlineReadSnapshot())
    let offlineFailureCount = 0
    const failedRequest = request => {
      if (['fetch','xhr'].includes(request.resourceType()) && new URL(request.url()).pathname.includes('/dkvs/')) offlineFailureCount++
    }
    const renameThroughUI = async name => {
      await page.evaluate(() => { location.hash = '#/wallet/manager' })
      await expect(page.getByRole('heading', {name:walletMessages.title,exact:true})).toBeVisible()
      // The current wallet's name has the only pencil button in this label.
      await page.locator('div.font-medium').filter({hasText:committedCatalog[0].name}).getByRole('button').click()
      const dialog = page.getByRole('dialog', {name:walletMessages.editWalletName,exact:true})
      await dialog.locator('#walletName').fill(name)
      await page.evaluate(() => { window.__offlineRenameObservation = null })
      await dialog.getByRole('button', {name:walletMessages.save,exact:true}).click()
      await expect.poll(() => page.evaluate(() => window.__offlineRenameObservation), {timeout:30000}).not.toBeNull()
      return dialog
    }
    page.on('requestfailed', failedRequest)
    const progressBefore = await page.evaluate(() => ({...window.__offlineProgress}))
    try {
      await page.context().setOffline(true)
      await status.getByRole('button', {name:accountMessages.readiness.retryNow,exact:true}).click()
      try {
        await expect.poll(() => offlineFailureCount,{timeout:90000}).toBeGreaterThan(0)
        await expect.poll(() => page.evaluate(() => window.__offlineAccountState.retrying),{timeout:90000}).toBe(false)
        await expect.poll(() => page.evaluate(baseline => {
          const current = window.__offlineProgress
          return current.events > baseline.events && current.rejectsAtEvent > baseline.rejects
        }, progressBefore),{timeout:90000,message:'SDK status event must follow a real DKVS fetch rejection'}).toBe(true)
      } finally {
        // Emit real state even when a prerequisite fails; never mistake a
        // missing diagnostic string for an observed write-boundary failure.
        const current = await accountCall(page,'getAccountView')
        const progress = await page.evaluate(() => ({...window.__offlineProgress,
          retryFailed:window.__offlineAccountState.retryFailed}))
        console.log(JSON.stringify({offlinePrerequisiteEvidence:{offlineFailureCount,...progress,
          processedEvent:progress.events > progressBefore.events && progress.rejectsAtEvent > progressBefore.rejects,
          state:current.state,error_code:current.error_code,
          moduleCanWrite:current.modules.map(module=>({name:module.name,can_write:module.can_write}))}}))
      }
      // Do not pre-wait for can_write=false: stale writable state is the defect.
      assert.deepEqual(await catalog(page),committedCatalog,'offline cached catalog remains readable')
      const offlineView = await accountCall(page,'getAccountView')
      await renameThroughUI('Offline rename must be rejected')
      const observation = await page.evaluate(() => window.__offlineRenameObservation)
      const afterCatalog = await catalog(page)
      // WASM returns Go maps; object property insertion order is not catalog data.
      const catalogUnchanged = isDeepStrictEqual(afterCatalog,committedCatalog)
      console.log(JSON.stringify({offlineAccountWriteEvidence:{offlineFailureCount,
        returnedCode:observation.code,renameRejected:observation.rejected,catalogUnchanged,
        moduleCanWrite:offlineView.modules.map(module=>({name:module.name,can_write:module.can_write}))}}))
      assert.equal(observation.rejected,true,'a real offline SDK-observed account must reject wallet rename')
      assert.deepEqual(afterCatalog,committedCatalog,'rejected offline rename changed committed catalog')
      assert.equal(await page.evaluate(() => window.__offlineReadSnapshot()),snapshotBefore,'rejected offline rename changed PWA snapshot')
      assert.ok(offlineView.modules.every(module=>!module.can_write),'offline account view must not advertise writable modules')
    } finally {
      await page.context().setOffline(false)
      page.off('requestfailed',failedRequest)
      await page.evaluate(() => { window.__offlineRestoreRenameObserver(); window.__offlineRestoreProgress() })
    }
    // GREEN continuation proves reconnect/retry restores ordinary rename+backup.
    await page.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
    await status.getByRole('button',{name:accountMessages.readiness.retryNow,exact:true}).click()
    await settled(page)
    await waitForAccountWrite(page,accountCall)
    // Restore only the observation wrapper for this final normal UI action.
    await page.evaluate(() => {
      const original = window.sat20wallet_wasm.updateWalletName
      window.__offlineRestoreRenameObserver = () => { window.sat20wallet_wasm.updateWalletName = original }
      window.sat20wallet_wasm.updateWalletName = async (...args) => {
        const result = await original(...args)
        window.__offlineRenameObservation = {code:result?.code,rejected:result?.code !== 0}
        return result
      }
    })
    try {
      const dialog = await renameThroughUI('Online rename after retry')
      assert.deepEqual(await page.evaluate(() => window.__offlineRenameObservation),{code:0,rejected:false})
      await expect(dialog).not.toBeVisible()
      assert.equal((await catalog(page))[0].name,'Online rename after retry')
      await settled(page)
    } finally { await page.evaluate(() => { window.__offlineRestoreRenameObserver() }) }
  }))
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
    await page.getByRole('button', { name: 'Set address DID', exact: true }).click()
    await page.getByRole('heading', { name: 'Set address DID', exact: true }).waitFor()
  }
  const saveDID = async (page, did) => {
    await page.getByLabel('Address DID (optional)', { exact: true }).fill(did)
    await page.getByRole('button', { name: 'Save DID', exact: true }).click()
    await expect(page.getByText('DID saved', { exact: true })).toBeVisible()
  }

  await scenario('usage: independent address DID entry persists changes and clearing through recovery', () => using(async page => {
    await didSettings(page)
    await saveDID(page, 'alice.btc')
    await saveDID(page, 'bob.btc')
    const before = await catalog(page)
    assert.equal(before[0].accounts[0].did, 'bob.btc')
    await page.reload(); await ready(page); await unlock(page)
    await didSettings(page)
    await expect(page.getByLabel('Address DID (optional)', { exact: true })).toHaveValue('bob.btc')
    const material = await setup(page)
    const restored = await device()
    try {
      await restore(restored, material)
      assert.equal((await catalog(restored))[0].accounts[0].did, 'bob.btc')
      assert.equal((await catalog(restored))[0].accounts[0].name, before[0].accounts[0].name)
      await expect.poll(async () => {
        const status = await accountCall(restored, 'getAccountView')
        return status.active && status.state === 'ready'
      }, { timeout: 90000 }).toBe(true)
      await didSettings(restored)
      await restored.getByRole('button', { name: 'Clear DID', exact: true }).click()
      await expect(restored.getByLabel('Address DID (optional)', { exact: true })).toHaveValue('')
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
        if (this.name === 'kv' && String(key).endsWith('account-management-profile-v1')) {
          throw new DOMException('DID profile persistence failure', 'QuotaExceededError')
        }
        return put.call(this, value, key)
      }
    })
    try {
      await page.getByLabel('Address DID (optional)', { exact: true }).fill('rejected.btc')
      await page.getByRole('button', { name: 'Save DID', exact: true }).click()
      await expect(page.getByRole('alert')).toContainText('DID profile persistence failure')
      assert.deepEqual(await catalog(page), before)
    } finally { await page.evaluate(() => window.__restoreDIDPut()) }
    await page.reload(); await ready(page); await unlock(page)
    await didSettings(page)
    await expect(page.getByLabel('Address DID (optional)', { exact: true })).toHaveValue('saved.btc')
  }))

  await scenario('usage: committed creation and import pages recover from a catalog read failure without replay', async () => {
    // Use the preceding creation's real phrase. The shared root already has a
    // remote recovery package and would bypass the ordinary import under test.
    let committedCreateMnemonic = ''
    for (const entry of ['create', 'import', 'manager', 'manager-import']) {
      const manager = entry.startsWith('manager')
      const importing = entry.endsWith('import')
      const page = manager ? await fresh() : await device()
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
          const method = entry.endsWith('import') ? 'importWallet' : 'createWallet'
          const submit = sdk[method].bind(sdk)
          sdk[method] = async (...args) => {
            window.committedPageSubmissions++
            const result = await submit(...args)
            if (!result[0] && result[1]) armed = true
            return result
          }
          location.hash = entry.startsWith('manager') ? '#/wallet/manager' : `#/${entry}`
        }, entry)
        if (entry === 'manager-import') {
          await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
          const dialog = page.getByRole('dialog')
          await dialog.getByRole('textbox', { name: 'RECOVERY PHRASE', exact: true }).fill(committedCreateMnemonic)
          await dialog.locator('button[type="submit"]').click()
          await confirmPassword(page)
          await expect(dialog.getByRole('button', { name: 'Reload wallet', exact: true })).toBeVisible({ timeout: 90000 })
          await expect(dialog.locator('button[type="submit"]')).toBeDisabled()
          // A programmatic submit exercises the handler's replay guard too.
          await dialog.locator('form').dispatchEvent('submit')
        } else if (entry === 'import') {
          assert.ok(committedCreateMnemonic)
          await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(committedCreateMnemonic)
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
        if (!importing) await expect(page.getByRole('button', { name: 'I saved my phrase, reload wallet', exact: true })).toBeVisible({ timeout: 90000 })
        const committed = await catalog(page)
        const added = committed.filter(wallet => !before.some(old => String(old.id) === String(wallet.id)))
        assert.equal(added.length, 1)
        const mnemonic = importing ? committedCreateMnemonic : (await page.locator('.grid.grid-cols-3 .blur-sm').allTextContents()).map(word => word.trim()).join(' ')
        if (entry === 'create') committedCreateMnemonic = mnemonic
        assert.equal((await walletCall(page, 'validateMnemonic', mnemonic, '')).fingerprint, added[0].fingerprint)
        assert.equal(await page.evaluate(() => window.committedPageFaults), 1)
        assert.equal(await page.evaluate(() => window.committedPageSubmissions), 1)
        await page.getByRole('button', { name: importing ? 'Reload wallet' : 'I saved my phrase, reload wallet', exact: true }).click()
        await ready(page)
        await page.locator('input[type="password"]').fill(password)
        await page.locator('form button[type="submit"]').click()
        await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: 90000 })
        assert.deepEqual(await catalog(page), committed)
      } catch (error) {
        const observation = await page.evaluate(() => ({
          submissions: window.committedPageSubmissions, catalogFaults: window.committedPageFaults,
        })).catch(() => null)
        console.error(JSON.stringify({ committedPageFailure: { entry, route: new URL(page.url()).hash, observation } }))
        throw error
      } finally { await page.context().close() }
    }
  })

  await scenario('usage: SDK creation page delivers mnemonic without redundant PWA persistence', async () => {
    const page = await device()
    let watchCount = 0
    const faultPath = '**/dkvs/active/sync*'
    let faultHits = 0
    const failDiscovery = route => { faultHits++; return route.abort('failed') }
    const observeWatch = request => {
      if (new URL(request.url()).pathname.endsWith('/dkvs/active/watch')) watchCount++
    }
    page.context().on('request', observeWatch)
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
      await page.context().addInitScript(() => {
        const observed = {started:0, completedStart:0, events:0, eventStart:0, rejected:0, eventRejected:0}
        window.__dkvsEntryProbe = observed
        const original = window.fetch
        window.fetch = async function (...args) {
          const input = args[0]
          const url = typeof input === 'string' || input instanceof URL ? String(input) : input.url
          const sync = new URL(url, location.href).pathname.endsWith('/dkvs/active/sync')
          const start = sync ? ++observed.started : 0
          try {
            const response = await Reflect.apply(original, this, args)
            if (sync && response.ok) observed.completedStart = Math.max(observed.completedStart,start)
            return response
          } catch (error) {
            if (sync) observed.rejected++
            throw error
          }
        }
        const event = () => { observed.events++; observed.eventStart = observed.completedStart; observed.eventRejected = observed.rejected }
        window.addEventListener('sat20:account-status-updated',event)
        window.__restoreEntryProbe = () => {
          window.fetch = original
          window.removeEventListener('sat20:account-status-updated',event)
        }
      })
      await page.reload(); await ready(page); await unlock(page)
      try {
        await expect.poll(() => page.evaluate(() => {
          const p = window.__dkvsEntryProbe
          return p.completedStart > 0 && p.eventStart >= p.completedStart
        }),{timeout:90000,message:'cold entry sync must complete before its SDK status event'}).toBe(true)
      } finally {
        const progress = await page.evaluate(() => ({...window.__dkvsEntryProbe}))
        console.log(JSON.stringify({dkvsActivationWatchEvidence:{phase:'cold-entry',...progress,watchCount}}))
      }
      const displayed = await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().wallets.map(wallet => wallet.fingerprint).sort())
      assert.deepEqual(displayed, committed.map(wallet => wallet.fingerprint).sort())
      assert.deepEqual(await catalog(page), committed)
      // Initial discovery sync is allowed; ordinary long-poll is not started
      // until account recovery activation. Observe actual transport only.
      const inactive = await accountCall(page, 'getAccountView')
      const beforeActivationWatchCount = watchCount
      console.log(JSON.stringify({dkvsActivationWatchEvidence:{
        phase:'before-activation',watchCount:beforeActivationWatchCount,
        backupRisk:inactive.backup_risk,locatorPresent:Boolean(inactive.locator)}}))
      assert.equal(inactive.backup_risk,'notConfigured')
      assert.equal(Boolean(inactive.locator),false)
      assert.equal(beforeActivationWatchCount,0,'unactivated wallet started ordinary DKVS watch')
      await page.evaluate(async () => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        await store.updateWalletName(store.walletId, 'Local before cold start')
      })
      const localCatalog = await catalog(page)
      assert.equal(localCatalog[0].name,'Local before cold start')
      const localView = await accountCall(page,'getAccountView')
      assert.equal(localView.backup_state,'none')
      assert.equal(localView.backup_risk,'notConfigured')
      // A cold device retains its catalog. Editing requires a successful root check.
      await page.context().route(faultPath, failDiscovery)
      await page.reload(); await ready(page); await unlock(page)
      try {
        await expect.poll(async () => {
          const probe = await page.evaluate(() => ({...window.__dkvsEntryProbe}))
          const view = await accountCall(page, 'getAccountView')
          return faultHits > 0 && probe.rejected > 0 && probe.eventRejected > 0 &&
            view.discovery_status === 'unknown' && Boolean(view.error_code) &&
            view.backup_risk === 'notConfigured' && view.modules.every(module => !module.can_write)
        },{timeout:90000,message:'SDK must process failed cold discovery before the warning assertion'}).toBe(true)
      } finally {
        const probe = await page.evaluate(() => ({...window.__dkvsEntryProbe}))
        const view = await accountCall(page, 'getAccountView')
        console.log(JSON.stringify({coldDiscoveryWarning:{faultHits,...probe,state:view.state,
          backupRisk:view.backup_risk,errorCode:view.error_code,hasLocator:Boolean(view.locator),watchCount}}))
      }
      assert.deepEqual(await catalog(page), localCatalog, 'failed discovery must retain the local catalog')
      assert.equal(watchCount,0)
      assert.equal((await accountCall(page,'getAccountView')).discovery_status,'unknown')
      const warning = page.locator('aside').filter({hasText:'Remote backup has not been verified'})
      await expect(warning).toBeVisible()
      await expect(warning.getByRole('button',{name:'Restore account',exact:true})).toBeVisible()
      await expect(warning.getByRole('button',{name:accountMessages.readiness.retryNow,exact:true})).toHaveCount(0)
      await expect(warning.getByRole('button',{name:'Continue locally',exact:true})).toHaveCount(0)
      await page.evaluate(() => { location.hash = '#/wallet/manager' })
      const walletText = JSON.parse(readFileSync(new URL('../../locales/en.json', import.meta.url),'utf8')).walletManager
      await expect(page.getByRole('heading',{name:walletText.title,exact:true})).toBeVisible()
      await page.locator('div.font-medium').filter({hasText:localCatalog[0].name}).getByRole('button').click()
      const renameDialog = page.getByRole('dialog',{name:walletText.editWalletName,exact:true})
      await renameDialog.locator('#walletName').fill('Offline rename must be rejected')
      await renameDialog.getByRole('button',{name:walletText.save,exact:true}).click()
      await expect(page.getByText(/managed account discovery is pending/i).first()).toBeVisible()
      await expect(renameDialog).toBeVisible()
      assert.deepEqual(await catalog(page),localCatalog,'rejected rename must leave the durable catalog unchanged')
      assert.equal(watchCount,0,'failed discovery must not start a backup watch')
      await page.keyboard.press('Escape')
      await page.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
      await expect(page.locator('section').first()).toBeVisible()
      const retry = page.locator('section').first().getByRole('button',{
        name:accountMessages.readiness.retryNow,exact:true})
      await expect(retry).toHaveCount(0)
      await page.context().setOffline(true)
      await expect.poll(() => page.evaluate(() => navigator.onLine)).toBe(false)
      const baseline = await page.evaluate(() => ({...window.__dkvsEntryProbe}))
      assert.equal(faultHits,1,'the cold check must not poll while disconnected')
      await page.context().unroute(faultPath, failDiscovery)
      await page.context().setOffline(false)
      try {
        await expect.poll(() => page.evaluate(previous => {
          const p = window.__dkvsEntryProbe
          return p.completedStart > previous.started && p.eventStart > previous.started && p.events > previous.events
        },baseline),{timeout:90000,message:'reconnect must complete one SDK discovery and publish its result'}).toBe(true)
        assert.equal(await page.evaluate(() => window.__dkvsEntryProbe.started),baseline.started+1)
        await expect(retry).toHaveCount(0)
      } finally {
        const progress = await page.evaluate(() => ({...window.__dkvsEntryProbe}))
        console.log(JSON.stringify({dkvsActivationWatchEvidence:{phase:'reconnect',
          baselineStarted:baseline.started,baselineEvents:baseline.events,...progress,watchCount}}))
      }
      assert.equal(watchCount,0,'reconnect must not start an unactivated backup watch')
      const verifiedLocal = await accountCall(page,'getAccountView')
      assert.equal(verifiedLocal.error_code || '', '')
      assert.equal(verifiedLocal.discovery_status,'inactive')
      await expect(page.locator('aside').filter({hasText:'Remote backup has not been verified'})).toHaveCount(0)
      await page.evaluate(() => { location.hash = '#/wallet/manager' })
      await page.locator('div.font-medium').filter({hasText:localCatalog[0].name}).getByRole('button').click()
      await renameDialog.locator('#walletName').fill('Renamed after reconnect')
      await renameDialog.getByRole('button',{name:walletText.save,exact:true}).click()
      await expect(renameDialog).not.toBeVisible()
      assert.equal((await catalog(page))[0].name,'Renamed after reconnect')
      // Start a fresh SDK with the browser itself offline, including the node config request.
      await page.reload(); await ready(page)
      await page.context().setOffline(true)
      await unlock(page)
      await expect.poll(() => accountCall(page,'getAccountView'),{timeout:90000}).toMatchObject({
        discovery_status:'unknown', backup_risk:'notConfigured', error_code:expect.stringMatching(/.+/),
      })
      const offlineReads = await page.evaluate(() => window.__dkvsEntryProbe.started)
      await page.context().setOffline(false)
      await waitForAccountWrite(page,accountCall)
      assert.equal(await page.evaluate(() => window.__dkvsEntryProbe.started),offlineReads+1)
      assert.equal(watchCount,0)
      // Existing public SDK setup performs real temporary-storage activation;
      // this positive control does not synthesize profile or transport state.
      await setup(page)
      const activated = await accountCall(page,'getAccountView')
      assert.equal(activated.backup_state,'backed_up')
      assert.ok(activated.locator)
      await expect.poll(() => watchCount-beforeActivationWatchCount,{timeout:90000}).toBeGreaterThan(0)
      console.log(JSON.stringify({dkvsActivationWatchEvidence:{
        phase:'after-activation',watchCount,backupState:activated.backup_state,
        locatorPresent:Boolean(activated.locator)}}))
    } finally {
      await page.context().unroute(faultPath, failDiscovery)
      page.context().off('request', observeWatch)
      await page.evaluate(() => { window.__restoreEntryProbe?.(); window.restoreUsagePut?.() }).catch(() => {})
      await page.context().close()
    }
  })

  await scenario('usage: import page and cold selection use SDK without redundant PWA persistence', async () => {
    const source = await device()
    let importMnemonic, rootId
    try {
      importMnemonic = await createLocal(source)
      await waitForAccountWrite(source, accountCall)
      const view = await accountCall(source, 'getAccountView')
      assert.equal(view.discovery_status, 'inactive')
      assert.ok(view.account_id)
      rootId = view.account_id
    } finally {
      await source.context().close()
    }
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
      const submit = page.getByRole('button', { name: 'Import Wallet', exact: true })
      const fillImport = async () => {
        await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(importMnemonic)
        await page.getByLabel('New Wallet Password', { exact: true }).fill(password)
        await page.getByLabel('Confirm Password', { exact: true }).fill(password)
      }
      await fillImport()
      // Fresh empty device, real endpoint fault, no fabricated SDK response.
      // AccountView requires an unlocked session and is not a pre-import API.
      assert.deepEqual(await catalog(page), [])
      await page.evaluate(() => {
        const api = window.sat20wallet_wasm
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        const recover = api.recoverAccountManagementFromRootMnemonic
        const importWallet = api.importWallet
        const storeImport = store.importWallet
        const observed = {recoverCode:null,recoverStatus:null,importCalls:0,settled:0}
        window.__pendingImportObserved = observed
        api.recoverAccountManagementFromRootMnemonic = async (...args) => {
          const result = await recover(...args)
          observed.recoverCode = result?.code
          observed.recoverStatus = result?.data?.status
          return result
        }
        api.importWallet = async (...args) => { observed.importCalls++; return importWallet(...args) }
        store.importWallet = async (...args) => {
          try { return await storeImport(...args) } finally { observed.settled++ }
        }
        window.__restorePendingImport = () => {
          api.recoverAccountManagementFromRootMnemonic = recover
          api.importWallet = importWallet
          store.importWallet = storeImport
        }
      })
      const rootRoute = '**/dkvs/active/sync*'
      const observed = () => page.evaluate(() => ({...window.__pendingImportObserved}))
      const rootKey = `/personal/${rootId.toLowerCase()}/account/root`
      const attempts = []
      let response = 0
      let releaseRead
      const heldRead = new Promise(resolve => { releaseRead = resolve })
      const fault = async route => {
        const request = route.request()
        const keys = request.method() === 'POST' && request.postDataJSON()?.scope?.keys
        if (keys?.length !== 1 || keys[0] !== rootKey) return route.fallback()
        attempts.push({at:Date.now(),response})
        if (response === 0) {
          await heldRead
          return route.fallback()
        }
        if (response === 200) {
          // A healthy but slow node must be able to finish the same request.
          await new Promise(resolve => setTimeout(resolve, 6000))
          return route.fallback()
        }
        return route.fulfill({status:response, contentType:'text/plain', body:'unavailable',
          headers:{'access-control-allow-origin':'*'}})
      }
      await page.context().route(rootRoute, fault)
      try {
        await submit.click()
        await expect.poll(() => attempts.length, {timeout:90000}).toBeGreaterThan(0)
        await page.evaluate(() => { location.hash = '#/create' })
        await expect.poll(observed, {timeout:3000, message:'cancel the in-flight root read promptly'})
          .toMatchObject({settled:1,importCalls:0})
        releaseRead()
        const afterCancel = attempts.length
        await page.waitForTimeout(5500)
        assert.equal(attempts.length, afterCancel, 'leaving the import page must cancel the next read')
        assert.deepEqual(await catalog(page), [])

        await page.evaluate(() => { location.hash = '#/import' })
        await fillImport()
        response = 403
        await submit.click()
        await expect.poll(observed, {timeout:15000}).toMatchObject({settled:2,importCalls:0})
        assert.equal(attempts.length, afterCancel + 1, 'access denial must end this request')
        assert.deepEqual(await catalog(page), [])
        await expect(submit).toBeEnabled()

        response = 503
        const start = attempts.length
        await submit.click()
        await expect.poll(() => attempts.length > start ? attempts.at(-1).at - attempts[start].at : 0,
          {timeout:90000, message:'retry a temporary failure beyond the old 30s limit'}).toBeGreaterThan(30000)
        await expect(submit).toBeDisabled()
        assert.equal(new URL(page.url()).hash, '#/import')
        assert.deepEqual(await catalog(page), [])
        const pending = await observed()
        assert.equal(pending.settled, 2)
        assert.equal(pending.importCalls, 0)
        response = 200
        await expect.poll(() => new URL(page.url()).hash, {timeout:90000}).toBe('#/wallet')
        await expect.poll(observed).toMatchObject({recoverCode:0,recoverStatus:'not_found',settled:3,importCalls:1})
        const atCommit = attempts.length
        assert.equal(attempts.filter(item => item.response === 200).length, 1, 'accept the slow root response on its first attempt')
        await page.waitForTimeout(5500)
        assert.equal(attempts.length, atCommit, 'verified absence must stop foreground retries')
        console.log(JSON.stringify({pendingImportEvidence:{cancelReads:afterCancel,deniedReads:1,
          unavailableReads:attempts.slice(start).filter(item => item.response === 503).length,
          unavailableMs:attempts[atCommit-2].at-attempts[start].at,...await observed()}}))
      } finally {
        releaseRead()
        await page.context().unroute(rootRoute, fault)
        await page.evaluate(() => window.__restorePendingImport())
      }
      assert.equal(await page.evaluate(() => window.usageRedundantWrites), 0)
      await waitForAccountWrite(page, accountCall)
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
      assert.equal((await catalog(page))[0].accounts[0].account_id, rootId)
      const saved = await page.evaluate(async () => {
        const { Storage } = await import('/lib/storage-adapter.ts')
        const raw = (await Storage.get({ key: 'local:wallet_state_snapshot_v1' })).value
        return raw ? Object.keys(JSON.parse(raw).state) : []
      })
      for (const key of ['wallets','walletId','accountIndex','address','pubkey','locked','rootAccountId','hasWallet']) assert.equal(saved.includes(key), false)
      const holdCommittedImport = async (late, expectedWallets = 1) => {
        await late.evaluate(async () => {
          const sdk = (await import('/utils/sat20.ts')).default
          const submit = sdk.importWallet.bind(sdk)
          const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
          const importWallet = store.importWallet
          store.importWallet = async (...args) => {
            try { return await importWallet(...args) } finally { window.__importDone = true }
          }
          sdk.importWallet = async (...args) => {
            const result = await submit(...args)
            if (result[0] || !result[1]) throw new Error('SDK did not commit the wallet')
            await new Promise(resolve => { window.__deliverImport = resolve })
            return result
          }
          location.hash = '#/import'
        })
        await late.getByRole('textbox', {name:'Recovery Phrase',exact:true}).fill(importMnemonic)
        await late.getByLabel('New Wallet Password', {exact:true}).fill(password)
        await late.getByLabel('Confirm Password', {exact:true}).fill(password)
        await late.getByRole('button', {name:'Import Wallet',exact:true}).click()
        await late.waitForFunction(() => typeof window.__deliverImport === 'function', undefined, {timeout:90000})
        assert.equal((await catalog(late)).length, expectedWallets)
      }
      const edited = await device()
      try {
        await holdCommittedImport(edited)
        await edited.getByLabel('New Wallet Password', {exact:true}).fill(newPassword)
        await edited.evaluate(() => window.__deliverImport())
        await expect(edited.getByRole('button', {name:'Reload wallet',exact:true})).toBeVisible()
        await expect(edited.getByRole('button', {name:'Import Wallet',exact:true})).toBeDisabled()
      } finally { await edited.context().close() }
      for (const existing of [false, true]) {
        const late = existing ? await fresh() : await device()
        const expectedWallets = existing ? 2 : 1
        try {
          if (existing) await late.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().lockWallet())
          await holdCommittedImport(late, expectedWallets)
          await late.evaluate(async existing => {
            if (!existing) await window.__SAT20_PWA_VERIFY__.useWalletStore().lockWallet()
            location.hash = '#/unlock'
          }, existing)
          await late.waitForURL('**/#/unlock')
          await expect(late.getByRole('button', {name:'Import Wallet',exact:true})).toHaveCount(0)
          await late.evaluate(() => window.__deliverImport())
          await late.waitForFunction(() => window.__importDone === true)
          assert.equal(await late.evaluate(async () => (await import('/lib/walletSession.ts')).isWalletSessionUnlocked()), false)
          assert.equal(await late.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), true)
          assert.equal(new URL(late.url()).hash, '#/unlock')
          assert.equal((await catalog(late)).length, expectedWallets)
        } finally { await late.context().close() }
      }
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

  const assertWaitingStart = async page => {
    await expect(page.getByTestId('wallet-startup-shell')).toBeVisible()
    await expect(page.getByTestId('wallet-startup-shell')).toHaveAttribute('data-state', 'waiting')
    assert.equal(await page.evaluate(() => Boolean(window.__SAT20_PWA_VERIFY__)), false,
      'waiting page must not create the PWA Manager')
    assert.deepEqual(await page.evaluate(() => window.__walletStartProbe),
      { wasm: 0, dbOpen: 0, dbWrite: 0, storageWrite: 0 },
      'waiting page must not instantiate WASM, open IndexedDB or write wallet storage')
  }

  await scenario('usage: same-context PWA pages serialize Manager lifetime', async () => {
    const owner = await fresh()
    const context = owner.context()
    const before = await catalog(owner)
    try {
      const waiting = await waitingDevice(context)
      await assertWaitingStart(waiting)
      const focus = waiting.getByTestId('wallet-focus-existing')
      assert.equal(await focus.isEnabled(), true, 'same-origin focus request should be available')
      await focus.click()
      await expect(waiting.getByTestId('wallet-startup-status')).toHaveAttribute('data-state', 'focus-requested')

      await owner.evaluate(() => window.__SAT20_PWA_VERIFY__?.useWalletStore().lockWallet())
      await assertWaitingStart(waiting)
      await unlock(owner, password)

      const reinitialized = await owner.evaluate(async () => {
        const [releaseError] = await window.sat20.release()
        if (releaseError) throw releaseError
        let wasmCalls = 0
        let initCalls = 0
        const originalInstantiate = WebAssembly.instantiate
        const originalInit = window.sat20wallet_wasm.init
        WebAssembly.instantiate = function (...args) {
          wasmCalls++
          return Reflect.apply(originalInstantiate, this, args)
        }
        window.sat20wallet_wasm.init = function (...args) {
          initCalls++
          return Reflect.apply(originalInit, this, args)
        }
        try {
          const wasm = await import('/utils/wasm.ts')
          await Promise.all([wasm.loadWasm(), wasm.loadWasm(), wasm.loadWasm()])
          return { wasmCalls, initCalls }
        } finally {
          WebAssembly.instantiate = originalInstantiate
          window.sat20wallet_wasm.init = originalInit
        }
      })
      assert.deepEqual(reinitialized, { wasmCalls: 0, initCalls: 1 },
        'same-page release/reinit must reuse Go runtime and collapse repeated Manager init')
      await assertWaitingStart(waiting)

      await owner.close()
      await ready(waiting)
      await unlock(waiting, password)
      const probe = await waiting.evaluate(() => window.__walletStartProbe)
      assert.equal(probe.wasm, 1, 'the handoff page should instantiate one wallet WASM runtime')
      assert.ok(probe.dbOpen > 0, 'the handoff page should initialize the wallet database')
      assert.ok(probe.dbWrite > 0 || probe.storageWrite > 0,
        'the handoff page should complete real wallet initialization/unlock writes')
      assert.deepEqual(await catalog(waiting), before, 'handoff must reopen the same persisted wallet')
    } finally {
      await context.close().catch(() => {})
    }
  })

  await scenario('usage: refresh serializes PWA Manager startup', async () => {
    const owner = await fresh()
    const context = owner.context()
    try {
      await context.addInitScript(installWalletStartProbe)
      const waiting = await waitingDevice(context)
      await assertWaitingStart(waiting)

      await owner.reload({ waitUntil: 'domcontentloaded', timeout: 90000 })
      await owner.waitForFunction(() =>
        Boolean(window.__walletStartProbe) &&
        document.querySelector('[data-testid="wallet-startup-shell"]')?.getAttribute('data-state') === 'waiting' &&
        !window.__SAT20_PWA_VERIFY__,
      undefined, { timeout: 90000 })
      await assertWaitingStart(owner)

      await ready(waiting)
      await unlock(waiting, password)
      const first = await waiting.evaluate(() => window.__walletStartProbe)
      assert.equal(first.wasm, 1, 'the first queued page should create one wallet WASM runtime')
      assert.ok(first.dbOpen > 0, 'the first queued page should initialize the wallet database')

      await waiting.close()
      await ready(owner)
      await unlock(owner, password)
      const second = await owner.evaluate(() => window.__walletStartProbe)
      assert.equal(second.wasm, 1, 'the refreshed page should create one runtime only after handoff')
      assert.ok(second.dbOpen > 0, 'the refreshed page should complete database initialization')
    } finally {
      await context.close().catch(() => {})
    }
  })

  await scenario('usage: active Manager rejects a pending account write after same-page lock', () => using(async page => {
    const held = page.evaluate(async () => {
      const api = window.accountE2E
      const original = api.confirmStorage.bind(api)
      const { walletRequestSessionGuard } = await import('/lib/walletSession.ts')
      const checkSession = walletRequestSessionGuard('account.confirmStorage')
      window.releaseUsageRequest = null
      await new Promise(resolve => { window.releaseUsageRequest = resolve })
      let dispatched = false
      try {
        checkSession()
        dispatched = true
        await original((await api.getStorageOptions()).find(option => option.id === 'temporary'))
        return { rejected: false, dispatched }
      } catch {
        return { rejected: true, dispatched }
      }
    })
    await page.waitForFunction(() => typeof window.releaseUsageRequest === 'function')
    await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().lockWallet())
    await page.evaluate(() => window.releaseUsageRequest())
    assert.deepEqual(await held, { rejected: true, dispatched: false },
      'a request captured before lock must fail its session guard before dispatch')
    assert.equal(await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), true)
    await assert.rejects(accountCall(page, 'getStorageOptions'), /unlocked|session|wallet/i)
    assert.equal((await catalog(page)).length, 1)
  }))

  await scenario('usage: same-page subaccount switch discards a stale account response', () => using(async page => {
    await page.evaluate(async () => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      await store.addAccount('Same page savings', 1)
      await store.switchToAccount(0)
    })
    const held = page.evaluate(async () => {
      const { walletRequestSessionGuard } = await import('/lib/walletSession.ts')
      const checkSession = walletRequestSessionGuard('account.getAccountView')
      const previous = await window.accountE2E.getAccountView()
      await new Promise(resolve => { window.releaseUsageRequest = resolve })
      try { checkSession(); return { accepted: true, previous } }
      catch { return { accepted: false } }
    })
    await page.waitForFunction(() => typeof window.releaseUsageRequest === 'function')
    await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().switchToAccount(1))
    await page.evaluate(() => window.releaseUsageRequest())
    assert.equal((await held).accepted, false, 'old subaccount response was accepted after the same-page switch')
    const selected = await page.evaluate(() => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      return { index: store.accountIndex, address: store.address, pubKey: store.publicKey }
    })
    assert.equal(selected.index, 1)
    assert.equal(selected.address, (await walletCall(page, 'getWalletAddress', 1)).address)
    assert.equal(selected.pubKey, (await walletCall(page, 'getWalletPubkey', 1)).pubKey)
  }))

  await scenario('usage: committed wallet selection survives single Manager handoff', async () => {
    const owner = await fresh()
    const context = owner.context()
    try {
      const rootId = await owner.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().walletId)
      const childId = await owner.evaluate(async ({ credential, rootId }) => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        const [error] = await store.createWallet(credential)
        if (error) throw error
        const childId = store.walletId
        await store.switchWallet(rootId)
        return childId
      }, { credential: password, rootId })
      assert.notEqual(childId, rootId)
      const waiting = await waitingDevice(context)
      await assertWaitingStart(waiting)

      const switching = owner.evaluate(async id => {
        const api = window.sat20wallet_wasm
        const original = api.switchWallet
        api.switchWallet = async (...args) => {
          await new Promise(resolve => { window.releaseUsageWalletSwitch = resolve })
          return Reflect.apply(original, api, args)
        }
        try { await window.__SAT20_PWA_VERIFY__.useWalletStore().switchWallet(id) }
        finally { api.switchWallet = original }
      }, childId).then(() => null, error => error)
      try {
        await owner.waitForFunction(() => typeof window.releaseUsageWalletSwitch === 'function')
        await owner.evaluate(() => window.releaseUsageWalletSwitch())
        assert.equal(await switching, null, 'real SDK wallet switch failed')
        assert.equal(await owner.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().walletId), childId)
        await owner.close()

        await ready(waiting)
        const [error, result] = await waiting.evaluate(async credential => {
          const [error, result] = await window.__SAT20_PWA_VERIFY__.useWalletStore().unlockWallet(credential)
          return [error?.message, result]
        }, password)
        if (error) throw new Error(error)
        assert.equal(result.walletId, childId, 'handoff SDK Manager did not open committed selected wallet')
        assert.equal(await waiting.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().walletId), childId)
        assert.equal((await walletCall(waiting, 'getWalletAddress', 0)).address,
          await waiting.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().address))
      } finally {
        await owner.evaluate(() => window.releaseUsageWalletSwitch?.()).catch(() => {})
        await switching.catch(() => {})
      }
    } finally {
      await context.close().catch(() => {})
    }
  })

  await scenario('usage: password changes apply on single Manager handoff', async () => {
    const owner = await fresh()
    const context = owner.context()
    try {
      const before = await accountCall(owner, 'preflightRecovery', password, (await catalog(owner)).map(w => w.id))
      const waiting = await waitingDevice(context)
      await assertWaitingStart(waiting)
      await walletCall(owner, 'changePassword', password, newPassword)
      await owner.close()

      await ready(waiting)
      const acceptedOld = await walletCall(waiting, 'unlockWallet', password).then(() => true, () => false)
      await assert.rejects(walletCall(waiting, 'unlockWallet', password))
      await unlock(waiting, newPassword)
      assert.equal((await catalog(waiting)).length, 1)
      assert.equal(acceptedOld, false, 'handoff Manager authenticated with a replaced password')
      await assert.rejects(accountCall(waiting, 'preflightRecovery', password, (await catalog(waiting)).map(w => w.id)))
      assert.deepEqual(await accountCall(waiting, 'preflightRecovery', newPassword, (await catalog(waiting)).map(w => w.id)), before)
    } finally {
      await context.close().catch(() => {})
    }
  })

  assert.deepEqual(failures, [], 'PWA runtime regressions failed; see individual verdicts')
}
export { runChannelImportCase as runRgbPwaCases }
