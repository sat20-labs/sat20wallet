import { chromium } from '@playwright/test'

const cdp = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const origin = process.env.SAT20_DIAG_ORIGIN || 'http://localhost:5173'
const password = process.env.SAT20_DIAG_PASSWORD || '123456'

const browser = await chromium.connectOverCDP(cdp)
const context = browser.contexts()[0]
const page = context?.pages().find((candidate) => candidate.url().startsWith(origin))
if (!page) throw new Error(`page not found for ${origin}`)

await page.reload({ waitUntil: 'domcontentloaded' })
await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__ && globalThis.sat20account_wasm), null, {
  timeout: 180_000,
})

const result = await page.evaluate(async (plainPassword) => {
  const verify = window.__SAT20_PWA_VERIFY__
  await verify.walletStorage.initializeState()
  const wallet = verify.useWalletStore()
	await wallet.syncWalletCatalog()
	const [unlockError] = await wallet.unlockWallet(plainPassword)
  if (unlockError && !/already unlocked/i.test(String(unlockError.message || unlockError))) {
    throw unlockError
  }

  const deadline = Date.now() + 15_000
  let status = null
  while (Date.now() < deadline) {
    const response = await globalThis.sat20account_wasm.status('{}')
    status = response?.data || response
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  return {
    active: Boolean(status?.active),
    stateSeq: Number(status?.state_seq || 0),
    pendingChanges: Number(status?.pending_changes || 0),
    storageMode: status?.storage_mode || '',
  }
}, password)

console.log(JSON.stringify(result))
process.exit(0)
