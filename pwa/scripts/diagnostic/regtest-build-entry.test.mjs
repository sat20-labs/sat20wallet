import assert from 'node:assert/strict'
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'

const pwa = new URL('../../', import.meta.url)
function fixture() {
  const dir = mkdtempSync(join(tmpdir(), 'pwa-regtest-build-review-'))
  for (const path of ['scripts', 'public/wasm', 'node_modules/.bin', 'node_modules/vue-tsc/bin', 'node_modules/vite/bin']) mkdirSync(join(dir, path), { recursive: true })
  for (const path of ['package.json', 'scripts/build-with-status.cjs', 'scripts/write-version.cjs', 'scripts/write-integrity-manifest.cjs']) copyFileSync(new URL(path, pwa), join(dir, path))
  writeFileSync(join(dir, 'public/version.json'), JSON.stringify({ version: 'old', buildId: 'old' }))
  writeFileSync(join(dir, 'public/integrity-manifest.json'), JSON.stringify({ releaseId: 'stale', assets: [] }))
  writeFileSync(join(dir, 'public/wasm/sat20wallet.wasm'), 'new-WASM-fixture-bytes')
  writeFileSync(join(dir, 'public/wasm/wasm_exec.js'), '// runtime fixture')
  writeFileSync(join(dir, 'node_modules/vue-tsc/bin/vue-tsc.js'), `
    require('node:fs').appendFileSync('stages.txt', 'typecheck\\n')
    if (process.env.PWA_REVIEW_TYPECHECK_FAIL) process.exit(2)
  `)
  writeFileSync(join(dir, 'node_modules/vite/bin/vite.js'), `#!/usr/bin/env node
    const assert = require('node:assert/strict'), fs = require('node:fs'), crypto = require('node:crypto')
    const version = JSON.parse(fs.readFileSync('public/version.json'))
    const integrity = JSON.parse(fs.readFileSync('public/integrity-manifest.json'))
    assert.equal(integrity.releaseId, version.version + '-' + version.buildId, 'stale integrity release')
    for (const path of ['wasm/sat20wallet.wasm', 'wasm/wasm_exec.js']) {
      const bytes = fs.readFileSync('public/' + path), entry = integrity.assets.find(asset => asset.path === path)
      assert.ok(entry, 'missing manifest entry: ' + path)
      assert.equal(entry.size, bytes.length)
      assert.equal(entry.sha256, crypto.createHash('sha256').update(bytes).digest('hex'))
    }
    assert.deepEqual(process.argv.slice(2), ['build', '--mode', 'regtest'])
    assert.equal(fs.readFileSync('stages.txt', 'utf8'), 'typecheck\\n')
    fs.appendFileSync('stages.txt', 'vite-regtest\\n')
  `, { mode: 0o700 })
  symlinkSync('../vite/bin/vite.js', join(dir, 'node_modules/.bin/vite'))
  return dir
}

test('documented npm regtest entry regenerates stale integrity before typechecking and building', () => {
  const dir = fixture()
  try {
    const result = spawnSync('npm', ['run', 'build:regtest'], { cwd: dir, encoding: 'utf8', timeout: 30000 })
    assert.equal(result.status, 0, result.stdout + result.stderr)
    assert.equal(readFileSync(join(dir, 'stages.txt'), 'utf8'), 'typecheck\nvite-regtest\n')
    assert.equal(JSON.parse(readFileSync(join(dir, '.build-status/latest.json'))).profile, 'regtest')
  } finally { rmSync(dir, { recursive: true, force: true }) }
})

test('regtest entry stops before bundling when typechecking fails', () => {
  const dir = fixture()
  try {
    const result = spawnSync('npm', ['run', 'build:regtest'], { cwd: dir, env: { ...process.env, PWA_REVIEW_TYPECHECK_FAIL: '1' }, encoding: 'utf8', timeout: 30000 })
    assert.notEqual(result.status, 0)
    assert.ok(existsSync(join(dir, 'stages.txt')), 'the npm entry must run the typecheck before bundling')
    assert.equal(readFileSync(join(dir, 'stages.txt'), 'utf8'), 'typecheck\n')
    assert.equal(JSON.parse(readFileSync(join(dir, '.build-status/latest.json'))).status, 'FAILED')
  } finally { rmSync(dir, { recursive: true, force: true }) }
})
