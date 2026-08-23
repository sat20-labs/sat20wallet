import assert from 'node:assert/strict'
import { assetContextKey, isSameAssetContext } from '../../lib/assetContext.ts'

const base = {
  env: 'test',
  network: 'testnet',
  chain: 'btc',
  walletId: 'wallet-2',
  accountIndex: 2,
  address: 'tb1q-current',
}

assert.equal(assetContextKey(base), assetContextKey({ ...base }))
for (const [field, value] of [
  ['env', 'prod'],
  ['network', 'mainnet'],
  ['chain', 'satnet'],
  ['walletId', 'wallet-3'],
  ['accountIndex', 3],
  ['address', 'tb1q-other'],
]) {
  assert.notEqual(assetContextKey(base), assetContextKey({ ...base, [field]: value }))
}
assert.equal(isSameAssetContext(base, { ...base }), true)
assert.equal(isSameAssetContext(base, { ...base, accountIndex: 1 }), false)
assert.equal(isSameAssetContext(base, null), false)

console.log('asset context isolation tests passed')
