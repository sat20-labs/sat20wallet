import { chromium } from '@playwright/test'

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/#/wallet'
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456'
const AMOUNT = process.env.SAT20_ACCOUNT_TOPUP_AMOUNT || '1000'
const DRY_RUN = process.env.SAT20_ACCOUNT_TOPUP_DRY_RUN === '1'
const SENDER = 'tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g'
const RECEIVER = 'tb1p6rk7tq5avpjmpudgut4vkhda5m8eetlzpqd6mrcr6u2022tdwfssfsra5x'

const main = async () => {
  const browser = await chromium.connectOverCDP(CDP)
  const context = browser.contexts()[0] || await browser.newContext()
  let page = context.pages().find((candidate) => candidate.url().startsWith(new URL(PWA_URL).origin))
  if (!page) page = await context.newPage()
  await page.goto(PWA_URL, { waitUntil: 'domcontentloaded' })
  await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__), null, { timeout: 180_000 })

  const result = await page.evaluate(async ({ password, amount, sender, receiver, dryRun }) => {
    const verify = window.__SAT20_PWA_VERIFY__
    const wallet = verify.useWalletStore()
    const sat20 = (await import('/utils/sat20.ts')).default
    const hashed = await verify.hashPassword(password)
    const unwrap = (tuple, operation) => {
      if (tuple?.[0]) throw new Error(`${operation}: ${tuple[0].message || tuple[0]}`)
      return tuple?.[1]
    }

    await wallet.setPassword(hashed)
    await wallet.setNetwork(verify.Network.TESTNET)
    await wallet.setChain(verify.Chain.SATNET)
    const [unlockError] = await wallet.unlockWallet(hashed)
    if (unlockError && !/already unlocked/i.test(String(unlockError.message || unlockError))) throw unlockError
    await wallet.syncWalletCatalog()
    const senderWallet = wallet.wallets.find((item) => item.accounts?.[0]?.address === sender)
    if (!senderWallet) throw new Error('sender test wallet is unavailable')
    await wallet.switchWallet(senderWallet.id)
    await wallet.switchToAccount(0)
    await wallet.setChain(verify.Chain.SATNET)
    unwrap(await sat20.switchAccount(0), 'switch sender account')

    const senderBefore = unwrap(await sat20.getAssetAmount_SatsNet(sender, 'brc20:f:sgas'), 'sender balance')
    const receiverBefore = unwrap(await sat20.getAssetAmount_SatsNet(receiver, 'brc20:f:sgas'), 'receiver balance')
    const sent = dryRun
      ? null
      : unwrap(await sat20.sendAssets_SatsNet(receiver, 'brc20:f:sgas', amount, ''), 'send sgas')
    return { sender, receiver, amount, senderBefore, receiverBefore, sent }
  }, { password: PASSWORD, amount: AMOUNT, sender: SENDER, receiver: RECEIVER, dryRun: DRY_RUN })

  console.log(JSON.stringify(result, null, 2))
}

main().then(() => process.exit(0)).catch((error) => {
  console.error(error)
  process.exit(1)
})
