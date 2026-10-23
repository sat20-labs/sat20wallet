import test from 'node:test'
import assert from 'node:assert/strict'
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { checkLocalRealDependencies, discardLocalRealRegtestData, startLocalRealRegtest } from './local-real-regtest.mjs'

const workspaceRoot = new URL('../../../..', import.meta.url).pathname

function privateDir() {
  const directory = mkdtempSync('/private/tmp/local-real-regtest-test-')
  chmodSync(directory, 0o700)
  return directory
}

function writeExecutable(path, body) {
  writeFileSync(path, `#!/bin/sh\n${body}\n`, { mode: 0o700 })
  chmodSync(path, 0o700)
}

test('dependency check accepts Bitcoin Core 31.0 and Blockstream Electrs HTTP CLI', t => {
  const directory = privateDir()
  t.after(() => rmSync(directory, { recursive: true, force: true }))
  const bitcoind = join(directory, 'bitcoind')
  const bitcoinCli = join(directory, 'bitcoin-cli')
  const electrs = join(directory, 'electrs')
  const go = join(directory, 'go')
  writeExecutable(bitcoind, "printf 'Bitcoin Core version v31.0.0\\n'")
  writeExecutable(bitcoinCli, "printf 'Bitcoin Core RPC client version v31.0.0\\n'")
  writeExecutable(electrs, "printf '%s\\n' '--network --jsonrpc-import --daemon-dir --daemon-rpc-addr --db-dir --http-addr --electrum-rpc-addr --monitoring-addr'")
  writeExecutable(go, "printf 'go version go1.24.0\\n'")

  const dependencies = checkLocalRealDependencies({
    workspaceRoot,
    env: {
      PATH: '', GO: go, SAT20_REAL_BITCOIND: bitcoind,
      SAT20_REAL_BITCOIN_CLI: bitcoinCli, SAT20_REAL_ELECTRS: electrs,
    },
  })
  assert.equal(dependencies.bitcoinCoreVersion, '31.0')
  assert.equal(dependencies.electrsHTTP, true)
  assert.equal(dependencies.bitcoind, bitcoind)
  assert.equal(dependencies.bitcoinCli, bitcoinCli)
  assert.equal(dependencies.electrs, electrs)
  assert.match(dependencies.initialMiningAddress, /^bcrt1p[0-9a-z]+$/)
  const wallets = JSON.parse(readFileSync(join(workspaceRoot, 'sat20wallet/sdk/e2e/testdata/regtest/wallets.json'), 'utf8'))
  const ordinary = wallets.find(item => item.role === 'ordinary-1')
  assert.equal(dependencies.initialMiningAddress, ordinary.mining_address)
  assert.notEqual(dependencies.initialMiningAddress, ordinary.address)
})

test('dependency check rejects vanilla Electrs without the Esplora HTTP option', t => {
  const directory = privateDir()
  t.after(() => rmSync(directory, { recursive: true, force: true }))
  const bitcoind = join(directory, 'bitcoind')
  const bitcoinCli = join(directory, 'bitcoin-cli')
  const electrs = join(directory, 'electrs')
  const go = join(directory, 'go')
  writeExecutable(bitcoind, "printf 'Bitcoin Core version v31.0.0\\n'")
  writeExecutable(bitcoinCli, "printf 'Bitcoin Core RPC client version v31.0.0\\n'")
  writeExecutable(electrs, "printf '%s\\n' '--network --jsonrpc-import --daemon-dir --daemon-rpc-addr --db-dir --electrum-rpc-addr --monitoring-addr'")
  writeExecutable(go, "printf 'go version go1.24.0\\n'")

  assert.throws(() => checkLocalRealDependencies({
    workspaceRoot,
    env: {
      PATH: '', GO: go, SAT20_REAL_BITCOIND: bitcoind,
      SAT20_REAL_BITCOIN_CLI: bitcoinCli, SAT20_REAL_ELECTRS: electrs,
    },
  }), /lacks required --http-addr/)
})

test('start rejects a non-empty run directory before checking or starting dependencies', async t => {
  const directory = privateDir()
  t.after(() => rmSync(directory, { recursive: true, force: true }))
  writeFileSync(join(directory, 'existing.txt'), 'preserve')
  await assert.rejects(startLocalRealRegtest({ directory, workspaceRoot, env: {} }), /must be newly created or empty/)
})

test('start rejects runtime directories outside private tmp before resolving toolchains', async () => {
  await assert.rejects(startLocalRealRegtest({ directory: workspaceRoot, workspaceRoot, env: {} }), /below the system temporary directory or \/private\/tmp/)
})

test('discard removes only a closed group data set and preserves logs, manifest, and shared artifacts', () => {
  const base = privateDir()
  try {
    const runDirectory = join(base, 'group')
    const artifactsDirectory = join(base, 'artifacts')
    mkdirSync(runDirectory, { mode: 0o700 })
    mkdirSync(artifactsDirectory, { mode: 0o700 })
    const dataDirectories = ['bitcoin', 'indexer', 'electrs', 'runtime'].map(name => {
      const path = join(runDirectory, name)
      mkdirSync(path, { mode: 0o700 })
      writeFileSync(join(path, 'state.dat'), 'group-owned')
      return path
    })
    const walletFixture = join(runDirectory, 'wallets.json')
    writeFileSync(walletFixture, '{"synthetic":true}', { mode: 0o600 })
    const logsDirectory = join(runDirectory, 'logs')
    mkdirSync(logsDirectory, { mode: 0o700 })
    const logPath = join(logsDirectory, 'bitcoind.log')
    writeFileSync(logPath, 'preserve-log')
    const nodeDirectory = join(runDirectory, 'runtime', 'nodes-anchor', 'core')
    mkdirSync(join(nodeDirectory, 'data', 'logs', 'regtest'), { recursive: true, mode: 0o700 })
    mkdirSync(join(nodeDirectory, 'data', 'db'), { mode: 0o700 })
    writeFileSync(join(nodeDirectory, 'btcd.stdout.log'), 'preserve-node-stdout')
    writeFileSync(join(nodeDirectory, 'data', 'logs', 'regtest', 'stpd.log'), 'preserve-stp-log')
    writeFileSync(join(nodeDirectory, 'data', 'db', '000009.log'), 'remove-database-journal')
    const browserDirectory = join(runDirectory, 'runtime', 'pwa-browser-evidence')
    mkdirSync(browserDirectory, { mode: 0o700 })
    writeFileSync(join(browserDirectory, 'pwa-wallet.jsonl'), 'preserve-browser-log')
    const artifactPath = join(artifactsDirectory, 'sat20-l1-indexer')
    writeFileSync(artifactPath, 'preserve-shared-binary')
    const manifestPath = join(runDirectory, 'manifest.json')
    const manifest = { dataRemovedAt: null }
    writeFileSync(manifestPath, JSON.stringify(manifest), { mode: 0o600 })
    let persistCalls = 0

    assert.equal(discardLocalRealRegtestData({
      runDirectory, dataDirectories, walletFixture, processes: [{ child: { exitCode: 0, signalCode: null } }],
      closed: true, manifest, persist: () => {
        persistCalls++
        writeFileSync(manifestPath, JSON.stringify(manifest))
      },
    }), true)

    assert.ok(dataDirectories.every(path => !existsSync(path)))
    assert.equal(existsSync(walletFixture), false)
    assert.equal(readFileSync(logPath, 'utf8'), 'preserve-log')
    const retained = join(logsDirectory, 'retained', 'runtime')
    assert.equal(readFileSync(join(retained, 'nodes-anchor', 'core', 'btcd.stdout.log'), 'utf8'), 'preserve-node-stdout')
    assert.equal(readFileSync(join(retained, 'nodes-anchor', 'core', 'data', 'logs', 'regtest', 'stpd.log'), 'utf8'), 'preserve-stp-log')
    assert.equal(existsSync(join(retained, 'nodes-anchor', 'core', 'data', 'db', '000009.log')), false)
    assert.equal(readFileSync(join(retained, 'pwa-browser-evidence', 'pwa-wallet.jsonl'), 'utf8'), 'preserve-browser-log')
    assert.equal(readFileSync(artifactPath, 'utf8'), 'preserve-shared-binary')
    assert.ok(manifest.dataRemovedAt)
    assert.equal(JSON.parse(readFileSync(manifestPath, 'utf8')).dataRemovedAt, manifest.dataRemovedAt)
    assert.equal(persistCalls, 1)
    assert.equal(discardLocalRealRegtestData({
      runDirectory, dataDirectories, walletFixture, processes: [], closed: true, manifest, persist: () => assert.fail('already discarded'),
    }), false)
  } finally {
    rmSync(base, { recursive: true, force: true })
  }
})

test('discard preserves all data until the group is closed and every owned process has exited', () => {
  const base = privateDir()
  try {
    const runDirectory = join(base, 'group')
    mkdirSync(runDirectory, { mode: 0o700 })
    const dataDirectories = ['bitcoin', 'indexer', 'electrs', 'runtime'].map(name => {
      const path = join(runDirectory, name)
      mkdirSync(path, { mode: 0o700 })
      return path
    })
    const walletFixture = join(runDirectory, 'wallets.json')
    writeFileSync(walletFixture, '{}', { mode: 0o600 })
    const manifest = { dataRemovedAt: null }
    const args = {
      runDirectory, dataDirectories, walletFixture, processes: [{ child: { exitCode: null, signalCode: null } }],
      manifest, persist: () => assert.fail('must not persist a failed discard'),
    }

    assert.throws(() => discardLocalRealRegtestData({ ...args, closed: false }), /close must complete/)
    assert.throws(() => discardLocalRealRegtestData({ ...args, closed: true }), /owned local regtest children must exit/)
    assert.ok(dataDirectories.every(existsSync))
    assert.equal(existsSync(walletFixture), true)
    assert.equal(manifest.dataRemovedAt, null)
  } finally {
    rmSync(base, { recursive: true, force: true })
  }
})
