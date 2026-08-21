import { chromium } from '@playwright/test'
import { chmodSync, readFileSync, unlinkSync } from 'node:fs'
import { createHash } from 'node:crypto'

import { classifyLocalPatchHash } from './account-testnet-seq2-apply-local-state.mjs'

const cdp = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const origin = 'http://localhost:5173'
const patchPath = process.env.SAT20_ACCOUNT_REPAIR_PATCH
const applyToken = process.env.SAT20_ACCOUNT_REPAIR_APPLY_LOCAL

if (!patchPath || !patchPath.startsWith('/private/tmp/')) {
  throw new Error('SAT20_ACCOUNT_REPAIR_PATCH must be an explicit /private/tmp path')
}
if (applyToken !== 'APPLY-LOCAL-TESTNET-ACCOUNT-148CBE-SEQ2') {
  throw new Error('local profile patch is dry-run; exact apply token is required')
}
chmodSync(patchPath, 0o600)
const patch = JSON.parse(readFileSync(patchPath, 'utf8'))
if (patch.origin !== origin || typeof patch.key !== 'string' ||
    !patch.key.endsWith('account-management-profile-v2') ||
    typeof patch.expected_sha256 !== 'string' || typeof patch.value !== 'string' ||
    typeof patch.remote_state_hash !== 'string' || typeof patch.remote_blob_hash !== 'string') {
  throw new Error('invalid account repair patch')
}

const oldHash = patch.expected_sha256.toLowerCase()
const targetHash = createHash('sha256').update(Buffer.from(patch.value, 'base64')).digest('hex')
if (!/^[0-9a-f]{64}$/.test(oldHash) || !/^[0-9a-f]{64}$/.test(targetHash) ||
    oldHash === targetHash) {
  throw new Error('invalid account repair patch hashes')
}

function retryablePageError(error) {
  const message = String(error?.message || error).toLowerCase()
  return message.includes('execution context was destroyed') ||
    message.includes('cannot find context with specified id') ||
    message.includes('most likely because of a navigation') ||
    message.includes('target page, context or browser has been closed')
}

const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds))

const browser = await chromium.connectOverCDP(cdp)
try {
  const context = browser.contexts()[0]
  if (!context) throw new Error('CDP browser context is unavailable')
  let result = null
  let lastError = null
  for (let attempt = 0; attempt < 5 && !result; attempt++) {
    const page = context.pages().find((candidate) => candidate.url().startsWith(origin))
    if (!page) {
      lastError = new Error(`page not found for ${origin}`)
      await wait(400)
      continue
    }
    try {
      result = await page.evaluate(async (value) => {
        if (location.origin !== value.origin) throw new Error('repair origin changed')
        const hashValue = async (encoded) => {
          const bytes = Uint8Array.from(atob(encoded), (char) => char.charCodeAt(0))
          const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))
          return [...digest].map((part) => part.toString(16).padStart(2, '0')).join('')
        }
        const current = localStorage.getItem(value.key)
        if (current === null) return { state: 'missing' }
        const currentHash = (await hashValue(current)).toLowerCase()
        if (currentHash === value.targetHash) return { state: 'already_applied' }
        if (currentHash !== value.oldHash) return { state: 'third_hash' }

        localStorage.setItem(value.key, value.nextValue)
        const stored = localStorage.getItem(value.key)
        if (stored === null) return { state: 'verification_failed' }
        const storedHash = (await hashValue(stored)).toLowerCase()
        if (storedHash !== value.targetHash) return { state: 'verification_failed' }
        return { state: 'applied' }
      }, {
        origin: patch.origin,
        key: patch.key,
        oldHash,
        targetHash,
        nextValue: patch.value,
      })
    } catch (error) {
      lastError = error
      if (!retryablePageError(error) || attempt === 4) throw error
      await wait(400)
    }
  }
  if (!result) throw lastError || new Error('local profile patch state is unavailable')
  if (result.state === 'missing') throw new Error('local account profile is absent')
  if (result.state === 'third_hash' || result.state === 'verification_failed') {
    throw new Error('local account profile has an unapproved third hash; patch retained')
  }
  if (result.state !== 'applied' && result.state !== 'already_applied') {
    throw new Error(`unexpected local profile patch state ${result.state}`)
  }
  if (classifyLocalPatchHash(targetHash, oldHash, targetHash) !== 'already_applied') {
    throw new Error('local patch state classifier failed closed')
  }
  unlinkSync(patchPath)
  console.log(JSON.stringify({ state: result.state, key: patch.key,
    new_profile_sha256: targetHash, patch_deleted: true }))
} finally {
  // Disconnect by process exit; browser.close() would terminate the attached browser.
}
process.exit(0)
