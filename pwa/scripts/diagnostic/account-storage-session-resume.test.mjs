import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = relative => readFileSync(new URL(relative, import.meta.url), 'utf8')

test('account storage authorization is owned by SDK, not PWA state', () => {
  const sdk = source('../../utils/accountManagement.ts')
  const page = source('../../entrypoints/popup/pages/wallet/settings/account-management/Index.vue')
  const wasm = source('../../../sdk/wasm/account_management.go')
  const wallet = source('../../../sdk/wallet/account_pwa.go')

  assert.doesNotMatch(sdk, /pendingStorageAuthorization/)
  assert.doesNotMatch(page, /storage_authorization_id/)
  assert.doesNotMatch(page, /const\s+storageAuthorization\s*=/)
  assert.match(page, /resumePendingStorageAuthorization\(\)/)
  assert.match(wasm, /PendingAccountStorageAuthorization\(\)/)
  assert.match(wasm, /CancelPendingAccountStorageAuthorization\(\)/)
  assert.doesNotMatch(wasm, /accountSessions\.storage/)
  assert.match(wallet, /PendingAccountStorageAuthorization/)
  assert.match(wallet, /CancelPendingAccountStorageAuthorization/)
  assert.match(wallet, /ReusePaidAccountStorage/)
  assert.doesNotMatch(wallet, /func \(p \*Manager\) ReusePaidAccountStorage[\s\S]{0,2200}fundAccountAutopayWithWallet/)
})
