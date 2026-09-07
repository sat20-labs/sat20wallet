import { chromium } from '@playwright/test'
import {
  appendFileSync,
  chmodSync,
  existsSync,
  lstatSync,
  readFileSync,
  renameSync,
  unlinkSync,
  writeFileSync,
} from 'node:fs'
import { dirname, resolve } from 'node:path'

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456'
const PRIMARY_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/#/'
const RECOVERY_URL = process.env.SAT20_RECOVERY_PWA_URL || 'http://127.0.0.1:5173/#/'
const CONTINUE_ACTIVE = process.env.SAT20_ACCOUNT_CONTINUE_ACTIVE === '1'
const TRACE_DKVS = process.env.SAT20_TRACE_DKVS === '1'
const TRACE_DKVS_FILE = process.env.SAT20_TRACE_DKVS_FILE || ''
const RESET_PRIMARY = process.env.SAT20_ACCOUNT_RESET_PRIMARY === '1'
const MESSAGE_SERVICE_URL = process.env.SAT20_ACCOUNT_MESSAGE_SERVICE_URL ||
  'https://apiprd.ordx.market/stp/testnet/message/service'
const RECOVERY_CHECKPOINT = resolve(process.env.SAT20_ACCOUNT_RECOVERY_CHECKPOINT ||
  '/private/tmp/sat20-account-management-pwa-live-checkpoint.json')
if (!RECOVERY_CHECKPOINT.startsWith('/private/tmp/') || dirname(RECOVERY_CHECKPOINT) !== '/private/tmp') {
  throw new Error('SAT20_ACCOUNT_RECOVERY_CHECKPOINT must be a direct /private/tmp file path')
}
const DEFAULT_TEST_MNEMONICS = [
  'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire',
  'comfort very add tuition senior run eight snap burst appear exile dutch',
]
const TEST_MNEMONICS = process.env.SAT20_ACCOUNT_TEST_MNEMONICS
  ? process.env.SAT20_ACCOUNT_TEST_MNEMONICS.split('|').map((value) => value.trim()).filter(Boolean)
  : DEFAULT_TEST_MNEMONICS
if (TEST_MNEMONICS.length !== 2) {
  throw new Error('SAT20_ACCOUNT_TEST_MNEMONICS must contain exactly two pipe-separated mnemonics')
}
if (process.env.SAT20_ACCOUNT_REVERSE_WALLETS === '1') TEST_MNEMONICS.reverse()
const ANSWERS = [
  { question_id: 'account-e2e-a', answer: 'sat20 account recovery answer alpha' },
  { question_id: 'account-e2e-b', answer: 'sat20 account recovery answer beta' },
  { question_id: 'account-e2e-c', answer: 'sat20 account recovery answer gamma' },
]

const assertCheckpointFile = () => {
  const info = lstatSync(RECOVERY_CHECKPOINT)
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('recovery checkpoint is not a regular file')
  if (typeof process.getuid === 'function' && info.uid !== process.getuid()) {
    throw new Error('recovery checkpoint is owned by another user')
  }
  if ((info.mode & 0o077) !== 0) throw new Error('recovery checkpoint permissions must be 0600')
}

const MANAGED_PHASES = [
  'not_started',
  'second_account_synced',
  'temporary_wallet_created',
  'temporary_account_added',
  'temporary_wallet_synced',
  'temporary_wallet_delete_requested',
  'temporary_wallet_deleted',
  'complete',
]

const validManagedProgress = (value) => value &&
  MANAGED_PHASES.includes(value.phase) &&
  typeof value.second_wallet_id === 'string' &&
  (value.temporary_wallet_id === undefined || typeof value.temporary_wallet_id === 'string')

const validCheckpoint = (value) => (value?.version === 1 || value?.version === 2) &&
  typeof value.primary_origin === 'string' && value.primary_origin.length > 0 &&
  typeof value.recovery_origin === 'string' && value.recovery_origin.length > 0 &&
  typeof value.account_id === 'string' && value.account_id.length > 0 &&
  typeof value.root_wallet_id === 'string' && value.root_wallet_id.length > 0 &&
  typeof value.session_id === 'string' && value.session_id.length > 0 &&
  typeof value.locator === 'string' && value.locator.length > 0 &&
  typeof value.user_share === 'string' && value.user_share.length > 0 && Number.isSafeInteger(value.created_at) &&
  Number.isSafeInteger(value.session_expires_at) &&
  (value.version === 1 || validManagedProgress(value.managed_progress))

const loadRecoveryCheckpoint = () => {
  if (!existsSync(RECOVERY_CHECKPOINT)) return null
  assertCheckpointFile()
  const checkpoint = JSON.parse(readFileSync(RECOVERY_CHECKPOINT, 'utf8'))
  if (!validCheckpoint(checkpoint)) throw new Error('recovery checkpoint is invalid')
  return checkpoint
}

const writeRecoveryCheckpoint = (checkpoint) => {
  if (!validCheckpoint(checkpoint)) throw new Error('refusing to persist an invalid recovery checkpoint')
  if (existsSync(RECOVERY_CHECKPOINT)) assertCheckpointFile()
  const temporary = `${RECOVERY_CHECKPOINT}.${process.pid}.${Date.now()}.tmp`
  try {
    writeFileSync(temporary, JSON.stringify(checkpoint), { encoding: 'utf8', mode: 0o600, flag: 'wx' })
    chmodSync(temporary, 0o600)
    renameSync(temporary, RECOVERY_CHECKPOINT)
    chmodSync(RECOVERY_CHECKPOINT, 0o600)
  } finally {
    if (existsSync(temporary)) unlinkSync(temporary)
  }
}

const persistManagedProgress = (checkpoint, progress) => {
  if (!checkpoint) return null
  const updated = { ...checkpoint, version: 2, managed_progress: { ...progress } }
  writeRecoveryCheckpoint(updated)
  console.log(`[Account live] managed checkpoint advanced to ${progress.phase}`)
  return updated
}

const clearRecoveryCheckpoint = () => {
  if (!existsSync(RECOVERY_CHECKPOINT)) return
  assertCheckpointFile()
  unlinkSync(RECOVERY_CHECKPOINT)
}

const messageServicePingURL = () => {
  const target = new URL(MESSAGE_SERVICE_URL)
  if (!/\/message\/service\/?$/.test(target.pathname)) {
    throw new Error('SAT20_ACCOUNT_MESSAGE_SERVICE_URL must end in /message/service')
  }
  target.pathname = target.pathname.replace(/\/message\/service\/?$/, '/ping')
  return target.href
}

const assertMessageServiceCors = async (page, label) => {
  const result = await page.evaluate(async ({ url }) => {
    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), 15_000)
    try {
      const response = await fetch(url, {
        method: 'GET', mode: 'cors', credentials: 'omit', cache: 'no-store', signal: controller.signal,
      })
      return { allowed: response.type === 'cors' || response.type === 'basic', status: response.status }
    } catch (error) {
      return { allowed: false, error: error instanceof Error ? error.message : String(error) }
    } finally {
      clearTimeout(timeout)
    }
  }, { url: messageServicePingURL() })
  if (!result.allowed) {
    throw new Error(`${label} message service CORS preflight failed for ${new URL(page.url()).origin}`)
  }
  console.log(`[Account live] ${label} message service CORS preflight passed (${result.status})`)
}

const primeEnvironment = async (page, targetURL) => {
  const manifest = new URL('/manifest.webmanifest', targetURL)
  await page.goto(manifest.href, { waitUntil: 'domcontentloaded' })
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
      transaction.oncomplete = resolve
      transaction.onerror = () => reject(transaction.error)
    })
    db.close()
  })
  await page.goto(targetURL, { waitUntil: 'domcontentloaded' })
  await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__ && globalThis.sat20account_wasm), null, {
    timeout: 180_000,
  })
}

const traceDKVSRequests = (page) => {
  if (!TRACE_DKVS) return
  page.on('request', (request) => {
    if (request.method() !== 'POST' || !request.url().includes('/v3/dkvs/')) return
    const body = request.postData()
    if (!body) return
    if (TRACE_DKVS_FILE) appendFileSync(TRACE_DKVS_FILE, `${body}\n`)
    try {
      const payload = JSON.parse(body)
      const mutations = Array.isArray(payload.mutations) ? payload.mutations : []
      const field = (record, name) => record?.[name] ?? record?.[name[0].toUpperCase() + name.slice(1)]
      console.log('[Account live][DKVS request]', JSON.stringify({
        url: request.url(),
        endpoint_id: payload.endpoint_id || '',
        mutations: mutations.map(({ record, expected_hash, expect_absent }) => ({
          fields: Object.keys(record || {}),
          key: field(record, 'key'),
          seq: field(record, 'seq'),
          path_generation: field(record, 'path_generation') ?? field(record, 'pathGeneration'),
          ttl: field(record, 'ttl'),
          expiry_height: field(record, 'expiry_height') ?? field(record, 'expiryHeight'),
          flags: field(record, 'flags'),
          value_bytes: typeof field(record, 'value') === 'string' ? field(record, 'value').length : 0,
          pub_key: field(record, 'pub_key') ?? field(record, 'pubKey') ?? '',
          signature: field(record, 'signature') || '',
          fee_proof: field(record, 'fee_proof') ?? field(record, 'feeProof') ?? '',
          expected_hash: expected_hash || '',
          expect_absent: Boolean(expect_absent),
        })),
        path_preconditions: payload.path_preconditions || [],
      }))
    } catch {
      console.log('[Account live][DKVS request]', request.url(), body.length)
    }
  })
}

const main = async () => {
  const browser = await chromium.connectOverCDP(CDP)
  // A fresh CDP BrowserContext keeps this long-running account rehearsal from
  // sharing localStorage/WASM sessions with another acceptance flow.  The
  // option is deliberately test-only; the normal path continues to reuse the
  // existing desktop context.
  const context = process.env.SAT20_CDP_NEW_CONTEXT === '1'
    ? await browser.newContext()
    : (browser.contexts()[0] || await browser.newContext())
  const primaryOrigin = new URL(PRIMARY_URL).origin
  const recoveryOrigin = new URL(RECOVERY_URL).origin
  if (primaryOrigin === recoveryOrigin) throw new Error('primary and recovery PWA origins must be different')
  const savedCheckpoint = loadRecoveryCheckpoint()
  if (savedCheckpoint && (savedCheckpoint.primary_origin !== primaryOrigin ||
      savedCheckpoint.recovery_origin !== recoveryOrigin)) {
    throw new Error('recovery checkpoint belongs to different PWA origins')
  }
  if (RESET_PRIMARY && savedCheckpoint) {
    throw new Error('refusing to reset the primary origin while a recovery checkpoint exists')
  }
  let primary = context.pages().find((page) => page.url().startsWith(primaryOrigin))
  if (savedCheckpoint && !primary) {
    throw new Error('checkpoint resume requires the original primary PWA page and WASM session')
  }
  if (!primary) primary = await context.newPage()
  traceDKVSRequests(primary)
  primary.on('console', (message) => {
    if (message.type() === 'error' || message.text().includes('[Account live]')) {
      console.error(`[page:${message.type()}] ${message.text()}`)
    }
  })
  if (RESET_PRIMARY) {
    const cdp = await context.newCDPSession(primary)
    await cdp.send('Storage.clearDataForOrigin', {
      origin: new URL(PRIMARY_URL).origin,
      storageTypes: 'all',
    })
  }
  if (savedCheckpoint) {
    await primary.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__ && globalThis.sat20account_wasm), null, {
      timeout: 30_000,
    })
    console.log('[Account live] original primary PWA/WASM session preserved for checkpoint resume')
  } else {
    await primeEnvironment(primary, PRIMARY_URL)
    console.log('[Account live] primary PWA ready')
  }

  let recovery = context.pages().find((page) => page !== primary && page.url().startsWith(recoveryOrigin))
  if (!recovery) recovery = await context.newPage()
  await primeEnvironment(recovery, RECOVERY_URL)
  console.log('[Account live] recovery PWA ready for CORS preflight')
  await assertMessageServiceCors(primary, 'primary')
  await assertMessageServiceCors(recovery, 'recovery')

  const setup = await primary.evaluate(async ({
    password, mnemonics, answers, continueActive, resumeCheckpoint, recoveryOrigin, managedPhases,
  }) => {
    const verify = window.__SAT20_PWA_VERIFY__
    const wallet = verify.useWalletStore()
    const account = globalThis.sat20account_wasm
		const credential = password
    const call = async (method, payload = {}) => {
      const response = await account[method](JSON.stringify(payload))
      if (!response || response.code !== 0) throw new Error(`${method}: ${response?.msg || 'failed'}`)
      return response.data
    }
    const tuple = (result, operation) => {
      if (result?.[0]) throw new Error(`${operation}: ${result[0].message || result[0]}`)
      return result?.[1]
    }
    const summarizeCatalog = (wallets) => wallets.map((item) => ({
      id: item.id,
      name: item.name,
      accounts: item.accounts.map((entry) => ({ index: entry.index, address: entry.address })),
    }))
    const metadata = () => wallet.wallets.map((item) => ({
      id: Number(item.id),
      name: item.name,
      sub_accounts: Object.fromEntries(item.accounts.map((entry) => [entry.index, entry.did || ''])),
    }))
    const hasMnemonicWallet = (wallets, identity) => wallets.some((item) =>
      item.accounts.some((entry) => entry.index === 0 && entry.address === identity.address))
    const mnemonicWallet = (wallets, identity) => wallets.find((item) =>
      item.accounts.some((entry) => entry.index === 0 && entry.address === identity.address))
    const requiresPaidRecoverySetup = (current) => !current.active || (
      current.storage_mode === 'temporary' &&
      current.recovery_configured === false &&
      Number(current.state_seq || 0) === 1
    )
    const waitSynced = async (label) => {
      const deadline = Date.now() + 180_000
      let status
      while (Date.now() < deadline) {
        status = await call('status')
        if (status.active && Number(status.pending_changes || 0) === 0) return status
        await new Promise((resolve) => setTimeout(resolve, 1_000))
      }
      throw new Error(`${label}: account state did not synchronize (${JSON.stringify(status)})`)
    }

    await verify.walletStorage.initializeState()
    await verify.walletStorage.setValue('env', 'prd')
    await verify.walletStorage.setValue('network', 'testnet')
    await verify.walletStorage.setValue('chain', 'btc')
    await wallet.syncWalletCatalog().catch(() => [])
    if (wallet.wallets.length > 0) {
			const [catalogUnlockError] = await wallet.unlockWallet(credential)
			if (catalogUnlockError) throw catalogUnlockError
			await wallet.syncWalletCatalog()
    }
    for (const mnemonic of mnemonics) {
			// Wallet import uses the password only as a storage credential, not as a BIP39 passphrase.
			const validation = await globalThis.sat20wallet_wasm.validateMnemonic(mnemonic, '')
			if (!validation || validation.code !== 0 || !validation.data) {
				throw new Error(`validateMnemonic: ${validation?.msg || 'failed'}`)
			}
			if (hasMnemonicWallet(wallet.wallets, validation.data)) continue
			tuple(await wallet.importWallet(mnemonic, credential), 'importWallet')
    }
	const [sessionUnlockError] = await wallet.unlockWallet(credential)
	if (sessionUnlockError) throw sessionUnlockError
    await wallet.setNetwork(verify.Network.TESTNET)
    await wallet.setChain(verify.Chain.BTC)
    await wallet.syncWalletCatalog()
    if (wallet.wallets.length < 2) throw new Error('two test wallets are required')

    const configuredWallets = []
    for (const mnemonic of mnemonics) {
      const validation = await globalThis.sat20wallet_wasm.validateMnemonic(mnemonic, '')
      if (!validation || validation.code !== 0 || !validation.data) {
        throw new Error(`validateMnemonic: ${validation?.msg || 'failed'}`)
      }
      const configured = mnemonicWallet(wallet.wallets, validation.data)
      if (!configured) throw new Error('configured mnemonic wallet is missing after catalog sync')
      configuredWallets.push(configured)
    }
    if (new Set(configuredWallets.map((item) => String(item.id))).size !== mnemonics.length) {
      throw new Error('configured mnemonics did not resolve to distinct wallets')
    }

    const initialCatalog = summarizeCatalog(wallet.wallets)
		const preflight = await call('preflight', { password: credential, wallets: metadata() })
    const status = await call('status')
    const checkpointMatches = !resumeCheckpoint || (
      resumeCheckpoint.primary_origin === location.origin &&
      resumeCheckpoint.recovery_origin === recoveryOrigin &&
      resumeCheckpoint.account_id === String(status.account_id || '') &&
      resumeCheckpoint.root_wallet_id === String(status.root_wallet_id || '')
    )
    if (!checkpointMatches) throw new Error('recovery checkpoint does not match the active account')
    const configuredIDs = new Set(configuredWallets.map((item) => String(item.id)))
    const secondWalletID = String(configuredWallets[1].id)
    const inferLegacyManagedProgress = () => {
      const extras = wallet.wallets.filter((item) => !configuredIDs.has(String(item.id)))
      const secondHasManagedAccount = configuredWallets[1].accounts.some((entry) => entry.index === 2)
      if (extras.length === 0) {
        if (Number(status.pending_changes || 0) !== 0 || status.managed_data_dirty) {
          throw new Error('legacy checkpoint has pending managed changes without a uniquely identifiable temporary wallet')
        }
        return {
          phase: secondHasManagedAccount ? 'second_account_synced' : 'not_started',
          second_wallet_id: secondWalletID,
        }
      }
      if (extras.length !== 1 || !secondHasManagedAccount ||
          !status.active || status.storage_mode !== 'paid' || !status.recovery_configured) {
        throw new Error('legacy checkpoint cannot safely identify managed-mutation progress')
      }
      const temporary = extras[0]
      const indexes = temporary.accounts.map((entry) => Number(entry.index)).sort((a, b) => a - b)
      if (String(wallet.walletId) !== String(temporary.id) ||
          (indexes.join(',') !== '0' && indexes.join(',') !== '0,1')) {
        throw new Error('legacy checkpoint temporary wallet evidence is ambiguous')
      }
      return {
        phase: indexes.includes(1) ? 'temporary_account_added' : 'temporary_wallet_created',
        second_wallet_id: secondWalletID,
        temporary_wallet_id: String(temporary.id),
      }
    }
    const validateManagedProgress = (progress) => {
      if (!progress || progress.second_wallet_id !== secondWalletID) {
        throw new Error('managed checkpoint does not match the configured second wallet')
      }
      const phaseIndex = managedPhases.indexOf(progress.phase)
      if (phaseIndex < 0) throw new Error('managed checkpoint phase is invalid')
      const temporary = progress.temporary_wallet_id
        ? wallet.wallets.find((item) => String(item.id) === progress.temporary_wallet_id)
        : null
      if (progress.temporary_wallet_id && configuredIDs.has(progress.temporary_wallet_id)) {
        throw new Error('managed checkpoint temporary wallet matches a configured mnemonic wallet')
      }
      if (phaseIndex >= managedPhases.indexOf('temporary_wallet_created') &&
          phaseIndex < managedPhases.indexOf('temporary_wallet_delete_requested') && !temporary) {
        throw new Error('managed checkpoint temporary wallet is missing from the catalog')
      }
      if (temporary && phaseIndex < managedPhases.indexOf('temporary_wallet_created')) {
        throw new Error('managed checkpoint omits an existing temporary wallet')
      }
      if (temporary && phaseIndex >= managedPhases.indexOf('temporary_account_added') &&
          !temporary.accounts.some((entry) => entry.index === 1)) {
        throw new Error('managed checkpoint temporary subaccount is missing')
      }
      if (temporary && phaseIndex >= managedPhases.indexOf('temporary_wallet_deleted')) {
        throw new Error('managed checkpoint says the temporary wallet was deleted but it still exists')
      }
      return progress
    }
    let managedProgress = resumeCheckpoint?.version === 2
      ? validateManagedProgress(resumeCheckpoint.managed_progress)
      : null
    let checkpointUpdate
    if (resumeCheckpoint?.version === 1) {
      managedProgress = inferLegacyManagedProgress()
      checkpointUpdate = { ...resumeCheckpoint, version: 2, managed_progress: managedProgress }
    }
    if (requiresPaidRecoverySetup(status)) {
      if (resumeCheckpoint) {
        if (Date.now() >= resumeCheckpoint.session_expires_at) {
          throw new Error('activation session expired; checkpoint retained for explicit recovery')
        }
        return { phase: 'resume', preflight, initialCatalog, status, managedProgress, checkpointUpdate }
      }
      const storage = await call('confirmStorage', { option_id: 'paid', record_count: 100 })
      const questions = answers.map((entry, index) => ({
        id: entry.question_id,
        prompt: `SAT20 account E2E recovery question ${index + 1}`,
        answer: entry.answer,
        confirmation: entry.answer,
        ignore_punctuation: true,
      }))
      const created = await call('createRecovery', {
			password: credential,
        wallets: metadata(),
        recovery_mode: '2of2',
        questions,
        storage_authorization_id: storage.id,
      })
      return {
        phase: 'created', preflight, initialCatalog, status,
        checkpoint: {
          version: 2,
          primary_origin: location.origin,
          recovery_origin: recoveryOrigin,
          account_id: String(status.account_id || ''),
          root_wallet_id: String(status.root_wallet_id || ''),
          session_id: created.session_id,
          locator: created.locator,
          user_share: created.user_share,
          created_at: Date.now(),
          session_expires_at: Date.now() + 20 * 60 * 1000,
          managed_progress: { phase: 'not_started', second_wallet_id: secondWalletID },
        },
      }
    } else if (resumeCheckpoint) {
      if (!status.active || status.storage_mode !== 'paid' || !status.recovery_configured) {
        throw new Error('recovery checkpoint conflicts with the current account status')
      }
      return { phase: 'active-checkpoint', preflight, initialCatalog, status, managedProgress, checkpointUpdate }
    } else if (continueActive) {
      return { phase: 'continue-active', preflight, initialCatalog, status }
    } else {
      throw new Error('test account management is already active; locator is not available to this isolated run')
    }
  }, {
    password: PASSWORD,
    mnemonics: TEST_MNEMONICS,
    answers: ANSWERS,
    continueActive: CONTINUE_ACTIVE,
    resumeCheckpoint: savedCheckpoint,
    recoveryOrigin,
    managedPhases: MANAGED_PHASES,
  })

  let recoveryCheckpoint = savedCheckpoint
  if (setup.checkpoint) {
    writeRecoveryCheckpoint(setup.checkpoint)
    recoveryCheckpoint = setup.checkpoint
    delete setup.checkpoint
    console.log(`[Account live] recovery checkpoint saved with mode 0600 at ${RECOVERY_CHECKPOINT}`)
  } else if (setup.checkpointUpdate) {
    writeRecoveryCheckpoint(setup.checkpointUpdate)
    recoveryCheckpoint = setup.checkpointUpdate
    setup.managedProgress = setup.checkpointUpdate.managed_progress
    delete setup.checkpointUpdate
    console.log('[Account live] legacy recovery checkpoint upgraded with verified managed progress')
  }

  const activationBase = await primary.evaluate(async ({ password, answers, setup, checkpoint, managedPhases }) => {
    const verify = window.__SAT20_PWA_VERIFY__
    const wallet = verify.useWalletStore()
    const account = globalThis.sat20account_wasm
		const credential = password
    const call = async (method, payload = {}) => {
      const response = await account[method](JSON.stringify(payload))
      if (!response || response.code !== 0) throw new Error(`${method}: ${response?.msg || 'failed'}`)
      return response.data
    }
    const waitSynced = async (label) => {
      const deadline = Date.now() + 180_000
      let status
      while (Date.now() < deadline) {
        status = await call('status')
        if (status.active && Number(status.pending_changes || 0) === 0) return status
        await new Promise((resolve) => setTimeout(resolve, 1_000))
      }
      throw new Error(`${label}: account state did not synchronize (${JSON.stringify(status)})`)
    }

    let status
    if (setup.phase === 'created' || setup.phase === 'resume') {
      if (!checkpoint) throw new Error('recovery checkpoint is required for rehearsal')
      const rehearsal = await call('rehearse', {
        session_id: checkpoint.session_id,
        answers,
        user_share: checkpoint.user_share,
			password: credential,
      })
      if (!rehearsal.verified) throw new Error('account recovery rehearsal failed')
      status = await waitSynced('activation')
    } else {
      status = await waitSynced('existing pending changes')
    }

    const progress = checkpoint?.managed_progress || setup.managedProgress || {
      phase: 'not_started', second_wallet_id: String(wallet.wallets[1].id),
    }
    const rootWalletID = wallet.wallets[0].id
    const secondWalletID = wallet.wallets.find((item) => String(item.id) === progress.second_wallet_id)?.id
    if (secondWalletID === undefined) throw new Error('managed checkpoint second wallet is missing')
    const [rootDeleteError] = await wallet.deleteWallet(rootWalletID)
    if (!rootDeleteError) throw new Error('root wallet deletion was unexpectedly allowed')

    await wallet.switchWallet(secondWalletID)
    const second = wallet.wallets.find((item) => item.id === secondWalletID)
    if (!second.accounts.some((entry) => entry.index === 2)) {
      await wallet.addAccount('Account 3', 2)
    }
    status = await waitSynced('new subaccount')
    return {
      preflight: setup.preflight,
      initialCatalog: setup.initialCatalog,
      status,
      progress: {
        ...progress,
        phase: managedPhases.indexOf(progress.phase) >= managedPhases.indexOf('second_account_synced')
          ? progress.phase
          : 'second_account_synced',
        second_wallet_id: String(secondWalletID),
      },
    }
  }, {
    password: PASSWORD, answers: ANSWERS, setup, checkpoint: recoveryCheckpoint, managedPhases: MANAGED_PHASES,
  })

  let managedProgress = activationBase.progress
  recoveryCheckpoint = persistManagedProgress(recoveryCheckpoint, managedProgress) || recoveryCheckpoint

  const temporaryCreated = await primary.evaluate(async ({ password, progress, managedPhases }) => {
    const verify = window.__SAT20_PWA_VERIFY__
    const wallet = verify.useWalletStore()
    const tuple = (result, operation) => {
      if (result?.[0]) throw new Error(`${operation}: ${result[0].message || result[0]}`)
      return result?.[1]
    }
    await wallet.syncWalletCatalog()
    const createdIndex = managedPhases.indexOf('temporary_wallet_created')
    if (managedPhases.indexOf(progress.phase) >= managedPhases.indexOf('temporary_wallet_delete_requested')) {
      return progress
    }
    if (managedPhases.indexOf(progress.phase) >= createdIndex) {
      const existing = wallet.wallets.find((item) => String(item.id) === progress.temporary_wallet_id)
      if (!existing) throw new Error('checkpointed temporary wallet is missing before resume')
      return progress
    }
    tuple(await wallet.createWallet(password), 'create temporary wallet')
    const temporaryWalletID = String(wallet.walletId)
    if (!temporaryWalletID || temporaryWalletID === progress.second_wallet_id) {
      throw new Error('created temporary wallet identity is invalid')
    }
    return { ...progress, phase: 'temporary_wallet_created', temporary_wallet_id: temporaryWalletID }
  }, { password: PASSWORD, progress: managedProgress, managedPhases: MANAGED_PHASES })
  managedProgress = temporaryCreated
  recoveryCheckpoint = persistManagedProgress(recoveryCheckpoint, managedProgress) || recoveryCheckpoint

  const temporaryAccountAdded = await primary.evaluate(async ({ progress, managedPhases }) => {
    const verify = window.__SAT20_PWA_VERIFY__
    const wallet = verify.useWalletStore()
    await wallet.syncWalletCatalog()
    if (managedPhases.indexOf(progress.phase) >= managedPhases.indexOf('temporary_wallet_delete_requested')) {
      return progress
    }
    const temporary = wallet.wallets.find((item) => String(item.id) === progress.temporary_wallet_id)
    if (!temporary) throw new Error('checkpointed temporary wallet is missing before subaccount creation')
    await wallet.switchWallet(temporary.id)
    if (managedPhases.indexOf(progress.phase) < managedPhases.indexOf('temporary_account_added') &&
        !temporary.accounts.some((entry) => entry.index === 1)) {
      await wallet.addAccount('Temporary Account 2', 1)
    }
    return {
      ...progress,
      phase: managedPhases.indexOf(progress.phase) >= managedPhases.indexOf('temporary_account_added')
        ? progress.phase
        : 'temporary_account_added',
    }
  }, { progress: managedProgress, managedPhases: MANAGED_PHASES })
  managedProgress = temporaryAccountAdded
  recoveryCheckpoint = persistManagedProgress(recoveryCheckpoint, managedProgress) || recoveryCheckpoint

  const temporarySynced = await primary.evaluate(async ({ progress, managedPhases }) => {
    const account = globalThis.sat20account_wasm
    const call = async (method, payload = {}) => {
      const response = await account[method](JSON.stringify(payload))
      if (!response || response.code !== 0) throw new Error(`${method}: ${response?.msg || 'failed'}`)
      return response.data
    }
    const deadline = Date.now() + 180_000
    let status
    while (Date.now() < deadline) {
      status = await call('status')
      if (status.active && Number(status.pending_changes || 0) === 0) {
        return {
          status,
          progress: {
            ...progress,
            phase: managedPhases.indexOf(progress.phase) >= managedPhases.indexOf('temporary_wallet_synced')
              ? progress.phase
              : 'temporary_wallet_synced',
          },
        }
      }
      await new Promise((resolve) => setTimeout(resolve, 1_000))
    }
    throw new Error(`temporary wallet creation: account state did not synchronize (${JSON.stringify(status)})`)
  }, { progress: managedProgress, managedPhases: MANAGED_PHASES })
  managedProgress = temporarySynced.progress
  recoveryCheckpoint = persistManagedProgress(recoveryCheckpoint, managedProgress) || recoveryCheckpoint

  const temporaryDeleteRequested = await primary.evaluate(async ({ progress, managedPhases }) => {
    const verify = window.__SAT20_PWA_VERIFY__
    const wallet = verify.useWalletStore()
    await wallet.syncWalletCatalog()
    if (managedPhases.indexOf(progress.phase) >= managedPhases.indexOf('temporary_wallet_deleted')) {
      return progress
    }
    const temporary = wallet.wallets.find((item) => String(item.id) === progress.temporary_wallet_id)
    if (temporary) {
      const [deleteError] = await wallet.deleteWallet(temporary.id)
      if (deleteError) throw deleteError
    }
    return { ...progress, phase: 'temporary_wallet_delete_requested' }
  }, { progress: managedProgress, managedPhases: MANAGED_PHASES })
  managedProgress = temporaryDeleteRequested
  recoveryCheckpoint = persistManagedProgress(recoveryCheckpoint, managedProgress) || recoveryCheckpoint

  const activationFinal = await primary.evaluate(async ({ progress }) => {
    const verify = window.__SAT20_PWA_VERIFY__
    const wallet = verify.useWalletStore()
    const account = globalThis.sat20account_wasm
    const call = async (method, payload = {}) => {
      const response = await account[method](JSON.stringify(payload))
      if (!response || response.code !== 0) throw new Error(`${method}: ${response?.msg || 'failed'}`)
      return response.data
    }
    const summarizeCatalog = (wallets) => wallets.map((item) => ({
      id: item.id,
      name: item.name,
      accounts: item.accounts.map((entry) => ({ index: entry.index, address: entry.address })),
    }))
    const deadline = Date.now() + 180_000
    let status
    while (Date.now() < deadline) {
      status = await call('status')
      if (status.active && Number(status.pending_changes || 0) === 0 &&
          status.managed_data_dirty !== true && !status.last_dkvs_sync_error) break
      await new Promise((resolve) => setTimeout(resolve, 1_000))
    }
    if (!status?.active || Number(status.pending_changes || 0) !== 0 ||
        status.managed_data_dirty === true || status.last_dkvs_sync_error) {
      throw new Error(`temporary wallet deletion: account state did not synchronize (${JSON.stringify(status)})`)
    }
    const catalog = await wallet.syncWalletCatalog()
    if (catalog.some((item) => String(item.id) === progress.temporary_wallet_id)) {
      throw new Error('deleted temporary wallet remains in the catalog')
    }
    const finalCatalog = summarizeCatalog(catalog)
    const finalSecond = finalCatalog.find((item) => String(item.id) === progress.second_wallet_id)
    if (!finalSecond?.accounts.some((entry) => entry.index === 2)) {
      throw new Error('managed subaccount is missing before recovery')
    }
    return { status, finalCatalog }
  }, { progress: managedProgress })
  managedProgress = { ...managedProgress, phase: 'temporary_wallet_deleted' }
  recoveryCheckpoint = persistManagedProgress(recoveryCheckpoint, managedProgress) || recoveryCheckpoint
  managedProgress = { ...managedProgress, phase: 'complete' }
  recoveryCheckpoint = persistManagedProgress(recoveryCheckpoint, managedProgress) || recoveryCheckpoint
  const activation = {
    locator: recoveryCheckpoint?.locator,
    userShare: recoveryCheckpoint?.user_share,
    preflight: activationBase.preflight,
    initialCatalog: activationBase.initialCatalog,
    finalCatalog: activationFinal.finalCatalog,
    status: activationFinal.status,
  }

  console.log('[Account live] activation and managed mutations synchronized')
  if (!activation.locator || !activation.userShare) {
    console.log(JSON.stringify({
      network: 'production/testnet',
      storageMode: activation.status.storage_mode,
      accountStateSeq: activation.status.state_seq,
      pendingChanges: activation.status.pending_changes,
      preflightWalletCount: activation.preflight.wallets.length,
      initialCatalog: activation.initialCatalog,
      finalCatalog: activation.finalCatalog,
      recovery: 'not-run: existing activation share is intentionally unavailable',
    }, null, 2))
    return
  }
  const cdp = await context.newCDPSession(recovery)
  await cdp.send('Storage.clearDataForOrigin', { origin: recoveryOrigin, storageTypes: 'all' })
  await primeEnvironment(recovery, RECOVERY_URL)
  console.log('[Account live] independent recovery PWA ready')

	const restored = await recovery.evaluate(async ({ credential, locator, userShare, answers }) => {
    const account = globalThis.sat20account_wasm
    const verify = window.__SAT20_PWA_VERIFY__
    const call = async (method, payload = {}) => {
      const response = await account[method](JSON.stringify(payload))
      if (!response || response.code !== 0) throw new Error(`${method}: ${response?.msg || 'failed'}`)
      return response.data
    }
    const summarizeCatalog = (wallets) => wallets.map((item) => ({
      id: item.id,
      name: item.name,
      accounts: item.accounts.map((entry) => ({ index: entry.index, address: entry.address })),
    }))
    const loaded = await call('loadRecovery', { locator })
    await call('recoverKnowledge', { session_id: loaded.session_id, answers })
    await call('setUserShare', { session_id: loaded.session_id, user_share: userShare })
    const preview = await call('previewRecovery', { session_id: loaded.session_id })
		const committed = await call('commitRecovery', { session_id: loaded.session_id, password: credential })
    await verify.walletStorage.initializeState()
    const wallet = verify.useWalletStore()
    const catalog = await wallet.syncWalletCatalog()
    return { preview: preview.summary, committed: committed.wallets, catalog: summarizeCatalog(catalog) }
  }, {
		credential: PASSWORD,
    locator: activation.locator,
    userShare: activation.userShare,
    answers: ANSWERS,
  })

  if (restored.catalog.length !== activation.finalCatalog.length) {
    throw new Error(`restored wallet count mismatch: ${restored.catalog.length} != ${activation.finalCatalog.length}`)
  }
  for (const expected of activation.finalCatalog) {
    const expectedRootAddress = expected.accounts.find((entry) => entry.index === 0)?.address
    const actual = restored.catalog.find((item) =>
      item.accounts.some((entry) => entry.index === 0 && entry.address === expectedRootAddress))
    if (!actual || actual.accounts.length !== expected.accounts.length) {
      throw new Error(`restored catalog mismatch for wallet ${expectedRootAddress}`)
    }
    for (const account of expected.accounts) {
      const restoredAccount = actual.accounts.find((entry) => entry.index === account.index)
      if (!restoredAccount || restoredAccount.address !== account.address) {
        throw new Error(`restored account mismatch for wallet ${expectedRootAddress} account ${account.index}`)
      }
    }
  }

  if (recoveryCheckpoint) {
    clearRecoveryCheckpoint()
    console.log('[Account live] recovery checkpoint removed after verified independent recovery')
  }

  console.log(JSON.stringify({
    network: 'production/testnet',
    storageMode: activation.status.storage_mode,
    accountStateSeq: activation.status.state_seq,
    pendingChanges: activation.status.pending_changes,
    preflightWalletCount: activation.preflight.wallets.length,
    initialCatalog: activation.initialCatalog,
    finalCatalog: activation.finalCatalog,
    recoveredWalletCount: restored.catalog.length,
    recoveredAccounts: restored.catalog.map((wallet) => ({ id: wallet.id, count: wallet.accounts.length })),
  }, null, 2))
}

main().then(() => process.exit(0)).catch((error) => {
  console.error(error)
  process.exit(1)
})
