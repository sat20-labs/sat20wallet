import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { transformSync } from 'esbuild'

const read = (path) => readFileSync(new URL(`../../${path}`, import.meta.url), 'utf8')
const readRepo = (path) => readFileSync(new URL(`../../../${path}`, import.meta.url), 'utf8')

test('closed current channels clear balances without clearing closing channels or accepting stale requests', async () => {
  const source = read('store/channel.ts')
    .replace(/^import .*$/gm, '')
    .replace('export const useChannelStore', 'const useChannelStore')
  const { code } = transformSync(source, { loader: 'ts', target: 'es2022' })
  const requests = []
  const store = runInNewContext(`${code}\nuseChannelStore()`, {
    defineStore: (_name, setup) => setup,
    ref: (value) => ({ value }),
    computed: (fn) => ({ get value() { return fn() } }),
    watch: () => {},
    satsnetStp: { getCurrentChannel: () => new Promise((resolve) => requests.push(resolve)) },
    console: { log() {}, warn() {} },
  })
  const seed = () => {
    store.channel.value = { status: 16, localbalanceL1: [] }
    store.totalSats.value = 90500
    store.plainBalance.value = 90500
  }
  const respond = (channel) => requests.shift()([null, { json: JSON.stringify(channel) }])
  for (const closed of [{ status: 0 }, { status: 0, localbalanceL1: [] }]) {
    seed()
    const pending = store.getCurrentChannel()
    respond(closed)
    await pending
    assert.equal(store.channel.value, null)
    assert.equal(store.totalSats.value, 0)
    assert.equal(store.plainBalance.value, 0)
    assert.equal(store.plainList.value.length, 0)
  }
  for (const status of [16, 7, 8, 9, 10, 11, 12, 13, 14, 15]) {
    const pending = store.getCurrentChannel()
    respond({ status, localbalanceL1: [] })
    await pending
    assert.equal(store.channel.value.status, status)
  }
  seed()
  const stale = store.getCurrentChannel()
  store.invalidateCurrentChannel()
  const current = store.getCurrentChannel()
  respond({ status: 16, localbalanceL1: [] })
  await stale
  assert.equal(store.channel.value, null)
  respond({ status: 0, localbalanceL1: [] })
  await current
  assert.equal(store.channel.value, null)
  const page = read('entrypoints/popup/pages/wallet/index.vue')
  assert.match(page, /case 'channelclosed':[\s\S]*?channelStore\.invalidateCurrentChannel\(\)[\s\S]*?await channelHandler\(\)/)
})

test('invoke fee display splits only addliq principal and preserves the SDK total', () => {
  const source = read('components/approve/ApproveInvokeContractV2SatsNet.vue')
  const feeComputeds = source.slice(source.indexOf('const networkFee = computed'), source.indexOf('const num = computed'))
  const quote = ({ action = 'addliq', param = { orderType: 9, value: 1000 }, fee = '1010', metadata = { action } } = {}) => {
    const context = {
      computed: (fn) => ({ get value() { return fn() } }),
      props: { data: { metadata } },
      parsedInvoke: { value: { action } },
      invokeParam: { value: param },
      estimatedFee: { value: fee },
    }
    return JSON.parse(JSON.stringify(runInNewContext(`${feeComputeds};\n({
      network: networkFee.value, principal: liquidityPrincipal.value,
      service: serviceFee.value, total: totalCost.value
    })`, context)))
  }
  assert.deepEqual(quote(), { network: 10, principal: 1000, service: 10, total: '1020 sats' })
  assert.deepEqual(quote({ metadata: { action: 'addliq', networkFee: 0 } }),
    { network: 0, principal: 1000, service: 10, total: '1010 sats' })
  assert.equal(quote({ metadata: { action: 'addliq', networkFee: null } }).network, 10)
  assert.equal(quote({ fee: '1000' }).service, 0)
  for (const action of ['swap', 'removeliq', 'withdraw']) {
    assert.equal(quote({ action }).principal, null)
    assert.equal(quote({ action }).service, '1010')
  }
  assert.deepEqual(quote({ action: 'swap', param: {}, fee: '10', metadata: { action: 'swap', orderType: 2, sats: 111 } }),
    { network: 10, principal: null, service: '10', total: '131 sats' })
  assert.equal(quote({ param: {} }).principal, null)
  assert.equal(quote({ param: { orderType: 1, value: 1000 } }).principal, null)
  assert.equal(quote({ param: { orderType: 9, value: 'bad' } }).principal, null)
  assert.equal(quote({ fee: '10' }).principal, null)
  assert.equal(quote({ fee: null }).total, '-')
  assert.match(source, /\{\{ networkFee \}\} sats/)
  assert.match(source, /\{\{ serviceFee \?\? '-' \}\} sats/)
})

test('legacy refund fee label changes without relabeling other contract calls', () => {
  const source = read('components/approve/ApproveInvokeContractSatsNet.vue')
  const refundComputed = source.slice(source.indexOf('const isRefund = computed'), source.indexOf('const formattedInvoke = computed'))
  const isRefund = (invoke) => runInNewContext(`${refundComputed}\nisRefund.value`, {
    computed: (fn) => ({ get value() { return fn() } }), props: { data: { invoke } },
  })
  assert.equal(isRefund('{"action":"refund"}'), true)
  for (const invoke of ['{"action":"mint"}', '{}', 'null', '{']) assert.equal(isRefund(invoke), false)
  assert.match(source, /isRefund \? \$t\('invokeContractSatsNet.callFee', '合约调用费'\)/)
})

test('lock-with-expand submitted toast uses an existing localized message', () => {
  const source = read('composables/useAssetActions.ts')
  const action = source.slice(source.indexOf('const lockUtxoWithExpand = async'))
  assert.match(action, /title: t\('tools.messages.txSubmitted'\)/)
  assert.doesNotMatch(action, /title: t\('messages.txSubmitted'\)/)
  for (const locale of ['en', 'zh']) {
    const messages = JSON.parse(read(`locales/${locale}.json`))
    assert.ok(messages.tools.messages.txSubmitted.length > 0)
  }
})

test('asset operation uses one dialog lifecycle without manual body cleanup', () => {
  const source = read('components/wallet/AssetOperationDialog.vue')
  const dialogContent = read('components/ui/dialog/DialogContent.vue')
  assert.match(source, /dialogStage === 'confirm'/)
  assert.doesNotMatch(source, /AlertDialog/)
  assert.doesNotMatch(source, /document\.body/)
  assert.doesNotMatch(source, /setTimeout/)
  assert.match(dialogContent, /data-\[state=closed\]:pointer-events-none/)
})

test('network selection closes first and avoids select double writes', () => {
  const picker = read('components/common/NetworkSelect.vue')
  const settings = read('components/setting/NetworkSetting.vue')
  assert.match(picker, /isOpen\.value = false[\s\S]*await nextTick\(\)[\s\S]*walletStore\.setNetwork/)
  assert.match(picker, /finally \{[\s\S]*switching\.value = false/)
  assert.match(settings, /:model-value="network"/)
  assert.doesNotMatch(settings, /v-model="network"/)
})

test('WASM release is asynchronous', () => {
  const wasm = readRepo('sdk/wasm/main.go')
  const release = wasm.slice(wasm.indexOf('func releaseManager'), wasm.indexOf('func startBTCLuckyMining'))
  assert.match(release, /Promise.*New\(handler\)/s)
  assert.match(release, /managerClosing = true/)
  assert.match(release, /managerAsyncTasks\.Wait\(\)[\s\S]*_mgr = nil[\s\S]*mgr\.Close\(\)/)
  assert.match(wasm, /func createAsyncJsHandler[\s\S]*if managerClosing[\s\S]*managerAsyncTasks\.Add\(1\)/)
})

test('explicit biometric unavailability blocks WebAuthn before loading', () => {
  const source = read('components/setting/SecuritySetting.vue')
  assert.match(source, /biometricExplicitlyUnavailable/)
  assert.match(source, /if \(biometricExplicitlyUnavailable\.value\)[\s\S]*return[\s\S]*const originError/)
  assert.match(source, /:disabled="biometricLoading \|\| \(!biometricEnabled && biometricExplicitlyUnavailable\)"/)
})

test('wallet basics uses strict page selection and bounded unlock stages', () => {
  const source = read('scripts/verify/wallet-basics-flow.mjs')
  assert.match(source, /selectPwaPage\(pages, PWA_URL\)/)
  assert.match(source, /actualOrigin !== expectedOrigin/)
	assert.match(source, /withTimeout\(wallet\.unlockWallet\(credential\), 'unlockWallet', 30000\)/)
  assert.match(source, /checking running-wallet password verification/)
})

test('channel lock forwards an invalid channel id to WASM validation', () => {
  const source = read('utils/stp.ts')
  const start = source.indexOf('async lockToChannel(')
  const end = source.indexOf('async unlockFromChannel(', start)
  const lockMethods = source.slice(start, end)
  assert.match(lockMethods, /'lockToChannel',\s*chanPoint,/)
  assert.match(lockMethods, /'lockToChannelWithExpand',\s*chanPoint,/)
  assert.doesNotMatch(lockMethods, /chanPoint\.toString\(\)/)
})

test('password settings persist the change through the wallet SDK', () => {
  const source = read('entrypoints/popup/pages/wallet/settings/password.vue')
	assert.match(source, /walletManager\.changePassword\(values\.oldPassword, values\.newPassword\)/)
	assert.doesNotMatch(source, /setWalletSessionPassword|setPassword/)
	assert.match(source, /finally\s*\{\s*resetForm\(\)/)
	assert.doesNotMatch(source, /hashPassword|oldHash|newHash/)
})

test('network switching publishes one atomic target snapshot after target reads succeed', () => {
  const source = read('store/wallet.ts')
  const start = source.indexOf('const setNetwork = async')
  const end = source.indexOf('const setChain = async', start)
  const networkSwitch = source.slice(start, end)
  assert.match(networkSwitch, /const previousWalletId = walletId\.value/)
  assert.match(networkSwitch, /const previousAccountIndex = Number\(accountIndex\.value/)
  assert.match(networkSwitch, /const previousWalletFingerprint = sourceCatalog\.find/)
  assert.match(networkSwitch, /const fixedRootAccountId = rootAccountId\.value \|\| await backfillTrustedRootAccountId/)
  assert.doesNotMatch(networkSwitch, /structuredClone\([^)]*wallets\.value/)
  assert.doesNotMatch(networkSwitch, /structuredClone\([^)]*accountRecovery\.value/)
  assert.match(networkSwitch, /const recovery = await recoverTargetNetworkAccount\(password, fixedRootAccountId\)/)
  assert.match(networkSwitch, /const targetRecovery = \{[\s\S]*network: value,[\s\S]*rootAccountId: fixedRootAccountId/)
  assert.match(networkSwitch, /const targetCatalog = await readWalletCatalog\(\)[\s\S]*assertWalletTopologyCompatible\(sourceCatalog, targetCatalog, !sourceRecoveryWasComplete\)[\s\S]*const identity = await readWalletIdentity\(previousAccountIndex\)/)
  assert.match(networkSwitch, /walletStorage\.batchUpdate\(\{[\s\S]*network: value,[\s\S]*walletId: targetWallet\.id,[\s\S]*rootAccountId: fixedRootAccountId,[\s\S]*wallets: toRaw\(targetCatalog\)/)
  assert.doesNotMatch(networkSwitch, /walletId\.value = recovery\.walletId/)
  assert.doesNotMatch(networkSwitch, /walletStorage\.setValue\('accountRecovery'/)
})

test('first mnemonic import discovers managed recovery before falling back to local import', () => {
  const source = read('store/wallet.ts')
  const start = source.indexOf('const importWallet = async')
  const end = source.indexOf('const getWalletInfo = async', start)
  const importFlow = source.slice(start, end)
  assert.match(importFlow, /walletManager\.validateMnemonic\(mnemonic, ''\)/)
  assert.doesNotMatch(importFlow, /walletManager\.validateMnemonic\(mnemonic, password\)/)
  assert.match(importFlow, /const shouldDiscoverRoot = !rootAccountId\.value \|\| !hasWallet\.value \|\| accountRecovery\.value\?\.status === 'pending'/)
  assert.match(importFlow, /walletManager\.recoverAccountManagementFromRootMnemonic\([\s\S]*if \(recoveryError \|\| !recovery\)[\s\S]*return/)
  assert.match(importFlow, /const fixedRootAccountId = rootAccountId\.value \|\| recoveredRootAccountId[\s\S]*discoveredRootAccountId = fixedRootAccountId[\s\S]*walletStorage\.batchUpdate\(\{[\s\S]*rootAccountId: discoveredRootAccountId/)
  assert.match(importFlow, /if \(recovery\.status === 'found'\)[\s\S]*recovered = true/)
  assert.match(importFlow, /if \(!recovered\)[\s\S]*walletManager\.importWallet\(processedMnemonic, password\)/)
  assert.match(importFlow, /walletManager\.importWallet\(processedMnemonic, password\)/)
})

test('root account identity is a stable public key and local wallet IDs remain transient', () => {
  const store = read('store/wallet.ts')
  const storage = read('lib/walletStorage.ts')
  const restore = read('entrypoints/popup/pages/RestoreAccount.vue')
  assert.match(storage, /rootAccountId: string/)
  assert.match(storage, /rootAccountId: ''/)
  assert.doesNotMatch(storage, /rootWalletId/)
  assert.match(store, /const createsRootWallet = wallets\.value\.length === 0 && !rootAccountId\.value[\s\S]*await setRootAccountId\(createdRootAccountId\)/)
  assert.match(store, /const managedRoot = status\.active && status\.account_id[\s\S]*Managed root account is missing from the local wallet catalog/)
  assert.match(store, /account\.accountId === rootAccountId\.value[\s\S]*Root wallet cannot be deleted/)
  assert.doesNotMatch(store, /setRootAccountId\(walletId\)/)
  assert.match(restore, /const rootAccountId = String\(result\.account_id \|\| ''\)[\s\S]*rootAccountId,[\s\S]*walletId: rootWalletId/)
})

test('network identity uses mainnet and migrates legacy livenet storage', () => {
  const types = read('types/index.ts')
  const config = read('config/wasm.ts')
  const storage = read('lib/walletStorage.ts')
  assert.match(types, /MAINNET = 'mainnet'/)
  assert.doesNotMatch(types, /LIVENET/)
  assert.match(config, /\[Network\.MAINNET\]: NetworkConfig/)
  assert.doesNotMatch(config, /Network\.LIVENET/)
  assert.match(storage, /network: Network\.MAINNET/)
  assert.match(storage, /state\.network as string\) === 'livenet'[\s\S]*state\.network = Network\.MAINNET/)
  assert.match(storage, /state\.accountRecovery && \(state\.accountRecovery\.network as string\) === 'livenet'[\s\S]*state\.accountRecovery\.network = Network\.MAINNET/)
})

test('account live setup distinguishes the automatic temporary profile from configured active state', () => {
  const source = read('scripts/live/account-management-pwa-live.mjs')
  const start = source.indexOf('const requiresPaidRecoverySetup')
  const end = source.indexOf('const waitSynced', start)
  assert.ok(start >= 0 && end > start)
  const classifier = source.slice(start, end)
  const requiresSetup = (status) => runInNewContext(
    `${classifier}; requiresPaidRecoverySetup(status)`, { status },
  )

  assert.equal(requiresSetup({ active: false }), true)
  assert.equal(requiresSetup({
    active: true, storage_mode: 'temporary', recovery_configured: false, state_seq: 1,
  }), true)
  for (const status of [
    { active: true, storage_mode: 'temporary', recovery_configured: false, state_seq: 2 },
    { active: true, storage_mode: 'temporary', recovery_configured: true, state_seq: 1 },
    { active: true, storage_mode: 'paid', recovery_configured: false, state_seq: 1 },
    { active: true, storage_mode: 'paid', recovery_configured: true, state_seq: 5 },
  ]) {
    assert.equal(requiresSetup(status), false)
  }
  assert.match(source, /if \(requiresPaidRecoverySetup\(status\)\)[\s\S]*else if \(continueActive\)[\s\S]*already active/)
})

test('account live setup imports only missing configured mnemonics', () => {
  const source = read('scripts/live/account-management-pwa-live.mjs')
  const start = source.indexOf('const hasMnemonicWallet')
  const end = source.indexOf('const requiresPaidRecoverySetup', start)
  assert.ok(start >= 0 && end > start)
  const matcher = source.slice(start, end)
  const known = (wallets, identity) => runInNewContext(
    `${matcher}; hasMnemonicWallet(wallets, identity)`, { wallets, identity },
  )
  const firstValidation = {
    code: 0,
    msg: 'ok',
    data: {
      normalized: 'first twelve word test mnemonic',
      language: 'english',
      wordCount: '12',
      fingerprint: 'first-fingerprint',
      address: 'tb1-first',
    },
  }
  const secondValidation = {
    code: 0,
    msg: 'ok',
    data: {
      normalized: 'second twelve word test mnemonic',
      language: 'english',
      wordCount: '12',
      fingerprint: 'second-fingerprint',
      address: 'tb1-second',
    },
  }
  const first = firstValidation.data
  const second = secondValidation.data
  const lockedWallets = [{
    id: '1788164317331000',
    name: 'Wallet 1',
    accounts: [{ index: 0, name: 'Account 1', did: '' }],
  }]
  const wallets = [{
    id: '1788164317331000',
    name: 'Wallet 1',
    accounts: [{
      index: 0,
      name: 'Account 1',
      did: '',
      address: first.address,
      pubKey: 'first-pubkey',
    }],
  }]
  const configured = [first, second]

  assert.equal(known(lockedWallets, first), false)
  assert.equal(known(wallets, first), true)
  assert.equal(known(wallets, second), false)
  assert.equal(known([], first), false)
  assert.deepEqual(configured.filter((identity) => !known([], identity)), configured)
  assert.deepEqual(configured.filter((identity) => !known(wallets, identity)), [second])
  assert.deepEqual(configured.filter((identity) => !known([
    { accounts: [{ index: 0, address: second.address }] },
  ], identity)), [first])
  assert.match(source, /syncWalletCatalog\(\)\.catch[\s\S]*if \(wallet\.wallets\.length > 0\)[\s\S]*wallet\.unlockWallet\(credential\)[\s\S]*wallet\.syncWalletCatalog\(\)[\s\S]*for \(const mnemonic of mnemonics\)/)
  assert.match(source, /for \(const mnemonic of mnemonics\)[\s\S]*validateMnemonic\(mnemonic, ''\)[\s\S]*hasMnemonicWallet\(wallet\.wallets, validation\.data\)\) continue[\s\S]*wallet\.importWallet\(mnemonic, credential\)/)
})

test('account live checks both browser origins before any paid recovery write', () => {
  const source = read('scripts/live/account-management-pwa-live.mjs')
  const primaryCheck = source.indexOf("await assertMessageServiceCors(primary, 'primary')")
  const recoveryCheck = source.indexOf("await assertMessageServiceCors(recovery, 'recovery')")
  const paidWrite = source.indexOf("call('confirmStorage', { option_id: 'paid'")
  assert.ok(primaryCheck >= 0 && recoveryCheck > primaryCheck && paidWrite > recoveryCheck)
  const probeStart = source.indexOf('const assertMessageServiceCors = async')
  const probeEnd = source.indexOf('const primeEnvironment', probeStart)
  const probe = source.slice(probeStart, probeEnd)
  assert.match(probe, /fetch\(url,[\s\S]*method: 'GET'[\s\S]*mode: 'cors'[\s\S]*credentials: 'omit'/)
  assert.doesNotMatch(probe, /method: 'POST'/)
  assert.match(source, /primaryOrigin === recoveryOrigin[\s\S]*primary and recovery PWA origins must be different/)
})

test('account live checkpoints a created recovery before rehearsal and resumes it safely', () => {
  const source = read('scripts/live/account-management-pwa-live.mjs')
  assert.match(source, /SAT20_ACCOUNT_RECOVERY_CHECKPOINT[\s\S]*\/private\/tmp\/sat20-account-management-pwa-live-checkpoint\.json/)
  assert.match(source, /RECOVERY_CHECKPOINT\.startsWith\('\/private\/tmp\/'\)/)
  assert.match(source, /lstatSync\(RECOVERY_CHECKPOINT\)[\s\S]*isSymbolicLink\(\)[\s\S]*info\.mode & 0o077/)
  assert.match(source, /writeFileSync\(temporary,[\s\S]*mode: 0o600, flag: 'wx'[\s\S]*renameSync\(temporary, RECOVERY_CHECKPOINT\)/)
  assert.match(source, /finally\s*\{[\s\S]*existsSync\(temporary\)[\s\S]*unlinkSync\(temporary\)/)
  assert.match(source, /if \(savedCheckpoint\)[\s\S]*original primary PWA\/WASM session preserved[\s\S]*else \{[\s\S]*primeEnvironment\(primary, PRIMARY_URL\)/)
  assert.match(source, /savedCheckpoint && !primary[\s\S]*checkpoint resume requires the original primary PWA page/)

  const created = source.indexOf("const created = await call('createRecovery'")
  const checkpointResult = source.indexOf("phase: 'created'", created)
  const persisted = source.indexOf('writeRecoveryCheckpoint(setup.checkpoint)', checkpointResult)
  const rehearsal = source.indexOf("const rehearsal = await call('rehearse'", persisted)
  assert.ok(created >= 0 && checkpointResult > created && persisted > checkpointResult && rehearsal > persisted)
  assert.match(source.slice(created, checkpointResult), /createRecovery/)
  assert.match(source, /if \(resumeCheckpoint\)[\s\S]*phase: 'resume'[\s\S]*confirmStorage/)
  assert.match(source, /setup\.phase === 'created' \|\| setup\.phase === 'resume'[\s\S]*session_id: checkpoint\.session_id[\s\S]*user_share: checkpoint\.user_share/)

  const checkpointFields = source.slice(source.indexOf('checkpoint: {', created), source.indexOf('session_expires_at:', created) + 80)
  for (const field of ['session_id', 'locator', 'user_share', 'account_id', 'root_wallet_id']) {
    assert.match(checkpointFields, new RegExp(`${field}:`))
  }
  assert.doesNotMatch(checkpointFields, /password:|mnemonics:|answers:/)
  const verification = source.indexOf('for (const expected of activation.finalCatalog)')
  const cleanup = source.indexOf('clearRecoveryCheckpoint()', verification)
  assert.ok(verification >= 0 && cleanup > verification)
  assert.match(source.slice(cleanup - 100, cleanup + 200), /if \(recoveryCheckpoint\)[\s\S]*clearRecoveryCheckpoint\(\)/)
})

test('account live checkpoints managed mutation progress and never guesses a resumed temporary wallet', () => {
  const source = read('scripts/live/account-management-pwa-live.mjs')
  assert.match(source, /value\?\.version === 1 \|\| value\?\.version === 2/)
  assert.match(source, /managed_progress:[\s\S]*phase: 'not_started'[\s\S]*second_wallet_id:/)
  assert.match(source, /inferLegacyManagedProgress[\s\S]*extras\.length !== 1[\s\S]*legacy checkpoint cannot safely identify/)
  assert.match(source, /String\(wallet\.walletId\) !== String\(temporary\.id\)[\s\S]*legacy checkpoint temporary wallet evidence is ambiguous/)
  assert.match(source, /indexes\.includes\(1\) \? 'temporary_account_added' : 'temporary_wallet_created'/)
  assert.match(source, /resumeCheckpoint\?\.version === 1[\s\S]*checkpointUpdate = \{ \.\.\.resumeCheckpoint, version: 2/)

  const create = source.indexOf("tuple(await wallet.createWallet(password), 'create temporary wallet')")
  const createdReturn = source.indexOf("phase: 'temporary_wallet_created'", create)
  const createdPersist = source.indexOf('persistManagedProgress(recoveryCheckpoint, managedProgress)', createdReturn)
  const addAccount = source.indexOf("wallet.addAccount('Temporary Account 2', 1)", createdPersist)
  assert.ok(create >= 0 && createdReturn > create && createdPersist > createdReturn && addAccount > createdPersist)

  assert.match(source, /if \(managedPhases\.indexOf\(progress\.phase\) >= createdIndex\)[\s\S]*checkpointed temporary wallet is missing before resume[\s\S]*return progress/)
  assert.match(source, /temporary_wallet_id: temporaryWalletID/)
  assert.match(source, /'temporary_account_added'/)
  assert.match(source, /'temporary_wallet_synced'/)
  assert.match(source, /phase: 'temporary_wallet_deleted'/)
  assert.match(source, /managedProgress = \{ \.\.\.managedProgress, phase: 'complete' \}/)

  const inferStart = source.indexOf('const inferLegacyManagedProgress = () => {')
  const inferEnd = source.indexOf('    const validateManagedProgress =', inferStart)
  assert.ok(inferStart >= 0 && inferEnd > inferStart)
  const declaration = source.slice(inferStart, inferEnd).trim()
  const infer = new Function(
    'wallet', 'status', 'configuredIDs', 'configuredWallets', 'secondWalletID',
    `${declaration}; return inferLegacyManagedProgress()`,
  )
  const configuredWallets = [
    { id: '11', accounts: [{ index: 0 }] },
    { id: '22', accounts: [{ index: 0 }, { index: 2 }] },
  ]
  const currentFailureCatalog = {
    walletId: '33',
    wallets: [...configuredWallets, { id: '33', accounts: [{ index: 0 }, { index: 1 }] }],
  }
  const currentFailureStatus = {
    active: true,
    storage_mode: 'paid',
    recovery_configured: true,
    pending_changes: 1,
    managed_data_dirty: true,
  }
  assert.deepEqual(infer(
    currentFailureCatalog,
    currentFailureStatus,
    new Set(['11', '22']),
    configuredWallets,
    '22',
  ), {
    phase: 'temporary_account_added',
    second_wallet_id: '22',
    temporary_wallet_id: '33',
  })
  assert.throws(() => infer(
    { ...currentFailureCatalog, walletId: '22' },
    currentFailureStatus,
    new Set(['11', '22']),
    configuredWallets,
    '22',
  ), /temporary wallet evidence is ambiguous/)
})

test('an explicit local market URL is not rewritten to the production swap path', () => {
  const source = read('entrypoints/popup/pages/wallet/DappMarket.vue')
  const start = source.indexOf('const resolveMarketUrl =')
  const end = source.indexOf('const marketUrl =', start)
  const resolver = source.slice(start, end)
  assert.match(resolver, /const configuredUrl = import\.meta\.env\.VITE_SAT20_MARKET_URL/)
  assert.match(resolver, /DEFAULT_TESTNET_MARKET_URL/)
  assert.doesNotMatch(resolver, /url\.pathname = ['"]\/swap\//)
})

test('available sats reset immediately when the wallet context changes', () => {
  const source = read('components/asset/BalanceSummary.vue')
  assert.match(source, /watch\(availableSatsContextKey,[\s\S]*availableAmt: 0, lockedAmt: 0[\s\S]*flush: 'sync'/)
})

test('RGB11 free address transfer is exposed only for the account-management root', () => {
  const source = read('components/wallet/RGB11SendDialog.vue')
  assert.match(source, /const directAddressAvailable = computed\([\s\S]*Number\(walletStore\.accountIndex\) === 0/)
  assert.match(source, /account\.accountId === walletStore\.rootAccountId/)
  assert.match(source, /:disabled="!directAddressAvailable"/)
  assert.match(source, /transferMode\.value = 'invoice'/)
  assert.match(source, /rgb11Transfer\.directRootOnly/)
})

test('live account root repair requires explicit apply opt-in', () => {
  const source = read('scripts/live/account-root-wrapper-repair-live.mjs')
  assert.match(source, /SAT20_ACCOUNT_REPAIR_APPLY === '1'/)
  assert.doesNotMatch(source, /SAT20_ACCOUNT_REPAIR_APPLY !== '0'/)
})
