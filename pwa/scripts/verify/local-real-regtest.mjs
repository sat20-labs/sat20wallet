import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import { spawn, spawnSync } from 'node:child_process'
import {
  accessSync, chmodSync, closeSync, copyFileSync, existsSync, lstatSync, mkdirSync,
  mkdtempSync, openSync, readFileSync, readdirSync, realpathSync, renameSync, rmSync, statSync, writeFileSync,
} from 'node:fs'
import { constants as fsConstants } from 'node:fs'
import { createServer } from 'node:net'
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'
import { tmpdir } from 'node:os'
import { performance } from 'node:perf_hooks'

const CORE_P2P_PORT = 18444
const INDEXER_CONFIG_RELATIVE_PATH = 'install/conf_regtest.yaml'
const SDK_WALLETS_RELATIVE_PATH = 'sat20wallet/sdk/e2e/testdata/regtest/wallets.json'
const PRIVATE_TMP_ROOT = '/private/tmp'
const CORE_READY_TIMEOUT_MS = 60_000
const INDEXER_READY_TIMEOUT_MS = 180_000
const ELECTRS_READY_TIMEOUT_MS = 180_000

function canonicalWithMissingSuffix(path) {
  const fullPath = resolve(path)
  let existing = fullPath
  while (!existsSync(existing)) {
    const parent = dirname(existing)
    assert.notEqual(parent, existing, `no existing parent for ${fullPath}`)
    existing = parent
  }
  const realExisting = realpathSync(existing)
  return resolve(realExisting, relative(existing, fullPath))
}

const runAsText = (command, args, env) => {
  const result = spawnSyncText(command, args, env)
  assert.equal(result.error, undefined, `could not start ${basename(command)}: ${result.error?.message ?? ''}`)
  assert.equal(result.status, 0, `${basename(command)} ${args[0] ?? ''} failed with exit ${result.status}`)
  return `${result.stdout}\n${result.stderr}`
}

function spawnSyncText(command, args, env) {
  // --version and --help are small, read-only capability checks. Keep their
  // output in memory only; neither command is expected to print credentials.
  return spawnSync(command, args, {
    env,
    encoding: 'utf8',
    timeout: 15_000,
    maxBuffer: 2 * 1024 * 1024,
    stdio: ['ignore', 'pipe', 'pipe'],
  })
}

function privateTmpPath(path, label) {
  assert.equal(typeof path, 'string', `${label} must be an absolute path`)
  assert.ok(isAbsolute(path), `${label} must be an absolute path`)
  const fullPath = resolve(path)
  const canonical = canonicalWithMissingSuffix(fullPath)
  const tempRoots = new Set([realpathSync(tmpdir())])
  try {
    tempRoots.add(realpathSync(PRIVATE_TMP_ROOT))
  } catch (error) {
    if (error.code !== 'ENOENT') throw error
  }
  assert.ok([...tempRoots].some(root => canonical.startsWith(`${root}${sep}`)),
    `${label} must be below the system temporary directory or ${PRIVATE_TMP_ROOT}`)
  return fullPath
}

function resolveExecutable(name, configuredPath, env) {
  if (configuredPath) {
    assert.ok(isAbsolute(configuredPath), `${name} override must be an absolute path`)
    const resolvedPath = realpathSync(configuredPath)
    accessSync(resolvedPath, fsConstants.X_OK)
    assert.ok(statSync(resolvedPath).isFile(), `${name} override must be a regular file`)
    return resolvedPath
  }

  const pathValue = env.PATH ?? process.env.PATH ?? ''
  for (const entry of pathValue.split(':').filter(Boolean)) {
    const candidate = join(entry, name)
    try {
      accessSync(candidate, fsConstants.X_OK)
      if (statSync(candidate).isFile()) return realpathSync(candidate)
    } catch {
      // Continue searching PATH.
    }
  }
  throw new Error(`missing ${name}; install it in PATH or set its SAT20_REAL_* absolute path`)
}

function ensureWorkspace(workspaceRoot) {
  assert.ok(isAbsolute(workspaceRoot), 'workspaceRoot must be absolute')
  const root = realpathSync(workspaceRoot)
  const indexerRoot = join(root, 'indexer')
  const configTemplate = join(indexerRoot, INDEXER_CONFIG_RELATIVE_PATH)
  const fixture = join(root, SDK_WALLETS_RELATIVE_PATH)
  assert.ok(existsSync(join(indexerRoot, 'go.mod')), `indexer Go module not found under ${root}`)
  assert.ok(existsSync(configTemplate), `indexer regtest config template not found: ${configTemplate}`)
  assert.ok(existsSync(fixture), `public synthetic regtest wallet fixture not found: ${fixture}`)
  const fixtureData = JSON.parse(readFileSync(fixture, 'utf8'))
  const roles = new Set(Array.isArray(fixtureData) ? fixtureData.map(item => item?.Role ?? item?.role) : [])
  for (const role of ['bootstrap', 'core', 'ordinary-1', 'ordinary-2']) {
    assert.ok(roles.has(role), `public synthetic regtest wallet fixture is missing role ${role}`)
  }
  const ordinaryWallet = fixtureData.find(item => (item?.Role ?? item?.role) === 'ordinary-1')
  assert.match(ordinaryWallet?.address ?? '', /^bcrt1p[0-9a-z]+$/, 'ordinary-1 fixture must provide its public regtest taproot address')
  assert.match(ordinaryWallet?.mining_address ?? '', /^bcrt1p[0-9a-z]+$/, 'ordinary-1 fixture must provide its public regtest account-1 mining address')
  assert.notEqual(ordinaryWallet.mining_address, ordinaryWallet.address, 'initial regtest mining must not fund ordinary-1 account 0')
  return { root, indexerRoot, configTemplate, walletFixture: fixture, initialMiningAddress: ordinaryWallet.mining_address }
}

function isBitcoinCore31(versionOutput) {
  return /Bitcoin Core(?: daemon| RPC client)? version v?31\.0(?:\.0)?(?:\s|$)/m.test(versionOutput)
}

function requireElectrsHTTPCLI(helpText) {
  for (const option of [
    '--network', '--jsonrpc-import', '--daemon-dir', '--daemon-rpc-addr',
    '--db-dir', '--http-addr', '--electrum-rpc-addr', '--monitoring-addr',
  ]) {
    assert.ok(helpText.includes(option), `configured Electrs binary lacks required ${option}; use Blockstream/electrs HTTP build`)
  }
}

export function checkLocalRealDependencies({ env = process.env, workspaceRoot }) {
  const workspace = ensureWorkspace(workspaceRoot)
  const bitcoind = resolveExecutable('bitcoind', env.SAT20_REAL_BITCOIND, env)
  const bitcoinCli = resolveExecutable('bitcoin-cli', env.SAT20_REAL_BITCOIN_CLI, env)
  const electrs = resolveExecutable('electrs', env.SAT20_REAL_ELECTRS, env)
  const go = resolveExecutable('go', env.GO, env)

  const versionDataDir = mkdtempSync(join(tmpdir(), 'sat20-real-regtest-version-'))
  chmodSync(versionDataDir, 0o700)
  let bitcoindVersion
  let bitcoinCliVersion
  try {
    bitcoindVersion = runAsText(bitcoind, [`-datadir=${versionDataDir}`, '-version'], env)
    bitcoinCliVersion = runAsText(bitcoinCli, [`-datadir=${versionDataDir}`, '-version'], env)
  } finally {
    rmSync(versionDataDir, { recursive: true, force: true })
  }
  assert.ok(isBitcoinCore31(bitcoindVersion), 'bitcoind must be Bitcoin Core 31.0')
  assert.ok(isBitcoinCore31(bitcoinCliVersion), 'bitcoin-cli must be Bitcoin Core 31.0')
  const electrsHelp = runAsText(electrs, ['--help'], env)
  requireElectrsHTTPCLI(electrsHelp)

  return {
    workspaceRoot: workspace.root,
    indexerRoot: workspace.indexerRoot,
    walletFixture: workspace.walletFixture,
    initialMiningAddress: workspace.initialMiningAddress,
    bitcoind,
    bitcoinCli,
    electrs,
    go,
    bitcoinCoreVersion: '31.0',
    electrsHTTP: true,
  }
}

function mkdirPrivate(path) {
  const fullPath = privateTmpPath(path, 'private directory')
  mkdirSync(fullPath, { recursive: true, mode: 0o700 })
  const info = lstatSync(fullPath)
  assert.ok(info.isDirectory() && !info.isSymbolicLink(), `expected a real private directory: ${fullPath}`)
  assert.equal(info.mode & 0o077, 0, `directory must be private: ${fullPath}`)
}

function prepareRunDirectory(path) {
  const info = (() => {
    try { return lstatSync(path) } catch (error) { if (error.code === 'ENOENT') return null; throw error }
  })()
  if (info) {
    assert.ok(info.isDirectory() && !info.isSymbolicLink(), 'run directory must be a real directory')
    assert.deepEqual(readdirSync(path), [], 'run directory must be newly created or empty')
    assert.equal(info.mode & 0o077, 0, 'existing run directory must already be private (0700)')
  } else {
    mkdirPrivate(path)
  }
}

function writePrivate(path, contents) {
  const fd = openSync(path, 'wx', 0o600)
  try { writeFileSync(fd, contents) } finally { closeSync(fd) }
  chmodSync(path, 0o600)
}

function writeManifest(path, manifest) {
  const tempPath = `${path}.tmp-${randomBytes(6).toString('hex')}`
  writePrivate(tempPath, `${JSON.stringify(manifest, null, 2)}\n`)
  renameSync(tempPath, path)
  chmodSync(path, 0o600)
}

async function unusedLoopbackPort() {
  const server = createServer()
  await new Promise((resolvePromise, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolvePromise)
  })
  const port = server.address().port
  await new Promise((resolvePromise, reject) => server.close(error => error ? reject(error) : resolvePromise()))
  return port
}

async function requirePortFree(port, label) {
  const server = createServer()
  await new Promise((resolvePromise, reject) => {
    server.once('error', error => reject(new Error(`${label} port ${port} is unavailable (${error.code})`)))
    server.listen(port, '127.0.0.1', () => server.close(error => error ? reject(error) : resolvePromise()))
  })
}

function spawnLogged({ command, args, cwd, env, logPath, onClose }) {
  const logFD = openSync(logPath, 'wx', 0o600)
  let child
  try {
    child = spawn(command, args, { cwd, env, stdio: ['ignore', logFD, logFD] })
  } catch (error) {
    closeSync(logFD)
    throw error
  }
  closeSync(logFD)
  const entry = {
    command: [command, ...args], cwd, logPath, pid: child.pid,
    startedAt: new Date().toISOString(), endedAt: null, exitCode: null, signal: null,
  }
  child.once('close', (code, signal) => {
    entry.endedAt = new Date().toISOString()
    entry.exitCode = code
    entry.signal = signal
    onClose?.(entry)
  })
  return { child, entry }
}

function runLogged({ command, args, cwd, env, logPath }) {
  return new Promise((resolvePromise, reject) => {
    const { child, entry } = spawnLogged({ command, args, cwd, env, logPath })
    child.once('error', error => reject(new Error(`command could not start; complete log: ${logPath}; ${error.message}`)))
    child.once('close', code => {
      entry.exitCode = code
      entry.endedAt = new Date().toISOString()
      if (code !== 0) reject(new Error(`command exited ${code}; complete log: ${logPath}`))
      else resolvePromise(entry)
    })
  })
}

async function stopOwnedProcess(processEntry, graceMs = 20_000) {
  const { child, entry } = processEntry
  if (child.exitCode !== null || child.signalCode !== null) return
  const wait = milliseconds => new Promise(resolvePromise => {
    let settled = false
    const timer = setTimeout(() => {
      if (settled) return
      settled = true
      child.off('close', onClose)
      resolvePromise(false)
    }, milliseconds)
    const onClose = () => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      resolvePromise(true)
    }
    child.once('close', onClose)
  })
  try { child.kill('SIGINT') } catch (error) { if (error.code !== 'ESRCH') throw error }
  if (await wait(graceMs)) return
  try { child.kill('SIGTERM') } catch (error) { if (error.code !== 'ESRCH') throw error }
  if (await wait(5000)) return
  try { child.kill('SIGKILL') } catch (error) { if (error.code !== 'ESRCH') throw error }
  if (!await wait(2000)) throw new Error(`owned child ${entry.pid} did not exit; log: ${entry.logPath}`)
}

function readCookie(cookiePath) {
  const value = readFileSync(cookiePath, 'utf8').trim()
  const separator = value.indexOf(':')
  assert.ok(separator > 0 && separator < value.length - 1, 'Bitcoin Core RPC cookie is malformed')
  return { username: value.slice(0, separator), password: value.slice(separator + 1) }
}

function quoteYaml(value) {
  return JSON.stringify(String(value))
}

function renderIndexerConfig({ dataDir, logDir, host, rpcPort, user, password, apiPort }) {
  return `chain: regtest\n` +
    `db:\n  path: ${quoteYaml(join(dataDir, 'db'))}\n` +
    `  badger_block_cache_total_mb: 64\n  badger_index_cache_total_mb: 16\n` +
    `  badger_num_compactors: 2\n  badger_flatten_workers: 1\n` +
    `share_rpc:\n  bitcoin:\n    host: ${quoteYaml(host)}\n    port: ${rpcPort}\n` +
    `    user: ${quoteYaml(user)}\n    password: ${quoteYaml(password)}\n` +
    `log:\n  level: info\n  path: ${quoteYaml(logDir)}\n` +
    `basic_index:\n  max_index_height: 0\n  period_flush_to_db: 12\n` +
    `rpc_service:\n  addr: ${quoteYaml(`${host}:${apiPort}`)}\n` +
    `  proxy: btc/regtest\n  log_path: ${quoteYaml(logDir)}\n` +
    `  swagger:\n    host: ${quoteYaml(host)}\n    schemes:\n      - http\n` +
    `  api:\n    nolimit_api_list:\n      - /health\n`
}

async function waitFor(label, timeoutMs, check, logPath) {
  const deadline = performance.now() + timeoutMs
  let lastError
  while (performance.now() < deadline) {
    try {
      const value = await check()
      if (value) return value
      lastError = undefined
    } catch (error) { lastError = error }
    await new Promise(resolvePromise => setTimeout(resolvePromise, 250))
  }
  const reason = lastError ? ` (${lastError.message})` : ''
  throw new Error(`${label} did not become ready within ${timeoutMs}ms${reason}; complete log: ${logPath}`)
}

function bitcoinCLI(dependencies, dataDir, rpcPort, args, timeout = 15_000) {
  const result = spawnSync(dependencies.bitcoinCli, [
    `-datadir=${dataDir}`, '-regtest', `-rpcport=${rpcPort}`, ...args,
  ], { encoding: 'utf8', timeout, stdio: ['ignore', 'pipe', 'pipe'] })
  assert.equal(result.error, undefined, `bitcoin-cli ${args[0]} could not start`)
  assert.equal(result.status, 0, `bitcoin-cli ${args[0]} failed (see private run logs for the owned Core process)`)
  return result.stdout.trim()
}

function parseRPCJSON(text, label) {
  try { return JSON.parse(text) } catch { throw new Error(`Bitcoin RPC ${label} returned invalid JSON`) }
}

async function waitCore(dependencies, dataDir, rpcPort, logPath) {
  const blockchainInfo = await waitFor('Bitcoin Core RPC', CORE_READY_TIMEOUT_MS, () => {
    try {
      const info = parseRPCJSON(bitcoinCLI(dependencies, dataDir, rpcPort, ['getblockchaininfo']), 'getblockchaininfo')
      return info.chain === 'regtest' ? info : false
    } catch { return false }
  }, logPath)
  assert.equal(blockchainInfo.blocks, 0, 'fresh local Bitcoin Core regtest must start at height zero')
  const version = parseRPCJSON(bitcoinCLI(dependencies, dataDir, rpcPort, ['getnetworkinfo']), 'getnetworkinfo').version
  assert.equal(version, 310000, 'local Bitcoin Core must be version 31.0')
  const peers = parseRPCJSON(bitcoinCLI(dependencies, dataDir, rpcPort, ['getpeerinfo']), 'getpeerinfo')
  assert.ok(Array.isArray(peers) && peers.every(peer => isLoopbackPeerAddr(peer.addr)),
    'local Bitcoin Core may only have loopback peers')
  return blockchainInfo
}

async function prepareCoreInitialSync(dependencies, dataDir, rpcPort, logPath, manifest, persist) {
  const initial = await waitCore(dependencies, dataDir, rpcPort, logPath)
  manifest.coreInitialState = {
    chain: initial.chain,
    blocks: initial.blocks,
    headers: initial.headers,
    initialBlockDownload: initial.initialblockdownload,
  }
  persist()

  if (!initial.initialblockdownload) return

  const hashes = parseRPCJSON(
    bitcoinCLI(dependencies, dataDir, rpcPort, ['generatetoaddress', '1', dependencies.initialMiningAddress]),
    'generatetoaddress',
  )
  assert.ok(Array.isArray(hashes) && hashes.length === 1 && /^[0-9a-f]{64}$/.test(hashes[0]),
    'fresh Core IBD preparation must mine exactly one real block to the fixed ordinary test address')
  const synced = await waitFor('Bitcoin Core fresh regtest initial sync', CORE_READY_TIMEOUT_MS, () => {
    try {
      const info = parseRPCJSON(bitcoinCLI(dependencies, dataDir, rpcPort, ['getblockchaininfo']), 'getblockchaininfo')
      return info.chain === 'regtest' && info.blocks === 1 && info.headers === 1 && !info.initialblockdownload ? info : false
    } catch { return false }
  }, logPath)
  manifest.initialMinedBlock = { height: synced.blocks, hash: hashes[0] }
  manifest.coreReadyState = {
    chain: synced.chain,
    blocks: synced.blocks,
    headers: synced.headers,
    initialBlockDownload: synced.initialblockdownload,
  }
  persist()
}

function isLoopbackPeerAddr(value) {
  if (typeof value !== 'string') return false
  const match = value.match(/^\[([0-9a-fA-F:]+)]:(\d+)$/) ?? value.match(/^([0-9.]+):(\d+)$/)
  if (!match) return false
  const host = match[1]
  if (host.includes(':')) return host.toLowerCase() === '::1'
  return Number(host.split('.')[0]) === 127
}

async function waitIndexer(url, logPath) {
  await waitFor('real L1 indexer health', INDEXER_READY_TIMEOUT_MS, async () => {
    const response = await fetch(`${url}/health`, { signal: AbortSignal.timeout(2000) })
    if (response.status !== 200) return false
    const body = await response.json()
    return body.status === 'ok'
  }, logPath)
}

async function waitElectrs(url, coreGenesisHash, logPath) {
  await waitFor('local Electrs Esplora API', ELECTRS_READY_TIMEOUT_MS, async () => {
    const response = await fetch(`${url}/blocks/tip/hash`, { signal: AbortSignal.timeout(3000) })
    if (!response.ok) return false
    return (await response.text()).trim() === coreGenesisHash
  }, logPath)
}

function safeEnv(baseEnv, overrides) {
  const result = { ...baseEnv }
  for (const key of [
    'SAT20_REAL_SATOSHINET_ATTACH', 'SAT20_REAL_PWA_EXISTING_URL',
    'SAT20_REAL_RECOVERY_FIXTURES', 'SAT20_REAL_SDK_WALLET_DIR',
  ]) delete result[key]
  return { ...result, ...overrides }
}

export function discardLocalRealRegtestData({ runDirectory, dataDirectories, walletFixture, processes, closed, manifest, persist }) {
  assert.ok(closed, 'close must complete before discarding local regtest data')
  assert.ok(processes.every(({ child }) => child.exitCode !== null || child.signalCode !== null),
    'all owned local regtest children must exit before discarding data')
  if (manifest.dataRemovedAt) return false

  const canonicalRunDir = realpathSync(runDirectory)
  for (const path of dataDirectories) {
    const info = lstatSync(path)
    assert.ok(info.isDirectory() && !info.isSymbolicLink(), `refusing to discard non-owned data path: ${path}`)
    assert.ok(realpathSync(path).startsWith(`${canonicalRunDir}${sep}`),
      `refusing to discard data outside this run directory: ${path}`)
  }
  const walletInfo = lstatSync(walletFixture)
  assert.ok(walletInfo.isFile() && !walletInfo.isSymbolicLink(),
    `refusing to discard non-owned wallet fixture: ${walletFixture}`)
  assert.ok(realpathSync(walletFixture).startsWith(`${canonicalRunDir}${sep}`),
    'refusing to discard a wallet fixture outside this run directory')

  // Node log directories and browser output live alongside their databases.
  // Keep the diagnostic streams before removing that data; numeric LevelDB
  // *.log files are database journals and must not be retained as test logs.
  const preserveLogs = directory => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const source = join(directory, entry.name)
      if (entry.isDirectory()) {
        preserveLogs(source)
      } else if (entry.isFile()) {
        const parts = relative(runDirectory, source).split(sep)
        if (!parts.includes('logs') && entry.name !== 'btcd.stdout.log' && entry.name !== 'debug.log' &&
          !(parts.includes('pwa-browser-evidence') && entry.name.endsWith('.jsonl'))) continue
        const target = join(runDirectory, 'logs', 'retained', ...parts)
        mkdirPrivate(dirname(target))
        copyFileSync(source, target, fsConstants.COPYFILE_EXCL)
        chmodSync(target, 0o600)
      }
    }
  }
  for (const path of dataDirectories) preserveLogs(path)
  for (const path of dataDirectories) rmSync(path, { recursive: true })
  rmSync(walletFixture)
  manifest.dataRemovedAt = new Date().toISOString()
  persist()
  return true
}

export async function startLocalRealRegtest({ directory, workspaceRoot, env = process.env, artifactsDirectory }) {
  const runDir = privateTmpPath(directory, 'directory')
  prepareRunDirectory(runDir)
  const artifactsDir = privateTmpPath(artifactsDirectory ?? join(runDir, 'artifacts'), 'artifactsDirectory')
  const dependencies = checkLocalRealDependencies({ env, workspaceRoot })
  mkdirPrivate(artifactsDir)

  const coreDir = join(runDir, 'bitcoin')
  const indexerDataDir = join(runDir, 'indexer')
  const electrsDataDir = join(runDir, 'electrs')
  const runtimeDir = join(runDir, 'runtime')
  const logsDir = join(runDir, 'logs')
  for (const path of [coreDir, indexerDataDir, electrsDataDir, runtimeDir, logsDir]) mkdirPrivate(path)

  const copiedWalletFixture = join(runDir, 'wallets.json')
  copyFileSync(dependencies.walletFixture, copiedWalletFixture, fsConstants.COPYFILE_EXCL)
  chmodSync(copiedWalletFixture, 0o600)

  await requirePortFree(CORE_P2P_PORT, 'Bitcoin Core regtest P2P')
  const rpcPort = await unusedLoopbackPort()
  const apiPort = await unusedLoopbackPort()
  const electrsHTTPPort = await unusedLoopbackPort()
  const electrsElectrumPort = await unusedLoopbackPort()
  const electrsMonitoringPort = await unusedLoopbackPort()
  const coreCookiePath = join(coreDir, 'regtest', '.cookie')
  const coreConfigPath = join(coreDir, 'bitcoin.conf')
  const indexerConfigPath = join(indexerDataDir, 'indexer.yaml')
  const indexerLogDir = join(logsDir, 'indexer')
  mkdirPrivate(join(indexerDataDir, 'db'))
  mkdirPrivate(indexerLogDir)

  const coreConfig = [
    'server=1',
    '[regtest]',
    'rest=1', 'txindex=1', 'prune=0',
    'dnsseed=0', 'discover=0', 'upnp=0', 'natpmp=0', 'listen=1',
    `bind=127.0.0.1:${CORE_P2P_PORT}`, `port=${CORE_P2P_PORT}`,
    'rpcbind=127.0.0.1', 'rpcallowip=127.0.0.1', `rpcport=${rpcPort}`,
    'fallbackfee=0.0001', 'daemon=0', 'printtoconsole=1',
  ].join('\n') + '\n'
  writePrivate(coreConfigPath, coreConfig)

  const manifestPath = join(runDir, 'manifest.json')
  const manifest = {
    version: 1,
    mode: 'local-real-regtest',
    runDirectory: runDir,
    workspaceRoot: dependencies.workspaceRoot,
    createdAt: new Date().toISOString(),
    network: 'regtest',
    remoteServices: false,
    fakeL1: false,
    artifactsDirectory: artifactsDir,
    indexerBinary: join(artifactsDir, 'sat20-l1-indexer'),
    binaries: {
      bitcoind: dependencies.bitcoind,
      bitcoinCli: dependencies.bitcoinCli,
      electrs: dependencies.electrs,
      bitcoinCoreVersion: dependencies.bitcoinCoreVersion,
    },
    ports: {
      bitcoinRPC: rpcPort,
      bitcoinP2P: CORE_P2P_PORT,
      indexerHTTP: apiPort,
      electrsHTTP: electrsHTTPPort,
      electrsElectrum: electrsElectrumPort,
      electrsMonitoring: electrsMonitoringPort,
    },
    paths: {
      bitcoinData: coreDir,
      indexerData: indexerDataDir,
      electrsData: electrsDataDir,
      runtime: runtimeDir,
      walletFixture: copiedWalletFixture,
      coreConfig: coreConfigPath,
      indexerConfig: indexerConfigPath,
      coreCookie: coreCookiePath,
    },
    endpoints: {},
    processes: [],
    build: null,
    coreInitialState: null,
    initialMinedBlock: null,
    coreReadyState: null,
    closedAt: null,
    dataRemovedAt: null,
  }
  writeManifest(manifestPath, manifest)
  const processes = []
  let closed = false
  const persist = () => writeManifest(manifestPath, manifest)
  const close = async () => {
    if (closed) return
    closed = true
    const stopErrors = []
    for (const processEntry of [...processes].reverse()) {
      try { await stopOwnedProcess(processEntry) } catch (error) { stopErrors.push(error) }
    }
    manifest.closedAt = new Date().toISOString()
    persist()
    if (stopErrors.length) throw new AggregateError(stopErrors, 'one or more local regtest children did not stop cleanly')
  }
  const discardData = async () => {
    return discardLocalRealRegtestData({
      runDirectory: runDir,
      dataDirectories: [coreDir, indexerDataDir, electrsDataDir, runtimeDir],
      walletFixture: copiedWalletFixture,
      processes,
      closed,
      manifest,
      persist,
    })
  }

  const startProcess = ({ command, args, cwd, env: childEnv, logName }) => {
    const logPath = join(logsDir, logName)
    const processEntry = spawnLogged({ command, args, cwd, env: childEnv, logPath, onClose: persist })
    processes.push(processEntry)
    manifest.processes.push(processEntry.entry)
    persist()
    processEntry.child.once('error', error => {
      processEntry.entry.spawnError = error.message
      persist()
    })
    return processEntry
  }

  try {
    const indexerBinary = manifest.indexerBinary
    if (!existsSync(indexerBinary)) {
      const partialBinary = `${indexerBinary}.building-${randomBytes(6).toString('hex')}`
      const buildEnv = { ...env }
      delete buildEnv.GOFLAGS
      const build = await runLogged({
        command: dependencies.go,
        args: ['build', '-mod=readonly', '-o', partialBinary, '.'],
        cwd: dependencies.indexerRoot,
        env: buildEnv,
        logPath: join(logsDir, 'indexer-build.log'),
      })
      chmodSync(partialBinary, 0o700)
      renameSync(partialBinary, indexerBinary)
      manifest.build = { ...build, reused: false }
    } else {
      accessSync(indexerBinary, fsConstants.X_OK)
      manifest.build = { command: null, reused: true }
    }
    persist()

    const core = startProcess({
      command: dependencies.bitcoind,
      args: [`-datadir=${coreDir}`, `-conf=${coreConfigPath}`, '-regtest', '-daemon=0', '-printtoconsole=1'],
      cwd: coreDir,
      env: safeEnv(env, { SAT20_REAL_BITCOIN_DATADIR: coreDir }),
      logName: 'bitcoind.log',
    })
    await prepareCoreInitialSync(dependencies, coreDir, rpcPort, core.entry.logPath, manifest, persist)
    assert.equal(core.child.exitCode, null, 'owned Bitcoin Core exited before readiness')

    const cookie = readCookie(coreCookiePath)
    const indexerConfig = renderIndexerConfig({
      dataDir: indexerDataDir,
      logDir: indexerLogDir,
      host: '127.0.0.1',
      rpcPort,
      user: cookie.username,
      password: cookie.password,
      apiPort,
    })
    writePrivate(indexerConfigPath, indexerConfig)

    const indexer = startProcess({
      command: indexerBinary,
      args: ['-env', indexerConfigPath],
      cwd: dependencies.indexerRoot,
      env: safeEnv(env, { SAT20_REAL_BITCOIN_DATADIR: coreDir }),
      logName: 'indexer-process.log',
    })
    const indexerURL = `http://127.0.0.1:${apiPort}/btc/regtest`
    await waitIndexer(indexerURL, indexer.entry.logPath)
    assert.equal(indexer.child.exitCode, null, 'owned L1 indexer exited before readiness')

    const coreTipHash = bitcoinCLI(dependencies, coreDir, rpcPort, ['getbestblockhash'])
    const electrs = startProcess({
      command: dependencies.electrs,
      args: [
        '--network', 'regtest', '--jsonrpc-import', '--daemon-dir', coreDir,
        '--daemon-rpc-addr', `127.0.0.1:${rpcPort}`,
        '--db-dir', electrsDataDir,
        '--http-addr', `127.0.0.1:${electrsHTTPPort}`,
        '--electrum-rpc-addr', `127.0.0.1:${electrsElectrumPort}`,
        '--monitoring-addr', `127.0.0.1:${electrsMonitoringPort}`,
      ],
      cwd: runDir,
      env: safeEnv(env, { SAT20_REAL_BITCOIN_DATADIR: coreDir }),
      logName: 'electrs.log',
    })
    const electrsURL = `http://127.0.0.1:${electrsHTTPPort}`
    await waitElectrs(electrsURL, coreTipHash, electrs.entry.logPath)
    assert.equal(electrs.child.exitCode, null, 'owned Electrs exited before readiness')

    manifest.endpoints = { coreRPC: `127.0.0.1:${rpcPort}`, indexer: indexerURL, esplora: electrsURL }
    manifest.readyAt = new Date().toISOString()
    persist()

    const ownedEnv = safeEnv(env, {
      SAT20_REAL_BITCOIND: dependencies.bitcoind,
      SAT20_REAL_BITCOIN_CLI: dependencies.bitcoinCli,
      SAT20_REAL_ELECTRS: dependencies.electrs,
      SAT20_REAL_BITCOIN_DATADIR: coreDir,
      SAT20_REAL_BITCOIN_RPC_PORT: String(rpcPort),
      SAT20_REAL_TEST_WALLETS: copiedWalletFixture,
      SAT20_REAL_L1_INDEXER: indexerURL,
      SAT20_REAL_ESPLORA: electrsURL,
      SAT20_REAL_REGTEST_RUNTIME: runtimeDir,
    })
    return { env: ownedEnv, close, discardData, manifest }
  } catch (error) {
    try { await close() } catch (closeError) {
      error.message += `; cleanup failed: ${closeError.message}`
    }
    throw error
  }
}
