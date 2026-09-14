import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'

const main = readFileSync('main.ts', 'utf8')
const shellCode = main.slice(main.indexOf('let startupSlowTimer:'), main.indexOf("window.addEventListener('sat20:wasm-runtime-error'"))
const js = ts.transpileModule(shellCode, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
function node(tag) {
  return { tag, children: [], attrs: {}, style: {}, listeners: {}, textContent: '',
    append(...children) { this.children.push(...children) },
    replaceChildren(...children) { this.children = children },
    setAttribute(key, value) { this.attrs[key] = value },
    addEventListener(key, fn) { this.listeners[key] = fn },
  }
}
const root = node('div'); let reloads = 0, timer, cleared = 0
const context = {
  console: { error() {} },
  document: { getElementById: id => id === 'app' ? root : null, createElement: node },
  window: { location: { reload: () => reloads++ } },
  i18n: { global: { t: key => key } },
  setTimeout(fn) { timer = fn; return 1 }, clearTimeout() { cleared++; timer = null },
}
vm.runInNewContext(js + '\nglobalThis.fail = renderStartupError; globalThis.finish = finishStartupShell;', context)
const state = () => root.children[0].children[1]
const retry = () => root.children[0].children[2]
assert.equal(state().textContent, 'startup.loading', 'shell is visible before runtime resolves')
assert.equal(state().attrs.role, 'status')
retry().listeners.click(); assert.equal(reloads, 1)
timer(); assert.equal(state().textContent, 'startup.slow', 'pending initialization remains visible')
context.fail(new Error('fetch failed'))
assert.equal(state().textContent, 'startup.error'); assert.equal(state().attrs.role, 'alert')
assert.equal(cleared, 1)
retry().listeners.click(); assert.equal(reloads, 2, 'failed startup supports a fresh-page retry')
context.finish(); assert.equal(cleared, 1)
assert.ok(!shellCode.includes('caches.delete') && !shellCode.includes('unregister') && !shellCode.includes('setValue'))
assert.ok(main.indexOf("renderStartupStatus('loading')") < main.indexOf('return loadWasm()'))
assert.ok(main.includes("finishStartupShell()\n  app.mount('#app')"), 'successful mount clears pending feedback timer')

// Execute the App's actual async status lifecycle: only successful readiness
// reveals RouterView; failure remains in its interactive fallback.
const app = readFileSync('entrypoints/popup/App.vue', 'utf8')
assert.ok(app.includes('<main v-else') && app.includes('@click="retryStartup"'))
const hook = app.slice(app.indexOf('onBeforeMount(async () => {'), app.indexOf('\nonMounted(() => {'))
for (const fail of [false, true]) {
  let hookFn
  const loading = { value: true }, startupError = { value: false }
  vm.runInNewContext(hook, {
    onBeforeMount: fn => { hookFn = fn }, loading, startupError,
    getWalletStatus: async () => { if (fail) throw new Error('status unavailable') },
    console: { error() {} },
  })
  assert.equal(loading.value, true)
  await hookFn()
  assert.equal(loading.value, fail)
  assert.equal(startupError.value, fail)
}
console.log('startup shell: PASS (initial/pending/error/retry, no storage reset, successful mount cleanup, App readiness/error)')
