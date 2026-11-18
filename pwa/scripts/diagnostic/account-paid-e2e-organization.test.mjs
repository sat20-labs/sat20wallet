import assert from 'node:assert/strict'
import test from 'node:test'
import { runAutopayPageCase, autopayPageCase } from '../verify/autopay-page-e2e.mjs'

// Only orchestration/failure propagation and cleanup. The real runner's signed
// payment assertions remain in its browser/Go E2E; these mocks do not prove payment.
for (const mode of ['success', 'check-failure', 'action-failure']) {
  test(`current autopay page orchestration: ${mode}`, async () => {
    const failure = new Error('injected failure'), visited = [], cleanup = []
    const context = {
      async unroute(pattern, handler) { cleanup.push(['unroute', pattern, typeof handler]) },
      async close() { cleanup.push(['close']) },
    }
    const page = { context: () => context, async evaluate() { throw failure } }
    const helpers = { device: async () => page, async check(name, action) {
      visited.push(name)
      if (mode === 'check-failure') throw failure
      if (mode === 'action-failure') await action()
    } }
    const pending = runAutopayPageCase(helpers, {})
    if (mode === 'success') await pending
    else await assert.rejects(pending, error => error === failure)
    assert.deepEqual(visited, [autopayPageCase])
    assert.deepEqual(cleanup, [['unroute', '**/*', 'function'], ['close']])
  })
}
