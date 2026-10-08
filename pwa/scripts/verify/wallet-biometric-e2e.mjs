import assert from 'node:assert/strict'
import { expect } from '@playwright/test'

export const requiredPwaBiometricCases = [
  'WebAuthn PWA: real PRF credential creation persists an encrypted wallet binding',
  'WebAuthn PWA: an actual authenticator assertion unlocks the same SDK wallet after reload',
  'WebAuthn PWA: password change clears the old binding and permits enrollment with the new password',
  'WebAuthn PWA: deleting the biometric binding persists and restores password-only unlock',
]

const poll = { timeout: 120000, intervals: [250, 500, 1000] }
const biometricButtonName = /^Use (?:Biometric Unlock|Touch ID|Face ID|Fingerprint)$/

export async function runPwaBiometricCases(t, fixture) {
  const { check, device, ready, walletCall } = t
  const actor = fixture.basicWallet
  assert.ok(actor?.mnemonic && actor.address && actor.password)
  const page = await device()
  const context = page.context()
  const cdp = await context.newCDPSession(page)
  let authenticatorId
  let createdCredential
  let originalIdentity
  let currentPassword = actor.password
  const identity = () => page.evaluate(() => {
    const store = window.__SAT20_PWA_VERIFY__.useWalletStore()
    return { address: store.address, publicKey: store.publicKey, walletId: String(store.walletId),
      accountIndex: Number(store.accountIndex), network: store.network, locked: store.locked }
  })
  // Read metadata only; credential private keys and wrapped password bytes
  // never enter the assertion output or artifact logs.
  const deviceCredentials = async () => {
    const result = await cdp.send('WebAuthn.getCredentials', { authenticatorId })
    return result.credentials.map(item => ({ id: item.credentialId, rpId: item.rpId,
      resident: item.isResidentCredential, signCount: item.signCount }))
  }
  const bindings = () => page.evaluate(async password => {
    const { biometricCredentialManager } = await import('/utils/biometricCredentials.ts')
    const active = await biometricCredentialManager.getActiveCredentials()
    return active.map(item => ({ id: item.id, active: item.isActive, prfMode: item.prfMode,
      encryptedLength: item.encryptedPassword.length, saltLength: item.salt.length, ivLength: item.iv.length,
      lastUsed: item.lastUsed || 0, containsPlainPassword: JSON.stringify(item).includes(password) }))
  }, actor.password)
  const security = async () => {
    await page.evaluate(() => { location.hash = '#/wallet/setting' })
    await page.getByRole('button', { name: /Security Options/ }).click()
    return page.getByRole('heading', { name: 'Security Options', exact: true }).locator('../..').locator('..')
  }
  const closeAlert = async message => {
    const alert = page.getByRole('dialog').filter({ hasText: message })
    await expect(alert).toBeVisible({ timeout: 120000 })
    await alert.getByRole('button', { name: '确定', exact: true }).click()
    await expect(alert).toBeHidden()
  }
  try {
    await check(requiredPwaBiometricCases[0], async () => {
      // WebAuthn RP IDs are domain names. Reuse the same loopback Vite server
      // under localhost, with this context's own databases and permission set.
      const url = new URL(page.url())
      assert.ok(['127.0.0.1', 'localhost'].includes(url.hostname))
      url.hostname = 'localhost'
      await page.goto(url.href, { waitUntil: 'domcontentloaded' })
      await ready(page)
      await cdp.send('WebAuthn.enable', { enableUI: false })
      const added = await cdp.send('WebAuthn.addVirtualAuthenticator', { options: {
        protocol: 'ctap2', ctap2Version: 'ctap2_1', transport: 'internal',
        hasResidentKey: true, hasUserVerification: true, isUserVerified: true,
        hasPrf: true, automaticPresenceSimulation: true,
      } })
      authenticatorId = added.authenticatorId
      // Unsupported Chrome/PRF is a failing capability requirement, never a
      // successful fallback to a fabricated navigator.credentials response.
      assert.equal(await page.evaluate(() => isSecureContext), true)
      assert.equal(await page.evaluate(() => PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable()), true)
      await page.evaluate(() => { location.hash = '#/import' })
      await page.getByRole('textbox', { name: 'Recovery Phrase', exact: true }).fill(actor.mnemonic)
      await page.getByLabel('New Wallet Password', { exact: true }).fill(actor.password)
      await page.getByLabel('Confirm Password', { exact: true }).fill(actor.password)
      await page.getByRole('button', { name: 'Import Wallet', exact: true }).click()
      await expect(page.getByRole('tab', { name: 'Bitcoin', exact: true })).toBeVisible({ timeout: 120000 })
      originalIdentity = await identity()
      assert.equal(originalIdentity.address, actor.address)
      assert.equal(originalIdentity.locked, false)
      assert.equal((await walletCall(page, 'getWalletPubkey', 0)).pubKey, originalIdentity.publicKey)
      await page.evaluate(() => {
        const create = navigator.credentials.create.bind(navigator.credentials)
        navigator.credentials.create = options => {
          window.biometricResidentKeyRequest = options.publicKey.authenticatorSelection.residentKey
          return create(options)
        }
      })
      const settings = await security()
      await expect(settings.getByRole('button', { name: 'Enable', exact: true })).toBeEnabled()
      await settings.getByRole('button', { name: 'Enable', exact: true }).click()
      const prompt = page.getByRole('dialog').filter({ has: page.locator('input[type="password"]') })
      await prompt.getByLabel('Password', { exact: true }).fill(actor.password)
      await prompt.getByRole('button', { name: 'Confirm', exact: true }).click()
      await closeAlert('Biometric credential created successfully! You can now use biometric authentication for quick unlock.')
      const saved = await bindings()
      assert.equal(saved.length, 1)
      assert.equal(saved[0].active, true)
      assert.equal(saved[0].containsPlainPassword, false)
      assert.ok(saved[0].encryptedLength > actor.password.length)
      assert.ok(saved[0].saltLength >= 43 && saved[0].ivLength >= 16)
      assert.ok(['eval', 'evalByCredential'].includes(saved[0].prfMode))
      const actual = await deviceCredentials()
      assert.equal(actual.length, 1)
      createdCredential = actual[0]
      assert.equal(createdCredential.rpId, 'localhost')
      const requestedResidentKey = await page.evaluate(() => window.biometricResidentKeyRequest)
      assert.ok(['required', 'discouraged'].includes(requestedResidentKey))
      assert.equal(createdCredential.resident, requestedResidentKey === 'required',
        'authenticator credential did not match the actual platform registration request')
      assert.equal(Buffer.from(createdCredential.id, 'base64').toString('base64url'), saved[0].id)
      await expect(settings.getByRole('button', { name: 'Delete Biometric Credential', exact: true })).toBeVisible()
    })

    await check(requiredPwaBiometricCases[1], async () => {
      const before = await deviceCredentials()
      await page.reload()
      await ready(page)
      await expect.poll(() => new URL(page.url()).hash.startsWith('#/unlock'), poll).toBe(true)
      assert.equal((await identity()).locked, true)
      const biometric = page.getByRole('button', { name: biometricButtonName })
      await expect(biometric).toBeVisible({ timeout: 120000 })
      await expect(page.locator('form input[type="password"]')).toBeHidden()
      await biometric.click()
      await expect.poll(async () => (await identity()).locked, poll).toBe(false)
      assert.deepEqual(await identity(), originalIdentity)
      assert.equal((await walletCall(page, 'getWalletAddress', 0)).address, actor.address)
      assert.equal((await walletCall(page, 'getWalletPubkey', 0)).pubKey, originalIdentity.publicKey)
      const after = await deviceCredentials()
      assert.equal(after.length, 1)
      assert.equal(after[0].id, createdCredential.id)
      assert.ok(after[0].signCount > before[0].signCount, 'unlock performed no actual authenticator assertion')
      const saved = await bindings()
      assert.equal(saved.length, 1)
      assert.ok(saved[0].lastUsed > 0)
    })

    await check(requiredPwaBiometricCases[2], async () => {
      const replacement = actor.password + '-changed'
      await page.evaluate(() => { location.hash = '#/wallet/setting/password' })
      await page.getByLabel('旧密码', { exact: true }).fill(currentPassword)
      await page.getByLabel('新密码', { exact: true }).fill(replacement)
      await page.getByLabel('确认新密码', { exact: true }).fill(replacement)
      await page.getByRole('button', { name: '确认修改', exact: true }).click()
      await expect.poll(bindings, poll).toEqual([])
      await page.reload(); await ready(page)
      await expect(page.getByRole('button', { name: biometricButtonName })).toBeHidden()
      const wrong = await page.evaluate(async password => {
        const [error] = await window.__SAT20_PWA_VERIFY__.sat20.unlockWallet(password)
        return Boolean(error)
      }, currentPassword)
      assert.equal(wrong, true)
      currentPassword = replacement
      await page.locator('form input[type="password"]').fill(currentPassword)
      await page.locator('form button[type="submit"]').click()
      await expect.poll(async () => (await identity()).locked, poll).toBe(false)
      assert.deepEqual(await identity(), originalIdentity)
      const settings = await security()
      await settings.getByRole('button', { name: 'Enable', exact: true }).click()
      const prompt = page.getByRole('dialog').filter({ has: page.locator('input[type="password"]') })
      await prompt.getByLabel('Password', { exact: true }).fill(currentPassword)
      await prompt.getByRole('button', { name: 'Confirm', exact: true }).click()
      await closeAlert('Biometric credential created successfully! You can now use biometric authentication for quick unlock.')
      assert.equal((await bindings()).length, 1)
      await page.reload(); await ready(page)
      await expect(page.getByRole('button', { name: biometricButtonName })).toBeVisible({ timeout: 120000 })
      await page.getByRole('button', { name: biometricButtonName }).click()
      await expect.poll(async () => (await identity()).locked, poll).toBe(false)
      assert.deepEqual(await identity(), originalIdentity)
    })

    await check(requiredPwaBiometricCases[3], async () => {
      const settings = await security()
      page.once('dialog', async dialog => {
        assert.equal(dialog.type(), 'confirm')
        assert.match(dialog.message(), /delete biometric credentials/i)
        await dialog.accept()
      })
      await settings.getByRole('button', { name: 'Delete Biometric Credential', exact: true }).click()
      await closeAlert('Biometric credentials deleted')
      assert.deepEqual(await bindings(), [])
      await page.reload()
      await ready(page)
      await expect.poll(() => new URL(page.url()).hash.startsWith('#/unlock'), poll).toBe(true)
      await expect(page.getByRole('button', { name: biometricButtonName })).toBeHidden()
      await expect(page.locator('form input[type="password"]')).toBeVisible()
      assert.equal((await identity()).locked, true)
      assert.deepEqual(await bindings(), [])
      await page.locator('form input[type="password"]').fill(currentPassword)
      await page.locator('form button[type="submit"]').click()
      await expect.poll(async () => (await identity()).locked, poll).toBe(false)
      assert.deepEqual(await identity(), originalIdentity)
      // The product deletes its local wallet binding. It does not claim to
      // erase a system passkey; the virtual device itself is cleanup below.
    })
  } finally {
    if (authenticatorId) await cdp.send('WebAuthn.removeVirtualAuthenticator', { authenticatorId }).catch(() => {})
    await cdp.detach().catch(() => {})
    await context.close()
  }
}
