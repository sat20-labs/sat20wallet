import assert from 'node:assert/strict'
import { appendFileSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { expect } from '@playwright/test'

const accountMessages = JSON.parse(readFileSync(new URL('../../locales/en.json', import.meta.url), 'utf8')).accountManagement

function dkvsFundingButton(page, quote) {
  assert.equal(quote.can_fund, true, 'current DKVS quote cannot be funded')
  return page.getByRole('alert', { name: accountMessages.ui.backupStatusLabel, exact: true }).getByRole('button', {
    name: quote.ready ? /^(Renew DKVS storage|续费 DKVS 存储)$/ : /^(Pay DKVS storage fees|支付 DKVS 存储费用)$/,
  })
}

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

export const fixedRegtestAccountUsageCaseNames = [
  'usage: fixed regtest ordinary-1 adds selects renames subaccount and preserves root identity',
  'usage: fixed regtest ordinary-1 renews paid storage through current AUTOPAY',
]

export const regtestAccountUsageCaseNames = [
  ...fixedRegtestAccountUsageCaseNames,
  'usage: real 2of3 settings and independent Guardian pages complete setup and rehearsal',
]

// Public BIP39 test vectors; browser wallet configuration supplies the actual
// regtest chain parameters and derives both identities for this fresh group.
const guardianRecoverySyntheticMnemonics = {
  owner: 'abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about',
  guardian: 'legal winner thank year wave sausage worth useful legal winner thank yellow',
}

export function requireFixedAccountManagementOwner(fixture) {
  const owner = fixture?.owner
  assert.ok(typeof owner?.mnemonic === 'string' && owner.mnemonic.trim() &&
    typeof owner?.password === 'string' && owner.password,
  'fixed ordinary-1 account-management owner fixture requires mnemonic and password')
  assert.ok(owner.role === undefined || owner.role === 'ordinary-1',
    'fixed account-management owner role must be ordinary-1')
  for (const field of ['address', 'fingerprint']) {
    assert.ok(owner[field] === undefined || (typeof owner[field] === 'string' && owner[field]),
      `fixed ordinary-1 owner ${field} must be a non-empty string when provided`)
  }
  return {
    mnemonic: owner.mnemonic,
    password: owner.password,
    role: owner.role,
    address: owner.address,
    fingerprint: owner.fingerprint,
  }
}

export function assertFixedOwnerMnemonicIdentity(owner, derived) {
  assert.ok(typeof derived?.fingerprint === 'string' && derived.fingerprint &&
    typeof derived?.address === 'string' && derived.address,
  'fixed ordinary-1 mnemonic must derive a fingerprint and address')
  if (owner?.address !== undefined) {
    assert.equal(derived.address, owner.address, 'fixed ordinary-1 mnemonic does not match fixture address')
  }
  if (owner?.fingerprint !== undefined) {
    assert.equal(derived.fingerprint, owner.fingerprint, 'fixed ordinary-1 mnemonic does not match fixture fingerprint')
  }
}

export function assertFixedRootIdentity(expected, observed) {
  assert.ok(typeof expected?.fingerprint === 'string' && expected.fingerprint &&
    typeof expected?.address === 'string' && expected.address,
  'fixed-root expected identity must include fingerprint and address')
  assert.ok(typeof observed?.fingerprint === 'string' && observed.fingerprint &&
    typeof observed?.address === 'string' && observed.address,
  'fixed-root observed identity must include fingerprint and address')
  assert.equal(observed.fingerprint, expected.fingerprint, 'fixed root fingerprint changed')
  assert.equal(observed.address, expected.address, 'fixed root address changed')
}

// Discovery, write eligibility and a recoverable first backup are separate
// facts. Counters alone never prove that another device can restore the backup.
function accountReadiness(status) {
  const knownDiscovery = ['inactive', 'active', 'active-waiting-backup'].includes(status?.discovery_status)
  return {
    discovery: knownDiscovery ? status.discovery_status : 'unresolved',
    writeQualified: status?.recovery_configured === true && status?.activation_pending === false &&
      status?.dkvs_write_allowed === true,
  }
}

export async function waitForAccountWrite(page, accountCall) {
  let observed
  try {
    await expect.poll(async () => {
      const status = await accountCall(page, 'status')
      observed = Object.fromEntries(['discovery_status', 'local_only', 'recovery_configured',
        'activation_pending', 'dkvs_write_allowed', 'account_state'].map(key => [key, status[key]]))
      return accountReadiness(status).writeQualified ||
        (status.discovery_status === 'inactive' && status.local_only === true &&
          status.recovery_configured === false && !status.activation_pending)
    }, { timeout: 90000, message: 'business preparation requires a verified account write qualification' }).toBe(true)
  } catch (error) {
    console.error(JSON.stringify({ accountWriteWaitFailure: observed }))
    throw error
  }
}

async function assertBackupPreview(page, expectedCatalog) {
  const heading = page.getByRole('heading', { name: accountMessages.ui.confirmRecovery, exact: true })
  const cards = page.locator('section').filter({ has: heading }).locator(':scope > div.rounded-lg')
  let phase = 'heading'
  let walletIndex = -1
  let matchingCards = 0
  try {
    await expect(heading).toBeVisible()
    phase = 'wallet-count'
    await expect(cards).toHaveCount(expectedCatalog.length)
    for (const [index, wallet] of expectedCatalog.entries()) {
      walletIndex = index
      const card = cards.filter({ has: page.getByText(wallet.name, { exact: true }) })
      phase = 'wallet-name'
      matchingCards = await card.count()
      await expect(card).toHaveCount(1)
      await expect(card.getByText(wallet.name, { exact: true })).toBeVisible()
      phase = 'account-count'
      await expect(card.getByText(`Subaccounts: ${wallet.accounts.length}`, { exact: true })).toBeVisible()
    }
    phase = 'password-inputs'
    await expect(page.getByLabel(accountMessages.ui.newDevicePassword, { exact: true })).toBeVisible()
    await expect(page.getByLabel(accountMessages.ui.repeatUnlockPassword, { exact: true })).toBeVisible()
  } catch (error) {
    console.error(JSON.stringify({ backupPreviewFailure: { phase, wallet_index: walletIndex,
      expected_cards: expectedCatalog.length, actual_cards: await cards.count(), matching_cards: matchingCards } }))
    throw error
  }
}

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
    await waitForAccountWrite(page, accountCall)
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
  const { check, device, waitingDevice, ready, unlock, catalog, walletCall, accountCall, password, newPassword, questions, answers } = t
  const importMnemonic = t.rootMnemonic || 'abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about'
  const fixedOwner = t.fixedAccountManagement == null
    ? null
    : requireFixedAccountManagementOwner(t.fixedAccountManagement)
  const failures = []
  const scenario = async (name, action) => {
    try { await check(name, action) } catch (error) { failures.push({ name, message: error.message }) }
  }
  const fresh = async () => {
    const page = await device()
    let readiness
    try {
      await page.evaluate(async credential => {
        const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(credential)
        if (error) throw error
      }, password)
      // This helper creates a new random first wallet. Its local identity is
      // not an activation decision; wait for verified online absence first.
      await expect.poll(async () => {
        const status = await accountCall(page, 'status')
        readiness = {
          discovery_status: status.discovery_status,
          dkvs_status: status.dkvs_status,
          local_only: status.local_only,
          recovery_configured: status.recovery_configured,
          activation_pending: status.activation_pending,
          dkvs_write_allowed: status.dkvs_write_allowed,
        }
        const qualification = accountReadiness(status)
        if (status.recovery_configured || status.activation_pending ||
          qualification.discovery === 'active' || qualification.discovery === 'active-waiting-backup') {
          throw new Error('new random first wallet unexpectedly has an active or pending service')
        }
        return qualification.discovery === 'inactive' && status.local_only === true &&
          status.recovery_configured === false && !status.activation_pending
      }, { timeout: 90000, message: 'new random first wallet requires real inactive discovery before local business' }).toBe(true)
      return page
    } catch (error) {
      console.error(JSON.stringify({ freshReadinessFailure: readiness ?? { status: 'not observed' } }))
      await t.preserveBrowserFailureState?.()
      await page.context().close().catch(() => {})
      throw error
    }
  }
  const freshSynthetic = async mnemonic => {
    const page = await device()
    try {
      const expectedIdentity = await walletCall(page, 'validateMnemonic', mnemonic, '')
      assert.ok(expectedIdentity?.fingerprint && expectedIdentity?.address,
        'public synthetic mnemonic must derive a configured-chain identity')
      const importError = await page.evaluate(async ({ mnemonic: phrase, credential }) => {
        const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().importWallet(phrase, credential)
        return error?.message
      }, { mnemonic, credential: password })
      assert.equal(importError, undefined, 'PWA import of the public synthetic identity failed')
      const rootIdentity = root => ({ fingerprint: root.fingerprint, address: root.accounts?.[0]?.address })
      const assertImportedRoot = async phase => {
        const roots = await catalog(page)
        assert.equal(roots.length, 1, `${phase}: expected one synthetic root, not a newly generated wallet`)
        assertFixedRootIdentity(expectedIdentity, rootIdentity(roots[0]))
      }
      await assertImportedRoot('initial import')
      await page.reload(); await ready(page); await unlock(page, password)
      await assertImportedRoot('reload')
      return page
    } catch (error) {
      await t.preserveBrowserFailureState?.()
      await page.context().close().catch(() => {})
      throw error
    }
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
  const usingSynthetic = async (mnemonic, action) => {
    const page = await freshSynthetic(mnemonic)
    try { await action(page) } catch (error) {
      await t.preserveBrowserFailureState?.()
      const view = await page.evaluate(() => ({
        headings: [...document.querySelectorAll('h1,h2')].map(element => element.textContent),
        alerts: [...document.querySelectorAll('[role="alert"]')].map(element => element.textContent),
      })).catch(() => null)
      console.error(JSON.stringify({ syntheticIdentityFailureView: view, url: page.url() }))
      throw error
    } finally { await page.context().close() }
  }
  const usingGuardianOwner = action => t.regtestMode
    ? usingSynthetic(guardianRecoverySyntheticMnemonics.owner, action)
    : using(action)
  const freshGuardian = () => t.regtestMode
    ? freshSynthetic(guardianRecoverySyntheticMnemonics.guardian)
    : fresh()
  const setup = async page => {
    const before = (await catalog(page)).map(({ fingerprint, name, accounts }) => ({ fingerprint, name, accounts }))
    assert.ok(before.length > 0, 'local account catalog is empty before recovery setup')
    await page.reload(); await ready(page); await unlock(page, password)
    assert.deepEqual((await catalog(page)).map(({ fingerprint, name, accounts }) => ({ fingerprint, name, accounts })), before,
      'unactivated account catalog was not persisted locally')
    // Cold unlock starts real discovery again; do not treat its initial status
    // as the confirmed absence required by this unactivated setup.
    await waitForAccountWrite(page, accountCall)
    const local = await accountCall(page, 'status')
    assert.equal(local.local_only, true, 'unactivated account must remain local-only')
    assert.equal(local.recovery_configured, false)
    await accountCall(page, 'confirmStorage', 'temporary')
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
    await page.getByRole('heading', { name: accountMessages.ui.managementTitle, exact: true }).waitFor()
  }
  const usingFixedOwner = async action => {
    assert.ok(fixedOwner, 'fixed-root cases require the fixed ordinary-1 owner fixture')
    const page = await device()
    try {
      const expectedIdentity = await walletCall(page, 'validateMnemonic', fixedOwner.mnemonic, '')
      assertFixedOwnerMnemonicIdentity(fixedOwner, expectedIdentity)
      const importError = await page.evaluate(async ({ mnemonic, credential }) => {
        const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().importWallet(mnemonic, credential)
        return error?.message
      }, { mnemonic: fixedOwner.mnemonic, credential: fixedOwner.password })
      assert.equal(importError, undefined, 'PWA import of the fixed ordinary-1 root failed')

      const imported = await catalog(page)
      assert.equal(imported.length, 1, 'fixed fixture import must open one existing root, not create a random root')
      const rootIdentity = root => ({ fingerprint: root.fingerprint, address: root.accounts?.[0]?.address })
      assertFixedRootIdentity(expectedIdentity, rootIdentity(imported[0]))

      await page.reload(); await ready(page); await unlock(page, fixedOwner.password)
      const restored = await catalog(page)
      assert.equal(restored.length, 1, 'fixed ordinary-1 root changed across reload')
      assertFixedRootIdentity(expectedIdentity, rootIdentity(restored[0]))
      await action(page, { expectedIdentity, root: restored[0] })
    } catch (error) {
      await t.preserveBrowserFailureState?.()
      let diagnosticTimer
      const view = await Promise.race([
        page.evaluate(() => ({
          headings: [...document.querySelectorAll('h1,h2')].map(element => element.textContent),
          alerts: [...document.querySelectorAll('[role="alert"]')].map(element => element.textContent),
        })).catch(() => null),
        new Promise(resolve => { diagnosticTimer = setTimeout(() => resolve(null), 2000) }),
      ]).finally(() => clearTimeout(diagnosticTimer))
      console.error(JSON.stringify({ fixedOwnerFailureView: view, url: page.url() }))
      throw error
    } finally { await page.context().close() }
  }
  const confirmPassword = async (page, credential = password) => {
    const dialog = page.getByRole('dialog', { name: 'Confirm wallet password', exact: true })
    await dialog.locator('input[type="password"]').fill(credential)
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await dialog.waitFor({ state: 'hidden' })
  }
  const preflightUI = async page => {
    await settings(page)
    await page.getByRole('button', { name: accountMessages.ui.checkAccount, exact: true }).click()
    const dialog = page.getByRole('dialog')
    await dialog.locator('input[type="password"]').fill('incorrect')
    await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
    await dialog.getByRole('alert').waitFor()
    await confirmPassword(page)
    await page.getByRole('button', { name: accountMessages.ui.confirmStorage, exact: true }).waitFor()
  }
  const temporaryUI = async page => {
    const options = (await accountCall(page, 'getStorageOptions')).options
    assert.ok(options.some(option => option.id === 'temporary' && option.available))
    await page.getByRole('button', { name: accountMessages.ui.temporaryStorage }).click()
    await page.getByRole('button', { name: accountMessages.ui.confirmStorage, exact: true }).click()
    await page.getByRole('button', { name: accountMessages.ui.createBackup, exact: true }).waitFor()
  }
  const fillQuestions = async page => {
    for (let i = 0; i < 3; i++) {
      await page.getByPlaceholder(accountMessages.ui.answer, { exact: true }).nth(i).fill(questions[i].answer)
      await page.getByPlaceholder(accountMessages.ui.repeatAnswer, { exact: true }).nth(i).fill(questions[i].confirmation)
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

  const paidQuoteCase = 'usage: Guardian paid confirmation uses 100 records and root identity after a 200 record child-wallet quote'
  if (!t.material?.guardian_setup && t.focusedUsageCases?.includes(paidQuoteCase)) {
    const owner = await fresh()
    const guardian = await freshGuardian()
    try {
      await settings(guardian)
      await guardian.getByRole('button', { name: accountMessages.ui.generateContact, exact: true }).click()
      await confirmPassword(guardian)
      const guardianTools = guardian.getByRole('heading', { name: accountMessages.ui.guardianTools, exact: true }).locator('xpath=..')
      const copyContact = guardianTools.getByRole('button', { name: accountMessages.ui.copyContact, exact: true })
      await expect(copyContact).toBeVisible()
      const contactOutput = copyContact.locator('xpath=..').locator('textarea[readonly]')
      await expect(contactOutput).toHaveCount(1)
      const contact = JSON.parse(await contactOutput.inputValue())
      assert.ok(typeof contact.network === 'string' && contact.network.length > 0,
        'Guardian contact must include its network')
      assert.match(contact.mailbox_id, /^[0-9a-f]{64}$/i,
        'Guardian contact must include a 32-byte mailbox id')
      assert.ok(typeof contact.recovery_public_key === 'string' && contact.recovery_public_key.length > 0,
        'Guardian contact must include its recovery public key')
      const ownerStatus = await accountCall(owner, 'status')
      const ownerReadiness = accountReadiness(ownerStatus)
      assert.equal(ownerReadiness.discovery, 'inactive', 'Guardian quote material owner must be freshly discovered as inactive')
      assert.equal(ownerReadiness.writeQualified, false, 'Guardian quote prep must not grant write eligibility')
      assert.equal(ownerStatus.recovery_configured, false, 'Guardian quote prep must not pre-activate its owner')
      assert.equal(ownerStatus.activation_pending, false)
      assert.notEqual(contact.mailbox_id, ownerStatus.account_id, 'quote material must use an independent Guardian')
      await preflightUI(owner)
      await temporaryUI(owner)
      await fillQuestions(owner)
      const material = await accountCall(owner, 'createRecovery', {
        password, wallets: [], recovery_mode: '2of3', questions, guardian: contact,
      })
      assert.ok(typeof material.guardian_setup === 'string' && material.guardian_setup.length,
        'real PWA setup must produce Guardian material for the focused quote case')
      t.material = { ...(t.material || {}), guardian_setup: material.guardian_setup }
      await accountCall(owner, 'cancelPendingStorageAuthorization')
    } finally {
      await Promise.all([owner.context().close(), guardian.context().close()])
    }
  }

  if (!t.material && t.focusedUsageCases?.includes(
    'usage: offline catalog is read-only and resumes synchronization after reconnect')) {
    const source = await fresh()
    try { t.material = await setup(source) }
    finally { await source.context().close() }
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
    await settings(page)
    await expect(page.getByRole('textbox', { name: 'Saved DID for subaccount 0', exact: true })).toHaveValue('bob.btc')
    assert.equal(await page.getByRole('textbox', { name: 'Saved DID for subaccount 0', exact: true }).getAttribute('readonly'), '')
    const material = await setup(page)
    const restored = await device()
    try {
      await restore(restored, material)
      assert.equal((await catalog(restored))[0].accounts[0].did, 'bob.btc')
      assert.equal((await catalog(restored))[0].accounts[0].name, before[0].accounts[0].name)
      await expect.poll(async () => {
        const status = await accountCall(restored, 'status')
        return status.recovery_configured && status.dkvs_write_allowed
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

  await scenario('usage: remote deletion rejects offline edits and adopts the confirmed remote state', () => using(async page => {
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
      await expect.poll(async () => {
        const statuses = await Promise.all([page, replica].map(p => accountCall(p, 'status')))
        return statuses.every(status => status.recovery_configured && status.dkvs_write_allowed)
      }, { timeout: 90000 }).toBe(true)
      const before = await catalog(page)
      const rootID = (await accountCall(page, 'status')).root_wallet_id
      const child = before.find(wallet => String(wallet.id) !== String(rootID))
      assert.ok(child)
      const replicaChild = (await catalog(replica)).find(wallet => wallet.fingerprint === child.fingerprint)
      assert.ok(replicaChild)
      const localBefore = await catalog(replica)
      await replica.evaluate(async childID => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        await store.switchWallet(childID)
      }, String(replicaChild.id))
      await expect.poll(async () => (await accountCall(replica, 'status')).dkvs_write_allowed,
        { timeout: 90000 }).toBe(true)
      // Offline rejection is measured from a confirmed, clean baseline;
      // write eligibility alone also permits normal asynchronous backup.
      await settled(replica)
      assert.equal((await accountCall(replica, 'status')).pending_changes ?? 0, 0,
        'replica preparation left pending changes before the offline boundary')
      for (const route of routes) await replica.context().route(route, unavailable)
      let offlineQualification
      try {
        await expect.poll(async () => {
          const status = await accountCall(replica, 'status')
          offlineQualification = { account_state: status.account_state,
            write_allowed: status.dkvs_write_allowed, error_code: status.last_dkvs_sync_error_code || '' }
          return Boolean(status.last_dkvs_sync_error) && !status.dkvs_write_allowed
        }, { timeout: 90000, message: 'offline edits require the actual write gate to close' }).toBe(true)
      } catch (error) {
        console.error(JSON.stringify({ offlineQualificationFailure: offlineQualification }))
        throw error
      }
      const rejected = await replica.evaluate(async ({ childID, rootID }) => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        const attempt = async operation => {
          try {
            const result = await operation()
            const error = Array.isArray(result) ? result[0] : null
            return error ? String(error.message || error) : ''
          } catch (error) { return String(error?.message || error) }
        }
        return {
          childName: await attempt(() => store.updateWalletName(childID, 'Forbidden offline child')),
          childAccount: await attempt(() => store.addAccount('Forbidden offline account', 1)),
          childDID: await attempt(() => store.updateAccountDID(0, 'forbidden.btc')),
          rootName: await attempt(() => store.updateWalletName(rootID, 'Forbidden offline root')),
        }
      }, { childID: String(replicaChild.id), rootID: String((await accountCall(replica, 'status')).root_wallet_id) })
      for (const [operation, error] of Object.entries(rejected)) {
        assert.match(error, /DKVS|synchroniz|offline|同步|离线/i,
          `offline ${operation} did not reject at the synchronization boundary`)
      }
      assert.deepEqual(await catalog(replica), localBefore)
      assert.equal((await accountCall(replica, 'status')).pending_changes ?? 0, 0,
        'rejected offline edits created pending changes')
      await replica.reload(); await ready(replica); await unlock(replica)
      assert.deepEqual(await catalog(replica), localBefore, 'offline reload changed confirmed account data')
      assert.equal((await accountCall(replica, 'status')).pending_changes ?? 0, 0,
        'offline reload changed the confirmed backup baseline')
      await page.evaluate(async ({ childID, rootID }) => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        const [error] = await store.deleteWallet(childID)
        if (error) throw error
        await store.switchWallet(rootID)
      }, { childID: String(child.id), rootID: String(rootID) })
      // Removing a wallet changes DKVS registrations. Wait for the online
      // device's write gate before making its independent root-account edit.
      await waitForAccountWrite(page, accountCall)
      await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore()
        .updateAccountName(0, 'Remote independent account'))
      await settled(page)
      for (const route of routes) await replica.context().unroute(route, unavailable)
      await settled(replica)
      const semantic = value => value.map(wallet => ({ fingerprint: wallet.fingerprint, name: wallet.name,
        accounts: wallet.accounts.map(account => ({ index: account.index, name: account.name, did: account.did || '' })) }))
      const remote = semantic(await catalog(page))
      await expect.poll(async () => semantic(await catalog(replica)), { timeout: 90000 }).toEqual(remote)
      const final = await catalog(replica)
      assert.equal(final.length, 1)
      assert.equal(final[0].name, before.find(wallet => String(wallet.id) === String(rootID)).name)
      assert.equal(final[0].accounts[0].name, 'Remote independent account')
      assert.equal(final[0].accounts[0].did || '', before.find(wallet => String(wallet.id) === String(rootID)).accounts[0].did || '')
      assert.equal(final.some(wallet => wallet.name.startsWith('Forbidden offline')), false)
      assert.equal(final.some(wallet => wallet.fingerprint === child.fingerprint), false)
      await replica.reload(); await ready(replica); await unlock(replica)
      assert.deepEqual(semantic(await catalog(replica)), remote,
        'cold reload lost the persisted remote deletion before synchronization')
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
      await waitForAccountWrite(replica, accountCall)
      await settled(replica)
      assert.equal((await accountCall(replica, 'status')).pending_changes || 0, 0,
        'online reload has not reconciled the remote deletion baseline')
    } finally {
      for (const route of routes) await replica.context().unroute(route, unavailable)
      await replica.context().close()
    }
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
    await page.getByRole('button', { name: accountMessages.ui.rehearsalTitle, exact: true }).click()
    await page.getByRole('button', { name: accountMessages.ui.loadBackup, exact: true }).click()
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
    await expect(page.getByPlaceholder(accountMessages.ui.pasteOptionalShare)).toHaveCount(0)
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
    await page.getByRole('heading', { name: accountMessages.ui.maintainRecovery, exact: true }).waitFor()
    await page.bringToFront()
    const card = page.getByRole('alert', { name: accountMessages.ui.backupStatusLabel, exact: true })
    await expect(card).toContainText('Data storage: Service node temporary cache')
    await expect(card).toContainText('Pending changes: 0')
    // Pending can finish before the background worker clears an earlier CAS
    // error. Establish a healthy baseline before injecting this outage.
    await expect.poll(async () => (await accountCall(page, 'status')).last_dkvs_sync_error || '', { timeout: 90000 }).toBe('')
    await expect(card).not.toContainText(accountMessages.readiness.error, { timeout: 45000 })
    await expect.poll(async () => {
      const status = await accountCall(page, 'status')
      return status.recovery_configured && !status.activation_pending && status.dkvs_write_allowed &&
        (status.pending_changes ?? 0) === 0 && !status.managed_data_dirty &&
        (status.dkvs_pending_requests ?? 0) === 0
    }, { timeout: 90000 }).toBe(true)
    const baseline = await accountCall(page, 'status')
    assert.equal(baseline.dkvs_write_allowed, true)
    assert.equal(baseline.pending_changes ?? 0, 0)
    assert.equal(baseline.managed_data_dirty ?? false, false)
    assert.equal(baseline.dkvs_pending_requests ?? 0, 0)
    console.log(JSON.stringify({ backgroundBackupGate: 'ready', write_allowed: true,
      pending_changes: 0, managed_data_dirty: false, pending_requests: 0 }))
    const location = page.url()
    const stateKey = `/personal/${baseline.account_id}/account/state`
    let failBackup = true
    let blocked = false
    let releaseWrite
    const writeGate = new Promise(resolve => { releaseWrite = resolve })
    const heldWrites = []
    const unavailable = async route => {
      const request = route.request()
      if (request.method() !== 'POST' || !new URL(request.url()).pathname.endsWith('/records/batch-cas')) {
        return route.fallback()
      }
      const body = request.postDataJSON()
      const isBackup = (body.mutations || []).some(({ record }) => record?.Key === stateKey)
      if (!isBackup || !failBackup) return route.fallback()
      const first = !blocked
      blocked = true
      if (first) console.log(JSON.stringify({ backgroundBackupGate: 'write-held' }))
      const completion = (async () => {
        await writeGate
        if (!failBackup) return route.fallback()
        await route.abort('failed')
        if (first) console.log(JSON.stringify({ backgroundBackupGate: 'write-failed' }))
      })()
      heldWrites.push(completion)
      return completion
    }
    const routes = t.nodeAPIOrigins.map(origin => `${origin}/**`)
    try {
      for (const route of routes) await page.context().route(route, unavailable)
      const renamedID = await page.evaluate(async () => {
        const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
        await store.updateWalletName(store.walletId, 'Background backup status')
        return store.walletId
      })
      assert.equal((await catalog(page)).find(wallet => String(wallet.id) === String(renamedID))?.name,
        'Background backup status', 'local wallet rename did not commit')
      console.log(JSON.stringify({ backgroundBackupGate: 'local-commit' }))
      await expect.poll(() => blocked, { timeout: 90000 }).toBe(true)
      const heldStatus = await accountCall(page, 'status')
      const heldCardText = await card.textContent() || ''
      console.log(JSON.stringify({ backgroundBackupGate: 'held-observed',
        backup_post_blocked: blocked,
        held_requests: heldWrites.length,
        same_account: heldStatus.account_id === baseline.account_id,
        sdk: {
          pending_changes: heldStatus.pending_changes ?? null,
          managed_data_dirty: heldStatus.managed_data_dirty ?? null,
          pending_requests: heldStatus.dkvs_pending_requests ?? null,
          state_seq: heldStatus.state_seq ?? null,
          data_revision: heldStatus.managed_data_revision ?? null,
          write_allowed: heldStatus.dkvs_write_allowed ?? null,
          dkvs_status: heldStatus.dkvs_status ?? null,
          error_code: heldStatus.last_dkvs_sync_error_code || '',
        },
        baseline: { state_seq: baseline.state_seq ?? null,
          data_revision: baseline.managed_data_revision ?? null },
        ui: {
          dirty_warning: heldCardText.includes(accountMessages.ui.unbackedNotice),
          pending_zero: heldCardText.includes('Pending changes: 0'),
          sync_error: heldCardText.includes(accountMessages.readiness.error),
          synchronized: heldCardText.includes('Synchronized'),
          writable: heldCardText.includes('Writable'),
        },
        same_location: page.url() === location }))
      await expect(card).not.toContainText('Pending changes: 0')
      releaseWrite()
      await Promise.all(heldWrites)
      await expect.poll(async () => Boolean((await accountCall(page, 'status')).last_dkvs_sync_error), { timeout: 90000 }).toBe(true)
      await expect(card).toContainText(accountMessages.readiness.error, { timeout: 45000 })
      await expect(card).toContainText(accountMessages.ui.unbackedNotice)
      await expect(card).not.toContainText('Pending changes: 0')
      const retryButton = card.getByRole('button', { name: accountMessages.readiness.retryNow, exact: true })
      await expect(retryButton).toBeVisible()
      const retriesBeforeClick = heldWrites.length
      await retryButton.click()
      await expect.poll(() => heldWrites.length, { timeout: 4000 }).toBeGreaterThan(retriesBeforeClick)
      await expect(card).toContainText(accountMessages.readiness.error)
      await expect(card).not.toContainText('Pending changes: 0')
      failBackup = false
      for (const route of routes) await page.context().unroute(route, unavailable)
      await settled(page)
      await expect(card).toContainText('Pending changes: 0', { timeout: 45000 })
      await expect(card).not.toContainText(accountMessages.ui.unbackedNotice)
      await expect(card).not.toContainText(accountMessages.readiness.error)
      assert.equal((await catalog(page)).find(wallet => String(wallet.id) === String(renamedID))?.name,
        'Background backup status', 'backup recovery lost the committed wallet name')
      assert.equal(page.url(), location)
    } finally {
      failBackup = false
      releaseWrite()
      try { for (const route of routes) await page.context().unroute(route, unavailable) }
      finally { await Promise.all(heldWrites) }
    }
  }))

  await scenario('usage: offline catalog is read-only and resumes synchronization after reconnect', async () => {
    const page = await device()
    const material = t.material
    try {
      await restore(page, material)
      await expect.poll(async () => {
        const status = await accountCall(page, 'status')
        return status.recovery_configured && status.dkvs_write_allowed
      }, { timeout: 90000 }).toBe(true)
      await page.evaluate(async credential => {
        const [error] = await window.__SAT20_PWA_VERIFY__.useWalletStore().createWallet(credential)
        if (error) throw error
      }, password)
      await settled(page)
      const before = await catalog(page)
      const status = await accountCall(page, 'status')
      const root = before.find(wallet => String(wallet.id) === String(status.root_wallet_id))
      const child = before.find(wallet => wallet.fingerprint !== root.fingerprint)
      assert.ok(root && child)
      await page.evaluate(async rootID => {
        await window.__SAT20_PWA_VERIFY__.useWalletStore().switchWallet(rootID)
      }, String(root.id))
      const replica = await device()
      const unavailable = route => route.abort('failed')
      const routes = t.nodeAPIOrigins.map(origin => `${origin}/**`)
      try {
        await restore(replica, material)
        await waitForAccountWrite(replica, accountCall)
        await settled(page)
        const confirmed = await catalog(page)
        for (const route of routes) await page.context().route(route, unavailable)
        // Wait for an observed transport failure. Merely installing a route
        // cannot retroactively invalidate an already verified online session.
        await expect.poll(async () => Boolean((await accountCall(page, 'status')).last_dkvs_sync_error),
          { timeout: 90000 }).toBe(true)
        const rejected = await page.evaluate(async ({ rootID, childID }) => {
          const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
          const attempt = async operation => {
            try {
              const result = await operation()
              const error = Array.isArray(result) ? result[0] : null
              return error ? String(error.message || error) : ''
            } catch (error) { return String(error?.message || error) }
          }
          return {
            rename: await attempt(() => store.updateWalletName(rootID, 'Forbidden offline root')),
            add: await attempt(() => store.addAccount('Forbidden offline savings', 2)),
            remove: await attempt(() => store.deleteWallet(childID)),
          }
        }, { rootID: String(root.id), childID: String(child.id) })
        for (const [operation, error] of Object.entries(rejected)) {
          assert.match(error, /DKVS|synchroniz|offline|同步|离线/i, `offline ${operation} did not reject at the synchronization boundary`)
        }
        assert.deepEqual(await catalog(page), confirmed)
        assert.equal((await accountCall(page, 'status')).pending_changes ?? 0, 0)
        await page.reload(); await ready(page); await unlock(page)
        assert.deepEqual(await catalog(page), confirmed, 'offline reload must preserve readable confirmed data')
        assert.equal((await accountCall(page, 'status')).pending_changes ?? 0, 0)
        await replica.evaluate(async () => {
          const root = (await window.sat20.getWalletCatalog())[1].wallets[0]
          const [error] = await window.sat20.updateAccountMetadata(String(root.id), 0, 'Online independent edit', 'did:online')
          if (error) throw error
        })
        await settled(replica)
        for (const route of routes) await page.context().unroute(route, unavailable)
        await expect.poll(async () => (await catalog(page)).find(wallet => wallet.fingerprint === root.fingerprint)?.accounts[0]?.did,
          { timeout: 90000 }).toBe('did:online')
        await settled(page)
        const final = await catalog(page)
        assert.equal(final.length, confirmed.length)
        assert.equal(final.find(wallet => wallet.fingerprint === root.fingerprint).name, root.name)
        assert.equal(final.some(wallet => wallet.fingerprint === child.fingerprint), true)
        assert.equal(final.find(wallet => wallet.fingerprint === root.fingerprint).accounts.some(account => account.index === 2), false)
      } finally {
        for (const route of routes) await page.context().unroute(route, unavailable)
        await replica.context().close()
      }
    } finally { await page.context().close() }
  })

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
        await original('temporary')
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
    assert.equal(await accountCall(page, 'resumePendingStorageAuthorization'), null)
    await assert.rejects(accountCall(page, 'confirmStorage', 'temporary'), /unlocked|session/i)
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
      const checkSession = walletRequestSessionGuard('account.status')
      const previous = await window.accountE2E.status()
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
      const guardianIdentity = await accountCall(owner, 'guardianIdentity', password)
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
      await assert.rejects(accountCall(waiting, 'guardianIdentity', password))
      assert.deepEqual(await accountCall(waiting, 'guardianIdentity', newPassword), guardianIdentity)
    } finally {
      await context.close().catch(() => {})
    }
  })

  const realTwoFactorSetupUI = async (page, statusReadMode = 'none') => {
    await preflightUI(page)
    await page.getByRole('button', { name: accountMessages.ui.modeSecure, exact: true }).click()
    await temporaryUI(page)
    await fillQuestions(page)
    await page.getByPlaceholder(accountMessages.ui.repeatAnswer, { exact: true }).first().fill('mismatched answer')
    await page.getByRole('button', { name: accountMessages.ui.createBackup, exact: true }).click()
    await confirmPassword(page)
    await page.getByRole('alert').filter({ hasText: accountMessages.ui.invalidAnswers }).waitFor()
    assert.equal((await accountCall(page, 'status')).recovery_configured, false)
    await fillQuestions(page)
    await page.getByRole('button', { name: accountMessages.ui.createBackup, exact: true }).click()
    await confirmPassword(page)
    await page.getByRole('button', { name: accountMessages.ui.startRehearsal, exact: true }).waitFor({ timeout: 90000 })
    const share = await page.locator('textarea[readonly]').nth(1).inputValue()
    await page.getByRole('button', { name: accountMessages.ui.startRehearsal, exact: true }).click()
    for (let i = 0; i < 2; i++) await page.getByPlaceholder(`Answer to question ${i + 1}`, { exact: true }).fill(questions[i].answer)
    await page.getByPlaceholder(accountMessages.ui.pasteShareAgain, { exact: true }).fill(share)
    if (statusReadMode !== 'none') await page.evaluate(mode => {
      const rehearse = window.accountE2E.rehearse.bind(window.accountE2E)
      const status = window.accountE2E.status.bind(window.accountE2E)
      window.setupCompletionFault = {
        mode, armed: false, hits: 0, verified: 0,
        manualAuthorized: false, manualClicks: 0, manualReads: 0, manualSuccesses: 0,
      }
      window.accountE2E.rehearse = async (...args) => {
        const result = await rehearse(...args)
        if (result.verified) {
          window.setupCompletionFault.verified++
          window.setupCompletionFault.armed = true
        }
        return result
      }
      window.accountE2E.status = async () => {
        const fault = window.setupCompletionFault
        if (fault.armed) {
          if (fault.mode === 'manual' && fault.manualAuthorized) {
            fault.manualAuthorized = false
            fault.manualReads++
            // Release the injected outage as the user's real read starts. A
            // concurrent SDK notification can now refresh normally as well.
            fault.armed = false
            const result = await status()
            fault.manualSuccesses++
            return result
          }
          fault.hits++
          if (fault.mode === 'automatic') fault.armed = false
          throw new Error('usage injected final configuration status read failure')
        }
        return status()
      }
    }, statusReadMode)
    await page.getByRole('button', { name: accountMessages.ui.runRehearsal, exact: true }).click()
    await confirmPassword(page)
    // First publication and active-scope refresh can advance the captured
    // baseline more than once. Exercise bounded user retries of the same
    // real session; every CAS refusal must leave recovery unconfigured.
    for (let attempt = 0; attempt < 4; attempt++) {
      let rehearsalOutcome
      await expect.poll(async () => {
        if (await page.getByRole('heading', { name: accountMessages.ui.recoveryConfigured, exact: true }).isVisible()) {
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
      await page.getByRole('button', { name: accountMessages.ui.runRehearsal, exact: true }).click()
      await confirmPassword(page)
    }
    await page.getByRole('heading', { name: accountMessages.ui.recoveryConfigured, exact: true }).waitFor()
    if (statusReadMode === 'automatic') {
      await expect.poll(async () => {
        const fault = await page.evaluate(() => window.setupCompletionFault)
        const errorVisible = await page.getByText(accountMessages.dkvsStatusReadFailed, { exact: true }).count()
        const configuredVisible = await page.getByText('Recovery configuration: Configured', { exact: true }).count()
        return fault.verified === 1 && fault.hits === 1 && !fault.armed && errorVisible === 0 && configuredVisible === 1
      }, { timeout: 30_000, intervals: [250, 500, 1000] }).toBe(true)
      assert.deepEqual(await page.evaluate(() => window.setupCompletionFault), {
        mode: 'automatic', armed: false, hits: 1, verified: 1,
        manualAuthorized: false, manualClicks: 0, manualReads: 0, manualSuccesses: 0,
      }, 'automatic status recovery replayed the recovery rehearsal or used the manual button')
    }
    if (statusReadMode === 'manual') {
      const statusError = page.getByText(accountMessages.dkvsStatusReadFailed, { exact: true })
      const retryRead = page.getByRole('button', { name: accountMessages.readiness.retryReadStatus, exact: true })
      await expect(statusError).toBeVisible()
      await expect(retryRead).toBeVisible()
      await expect(retryRead).toBeEnabled()
      assert.equal(await page.getByRole('button', { name: accountMessages.ui.runRehearsal, exact: true }).count(), 0)
      await page.evaluate(label => {
        const fault = window.setupCompletionFault
        // Background status reads can recreate the v-if button before the
        // click. Grant the permit only for the actual trusted click on Retry.
        const authorizeRead = event => {
          const button = event.target.closest('button')
          if (!event.isTrusted || button?.textContent.trim() !== label) return
          document.removeEventListener('click', authorizeRead, true)
          fault.manualClicks++
          fault.manualAuthorized = true
          // Vue must call status() during this click, before the permit expires.
          button.addEventListener('click', () => { fault.manualAuthorized = false }, { once: true })
        }
        document.addEventListener('click', authorizeRead, true)
      }, accountMessages.readiness.retryReadStatus)
      await retryRead.click()
      await expect.poll(async () => {
        const fault = await page.evaluate(() => window.setupCompletionFault)
        const errorVisible = await statusError.count()
        const configuredVisible = await page.getByText('Recovery configuration: Configured', { exact: true }).count()
        return { ...fault, errorVisible, configuredVisible }
      }, { timeout: 30_000, intervals: [250, 500, 1000] }).toMatchObject({
        verified: 1, manualClicks: 1, manualReads: 1, manualSuccesses: 1,
        manualAuthorized: false, armed: false, errorVisible: 0, configuredVisible: 1,
      })
      const fault = await page.evaluate(() => window.setupCompletionFault)
      assert.equal(fault.mode, 'manual')
      assert.ok(fault.hits >= 1, 'controlled status-read failure was never observed')
      assert.equal(fault.verified, 1, 'manual status retry replayed the recovery rehearsal')
      assert.equal(fault.manualClicks, 1)
      assert.equal(fault.manualReads, 1, 'manual button did not directly issue exactly one real status read')
      assert.equal(fault.manualSuccesses, 1, 'the status read issued by the manual button did not succeed')
    }
    assert.equal((await accountCall(page, 'status')).recovery_configured, true)
    await assertNoSecrets(page, [...questions.map(q => q.answer), share, password])
  }
  await scenario('usage: real 2of2 setup page validates answers and completes rehearsal',
    () => using(page => realTwoFactorSetupUI(page)))
  await scenario('usage: verified setup recovers automatically from a status read failure without replay',
    () => using(page => realTwoFactorSetupUI(page, 'automatic')))
  await scenario('usage: configured recovery status has an explicit read retry without replaying rehearsal',
    () => using(page => realTwoFactorSetupUI(page, 'manual')))

  await scenario('usage: configured account exposes maintenance and cancels without replacing recovery', () => using(async page => {
    const material = await setup(page)
    const before = await accountCall(page, 'status')
    const beforeCatalog = await catalog(page)
    await page.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
    await page.getByRole('button', { name: accountMessages.ui.reconfigure, exact: true }).click()
    await page.getByRole('button', { name: accountMessages.ui.checkAccount, exact: true }).click()
    await confirmPassword(page)
    await page.getByRole('button', { name: accountMessages.ui.modeSecure, exact: true }).click()
    await temporaryUI(page)
    await page.getByRole('button', { name: accountMessages.ui.cancelSetup, exact: true }).click()
    await page.getByRole('button', { name: accountMessages.ui.backToConfiguration, exact: true }).click()
    await page.getByRole('button', { name: accountMessages.ui.copyCurrentLocator, exact: true }).waitFor()
    const current = await accountCall(page, 'status')
    assert.equal(current.package_id, before.package_id)
    assert.equal(current.public_locator, material.locator)
    assert.deepEqual(await catalog(page), beforeCatalog)
    assert.equal(await accountCall(page, 'resumePendingStorageAuthorization'), null)
    await page.reload(); await ready(page); await unlock(page)
    await page.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
    await page.getByRole('button', { name: accountMessages.ui.reconfigure, exact: true }).click()
    await page.getByRole('button', { name: accountMessages.ui.checkAccount, exact: true }).waitFor()
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
    await page.getByRole('button', { name: accountMessages.ui.rehearsalTitle, exact: true }).click()
    await page.getByRole('heading', { name: accountMessages.ui.rehearsalTitle, exact: true }).waitFor()
    await expect(page.getByPlaceholder(accountMessages.ui.pasteLocator)).toHaveValue(material.locator)
    await page.getByRole('button', { name: accountMessages.ui.loadBackup, exact: true }).click()
    const inputs = page.locator('input[type="password"]')
    await inputs.nth(0).fill('incorrect private answer')
    await inputs.nth(1).fill('another incorrect answer')
    await page.getByRole('button', { name: accountMessages.ui.recoverShare, exact: true }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await inputs.nth(0).fill(questions[0].answer)
    await inputs.nth(1).fill(questions[1].answer)
    await page.getByRole('button', { name: accountMessages.ui.recoverShare, exact: true }).click()
    await page.getByPlaceholder(accountMessages.ui.pasteOptionalShare).fill(material.user_share)
    await page.getByRole('button', { name: accountMessages.ui.useShare, exact: true }).click()
    await expect(page.getByRole('button', { name: accountMessages.ui.previewRecovery, exact: true })).toBeEnabled()
    await page.getByRole('button', { name: accountMessages.ui.previewRecovery, exact: true }).click()
    await page.getByRole('heading', { name: accountMessages.ui.factorsVerified, exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: accountMessages.ui.restoreAll, exact: true }).count(), 0)
    assert.equal(await page.getByPlaceholder(accountMessages.ui.setPassword).count(), 0)
    const session = await page.evaluate(() => window.maintenanceSession)
    await page.getByRole('button', { name: accountMessages.ui.finishRehearsal, exact: true }).click()
    await page.getByRole('button', { name: accountMessages.ui.rehearsalTitle, exact: true }).waitFor()
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

  await scenario(regtestAccountUsageCaseNames[2], () => usingGuardianOwner(async page => {
    const guardian = await freshGuardian()
    try {
      await settings(guardian)
      await guardian.getByRole('button', { name: accountMessages.ui.generateContact, exact: true }).click()
      await confirmPassword(guardian)
      const contact = await guardian.locator('textarea[readonly]').first().inputValue()
      assert.ok(JSON.parse(contact).mailbox_id)
      await preflightUI(page)
      await temporaryUI(page)
      await fillQuestions(page)
      const contactInput = page.getByPlaceholder(accountMessages.ui.pasteGuardianContact)
      await contactInput.fill('{}')
      assert.equal(await page.getByRole('button', { name: accountMessages.ui.createBackup, exact: true }).isDisabled(), true)
      await contactInput.fill(contact)
      await page.getByRole('button', { name: accountMessages.ui.createBackup, exact: true }).click()
      await confirmPassword(page)
      await page.getByRole('button', { name: accountMessages.ui.verifyStored, exact: true }).waitFor({ timeout: 90000 })
      // Only the final, receipt-bound locator can be handed to the user.
      await expect(page.getByRole('button', { name: accountMessages.ui.copyLocator, exact: true })).toHaveCount(0)
      await page.context().grantPermissions(['clipboard-read', 'clipboard-write'], { origin: new URL(page.url()).origin })
      await page.getByRole('button', { name: accountMessages.ui.copyShare, exact: true }).click()
      const savedShare = await page.evaluate(() => navigator.clipboard.readText())
      assert.equal(savedShare, await page.getByRole('textbox', { name: accountMessages.ui.secretShare, exact: true }).inputValue())
      assert.ok(savedShare.startsWith('sat20-share-v1:'))
      const setupJSON = await page.getByRole('textbox', { name: accountMessages.ui.guardianSetup, exact: true }).inputValue()
      await guardian.getByPlaceholder(accountMessages.ui.pasteFriendSetup).fill(setupJSON)
      await guardian.locator('select').selectOption('temporary')
      await guardian.getByRole('button', { name: accountMessages.ui.acceptShare, exact: true }).click()
      await confirmPassword(guardian)
      await expect.poll(async () => guardian.locator('textarea[readonly]').count(), { timeout: 90000 }).toBe(2)
      const receipt = await guardian.locator('textarea[readonly]').last().inputValue()
      await page.getByPlaceholder(accountMessages.ui.pasteReceipt).fill(receipt)
      await page.getByRole('button', { name: accountMessages.ui.verifyStored, exact: true }).click()
      await expect(page.getByRole('heading', { name: accountMessages.ui.stepSave, exact: true })).toBeVisible()
      await page.getByRole('button', { name: accountMessages.ui.copyLocator, exact: true }).click()
      const savedLocator = await page.evaluate(() => navigator.clipboard.readText())
      assert.ok(savedLocator.startsWith('sat20account1:'))
      await page.getByRole('button', { name: accountMessages.ui.savedStartRehearsal, exact: true }).click()
      await page.getByPlaceholder(accountMessages.ui.pasteFinalLocatorAgain).fill('incorrect saved locator')
      await page.getByRole('button', { name: accountMessages.ui.createRehearsalRequest, exact: true }).click()
      await page.getByRole('alert').filter({ hasText: accountMessages.ui.finalLocatorRequired }).waitFor()
      assert.equal((await accountCall(page, 'status')).recovery_configured, false)
      await page.getByPlaceholder(accountMessages.ui.pasteFinalLocatorAgain).fill(savedLocator)
      await page.getByRole('button', { name: accountMessages.ui.createRehearsalRequest, exact: true }).click()
      const request = await page.locator('textarea[readonly]').first().inputValue()
      await guardian.getByPlaceholder(accountMessages.ui.pasteFriendRequest).fill(request)
      await guardian.getByRole('button', { name: accountMessages.ui.createResponse, exact: true }).click()
      await confirmPassword(guardian)
      await expect.poll(async () => guardian.locator('textarea[readonly]').count(), { timeout: 90000 }).toBe(3)
      const response = await guardian.locator('textarea[readonly]').last().inputValue()
      await page.getByPlaceholder(accountMessages.ui.pasteGuardianResponse).fill(response)
      await page.getByRole('button', { name: accountMessages.ui.verifyGuardianResponse, exact: true }).click()
      for (let i = 0; i < 2; i++) await page.getByPlaceholder(`Answer to question ${i + 1}`, { exact: true }).fill(questions[i].answer)
      await page.getByRole('button', { name: accountMessages.ui.runRehearsal, exact: true }).click()
      await confirmPassword(page)
      await page.getByRole('heading', { name: accountMessages.ui.recoveryConfigured, exact: true }).waitFor({ timeout: 90000 })
      const status = await accountCall(page, 'status')
      assert.equal(status.recovery_configured, true)
      assert.equal(status.recovery_mode, '2of3')
      await page.getByRole('button', { name: accountMessages.ui.copyCurrentLocator, exact: true }).click()
      assert.equal(await page.evaluate(() => navigator.clipboard.readText()), savedLocator)
      const expectedCatalog = await catalog(page)
      const normalize = wallets => wallets.map(({ fingerprint, name, accounts }) => ({ fingerprint, name, accounts }))
      await assertNoSecrets(page, [...questions.map(q => q.answer), savedShare, password])
      await assertNoSecrets(guardian, [...questions.map(q => q.answer), password])
      // Keep the source open until a separate device proves the first managed
      // backup can be decrypted and previewed through the real recovery UI.
      let firstBackupRecoverable = false
      for (const combination of ['knowledge+guardian', 'share+guardian']) {
        const recovery = await device()
        let stage = 'load-backup'
        const stageAt = next => {
          stage = next
          console.log(JSON.stringify({ guardianRecoveryStage: { combination, stage } }))
        }
        try {
          stageAt('load-backup')
          await recovery.evaluate(() => { location.hash = '#/restore-account' })
          await recovery.getByPlaceholder(accountMessages.ui.pasteLocator).fill(savedLocator)
          await recovery.getByRole('button', { name: accountMessages.ui.loadBackup, exact: true }).click()
          if (combination === 'knowledge+guardian') {
            stageAt('recover-knowledge')
            for (let i = 0; i < 2; i++) await recovery.locator('section input[type="password"]').nth(i).fill(questions[i].answer)
            await recovery.getByRole('button', { name: accountMessages.ui.recoverShare, exact: true }).click()
          } else {
            stageAt('set-user-share')
            await recovery.getByRole('button', { name: accountMessages.ui.shareAndGuardian, exact: true }).click()
            await recovery.getByPlaceholder(accountMessages.ui.pasteOptionalShare).fill(savedShare)
            await recovery.getByRole('button', { name: accountMessages.ui.useShare, exact: true }).click()
          }
          stageAt('guardian-request')
          await recovery.getByRole('button', { name: accountMessages.ui.createGuardianRequest, exact: true }).click()
          const recoveryRequest = await recovery.locator('textarea[readonly]').first().inputValue()
          const previousResponse = await guardian.locator('textarea[readonly]').last().inputValue()
          await guardian.getByPlaceholder(accountMessages.ui.pasteFriendRequest).fill(recoveryRequest)
          await guardian.getByRole('button', { name: accountMessages.ui.createResponse, exact: true }).click()
          await confirmPassword(guardian)
          await expect.poll(() => guardian.locator('textarea[readonly]').last().inputValue(), { timeout: 90000 }).not.toBe(previousResponse)
          stageAt('guardian-response')
          await recovery.getByPlaceholder(accountMessages.ui.pasteGuardianResponse).fill(await guardian.locator('textarea[readonly]').last().inputValue())
          await recovery.getByRole('button', { name: accountMessages.ui.useGuardianResponse, exact: true }).click()
          stageAt('preview')
          await recovery.getByRole('button', { name: accountMessages.ui.previewRecovery, exact: true }).click()
          stageAt('preview-content')
          await assertBackupPreview(recovery, expectedCatalog)
          if (!firstBackupRecoverable) {
            stageAt('source-status')
            const sourceStatus = await accountCall(page, 'status')
            const sourceReadiness = accountReadiness(sourceStatus)
            const locatorFacts = await page.evaluate(({ current, saved }) => {
              try {
                const decode = value => JSON.parse(atob(value.split(':')[1].replaceAll('-', '+').replaceAll('_', '/')))
                const actual = decode(current)
                const expected = decode(saved)
                const fields = ['network', 'storage_location', 'storage_mode', 'record_ttl',
                  'autopay_contract', 'guardian_location', 'locator']
                return { decoded: true, current_guardian: Boolean(actual.guardian_location),
                  saved_guardian: Boolean(expected.guardian_location),
                  matches: Object.fromEntries(fields.map(key => [key, JSON.stringify(actual[key]) === JSON.stringify(expected[key])])) }
              } catch { return { decoded: false } }
            }, { current: sourceStatus.public_locator, saved: savedLocator })
            console.log(JSON.stringify({ guardianSourceStatus: {
              configured: sourceStatus.recovery_configured, activation_pending: sourceStatus.activation_pending,
              locator_matches: sourceStatus.public_locator === savedLocator, locator: locatorFacts } }))
            assert.equal(sourceStatus.recovery_configured, true)
            assert.equal(sourceStatus.activation_pending, false)
            assert.equal(sourceStatus.public_locator, savedLocator)
            console.log(JSON.stringify({ guardianRecoveryReadiness: {
              discovery: sourceReadiness.discovery,
              write_qualified: sourceReadiness.writeQualified,
              first_backup_recoverable: true,
            } }))
            firstBackupRecoverable = true
            await page.context().close()
          }
          const localPassword = recovery.getByLabel(accountMessages.ui.newDevicePassword, { exact: true })
          const confirmPasswordBox = recovery.getByLabel(accountMessages.ui.repeatUnlockPassword, { exact: true })
          stageAt('password-inputs-visible')
          await localPassword.waitFor()
          await confirmPasswordBox.waitFor()
          stageAt('fill-local-password')
          await localPassword.fill(newPassword)
          stageAt('fill-confirm-password')
          await confirmPasswordBox.fill(newPassword)
          stageAt('restore-wallets')
          await Promise.all([recovery.waitForEvent('domcontentloaded', { timeout: 90000 }), recovery.getByRole('button', { name: accountMessages.ui.restoreAll, exact: true }).click()])
          await ready(recovery); await unlock(recovery, newPassword)
          stageAt('catalog-and-secret-checks')
          const restoredStatus = await accountCall(recovery, 'status')
          assert.equal(restoredStatus.recovery_configured, true)
          assert.equal(restoredStatus.public_locator, savedLocator, 'restored account lost the verified Guardian recovery route')
          assert.deepEqual(normalize(await catalog(recovery)), normalize(expectedCatalog), combination)
          await assertNoSecrets(recovery, [...questions.map(q => q.answer), savedShare, newPassword])
        } catch (error) {
          const view = await recovery.evaluate(() => {
            const visible = element => Boolean(element && element.getClientRects().length &&
              getComputedStyle(element).visibility !== 'hidden')
            return {
              hash_route: location.hash.split('?')[0],
              headings: [...document.querySelectorAll('h1,h2')].filter(visible).slice(0, 20).map(element => element.textContent),
              errors: [...document.querySelectorAll('[role="alert"]')].filter(visible).slice(0, 10).map(element => element.textContent),
              password_inputs: ['restore-local-password', 'restore-confirm-password'].map(id => {
                const element = document.getElementById(id)
                return { id, type: element?.getAttribute('type') ?? null,
                  label: document.querySelector(`label[for="${id}"]`)?.textContent ?? null,
                  visible: visible(element) }
              }),
            }
          }).catch(() => null)
          const redact = value => {
            let text = String(value ?? '').replace(/\{[\s\S]*\}|\[[\s\S]*\]/g, '[redacted-detail]')
            for (const secret of [password, newPassword, savedLocator, savedShare, importMnemonic,
              ...Object.values(guardianRecoverySyntheticMnemonics),
              ...questions.flatMap(question => [question.prompt, question.answer, question.confirmation])]) {
              if (typeof secret === 'string' && secret) text = text.replaceAll(secret, '[redacted]')
            }
            return text.replace(/sat20(?:account1:|-share-v1:)[^\s"'<>]+/g, '[redacted]')
              .replace(/[A-Za-z0-9_+/=-]{32,}/g, '[redacted]')
              .replace(/\b(?:[a-z]+\s+){11,23}[a-z]+\b/gi, '[redacted]')
              .slice(0, 240)
          }
          console.error(JSON.stringify({ guardianRecoveryFailure: { combination, stage,
            failure: { name: error?.name || '',
              operation: String(error?.message || '').match(/^[A-Za-z.]+/)?.[0] || '',
              timed_out: /Timeout|Timed out/i.test(String(error?.message || '')),
              strict_locator: /strict mode violation/i.test(String(error?.message || '')) },
            hash_route: redact(view?.hash_route), headings: view?.headings.map(redact) ?? [],
            errors: view?.errors.map(redact) ?? [],
            password_inputs: view?.password_inputs.map(input => ({ ...input, label: redact(input.label) })) ?? [] } }))
          await t.preserveBrowserFailureState?.()
          // Locator errors can contain fill arguments. Report the allowed view
          // above without rethrowing a password-bearing Playwright call log.
          throw new Error(`Guardian recovery ${combination} failed at ${stage}; see redacted guardianRecoveryFailure`)
        } finally { await recovery.context().close() }
      }
    } catch (error) {
      await t.preserveBrowserFailureState?.()
      throw error
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
      await page.getByPlaceholder(accountMessages.ui.pasteLocator).fill(t.material.locator)
      await page.getByRole('button', { name: accountMessages.ui.loadBackup, exact: true }).click()
      for (let i = 0; i < 2; i++) await page.locator('section input[type="password"]').nth(i).fill(questions[i].answer)
      await page.getByRole('button', { name: accountMessages.ui.recoverShare, exact: true }).click()
      await page.getByPlaceholder(accountMessages.ui.pasteOptionalShare).fill(t.material.user_share)
      await page.getByRole('button', { name: accountMessages.ui.useShare, exact: true }).click()
      await page.getByRole('button', { name: accountMessages.ui.previewRecovery, exact: true }).click()
      await page.getByPlaceholder(accountMessages.ui.setPassword).fill(newPassword)
      await page.getByPlaceholder(accountMessages.ui.repeatPassword).fill(newPassword)
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
      await page.getByRole('button', { name: accountMessages.ui.restoreAll, exact: true }).click()
      await expect.poll(() => new URL(page.url()).hash, { timeout: 90000 }).toBe('#/unlock')
      await expect(page.getByRole('button', { name: accountMessages.ui.restoreAll, exact: true })).toHaveCount(0)
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
    await page.getByPlaceholder(accountMessages.ui.pasteGuardianContact).fill(JSON.stringify(own))
    await expect(page.getByText(accountMessages.ui.selfGuardian, { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: accountMessages.ui.createBackup, exact: true })).toBeDisabled()
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
    await page.getByRole('button', { name: accountMessages.ui.paidStorage, exact: false }).click()
    await page.locator('#autopay-record-count').fill('200')
    await page.getByRole('button', { name: accountMessages.ui.confirmStorage, exact: true }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('200', { exact: true })).toBeVisible()
    await expect(dialog).toContainText(status.account_id)
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    await page.getByPlaceholder(accountMessages.ui.pasteFriendSetup).fill(t.material.guardian_setup)
    await page.locator('select').selectOption('paid')
    await page.getByRole('button', { name: accountMessages.ui.acceptShare, exact: true }).click()
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
    await page.getByRole('button', { name: accountMessages.ui.paidStorage, exact: false }).click()
    await page.evaluate(() => {
      const confirm = window.accountE2E.confirmStorage.bind(window.accountE2E)
      window.recordCountPaidRequests = 0
      window.accountE2E.confirmStorage = (...args) => { window.recordCountPaidRequests++; return confirm(...args) }
    })
    for (const value of ['99', '100.5', '9007199254740992']) {
      await page.locator('#autopay-record-count').fill(value)
      await expect(page.getByRole('button', { name: accountMessages.ui.confirmStorage, exact: true })).toBeDisabled()
      await expect(page.getByRole('dialog')).toHaveCount(0)
      assert.equal(await page.evaluate(() => window.recordCountPaidRequests), 0, 'invalid count dispatched paid storage')
    }
    await page.locator('#autopay-record-count').fill('100')
    await page.getByRole('button', { name: accountMessages.ui.confirmStorage, exact: true }).click()
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
      await page.getByPlaceholder(accountMessages.ui.pasteLocator).fill(t.material.locator)
      await page.evaluate(() => {
        const original = window.accountE2E.loadRecovery.bind(window.accountE2E)
        window.accountE2E.loadRecovery = async locator => {
          const result = await original(locator)
          window.usageLoadedSession = result.session_id
          return result
        }
      })
      await page.getByRole('button', { name: accountMessages.ui.loadBackup, exact: true }).click()
      for (let i = 0; i < 2; i++) await page.locator('section input[type="password"]').nth(i).fill(questions[i].answer)
      await page.getByRole('button', { name: accountMessages.ui.recoverShare, exact: true }).click()
      await page.getByPlaceholder(accountMessages.ui.pasteOptionalShare).fill(t.material.user_share)
      await page.getByRole('button', { name: accountMessages.ui.useShare, exact: true }).click()
      await page.getByRole('button', { name: accountMessages.ui.previewRecovery, exact: true }).click()
      const session = await page.evaluate(() => window.usageLoadedSession)
      await page.evaluate(() => { location.hash = '#/' })
      await page.getByRole('heading', { name: accountMessages.ui.confirmRecovery, exact: true }).waitFor({ state: 'hidden' })
      await assert.rejects(accountCall(page, 'commitRecovery', session, password))
      assert.deepEqual(await catalog(page), [])
      await assertNoSecrets(page, [...questions.map(q => q.answer), t.material.user_share, password])
      await page.reload(); await ready(page)
      await assert.rejects(accountCall(page, 'previewRecovery', session))
    } finally { await page.context().close() }
  })
  if (fixedOwner) {
    await scenario(fixedRegtestAccountUsageCaseNames[0], () => usingFixedOwner(async (page, { expectedIdentity, root }) => {
      const before = await catalog(page)
      assert.equal(before.length, 1)
      const beforeRoot = before[0]
      assertFixedRootIdentity(expectedIdentity, {
        fingerprint: beforeRoot.fingerprint,
        address: beforeRoot.accounts?.find(account => Number(account.index) === 0)?.address,
      })
      const existingAccounts = beforeRoot.accounts.map(({ index, name, address, pubKey }) => ({ index, name, address, pubKey }))
      assert.ok(existingAccounts.some(account => Number(account.index) === 0), 'fixed ordinary-1 root account 0 is missing')
      const accountIndex = Math.max(0, ...existingAccounts.map(account => Number(account.index))) + 1
      const initialName = `Fixed regtest subaccount ${accountIndex}`
      const renamed = `Fixed regtest selected ${accountIndex}`
      await page.evaluate(async ({ accountIndex, initialName, renamed }) => {
        const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
        await wallet.addAccount(initialName, accountIndex)
        await wallet.updateAccountName(accountIndex, renamed)
      }, { accountIndex, initialName, renamed })

      const after = await catalog(page)
      assert.equal(after.length, 1, 'fixed account action created another root wallet')
      const updatedRoot = after[0]
      assert.equal(String(updatedRoot.id), String(root.id), 'fixed root wallet id changed')
      assertFixedRootIdentity(expectedIdentity, {
        fingerprint: updatedRoot.fingerprint,
        address: updatedRoot.accounts?.find(account => Number(account.index) === 0)?.address,
      })
      assert.equal(updatedRoot.accounts.length, existingAccounts.length + 1, 'adding one subaccount changed the account count unexpectedly')
      for (const existing of existingAccounts) {
        const actual = updatedRoot.accounts.find(account => Number(account.index) === Number(existing.index))
        assert.deepEqual(actual && {
          index: actual.index,
          name: actual.name,
          address: actual.address,
          pubKey: actual.pubKey,
        }, existing,
          `existing account ${existing.index} changed while adding a subaccount`)
      }
      const created = updatedRoot.accounts.find(account => Number(account.index) === accountIndex)
      assert.ok(created, 'selected subaccount was not added to the fixed root wallet')
      assert.equal(created.name, renamed)

      const selected = await page.evaluate(() => {
        const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
        return { index: wallet.accountIndex, address: wallet.address, pubKey: wallet.publicKey }
      })
      assert.equal(selected.index, accountIndex, 'addAccount did not select the new subaccount')
      assert.equal(selected.address, (await walletCall(page, 'getWalletAddress', accountIndex)).address)
      assert.equal(selected.pubKey, (await walletCall(page, 'getWalletPubkey', accountIndex)).pubKey)

      await page.evaluate(async () => window.__SAT20_PWA_VERIFY__.useWalletStore().switchToAccount(0))
      assert.equal((await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().accountIndex)), 0,
        'switching back to root account 0 did not take effect')
      assert.equal((await page.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().address)),
        (await walletCall(page, 'getWalletAddress', 0)).address)

      await page.reload(); await ready(page); await unlock(page, fixedOwner.password)
      const restored = await catalog(page)
      assert.equal(restored.length, 1, 'reload changed the fixed root wallet count')
      assertFixedRootIdentity(expectedIdentity, {
        fingerprint: restored[0].fingerprint,
        address: restored[0].accounts?.find(account => Number(account.index) === 0)?.address,
      })
      assert.deepEqual(restored[0].accounts.map(({ index, name, address, pubKey }) => ({ index, name, address, pubKey })),
        updatedRoot.accounts.map(({ index, name, address, pubKey }) => ({ index, name, address, pubKey })),
        'subaccount selection or rename did not persist across reload')
      assert.equal((await walletCall(page, 'getWalletCatalog')).current_account_index, 0,
        'reload did not preserve the explicitly selected root account')
    }))

    await scenario(fixedRegtestAccountUsageCaseNames[1], () => usingFixedOwner(async (page, { expectedIdentity, root }) => {
      const beforeCatalog = await catalog(page)
      assert.equal(beforeCatalog.length, 1, 'fixed AUTOPAY owner imported more than one root')
      assertFixedRootIdentity(expectedIdentity, {
        fingerprint: beforeCatalog[0].fingerprint,
        address: beforeCatalog[0].accounts?.find(account => Number(account.index) === 0)?.address,
      })
      const accountStatus = await accountCall(page, 'status')
      assert.equal(accountStatus.active, true, 'fixed ordinary-1 account management is not active')
      assert.equal(accountStatus.recovery_configured, true, 'fixed ordinary-1 recovery is not configured')
      assert.equal(accountStatus.storage_mode, 'paid', 'fixed ordinary-1 is not using paid AUTOPAY storage')

      await settings(page)
      const initial = await accountCall(page, 'autopayStatus')
      assert.equal(initial.required, true, 'fixed ordinary-1 has no AUTOPAY requirement')
      assert.equal(initial.ready, true, 'current AUTOPAY is not ready for renewal')
      assert.equal(initial.can_fund, true, 'current AUTOPAY does not permit a normal renewal')
      assert.ok(initial.contract_address, 'current AUTOPAY contract address is missing')
      assert.ok(initial.fee_asset, 'current AUTOPAY fee asset is missing')
      assert.ok(initial.payer, 'current AUTOPAY payer is missing')
      assert.ok(Number(initial.recommended_funding_amount) > 0, 'current AUTOPAY renewal amount is invalid')
      const initialExpiry = Number(initial.estimated_expiry_height)
      const fundedExpiry = Number(initial.estimated_funded_expiry_height)
      assert.ok(Number.isSafeInteger(initialExpiry) && initialExpiry > 0,
        'current AUTOPAY expiry estimate is missing')
      assert.ok(Number.isSafeInteger(fundedExpiry) && fundedExpiry > initialExpiry,
        'current AUTOPAY quote does not project an added paid interval')
      assert.equal(initial.payer, (await walletCall(page, 'getWalletAddress', 0)).address,
        'AUTOPAY funding source is not fixed ordinary-1 root account 0')
      const selection = await walletCall(page, 'getWalletCatalog')
      assert.equal(selection.current_account_index, 0, 'AUTOPAY renewal must use root account 0')

      const submissions = []
      // Observe the real UI call without replacing its result or issuing a
      // second payment. Keep a failure visible before closing this context.
      await page.evaluate(async () => {
        const api = (await import('/utils/accountManagement.ts')).default
        const original = api.fundAutopay
        const state = { called: false, settled: false, transaction_id: '', pending: false, error: '' }
        window.__SAT20_FIXED_AUTOPAY_DIAGNOSTIC__ = state
        api.fundAutopay = async function (...args) {
          state.called = true
          try {
            const result = await original.apply(this, args)
            state.transaction_id = result?.transaction_id || ''
            state.pending = Boolean(result?.pending)
            return result
          } catch (error) {
            state.error = String(error?.message || error).slice(0, 500)
            throw error
          } finally { state.settled = true }
        }
      })
      const routeFunding = async route => {
        if (route.request().method() !== 'POST' || !/\/btc\/tx$/.test(new URL(route.request().url()).pathname)) {
          return route.fallback()
        }
        if (submissions.length > 0) {
          submissions.push({ duplicate: true })
          return route.abort('blockedbyclient')
        }
        const response = await route.fetch()
        const result = await response.json()
        submissions.push({
          http_status: response.status(),
          code: result?.code,
          txid: typeof result?.data === 'string' ? result.data : '',
        })
        await route.fulfill({ response })
      }
      await page.context().route('**/*', routeFunding)
      try {
        const renew = page.getByRole('button', { name: /^(Renew DKVS storage|续费 DKVS 存储)$/ })
        await expect(renew).toBeVisible()
        await renew.click()
        const dialog = page.getByRole('dialog')
        await expect(dialog).toBeVisible()
        await expect(dialog).toContainText(initial.payer)
        await expect(dialog).toContainText(initial.contract_address)
        await expect(dialog).toContainText(initial.fee_asset)
        await expect(dialog).toContainText(initial.recommended_funding_amount)
        await expect(dialog).toContainText(String(fundedExpiry))
        await dialog.getByRole('button', { name: /^(Confirm|确认)$/ }).click()
        await expect(dialog).toBeHidden()
        await expect.poll(() => submissions.length, { timeout: 90000 }).toBe(1)
        const [submission] = submissions
        assert.equal(submission.http_status, 200, 'AUTOPAY renewal submission returned an HTTP error')
        assert.equal(submission.code, 0, 'AUTOPAY renewal submission failed')
        assert.match(submission.txid, /^[0-9a-f]{64}$/i, 'AUTOPAY renewal did not return a transaction id')
        assert.equal(submissions.some(item => item.duplicate), false, 'AUTOPAY renewal attempted a duplicate transaction')
        await expect(page.getByText(submission.txid, { exact: false })).toBeVisible({ timeout: 90000 })
        await expect.poll(async () => {
          const current = await accountCall(page, 'autopayStatus')
          return current.funding_transaction_id === submission.txid && current.ready && !current.funding_pending &&
            Number(current.estimated_expiry_height) > initialExpiry
        }, { timeout: 90000, message: `AUTOPAY renewal ${submission.txid} did not extend the paid interval` }).toBe(true)
        const afterQuote = await accountCall(page, 'autopayStatus')
        assert.ok(Number(afterQuote.estimated_expiry_height) > initialExpiry,
          'confirmed AUTOPAY renewal did not increase the estimated expiry height')
        const expiryMessages = [
          `预计从聪网高度 ${afterQuote.estimated_expiry_height} 起余额不足以继续付费`,
          `Estimated insufficient payment balance from SatoshiNet height ${afterQuote.estimated_expiry_height}`,
        ]
        // The SDK query above does not update the page's reactive quote. Wait
        // for its normal 30-second refresh to render the confirmed renewal.
        await expect.poll(async () => {
          const expiryPanel = await page.locator('[aria-label="DKVS 付费高度"]').innerText()
          return expiryMessages.some(message => expiryPanel.includes(message))
        }, { timeout: 90000, message: 'PWA paid-height panel does not display the renewed expiry estimate' }).toBe(true)
        const after = await accountCall(page, 'status')
        assert.equal(after.active, true)
        assert.equal(after.recovery_configured, true)
        assert.equal(after.storage_mode, 'paid')
        assert.equal(after.package_id, accountStatus.package_id, 'AUTOPAY renewal changed the account-management package')
        assert.equal(after.public_locator, accountStatus.public_locator, 'AUTOPAY renewal changed the recovery locator')
        assert.deepEqual(await catalog(page), beforeCatalog, 'AUTOPAY renewal changed root wallet or subaccount metadata')
        assertFixedRootIdentity(expectedIdentity, {
          fingerprint: root.fingerprint,
          address: root.accounts?.find(account => Number(account.index) === 0)?.address,
        })
        console.log(JSON.stringify({ fixedAutopayRenewal: {
          transaction_id: submission.txid, contract: initial.contract_address,
          payer: initial.payer, funding_amount: initial.recommended_funding_amount,
          previous_expiry_height: initialExpiry, renewed_expiry_height: afterQuote.estimated_expiry_height,
          observed_submissions: submissions.length,
        } }))
      } catch (error) {
        let diagnosticTimer
        const view = await Promise.race([
          page.evaluate(() => ({
            sdk: window.__SAT20_FIXED_AUTOPAY_DIAGNOSTIC__,
            alerts: [...document.querySelectorAll('[role="alert"]')].map(element => element.textContent),
          })).catch(() => null),
          new Promise(resolve => { diagnosticTimer = setTimeout(() => resolve(null), 2000) }),
        ]).finally(() => clearTimeout(diagnosticTimer))
        console.error(JSON.stringify({ fixedAutopayFailure: view, observedSubmissions: submissions }))
        throw error
      } finally {
        await page.context().unroute('**/*', routeFunding)
      }
    }))
  }
  assert.deepEqual(failures, [], 'PWA usage regressions failed; see individual verdicts')
}

export async function runRgbPwaCases(t, fixture) {
  const { check, device, ready, unlock, catalog, walletCall, accountCall } = t
  const failures = []
  const waitForPeerCatalog = async peer => {
    await waitForAccountWrite(peer, accountCall)
    await expect.poll(async () => {
      const status = await accountCall(peer, 'status')
      return accountReadiness(status).writeQualified && status.modules?.some(module =>
        module.provider === 'wallets' && module.scope === 'global' && module.write_allowed)
    }, { timeout: 90000, message: 'restored peer wallet catalog must be writable' }).toBe(true)
  }
  const navigateRecovery = async page => {
    await page.evaluate(() => { location.hash = '#/restore-account' })
    await page.getByPlaceholder(accountMessages.ui.pasteLocator).fill(fixture.locator)
    await page.getByRole('button', { name: accountMessages.ui.loadBackup, exact: true }).click()
    for (let i = 0; i < fixture.answers.length; i++) await page.locator('section input[type="password"]').nth(i).fill(fixture.answers[i].answer)
    await page.getByRole('button', { name: accountMessages.ui.recoverShare, exact: true }).click()
    await page.getByPlaceholder(accountMessages.ui.pasteOptionalShare).fill(fixture.user_share)
    await page.getByRole('button', { name: accountMessages.ui.useShare, exact: true }).click()
    await page.getByRole('button', { name: accountMessages.ui.previewRecovery, exact: true }).click()
    await page.getByPlaceholder(accountMessages.ui.setPassword).fill(fixture.password)
    await page.getByPlaceholder(accountMessages.ui.repeatPassword).fill(fixture.password)
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
      page.getByRole('button', { name: accountMessages.ui.restoreAll, exact: true }).click(),
    ])
    await ready(page); await unlockRecovered(page)
  }
  const verify = async (page, expectedScopes = fixture.scopes) => {
    const wallets = await catalog(page)
    assert.equal(wallets.length, fixture.wallet_count)
    const observed = []
    for (const expected of expectedScopes) {
      const wallet = wallets.find(item => item.fingerprint === expected.fingerprint)
      assert.ok(wallet)
      // The catalog is committed before provider import completes. A later
      // remote apply can also start after unlock, so retry only this public
      // selection/read boundary on the explicit import-incomplete error.
      let state, locks
      const deadline = Date.now() + 90000
      for (;;) {
        try {
          await page.evaluate(async ({ id, index }) => {
            const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
            await store.switchWallet(id)
            await store.switchToAccount(index)
          }, { id: String(wallet.id), index: expected.index })
          state = JSON.parse((await walletCall(page, 'getRGB11State')).state)
          const address = wallet.accounts.find(account => account.index === expected.index).address
          locks = await walletCall(page, 'getAllLockedUtxo', address)
          break
        } catch (error) {
          if (!/account-managed data import is incomplete; restore the wallet before uploading/.test(error.message) ||
            Date.now() >= deadline) throw error
          await new Promise(resolve => setTimeout(resolve, 250))
        }
      }
      const stable = value => Object.fromEntries(['assets', 'proofs'].map(key => [key,
        [...(value[key] ?? [])].sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)))]))
      const actual = stable(state)
      const reference = stable(expected.state)
      assert.deepEqual(actual, reference, 'browser RGB assets/proofs differ from expected ownership')
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
          // Fail only the first affected RGB scope, including after a page reload.
          // Other wallets and the SDK account catalog must remain usable.
          const installFault = () => {
            const put = IDBObjectStore.prototype.put
            IDBObjectStore.prototype.put = function(value, key) {
              const row = String(key)
              if (sessionStorage.getItem('rgbProofFault') && this.name === 'kv' &&
                  row.startsWith('rgb11-') && row.includes('-proof-')) {
                const scope = row.slice(0, row.indexOf('-proof-'))
                const target = sessionStorage.getItem('rgbProofScope') ?? scope
                if (scope === target) {
                  sessionStorage.setItem('rgbProofScope', scope)
                  throw new DOMException('usage injected real RGB proof persistence failure', 'QuotaExceededError')
                }
              }
              return put.call(this, value, key)
            }
          }
          await page.evaluate(() => sessionStorage.setItem('rgbProofFault', '1'))
          await page.addInitScript(installFault)
          await page.evaluate(installFault)
        }
        if (mnemonic) {
          await page.locator('form button[type="submit"]').click()
          await expect.poll(() => page.evaluate(() => location.hash), { timeout: 90000 }).toBe('#/wallet')
          await unlockRecovered(page)
        } else await commit(page)
        if (fault) {
          assert.ok(await page.evaluate(() => sessionStorage.getItem('rgbProofScope')), 'real proof write must fail')
          const failedScope = async () => (await accountCall(page, 'status')).modules?.find(module =>
            module.provider === 'rgb11' && !module.write_allowed &&
            /usage injected real RGB proof persistence failure/.test(module.last_error ?? ''))
          await expect.poll(async () => Boolean(await failedScope()), { timeout: 90000 }).toBe(true)
          const target = (await failedScope()).scope
          assert.equal((await catalog(page)).length, fixture.wallet_count)
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
              await waitForPeerCatalog(peer)
              await peer.evaluate(async id => {
                await window.__SAT20_PWA_VERIFY__.useWalletStore().updateWalletName(id, 'Independent device newer state')
              }, String(root.id))
              await expect.poll(async () => (await accountCall(peer, 'status')).pending_changes ?? 0,
                { timeout: 90000 }).toBe(0)
            } finally { await peer.context().close() }
          }
          await page.reload(); await ready(page); await unlockRecovered(page)
          await expect.poll(async () => (await failedScope())?.scope, { timeout: 90000 }).toBe(target)
          await page.evaluate(() => sessionStorage.removeItem('rgbProofFault'))
          const retry = await accountCall(page, 'retryDKVSSync')
          assert.equal(retry.queued, true, 'DKVS retry must report that the recovery work was queued')
          await expect.poll(async () => {
            const module = (await accountCall(page, 'status')).modules?.find(item =>
              item.provider === 'rgb11' && item.scope === target)
            return Boolean(module?.write_allowed && !module.last_error && module.backup_state === 'backed_up')
          }, { timeout: 90000 }).toBe(true)
        }
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
  const rgbCaptures = []
  const captureRGB = async (page, phase) => {
    if (!fixture.rgb_diagnostic_file) return
    const records = await page.evaluate(async () => {
      const db = await new Promise((resolve, reject) => {
        const request = indexedDB.open('sat20-wallet-sdk', 1)
        request.onsuccess = () => resolve(request.result)
        request.onerror = () => reject(request.error)
      })
      try {
        return await new Promise((resolve, reject) => {
          const records = {}
          const tx = db.transaction('kv', 'readonly')
          const cursor = tx.objectStore('kv').openCursor(IDBKeyRange.bound('rgb11-', 'rgb11.'))
          cursor.onsuccess = () => {
            const row = cursor.result
            if (!row) return
            if (typeof row.key === 'string' && row.key.startsWith('rgb11-')) records[row.key] = row.value
            row.continue()
          }
          tx.oncomplete = () => resolve(records)
          tx.onabort = () => reject(tx.error)
          tx.onerror = () => reject(tx.error)
        })
      } finally { db.close() }
    })
    rgbCaptures.push({ phase, records })
    writeFileSync(fixture.rgb_diagnostic_file, JSON.stringify(rgbCaptures), { mode: 0o600 })
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
        await expect.poll(async () => (await accountCall(peer, 'status')).pending_changes ?? 0,
          { timeout: 90000 }).toBe(0)
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
        await captureRGB(channelPage, 'seed')
        await channelPage.reload(); await ready(channelPage)
        await captureRGB(channelPage, 'reload-ready')
        await unlockRecovered(channelPage, 'same-device healthy cold startup before injected fault')
        await captureRGB(channelPage, 'unlock')
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
        let localRGBBeforeFault
        try { localRGBBeforeFault = await verify(channelPage, fixture.local_scopes) }
        catch (error) {
          // Preserve the original strict assertion even if diagnostics cannot be saved.
          await captureRGB(channelPage, 'verify-fail').catch(() => {})
          throw error
        }
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
        await expect.poll(async () => (await accountCall(peer, 'status')).pending_changes ?? 0,
          { timeout: 90000 }).toBe(0)
        await expect.poll(() => channelPage.evaluate(() => window.__usageCatalogAborted === true),
          { timeout: 90000 }).toBe(true)
        await expect.poll(async () => (await accountCall(channelPage, 'status')).last_dkvs_sync_error_code,
          { timeout: 90000 }).toBe('ACCOUNT_DB_WRITE_FAILED')
        assert.ok(await channelPage.evaluate(checkLocalRows, { keys: catalogKeys }),
          'aborted remote catalog transaction changed durable wallet rows')
        // Reload drops the page-local fault and starts a fresh Manager. It must
        // finish the interrupted wallets/global import from its durable copy.
        await channelPage.reload(); await ready(channelPage)
        await unlockRecovered(channelPage, 'same-device cold retry after removing injected fault')
        await expect.poll(async () => {
          const wallets = await catalog(channelPage)
          const module = (await accountCall(channelPage, 'status')).modules?.find(item =>
            item.provider === 'wallets' && item.scope === 'global')
          return wallets.some(wallet => String(wallet.id) === String(renamedWallet.id) &&
            wallet.name === 'Pending channel recovery state') &&
            module?.write_allowed && module.backup_state === 'backed_up' && !module.last_error
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
        await captureRGB(channelPage, 'post-retry')
        const localRGBAfterRetry = await verify(channelPage, fixture.local_scopes)
        assert.deepEqual(localRGBAfterRetry, localRGBBeforeFault,
          'catalog import retry changed same-device RGB proofs or spending locks')
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
      await snapshotPage.getByPlaceholder(accountMessages.ui.setPassword).fill(replacementPassword)
      await snapshotPage.getByPlaceholder(accountMessages.ui.repeatPassword).fill(replacementPassword)
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
      await snapshotPage.getByRole('button', { name: accountMessages.ui.restoreAll, exact: true }).click()
      await expect.poll(() => new URL(snapshotPage.url()).hash, { timeout: 90000 }).toBe('#/unlock')
      await expect(snapshotPage.getByRole('button', { name: accountMessages.ui.restoreAll, exact: true })).toHaveCount(0)
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

  try {
    await runRgbPaidCases({ ...t, device: async (...args) => {
      const page = await t.device(...args)
      // Paid maintenance creates cold peers too; they must see the same
      // controlled Bitcoin facts as the other RGB recovery devices.
      await page.context().route('**/v3/bitcoin/**', bitcoinRoute)
      return page
    } }, fixture)
  }
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
      // Before activation, the new subaccount is durable locally and still
      // pending its first remote backup.
      await waitForAccountWrite(paidPage, accountCall)
      const local = await accountCall(paidPage, 'status')
      assert.equal(local.local_only, true)
      assert.equal(local.recovery_configured, false)
      const edited = (await catalog(paidPage)).find(wallet => wallet.fingerprint === root.fingerprint)
      const savings = edited?.accounts.find(account => account.index === 1)
      assert.equal(savings?.name, 'Upgrade savings')
      assert.equal(savings.did, 'did:upgrade:1')
      await accountCall(paidPage, 'confirmStorage', 'temporary')
      const temporary = await accountCall(paidPage, 'createRecovery', { password: fixture.password,
        wallets: [], recovery_mode: '2of2', questions })
      // Prepare the same temporary recovery session using the existing page's
      // bounded user retry after a safe CAS refusal. Do not replace material.
      for (let attempt = 0; attempt < 4; attempt++) {
        try {
          const result = await accountCall(paidPage, 'rehearse', temporary.session_id,
            questions.slice(0, 2).map(({ id, answer }) => ({ question_id: id, answer })), temporary.user_share, fixture.password)
          assert.equal(result.verified, true)
          break
        } catch (error) {
          if (!/publish account activation state: dkvs write conflict/.test(error.message) || attempt === 3) throw error
          assert.equal((await accountCall(paidPage, 'status')).recovery_configured, false, 'CAS refusal falsely configured recovery')
          console.log(JSON.stringify({ activationUserRetry: attempt + 1, outcome: 'conflict', entry: 'RGB paid setup' }))
          await waitForAccountWrite(paidPage, accountCall)
        }
      }
      await expect.poll(async () => {
        const status = await accountCall(paidPage, 'status')
        return accountReadiness(status).writeQualified && !status.pending_changes && !status.managed_data_dirty
      }, { timeout: 90000, message: 'temporary recovery must back up the edited catalog before the paid upgrade' }).toBe(true)
      const configured = await accountCall(paidPage, 'status')
      assert.equal(configured.active, true)
      assert.equal(configured.recovery_configured, true)
      assert.equal(configured.storage_mode, 'temporary')
      const beforeCatalog = await catalog(paidPage)
      await paidPage.reload(); await ready(paidPage); await unlock(paidPage, fixture.password)
      await paidPage.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
      await paidPage.getByRole('heading', { name: accountMessages.ui.maintainRecovery, exact: true }).waitFor()
      await expect(paidPage.getByRole('textbox', { name: accountMessages.ui.currentLocator, exact: true })).toHaveValue(temporary.locator)
      await paidPage.getByRole('button', { name: accountMessages.ui.reconfigure, exact: true }).click()
      await paidPage.getByRole('button', { name: accountMessages.ui.checkAccount, exact: true }).click()
      const dialog = paidPage.getByRole('dialog')
      await dialog.locator('input[type="password"]').fill(fixture.password)
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await dialog.waitFor({ state: 'hidden' })
      await paidPage.getByRole('button', { name: accountMessages.ui.modeSecure, exact: true }).click()
      const paid = (await accountCall(paidPage, 'getStorageOptions')).options.find(option => option.id === 'paid')
      assert.ok(paid?.available, 'the funded fixture must expose paid storage')
      await paidPage.getByRole('button', { name: accountMessages.ui.paidStorage, exact: false }).click()
      // The cold unlock starts a new DKVS session for this active account.
      await waitForAccountWrite(paidPage, accountCall)
      await paidPage.getByRole('button', { name: accountMessages.ui.reuseAutopay, exact: true }).click()
      await paidPage.getByRole('button', { name: accountMessages.ui.createBackup, exact: true }).waitFor()
      const authorization = await accountCall(paidPage, 'resumePendingStorageAuthorization')
      assert.equal(authorization.mode, 'paid')
      assert.equal(Boolean(authorization.transaction_id), false)
      await paidPage.getByRole('button', { name: accountMessages.ui.cancelSetup, exact: true }).click()
      assert.equal(await accountCall(paidPage, 'resumePendingStorageAuthorization'), null)
      assert.equal((await accountCall(paidPage, 'status')).public_locator, temporary.locator)
      await paidPage.getByRole('button', { name: accountMessages.ui.reuseAutopay, exact: true }).click()
      for (let i = 0; i < 3; i++) {
        await paidPage.getByPlaceholder(accountMessages.ui.answer, { exact: true }).nth(i).fill(questions[i].answer)
        await paidPage.getByPlaceholder(accountMessages.ui.repeatAnswer, { exact: true }).nth(i).fill(questions[i].confirmation)
      }
      await expect.poll(async () => {
        const status = await accountCall(paidPage, 'status')
        return !status.pending_changes && !status.managed_data_dirty
      }, { timeout: 90000 }).toBe(true)
      await paidPage.getByRole('button', { name: accountMessages.ui.createBackup, exact: true }).click()
      await dialog.locator('input[type="password"]').fill(fixture.password)
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await dialog.waitFor({ state: 'hidden' })
      const finalLocator = await paidPage.getByRole('textbox', { name: accountMessages.ui.finalLocator, exact: true }).inputValue()
      const userShare = await paidPage.getByRole('textbox', { name: accountMessages.ui.secretShare, exact: true }).inputValue()
      await paidPage.getByRole('button', { name: accountMessages.ui.startRehearsal, exact: true }).click()
      for (let i = 0; i < 2; i++) await paidPage.getByPlaceholder(`Answer to question ${i + 1}`, { exact: true }).fill(questions[i].answer)
      await paidPage.getByPlaceholder(accountMessages.ui.pasteShareAgain, { exact: true }).fill(userShare)
      await paidPage.getByRole('button', { name: accountMessages.ui.runRehearsal, exact: true }).click()
      await dialog.locator('input[type="password"]').fill(fixture.password)
      await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
      await dialog.waitFor({ state: 'hidden' })
      await paidPage.getByRole('heading', { name: accountMessages.ui.recoveryConfigured, exact: true }).waitFor()
      assert.equal((await accountCall(paidPage, 'status')).storage_mode, 'paid')
      await paidPage.reload(); await ready(paidPage); await unlock(paidPage, fixture.password)
      await paidPage.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
      await expect(paidPage.getByRole('textbox', { name: accountMessages.ui.currentLocator, exact: true })).toHaveValue(finalLocator)
      await expect(paidPage.getByRole('alert', { name: accountMessages.ui.backupStatusLabel, exact: true }))
        .toContainText('Data storage: AUTOPAY persistent storage')
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
      await paidPage.getByRole('heading', { name: accountMessages.ui.maintainRecovery, exact: true }).waitFor()
      const initial = await accountCall(paidPage, 'autopayStatus')
      const publicDiagnostic = data => appendFileSync(
        join(tmpdir(), `sat20wallet-pwa-e2e-funding-diagnostics-${process.pid}.jsonl`),
        JSON.stringify({ at: new Date().toISOString(), ...data }) + '\n', { mode: 0o600 })
      assert.ok(initial.required && initial.ready)
      assert.ok(Number.isSafeInteger(initial.blob_key_limit) && initial.blob_key_limit > 0)
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
          Action: action, Param: JSON.stringify(action === 'config' ? { amountPerBlock: String(rate), blobKeyLimit: initial.blob_key_limit } : {}),
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
        await dkvsFundingButton(paidPage, await accountCall(paidPage, 'autopayStatus')).click()
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
          await expect(dkvsFundingButton(paidPage, quote)).toBeEnabled()
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
          await expect(paidPage.getByRole('button', { name: accountMessages.ui.checkFunding, exact: true })).toBeEnabled({ timeout: 160000 })
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
        await paidPage.getByRole('button', { name: accountMessages.ui.confirmStorage, exact: true }).click()
        await paidPage.getByRole('dialog').getByRole('button', { name: 'Confirm', exact: true }).click()
        await paidPage.getByRole('dialog').waitFor({ state: 'hidden' })
      }
      const select200 = async () => {
        await paidPage.getByRole('button', { name: accountMessages.ui.reconfigure, exact: true }).click()
        await paidPage.getByRole('button', { name: accountMessages.ui.checkAccount, exact: true }).click()
        const dialog = paidPage.getByRole('dialog')
        await dialog.locator('input[type="password"]').fill(fixture.password)
        await dialog.getByRole('button', { name: 'Confirm', exact: true }).click()
        await dialog.waitFor({ state: 'hidden' })
        const paid = (await accountCall(paidPage, 'getStorageOptions')).options.find(option => option.id === 'paid')
        assert.ok(paid?.available, 'the funded fixture must expose paid storage')
        await paidPage.getByRole('button', { name: accountMessages.ui.paidStorage, exact: false }).click()
        await paidPage.getByLabel(accountMessages.ui.recordCount, { exact: true }).fill('200')
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
        // Delay contract state and execution-history queries after a real submission to
        // exercise the unresolved-receipt path. No SDK clock override.
        const tracker = paidPage.getByRole('region', { name: accountMessages.ui.fundingTracking })
        await expect(tracker).toContainText(accountMessages.ui.fundingPending, { timeout: 160000 })
        await expect(paidPage.getByRole('button', { name: accountMessages.ui.confirmStorage, exact: true })).toBeEnabled({ timeout: 160000 })
        const trackingText = await tracker.innerText()
        const txid = trackingText.match(/[0-9a-f]{64}/)?.[0]
        assert.ok(txid, 'the rendered receipt must retain the actual signed transaction ID')
        assert.ok(delayedQueries > 0)
        assert.equal(await accountCall(paidPage, 'resumePendingStorageAuthorization'), null,
          'pending payment cannot authorize paid recovery storage')
        await paidPage.reload(); await ready(paidPage); await unlock(paidPage, fixture.password)
        await paidPage.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
        await expect(paidPage.getByRole('region', { name: accountMessages.ui.fundingTracking })).toContainText(txid)
        await paidPage.getByRole('button', { name: accountMessages.ui.resumeFunding, exact: true }).click()
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
        let history = []
        await expect.poll(async () => {
          const current = []
          for (let start = 0; ;) {
            const response = await walletCall(paidPage, 'queryContract', {
              ContractType: 'template', Query: 'history', Contract: receipt.parameters.contract, Start: start, Limit: 100,
            })
            const batch = JSON.parse(response.result)
            current.push(...batch.data)
            start += batch.data.length
            if (!batch.data.length || start >= batch.total) break
          }
          history = current
          return current.filter(item => item.txid === txid && item.kind === 'invoke' && item.height > 0).length
        }, {
          timeout: 90000,
          intervals: [250, 500, 1000],
          message: 'real node history must contain exactly one confirmed original invocation',
        }).toBe(1)
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
        await paidPage.getByRole('button', { name: accountMessages.ui.checkFunding, exact: true }).click()
        await expect.poll(async () => {
          await paidPage.getByRole('button', { name: accountMessages.ui.checkFunding, exact: true }).click()
          return (await accountCall(paidPage, 'autopayStatus')).ready
        }, { timeout: 90000 }).toBe(true)
        await expect(paidPage.getByRole('region', { name: accountMessages.ui.fundingTracking })).toContainText(accountMessages.ui.fundingReady)
        await expect(paidPage.getByRole('region', { name: accountMessages.ui.fundingTracking })).toContainText(txid)
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
        await waitForAccountWrite(paidPage, accountCall)
        await confirmPaid()
        await paidPage.getByRole('button', { name: accountMessages.ui.createBackup, exact: true }).waitFor()
        assert.equal(submissions, 2, 'ready contract must reuse the original funding')
        await paidPage.getByRole('button', { name: accountMessages.ui.cancelSetup, exact: true }).click()
      } finally { await paidPage.context().unroute('**/*', fundingRoute) }
    })
  } finally { await paidPage.context().close() }
}

export async function runAutopayPwaReviewCases(t, fixture) {
  const { check, device, ready, accountCall, walletCall, questions, answers } = t
  const g = await device(), owner = await device(), password = fixture.password
  const poll = { timeout: 90000, intervals: [250, 500, 1000] }
  const waitInactive = async page => {
    await expect.poll(async () => {
      const s = await accountCall(page, 'status')
      return s.discovery_status === 'inactive' && s.local_only === true &&
        !s.recovery_configured && !s.activation_pending
    }, poll).toBe(true)
  }
  const waitBackup = async page => {
    await expect.poll(async () => {
      const s = await accountCall(page, 'status')
      return s.recovery_configured === true && !s.activation_pending &&
        s.dkvs_write_allowed === true && !s.pending_changes && !s.managed_data_dirty
    }, poll).toBe(true)
  }
  const mine = async () => {
    const r = await fetch(new URL('/mine', fixture.control_url), { method: 'POST', signal: AbortSignal.timeout(90000) })
    assert.equal(r.ok, true); assert.equal((await r.json()).mined, true)
  }
  const management = async () => {
    await g.evaluate(() => { location.hash = '#/wallet/setting/account-management' })
    await g.getByRole('heading', { name: accountMessages.ui.maintainRecovery, exact: true }).waitFor()
  }
  const cold = async () => {
    await g.reload(); await ready(g)
    await g.locator('input[type="password"]').fill(password)
    await g.locator('form button[type="submit"]').click()
    await expect.poll(() => g.evaluate(() => window.__SAT20_PWA_VERIFY__.useWalletStore().locked), poll).toBe(false)
    await management()
    await waitForAccountWrite(g, accountCall)
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
    await expect(dkvsFundingButton(g, await accountCall(g, 'autopayStatus'))).toBeVisible()
  }
  const fund = async () => {
    await dkvsFundingButton(g, await accountCall(g, 'autopayStatus')).click()
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
      await waitInactive(g)
      await waitInactive(owner)
      await accountCall(g, 'confirmStorage', 'temporary')
      const own = await accountCall(g, 'createRecovery', { password, wallets: [], recovery_mode: '2of2', questions })
      for (let attempt = 0; attempt < 4; attempt++) {
        try { await accountCall(g, 'rehearse', own.session_id, answers, own.user_share, password); break }
        catch (error) {
          if (!/publish account activation state: dkvs write conflict/.test(error.message) || attempt === 3) throw error
          assert.equal((await accountCall(g, 'status')).recovery_configured, false)
          await waitInactive(g)
        }
      }
      await waitBackup(g)
      const before = await accountCall(g, 'status'); assert.equal(before.storage_mode, 'temporary')
      await management()
      await g.getByRole('button', { name: accountMessages.ui.generateContact, exact: true }).click(); await passwordDialog()
      await expect.poll(() => g.locator('textarea[readonly]').count(), poll).toBe(2)
      const contact = JSON.parse(await g.locator('textarea[readonly]').nth(1).inputValue())
      await accountCall(owner, 'confirmStorage', 'temporary')
      const material = await accountCall(owner, 'createRecovery', { password, wallets: [], recovery_mode: '2of3', questions, guardian: contact })
      await g.getByPlaceholder(accountMessages.ui.pasteFriendSetup).fill(material.guardian_setup)
      await g.locator('select').selectOption('paid')
      await g.getByRole('button', { name: accountMessages.ui.acceptShare, exact: true }).click(); await passwordDialog()
      await expect(g.getByRole('dialog')).toContainText('100')
      await g.getByRole('dialog').getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect.poll(() => g.getByRole('button', { name: accountMessages.ui.acceptShare, exact: true }).isEnabled(), poll).toBe(true)
      await expect.poll(() => g.locator('textarea[readonly]').count(), poll).toBe(3)
      const receipt = await g.locator('textarea[readonly]').last().inputValue()
      await accountCall(owner, 'checkGuardianSetup', material.session_id, receipt)
      const request = (await accountCall(owner, 'createGuardianRequest', material.session_id)).request
      await cancel(); await fund(); await mine()
      await expect.poll(async () => (await accountCall(g, 'autopayStatus')).ready, poll).toBe(true)
      await cold()
      const after = await accountCall(g, 'status')
      for (const field of ['storage_mode', 'package_id', 'public_locator']) assert.equal(after[field], before[field])
      await g.getByPlaceholder(accountMessages.ui.pasteFriendRequest).fill(request)
      await g.getByRole('button', { name: accountMessages.ui.createResponse, exact: true }).click(); await passwordDialog()
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
          await expect(g.getByText(accountMessages.ui.transactionId.replace('{value}', pending.funding_transaction_id), { exact: true })).toBeVisible()
          await g.getByRole('button', { name: accountMessages.ui.checkFunding, exact: true }).click()
          await expect(g.getByRole('button', { name: accountMessages.ui.resumeFunding, exact: true })).toBeEnabled()
          assert.equal(attempts.length, 1)
          await g.getByRole('button', { name: accountMessages.ui.resumeFunding, exact: true }).click()
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
