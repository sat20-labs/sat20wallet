import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { transformSync } from 'esbuild'
import { ref, computed } from 'vue'

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

function actualRestorePage({ commitError, rehearsalOnly = false } = {}) {
  const page = readFileSync(process.env.SAT20_RESTORE_SOURCE || new URL('../../entrypoints/popup/pages/RestoreAccount.vue', import.meta.url), 'utf8')
  const script = page.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*$/gm, '')
  const calls = { commits: 0, aborted: [], reloads: [], timers: [], routes: [], guardianRequests: [], guardianResponses: [] }
  const lifecycle = { beforeUnmount: [], mounted: [] }
  let now = 0
  const context = {
    ref, computed,
    onBeforeUnmount: callback => lifecycle.beforeUnmount.push(callback),
    onMounted: callback => lifecycle.mounted.push(callback),
    useRoute: () => ({ query: rehearsalOnly ? { mode: 'rehearsal' } : {} }),
    useRouter: () => ({ push: async route => { calls.routes.push(route) } }),
    accountSDK: {
      async loadRecovery() { return { session_id: 'fresh-session', questions: [] } },
      async commitRecovery() { calls.commits++; if (commitError) throw new Error(commitError) },
      async abortSession(id) { calls.aborted.push(id) },
      async createGuardianRequest(id) { calls.guardianRequests.push(id); return { request: 'fixture-guardian-request' } },
      async consumeGuardianResponse(id, response) { calls.guardianResponses.push({ id, response }) },
    },
    setTimeout: (callback, delay = 0) => {
      calls.reloads.push(callback)
      calls.timers.push({ callback, due: now + delay })
    },
  }
  runInNewContext(transformSync(script + `\nglobalThis.page = { loadRecovery, commitRecovery, cancelRecovery, createGuardianRequest, acceptGuardianResponse, step, loaded, preview, locator, answers, userShare, guardianRequest, guardianResponse, knowledgeReady, userShareReady, guardianReady, password, confirmPassword, error }`, { loader: 'ts', target: 'es2022' }).code, context)
  const ui = context.page
  ui.step.value = 4
  ui.loaded.value = { session_id: 'test-session' }
  ui.preview.value = { wallets: [{ name: 'Wallet 1', account_count: 1, dids: [] }] }
  ui.locator.value = 'saved-public-locator'
  ui.answers.value = ['private-answer']
  for (const name of ['userShare', 'guardianRequest', 'guardianResponse', 'password', 'confirmPassword']) ui[name].value = 'local-fixture-material'
  for (const name of ['knowledgeReady', 'userShareReady', 'guardianReady']) ui[name].value = true
  return {
    ui, calls, lifecycle,
    advanceClock: duration => {
      const target = now + duration
      while (true) {
        const next = calls.timers.findIndex(timer => timer.due <= target)
        if (next < 0) break
        const [timer] = calls.timers.splice(next, 1)
        now = timer.due
        timer.callback()
      }
      now = target
      return now
    },
  }
}

test('invalid recovery session returns to loading and clears private material without committing again', async () => {
  const { ui, calls } = actualRestorePage({ commitError: 'recovery session expired' })
  await ui.commitRecovery()
  assert.equal(ui.step.value, 1)
  assert.equal(ui.loaded.value, null)
  assert.equal(ui.preview.value, null)
  assert.equal(ui.locator.value, 'saved-public-locator')
  assert.equal(ui.answers.value.length, 0)
  for (const name of ['userShare', 'guardianRequest', 'guardianResponse', 'password', 'confirmPassword']) assert.equal(ui[name].value, '', name)
  for (const name of ['knowledgeReady', 'userShareReady', 'guardianReady']) assert.equal(ui[name].value, false, name)
  assert.match(ui.error.value, /失效.*重新加载/)
  assert.equal(calls.commits, 1)
  assert.deepEqual(calls.aborted, ['test-session'])
  assert.equal(calls.reloads.length, 0)
  await ui.loadRecovery()
  assert.equal(ui.step.value, 2)
  assert.equal(ui.loaded.value.session_id, 'fresh-session')
  assert.equal(ui.locator.value, 'saved-public-locator')
  assert.equal(calls.commits, 1)
})

test('an unrelated recovery failure keeps the confirmed preview and reports the actual error', async () => {
  const { ui, calls } = actualRestorePage({ commitError: 'network unavailable' })
  await ui.commitRecovery()
  assert.equal(ui.step.value, 4)
  assert.ok(ui.loaded.value && ui.preview.value)
  assert.equal(ui.error.value, 'network unavailable')
  assert.equal(calls.commits, 1)
  assert.deepEqual(calls.aborted, [])
  assert.equal(calls.reloads.length, 0)
})

test('successful recovery clears credentials and schedules one reload without another commit', async () => {
  const { ui, calls } = actualRestorePage()
  await ui.commitRecovery()
  assert.equal(ui.step.value, 5)
  assert.equal(ui.loaded.value, null)
  assert.equal(ui.password.value, '')
  assert.equal(ui.confirmPassword.value, '')
  assert.equal(calls.commits, 1)
  assert.equal(calls.reloads.length, 1)
})

test('read-only recovery rehearsal still refuses a restore commit', async () => {
  const { ui, calls } = actualRestorePage({ rehearsalOnly: true })
  await ui.commitRecovery()
  assert.equal(calls.commits, 0)
  assert.match(ui.error.value, /只读演练不能提交恢复/)
})

test('recovery remains available after more than twenty minutes of page inactivity', async () => {
  const { ui, calls, advanceClock } = actualRestorePage()
  await ui.loadRecovery()
  assert.equal(ui.loaded.value.session_id, 'fresh-session')
  assert.equal(calls.timers.length, 0)
  advanceClock(25 * 60 * 1000)
  assert.equal(ui.loaded.value.session_id, 'fresh-session')
  ui.password.value = 'new-password'
  ui.confirmPassword.value = 'new-password'
  await ui.commitRecovery()
  assert.equal(calls.commits, 1)
  assert.equal(calls.reloads.length, 1)
  assert.deepEqual(calls.aborted, [])
})

test('Guardian request and response steps preserve the same recovery session', async () => {
  const { ui, calls } = actualRestorePage()
  ui.step.value = 3
  await ui.createGuardianRequest()
  assert.equal(ui.guardianRequest.value, 'fixture-guardian-request')
  ui.guardianResponse.value = 'fixture-guardian-response'
  await ui.acceptGuardianResponse()
  assert.equal(ui.loaded.value.session_id, 'test-session')
  assert.equal(ui.guardianReady.value, true)
  assert.deepEqual(calls.guardianRequests, ['test-session'])
  assert.deepEqual(calls.guardianResponses, [{ id: 'test-session', response: 'fixture-guardian-response' }])
  assert.deepEqual(calls.aborted, [])
})

test('cancel and page leave release a recovery session once and clear private material', async () => {
  const { ui, calls, lifecycle } = actualRestorePage()
  await ui.cancelRecovery()
  assert.equal(ui.step.value, 1)
  assert.equal(ui.loaded.value, null)
  assert.equal(ui.preview.value, null)
  assert.equal(ui.userShare.value, '')
  assert.equal(ui.guardianRequest.value, '')
  assert.equal(ui.guardianResponse.value, '')
  assert.deepEqual(calls.aborted, ['test-session'])
  assert.deepEqual(calls.routes, ['/'])
  lifecycle.beforeUnmount[0]()
  await Promise.resolve()
  assert.deepEqual(calls.aborted, ['test-session'])
})

test('component unmount releases the active session for route leave or lock exit', async () => {
  const { ui, calls, lifecycle } = actualRestorePage()
  lifecycle.beforeUnmount[0]()
  await Promise.resolve()
  assert.equal(ui.loaded.value, null)
  assert.equal(ui.preview.value, null)
  assert.equal(ui.password.value, '')
  assert.deepEqual(calls.aborted, ['test-session'])
})
