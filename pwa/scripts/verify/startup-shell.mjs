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
const bootShell = () => vm.runInNewContext(js + '\n({ fail: renderStartupError, finish: finishStartupShell });', { ...context })
const state = () => root.children[0].children[1]
const retry = () => root.children[0].children[2]
const fastBoot = bootShell()
assert.equal(root.children.length, 0, 'normal startup begins without an intermediate page')
fastBoot.finish()
assert.equal(timer, null, 'normal startup cancels delayed feedback')
assert.equal(root.children.length, 0, 'normal startup shows no intermediate page')
const slowBoot = bootShell()
timer(); assert.equal(state().textContent, 'startup.slow', 'pending initialization remains visible')
assert.equal(state().attrs.role, 'status')
retry().listeners.click(); assert.equal(reloads, 1)
slowBoot.fail(new Error('fetch failed'))
assert.equal(state().textContent, 'startup.error'); assert.equal(state().attrs.role, 'alert')
assert.equal(cleared, 2)
retry().listeners.click(); assert.equal(reloads, 2, 'failed startup supports a fresh-page retry')
slowBoot.finish(); assert.equal(cleared, 2)
assert.ok(!shellCode.includes('caches.delete') && !shellCode.includes('unregister') && !shellCode.includes('setValue'))
assert.ok(main.includes("finishStartupShell()\n  app.mount('#app')"), 'successful mount clears pending feedback timer')

// Execute the App's actual async status lifecycle: only successful readiness
// reveals RouterView; failure remains in its interactive fallback.
const app = readFileSync('entrypoints/popup/App.vue', 'utf8')
assert.ok(app.includes('v-else-if="startupSlow || startupError"') && app.includes('@click="retryStartup"'))
const hook = app.slice(app.indexOf('onBeforeMount(async () => {'), app.indexOf('\nonMounted(() => {'))
for (const { fail, slow } of [{ fail: false, slow: false }, { fail: false, slow: true }, { fail: true, slow: false }]) {
  let hookFn, showSlow
  const loading = { value: true }, startupError = { value: false }, startupSlow = { value: false }
  vm.runInNewContext(hook, {
    onBeforeMount: fn => { hookFn = fn }, loading, startupError, startupSlow,
    setTimeout: fn => { showSlow = fn; return 1 }, clearTimeout: () => { showSlow = null },
    getWalletStatus: async () => { if (fail) throw new Error('status unavailable') },
    accountManagementStore: { start() {} },
    console: { error() {} },
  })
  assert.equal(loading.value, true)
  const boot = hookFn()
  assert.equal(startupSlow.value, false)
  if (slow) showSlow()
  assert.equal(startupSlow.value, slow)
  await boot
  assert.equal(showSlow, null)
  assert.equal(loading.value, fail)
  assert.equal(startupError.value, fail)
}
console.log('startup shell: PASS (fast startup, delayed feedback, immediate error, retry, App readiness)')
