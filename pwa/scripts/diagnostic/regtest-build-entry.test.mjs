import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const pwa = fileURLToPath(new URL('../../', import.meta.url))

test('regtest build stops on a real TypeScript error', t => {
  const outputDir = mkdtempSync(join(tmpdir(), 'pwa-regtest-build-'))
  t.after(() => rmSync(outputDir, { recursive: true, force: true }))
  const inputDir = mkdtempSync(join(pwa, 'regtest-typecheck-'))
  t.after(() => rmSync(inputDir, { recursive: true, force: true }))
  writeFileSync(join(inputDir, 'invalid-assignment.ts'), "export const value: number = 'invalid'\n")

  const statusDir = join(outputDir, 'status')
  const result = spawnSync('npm', ['run', 'build:regtest'], {
    cwd: pwa,
    env: {
      ...process.env,
      SAT20_PWA_BUILD_DIR: outputDir,
      SAT20_PWA_BUILD_STATUS_DIR: statusDir,
    },
    encoding: 'utf8',
    timeout: 300000,
  })
  assert.ifError(result.error)
  const output = result.stdout + result.stderr
  assert.equal(result.status, 1, output)
  assert.match(output, /regtest-typecheck-[^/\s]+[/\\]invalid-assignment\.ts.*error TS2322/)
  assert.doesNotMatch(output, /\] vite production build/)
  assert.ok(existsSync(join(outputDir, 'public', 'wasm', 'sat20wallet.wasm')))
  assert.ok(existsSync(join(outputDir, 'public', 'integrity-manifest.json')))
  assert.deepEqual(readdirSync(join(outputDir, 'dist')), [])

  const status = JSON.parse(readFileSync(join(statusDir, 'latest.json'), 'utf8'))
  assert.equal(status.profile, 'regtest')
  assert.equal(status.status, 'FAILED')
  assert.match(status.error, /^typecheck failed with exit code [1-9]\d*$/)
})
