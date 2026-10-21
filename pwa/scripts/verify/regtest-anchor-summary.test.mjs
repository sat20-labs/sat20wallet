import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

// Exercise the runner's actual selection expression without launching Vite,
// Chromium, or a wallet. These two amounts coexist in the original live reply.
const source = readFileSync(new URL('./account-management-e2e.mjs', import.meta.url), 'utf8')
const expression = source.match(/const credited = l2Summary\.data\.find\(([\s\S]*?)\)\s*\n\s*assert\.ok\(credited/)
assert.ok(expression, 'the real anchor-summary selection must be exercised')
const select = new Function('l2Summary', 'anchor', 'protocol', 'type', 'ticker', `return l2Summary.data.find(${expression[1]})`)
const rows = [
  { Name: { Protocol: '', Type: '*', Ticker: '' }, Amount: '706999' },
  { Name: { Protocol: '', Type: '', Ticker: '' }, Amount: '706150' },
  { Name: { Protocol: 'runes', Type: 'f', Ticker: 'SAT•ACCEPTANCE•PURE' }, Amount: '75.32' },
]
const find = (assetKey, data = rows) => {
  const [protocol, type, ...ticker] = assetKey.split(':')
  return select({ data }, { l2Credit: { assetKey } }, protocol, type, ticker.join(':'))
}

test('anchor provenance compares SDK plain sats with plain sats, even when total sats is first', () => {
  assert.equal(find('::')?.Amount, '706150')
  assert.equal(find('::', [...rows].reverse())?.Amount, '706150')
  assert.equal(find('::', [rows[0]]), undefined, 'missing plain amount cannot fall back to all sats')
})

test('an explicit all-sats key retains the total amount', () => {
  assert.equal(find(':*:'), rows[0])
})

test('named assets retain exact protocol, type, and ticker matching', () => {
  assert.equal(find('runes:f:SAT•ACCEPTANCE•PURE'), rows[2])
  assert.equal(find('brc20:f:SAT•ACCEPTANCE•PURE'), undefined)
  assert.equal(find('runes:o:SAT•ACCEPTANCE•PURE'), undefined)
  assert.equal(find('runes:f:SAT•ACCEPTANCE•MINT'), undefined)
})
