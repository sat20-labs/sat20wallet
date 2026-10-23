#!/usr/bin/env node
import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { chmodSync, closeSync, existsSync, mkdtempSync, openSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { createServer, isIP } from 'node:net'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { performance } from 'node:perf_hooks'

const scriptDir = dirname(fileURLToPath(import.meta.url))
export const pwaRoot = resolve(scriptDir, '../..')
export const workspaceRoot = resolve(pwaRoot, '../..')

// The normal path excludes two one-shot historical flows: PrivateBTCRoundTrip
// relies on a previously created outpoint, and ChannelActivation relies on a
// reviewed old activation height. Diagnostic/continuation/readback cases stay opt-in.
const pwaCanonicalTests = [
  'TestSDKPWARealRegtestWalletBasicAndBiometricBrowser',
  'TestSDKPWARealRegtestRGBBrowser',
  'TestSDKPWARealRegtestBRC20ExistingTransferSendBrowser',
  'TestSDKPWARealRegtestRunesDecimalSendBrowser',
  'TestSDKPWARealRegtestRunesPremineReturnBrowser',
  'TestSDKPWARealRegtestRunesOpenMintSendBrowser',
  'TestSDKPWARealRegtestRunesOpenMintReturnBrowser',
  'TestSDKPWARealRegtestBitcoinFundsSendBrowser',
  'TestSDKPWARealRegtestSatoshiNetPlainSendBrowser',
  'TestSDKPWARealRegtestChannelOpeningOrReadyBrowser',
  'TestSDKPWARealRegtestChannelSplicingBrowser',
  'TestSDKPWARealRegtestChannelSafetyBrowser',
  'TestSDKPWARealRegtestPrivateBTCSplicingOutBrowser',
  'TestSDKPWARealRegtestChannelV2AssetSplicingBrowser',
  'TestSDKPWARealRegtestBRC20BalanceSendBrowser',
  'TestSDKPWARealRegtestMintBrowser',
  'TestSDKPWARealRegtestMiningBrowser',
  'TestSDKPWARealRegtestDappPermissionsBrowser',
]

const fixedAccountUsageTests = ['TestSDKPWARealRegtestFixedAccountManagementBrowser']
const sdkRequiredEnv = [
  'SAT20_REAL_BITCOIN_CLI',
  'SAT20_REAL_BITCOIN_DATADIR',
  'SAT20_REAL_BITCOIN_RPC_PORT',
  'SAT20_REAL_TEST_WALLETS',
  'SAT20_REAL_L1_INDEXER',
  'SAT20_REAL_ESPLORA',
  'SAT20_REAL_REGTEST_RUNTIME',
]
const silentPayTests = [
  'TestSilentPayE2EReceiveLiveRecoverAndSpend',
  'TestSilentPayE2EReorgReloadRestoresLive',
  'TestSilentPayE2ECheckSelfDetectsCorruptionWithoutRepair',
  'TestSilentPayE2EDatabaseReopen',
  'TestSilentPayE2ESameBlockSpendAndFullySpentHistoryAudit',
  'TestSilentPayE2EBaseBufferedCloneSubtractCommit',
]
const posSafetyTests = [
  'TestPOSMinerStatusRepeatedStartStopAndRestart',
  'TestPOSMinerQueryConcurrentWithStop',
  'TestGenerateNBlocksRejectsPOSV2WithoutApproval',
  'TestPOSMinerCandidateDuringDiscreteGeneration',
  'TestPOSMinerCandidateConcurrentWithStopAndRestart',
  'TestPOSMinerShutdownWaitsForScheduledWork',
]
const networkContractTests = [
  'TestNetworkTemplateLimitOrderContract',
  'TestNetworkTemplateDefaultInvokeLimitOrderBuy',
  'TestNetworkTemplateExchangeDefaultFundBuyAndClose',
  'TestNetworkTemplateLimitOrderLargeBuyFilledBySmallSells',
  'TestNetworkTemplateLimitOrderLargeSellFilledBySmallBuys',
  'TestNetworkTemplateLimitOrderBuyTakesLowerPricedSells',
  'TestNetworkTemplateLimitOrderRefundOpenOrders',
  'TestNetworkTemplateLimitOrderRefundPartiallyFilledSell',
  'TestNetworkTemplateAMMContract',
  'TestNetworkTemplateAMMWaitsUntilAddLiquidityMeetsK',
  'TestNetworkTemplateAMMRejectsBuySlippage',
  'TestNetworkTemplateAMMSellAddsAssetToPool',
  'TestNetworkTemplateAMMAddRemoveLiquidity',
  'TestNetworkTemplateAutopayCloseAcrossOutputLimit',
  'TestNetworkTemplateAutopaySatoshiResult',
  'TestNetworkTemplatePartialBindingResultFragments',
  'TestNetworkTemplateLifecycleMatrix',
  'TestNetworkTemplateLimitOrderScenarioMatrix',
  'TestNetworkTemplateAMMLiquidityScenarioMatrix',
  'TestNetworkTemplateExchangeScenarioMatrix',
  'TestNetworkTemplateAutopayScenarioMatrix',
  'TestNetworkTemplateAMMDefaultSwapAndCloseOwners',
  'TestNetworkTemplateDeploymentRejectsInvalidContent',
  'TestNetworkTemplateRefundRejectsNonNumericOpcode',
  'TestNetworkLimitRefundBuy',
  'TestNetworkAMMSameBlockSequentialPricing',
]

const exactSelector = names => `^(${names.join('|')})$`
const goFlags = ['-mod=readonly', '-p=1', '-parallel=1', '-failfast', '-json', '-count=1']

function goStage(id, repo, packageName, tests, timeout) {
  return {
    id, repo, cwd: resolve(workspaceRoot, repo), packageName, tests, timeout,
    timeoutMs: Number.parseInt(timeout, 10) * (timeout.endsWith('m') ? 60_000 : 1000),
    requires: repo === 'sat20wallet/sdk' ? sdkRequiredEnv : [],
  }
}

export function buildCommand(stage, env = process.env) {
  const selector = stage.id === 'transcend-stp' ? '^TestRunAll$' : exactSelector(stage.tests)
  const args = ['test', ...goFlags, `-timeout=${stage.timeout}`, '-run', selector]
  if (stage.id === 'transcend-stp') {
    args.push('-tags=stp_aggregate')
    const modfile = env.SAT20_TRANSCEND_E2E_MODFILE
    if (modfile) {
      assert.equal(resolve(modfile), modfile, 'SAT20_TRANSCEND_E2E_MODFILE must be an absolute path')
      assert.ok(modfile.endsWith('.mod'), 'SAT20_TRANSCEND_E2E_MODFILE must end in .mod')
      assert.ok(existsSync(modfile), 'configured private Transcend modfile does not exist')
      const sumfile = `${modfile.slice(0, -4)}.sum`
      assert.ok(existsSync(sumfile), 'configured private Transcend modfile is missing its matching .sum file')
      args.push(`-modfile=${modfile}`)
    }
  }
  args.push(stage.packageName)
  return [env.GO || 'go', ...args]
}

export const stageCatalog = [
  goStage('indexer-silentpay', 'indexer', './indexer/base', silentPayTests, '45m'),
  goStage('satoshinet-pos-safety', 'satoshinet', './mining/posminer', posSafetyTests, '45m'),
  goStage('satoshinet-contract-e2e', 'satoshinet', './integration/contract_e2e', networkContractTests, '45m'),
  goStage('sdk-assets', 'sat20wallet/sdk', './e2e', ['TestSDKPWARealRegtestL1AssetPreparation'], '60m'),
  goStage('sdk-node-readiness', 'sat20wallet/sdk', './e2e', ['TestSDKPWARealRegtestNodeReadiness'], '90m'),
  goStage('sdk-rgb-closed-loop', 'sat20wallet/sdk', './e2e', ['TestSDKPWARealRegtestRGB11StandardRecoveryDirectReturn'], '90m'),
  goStage('pwa-canonical', 'sat20wallet/sdk', './e2e', pwaCanonicalTests, '180m'),
  goStage('pwa-fixed-account-usage', 'sat20wallet/sdk', './e2e', fixedAccountUsageTests, '90m'),
  goStage('transcend-stp', 'transcend', './stp', ['TestRunAll'], '45m'),
]

export function parsePort(value, fallback = 5189) {
  const text = value === undefined || value === null || value === '' ? String(fallback) : String(value)
  assert.match(text, /^\d+$/, 'PWA E2E port must be an integer')
  const port = Number(text)
  assert.ok(Number.isSafeInteger(port) && port >= 1 && port <= 65535, 'PWA E2E port must be from 1 to 65535')
  return port
}

export function selectStages(ids, catalog = stageCatalog) {
  if (ids === null) return [...catalog]
  assert.ok(ids.length > 0, '--only needs at least one exact stage ID')
  assert.equal(new Set(ids).size, ids.length, '--only cannot repeat a stage ID')
  const byId = new Map(catalog.map(stage => [stage.id, stage]))
  for (const id of ids) assert.ok(byId.has(id), `unknown stage ID: ${id}`)
  return catalog.filter(stage => ids.includes(stage.id))
}

export function configureStages(stages, env) {
  return stages.map(stage => stage.id === 'sdk-node-readiness' && env.SAT20_REAL_SATOSHINET_ATTACH ? {
    ...stage, tests: ['TestSDKPWARealRegtestAttachedNodeReadback'],
    boundary: 'attached existing-node readback; initial anchor creation and same-DB restart not executed',
  } : stage)
}

export function selectTests(stages, names) {
  if (names === null || names === undefined) return stages
  assert.ok(names.length > 0, '--tests needs at least one exact test name')
  assert.equal(new Set(names).size, names.length, '--tests cannot repeat a test name')
  const known = new Set(stages.flatMap(stage => stage.tests))
  for (const name of names) assert.ok(known.has(name), `unknown test in selected stages: ${name}`)
  return stages.map(stage => ({ ...stage, tests: stage.tests.filter(name => names.includes(name)) }))
    .filter(stage => stage.tests.length > 0)
}

export function parseGoJSON(text) {
  const events = []
  for (const line of text.split(/\r?\n/)) {
    if (!line) continue
    try {
      const item = JSON.parse(line)
      if (item && typeof item === 'object') events.push(item)
    } catch {
      // go test -json should be machine-readable; preserve malformed lines in
      // the private log and let expected-leaf validation fail closed.
    }
  }
  return events
}

export function summarizeGoJSON(text, expectedTests) {
  const events = parseGoJSON(text)
  const matching = events.filter(event => typeof event.Test === 'string' &&
    expectedTests.some(name => event.Test === name || event.Test.startsWith(`${name}/`)))
  const skipped = [...new Set(matching.filter(event => event.Action === 'skip').map(event => event.Test))]
  const failed = [...new Set(matching.filter(event => event.Action === 'fail').map(event => event.Test))]
  const passed = expectedTests.filter(name => matching.some(event => event.Test === name && event.Action === 'pass'))
  const missing = expectedTests.filter(name => !passed.includes(name))
  return { expected: expectedTests.length, passed, failed, skipped, missing, testEventCount: matching.length }
}

export function parseArguments(argv) {
  const options = { mode: 'run', only: null, tests: null, envFile: null }
  const flags = new Set()
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--help' || arg === '-h') { options.mode = 'help'; continue }
    if (arg === '--plan' || arg === '--check') {
      assert.equal(options.mode, 'run', `${arg} cannot be combined with another mode`)
      options.mode = arg.slice(2)
      continue
    }
    if (arg === '--only' || arg === '--tests') {
      assert.ok(!flags.has(arg), `${arg} may be provided once`)
      flags.add(arg)
      const values = []
      while (argv[i + 1] && !argv[i + 1].startsWith('--')) values.push(...argv[++i].split(',').filter(Boolean))
      options[arg === '--only' ? 'only' : 'tests'] = values
      continue
    }
    if (arg === '--env-file') {
      assert.ok(!flags.has(arg), '--env-file may be provided once')
      flags.add(arg)
      assert.ok(argv[i + 1] && !argv[i + 1].startsWith('--'), '--env-file requires a path')
      options.envFile = argv[++i]
      continue
    }
    throw new Error(`unknown option: ${arg}`)
  }
  if (options.mode === 'help') return options
  options.stages = selectStages(options.only)
  return options
}

function readEnvFile(path) {
  if (!path) return {}
  const fullPath = resolve(path)
  const mode = statSync(fullPath).mode & 0o777
  assert.equal(mode & 0o077, 0, 'environment file must be private (0600 or stricter)')
  const values = {}
  for (const [lineNumber, rawLine] of readFileSync(fullPath, 'utf8').split(/\r?\n/).entries()) {
    const line = rawLine.trim()
    if (!line || line.startsWith('#')) continue
    const match = line.match(/^([A-Z][A-Z0-9_]*)=(.*)$/)
    assert.ok(match, `invalid env-file line ${lineNumber + 1}`)
    const [, key] = match
    assert.ok(/^(?:SAT20_REAL_|SATOSHINET_|SAT20_PWA_E2E_PORT$|SAT20_TRANSCEND_E2E_MODFILE$|GOFLAGS$|GOCACHE$|GOMODCACHE$|GOTOOLCHAIN$|GO$)/.test(key),
      `unsupported env-file key ${key}`)
    let value = match[2]
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) value = value.slice(1, -1)
    values[key] = value
  }
  return values
}

function effectiveEnv(envFile) {
  return { ...readEnvFile(envFile), ...process.env }
}

async function assertPortFree(port, label) {
  await new Promise((resolvePromise, reject) => {
    const server = createServer()
    server.once('error', error => reject(new Error(`${label} port ${port} is unavailable: ${error.code}`)))
    server.listen(port, '127.0.0.1', () => server.close(error => error ? reject(error) : resolvePromise()))
  })
}

function parseLoopbackURL(value, label, expectedPath) {
  const url = new URL(value)
  assert.equal(url.protocol, 'http:', `${label} must use HTTP on loopback`)
  assert.equal(url.hostname, '127.0.0.1', `${label} must use 127.0.0.1`)
  assert.ok(url.port, `${label} must specify a forwarded/local port`)
  assert.equal(url.username, '', `${label} cannot contain credentials`)
  assert.equal(url.password, '', `${label} cannot contain credentials`)
  if (expectedPath) assert.equal(url.pathname.replace(/\/+$/, ''), expectedPath)
  return url
}

export function isLoopbackPeerAddr(value) {
  if (typeof value !== 'string') return false
  let host
  if (value.startsWith('[')) {
    const end = value.indexOf(']')
    if (end < 0 || !/^:\d+$/.test(value.slice(end + 1))) return false
    host = value.slice(1, end)
  } else {
    const separator = value.lastIndexOf(':')
    if (separator <= 0 || !/^\d+$/.test(value.slice(separator + 1))) return false
    host = value.slice(0, separator)
  }
  const version = isIP(host)
  if (version === 4) return Number(host.split('.')[0]) === 127
  return version === 6 && host.toLowerCase() === '::1'
}

function checkedBitcoinJSON(env, method) {
  const result = spawnSync(env.SAT20_REAL_BITCOIN_CLI, [
    `-datadir=${env.SAT20_REAL_BITCOIN_DATADIR}`, '-regtest',
    `-rpcport=${env.SAT20_REAL_BITCOIN_RPC_PORT}`, method,
  ], { encoding: 'utf8', timeout: 15000, stdio: ['ignore', 'pipe', 'pipe'] })
  assert.equal(result.error, undefined, `${method} could not start`)
  assert.equal(result.status, 0, `${method} failed`)
  return JSON.parse(result.stdout)
}

async function checkSDKEnvironment(env, port) {
  const missing = sdkRequiredEnv.filter(key => !env[key])
  assert.deepEqual(missing, [], `missing required environment variables: ${missing.join(', ')}`)
  for (const key of ['SAT20_REAL_BITCOIN_CLI', 'SAT20_REAL_BITCOIN_DATADIR', 'SAT20_REAL_TEST_WALLETS', 'SAT20_REAL_REGTEST_RUNTIME']) {
    assert.ok(resolve(env[key]) === env[key], `${key} must be an absolute path`)
  }
  assert.ok(existsSync(env.SAT20_REAL_BITCOIN_CLI), 'configured bitcoin-cli does not exist')
  if (!env.SAT20_REAL_SATOSHINET_ATTACH) {
    assert.ok(existsSync(env.SAT20_REAL_BITCOIN_DATADIR), 'configured Bitcoin regtest datadir does not exist')
  }
  assert.ok(existsSync(env.SAT20_REAL_TEST_WALLETS), 'private fixed-wallet fixture does not exist')
  const fixtureMode = statSync(env.SAT20_REAL_TEST_WALLETS).mode & 0o777
  assert.equal(fixtureMode & 0o077, 0, 'fixed-wallet fixture must be private (0600 or stricter)')
  const wallets = JSON.parse(readFileSync(env.SAT20_REAL_TEST_WALLETS, 'utf8'))
  const roles = new Set(Array.isArray(wallets) ? wallets.map(wallet => wallet.Role ?? wallet.role) : [])
  for (const role of ['bootstrap', 'core', 'ordinary-1', 'ordinary-2']) assert.ok(roles.has(role), `fixed-wallet fixture is missing ${role}`)
  assert.ok(Number.isInteger(Number(env.SAT20_REAL_BITCOIN_RPC_PORT)) && Number(env.SAT20_REAL_BITCOIN_RPC_PORT) > 0,
    'SAT20_REAL_BITCOIN_RPC_PORT must be a positive integer')
  assert.ok(resolve(env.SAT20_REAL_REGTEST_RUNTIME) === env.SAT20_REAL_REGTEST_RUNTIME,
    'SAT20_REAL_REGTEST_RUNTIME must be absolute')
  parseLoopbackURL(env.SAT20_REAL_L1_INDEXER, 'L1 indexer', '/btc/regtest')
  parseLoopbackURL(env.SAT20_REAL_ESPLORA, 'Electrs/Esplora')
  const chain = checkedBitcoinJSON(env, 'getblockchaininfo')
  const network = checkedBitcoinJSON(env, 'getnetworkinfo')
  assert.equal(chain.chain, 'regtest', 'configured Bitcoin Core is not regtest')
  assert.equal(network.version, 310000, 'configured Bitcoin Core is not version 31.0')
  const peers = checkedBitcoinJSON(env, 'getpeerinfo')
  assert.ok(Array.isArray(peers), 'Bitcoin Core getpeerinfo did not return a peer list')
  assert.equal(peers.every(peer => isLoopbackPeerAddr(peer?.addr)), true,
    'configured Bitcoin Core must have loopback peers only')
  const healthURL = `${env.SAT20_REAL_L1_INDEXER.replace(/\/+$/, '')}/health`
  const health = await fetch(healthURL, { signal: AbortSignal.timeout(10000) })
  assert.equal(health.status, 200, 'L1 indexer health endpoint must return HTTP 200')
  const healthBody = await health.json()
  assert.equal(healthBody.status, 'ok', 'L1 indexer must report healthy')
  if (env.SAT20_REAL_PWA_EXISTING_URL) {
    assert.ok(env.SAT20_REAL_SATOSHINET_ATTACH, 'reusing the PWA requires the explicit regtest node attach configuration')
    assert.equal(env.SAT20_REAL_PWA_EXISTING_URL, `http://127.0.0.1:${port}/`)
    const response = await fetch(`${env.SAT20_REAL_PWA_EXISTING_URL}generated/wasm-integrity.ts`, { signal: AbortSignal.timeout(10000) })
    assert.equal(response.status, 200, 'existing PWA integrity module must be available')
    assert.match(await response.text(), /"releaseId"\s*:\s*"[^"]+"/, 'existing PWA must declare its actual release ID')
  } else {
    await assertPortFree(port, 'PWA browser')
  }
  if (env.SAT20_REAL_SATOSHINET_ATTACH) {
    assert.equal(resolve(env.SAT20_REAL_SATOSHINET_ATTACH), env.SAT20_REAL_SATOSHINET_ATTACH)
    assert.equal(statSync(env.SAT20_REAL_SATOSHINET_ATTACH).mode & 0o777, 0o600,
      'node attach configuration must be private')
  } else {
    for (const nodePort of [30250, 30251, 30252, 30253, 30254, 30260, 30261, 30262, 30263, 30264]) {
      await assertPortFree(nodePort, 'SDK-owned SatoshiNet test node')
    }
  }
  return { core: `Bitcoin Core 31.0 regtest; ${peers.length} loopback peer(s)`, l1: 'loopback /btc/regtest healthy',
    walletFixture: 'private and required roles present', pwaPort: port,
    pwaService: env.SAT20_REAL_PWA_EXISTING_URL ? 'existing service; isolated browser context' : 'test-owned Vite',
    sdkNodePorts: env.SAT20_REAL_SATOSHINET_ATTACH ? 'existing loopback forwards; test does not own node processes' : '30250-30254, 30260-30264 free' }
}

export function buildPlan(stages, port, env = process.env) {
  return stages.map((stage, index) => ({
    order: index + 1, stage: stage.id, repository: stage.repo, package: stage.packageName,
    command: buildCommand(stage, env),
    expectedTopLevelTests: stage.tests, timeout: stage.timeoutMs,
    pwaBrowserPort: stage.repo === 'sat20wallet/sdk' ? port : undefined,
    requires: stage.requires,
    boundary: stage.boundary ?? (stage.id === 'satoshinet-contract-e2e' ? 'local Core + fake L1 fixtures' : undefined),
  }))
}

export function spawnStage(stage, env, logPath, { command = buildCommand(stage, env), graceMs = 30_000, killGraceMs = 10_000 } = {}) {
  return new Promise(resolvePromise => {
    const logFD = openSync(logPath, 'wx', 0o600)
    const started = performance.now()
    let settled = false
    let timedOut = false
    let timer
    const grouped = process.platform !== 'win32'
    const ownershipKey = 'SAT20_WORKSPACE_E2E_STAGE'
    const ownership = randomUUID()
    let child
    let cleanup
    let interrupted
    const ownedProcesses = () => {
      // Go tests, Node runners and Playwright inherit this per-stage marker,
      // including browsers that create a separate process group or get orphaned.
      // Inspect only this user's processes; never log commands or environments.
      const inspection = spawnSync('ps', ['eww', '-U', String(process.getuid()), '-o', 'pid=,pgid=,stat=,command='], {
        encoding: 'utf8', timeout: 5000, maxBuffer: 64 * 1024 * 1024,
      })
      if (inspection.error || inspection.status !== 0) throw new Error('Unable to inspect stage-owned processes')
      const marker = new RegExp(`(?:^|\\s)${ownershipKey}=${ownership}(?:\\s|$)`)
      return inspection.stdout.split('\n').flatMap(line => {
        const entry = /^\s*(\d+)\s+(\d+)\s+(\S+)\s+(.*)$/.exec(line)
        return entry && !entry[3].startsWith('Z') && (Number(entry[2]) === child?.pid || marker.test(entry[4]))
          ? [{ pid: Number(entry[1]), group: Number(entry[2]) }] : []
      })
    }
    const signalOwned = (pid, signal) => {
      try { process.kill(pid, signal) }
      catch (error) {
        if (error.code === 'ESRCH') return
        // On macOS a process can become a zombie between inspection and kill,
        // causing EPERM. Confirm it cannot run before ignoring that error.
        if (error.code === 'EPERM' && !ownedProcesses().some(entry => pid < 0 ? entry.group === -pid : entry.pid === pid)) return
        throw error
      }
    }
    const signalTree = signal => {
      if (!child?.pid) return
      if (grouped) {
        signalOwned(-child.pid, signal)
        for (const { pid } of ownedProcesses()) signalOwned(pid, signal)
      } else {
        try { child.kill(signal) }
        catch (error) { if (error.code !== 'ESRCH') throw error }
      }
    }
    const treeAlive = () => {
      if (!child?.pid) return false
      if (!grouped) return child.exitCode === null && child.signalCode === null
      return ownedProcesses().length > 0
    }
    const waitForTree = async (deadline, signal) => {
      while (treeAlive()) {
        if (performance.now() >= deadline) return false
        if (signal) signalTree(signal)
        await new Promise(resolve => setTimeout(resolve, 25))
      }
      return true
    }
    const stopTree = () => cleanup ??= (async () => {
      if (!treeAlive()) return
      signalTree('SIGTERM')
      if (await waitForTree(performance.now() + killGraceMs)) return
      signalTree('SIGKILL')
      if (!await waitForTree(performance.now() + 2000, 'SIGKILL')) throw new Error('Stage-owned processes did not stop after SIGKILL')
    })()
    const onInterrupt = signal => {
      interrupted = signal
      void finish({ exitCode: null, timedOut, error: `interrupted by ${signal}` })
    }
    const onSigint = () => onInterrupt('SIGINT')
    const onSigterm = () => onInterrupt('SIGTERM')
    const finish = async result => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      try { await stopTree() }
      catch (error) { result = { ...result, exitCode: null, error: error.message } }
      if (interrupted) result = { ...result, exitCode: null, error: `interrupted by ${interrupted}` }
      process.off('SIGINT', onSigint)
      process.off('SIGTERM', onSigterm)
      closeSync(logFD)
      resolvePromise({ ...result, durationMs: Math.round(performance.now() - started) })
    }
    try {
      child = spawn(command[0], command.slice(1), {
        cwd: stage.cwd, env: { ...env, [ownershipKey]: ownership }, stdio: ['ignore', logFD, logFD], detached: grouped,
      })
    } catch (error) {
      finish({ exitCode: null, timedOut, error: error.message })
      return
    }
    timer = setTimeout(() => {
      timedOut = true
      void finish({ exitCode: null, timedOut })
    }, stage.timeoutMs + graceMs)
    timer.unref()
    process.on('SIGINT', onSigint)
    process.on('SIGTERM', onSigterm)
    child.once('error', error => {
      clearTimeout(timer)
      finish({ exitCode: null, timedOut, error: error.message })
    })
    child.once('close', code => {
      clearTimeout(timer)
      finish({ exitCode: code, timedOut })
    })
  })
}

function helpText() {
  return [
    'Usage: node workspace-e2e.mjs [--plan|--check] [--only STAGE... ] [--tests EXACT_TEST... ] [--env-file PRIVATE_FILE]',
    '',
    'Modes: --plan prints exact serial stages; --check validates current regtest prerequisites; default runs all stages.',
    '--only accepts exact stage IDs and runs only those stages, in catalog order; no previous results are reused automatically.',
    '--tests selects exact cases within those stages; omitted cases remain recorded as not selected, never as PASS.',
    'For Transcend, optional SAT20_TRANSCEND_E2E_MODFILE selects a private .mod/.sum pair; otherwise Go uses the workspace module files.',
    `Stages: ${stageCatalog.map(stage => stage.id).join(', ')}`,
    'The browser runner defaults to SAT20_PWA_E2E_PORT=5189. Standalone PWA commands keep their existing 5179 default.',
  ].join('\n')
}

async function main(argv) {
  const options = parseArguments(argv)
  if (options.mode === 'help') {
    process.stdout.write(`${helpText()}\n`)
    return 0
  }
  const env = effectiveEnv(options.envFile)
  const configuredStages = configureStages(options.stages, env)
  const stages = selectTests(configuredStages, options.tests)
  const port = parsePort(env.SAT20_PWA_E2E_PORT)
  env.SAT20_PWA_E2E_PORT = String(port)
  if (options.mode === 'plan') {
    process.stdout.write(`${JSON.stringify({ mode: 'plan', pwaBrowserPort: port, autoReuse: false, stages: buildPlan(stages, port, env) }, null, 2)}\n`)
    return 0
  }
  const needsSDK = stages.some(stage => stage.repo === 'sat20wallet/sdk')
  const prerequisites = needsSDK ? await checkSDKEnvironment(env, port) : { pwaBrowserPort: port }
  for (const stage of stages) buildCommand(stage, env)
  const requiredGo = stages.some(stage => stage.repo !== 'sat20wallet/pwa')
  if (requiredGo) {
    const version = spawnSync(env.GO || 'go', ['version'], { encoding: 'utf8', timeout: 10000, env })
    assert.equal(version.status, 0, `Go toolchain unavailable: ${version.error?.message || version.stderr}`)
  }
  const nodeVersion = spawnSync(process.execPath, ['--version'], { encoding: 'utf8', timeout: 10000 })
  assert.equal(nodeVersion.status, 0, 'Node.js is unavailable')
  if (options.mode === 'check') {
    process.stdout.write(`${JSON.stringify({ mode: 'check', status: 'ready', prerequisites, selectedStages: stages.map(stage => stage.id) }, null, 2)}\n`)
    return 0
  }

  const runDir = mkdtempSync(resolve(tmpdir(), 'sat20-workspace-e2e-'))
  chmodSync(runDir, 0o700)
  const report = { mode: 'run', runDir, pwaBrowserPort: port, prerequisites,
    selectedStages: stages.map(stage => stage.id),
    notSelectedTests: configuredStages.flatMap(stage => stage.tests.filter(name => !stages.some(selectedStage => selectedStage.id === stage.id && selectedStage.tests.includes(name)))
      .map(test => ({ stage: stage.id, test, reason: 'not selected' }))),
    executed: [], reused: [], notRun: [], status: 'running' }
  const reportPath = resolve(runDir, 'summary.json')
  const writeReport = () => {
    writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 })
    chmodSync(reportPath, 0o600)
  }
  const selected = new Set(stages.map(stage => stage.id))
  let failed = false
  for (const stage of stages) {
    const logPath = resolve(runDir, `${stage.id}.jsonl`)
    process.stdout.write(`Running ${stage.id}; detailed output is private.\n`)
    const result = await spawnStage(stage, env, logPath, { command: buildCommand(stage, env) })
    const summary = summarizeGoJSON(readFileSync(logPath, 'utf8'), stage.tests)
    const passed = result.exitCode === 0 && !result.timedOut && summary.skipped.length === 0 &&
      summary.failed.length === 0 && summary.missing.length === 0
    report.executed.push({ stage: stage.id, status: passed ? 'pass' : 'fail', exitCode: result.exitCode,
      timedOut: result.timedOut, durationMs: result.durationMs, log: logPath, tests: summary,
      ...(stage.boundary ? { boundary: stage.boundary } : {}),
      ...(result.error ? { spawnError: result.error } : {}) })
    if (!passed) {
      failed = true
      const rest = stages.slice(stages.findIndex(item => item.id === stage.id) + 1)
      report.notRun.push(...rest.map(item => ({ stage: item.id, reason: 'earlier stage failed' })))
      break
    }
    writeReport()
  }
  if (!failed) report.status = 'pass'
  else report.status = 'fail'
  report.notRun.push(...stageCatalog.filter(stage => !selected.has(stage.id)).map(stage => ({ stage: stage.id, reason: 'not selected' })))
  writeReport()
  process.stdout.write(`${JSON.stringify({ status: report.status, runDir, summary: reportPath,
    executed: report.executed.map(({ stage, status, exitCode, durationMs, tests }) => ({ stage, status, exitCode, durationMs,
      topLevelPassed: tests.passed.length, topLevelSkipped: tests.skipped.length })),
    notRun: report.notRun.map(item => item.stage), reused: [] })}\n`)
  return failed ? 1 : 0
}

const invokedPath = process.argv[1] ? resolve(process.argv[1]) : ''
if (invokedPath && import.meta.url === pathToFileURL(invokedPath).href) {
  main(process.argv.slice(2)).then(code => { process.exitCode = code }).catch(error => {
    process.stderr.write(`workspace-e2e: ${error.message}\n`)
    process.exitCode = 1
  })
}
