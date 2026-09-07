import { chromium } from '@playwright/test'
import { spawn } from 'node:child_process'
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const ORIGIN = process.env.SAT20_ACCOUNT_REPAIR_ORIGIN || 'http://localhost:5174'
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456'
const PEER_HOST = process.env.SAT20_ACCOUNT_REPAIR_PEER_HOST || ''
const APPLY = process.env.SAT20_ACCOUNT_REPAIR_APPLY === '1'
const RESUME_FLOOR = process.env.SAT20_ACCOUNT_REPAIR_RESUME_FLOOR || ''
const scriptDir = dirname(fileURLToPath(import.meta.url))
const sdkDir = resolve(scriptDir, '../../../sdk')

const run = (command, args, options) => new Promise((resolvePromise, reject) => {
  const child = spawn(command, args, { ...options, stdio: ['ignore', 'pipe', 'pipe'] })
  let stdout = ''
  let stderr = ''
  child.stdout.on('data', (chunk) => { stdout += chunk })
  child.stderr.on('data', (chunk) => { stderr += chunk })
  child.on('error', reject)
  child.on('close', (code) => {
    if (code === 0) resolvePromise({ stdout, stderr })
    else reject(new Error(`${command} exited ${code}\n${stdout}\n${stderr}`))
  })
})

const browser = await chromium.connectOverCDP(CDP)
const context = browser.contexts()[0]
const page = context.pages().find((candidate) => candidate.url().startsWith(ORIGIN))
if (!page) throw new Error(`PWA page not found for ${ORIGIN}`)

const snapshot = await page.evaluate(async ({ password, origin }) => {
  const verify = window.__SAT20_PWA_VERIFY__
	if (!verify) throw new Error('PWA verification API is unavailable')
  const values = {}
  for (let index = 0; index < localStorage.length; index++) {
    const key = localStorage.key(index)
    if (!key) continue
    if (!key.startsWith('wallet-id-') &&
      !key.endsWith('account-management-profile-v2') &&
      !key.startsWith('dkvs-batch-outbox:')) continue
    const value = localStorage.getItem(key)
    if (value !== null) values[key] = value
  }
  if (!Object.keys(values).some((key) => key.endsWith('account-management-profile-v2'))) {
    throw new Error('account management profile is absent from local encrypted storage')
  }
  if (!Object.keys(values).some((key) => key.startsWith('wallet-id-'))) {
    throw new Error('encrypted root wallet record is absent')
  }
  return {
    origin,
		password,
    values,
  }
}, { password: PASSWORD, origin: ORIGIN })

const tempDir = mkdtempSync(join(tmpdir(), 'sat20-account-wrapper-repair-'))
const snapshotPath = join(tempDir, 'snapshot.json')
try {
  writeFileSync(snapshotPath, JSON.stringify(snapshot), { mode: 0o600 })
  chmodSync(snapshotPath, 0o600)
  const diagnosticEnv = {
    ...process.env,
    GOCACHE: process.env.GOCACHE || '/tmp/sat20wallet-go-build',
    SAT20_ACCOUNT_DIAG_SNAPSHOT: snapshotPath,
    SAT20_ACCOUNT_DIAG_ORIGIN: ORIGIN,
    SAT20_ACCOUNT_DIAG_PEER_HOST: PEER_HOST,
  }
  if (!APPLY) diagnosticEnv.SAT20_ACCOUNT_DIAG_INSPECT_ROOT_WRAPPER_PATH = '1'
  if (APPLY) diagnosticEnv.SAT20_ACCOUNT_DIAG_REPAIR_ROOT_WRAPPER = '1'
  if (RESUME_FLOOR) diagnosticEnv.SAT20_ACCOUNT_DIAG_RESUME_ROOT_WRAPPER_FLOOR = RESUME_FLOOR
  const result = await run('go', [
    'test', './wallet',
    '-run', '^TestDiagnosticAccountDKVSProfile$',
    '-count=1', '-v',
  ], {
    cwd: sdkDir,
    env: diagnosticEnv,
  })
  process.stdout.write(result.stdout)
  process.stderr.write(result.stderr)

  if (!APPLY) {
    console.log(JSON.stringify({ result: 'READ_ONLY_PASS', origin: ORIGIN }, null, 2))
  } else {
    await page.reload({ waitUntil: 'domcontentloaded' })
    await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__ &&
      globalThis.sat20wallet_wasm && globalThis.sat20account_wasm), null, { timeout: 180_000 })
    const recovery = await page.evaluate(async ({ password }) => {
    const verify = window.__SAT20_PWA_VERIFY__
		await verify.walletStorage.initializeState()
    const wallet = verify.useWalletStore()
    await wallet.syncWalletCatalog()
		const [unlockError] = await wallet.unlockWallet(password)
    if (unlockError && !/already unlocked/i.test(String(unlockError.message || unlockError))) {
      throw unlockError
    }
    const selectedWalletId = String(wallet.walletId || '')
    if (!selectedWalletId) throw new Error('active PWA wallet id is unavailable')
		const switchWalletResponse = await globalThis.sat20wallet_wasm.switchWallet(selectedWalletId, password)
    if (Number(switchWalletResponse?.code ?? -1) !== 0) {
      throw new Error(String(switchWalletResponse?.msg || 'switch active wallet failed'))
    }
    const switchAccountResponse = await globalThis.sat20wallet_wasm.switchAccount(Number(wallet.accountIndex || 0))
    if (Number(switchAccountResponse?.code ?? -1) !== 0) {
      throw new Error(String(switchAccountResponse?.msg || 'switch active account failed'))
    }
		const response = await globalThis.sat20wallet_wasm.recoverAccountManagementFromCurrentWallet(password)
    const statusResponse = await globalThis.sat20account_wasm.status('{}')
    const status = statusResponse?.data || statusResponse
    return {
      recovery: {
        code: Number(response?.code ?? -1),
        message: String(response?.msg || ''),
        status: response?.data?.status || '',
        recoveryCode: response?.data?.code || '',
      },
      status: {
        active: Boolean(status?.active),
        storageMode: status?.storage_mode || '',
        stateSeq: Number(status?.state_seq || 0),
        managedDataRevision: Number(status?.managed_data_revision || 0),
        managedDataDirty: Boolean(status?.managed_data_dirty),
        pendingChanges: Number(status?.pending_changes || 0),
        lastErrorCode: status?.last_dkvs_sync_error_code || '',
        lastError: status?.last_dkvs_sync_error || '',
      },
    }
    }, { password: PASSWORD })
    if (recovery.recovery.code !== 0 || recovery.recovery.status !== 'found') {
      throw new Error(`root recovery verification failed: ${JSON.stringify(recovery.recovery)}`)
    }
    if (!recovery.status.active || recovery.status.managedDataDirty || recovery.status.pendingChanges !== 0) {
      throw new Error(`account status is not ready after root-wrapper repair: ${JSON.stringify(recovery.status)}`)
    }
    console.log(JSON.stringify({ result: 'PASS', origin: ORIGIN, peerHost: PEER_HOST, ...recovery }, null, 2))
  }
} finally {
	snapshot.password = ''
  snapshot.values = {}
  rmSync(tempDir, { recursive: true, force: true })
  await browser.close().catch(() => {})
}
