import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const read = (path) => readFileSync(new URL(`../../${path}`, import.meta.url), 'utf8')
const readRepo = (path) => readFileSync(new URL(`../../../${path}`, import.meta.url), 'utf8')

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
  assert.match(source, /withTimeout\(wallet\.unlockWallet\(hashed\), 'unlockWallet', 30000\)/)
  assert.match(source, /checking already-unlocked SDK response/)
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
  assert.match(source, /hashPassword\(values\.oldPassword\)/)
  assert.match(source, /walletManager\.changePassword\(oldHash, newHash\)/)
  assert.match(source, /if \(err\)[\s\S]*return[\s\S]*walletStore\.setPassword\(newHash\)/)
})

test('network switching publishes one atomic target snapshot after target reads succeed', () => {
  const source = read('store/wallet.ts')
  const start = source.indexOf('const setNetwork = async')
  const end = source.indexOf('const setChain = async', start)
  const networkSwitch = source.slice(start, end)
  assert.match(networkSwitch, /const previousWalletId = walletId\.value/)
  assert.match(networkSwitch, /const previousAccountIndex = Number\(accountIndex\.value/)
  assert.match(networkSwitch, /const identity = await readWalletIdentity\(targetAccountIndex\)[\s\S]*walletStorage\.batchUpdate\(\{[\s\S]*network: value,[\s\S]*walletId: targetWalletId/)
  assert.doesNotMatch(networkSwitch, /walletId\.value = recoveredWalletId/)
  assert.doesNotMatch(networkSwitch, /walletStorage\.setValue\('accountRecovery'/)
})

test('available sats reset immediately when the wallet context changes', () => {
  const source = read('components/asset/BalanceSummary.vue')
  assert.match(source, /watch\(availableSatsContextKey,[\s\S]*availableAmt: 0, lockedAmt: 0[\s\S]*flush: 'sync'/)
})

test('live account root repair requires explicit apply opt-in', () => {
  const source = read('scripts/live/account-root-wrapper-repair-live.mjs')
  assert.match(source, /SAT20_ACCOUNT_REPAIR_APPLY === '1'/)
  assert.doesNotMatch(source, /SAT20_ACCOUNT_REPAIR_APPLY !== '0'/)
})
