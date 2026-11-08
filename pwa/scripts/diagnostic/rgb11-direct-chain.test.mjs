import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = relative => readFileSync(new URL(relative, import.meta.url), 'utf8')

test('Direct lifecycle authority stays in SDK reservations', () => {
  const tabs = source('../../components/asset/L1AssetsTabs.vue')
  const facade = source('../../utils/rgb11Address.ts')
  const dkvs = source('../../../sdk/wallet/rgb11_dkvs.go')
  const reservations = source('../../../sdk/wallet/rgb11_transfer_reservation.go')
  const broadcast = source('../../../sdk/wallet/rgb11_broadcast.go')

  for (const forbidden of [
    'directAutoTried',
    'canResumeDirect',
    'directAttemptKey',
    'needsDirectResume',
    'resumeRGB11Task(task, true)',
    'beforeDispatch',
  ]) {
    assert.equal((tabs + facade).includes(forbidden), false, `PWA still owns Direct state: ${forbidden}`)
  }

  assert.match(tabs, /syncDirectMailboxOnce\(false\)/)
  for (const required of [
    'resumeReadyRGB11AddressTransfers()',
    'p.continueRGB11AddressSend(state.TransferID)',
    'result, err := p.continueRGB11AddressSend(transferID)',
    'p.scopeStates.sendFlights.Do(key, func() (any, error)',
    'pending, batch, err := p.readRGB11AddressBatch(transferID)',
    'txID, err := p.broadcastRGB11PendingBatch(batch, nil)',
  ]) assert.ok(dkvs.includes(required), `SDK Direct recovery path missing ${required}`)
  assert.match(reservations, /State\s+\*rgb11wallet\.TransferState/)
  assert.match(broadcast, /rgb11StatusBroadcastAttempted/)
})

test('Direct mailbox sync owns its mailbox subscription', () => {
  const dkvs = source('../../../sdk/wallet/rgb11_dkvs.go')
  assert.match(dkvs, /mailboxSubscriptionTarget\(accountID\)/)
  assert.match(dkvs, /owner\.SubscribeDKVSPrefix\(mailboxTarget\)/)
  assert.match(dkvs, /ensureCurrentSubscription\(store\.client\)/)
})
