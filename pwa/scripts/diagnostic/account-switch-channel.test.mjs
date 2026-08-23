import assert from 'node:assert/strict'
import { awaitAccountChannelRefresh } from '../../lib/accountSwitchChannel.ts'

let completed = false
const pending = awaitAccountChannelRefresh(
  async () => {
    await Promise.resolve()
    completed = true
  },
  () => assert.fail('successful refresh must not report an error'),
)
assert.equal(completed, false)
await pending
assert.equal(completed, true)

const expected = new Error('local channel lookup failed')
let observed
await assert.doesNotReject(async () => {
  await awaitAccountChannelRefresh(
    async () => { throw expected },
    (error) => { observed = error },
  )
})
assert.equal(observed, expected)

console.log('account switch channel refresh tests passed')
