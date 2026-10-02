import assert from 'node:assert/strict'
import { openExternalWindow } from '../../lib/externalNavigation.ts'

const calls = []
openExternalWindow((...args) => {
  calls.push(args)
  return {}
}, 'https://example.com/history', '_blank', 'noopener,noreferrer', () => assert.fail('must not fall back'), undefined)
assert.deepEqual(calls, [['https://example.com/history', '_blank', 'noopener,noreferrer']])

const assigned = []
openExternalWindow(() => null, 'https://example.com/history', '_blank', 'noopener,noreferrer',
  (url) => assigned.push(url), undefined)
assert.deepEqual(assigned, [], 'noopener may return null after opening a new page; keep the wallet page')

const unprotectedFallbackAssignments = []
openExternalWindow(() => null, 'https://example.com/history', '_blank', '',
  (url) => unprotectedFallbackAssignments.push(url), undefined)
assert.deepEqual(unprotectedFallbackAssignments, ['https://example.com/history'])

const bridgeCalls = []
openExternalWindow(() => assert.fail('window.open must not run after bridge success'),
  'https://example.com/history', '_blank', 'noopener,noreferrer',
  () => assert.fail('must not fall back'), (...args) => {
    bridgeCalls.push(args)
    return {}
  })
assert.deepEqual(bridgeCalls, [['https://example.com/history', '_blank', 'noopener,noreferrer']])

const bridgeFallbackAssignments = []
openExternalWindow(() => null, 'https://example.com/history', '_blank', 'noopener,noreferrer',
  (url) => bridgeFallbackAssignments.push(url), () => { throw new Error('bridge unavailable') })
assert.deepEqual(bridgeFallbackAssignments, [])

assert.throws(() => openExternalWindow(() => ({}), 'javascript:alert(1)', '_blank', '',
  () => assert.fail('unsafe URL must not navigate'), undefined), /Unsupported external URL protocol/)

console.log('external navigation tests passed')
