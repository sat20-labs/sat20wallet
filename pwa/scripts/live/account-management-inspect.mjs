import { chromium } from '@playwright/test'

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const origins = (process.env.SAT20_ACCOUNT_INSPECT_ORIGINS || 'http://localhost:5174,http://127.0.0.1:5174')
  .split(',')
  .map((value) => value.trim())
  .filter(Boolean)

const browser = await chromium.connectOverCDP(CDP)
const context = browser.contexts()[0]
for (const origin of origins) {
  const page = context.pages().find((candidate) => candidate.url().startsWith(origin))
  if (!page) {
    console.log(JSON.stringify({ origin, error: 'page not found' }))
    continue
  }
  const result = await page.evaluate(async () => {
    const verify = window.__SAT20_PWA_VERIFY__
    await verify.walletStorage.initializeState()
    const wallet = verify.useWalletStore()
    const catalog = await wallet.syncWalletCatalog()
    let accountStatus = null
    if (globalThis.sat20account_wasm) {
      const response = await globalThis.sat20account_wasm.status('{}')
      accountStatus = response?.data || response
    }
    return {
      url: location.href,
      catalog: catalog.map((item) => ({
        id: item.id,
        name: item.name,
        accounts: item.accounts.map((account) => ({
          index: account.index,
          name: account.name,
          address: account.address,
        })),
      })),
      accountStatus,
    }
  })
  console.log(JSON.stringify({ origin, ...result }, null, 2))
}

process.exit(0)
