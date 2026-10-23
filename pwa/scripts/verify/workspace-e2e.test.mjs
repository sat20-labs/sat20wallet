import test from 'node:test'
import assert from 'node:assert/strict'
import { chmodSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { spawn, spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { buildCommand, buildPlan, configureStages, isLoopbackPeerAddr, parseArguments, parsePort, selectStages, selectTests, spawnStage, stageCatalog, summarizeGoJSON, workspaceRoot } from './workspace-e2e.mjs'

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

test('Transcend aggregate command enables its build tag and exact required selector', () => {
  const stage = stageCatalog.find(item => item.id === 'transcend-stp')
  const command = buildCommand(stage, { GO: 'go' })
  assert.ok(command.includes('-tags=stp_aggregate'))
  assert.equal(command[command.indexOf('-run') + 1], '^TestRunAll$')
})

test('default plan is the complete serial catalog and keeps the browser port isolated', () => {
  const plan = buildPlan(stageCatalog, parsePort(undefined))
  assert.equal(plan.length, 9)
  assert.deepEqual(plan.map(item => item.stage), [
    'indexer-silentpay', 'satoshinet-pos-safety', 'satoshinet-contract-e2e',
    'sdk-assets', 'sdk-node-readiness', 'sdk-rgb-closed-loop',
    'pwa-canonical', 'pwa-fixed-account-usage', 'transcend-stp',
  ])
  assert.equal(plan.find(item => item.stage === 'pwa-canonical').pwaBrowserPort, 5189)
  assert.equal(plan.find(item => item.stage === 'satoshinet-contract-e2e').expectedTopLevelTests.length, 26)
  assert.ok(!plan.find(item => item.stage === 'pwa-canonical').expectedTopLevelTests.includes('TestSDKPWARealRegtestPrivateBTCRoundTripBrowser'))
  assert.ok(!plan.find(item => item.stage === 'pwa-canonical').expectedTopLevelTests.includes('TestSDKPWARealRegtestChannelActivationBrowser'))
  assert.equal(plan.find(item => item.stage === 'pwa-fixed-account-usage').expectedTopLevelTests[0], 'TestSDKPWARealRegtestFixedAccountManagementBrowser')
})

test('--only selects exact known stages in dependency order and rejects missing selections', () => {
  assert.deepEqual(selectStages(['sdk-rgb-closed-loop', 'indexer-silentpay']).map(stage => stage.id), [
    'indexer-silentpay', 'sdk-rgb-closed-loop',
  ])
  assert.throws(() => selectStages(['sdk-rbg-closed-loop']), /unknown stage ID/)
  assert.throws(() => selectStages([]), /at least one exact stage/)
  assert.throws(() => selectStages(['sdk-assets', 'sdk-assets']), /cannot repeat/)
  assert.deepEqual(parseArguments(['--only', 'sdk-assets', 'sdk-node-readiness']).stages.map(stage => stage.id), [
    'sdk-assets', 'sdk-node-readiness',
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

test('attach plan selects the existing-node readback and explicitly excludes owned lifecycle coverage', () => {
  const original = stageCatalog.find(stage => stage.id === 'sdk-node-readiness')
  const [attached] = configureStages([original], { SAT20_REAL_SATOSHINET_ATTACH: '/private/existing.json' })
  assert.deepEqual(attached.tests, ['TestSDKPWARealRegtestAttachedNodeReadback'])
  assert.match(attached.boundary, /same-DB restart not executed/)
  assert.deepEqual(original.tests, ['TestSDKPWARealRegtestNodeReadiness'])
  assert.deepEqual(configureStages([original], {})[0], original)
  const plan = buildPlan([attached], 5179)
  assert.equal(plan[0].boundary, attached.boundary)
  assert.ok(plan[0].command.some(arg => arg.includes('TestSDKPWARealRegtestAttachedNodeReadback')))
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

test('stage commands use effective GO and the isolated Transcend modfile without changing go.sum', () => {
  const dir = makePrivateDir()
  try {
    const modfile = join(dir, 'transcend-fixture.mod')
    writeFileSync(modfile, 'module fixture\n', { mode: 0o600 })
    writeFileSync(join(dir, 'transcend-fixture.sum'), '', { mode: 0o600 })
    const stage = stageCatalog.find(item => item.id === 'transcend-stp')
    const command = buildCommand(stage, { GO: '/private/toolchain/go', SAT20_TRANSCEND_E2E_MODFILE: modfile })
    assert.equal(command[0], '/private/toolchain/go')
    assert.ok(command.includes('-mod=readonly'))
    assert.equal(command.filter(arg => arg === '-mod=readonly').length, 1)
    assert.ok(command.includes(`-modfile=${modfile}`))
    assert.equal(command.at(-1), './stp')
    const defaultStageCommand = buildCommand(stage, { GO: '/private/toolchain/go' })
    assert.ok(!defaultStageCommand.some(arg => arg.startsWith('-modfile=')))
    assert.equal(defaultStageCommand.filter(arg => arg === '-mod=readonly').length, 1)
    for (const catalogStage of stageCatalog) {
      assert.equal(buildCommand(catalogStage, { GO: '/private/toolchain/go' }).filter(arg => arg === '-mod=readonly').length,
        1, `${catalogStage.id} must protect the shared module files`)
    }
    const plan = buildPlan([stage], 5189, { GO: '/private/toolchain/go', SAT20_TRANSCEND_E2E_MODFILE: modfile })
    assert.equal(plan[0].command[0], '/private/toolchain/go')
    assert.ok(plan[0].command.includes(`-modfile=${modfile}`))
    assert.equal(plan[0].command.filter(arg => arg === '-mod=readonly').length, 1)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test('wrapper subprocess writes a private success summary and stops after the first failed stage', () => {
  const dir = makePrivateDir()
  try {
    const marker = join(dir, 'go-test-invocations.txt')
    const stubGo = join(dir, 'go-stub.mjs')
    const envFile = join(dir, 'e2e.env')
    writeFileSync(stubGo, `#!/usr/bin/env node
import { appendFileSync } from 'node:fs'
if (process.argv[2] === 'version') {
  process.stdout.write('go version go1.24.13 test-stub\\n')
  process.exit(0)
}
if (process.argv[2] !== 'test') process.exit(2)
appendFileSync(process.env.SAT20_TEST_STUB_MARKER, 'test\\n')
process.stdout.write(process.env.SAT20_TEST_STUB_JSON || '')
process.exit(Number(process.env.SAT20_TEST_STUB_EXIT || 0))
`, { mode: 0o700 })
    chmodSync(stubGo, 0o700)
    writeFileSync(envFile, `GO=${stubGo}\n`, { mode: 0o600 })
    const successJSON = [
      'TestSilentPayE2EReceiveLiveRecoverAndSpend',
      'TestSilentPayE2EReorgReloadRestoresLive',
      'TestSilentPayE2ECheckSelfDetectsCorruptionWithoutRepair',
      'TestSilentPayE2EDatabaseReopen',
      'TestSilentPayE2ESameBlockSpendAndFullySpentHistoryAudit',
      'TestSilentPayE2EBaseBufferedCloneSubtractCommit',
    ].map(Test => JSON.stringify({ Action: 'pass', Test })).join('\n') + '\n'
    const commonEnv = {
      PATH: process.env.PATH,
      SAT20_TEST_STUB_MARKER: marker,
      SAT20_TEST_STUB_JSON: successJSON,
      SAT20_TEST_STUB_EXIT: '0',
    }
    const success = spawnSync(process.execPath, [wrapperPath, '--only', 'indexer-silentpay', '--env-file', envFile], {
      cwd: workspaceRoot, env: commonEnv, encoding: 'utf8', timeout: 30000,
    })
    assert.equal(success.status, 0, success.stderr)
    const successSummary = JSON.parse(success.stdout.trim().split(/\r?\n/).at(-1))
    assert.equal(successSummary.status, 'pass')
    assert.equal(successSummary.executed[0].topLevelPassed, 6)
    const successReport = JSON.parse(readFileSync(successSummary.summary, 'utf8'))
    assert.equal(statSync(successSummary.runDir).mode & 0o777, 0o700)
    assert.equal(statSync(successSummary.summary).mode & 0o777, 0o600)
    assert.equal(successReport.executed.length, 1)
    assert.equal(readFileSync(marker, 'utf8').trim().split(/\r?\n/).length, 1)

    const subsetNames = ['TestSilentPayE2EReceiveLiveRecoverAndSpend', 'TestSilentPayE2EDatabaseReopen']
    const subset = spawnSync(process.execPath, [wrapperPath, '--only', 'indexer-silentpay', '--tests', ...subsetNames, '--env-file', envFile], {
      cwd: workspaceRoot,
      env: { ...commonEnv, SAT20_TEST_STUB_JSON: subsetNames.map(Test => JSON.stringify({ Action: 'pass', Test })).join('\n') + '\n' },
      encoding: 'utf8', timeout: 30000,
    })
    assert.equal(subset.status, 0, subset.stderr)
    const subsetSummary = JSON.parse(subset.stdout.trim().split(/\r?\n/).at(-1))
    const subsetReport = JSON.parse(readFileSync(subsetSummary.summary, 'utf8'))
    assert.equal(subsetReport.executed[0].tests.expected, 2)
    assert.equal(subsetReport.notSelectedTests.length, 4)
    assert.ok(subsetReport.notSelectedTests.every(item => !subsetNames.includes(item.test)))

    writeFileSync(marker, '', { mode: 0o600 })
    const failJSON = JSON.stringify({ Action: 'fail', Test: 'TestSilentPayE2EReceiveLiveRecoverAndSpend' }) + '\n'
    const failure = spawnSync(process.execPath, [wrapperPath, '--only', 'indexer-silentpay', 'satoshinet-pos-safety', '--env-file', envFile], {
      cwd: workspaceRoot,
      env: { ...commonEnv, SAT20_TEST_STUB_JSON: failJSON, SAT20_TEST_STUB_EXIT: '1' },
      encoding: 'utf8', timeout: 30000,
    })
    assert.equal(failure.status, 1, failure.stderr)
    const failureSummary = JSON.parse(failure.stdout.trim().split(/\r?\n/).at(-1))
    assert.equal(failureSummary.status, 'fail')
    const failureReport = JSON.parse(readFileSync(failureSummary.summary, 'utf8'))
    assert.equal(failureReport.executed.length, 1)
    assert.equal(failureReport.executed[0].stage, 'indexer-silentpay')
    assert.ok(failureReport.notRun.some(item => item.stage === 'satoshinet-pos-safety'))
    assert.equal(readFileSync(marker, 'utf8').trim().split(/\r?\n/).length, 1)
  } finally {
    rmSync(dir, { recursive: true, force: true })
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

test('interrupting the stage owner waits for its process group to stop', { skip: process.platform === 'win32' }, async () => {
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
