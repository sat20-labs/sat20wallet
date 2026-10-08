import assert from 'node:assert/strict'
import test from 'node:test'
import { runRgbPaidCases } from '../verify/account-management-usage-e2e.mjs'

const steps = [
  'RGB PWA: funded root reuses real AUTOPAY through settings without another funding',
  'RGB PWA: same paid mode reconfiguration converges across cold devices and root recovery',
  'RGB PWA: AutopayFundingConfirmationMatchesEffectiveRate',
  'RGB PWA: submitted AUTOPAY funding survives timeout reload and repeated confirmation',
]

// Exercise the actual journey's orchestration. Business actions are deliberately
// not run: these tests prove failure propagation/cleanup, not payment success.
function runner(failedStep, routeFailure = false) {
  const visited = [], routes = new Set()
  let closed = 0
  const failure = new Error('injected paid step failure')
  const context = {
    async route(pattern, handler) {
      if (routeFailure) throw failure
      routes.add(handler)
    },
    async unroute(pattern, handler) { routes.delete(handler) },
    async close() { closed++; routes.clear() },
  }
  const helpers = {
    device: async () => ({ context: () => context }),
    async check(name) {
      visited.push(name)
      if (name === failedStep) throw failure
    },
  }
  return { helpers, visited, failure, routes, closed: () => closed }
}

for (const failedStep of steps) {
  test(`failed ${failedStep} stops dependent paid steps and closes its context`, async () => {
    const r = runner(failedStep)
    await assert.rejects(runRgbPaidCases(r.helpers, {}), error => error === r.failure)
    assert.deepEqual(r.visited, steps.slice(0, steps.indexOf(failedStep) + 1),
      'a dependent payment ran against state left by a failed predecessor')
    assert.equal(r.closed(), 1)
    assert.equal(r.routes.size, 0)
  })
}

test('route preparation failure also closes the allocated paid context', async () => {
  const r = runner(undefined, true)
  await assert.rejects(runRgbPaidCases(r.helpers, {}), error => error === r.failure)
  assert.deepEqual(r.visited, [])
  assert.equal(r.closed(), 1)
})

test('successful paid orchestration runs every existing ledger step in order', async () => {
  const r = runner()
  await runRgbPaidCases(r.helpers, {})
  assert.deepEqual(r.visited, steps)
  assert.equal(r.closed(), 1)
  assert.equal(r.routes.size, 0)
})
