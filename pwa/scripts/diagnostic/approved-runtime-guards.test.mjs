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
  assert.match(source, /isRefund \? \$t\('invokeContractSatsNet.callFee'\) : \$t\('invokeContractSatsNet.mintingFee'\)/)
  for (const [locale, expectedCallFee, expectedMintingFee] of [
    ['en', 'Contract call fee', 'Minting fee'],
    ['zh', '合约调用费', '铸造费用'],
  ]) {
    const messages = JSON.parse(read(`locales/${locale}.json`))
    assert.equal(messages.invokeContractSatsNet.callFee, expectedCallFee)
    assert.equal(messages.invokeContractSatsNet.mintingFee, expectedMintingFee)
  }
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
  assert.match(importFlow, /const firstImportSignal = !hasWallet\.value \? signal : undefined/)
  assert.match(importFlow, /walletManager\.recoverAccountManagementFromRootMnemonic\(\s*processedMnemonic,\s*password,\s*firstImportSignal,?\s*\)[\s\S]*if \(recoveryError \|\| !recovery\)[\s\S]*return/)
  assert.match(importFlow, /const fixedRootAccountId = rootAccountId\.value \|\| recoveredRootAccountId[\s\S]*discoveredRootAccountId = fixedRootAccountId[\s\S]*walletStorage\.batchUpdate\(\{[\s\S]*rootAccountId: discoveredRootAccountId/)
  assert.match(importFlow, /if \(recovery\.status === 'found'\)[\s\S]*recovered = true/)
  assert.match(importFlow, /if \(!recovered\) \{[^}]*walletManager\.importWallet\(processedMnemonic, password, firstImportSignal\)/)
  assert.equal(importFlow.match(/walletManager\.importWallet\(/g)?.length, 1)
})

test('root account identity is a stable public key and local wallet IDs remain transient', () => {
  const store = read('store/wallet.ts')
  const storage = read('lib/walletStorage.ts')
  const restore = read('entrypoints/popup/pages/RestoreAccount.vue')
  assert.match(storage, /rootAccountId: string/)
  assert.match(storage, /rootAccountId: ''/)
  assert.doesNotMatch(storage, /rootWalletId/)
  assert.match(store, /const readWalletCatalogState = async[\s\S]*walletManager\.getWalletCatalog\(\)[\s\S]*rootAccountId: result\.root_account_id/)
  assert.match(store, /const syncWalletCatalog = async[\s\S]*rootAccountId: snapshot\.rootAccountId/)
  assert.match(store, /const managedRoot = status\.active && status\.account_id[\s\S]*Managed root account is missing from the local wallet catalog/)
  assert.match(store, /account\.accountId === rootAccountId\.value[\s\S]*Root wallet cannot be deleted/)
  assert.doesNotMatch(store, /setRootAccountId\(walletId\)/)
  assert.match(restore, /await accountSDK\.commitRecovery\(sessionId\.value, password\.value\)[\s\S]*step\.value = 6[\s\S]*step\.value = 5[\s\S]*setTimeout\(reloadWallet, 500\)/)
  assert.match(restore, /window\.location\.hash = '#\/unlock'[\s\S]*window\.location\.reload\(\)/)
  assert.doesNotMatch(restore, /walletStorage\.batchUpdate|setRootAccountId\(/)
})

test('network identity uses mainnet and migrates legacy livenet storage', () => {
  const types = read('types/index.ts')
  const config = read('config/wasm.ts')
  const storage = read('lib/walletStorage.ts')
  assert.match(types, /MAINNET = 'mainnet'/)
  assert.doesNotMatch(types, /LIVENET/)
  const source = `${types}\n${config.replace(/^import .*$/gm, '')}`
  const { code } = transformSync(source, { loader: 'ts', target: 'es2022', format: 'cjs' })
  const getConfig = runInNewContext(`${code}\nmodule.exports.getConfig`, { module: { exports: {} } })
  for (const env of ['dev', 'test', 'prd']) {
    const mainnet = getConfig(env, 'mainnet')
    assert.equal(mainnet.Chain, 'mainnet')
    assert.equal(mainnet.IndexerL1.Proxy, 'btc/mainnet')
    assert.equal(mainnet.IndexerL2.Proxy, 'satsnet/mainnet')
    assert.throws(() => getConfig(env, 'livenet'), /Invalid network/)
  }
  assert.doesNotMatch(config, /Network\.LIVENET/)
  const storageSource = `${types}\n${read('utils/networkMode.ts')}\n${storage}`
    .replace(/^import .*$/gm, '')
  for (const [mode, network] of [['production', 'mainnet'], ['regtest', 'regtest']]) {
    const { code: storageCode } = transformSync(storageSource, {
      loader: 'ts', target: 'es2022', format: 'cjs',
      define: { 'import.meta.env.MODE': JSON.stringify(mode) },
    })
    const walletStorage = runInNewContext(`${storageCode}\nmodule.exports.walletStorage`, { module: { exports: {} } })
    assert.equal(walletStorage.getValue('network'), network)
  }
  assert.match(storage, /state\.network as string\) === 'livenet'[\s\S]*state\.network = Network\.MAINNET/)
  assert.match(storage, /state\.accountRecovery && \(state\.accountRecovery\.network as string\) === 'livenet'[\s\S]*state\.accountRecovery\.network = Network\.MAINNET/)
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

test('RGB11 direct root admission belongs to SDK, not the thin PWA', () => {
  // Static ownership guard only. rgb11_address_api_test.go checks child-account
  // transfer rejection and non-root public-key scope rejection; RGBDirectOffline
  // exercises the ordinary root sending flow, not non-root admission.
  const source = readRepo('sdk/wallet/rgb_direct_preview.go')
  assert.match(source, /if !p\.rgb11ManagerIsRoot\(manager\)\s*\{\s*return nil, ErrRGB11TraditionalReceiveRequired\s*\}/)
  assert.match(readRepo('sdk/wallet/rgb11_address_api_test.go'), /if manager\.rgb11ManagerIsRoot\(manager\.rgbManager\)/)
})
