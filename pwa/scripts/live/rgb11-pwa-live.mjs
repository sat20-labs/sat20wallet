import { chromium } from '@playwright/test'
import fs from 'node:fs'

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/#/'
const LOCK_FILE = '/private/tmp/sat20-rgb11-pwa-live.lock'
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456'
const TEST_WALLETS = [
  {
    mnemonic: 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire',
    address: 'tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g',
  },
  {
    mnemonic: 'comfort very add tuition senior run eight snap burst appear exile dutch',
    address: 'tb1p6rk7tq5avpjmpudgut4vkhda5m8eetlzpqd6mrcr6u2022tdwfssfsra5x',
  },
]
const REVERSE_WALLETS = process.env.SAT20_RGB11_REVERSE_WALLETS === '1'
const [SENDER_WALLET, RECEIVER_WALLET] = REVERSE_WALLETS
  ? [...TEST_WALLETS].reverse()
  : TEST_WALLETS
const SENDER_MNEMONIC = SENDER_WALLET.mnemonic
const RECEIVER_MNEMONIC = RECEIVER_WALLET.mnemonic
const SENDER_ADDRESS = SENDER_WALLET.address
const RECEIVER_ADDRESS = RECEIVER_WALLET.address
const ISSUE_AMOUNT = '100'
const TRANSFER_AMOUNT = '10'
const TEST_ACCOUNT_INDEX = Number(process.env.SAT20_TEST_ACCOUNT_INDEX || '0')
const SENDER_ACCOUNT_INDEX = Number(process.env.SAT20_RGB11_SENDER_ACCOUNT_INDEX || TEST_ACCOUNT_INDEX)
const RECEIVER_ACCOUNT_INDEX = Number(process.env.SAT20_RGB11_RECEIVER_ACCOUNT_INDEX || TEST_ACCOUNT_INDEX)
const DIAGNOSE_ONLY = process.env.SAT20_RGB11_DIAGNOSE_ONLY === '1'
const ENSURE_TEST_ACCOUNT = process.env.SAT20_ENSURE_TEST_ACCOUNT === '1'
const TRANSFER_TRANSPORT = process.env.SAT20_RGB11_TRANSFER_TRANSPORT || 'address'
const PROXY_ENDPOINT = process.env.SAT20_RGB11_PROXY_ENDPOINT || ''
const CANCEL_OUT_OF_BAND_TRANSFER_ID = process.env.SAT20_RGB11_CANCEL_OUT_OF_BAND_TRANSFER_ID || ''
const CANCEL_EXPIRED_TRANSFER_ID = process.env.SAT20_RGB11_CANCEL_EXPIRED_TRANSFER_ID || ''
const REUSE_ASSET_NAME = process.env.SAT20_RGB11_REUSE_ASSET_NAME || ''
const RESUME_PENDING = process.env.SAT20_RGB11_RESUME_PENDING === '1'
const INSPECT_PENDING = process.env.SAT20_RGB11_INSPECT_PENDING === '1'
const SYNC_MAILBOX_ONLY = process.env.SAT20_RGB11_SYNC_MAILBOX_ONLY === '1'
const SYNC_MAILBOX_WALLET_INDEX = Number(process.env.SAT20_RGB11_SYNC_MAILBOX_WALLET_INDEX || '1')
const RESUME_PROXY_REQUEST_ID = process.env.SAT20_RGB11_RESUME_PROXY_REQUEST_ID || ''
const RESET_TEST_STORAGE = process.env.SAT20_RESET_TEST_STORAGE === '1'
const USE_CHECKPOINT_WALLETS = process.env.SAT20_RGB11_USE_CHECKPOINT_WALLETS === '1'
const VERIFY_RECOVERY_ASSET = process.env.SAT20_RGB11_VERIFY_RECOVERY_ASSET || ''
const VERIFY_RECOVERY_SENDER_AMOUNT = process.env.SAT20_RGB11_VERIFY_RECOVERY_SENDER_AMOUNT || ''
const VERIFY_RECOVERY_RECEIVER_AMOUNT = process.env.SAT20_RGB11_VERIFY_RECOVERY_RECEIVER_AMOUNT || ''

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
  initialized: state?.initialized,
  syncStatus: state?.sync_status,
  consistencyStatus: state?.consistency_status,
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
  if (!['address', 'rgb-json-rpc', 'out-of-band'].includes(TRANSFER_TRANSPORT)) {
    throw new Error(`unsupported RGB11 live transfer transport ${TRANSFER_TRANSPORT}`)
  }
  if (TRANSFER_TRANSPORT === 'rgb-json-rpc' && !PROXY_ENDPOINT) {
    throw new Error('SAT20_RGB11_PROXY_ENDPOINT is required for rgb-json-rpc transport')
  }
  acquireProcessLock()
  const launchBrowser = CDP === 'launch'
  const browser = launchBrowser
    ? await chromium.launch({ headless: true })
    : await chromium.connectOverCDP(CDP)
  let verificationPage
  try {
  const context = launchBrowser
    ? await browser.newContext()
    : browser.contexts()[0] || await browser.newContext()
  const page = await context.newPage()
  verificationPage = page
  if (RESET_TEST_STORAGE) {
    const session = await context.newCDPSession(page)
    await session.send('Storage.clearDataForOrigin', {
      origin: new URL(PWA_URL).origin,
      storageTypes: 'all',
    })
    console.log(`[RGB11 live] cleared test storage for ${new URL(PWA_URL).origin}`)
  }
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
    issueAmount, transferAmount, accountIndexes, diagnoseOnly, ensureTestAccount,
    transferTransport, proxyEndpoint, reuseAssetName,
    cancelOutOfBandTransferID, cancelExpiredTransferID, resumePending, inspectPending,
    syncMailboxOnly, syncMailboxWalletIndex,
    resumeProxyRequestID, useCheckpointWallets, verifyRecoveryAsset,
    verifyRecoverySenderAmount, verifyRecoveryReceiverAmount,
  }) => {
    const verify = window.__SAT20_PWA_VERIFY__
    if (!verify) throw new Error('PWA verification API is unavailable')
    const wallet = verify.useWalletStore()
    const { Chain, Network, rgb11Address, sat20, walletStorage } = verify
		const credential = password
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
        initialized: state?.initialized,
        sync_status: state?.sync_status,
        consistency_status: state?.consistency_status,
        assets: state?.assets,
        available_assets: state?.available_assets,
        pending_assets: state?.pending_assets,
        outputs: state?.outputs,
        proofs: state?.proofs,
        transfers: state?.transfers,
      })}`)
    }
    const waitForWalletDataReady = async (
      label,
      allowWarningForDiagnosis = false,
      allowPending = false,
    ) => {
      const deadline = Date.now() + 120_000
      let state
      let previousStatus = ''
      while (Date.now() < deadline) {
        state = await parseState()
        const syncStatus = state?.sync_status
        const consistencyStatus = state?.consistency_status
        const status = `${state?.initialized === true ? 'initialized' : 'uninitialized'}/${syncStatus || 'unknown'}/${consistencyStatus || 'unknown'}`
        if (status !== previousStatus) {
          previousStatus = status
          console.info(`[RGB11 live] ${label} RGB11 state: ${status}`)
        }

        if (syncStatus === 'error') {
          traceState(`${label} sync error`, state)
          throw new Error(`${label}: RGB11 synchronization failed`)
        }
        if (consistencyStatus === 'broken') {
          traceState(`${label} consistency broken`, state)
          throw new Error(`${label}: RGB11 state consistency is broken`)
        }
        if (!['idle', 'syncing', 'reorging'].includes(syncStatus)) {
          traceState(`${label} unknown sync status`, state)
          throw new Error(`${label}: unknown RGB11 sync status ${syncStatus || 'undefined'}`)
        }
        if (!['ok', 'warning'].includes(consistencyStatus)) {
          traceState(`${label} unknown consistency status`, state)
          throw new Error(`${label}: unknown RGB11 consistency status ${consistencyStatus || 'undefined'}`)
        }

        if (state?.initialized === true && allowPending &&
          ['idle', 'syncing'].includes(syncStatus) &&
          ['ok', 'warning'].includes(consistencyStatus)) {
          return state
        }
        if (state?.initialized === true && syncStatus === 'idle' && consistencyStatus === 'ok') {
          return state
        }
        if (state?.initialized === true && syncStatus === 'idle' && consistencyStatus === 'warning') {
          traceState(`${label} consistency warning`, state)
          if (allowWarningForDiagnosis) return state
          throw new Error(`${label}: RGB11 state consistency warning blocks live transactions`)
        }
        await new Promise((resolve) => setTimeout(resolve, 1_000))
      }
      traceState(`${label} readiness timeout`, state)
      throw new Error(
        `${label}: timed out waiting for RGB11 readiness (${previousStatus || 'unknown'})`,
      )
    }
    const progress = (phase) => {
      window.localStorage.setItem('__sat20_rgb11_live_progress', phase)
      console.info(`[RGB11 live] ${phase}`)
    }
    const saveTransferCheckpoint = (value) => {
      const addressCheckpoint = value?.transferTransport === 'address' && value?.receiverAddress
      const traditionalCheckpoint = value?.requestId && value?.invoice
      if (value?.kind !== 'rgb11-prepared-transfer' || value?.version !== 2 ||
        !value?.transferId || value.transferId !== value.preparedTransferId ||
        (!addressCheckpoint && !traditionalCheckpoint)) {
        throw new Error('refusing to save invalid RGB11 prepared-transfer checkpoint')
      }
      window.localStorage.setItem('__sat20_rgb11_live_checkpoint', JSON.stringify(value))
    }
    const clearTransferCheckpoint = () => {
      window.localStorage.removeItem('__sat20_rgb11_live_checkpoint')
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
    const permanentResumeErrorPattern = /key not found|record not found|invalid transfer|state mismatch|does not match|cannot resume|inconsistent/i
    const transientResumeErrorPattern = /dkvs path has not completed|dkvs[^\n]*(?:not ready|not synced|initializing)|not available yet|deadline exceeded|timed out|timeout|connection reset|connection refused|temporarily unavailable|network|fetch failed|witness is unresolved|outpoint status is unknown/i
    const missingRecordPattern = /key not found|record not found/i
    const retryResumeTuple = async (operation, invoke, attempts = 60) => {
      let lastError
      for (let attempt = 1; attempt <= attempts; attempt++) {
        const [error, value] = await invoke()
        if (!error) return value
        lastError = error
        const message = String(error.message || error)
        if (permanentResumeErrorPattern.test(message) ||
          !transientResumeErrorPattern.test(message) || attempt === attempts) {
          throw error
        }
        progress(`${operation} retry ${attempt}`)
        await new Promise((resolve) => setTimeout(resolve, 2_000))
      }
      throw lastError
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
    const waitForReceiverPendingProjection = async (assetName, expectedAmount) => {
      const timeout = 10 * 60_000
      const deadline = Date.now() + timeout
      let state
      while (Date.now() < deadline) {
        const [refreshError] = await sat20.refreshRGB11State()
        if (refreshError && !/witness is unresolved|outpoint status is unknown/i.test(
          String(refreshError.message || refreshError),
        )) {
          throw refreshError
        }
        state = await parseState()
        if (findAssetAmount(state.assets, assetName) === expectedAmount &&
          findAssetAmount(state.pending_assets, assetName) === expectedAmount &&
          findAssetAmount(state.available_assets, assetName) === '0') {
          return state
        }
        await new Promise((resolve) => setTimeout(resolve, 10_000))
      }
      throw new Error(`receiver pending RGB11 transfer was not projected within ${timeout / 60_000} minutes`)
    }
    const syncAddressMailboxUntilProcessed = async (transferID, assetName, expectedAmount) => {
      const timeout = 10 * 60_000
      const deadline = Date.now() + timeout
      let lastSync
      while (Date.now() < deadline) {
        const response = await unwrap(
          await rgb11Address.syncMailbox({}),
          'syncRGB11AddressMailbox',
        )
        lastSync = JSON.parse(response.result)
        if ((lastSync.invalid || 0) > 0) {
          throw new Error(`RGB11 mailbox rejected the delivery: ${JSON.stringify(lastSync)}`)
        }
        const state = await parseState()
        const transfer = (state.transfers || []).find((item) => item?.transfer_id === transferID)
        const projected = findAssetAmount(state.assets, assetName) === expectedAmount &&
          findAssetAmount(state.pending_assets, assetName) === expectedAmount &&
          findAssetAmount(state.available_assets, assetName) === '0'
        const acknowledged = transfer?.delivery_acknowledged === true || transfer?.ack_status === 'ack-sent'
        if ((lastSync.received || 0) > 0 ||
          ((lastSync.already_done || 0) > 0 && projected && acknowledged)) {
          return response
        }
        progress(`waiting for RGB11 mailbox delivery ${transferID}`)
        await new Promise((resolve) => setTimeout(resolve, 10_000))
      }
      throw new Error(
        `RGB11 mailbox did not process delivery ${transferID} within ${timeout / 60_000} minutes: ${JSON.stringify(lastSync || {})}`,
      )
    }

    progress('initializing isolated wallet storage')
    await walletStorage.initializeState()
    await walletStorage.setValue('env', 'prd')
    await walletStorage.setValue('network', 'testnet')
    await walletStorage.setValue('chain', 'btc')

	const [sessionUnlockError] = await wallet.unlockWallet(credential)
	if (sessionUnlockError && !/no wallet/i.test(String(sessionUnlockError.message || sessionUnlockError))) {
		throw sessionUnlockError
	}
    await wallet.setNetwork(Network.TESTNET)
    await wallet.setChain(Chain.BTC)
    await wallet.syncWalletCatalog().catch(() => [])
    const checkpointRawForWalletSelection = (
      resumePending || inspectPending || cancelExpiredTransferID || useCheckpointWallets
    )
      ? window.localStorage.getItem('__sat20_rgb11_live_checkpoint')
      : ''
    if ((resumePending || inspectPending || cancelExpiredTransferID || useCheckpointWallets)
      && !checkpointRawForWalletSelection) {
      throw new Error('RGB11 operation requires an existing wallet checkpoint')
    }
    const checkpointForWalletSelection = checkpointRawForWalletSelection
      ? JSON.parse(checkpointRawForWalletSelection)
      : undefined
    const checkpointWalletIDs = checkpointForWalletSelection
      ? [checkpointForWalletSelection.senderWalletId, checkpointForWalletSelection.receiverWalletId]
      : undefined
    const checkpointAddresses = checkpointForWalletSelection
      ? [checkpointForWalletSelection.senderAddress, checkpointForWalletSelection.receiverAddress]
      : undefined
    if (checkpointWalletIDs?.some((id) => id === undefined || id === null || String(id) === '')) {
      throw new Error('RGB11 checkpoint requires senderWalletId and receiverWalletId')
    }

    const expectedAddresses = [senderAddress, receiverAddress]
    const walletIDs = expectedAddresses.map((address, index) => {
      if (checkpointWalletIDs) {
        const checkpointWalletID = checkpointWalletIDs[index]
        const checkpointAddress = checkpointAddresses[index]
        if (!checkpointAddress || checkpointAddress !== address) {
          throw new Error(
            `RGB11 checkpoint wallet ${index} address ${checkpointAddress || 'missing'} does not match configured ${address}`,
          )
        }
        const selected = wallet.wallets.find((item) => String(item.id) === String(checkpointWalletID))
        if (!selected) {
          throw new Error(`RGB11 checkpoint wallet ${index} id ${checkpointWalletID} no longer exists`)
        }
        const selectedAccount = selected.accounts.find((account) => account.index === accountIndexes[index])
        if (selectedAccount?.address && selectedAccount.address !== checkpointAddress) {
          throw new Error(
            `RGB11 checkpoint wallet ${index} id ${checkpointWalletID} catalog address ${selectedAccount.address} does not match ${checkpointAddress}`,
          )
        }
        return selected.id
      }

      const matches = wallet.wallets.filter((item) => (
        item.accounts.some((account) => account.address === address)
      ))
      if (matches.length > 1) {
        throw new Error(`wallet ${index} address is ambiguous across IDs ${matches.map((item) => item.id).join(', ')}`)
      }
      return matches[0]?.id || ''
    })
    for (const [index, mnemonic] of [senderMnemonic, receiverMnemonic].entries()) {
      if (walletIDs[index]) continue
		// Importing the root wallet can immediately restore other account-managed
		// wallets. Refresh the catalog before each import so the live test reuses
		// that authoritative recovery result instead of attempting a duplicate
		// mnemonic import from the wallet list captured before recovery completed.
		await wallet.syncWalletCatalog().catch(() => [])
		const restoredMatches = wallet.wallets.filter((item) => (
			item.accounts.some((account) => account.address === expectedAddresses[index])
		))
		if (restoredMatches.length > 1) {
			throw new Error(
				`wallet ${index} address is ambiguous after managed recovery across IDs ${restoredMatches.map((item) => item.id).join(', ')}`,
			)
		}
		if (restoredMatches.length === 1) {
			walletIDs[index] = restoredMatches[0].id
			continue
		}
			const [error] = await wallet.importWallet(mnemonic, credential)
      if (error) throw error
      walletIDs[index] = wallet.walletId
    }
    console.info(`[RGB11 live] selected wallet IDs: sender=${walletIDs[0]}, receiver=${walletIDs[1]}`)
    progress('two test wallets selected')
    progress('wallet manager and catalog normalized before selection')
    if (walletIDs.length !== 2 || walletIDs.some((id) => !id) || walletIDs[0] === walletIDs[1]) {
      throw new Error('failed to identify the two imported test wallets')
    }
    const selectTestAccount = async (index) => {
      const testAccountIndex = accountIndexes[index]
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
      if (!checkpointWalletIDs && !account?.address) {
        throw new Error(`wallet ${index} has no test account ${testAccountIndex}`)
      }
      await withTimeout(wallet.switchToAccount(testAccountIndex), `select account wallet ${index}`)
      await withTimeout(wallet.setChain(Chain.BTC), `select bitcoin chain wallet ${index}`)
      await unwrap(
        await withTimeout(sat20.switchAccount(testAccountIndex), `WASM switchAccount wallet ${index}`),
        `switchAccount wallet ${index}`,
      )
      const actualAddressResult = await unwrap(
        await withTimeout(sat20.getWalletAddress(testAccountIndex), `get wallet address ${index}`),
        `getWalletAddress wallet ${index}`,
      )
      const actualAddress = actualAddressResult?.address
      const expectedAddress = checkpointAddresses?.[index] || expectedAddresses[index]
      if (!actualAddress || actualAddress !== expectedAddress) {
        throw new Error(
          `wallet ${index} id ${walletIDs[index]} derived address ${actualAddress || 'missing'} does not match ${expectedAddress}`,
        )
      }
      console.info(`[RGB11 live] wallet ${index} id ${walletIDs[index]} derived address verified: ${actualAddress}`)
      return actualAddress
    }
    const addresses = [
      await selectTestAccount(0),
      await selectTestAccount(1),
    ]
    const allowInitialPending = resumePending || inspectPending || syncMailboxOnly ||
      Boolean(resumeProxyRequestID) || Boolean(cancelOutOfBandTransferID) ||
      Boolean(cancelExpiredTransferID) || Boolean(verifyRecoveryAsset)
    for (let index = 0; index < walletIDs.length; index++) {
      await selectTestAccount(index)
      await waitForWalletDataReady(`wallet ${index}`, diagnoseOnly, allowInitialPending)
    }
    progress('wallet manager unlocked; initial wallet data synchronization completed')

    const switchWallet = async (index, allowPending = false) => {
      const testAccountIndex = accountIndexes[index]
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
      await waitForWalletDataReady(`wallet ${index}`, diagnoseOnly, allowPending)
    }

    if (verifyRecoveryAsset) {
      if (!verifyRecoverySenderAmount || !verifyRecoveryReceiverAmount) {
        throw new Error('cold RGB11 recovery verification requires both expected wallet amounts')
      }
      const recovered = []
      for (const [index, expectedAmount] of [
        verifyRecoverySenderAmount,
        verifyRecoveryReceiverAmount,
      ].entries()) {
        await switchWallet(index, true)
        const state = await parseState()
        const actualAmount = findAssetAmount(state.assets, verifyRecoveryAsset)
        if (actualAmount !== expectedAmount) {
          throw new Error(
            `cold RGB11 recovery wallet ${index} amount=${actualAmount} want=${expectedAmount} asset=${verifyRecoveryAsset}`,
          )
        }
        if ((state.ticker_infos || []).length === 0 ||
          (state.proofs || []).length === 0 || (state.transfers || []).length === 0) {
          throw new Error(`cold RGB11 recovery wallet ${index} is missing ticker/proof/transfer state`)
        }
        recovered.push(state)
      }
      return { verifiedColdRecovery: true, assetName: verifyRecoveryAsset, wallets: recovered }
    }

    if (cancelExpiredTransferID) {
      await switchWallet(0, true)
      await unwrap(
        await sat20.cancelExpiredRGB11Transfer(cancelExpiredTransferID),
        'cancelExpiredRGB11Transfer',
      )
      return { cancelledExpired: cancelExpiredTransferID, sender: await parseState() }
    }

    if (syncMailboxOnly) {
      if (!Number.isInteger(syncMailboxWalletIndex) || syncMailboxWalletIndex < 0 || syncMailboxWalletIndex > 1) {
        throw new Error(`invalid mailbox wallet index ${syncMailboxWalletIndex}`)
      }
      await switchWallet(syncMailboxWalletIndex, true)
      const receiveResult = await unwrap(
        await rgb11Address.syncMailbox({}),
        'syncRGB11AddressMailbox',
      )
      return {
        syncedMailboxOnly: true,
        receiveResult: JSON.parse(receiveResult.result),
        receiver: await parseState(),
      }
    }

    if (cancelOutOfBandTransferID) {
      await switchWallet(0, true)
      await unwrap(
        await sat20.cancelRGB11OutOfBandTransfer(cancelOutOfBandTransferID),
        'cancelRGB11OutOfBandTransfer',
      )
      return { cancelled: cancelOutOfBandTransferID, sender: await parseState() }
    }

    if (resumeProxyRequestID) {
      await switchWallet(1, true)
      const receiveResult = await receiveProxyWhenBitcoinEvidenceIsReady(resumeProxyRequestID)
      return {
        resumedProxyRequest: resumeProxyRequestID,
        receiveResult,
        receiver: await parseState(),
      }
    }

    if (resumePending) {
      const parsedCheckpoint = checkpointForWalletSelection
      const savedCheckpoint = {
        ...parsedCheckpoint,
        kind: parsedCheckpoint?.kind || 'rgb11-prepared-transfer',
        version: 2,
        preparedTransferId: parsedCheckpoint?.preparedTransferId || parsedCheckpoint?.transferId,
      }
      if (savedCheckpoint.transferTransport !== 'address') {
        throw new Error(`unsupported RGB11 checkpoint transport: ${savedCheckpoint.transferTransport || 'missing'}`)
      }
      if (!savedCheckpoint?.transferId || savedCheckpoint.transferId !== savedCheckpoint.preparedTransferId) {
        throw new Error('RGB11 address resume checkpoint has an invalid transfer id')
      }
      await switchWallet(0, true)
      const senderState = await parseState()
      const existingTransfer = (senderState.transfers || []).find(
        (item) => item?.transfer_id === savedCheckpoint.transferId,
      )
      const checkpointAlreadyBroadcast = ['broadcast', 'receiver_persisted', 'terminal'].includes(
        savedCheckpoint.resumePhase,
      ) && Boolean(savedCheckpoint.txid)
      const localTransferAlreadyBroadcast = ['broadcast', 'pending', 'settled'].includes(existingTransfer?.status) &&
        Boolean(existingTransfer?.witness_txid)
      const alreadyBroadcast = checkpointAlreadyBroadcast || localTransferAlreadyBroadcast
      let broadcast
      if (alreadyBroadcast) {
        broadcast = { txid: savedCheckpoint.txid || existingTransfer.witness_txid }
      } else {
        const resumedResponse = await retryResumeTuple(
          'resumeRGB11PreparedTransfer',
          () => sat20.resumeRGB11PreparedTransfer(savedCheckpoint.transferId),
        )
        const resumed = JSON.parse(resumedResponse.transfer)
        if (!resumed.state?.address_mode || resumed.state?.transport_mode !== 'address-dkvs') {
          throw new Error('RGB11 checkpoint is not an address mailbox transfer')
        }
        broadcast = await retryResumeTuple(
          'deliverAndBroadcastRGB11AddressTransfer',
          () => rgb11Address.deliverAndBroadcast({ transfer_id: savedCheckpoint.transferId }),
          3,
        )
		if (broadcast?.awaiting_ack) {
		  await switchWallet(1, true)
		  await syncAddressMailboxUntilProcessed(
			savedCheckpoint.transferId,
			savedCheckpoint.assetName,
			savedCheckpoint.transferAmount,
		  )
		  await switchWallet(0, true)
		  broadcast = await retryResumeTuple(
			'deliverAndBroadcastRGB11AddressTransfer after ACK',
			() => rgb11Address.deliverAndBroadcast({ transfer_id: savedCheckpoint.transferId }),
			3,
		  )
		}
        if (!broadcast?.txid || broadcast.txid !== resumed.txid) {
          throw new Error('resumed RGB11 address broadcast txid mismatch')
        }
      }
      if (!['receiver_persisted', 'terminal'].includes(savedCheckpoint.resumePhase)) {
        saveTransferCheckpoint({ ...savedCheckpoint, resumePhase: 'broadcast', txid: broadcast.txid })
      }
      await switchWallet(1, true)
      if (!['receiver_persisted', 'terminal'].includes(savedCheckpoint.resumePhase)) {
        const receiverBeforeMailbox = await parseState()
        const receiverTransferBeforeMailbox = (receiverBeforeMailbox.transfers || []).find(
          (item) => item?.transfer_id === savedCheckpoint.transferId,
        )
        const receiverOwnershipAlreadyProjected = findAssetAmount(
          receiverBeforeMailbox.assets,
          savedCheckpoint.assetName,
        ) === savedCheckpoint.transferAmount
        if (receiverOwnershipAlreadyProjected && !receiverTransferBeforeMailbox) {
          throw new Error(`receiver RGB11 transfer lifecycle ${savedCheckpoint.transferId} is missing after restart`)
        }
        if (!receiverOwnershipAlreadyProjected || !receiverTransferBeforeMailbox) {
          await syncAddressMailboxUntilProcessed(
            savedCheckpoint.transferId,
            savedCheckpoint.assetName,
            savedCheckpoint.transferAmount,
          )
        }
      }
      const [receiverRefreshError] = await sat20.refreshRGB11State()
      if (receiverRefreshError && !/witness is unresolved|outpoint status is unknown/i.test(
        String(receiverRefreshError.message || receiverRefreshError),
      )) {
        throw receiverRefreshError
      }
      const receiverState = await parseState()
      const receiverTransfer = (receiverState.transfers || []).find(
        (item) => item?.transfer_id === savedCheckpoint.transferId,
      )
      if (!receiverTransfer) {
        throw new Error(`receiver RGB11 transfer lifecycle ${savedCheckpoint.transferId} is missing after restart`)
      }
      if (findAssetAmount(receiverState.assets, savedCheckpoint.assetName) !== savedCheckpoint.transferAmount) {
        throw new Error('receiver RGB11 ownership projection is missing after restart')
      }
      saveTransferCheckpoint({ ...savedCheckpoint, resumePhase: 'receiver_persisted', txid: broadcast.txid })
      await switchWallet(0, true)
      const [senderRefreshError] = await sat20.refreshRGB11State()
      if (senderRefreshError && !/witness is unresolved|outpoint status is unknown/i.test(
        String(senderRefreshError.message || senderRefreshError),
      )) {
        throw senderRefreshError
      }
      const resumedSenderState = await parseState()
      const senderTransfer = (resumedSenderState.transfers || []).find(
        (item) => item?.transfer_id === savedCheckpoint.transferId,
      )
      if (!senderTransfer) {
        throw new Error(`sender RGB11 transfer lifecycle ${savedCheckpoint.transferId} is missing after restart`)
      }
      const terminal = senderTransfer.status === 'settled' && receiverTransfer.status === 'settled'
      if (terminal) clearTransferCheckpoint()
      return {
        resumed: true,
        alreadyBroadcast,
        receiverAccepted: true,
        terminal,
        resumedTransfer: { transferId: savedCheckpoint.transferId, txid: broadcast.txid },
        wallets: [resumedSenderState, receiverState],
      }

    }

    if (inspectPending) {
      const wallets = []
      for (let index = 0; index < walletIDs.length; index++) {
        await switchWallet(index, true)
        wallets.push(await parseState())
      }
      return { inspectedPending: true, wallets }
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
    let issued
    let ticker
    let assetName
    let senderStartAmount = issueAmount
    let senderIssuedState = await parseState()
    if (reuseAssetName) {
      const info = (senderIssuedState.ticker_infos || []).find((item) => (
        item.canonical_name === reuseAssetName ||
        `${item?.name?.Protocol || ''}:${item?.name?.Type || ''}:${item?.name?.Ticker || ''}` === reuseAssetName
      ))
      if (!info?.contract_id) throw new Error(`reusable RGB11 asset not found: ${reuseAssetName}`)
      assetName = info.canonical_name || reuseAssetName
      ticker = info.name?.Ticker || assetName.split(':').at(-1)
      senderStartAmount = findAssetAmount(senderIssuedState.available_assets, assetName)
      issued = { asset_name: info.name, contract_id: info.contract_id, schema_id: '' }
      progress(`reusing RGB11 asset ${assetName}`)
    } else {
      ticker = `R${Date.now().toString(36).slice(-7)}`.toUpperCase()
      const issueResponse = await unwrap(await sat20.issueRGB11Asset({
        schema: 'NIA',
        ticker,
        name: `SAT20 RGB ${ticker}`,
        precision: 0,
        amounts: [issueAmount],
        min_confirmations: 1,
      }), 'issueRGB11Asset')
      issued = JSON.parse(issueResponse.result)
      progress('RGB11 asset issued')
      assetName = `${issued.asset_name.Protocol}:${issued.asset_name.Type}:${issued.asset_name.Ticker}`
      senderIssuedState = await parseState()
    }
    traceState('sender state after issue', senderIssuedState)
    if (findAssetAmount(senderIssuedState.available_assets, assetName) !== senderStartAmount || senderStartAmount === '0') {
      throw new Error('sender issued RGB11 balance is missing before transfer')
    }

    await switchWallet(1)
    let imported = { projected: 0 }
    if (issued.armor) {
      const importedResponse = await unwrap(await sat20.importRGB11Contract(issued.armor), 'importRGB11Contract')
      imported = JSON.parse(importedResponse.result)
    }
    let invoice
    let externalInvoice = ''
    if (transferTransport === 'address') {
      await unwrap(await rgb11Address.enableReceive({}), 'enableRGB11AddressReceive')
      progress('receiver imported contract and enabled address mailbox receive')
    } else {
      invoice = await unwrap(await sat20.createRGB11Invoice({
        mode: 'witness',
        transport_mode: transferTransport,
        ...(transferTransport === 'rgb-json-rpc' ? { transport_endpoints: [proxyEndpoint] } : {}),
        contract_id: issued.contract_id,
        schema_id: issued.schema_id,
        amount_raw: transferAmount,
        assignment_name: 'assetOwner',
        expiry: Math.floor(Date.now() / 1000) + 24 * 60 * 60,
        witness_vout: 1,
      }), 'createRGB11Invoice')
      externalInvoice = invoice.invoice
      progress('receiver imported contract and created standard invoice')
    }

    await switchWallet(0)
    const senderBeforePrepareState = await parseState()
    traceState('sender state before prepare', senderBeforePrepareState)
    if (findAssetAmount(senderBeforePrepareState.available_assets, assetName) !== senderStartAmount) {
      throw new Error('sender RGB11 balance was overwritten before transfer preparation')
    }
    const preparedResponse = transferTransport === 'address'
      ? await unwrap(await rgb11Address.prepareTransfer({
        receiver_address: addresses[1],
        asset_name: assetName,
        amount_raw: transferAmount,
        fee_rate: 1,
        min_confirmations: 1,
      }), 'prepareRGB11AddressTransfer')
      : await unwrap(await sat20.prepareRGB11Transfer({
        invoice: externalInvoice,
        fee_rate: 1,
        min_confirmations: 1,
      }), 'prepareRGB11Transfer')
    const prepared = JSON.parse(preparedResponse.transfer)
    const transferID = prepared.state?.transfer_id
    if (!transferID) throw new Error('prepared transfer has no transfer id')
    if (prepared.state?.direction !== 'send' || prepared.state?.status !== 'prepared') {
      throw new Error(
        `prepared transfer has invalid state ${prepared.state?.direction || 'unknown'}/${prepared.state?.status || 'unknown'}`,
      )
    }
    if (transferTransport !== 'address' &&
      (prepared.state?.invoice !== externalInvoice || !prepared.recipient_consignment)) {
      throw new Error('prepared transfer does not match the requested invoice or has no recipient consignment')
    }
    const expectedPreparedTransport = transferTransport === 'address' ? 'address-dkvs' : transferTransport
    if (prepared.state?.transport_mode !== expectedPreparedTransport) {
      throw new Error(`expected ${expectedPreparedTransport} transfer, got ${prepared.state?.transport_mode || 'unknown'}`)
    }
    if (transferTransport === 'address' && !prepared.state?.address_mode) {
      throw new Error('prepared address transfer is not marked as address mailbox mode')
    }
    const transferCheckpoint = {
      kind: 'rgb11-prepared-transfer',
      version: 2,
      ticker,
      assetName,
      contractId: issued.contract_id,
      schemaId: issued.schema_id,
      requestId: invoice?.request_id || invoice?.requestId || '',
      invoice: externalInvoice,
      transferId: transferID,
      preparedTransferId: transferID,
      transferTransport,
      senderWalletId: walletIDs[0],
      receiverWalletId: walletIDs[1],
      senderAddress: addresses[0],
      receiverAddress: addresses[1],
      issueAmount: senderStartAmount,
      transferAmount,
      resumePhase: 'prepared',
    }
    saveTransferCheckpoint(transferCheckpoint)
    progress(`sender prepared ${transferTransport} transfer`)

    let broadcast
    let receiveResult
    let addressDelivery
    if (transferTransport === 'address') {
	  const delivered = await unwrap(
        await rgb11Address.deliverAndBroadcast({ transfer_id: transferID }),
        'deliverAndBroadcastRGB11AddressTransfer',
      )
	  if (!delivered.awaiting_ack || delivered.broadcast || delivered.txid) {
		throw new Error(`address delivery crossed ACK boundary: ${JSON.stringify(delivered)}`)
	  }
	  addressDelivery = JSON.parse(delivered.result)
      if (!addressDelivery.record_key?.startsWith('/mail/')) {
        throw new Error(`RGB11 address delivery did not use mailbox: ${addressDelivery.record_key || 'missing'}`)
      }
      await switchWallet(1, true)
      receiveResult = await syncAddressMailboxUntilProcessed(transferID, assetName, transferAmount)
	  saveTransferCheckpoint({ ...transferCheckpoint, resumePhase: 'receiver_persisted' })
	  await switchWallet(0, true)
	  broadcast = await unwrap(
		await rgb11Address.deliverAndBroadcast({ transfer_id: transferID }),
		'deliverAndBroadcastRGB11AddressTransfer after ACK',
	  )
	  if (!broadcast.broadcast || !broadcast.txid) {
		throw new Error(`address transfer did not broadcast after ACK: ${JSON.stringify(broadcast)}`)
	  }
    } else if (transferTransport === 'rgb-json-rpc') {
	  const delivered = await deliverProxyWithRetry(transferID)
	  if (!delivered.awaiting_ack || delivered.broadcast || delivered.txid) {
		throw new Error(`proxy delivery crossed ACK boundary: ${JSON.stringify(delivered)}`)
	  }
	  await switchWallet(1, true)
	  receiveResult = await receiveProxyWhenBitcoinEvidenceIsReady(invoice.request_id || invoice.requestId)
	  if (!receiveResult?.awaiting_broadcast || !receiveResult?.ack_posted) {
		throw new Error(`proxy receiver did not ACK prepared consignment: ${JSON.stringify(receiveResult)}`)
	  }
	  await switchWallet(0, true)
	  broadcast = await deliverProxyWithRetry(transferID)
	  if (!broadcast.broadcast || !broadcast.txid) {
		throw new Error(`proxy transfer did not broadcast after ACK: ${JSON.stringify(broadcast)}`)
	  }
    } else {
      await switchWallet(1)
      await unwrap(
        await sat20.prepareRGB11Consignment(
          invoice.request_id || invoice.requestId,
          prepared.recipient_consignment,
        ),
        'prepareRGB11Consignment',
      )
      await switchWallet(0)
      broadcast = await unwrap(await sat20.broadcastRGB11OutOfBand([transferID]), 'broadcastRGB11OutOfBand')
      await switchWallet(1, true)
      receiveResult = await acceptWhenBitcoinEvidenceIsReady(
        invoice.request_id || invoice.requestId,
        prepared.recipient_consignment,
      )
    }
	saveTransferCheckpoint({ ...transferCheckpoint, resumePhase: 'broadcast', txid: broadcast.txid })
    const expectedSenderAmount = (BigInt(senderStartAmount) - BigInt(transferAmount)).toString()
    progress(`transfer broadcast ${broadcast.txid}`)
    await switchWallet(0, true)
    const senderAfter = await waitForSenderChangeProjection(assetName, expectedSenderAmount)
    traceState('sender state after change projection', senderAfter)

    await switchWallet(1, true)
    if (transferTransport === 'rgb-json-rpc') {
      receiveResult = await receiveProxyWhenBitcoinEvidenceIsReady(invoice.request_id || invoice.requestId)
    }
    const receiverAcceptedState = await waitForReceiverPendingProjection(assetName, transferAmount)
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
    const [pendingSendError] = transferTransport === 'address'
      ? await rgb11Address.prepareTransfer({
        receiver_address: addresses[0], asset_name: assetName, amount_raw: transferAmount,
        fee_rate: 1, min_confirmations: 1,
      })
      : await sat20.prepareRGB11Transfer({
        invoice: externalInvoice,
        fee_rate: 1,
        min_confirmations: 1,
      })
    if (!pendingSendError) {
      throw new Error('unconfirmed RGB11 balance was accepted as a spendable transfer input')
    }
    progress(`receiver accepted ${transferTransport} consignment`)
    const receiverAfter = await parseState()

    await switchWallet(0, true)
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
      issueAmount: senderStartAmount,
      transferAmount,
      transferTransport,
      txid: broadcast.txid,
      receiveResult,
      addressDelivery,
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
    accountIndexes: [SENDER_ACCOUNT_INDEX, RECEIVER_ACCOUNT_INDEX],
    diagnoseOnly: DIAGNOSE_ONLY,
    ensureTestAccount: ENSURE_TEST_ACCOUNT,
    transferTransport: TRANSFER_TRANSPORT,
    proxyEndpoint: PROXY_ENDPOINT,
    cancelOutOfBandTransferID: CANCEL_OUT_OF_BAND_TRANSFER_ID,
    cancelExpiredTransferID: CANCEL_EXPIRED_TRANSFER_ID,
    reuseAssetName: REUSE_ASSET_NAME,
    resumePending: RESUME_PENDING,
    inspectPending: INSPECT_PENDING,
    syncMailboxOnly: SYNC_MAILBOX_ONLY,
    syncMailboxWalletIndex: SYNC_MAILBOX_WALLET_INDEX,
    resumeProxyRequestID: RESUME_PROXY_REQUEST_ID,
    useCheckpointWallets: USE_CHECKPOINT_WALLETS,
    verifyRecoveryAsset: VERIFY_RECOVERY_ASSET,
    verifyRecoverySenderAmount: VERIFY_RECOVERY_SENDER_AMOUNT,
    verifyRecoveryReceiverAmount: VERIFY_RECOVERY_RECEIVER_AMOUNT,
  })

  if (result.diagnoseOnly) {
    console.log(JSON.stringify(result, null, 2))
    return
  }
  if (result.verifiedColdRecovery) {
    console.log(JSON.stringify({
      verifiedColdRecovery: true,
      assetName: result.assetName,
      wallets: result.wallets.map((state) => summarizeState(state)),
    }, null, 2))
    return
  }
  if (result.cancelled) {
    console.log(JSON.stringify({ cancelled: result.cancelled, sender: summarizeState(result.sender) }, null, 2))
    return
  }
  if (result.cancelledExpired) {
    console.log(JSON.stringify({
      cancelledExpired: result.cancelledExpired,
      sender: summarizeState(result.sender),
    }, null, 2))
    return
  }
  if (result.resumed) {
    console.log(JSON.stringify({
      resumed: true,
      alreadyBroadcast: Boolean(result.alreadyBroadcast),
      receiverAccepted: Boolean(result.receiverAccepted),
      resumedTransfer: result.resumedTransfer,
      wallets: result.wallets.map((state) => summarizeState(state)),
    }, null, 2))
    return
  }
  if (result.inspectedPending) {
    console.log(JSON.stringify({
      inspectedPending: true,
      wallets: result.wallets.map((state) => summarizeState(state)),
    }, null, 2))
    return
  }
  if (result.syncedMailboxOnly) {
    console.log(JSON.stringify({
      syncedMailboxOnly: true,
      receiveResult: result.receiveResult,
      receiver: summarizeState(result.receiver),
    }, null, 2))
    return
  }
  if (result.resumedProxyRequest) {
    console.log(JSON.stringify({
      resumedProxyRequest: result.resumedProxyRequest,
      receiveResult: result.receiveResult,
      receiver: summarizeState(result.receiver),
    }, null, 2))
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
  } finally {
    if (verificationPage && !verificationPage.isClosed()) await verificationPage.close()
    if (launchBrowser) await browser.close()
  }
}

main().then(() => {
  process.exit(0)
}).catch((error) => {
  console.error(error)
  process.exit(1)
})
