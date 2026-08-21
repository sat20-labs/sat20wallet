import { chromium } from '@playwright/test'
import { chmodSync, renameSync, writeFileSync } from 'node:fs'

const cdp = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const origin = process.env.SAT20_DIAG_ORIGIN || 'http://localhost:5173'
const output = process.env.SAT20_DIAG_SNAPSHOT
const password = process.env.SAT20_DIAG_PASSWORD
const transferId = process.env.SAT20_ACCOUNT_REPAIR_TRANSFER_ID ||
  'rgb:csg:DIamFgCg-ZemqatB-7gXloCx-hvXtjI7-Tx8ri15-zcvAvZU#echo-atomic-polite'

if (!output || !output.startsWith('/private/tmp/')) {
  throw new Error('SAT20_DIAG_SNAPSHOT must be an explicit /private/tmp path')
}
if (!password) throw new Error('SAT20_DIAG_PASSWORD is required')

const browser = await chromium.connectOverCDP(cdp)
try {
  const context = browser.contexts()[0]
  let snapshot = null
  let lastError = null
  for (let attempt = 0; attempt < 5 && !snapshot; attempt++) {
    const page = context?.pages().find((candidate) => candidate.url().startsWith(origin))
    if (!page) throw new Error(`page not found for ${origin}`)
    try {
      snapshot = await page.evaluate(async ({ plainPassword, transferId }) => {
        const verify = window.__SAT20_PWA_VERIFY__
        if (!verify?.hashPassword) throw new Error('PWA password derivation helper is unavailable')
        const values = {}
        const counts = {
          profile: 0, status: 0, wallet: 0, projection: 0,
          engine: 0, rgbLocal: 0, locker: 0, lockTime: 0, ticker: 0,
          outbox: 0, targetPending: 0,
        }
        for (let index = 0; index < localStorage.length; index++) {
          const key = localStorage.key(index)
          if (!key) continue
          let category = ''
          if (key === 'prd-testnet-account-management-profile-v2') category = 'profile'
          else if (key === 'wallet-status') category = 'status'
          else if (key.startsWith('wallet-id-')) category = 'wallet'
          else if (key.startsWith('rgb11-engine-wallet-')) category = 'engine'
          else if (key.startsWith('rgb11-local-wallet-')) category = 'rgbLocal'
          else if (key.startsWith('rgb11-wallet-')) category = 'projection'
          else if (key.startsWith('prd-testnet-l-')) category = 'locker'
          else if (key.startsWith('prd-testnet-lt-')) category = 'lockTime'
          else if (key.startsWith('prd-testnet-t-')) category = 'ticker'
          else if (key.startsWith('dkvs-batch-outbox:')) category = 'outbox'
          const selected = category !== ''
          if (!selected) continue
          const value = localStorage.getItem(key)
          if (value !== null) {
            values[key] = value
            counts[category]++
            if (category === 'projection' &&
                key.endsWith(`-rgb11v2-pending-${transferId}`)) counts.targetPending++
          }
        }
        if (counts.profile !== 1 || counts.status !== 1 || counts.wallet < 1 ||
            counts.projection < 1 || counts.engine < 1 || counts.rgbLocal < 1 ||
            counts.targetPending !== 1 || counts.locker < 1) {
          throw new Error(`incomplete repair snapshot key classes: ${JSON.stringify(counts)}`)
        }
        return {
          origin: location.origin,
          passwordHash: await verify.hashPassword(plainPassword),
          keyClassCounts: counts,
          values,
        }
      }, { plainPassword: password, transferId })
    } catch (error) {
      lastError = error
      await new Promise((resolve) => setTimeout(resolve, 500))
    }
  }
  if (!snapshot) throw lastError || new Error('failed to read selected browser storage')

  if (snapshot.origin !== origin) throw new Error(`unexpected origin ${snapshot.origin}`)
  const temporary = `${output}.partial`
  writeFileSync(temporary, JSON.stringify(snapshot), { encoding: 'utf8', mode: 0o600 })
  chmodSync(temporary, 0o600)
  renameSync(temporary, output)
  chmodSync(output, 0o600)
} finally {
  // Disconnect by exiting; browser.close() would terminate the attached CDP browser.
}
process.exit(0)
