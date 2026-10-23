import test from 'node:test'
import assert from 'node:assert/strict'
import { chmodSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { spawn, spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { buildCommand, buildGroups, buildPlan, configureStages, isLoopbackPeerAddr, parseArguments, parsePort, selectStages, selectTests, shouldStopBatchAfterGroup, spawnStage, stageCatalog, summarizeGoJSON, workspaceRoot } from './workspace-e2e.mjs'

const wrapperPath = fileURLToPath(new URL('./workspace-e2e.mjs', import.meta.url))
const makePrivateDir = () => {
  const dir = mkdtempSync(join(tmpdir(), 'workspace-e2e-test-'))
  chmodSync(dir, 0o700)
  return dir
}

test('stage commands contain one Go test verb before their flags and package', () => {
  for (const stage of stageCatalog) {
    const command = buildCommand(stage, { GO: 'go' })
    assert.equal(command[1], 'test')
    assert.equal(command.filter(arg => arg === 'test').length, 1,
      `${stage.id} would treat an extra test verb as a package`)
    assert.ok(command[2].startsWith('-'), 'Go flags must follow the test verb')
    assert.ok(command.includes('-failfast'), 'shared-wallet cases stop after the first failure')
  }
})

test('default catalog contains only real local-chain entry points', () => {
  assert.equal(stageCatalog.length, 8)
  assert.ok(stageCatalog.every(stage => stage.tests.every(name => name.startsWith('TestSDKPWARealRegtest'))))
  assert.ok(selectStages(['pwa-canonical'])[0].tests.includes('TestSDKPWARealRegtestAssetContextStabilityBrowser'))
  assert.throws(() => selectStages(['indexer-silentpay']), /unknown stage/)
  assert.throws(() => selectStages(['transcend-stp']), /unknown stage/)
})

test('only shared setup errors stop later independent groups', () => {
  const fail = (test, purpose = 'prerequisite') => ({
    executed: [{ test, purpose, status: 'fail' }],
  })
  const sharedPreparation = [
    'TestSDKPWARealRegtestNodeReadiness',
    'TestSDKPWARealRegtestFreshAccountPreparation',
    'TestSDKPWARealRegtestL1AssetPreparation',
  ]
  for (const testName of sharedPreparation) {
    assert.equal(shouldStopBatchAfterGroup(fail(testName)), true, testName + ' failure stops the batch')
  }

  for (const testName of [
    'TestSDKPWARealRegtestChannelActivationBrowser',
    'TestSDKPWARealRegtestChannelOpeningOrReadyBrowser',
  ]) {
    assert.equal(shouldStopBatchAfterGroup(fail(testName)), false,
      testName + ' is a business prerequisite local to its group')
  }

  const completedSetupThenFailedJourney = {
    executed: [
      ...sharedPreparation.map(test => ({ test, purpose: 'prerequisite', status: 'pass' })),
      { test: 'TestSDKPWARealRegtestChannelActivationBrowser', purpose: 'prerequisite', status: 'fail' },
    ],
  }
  assert.equal(shouldStopBatchAfterGroup(completedSetupThenFailedJourney), false,
    'already-passed shared preparation does not turn a later journey failure into a batch setup error')
  assert.equal(shouldStopBatchAfterGroup(fail('TestSDKPWARealRegtestWalletBasicAndBiometricBrowser', 'case')), false,
    'an ordinary case assertion/exit failure still fails its group without suppressing independent groups')
  assert.equal(shouldStopBatchAfterGroup({ error: 'environment setup failed' }), true)
  assert.equal(shouldStopBatchAfterGroup({ cleanupError: 'environment cleanup failed' }), true)
})

test('dependent return case includes its send; unrelated cases get distinct preparation groups', () => {
  const stages = selectTests(selectStages(['pwa-canonical']), [
    'TestSDKPWARealRegtestRunesPremineReturnBrowser', 'TestSDKPWARealRegtestBitcoinFundsSendBrowser',
  ])
  const groups = buildGroups(stages)
  assert.equal(groups.length, 2)
  const steps = groups[0].steps.map(step => step.id)
  assert.deepEqual(steps.slice(0, 3), [
    'TestSDKPWARealRegtestNodeReadiness', 'TestSDKPWARealRegtestFreshAccountPreparation',
    'TestSDKPWARealRegtestL1AssetPreparation',
  ])
  assert.deepEqual(steps.slice(3), [
    'TestSDKPWARealRegtestRunesDecimalSendBrowser', 'TestSDKPWARealRegtestRunesPremineReturnBrowser',
  ])
  assert.equal(groups[0].steps.at(-2).purpose, 'prerequisite')
  assert.equal(groups[0].steps.at(-1).purpose, 'case')
  assert.notEqual(groups[0].id, groups[1].id)
  assert.deepEqual(groups[1].steps.slice(0, 3).map(step => step.id), steps.slice(0, 3))
})

test('SDK WASM gets channel preparation without consuming the independent PWA channel case', () => {
  const opening = 'TestSDKPWARealRegtestChannelOpeningOrReadyBrowser'
  const sdkCase = 'TestSDKPWARealRegtestSDKWASMInterfacesBrowser'
  const groups = buildGroups(selectTests(selectStages(['sdk-wasm-real', 'pwa-canonical']), [sdkCase, opening]))
  assert.equal(groups.length, 2)
  const sdkGroup = groups.find(group => group.selectedTests.includes(sdkCase))
  const pwaGroup = groups.find(group => group.selectedTests.includes(opening))
  assert.ok(sdkGroup)
  assert.ok(pwaGroup)
  assert.deepEqual(sdkGroup.selectedTests, [sdkCase])
  assert.deepEqual(pwaGroup.selectedTests, [opening])
  const sdkSteps = sdkGroup.steps
  assert.equal(sdkSteps.at(-2).id, opening)
  assert.equal(sdkSteps.at(-2).purpose, 'prerequisite')
  assert.equal(sdkSteps.at(-1).id, sdkCase)
  assert.equal(sdkSteps.at(-1).purpose, 'case')
  assert.equal(pwaGroup.steps.at(-1).id, opening)
  assert.equal(pwaGroup.steps.at(-1).purpose, 'case')
  assert.equal(groups.flatMap(group => group.steps).filter(step => step.id === opening).length, 2)
})

test('CSV force close uses its own opening and BTC splice preparation group', () => {
  const opening = 'TestSDKPWARealRegtestChannelOpeningOrReadyBrowser'
  const splice = 'TestSDKPWARealRegtestChannelSplicingBrowser'
  const forceClose = 'TestSDKPWARealRegtestForceCloseCSVBrowser'
  const groups = buildGroups(selectStages(['pwa-force-close-csv']))
  assert.equal(groups.length, 1)
  assert.deepEqual(groups[0].selectedTests, [forceClose])
  const steps = groups[0].steps
  assert.deepEqual(steps.slice(-3).map(step => step.id), [opening, splice, forceClose])
  assert.deepEqual(steps.slice(-3).map(step => step.purpose), ['prerequisite', 'prerequisite', 'case'])
  assert.match(groups[0].isolation, /fresh local Bitcoin Core/)
})

test('Guardian 2-of-3 recovery gets a fresh real-regtest group with temporary-storage preparation', () => {
  const guardian = 'TestSDKPWARealRegtestGuardianRecoveryBrowser'
  const group = buildGroups(selectStages(['pwa-guardian-2of3']))[0]
  assert.deepEqual(group.selectedTests, [guardian])
  assert.deepEqual(group.steps.slice(0, 3).map(step => step.id), [
    'TestSDKPWARealRegtestNodeReadiness', 'TestSDKPWARealRegtestFreshAccountPreparation',
    'TestSDKPWARealRegtestL1AssetPreparation',
  ])
  assert.deepEqual(group.steps.slice(0, 3).map(step => step.purpose), ['prerequisite', 'prerequisite', 'prerequisite'])
  assert.equal(group.steps.at(-1).id, guardian)
  assert.equal(group.steps.at(-1).purpose, 'case')
  assert.doesNotMatch(group.steps.map(step => step.id).join(' '), /ChannelOpeningOrReady|FixedAccountManagement/)
  assert.match(group.isolation, /fresh local Bitcoin Core/)
})

test('private BTC round trip joins the ordered channel journey without swallowing cases', () => {
  const opening = 'TestSDKPWARealRegtestChannelOpeningOrReadyBrowser'
  const splice = 'TestSDKPWARealRegtestChannelSplicingBrowser'
  const safety = 'TestSDKPWARealRegtestChannelSafetyBrowser'
  const privateBTC = 'TestSDKPWARealRegtestPrivateBTCRoundTripBrowser'
  const spliceOut = 'TestSDKPWARealRegtestPrivateBTCSplicingOutBrowser'
  const selectCases = names => selectTests(selectStages(['pwa-canonical']), names)

  const privateOnly = buildGroups(selectCases([privateBTC]))
  assert.equal(privateOnly.length, 1)
  assert.deepEqual(privateOnly[0].selectedTests, [privateBTC])
  assert.deepEqual(privateOnly[0].steps.slice(-4).map(step => step.id), [opening, splice, safety, privateBTC])
  assert.deepEqual(privateOnly[0].steps.slice(-4).map(step => step.purpose),
    ['prerequisite', 'prerequisite', 'prerequisite', 'case'])

  const spliceOutOnly = buildGroups(selectCases([spliceOut]))
  assert.deepEqual(spliceOutOnly[0].steps.slice(-5).map(step => step.id),
    [opening, splice, safety, privateBTC, spliceOut])
  assert.deepEqual(spliceOutOnly[0].steps.slice(-5).map(step => step.purpose),
    ['prerequisite', 'prerequisite', 'prerequisite', 'prerequisite', 'case'])

  const combined = buildGroups(selectCases([opening, safety, privateBTC, spliceOut]))
  assert.equal(combined.length, 1)
  assert.deepEqual(combined[0].selectedTests, [opening, safety, privateBTC, spliceOut])
  assert.deepEqual(combined[0].steps.slice(-5).map(step => step.id),
    [opening, splice, safety, privateBTC, spliceOut])
  assert.deepEqual(combined[0].steps.slice(-5).map(step => step.purpose),
    ['case', 'prerequisite', 'case', 'case', 'case'])

  const withIndependent = buildGroups(selectCases([privateBTC, 'TestSDKPWARealRegtestBitcoinFundsSendBrowser']))
  assert.equal(withIndependent.length, 2)
  assert.deepEqual(withIndependent.find(group => group.selectedTests.includes(privateBTC)).selectedTests, [privateBTC])
  assert.deepEqual(withIndependent.find(group => group.selectedTests.includes('TestSDKPWARealRegtestBitcoinFundsSendBrowser')).selectedTests,
    ['TestSDKPWARealRegtestBitcoinFundsSendBrowser'])
})

test('selecting both dependent cases runs their journey once in one group', () => {
  const stages = selectTests(selectStages(['pwa-canonical']), [
    'TestSDKPWARealRegtestRunesDecimalSendBrowser', 'TestSDKPWARealRegtestRunesPremineReturnBrowser',
  ])
  const groups = buildGroups(stages)
  assert.equal(groups.length, 1)
  assert.equal(groups[0].selectedTests.length, 2)
  assert.equal(groups[0].steps.length, 5)
  assert.equal(buildGroups(selectStages(['sdk-node-readiness']))[0].steps.length, 1)
})

test('--only selects exact known stages in dependency order and rejects missing selections', () => {
  assert.deepEqual(selectStages(['sdk-rgb-closed-loop', 'sdk-node-readiness']).map(stage => stage.id), [
    'sdk-node-readiness', 'sdk-rgb-closed-loop',
  ])
  assert.throws(() => selectStages(['sdk-rbg-closed-loop']), /unknown stage ID/)
  assert.throws(() => selectStages([]), /at least one exact stage/)
  assert.throws(() => selectStages(['sdk-assets', 'sdk-assets']), /cannot repeat/)
  assert.deepEqual(parseArguments(['--only', 'sdk-assets', 'sdk-node-readiness']).stages.map(stage => stage.id), [
    'sdk-node-readiness', 'sdk-assets',
  ])
})

test('--tests selects exact cases in catalog order without rerunning their whole stage', () => {
  const before = stageCatalog.map(stage => [...stage.tests])
  const names = ['TestSDKPWARealRegtestChannelSplicingBrowser', 'TestSDKPWARealRegtestRunesDecimalSendBrowser']
  const selected = selectTests(selectStages(['pwa-canonical']), names)
  assert.equal(selected.length, 1)
  assert.deepEqual(selected[0].tests, [...names].reverse())
  assert.deepEqual(stageCatalog.map(stage => stage.tests), before, 'selection must not mutate the shared catalog')
  assert.deepEqual(parseArguments(['--only', 'pwa-canonical', '--tests', ...names]).tests, names)
  assert.throws(() => selectTests(stageCatalog, []), /at least one exact test/)
  assert.throws(() => selectTests(stageCatalog, [names[0], names[0]]), /cannot repeat/)
  assert.throws(() => selectTests(stageCatalog, ['TestSDKPWARealRegtestRunes.*']), /unknown test/)
  assert.throws(() => selectTests(selectStages(['sdk-assets']), names), /unknown test/)
})

test('retained or attached environment is rejected even when its URL is loopback', () => {
  for (const key of ['SAT20_REAL_SATOSHINET_ATTACH', 'SAT20_REAL_RECOVERY_FIXTURES',
    'SAT20_REAL_SDK_WALLET_DIR', 'SAT20_REAL_PWA_EXISTING_URL', 'SAT20_REAL_REGTEST_RUNTIME',
    'SAT20_REAL_L1_INDEXER', 'SAT20_REAL_ESPLORA', 'SAT20_REAL_BITCOIN_DATADIR']) {
    assert.throws(() => configureStages(stageCatalog, { [key]: '/private/old-state' }), /external or retained state/)
  }
  assert.deepEqual(configureStages(stageCatalog, {}), stageCatalog)
})

test('canonical channel preparation opens only when absent and preserves existing READY channels', () => {
  const canonical = stageCatalog.find(stage => stage.id === 'pwa-canonical')
  assert.ok(canonical.tests.includes('TestSDKPWARealRegtestChannelOpeningOrReadyBrowser'))
  assert.ok(!canonical.tests.includes('TestSDKPWARealRegtestChannelOpeningBrowser'),
    'strict first opening must remain an explicit test instead of failing on legitimate retained state')
})

test('Go JSON result validation rejects skips, failures, and zero-hit selectors', () => {
  const expected = ['TestOne', 'TestTwo']
  const passed = [
    { Action: 'run', Test: 'TestOne' }, { Action: 'pass', Test: 'TestOne' },
    { Action: 'run', Test: 'TestTwo' }, { Action: 'pass', Test: 'TestTwo' },
  ].map(event => JSON.stringify(event)).join('\n')
  assert.deepEqual(summarizeGoJSON(passed, expected), {
    expected: 2, passed: expected, failed: [], skipped: [], missing: [], testEventCount: 4,
  })
  const skipped = `${JSON.stringify({ Action: 'run', Test: 'TestOne' })}\n${JSON.stringify({ Action: 'skip', Test: 'TestOne' })}`
  const result = summarizeGoJSON(skipped, expected)
  assert.deepEqual(result.skipped, ['TestOne'])
  assert.deepEqual(result.missing, expected)
  assert.deepEqual(summarizeGoJSON('', expected).missing, expected)
  const failed = `${JSON.stringify({ Action: 'fail', Test: 'TestTwo' })}`
  assert.deepEqual(summarizeGoJSON(failed, expected).failed, ['TestTwo'])
})

test('browser port validation preserves standalone default and rejects invalid values', () => {
  assert.equal(parsePort(undefined), 5189)
  assert.equal(parsePort('5189'), 5189)
  assert.throws(() => parsePort('0'), /from 1 to 65535/)
  assert.throws(() => parsePort('65536'), /from 1 to 65535/)
  assert.throws(() => parsePort('51x9'), /integer/)
})

test('Bitcoin Core readiness accepts only IPv4 or IPv6 loopback peer addresses', () => {
  assert.equal(isLoopbackPeerAddr('127.0.0.1:50386'), true)
  assert.equal(isLoopbackPeerAddr('127.255.12.4:8333'), true)
  assert.equal(isLoopbackPeerAddr('[::1]:50386'), true)
  assert.equal(isLoopbackPeerAddr('192.168.1.2:8333'), false)
  assert.equal(isLoopbackPeerAddr('8.8.8.8:8333'), false)
  assert.equal(isLoopbackPeerAddr('[::2]:8333'), false)
  assert.equal(isLoopbackPeerAddr('localhost:8333'), false)
  assert.equal(isLoopbackPeerAddr('[::1]'), false)
})

test('plan subprocess expands prerequisites without contacting services or requiring old files', () => {
  const result = spawnSync(process.execPath, [wrapperPath, '--plan', '--only', 'sdk-rgb-closed-loop'], {
    cwd: workspaceRoot, env: { PATH: process.env.PATH }, encoding: 'utf8', timeout: 10000,
  })
  assert.equal(result.status, 0, result.stderr)
  const plan = JSON.parse(result.stdout)
  assert.equal(plan.sharedHistoricalState, false)
  assert.equal(plan.groups.length, 1)
  assert.equal(plan.groups[0].steps.length, 2)
  for (const stage of stageCatalog) {
    const command = buildCommand(stage, { GO: '/private/toolchain/go' })
    assert.equal(command[0], '/private/toolchain/go')
    assert.equal(command.filter(arg => arg === '-mod=readonly').length, 1)
    assert.ok(command.includes('-timeout=60m'))
  }
})

test('spawnStage handles ENOENT once and applies its timeout after the requested duration', async () => {
  const dir = makePrivateDir()
  try {
    const baseStage = { id: 'stub-stage', cwd: dir, timeoutMs: 100 }
    const missing = await spawnStage(baseStage, process.env, join(dir, 'enoent.log'), {
      command: [join(dir, 'no-such-command')], graceMs: 5,
    })
    assert.equal(missing.exitCode, null)
    assert.match(missing.error, /ENOENT/)
    assert.equal(statSync(join(dir, 'enoent.log')).mode & 0o777, 0o600)

    const timed = await spawnStage(baseStage, process.env, join(dir, 'timeout.log'), {
      command: [process.execPath, '-e', 'setInterval(() => {}, 1000)'], graceMs: 5,
    })
    assert.equal(timed.timedOut, true)
    assert.ok(timed.durationMs >= 90, `timeout fired too early: ${timed.durationMs}ms`)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

for (const detached of [false, true]) {
  for (const parentExit of ['timeout', 'normal']) {
    test(`spawnStage cleans its stubborn ${detached ? 'detached' : 'same-group'} descendants after ${parentExit} without touching other processes`, { skip: process.platform === 'win32' }, async () => {
      const dir = makePrivateDir()
      const pidPath = join(dir, 'descendant.pid')
      const sentinel = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], {
        stdio: 'ignore', env: { ...process.env, SAT20_WORKSPACE_E2E_STAGE: 'unrelated-stage' },
      })
      let descendant
      try {
        const grandchild = `
          const fs = require('node:fs')
          process.on('SIGTERM', () => {})
          fs.writeFileSync(${JSON.stringify(pidPath)}, String(process.pid))
          process.stdout.write('ready')
          setInterval(() => {}, 1000)
        `
        const parent = `
          const { spawn } = require('node:child_process')
          const child = spawn(process.execPath, ['-e', ${JSON.stringify(grandchild)}], { detached: ${detached}, stdio: ['ignore', 'pipe', 'ignore'] })
          child.stdout.once('data', () => { ${parentExit === 'normal' ? 'process.exit(0)' : ''} })
          setInterval(() => {}, 1000)
        `
        const result = await spawnStage({ id: 'tree', cwd: dir, timeoutMs: 1000 }, process.env, join(dir, 'tree.log'), {
          command: [process.execPath, '-e', parent], graceMs: 5, killGraceMs: 100,
        })
        descendant = Number(readFileSync(pidPath, 'utf8'))
        assert.equal(result.error, undefined)
        assert.equal(result.timedOut, parentExit === 'timeout')
        if (parentExit === 'normal') assert.equal(result.exitCode, 0)
        // A returned stage result must already imply descendant cleanup.
        let alive = true
        try { process.kill(descendant, 0) } catch (error) { if (error.code === 'ESRCH') alive = false; else throw error }
        let state = ''
        if (alive) {
          const inspection = spawnSync('ps', ['-o', 'stat=', '-p', String(descendant)], { encoding: 'utf8' })
          assert.ifError(inspection.error)
          state = inspection.stdout.trim()
        }
        // kill(pid, 0) can still see an exited process awaiting reaping. Such a
        // zombie cannot run; any other surviving state must fail this check.
        assert.ok(!state || state.startsWith('Z'), `stage returned while its grandchild could still run (pid=${descendant}, state=${state})`)
        assert.doesNotThrow(() => process.kill(sentinel.pid, 0), 'unrelated/attached processes must survive')
      } finally {
        if (!descendant) { try { descendant = Number(readFileSync(pidPath, 'utf8')) } catch {} }
        if (descendant) { try { process.kill(descendant, 'SIGKILL') } catch (error) { if (error.code !== 'ESRCH') throw error } }
        sentinel.kill('SIGKILL')
        rmSync(dir, { recursive: true, force: true })
      }
    })
  }
}

test('failed process inspection still stops its owned child and reports failure', { skip: process.platform === 'win32' }, async () => {
  const dir = makePrivateDir(), pidPath = join(dir, 'owned.pid')
  const previousPath = process.env.PATH
  let owned
  try {
    writeFileSync(join(dir, 'ps'), '#!/bin/sh\nexit 1\n', { mode: 0o700 })
    process.env.PATH = `${dir}:${previousPath}`
    const result = await spawnStage({ cwd: dir, timeoutMs: 300 }, process.env, join(dir, 'inspection.log'), {
      command: [process.execPath, '-e', `require('node:fs').writeFileSync(${JSON.stringify(pidPath)}, String(process.pid)); setInterval(() => {}, 1000)`],
      graceMs: 0, killGraceMs: 100,
    })
    owned = Number(readFileSync(pidPath, 'utf8'))
    assert.equal(result.exitCode, null)
    assert.match(result.error, /Unable to inspect/)
    for (let attempt = 0; attempt < 100; attempt++) {
      try { process.kill(owned, 0) }
      catch (error) { if (error.code === 'ESRCH') { owned = null; break } throw error }
      await new Promise(resolve => setTimeout(resolve, 20))
    }
    assert.equal(owned, null, 'inspection failure must not leave its child running')
  } finally {
    process.env.PATH = previousPath
    if (owned) { try { process.kill(owned, 'SIGKILL') } catch (error) { if (error.code !== 'ESRCH') throw error } }
    rmSync(dir, { recursive: true, force: true })
  }
})

test('interrupting the stage owner waits for its process group to stop', { skip: process.platform === 'win32', timeout: 15000 }, async () => {
  const dir = makePrivateDir(), pidPath = join(dir, 'owned.pid')
  let owned
  const program = `
    import { spawnStage } from ${JSON.stringify(new URL('./workspace-e2e.mjs', import.meta.url).href)}
    const result = await spawnStage({ cwd: ${JSON.stringify(dir)}, timeoutMs: 30000 }, process.env, ${JSON.stringify(join(dir, 'interrupt.log'))}, {
      command: [process.execPath, '-e', ${JSON.stringify(`process.on('SIGTERM', () => {}); require('node:fs').writeFileSync(${JSON.stringify(pidPath)}, String(process.pid)); setInterval(() => {}, 1000)`)}],
      killGraceMs: 100,
    })
    console.log(JSON.stringify(result))
  `
  const runner = spawn(process.execPath, ['--input-type=module', '-e', program], { stdio: ['ignore', 'pipe', 'pipe'] })
  let stdout = '', stderr = ''
  runner.stdout.on('data', chunk => { stdout += chunk })
  runner.stderr.on('data', chunk => { stderr += chunk })
  const closed = new Promise(resolve => runner.once('close', code => resolve(code)))
  try {
    for (let attempt = 0; attempt < 100 && !owned; attempt++) {
      try { owned = Number(readFileSync(pidPath, 'utf8')) } catch {}
      if (!owned) await new Promise(resolve => setTimeout(resolve, 20))
    }
    assert.ok(owned, `stage never started: ${stderr}`)
    runner.kill('SIGTERM')
    assert.equal(await closed, 0, stderr)
    const result = JSON.parse(stdout.trim())
    assert.equal(result.exitCode, null)
    assert.match(result.error, /SIGTERM/)
    assert.throws(() => process.kill(owned, 0), error => error.code === 'ESRCH')
  } finally {
    if (owned) { try { process.kill(owned, 'SIGKILL') } catch (error) { if (error.code !== 'ESRCH') throw error } }
    if (runner.exitCode === null) runner.kill('SIGKILL')
    rmSync(dir, { recursive: true, force: true })
  }
})
