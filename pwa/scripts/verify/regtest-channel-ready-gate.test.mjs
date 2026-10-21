import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

// Exercise the runner's actual admission expression before spending a full
// WASM/browser run. This does not replace the real READY reload UI case.
const runner = readFileSync(new URL('./account-management-e2e.mjs', import.meta.url), 'utf8')
const start = runner.indexOf('new Set([...requiredPwaWalletBasicCases')
const end = runner.indexOf('\n  assert.ok(focusedWalletCases.every', start)
assert.ok(start >= 0 && end > start)
const expression = runner.slice(start, end).trim()
const names = [...new Set(expression.match(/requiredPwa\w+Cases/g))]
const posSource = readFileSync(new URL('./pos-pwa-e2e.mjs', import.meta.url), 'utf8')
const posArray = posSource.match(/export const requiredPwaPosCases = (\[[\s\S]*?\n\])/)[1]
const pos = Function(`return ${posArray}`)()
const admitted = Function('regtestWalletFixture', ...names, `return ${expression}`)
const cases = fixture => admitted(fixture, ...names.map(name => name === 'requiredPwaPosCases' ? pos : []))

test('retained READY UI gate requires both a real channel and the real Bitcoin controller', () => {
  const name = 'POS PWA: existing READY channel survives page reload without a new funding'
  assert.equal(pos[25], name)
  assert.ok(cases({ realChannel: true, realBitcoinControl: true }).has(name))
  for (const fixture of [{}, { realChannel: true }, { realBitcoinControl: true }]) {
    assert.ok(!cases(fixture).has(name), 'missing real prerequisites must remain rejected')
  }
  assert.ok(cases({ realChannel: true }).has(pos[1]), 'the existing channel gate must be unchanged')
})
