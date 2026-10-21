// Uses the existing diagnostic AST-callback approach; no browser or wallet writes.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
const text = readFileSync(new URL('../../components/asset/L1AssetsTabs.vue', import.meta.url), 'utf8')
const script = text.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
const ast = ts.createSourceFile('tabs.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const source = new Map()
ast.forEachChild(node => {
  if (ts.isVariableStatement(node)) for (const d of node.declarationList.declarations) {
    source.set(d.name.getText(ast), d.initializer?.getText(ast))
  }
})
const bind = (name, deps) => {
  const js = ts.transpileModule(`const callback = ${source.get(name)}`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText
  return Function(...Object.keys(deps), `${js}; return callback`)(...Object.values(deps))
}
const fixture = (type, pauseAt = '') => {
  const calls = [], state = { consistency_status: 'warning' }
  let active = true
  const check = () => { if (!active) throw new Error('session changed') }
  const endAt = point => { if (pauseAt === point) active = false }
  const deps = {
    selectedType: { value: type },
    walletManager: {
      refreshRGB11State: async () => { calls.push('chain-refresh'); endAt('chain-refresh'); return [undefined, {}] },
      getRGB11State: async () => { calls.push('state-read'); endAt('state-read'); return [undefined, { state: JSON.stringify({ consistency_status: 'ok' }) }] },
    },
    rgb11Store: { setState: next => { calls.push('state-write'); Object.assign(state, next) }, setError: error => calls.push(`error:${error}`) },
    emit: event => calls.push(`emit:${event}`),
    t: key => key,
    syncDirectMailboxOnce: async () => { calls.push('mailbox'); endAt('mailbox'); return 0 },
    startDirectMailboxRetry: () => calls.push('mailbox-retry'),
    walletRequestSessionGuard: () => { check(); return check },
    console: { log() {} },
  }
  deps.reloadRGB11TaskState = bind('reloadRGB11TaskState', deps)
  deps.refreshRGB11TaskState = bind('refreshRGB11TaskState', deps)
  return { run: bind('handlerRefresh', deps), calls, state }
}
test('RGB refresh button reconciles the current chain before presenting refreshed balances', async () => {
  const f = fixture('RGB11')
  await f.run()
  assert.equal(f.calls.filter(call => call === 'chain-refresh').length, 1,
    'the actual Vue refresh callback must dispatch the existing SDK chain refresh once')
  assert.equal(f.state.consistency_status, 'ok')
  assert.ok(f.calls.indexOf('chain-refresh') < f.calls.indexOf('state-read'))
})
test('ORDX refresh keeps its existing path without an RGB chain operation', async () => {
  const f = fixture('ORDX')
  await f.run()
  assert.deepEqual(f.calls, ['emit:refresh'])
  assert.equal(f.state.consistency_status, 'warning')
})

for (const point of ['mailbox', 'chain-refresh', 'state-read']) {
  test(`RGB refresh stops the old session after ${point}`, async () => {
    const f = fixture('RGB11', point)
    await f.run()
    assert.equal(f.calls.includes('state-write'), false)
    assert.equal(f.calls.includes('emit:refresh'), false)
    assert.equal(f.calls.some(call => call.startsWith('error:')), false)
    assert.equal(f.calls.filter(call => call === 'chain-refresh').length, point === 'mailbox' ? 0 : 1)
    assert.equal(f.calls.filter(call => call === 'state-read').length, point === 'state-read' ? 1 : 0)
    assert.equal(f.state.consistency_status, 'warning')
  })
}
