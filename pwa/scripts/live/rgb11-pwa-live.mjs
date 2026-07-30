import { chromium } from '@playwright/test'
import fs from 'node:fs'

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/#/'
const LOCK_FILE = '/private/tmp/sat20-rgb11-pwa-live.lock'
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456'
const SENDER_MNEMONIC = 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire'
const RECEIVER_MNEMONIC = 'comfort very add tuition senior run eight snap burst appear exile dutch'
const SENDER_ADDRESS = 'tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g'
const RECEIVER_ADDRESS = 'tb1p6rk7tq5avpjmpudgut4vkhda5m8eetlzpqd6mrcr6u2022tdwfssfsra5x'
const ISSUE_AMOUNT = '100'
const TRANSFER_AMOUNT = '10'
const TEST_ACCOUNT_INDEX = Number(process.env.SAT20_TEST_ACCOUNT_INDEX || '0')
const DIAGNOSE_ONLY = process.env.SAT20_RGB11_DIAGNOSE_ONLY === '1'
const ENSURE_TEST_ACCOUNT = process.env.SAT20_ENSURE_TEST_ACCOUNT === '1'
const TRANSFER_TRANSPORT = process.env.SAT20_RGB11_TRANSFER_TRANSPORT || 'rgb-json-rpc'

const acquireProcessLock = () => {
  try {
    const previousPID = Number(fs.readFileSync(LOCK_FILE, 'utf8'))
    if (Number.isInteger(previousPID) && previousPID > 0) {
      try {
        process.kill(previousPID, 0)
        throw new Error(`RGB11 live verification is already running as PID ${previousPID}`)
      } catch (error) {
        if (error?.code !== 'ESRCH') throw error
      }
    }
    fs.unlinkSync(LOCK_FILE)
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error
  }
  fs.writeFileSync(LOCK_FILE, String(process.pid), { flag: 'wx' })
  const release = () => {
    try {
      if (fs.readFileSync(LOCK_FILE, 'utf8') === String(process.pid)) {
        fs.unlinkSync(LOCK_FILE)
      }
    } catch {}
  }
  process.once('exit', release)
  process.once('SIGINT', () => process.exit(130))
  process.once('SIGTERM', () => process.exit(143))
}

const primeTestnetEnvironment = async (page) => {
  const originURL = new URL('/manifest.webmanifest', PWA_URL)
  await page.goto(originURL.href, { waitUntil: 'domcontentloaded' })
  await page.evaluate(async () => {
    const db = await new Promise((resolve, reject) => {
      const request = indexedDB.open('sat20-wallet-pwa', 1)
      request.onupgradeneeded = () => {
        if (!request.result.objectStoreNames.contains('wallet-state')) {
          request.result.createObjectStore('wallet-state')
        }
      }
      request.onsuccess = () => resolve(request.result)
      request.onerror = () => reject(request.error)
    })
    await new Promise((resolve, reject) => {
      const transaction = db.transaction('wallet-state', 'readwrite')
      const store = transaction.objectStore('wallet-state')
      store.put(JSON.stringify('prd'), 'local:wallet_env')
      store.put(JSON.stringify('testnet'), 'local:wallet_network')
      store.put(JSON.stringify('btc'), 'local:wallet_chain')
      transaction.oncomplete = () => resolve()
      transaction.onerror = () => reject(transaction.error)
    })
    db.close()
  })
  await page.goto(PWA_URL, { waitUntil: 'domcontentloaded' })
}

const summarizeState = (state) => ({
  consistency: state?.consistency_status,
  backup: state?.backup_status,
  assets: (state?.assets || []).map((asset) => ({
    name: `${asset?.Name?.Protocol || ''}:${asset?.Name?.Type || ''}:${asset?.Name?.Ticker || ''}`,
    amount: String(asset?.Amount?.Value ?? asset?.Amount?.value ?? '0'),
  })),
  availableAssets: (state?.available_assets || []).map((asset) => ({
    name: `${asset?.Name?.Protocol || ''}:${asset?.Name?.Type || ''}:${asset?.Name?.Ticker || ''}`,
    amount: String(asset?.Amount?.Value ?? asset?.Amount?.value ?? '0'),
  })),
  pendingAssets: (state?.pending_assets || []).map((asset) => ({
    name: `${asset?.Name?.Protocol || ''}:${asset?.Name?.Type || ''}:${asset?.Name?.Ticker || ''}`,
    amount: String(asset?.Amount?.Value ?? asset?.Amount?.value ?? '0'),
  })),
  transfers: (state?.transfers || []).map((transfer) => ({
    id: transfer?.transfer_id,
    direction: transfer?.direction,
    status: transfer?.status,
    txid: transfer?.witness_txid,
  })),
})

async function main() {
  if (!['rgb-json-rpc', 'out-of-band'].includes(TRANSFER_TRANSPORT)) {
    throw new Error(`unsupported RGB11 live transfer transport ${TRANSFER_TRANSPORT}`)
  }
  acquireProcessLock()
  const launchBrowser = CDP === 'launch'
  const browser = launchBrowser
    ? await chromium.launch({ headless: true })
    : await chromium.connectOverCDP(CDP)
  const context = launchBrowser
    ? await browser.newContext()
    : browser.contexts()[0] || await browser.newContext()
  const page = context.pages().find((candidate) => candidate.url().startsWith('http://localhost:5173/#'))
    || await context.newPage()
  context.on('page', (openedPage) => {
    console.error(`[browser:page] opened ${openedPage.url()}`)
  })
  page.on('close', () => console.error('[browser:page] closed'))
  page.on('crash', () => console.error('[browser:page] crashed'))
  page.on('console', (message) => {
    const messageText = message.text()
    if (
      message.type() === 'error'
      || messageText.includes('[RGB11 live]')
      || messageText.includes('RGB11')
    ) {
      console.error(`[page:${message.type()}] ${messageText}`)
    }
  })
  page.on('pageerror', (error) => console.error(`[page:error] ${error.message}`))

  await primeTestnetEnvironment(page)
  await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__), null, { timeout: 180_000 })
  console.log('[RGB11 live] PWA verification API ready')

  const result = await page.evaluate(async ({
    password, senderMnemonic, receiverMnemonic, senderAddress, receiverAddress,
    issueAmount, transferAmount, testAccountIndex, diagnoseOnly, ensureTestAccount,
    transferTransport,
  }) => {
    const verify = window.__SAT20_PWA_VERIFY__
    if (!verify) throw new Error('PWA verification API is unavailable')
    const wallet = verify.useWalletStore()
    const { Chain, Network, sat20, walletStorage } = verify
    const hashed = await verify.hashPassword(password)
    const unwrap = (tuple, operation) => {
      if (tuple?.[0]) throw new Error(`${operation}: ${tuple[0].message || String(tuple[0])}`)
      return tuple?.[1]
    }
    const parseState = async () => {
      const state = await unwrap(await sat20.getRGB11State(), 'getRGB11State')
      return JSON.parse(state.state)
    }
    const findAssetAmount = (assets, expectedAssetName) => {
      const asset = (Array.isArray(assets) ? assets : []).find((item) => (
        `${item?.Name?.Protocol || ''}:${item?.Name?.Type || ''}:${item?.Name?.Ticker || ''}` === expectedAssetName
      ))
      return String(asset?.Amount?.Value ?? asset?.Amount?.value ?? '0')
    }
    const traceState = (label, state) => {
      console.info(`[RGB11 live] ${label}: ${JSON.stringify({
        consistency_status: state?.consistency_status,
        backup_status: state?.backup_status,
        backup_enabled: state?.backup_enabled,
        backup_mode: state?.backup_mode,
        backup_retention_ms: state?.backup_retention_ms,
        assets: state?.assets,
        transfers: state?.transfers,
      })}`)
    }
    const waitForWalletDataReady = async (label) => {
      const deadline = Date.now() + 120_000
      let state
      let previousStatus = ''
      let warningSince = 0
      while (Date.now() < deadline) {
        state = await parseState()
        if (state.backup_status !== previousStatus) {
          previousStatus = state.backup_status
          console.info(`[RGB11 live] ${label} wallet data status: ${previousStatus}`)
        }
        if (['synced', 'not_configured', 'offline'].includes(state.backup_status)) {
          return state
        }
        if (state.backup_status === 'conflict') {
          throw new Error(`${label}: wallet data conflict`)
        }
        if (state.backup_status === 'warning') {
          warningSince ||= Date.now()
          if (Date.now() - warningSince >= 10_000) break
        } else {
          warningSince = 0
        }
        await new Promise((resolve) => setTimeout(resolve, 1_000))
      }
      throw new Error(
        `${label}: timed out waiting for wallet data synchronization (${state?.backup_status || 'unknown'})`,
      )
    }
    const progress = (phase) => {
      window.localStorage.setItem('__sat20_rgb11_live_progress', phase)
      console.info(`[RGB11 live] ${phase}`)
    }
    const checkpoint = (value) => {
      window.localStorage.setItem('__sat20_rgb11_live_checkpoint', JSON.stringify(value))
    }
    const withTimeout = async (promise, operation, timeout = 60_000) => {
      let timer
      try {
        return await Promise.race([
          promise,
          new Promise((_, reject) => {
            timer = setTimeout(() => reject(new Error(`${operation}: timed out after ${timeout}ms`)), timeout)
          }),
        ])
      } finally {
        clearTimeout(timer)
      }
    }
    const acceptWhenBitcoinEvidenceIsReady = async (requestID, consignment) => {
      const timeout = 10 * 60_000
      const deadline = Date.now() + timeout
      while (Date.now() < deadline) {
        const [error, receipt] = await sat20.acceptRGB11Consignment(requestID, consignment)
        if (!error) return receipt
        if (!String(error.message || error).includes('outpoint status is unknown')) {
          throw error
        }
        await new Promise((resolve) => setTimeout(resolve, 10_000))
      }
      throw new Error(`acceptRGB11Consignment: Bitcoin evidence remained unavailable for ${timeout / 60_000} minutes`)
    }
    const receiveProxyWhenBitcoinEvidenceIsReady = async (requestID) => {
      const timeout = 10 * 60_000
      const deadline = Date.now() + timeout
      while (Date.now() < deadline) {
        const [error, receipt] = await sat20.receiveRGB11ProxyConsignment(requestID)
        if (!error) return receipt
        if (!/consignment is not available yet|witness is unresolved|outpoint status is unknown/i.test(
          String(error.message || error),
        )) {
          throw error
        }
        await new Promise((resolve) => setTimeout(resolve, 10_000))
      }
      throw new Error(`receiveRGB11ProxyConsignment: Bitcoin evidence remained unavailable for ${timeout / 60_000} minutes`)
    }
    const deliverProxyWithRetry = async (transferID) => {
      let lastError
      for (let attempt = 1; attempt <= 3; attempt++) {
        const [error, result] = await sat20.deliverAndBroadcastRGB11ProxyTransfer([transferID])
        if (!error) return result
        lastError = error
        if (!/deadline exceeded|timed out|timeout|connection reset|temporarily unavailable/i.test(
          String(error.message || error),
        ) || attempt === 3) {
          throw error
        }
        progress(`proxy delivery retry ${attempt}`)
        await new Promise((resolve) => setTimeout(resolve, 10_000))
      }
      throw lastError
    }
    const waitForSenderChangeProjection = async (assetName, expectedAmount) => {
      const timeout = 2 * 60_000
      const deadline = Date.now() + timeout
      let state
      while (Date.now() < deadline) {
        const [refreshError] = await sat20.refreshRGB11State()
        if (refreshError && !String(refreshError.message || refreshError).includes('outpoint status is unknown')) {
          throw refreshError
        }
        state = await parseState()
        if (findAssetAmount(state.assets, assetName) === expectedAmount) return state
        await new Promise((resolve) => setTimeout(resolve, 10_000))
      }
      throw new Error(`sender RGB11 change was not projected within ${timeout / 60_000} minutes`)
    }

    progress('initializing isolated wallet storage')
    await walletStorage.initializeState()
    await walletStorage.setValue('env', 'prd')
    await walletStorage.setValue('network', 'testnet')
    await walletStorage.setValue('chain', 'btc')

    await wallet.syncWalletCatalog().catch(() => [])
    const walletIDs = [senderAddress, receiverAddress].map((address) => {
      return wallet.wallets.find((item) => item.accounts.some((account) => account.address === address))?.id || ''
    })
    for (const [index, mnemonic] of [senderMnemonic, receiverMnemonic].entries()) {
      if (walletIDs[index]) continue
      const [error] = await wallet.importWallet(mnemonic, hashed)
      if (error) throw error
      walletIDs[index] = wallet.walletId
    }
    progress('two test wallets selected')
    progress('setting in-memory password')
    await wallet.setPassword(hashed)
    progress('switching wallet network to testnet')
    await wallet.setNetwork(Network.TESTNET)
    progress('switching wallet chain to bitcoin')
    await wallet.setChain(Chain.BTC)
    progress('unlocking wallet manager')
    await unwrap(await wallet.unlockWallet(hashed), 'unlockWallet')
    if (walletIDs.length !== 2 || walletIDs.some((id) => !id) || walletIDs[0] === walletIDs[1]) {
      throw new Error('failed to identify the two imported test wallets')
    }
    const selectTestAccount = async (index) => {
      await withTimeout(wallet.switchWallet(walletIDs[index]), `select wallet ${index}`)
      let selected = wallet.wallets.find((item) => item.id === walletIDs[index])
      let account = selected?.accounts.find((item) => item.index === testAccountIndex)
      if (!account && ensureTestAccount) {
        await withTimeout(
          wallet.addAccount(`Account ${testAccountIndex + 1}`, testAccountIndex),
          `create account wallet ${index}`,
        )
        selected = wallet.wallets.find((item) => item.id === walletIDs[index])
        account = selected?.accounts.find((item) => item.index === testAccountIndex)
      }
      if (!account?.address) throw new Error(`wallet ${index} has no test account ${testAccountIndex}`)
      await withTimeout(wallet.switchToAccount(testAccountIndex), `select account wallet ${index}`)
      await withTimeout(wallet.setChain(Chain.BTC), `select bitcoin chain wallet ${index}`)
      await unwrap(
        await withTimeout(sat20.switchAccount(testAccountIndex), `WASM switchAccount wallet ${index}`),
        `switchAccount wallet ${index}`,
      )
      return account.address
    }
    const addresses = [
      await selectTestAccount(0),
      await selectTestAccount(1),
    ]
    for (let index = 0; index < walletIDs.length; index++) {
      await selectTestAccount(index)
      await waitForWalletDataReady(`wallet ${index}`)
    }
    progress('wallet manager unlocked; initial wallet data synchronization completed')

    const switchWallet = async (index) => {
      progress(`switching frontend wallet ${index}`)
      await withTimeout(wallet.switchWallet(walletIDs[index]), `switch wallet ${index}`)
      progress(`switching frontend account ${index}`)
      await withTimeout(wallet.switchToAccount(testAccountIndex), `switch account store ${index}`)
      progress(`selecting bitcoin chain ${index}`)
      await withTimeout(wallet.setChain(Chain.BTC), `switch bitcoin chain ${index}`)
      progress(`switching WASM account ${index}`)
      await unwrap(
        await withTimeout(sat20.switchAccount(testAccountIndex), `WASM switchAccount wallet ${index}`),
        `switchAccount wallet ${index}`,
      )
      progress(`waiting for wallet data ${index}`)
      await waitForWalletDataReady(`wallet ${index}`)
    }

    if (diagnoseOnly) {
      const wallets = []
      for (let index = 0; index < walletIDs.length; index++) {
        await switchWallet(index)
        wallets.push({
          address: addresses[index],
          summary: await unwrap(await sat20.getAssetSummary(addresses[index]), `wallet ${index} asset summary`),
          state: await parseState(),
        })
      }
      return { diagnoseOnly: true, wallets }
    }

    await switchWallet(0)
    const senderBefore = await unwrap(await sat20.getAssetSummary(addresses[0]), 'sender asset summary')
    const ticker = `R${Date.now().toString(36).slice(-7)}`.toUpperCase()
    const issueResponse = await unwrap(await sat20.issueRGB11Asset({
      schema: 'NIA',
      ticker,
      name: `SAT20 RGB ${ticker}`,
      precision: 0,
      amounts: [issueAmount],
      min_confirmations: 1,
    }), 'issueRGB11Asset')
    const issued = JSON.parse(issueResponse.result)
    progress('RGB11 asset issued')
    const assetName = `${issued.asset_name.Protocol}:${issued.asset_name.Type}:${issued.asset_name.Ticker}`
    const senderIssuedState = await parseState()
    traceState('sender state after issue', senderIssuedState)
    if (findAssetAmount(senderIssuedState.assets, assetName) !== issueAmount) {
      throw new Error('sender issued RGB11 balance is missing before transfer')
    }

    await switchWallet(1)
    const importedResponse = await unwrap(await sat20.importRGB11Contract(issued.armor), 'importRGB11Contract')
    const imported = JSON.parse(importedResponse.result)
    const invoice = await unwrap(await sat20.createRGB11Invoice({
      mode: 'witness',
      ...(transferTransport === 'rgb-json-rpc' ? { transport_mode: transferTransport } : {}),
      contract_id: issued.contract_id,
      schema_id: issued.schema_id,
      amount_raw: transferAmount,
      assignment_name: 'assetOwner',
      expiry: Math.floor(Date.now() / 1000) + 24 * 60 * 60,
      witness_vout: 1,
    }), 'createRGB11Invoice')
    let externalInvoice = invoice.invoice
    if (transferTransport === 'out-of-band') {
      const [invoicePath, invoiceQuery = ''] = invoice.invoice.split('?', 2)
      const externalInvoiceQuery = invoiceQuery
        .split('&')
        .filter((item) => item && !item.startsWith('sat20_'))
        .join('&')
      externalInvoice = externalInvoiceQuery
        ? `${invoicePath}?${externalInvoiceQuery}`
        : invoicePath
    }
    progress('receiver imported contract and created invoice')

    await switchWallet(0)
    const senderBeforePrepareState = await parseState()
    traceState('sender state before prepare', senderBeforePrepareState)
    if (findAssetAmount(senderBeforePrepareState.assets, assetName) !== issueAmount) {
      throw new Error('sender RGB11 balance was overwritten before transfer preparation')
    }
    const preparedResponse = await unwrap(await sat20.prepareRGB11Transfer({
      invoice: externalInvoice,
      fee_rate: 1,
      min_confirmations: 1,
    }), 'prepareRGB11Transfer')
    const prepared = JSON.parse(preparedResponse.transfer)
    const transferID = prepared.state?.transfer_id
    if (!transferID) throw new Error('prepared transfer has no transfer id')
    if (prepared.state?.transport_mode !== transferTransport) {
      throw new Error(`expected ${transferTransport} transfer, got ${prepared.state?.transport_mode || 'unknown'}`)
    }
    const transferCheckpoint = {
      version: 1,
      ticker,
      assetName,
      contractId: issued.contract_id,
      schemaId: issued.schema_id,
      requestId: invoice.request_id || invoice.requestId,
      invoice: externalInvoice,
      transferId: transferID,
      transferTransport,
      senderWalletId: walletIDs[0],
      receiverWalletId: walletIDs[1],
      senderAddress: addresses[0],
      receiverAddress: addresses[1],
      issueAmount,
      transferAmount,
    }
    checkpoint(transferCheckpoint)
    progress(`sender prepared ${transferTransport} transfer`)

    const broadcast = transferTransport === 'rgb-json-rpc'
      ? await deliverProxyWithRetry(transferID)
      : await unwrap(await sat20.broadcastRGB11OutOfBand([transferID]), 'broadcastRGB11OutOfBand')
    checkpoint({ ...transferCheckpoint, txid: broadcast.txid })
    const expectedSenderAmount = (BigInt(issueAmount) - BigInt(transferAmount)).toString()
    progress(`transfer broadcast ${broadcast.txid}`)
    const senderAfter = await waitForSenderChangeProjection(assetName, expectedSenderAmount)
    traceState('sender state after change projection', senderAfter)

    await switchWallet(1)
    const receiveResult = transferTransport === 'rgb-json-rpc'
      ? await receiveProxyWhenBitcoinEvidenceIsReady(invoice.request_id || invoice.requestId)
      : await acceptWhenBitcoinEvidenceIsReady(
        invoice.request_id || invoice.requestId,
        prepared.recipient_consignment,
      )
    const receiverAcceptedState = await parseState()
    if (findAssetAmount(receiverAcceptedState.assets, assetName) !== transferAmount) {
      throw new Error('receiver total RGB11 balance does not include the accepted transfer')
    }
    if (findAssetAmount(receiverAcceptedState.available_assets, assetName) !== '0') {
      throw new Error('unconfirmed receiver RGB11 balance became available')
    }
    if (findAssetAmount(receiverAcceptedState.pending_assets, assetName) !== transferAmount) {
      throw new Error('unconfirmed receiver RGB11 balance was not marked pending')
    }
    const receiverSummary = await unwrap(
      await sat20.getAssetSummary(addresses[1]),
      'receiver available asset summary',
    )
    if ((receiverSummary.assets || []).some((asset) => (
      `${asset?.Name?.Protocol || ''}:${asset?.Name?.Type || ''}:${asset?.Name?.Ticker || ''}` === assetName
    ))) {
      throw new Error('unconfirmed RGB11 balance leaked into the general available asset summary')
    }
    const [pendingSendError] = await sat20.prepareRGB11Transfer({
      invoice: externalInvoice,
      fee_rate: 1,
      min_confirmations: 1,
    })
    if (!pendingSendError) {
      throw new Error('unconfirmed RGB11 balance was accepted as a spendable transfer input')
    }
    progress(`receiver accepted ${transferTransport} consignment`)
    const receiverAfter = await parseState()

    await switchWallet(0)
    const proxyAck = transferTransport === 'rgb-json-rpc'
      ? await unwrap(await sat20.fetchRGB11ProxyAck(transferID), 'fetchRGB11ProxyAck')
      : null
    if (proxyAck && (!proxyAck.available || !proxyAck.accepted)) {
      throw new Error(`proxy acknowledgment is not accepted: ${JSON.stringify(proxyAck)}`)
    }
    const senderAfterRestore = await parseState()
    if (findAssetAmount(senderAfterRestore.assets, assetName) !== expectedSenderAmount) {
      throw new Error('sender RGB11 state was overwritten after wallet switch and DKVS synchronization')
    }

    return {
      ticker,
      assetName,
      issueAmount,
      transferAmount,
      transferTransport,
      txid: broadcast.txid,
      receiveResult,
      proxyAck,
      sender: {
        address: addresses[0],
        indexerAssetCountBeforeIssue: senderBefore.assets?.length || 0,
        issued: senderIssuedState,
        afterBroadcast: senderAfter,
        afterRestore: senderAfterRestore,
      },
      receiver: {
        address: addresses[1],
        importedAllocations: imported.projected,
        afterAccept: receiverAcceptedState,
        afterBroadcast: receiverAfter,
      },
    }
  }, {
    password: PASSWORD,
    senderMnemonic: SENDER_MNEMONIC,
    receiverMnemonic: RECEIVER_MNEMONIC,
    senderAddress: SENDER_ADDRESS,
    receiverAddress: RECEIVER_ADDRESS,
    issueAmount: ISSUE_AMOUNT,
    transferAmount: TRANSFER_AMOUNT,
    testAccountIndex: TEST_ACCOUNT_INDEX,
    diagnoseOnly: DIAGNOSE_ONLY,
    ensureTestAccount: ENSURE_TEST_ACCOUNT,
    transferTransport: TRANSFER_TRANSPORT,
  })

  if (result.diagnoseOnly) {
    console.log(JSON.stringify(result, null, 2))
    return
  }

  console.log(JSON.stringify({
    ...result,
    sender: {
      ...result.sender,
      issued: summarizeState(result.sender.issued),
      afterBroadcast: summarizeState(result.sender.afterBroadcast),
      afterRestore: summarizeState(result.sender.afterRestore),
    },
    receiver: {
      ...result.receiver,
      afterAccept: summarizeState(result.receiver.afterAccept),
      afterBroadcast: summarizeState(result.receiver.afterBroadcast),
    },
  }, null, 2))
}

main().catch((error) => {
  console.error(error)
  process.exit(1)
})
