import { chromium } from '@playwright/test'

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/#/'
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456'

const topology = (state) => JSON.stringify(
  state.wallets.map(wallet => ({
    fingerprint: wallet.fingerprint || '',
    accounts: wallet.accounts.map(account => Number(account.index)).sort((a, b) => a - b),
  })).sort((a, b) => a.fingerprint.localeCompare(b.fingerprint)),
)

async function main() {
  const browser = await chromium.connectOverCDP(CDP)
  const context = browser.contexts()[0] || await browser.newContext()
  const page = await context.newPage()
  try {
    await page.goto(PWA_URL, { waitUntil: 'domcontentloaded' })
    await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__), null, { timeout: 180_000 })
    await page.evaluate(async (password) => {
      const verify = window.__SAT20_PWA_VERIFY__
      await verify.walletStorage.initializeState()
      const wallet = verify.useWalletStore()
      const [unlockError] = await wallet.unlockWallet(password)
      if (unlockError) throw unlockError
    }, PASSWORD)

    const readState = () => page.evaluate(() => {
      const wallet = window.__SAT20_PWA_VERIFY__.useWalletStore()
      return {
        network: wallet.network,
        rootAccountId: wallet.rootAccountId || '',
        walletId: String(wallet.walletId || ''),
        walletFingerprint: wallet.wallet?.fingerprint || '',
        accountIndex: Number(wallet.accountIndex || 0),
        wallets: JSON.parse(JSON.stringify(wallet.wallets)),
        address: wallet.address,
      }
    })

    const switchNetwork = async (network) => {
      const switchPromise = page.evaluate(async (target) => {
        const verify = window.__SAT20_PWA_VERIFY__
        const wallet = verify.useWalletStore()
        const desired = target === 'testnet' ? verify.Network.TESTNET : verify.Network.MAINNET
        if (wallet.network === desired) return true
        return await wallet.setNetwork(desired)
      }, network)
      const passwordInput = page.locator('input[type="password"][autocomplete="current-password"]')
      await passwordInput.waitFor({ state: 'visible', timeout: 30_000 })
      await passwordInput.fill(PASSWORD)
      await passwordInput.press('Enter')
      const changed = await switchPromise
      if (!changed) throw new Error(`network switch to ${network} did not complete`)
      await page.waitForFunction(
        (target) => window.__SAT20_PWA_VERIFY__.useWalletStore().network === target,
        network,
        { timeout: 180_000 },
      )
      return readState()
    }

    let baseline = await readState()
    if (!baseline.rootAccountId) throw new Error('root account id is missing before network switch')
    if (baseline.network !== 'testnet') baseline = await switchNetwork('testnet')
    const mainnet = await switchNetwork('mainnet')
    const restored = await switchNetwork('testnet')

    for (const [label, state] of [['mainnet', mainnet], ['restored-testnet', restored]]) {
      if (state.rootAccountId !== baseline.rootAccountId) {
        throw new Error(`${label} root account changed`)
      }
      if (state.walletFingerprint !== baseline.walletFingerprint || state.accountIndex !== baseline.accountIndex) {
        throw new Error(`${label} current wallet/account selection changed`)
      }
      if (topology(state) !== topology(baseline)) {
        throw new Error(`${label} wallet topology changed`)
      }
    }

    console.log(JSON.stringify({ baseline, mainnet, restored }, null, 2))
  } finally {
    await page.close()
  }
}

main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})
