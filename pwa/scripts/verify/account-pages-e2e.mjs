import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { expect } from '@playwright/test'

const messages = JSON.parse(readFileSync(new URL('../../locales/en.json', import.meta.url), 'utf8'))
const ui = messages.accountManagement.ui
const common = messages.common
export const accountPageCases = [
  'PWA account: existing recovery file rehearsal abort and cold restore',
  'PWA account: root-only backup activates with temporary storage',
  'PWA account: private-key cold unlock and activation refusal',
]
const poll = { timeout: 180000, intervals: [500, 1000, 2000] }
const credential = 'regtest-page-only-7304'
const answers = ['violet hill beside the station', 'yellow bicycle beside the river', 'meet under the tree at six']
const decode = file => Buffer.from(file.data, 'base64').toString('utf8')

export async function runAccountPageCases(t) {
  const raw = async (page, method, input = {}) => page.evaluate(async ({ method, input }) =>
    window.sat20account_wasm[method](JSON.stringify(input)), { method, input })
  const account = async (page, method, input) => {
    const result = await raw(page, method, input)
    assert.equal(result.code, 0, `${method}: ${result.msg}`)
    return result.data
  }
  const go = async (page, route) => page.evaluate(route => { location.hash = `#${route}` }, route)
  const section = (page, heading) => page.locator('section').filter({ has: page.getByRole('heading', { name: heading, exact: true }) })
  const button = (scope, name) => scope.getByRole('button', { name, exact: true })
  // Observe one actual UI dispatch. No response, balance, proof or SDK state is fabricated.
  const observe = async (page, method, action, success = true) => {
    await page.evaluate(method => {
      window.__pageResult = null
      const original = window.sat20account_wasm[method]
      window.sat20account_wasm[method] = async (...args) => {
        window.sat20account_wasm[method] = original
        const result = await original(...args)
        window.__pageResult = result
        return result
      }
    }, method)
    await action()
    await page.waitForFunction(() => window.__pageResult !== null, undefined, { timeout: 180000 })
    const result = await page.evaluate(() => { const result = window.__pageResult; delete window.__pageResult; return result })
    if (success) assert.equal(result.code, 0, `${method}: ${result.msg}`)
    else assert.notEqual(result.code, 0, `${method} must reject invalid input`)
    return result.data
  }
  const password = async (page, action) => {
    await action()
    const dialog = page.getByRole('dialog', { name: messages.walletPasswordPrompt.title, exact: true })
    await dialog.locator('input[type=password]').fill(credential)
    await dialog.locator('button[type=submit]').click()
    await expect(dialog).not.toBeVisible()
  }
  const fresh = async pages => {
    const page = await t.device(); pages.push(page)
    await page.evaluate(async credential => {
      const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(credential)
      if (error) throw error
    }, credential)
    await go(page, '/wallet/setting')
    await button(page, ui.activateAccount).click()
    await expect(button(section(page, ui.backupSetupTitle), ui.startBackupSetup)).toBeVisible()
    return page
  }
  const preflight = page => observe(page, 'preflightRecovery', () => password(page, () =>
    button(section(page, ui.backupSetupTitle), ui.startBackupSetup).click()))
  // Existing recovery files can still be imported. Create one through the SDK
  // as a fixture for the retained file-restore page.
  const createLegacy = async page => {
    const options = await account(page, 'getStorageOptions', { availability_only: true })
    await account(page, 'confirmStorage', options.find(option => option.id === 'temporary'))
    const catalog = await page.evaluate(() => window.sat20wallet_wasm.getWalletCatalog())
    assert.equal(catalog.code, 0)
    return account(page, 'createRecovery', {
      password: credential,
      wallet_ids: catalog.data.wallets.map(wallet => wallet.id),
      mode: '2of2',
      questions: answers.map((answer, i) => ({
        question: { id: 'fixture-' + i, prompt: 'Fixture recovery question ' + (i + 1) },
        answer, confirmation: answer,
      })),
    })
  }
  const legacyActivation = created => ({ session_id: created.session_id, input: {
    password: credential,
    recovery_file: decode(created.files.find(file => file.name === 'user-share.txt')),
    factors: { answers: created.questions.map((item, i) => ({ question_id: item.id, text: answers[i] })) },
  } })
  const load = async (page, created) => {
    await page.getByPlaceholder(ui.pasteRecoveryFile).fill(decode(created.files.find(file => file.name === 'user-share.txt')))
    return observe(page, 'loadRecovery', () => button(page, ui.loadBackup).click())
  }
  const factors = async page => {
    for (const [i, answer] of answers.entries()) await page.locator('section input[type=password]').nth(i).fill(answer)
    const view = await observe(page, 'previewRecovery', () => button(page, ui.previewRecovery).click())
    assert.equal(view.summary.wallets.length, 1)
    await expect(page.locator('section > div.rounded-lg')).toHaveCount(1)
  }
  const restore = async (page, created) => {
    await go(page, '/restore-account')
    await load(page, created)
    await factors(page)
    await page.locator('#restore-local-password').fill(credential)
    await page.locator('#restore-confirm-password').fill('different-password')
    await button(page, ui.restoreAll).click()
    await expect(page.getByText(ui.passwordMismatch, { exact: true })).toBeVisible()
    await page.locator('#restore-confirm-password').fill(credential)
    // Record only completeness/count across the production's automatic reload.
    await page.evaluate(() => {
      const original = window.sat20account_wasm.commitRecovery
      window.sat20account_wasm.commitRecovery = async (...args) => {
        const result = await original(...args)
        sessionStorage.setItem('page-restore-result', JSON.stringify({ code: result.code, complete: result.data?.complete }))
        return result
      }
    })
    const reloaded = page.waitForEvent('domcontentloaded', { timeout: 90000 })
    await button(page, ui.restoreAll).click()
    await page.waitForFunction(() => sessionStorage.getItem('page-restore-result') !== null)
    assert.deepEqual(await page.evaluate(() => JSON.parse(sessionStorage.getItem('page-restore-result'))), { code: 0, complete: true })
    await reloaded
    await page.waitForURL('**/#/unlock', { timeout: 90000 })
    await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__))
    await expect(page.locator('#restore-local-password')).toHaveCount(0)
    const restored = await page.evaluate(async credential => {
      const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
      const [error] = await store.unlockWallet(credential)
      if (error) throw error
      const catalog = await window.sat20wallet_wasm.getWalletCatalog()
      const current = await window.sat20wallet_wasm.getWalletAddress(store.accountIndex)
      if (catalog.code || current.code) throw new Error('restored wallet identity unavailable')
      return { locked: store.locked, address: store.address, current: current.data.address, wallets: catalog.data.wallets.length }
    }, credential)
    assert.equal(restored.locked, false)
    assert.equal(restored.address, restored.current)
    assert.equal(restored.wallets, 1)
    const view = await account(page, 'getAccountView')
    assert.equal(view.locator === created.locator, true)
    await page.evaluate(() => sessionStorage.removeItem('page-restore-result'))
  }
  const cases = [async pages => {
    const page = await fresh(pages)
    const discarded = await createLegacy(page)
    await account(page, 'abortRecovery', { session_id: discarded.session_id })
    const expired = await raw(page, 'activateRecovery', { session_id: discarded.session_id, input: {} })
    assert.notEqual(expired.code, 0)
    assert.equal(expired.msg, 'activation session expired')
    const created = await createLegacy(page)
    await account(page, 'activateRecovery', legacyActivation(created))
    await expect.poll(async () => (await account(page, 'getAccountView')).backup_state, poll).toBe('backed_up')
    await go(page, '/wallet')
    await go(page, '/wallet/setting/account-management')
    await button(page, ui.rehearsalTitle).click()
    await load(page, created)
    await observe(page, 'previewRecovery', () => button(page, ui.previewRecovery).click(), false)
    await expect(page.getByText(ui.insufficientShares, { exact: true })).toBeVisible()
    await expect(page.locator('#restore-local-password')).toHaveCount(0)
    for (const input of await page.locator('section input[type=password]').all()) await input.fill('incorrect factor')
    await observe(page, 'previewRecovery', () => button(page, ui.previewRecovery).click(), false)
    await expect(page.getByText(ui.recoveryFailed, { exact: true })).toBeVisible()
    await expect(page.locator('#restore-local-password')).toHaveCount(0)
    await factors(page)
    await expect(page.locator('#restore-local-password')).toHaveCount(0)
    await observe(page, 'abortRecovery', () => button(page, ui.finishRehearsal).click())
    await expect(page.getByRole('heading', { name: ui.managementTitle, exact: true })).toBeVisible()
    await button(page, common.back).click()
    await page.waitForURL('**/#/wallet')
    const target = await t.device(); pages.push(target)
    await go(target, '/restore-account')
    const cancelled = await load(target, created)
    await factors(target)
    assert.equal(await target.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), true)
    await observe(target, 'abortRecovery', () => button(target, ui.cancel).click())
    const expiredRestore = await raw(target, 'commitRecovery', { session_id: cancelled.session_id, new_password: credential })
    assert.notEqual(expiredRestore.code, 0)
    assert.equal(expiredRestore.msg, 'recovery session expired')
    const emptyCatalog = await target.evaluate(() => window.sat20wallet_wasm.getWalletCatalog())
    assert.equal(emptyCatalog.code, 0)
    assert.equal(emptyCatalog.data.wallets.length, 0)
    await restore(target, created)
    // Reconfigure an already healthy account through the existing SDK API.
    // Only this device's real wrapper CAS is held; no SDK response is fabricated.
    await page.context().close()
    // This case exercises online healthy reconfiguration: durable backup alone
    // does not establish this restored device's current online write readiness.
    await expect.poll(async () => {
      const view = await account(target, 'getAccountView')
      return view.state === 'ready' && view.backup_state === 'backed_up'
    }, poll).toBe(true)
    const baseline = await account(target, 'getAccountView')
    assert.equal(baseline.locator, created.locator)
    await go(target, '/wallet')
    await go(target, '/wallet/setting/account-management')
    const replacement = await createLegacy(target)
    assert.notEqual(replacement.locator, created.locator)
    const before = await target.evaluate(() => window.sat20wallet_wasm.getWalletCatalog())
    assert.equal(before.code, 0)
    const oldName = before.data.wallets[0].name
    const newName = 'Renamed during recovery update'
    let release
    const held = new Promise(resolve => { release = resolve })
    let hits = 0; let released = false
    const pattern = '**/v3/dkvs/records/batch-cas*'
    const holdWrapper = async route => {
      const body = route.request().postDataJSON()
      if (body?.mutations?.some(item => String(item.record?.Key || '').endsWith('/account/root'))) {
        hits++
        await held
      }
      await route.continue()
    }
    await target.context().route(pattern, holdWrapper)
    await target.evaluate(() => {
      const activate = window.sat20account_wasm.activateRecovery
      const rename = window.sat20wallet_wasm.updateWalletName
      window.__reconfigProbe = { started: false, done: false, code: null, renameCode: null }
      window.sat20account_wasm.activateRecovery = async (...args) => {
        window.__reconfigProbe.started = true
        try {
          const result = await activate(...args)
          window.__reconfigProbe.code = result.code
          return result
        } finally { window.__reconfigProbe.done = true }
      }
      window.sat20wallet_wasm.updateWalletName = async (...args) => {
        const result = await rename(...args)
        window.__reconfigProbe.renameCode = result.code
        return result
      }
      window.__restoreReconfig = () => {
        window.sat20account_wasm.activateRecovery = activate
        window.sat20wallet_wasm.updateWalletName = rename
      }
    })
    const activation = raw(target, 'activateRecovery', legacyActivation(replacement))
    try {
      await expect.poll(() => hits, poll).toBeGreaterThan(0)
      assert.equal(released, false)
      assert.equal(await target.evaluate(() => window.__reconfigProbe.done), false)
      await button(target, common.back).click()
      await target.waitForURL('**/#/wallet')
      await go(target, '/wallet/manager')
      await expect(target.getByRole('heading', { name: messages.walletManager.title, exact: true })).toBeVisible()
      await target.locator('div.font-medium').filter({ hasText: oldName }).getByRole('button').click()
      const dialog = target.getByRole('dialog', { name: messages.walletManager.editWalletName, exact: true })
      await dialog.locator('#walletName').fill(newName)
      await button(dialog, messages.walletManager.save).click()
      await target.waitForFunction(() => window.__reconfigProbe.renameCode !== null)
      const probe = await target.evaluate(() => window.__reconfigProbe)
      const after = await target.evaluate(() => window.sat20wallet_wasm.getWalletCatalog())
      console.log(JSON.stringify({ phase: 'reconfigure-local-write', casHits: hits, released,
        baselineBackedUp: baseline.backup_state === 'backed_up', locatorChanged: replacement.locator !== baseline.locator,
        renameCode: probe.renameCode, catalogCode: after.code, walletCount: after.data?.wallets?.length,
        nameUpdated: after.data?.wallets?.[0]?.name === newName }))
      assert.equal(probe.renameCode, 0, 'healthy baseline must permit local rename while reconfiguration CAS waits')
      assert.equal(after.code, 0)
      assert.equal(after.data.wallets[0].name, newName)
    } finally {
      released = true; release()
      try {
        if (await target.evaluate(() => window.__reconfigProbe.started)) {
          await target.waitForFunction(() => window.__reconfigProbe.done, undefined, { timeout: 180000 })
        }
      } finally {
        await target.context().unroute(pattern, holdWrapper)
        await target.evaluate(() => window.__restoreReconfig())
      }
    }
    assert.equal((await activation).code, 0)
    await expect.poll(async () => (await account(target, 'getAccountView')).backup_state, poll).toBe('backed_up')
    await target.reload()
    await target.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__))
    await target.waitForURL('**/#/unlock')
    await target.locator('form input[type=password]').fill(credential)
    await target.locator('form button[type=submit]').click()
    await target.waitForURL('**/#/wallet')
    const persisted = await target.evaluate(() => window.sat20wallet_wasm.getWalletCatalog())
    assert.equal(persisted.code, 0)
    assert.equal(persisted.data.wallets[0].name, newName)
    assert.equal((await account(target, 'getAccountView')).locator, replacement.locator)
  }, async pages => {
    const page = await fresh(pages)
    await expect(section(page, ui.guardianService)).toHaveCount(0)
    await preflight(page)
    const scope = section(page, ui.backupSetupTitle)
    const available = await observe(page, 'getStorageOptions', () => button(scope, common.refresh).click())
    assert.deepEqual(available.map(option => [option.id, option.available, option.payment]), [['temporary', true, null]])
    await expect(scope.locator('input[type=number]')).toHaveCount(0)
    await expect(button(page, ui.upgradeStorage)).toHaveCount(0)
    await expect(scope.locator('select')).toHaveCount(0)
    await expect(scope.getByPlaceholder(ui.answer, { exact: true })).toHaveCount(0)
    for (const method of ['getGuardianContact', 'acceptGuardianSetup', 'confirmGuardianSetup',
      'createGuardianRequest', 'createGuardianResponse']) {
      const result = await raw(page, method, { password: credential })
      assert.notEqual(result.code, 0)
      assert.equal(result.msg, 'guardian recovery is disabled')
    }
    const view = await observe(page, 'activateRootAccount', () => password(page, () => button(scope, ui.enableBackup).click()))
    assert.equal(view.locator, '')
    assert.equal(view.recovery_mode, '')
    assert.equal(view.last_rehearsal_at, 0)
    await expect.poll(async () => (await account(page, 'getAccountView')).backup_state, poll).toBe('backed_up')
    await expect(page.getByText(ui.rootRestoreHint, { exact: true })).toBeVisible()
  }, async pages => {
    const page = await t.device(); pages.push(page)
    // The PWA has no private-key import tab. Prepare this supported SDK wallet,
    // then use the actual cold unlock and account-management forms.
    const imported = await page.evaluate(async credential =>
      window.sat20wallet_wasm.importWalletWithPrivKey('1'.padStart(64, '0'), credential), credential)
    assert.equal(imported.code, 0)
    const coldUnlock = async () => {
      await page.reload()
      await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__))
      await page.waitForURL('**/#/unlock')
      await page.locator('form input[type=password]').fill(credential)
      await page.locator('form button[type=submit]').click()
      await page.waitForURL('**/#/wallet')
    }
    await coldUnlock()
    const before = await page.evaluate(() => window.sat20wallet_wasm.getWalletCatalog())
    assert.equal(before.code, 0)
    assert.equal(before.data.wallets.length, 1)
    assert.equal(before.data.root_account_id, '')
    await go(page, '/wallet/setting/account-management')
    await expect(page.getByRole('alert').filter({ hasText: ui.mnemonicRootRequired })).toHaveText(ui.mnemonicRootRequired)
    await expect(button(page, ui.startBackupSetup)).toHaveCount(0)
    await expect(button(page, ui.enableBackup)).toHaveCount(0)
    const refused = await raw(page, 'preflightRecovery', { password: credential, wallet_ids: before.data.wallets.map(item => item.id) })
    assert.notEqual(refused.code, 0)
    await expect(section(page, ui.backupSetupTitle)).toHaveCount(0)
    assert.equal((await account(page, 'getAccountView')).active, false)
    assert.deepEqual(await page.evaluate(() => window.sat20wallet_wasm.getWalletCatalog()), before)
    await go(page, '/wallet')
    await coldUnlock()
    assert.deepEqual(await page.evaluate(() => window.sat20wallet_wasm.getWalletCatalog()), before)
    assert.equal(await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().address), imported.data.address)
  }]
  for (const [i, run] of cases.entries()) {
    if (t.selectedCases && !t.selectedCases.includes(accountPageCases[i])) continue
    const pages = []
    try { await t.check(accountPageCases[i], () => run(pages)) }
    catch (error) { await t.preserveBrowserFailureState?.(); throw error }
    finally { for (const page of pages) if (!page.isClosed()) await page.context().close() }
  }
}
