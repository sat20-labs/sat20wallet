import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { parse, compileScript } from '@vue/compiler-sfc'
import * as Vue from 'vue'
import ts from 'typescript'

const source = readFileSync(new URL('../../components/wallet/WalletManager.vue', import.meta.url), 'utf8')
const compiled = compileScript(parse(source).descriptor, { id: 'wallet-manager-import-review' }).content
const js = ts.transpileModule(compiled, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText

function fixture({ readFailure = false, switchFailure = false, importFailure = false, cancel = false } = {}) {
  const calls = { imports: 0, switches: 0, reloads: 0, toasts: [] }
  const store = Vue.reactive({ wallets: [], walletId: 'saved-wallet', isSwitchingWallet: false,
    importWallet: async () => { calls.imports++; return [readFailure || importFailure ? new Error('catalog read failed') : undefined, importFailure ? undefined : { fingerprint: 'saved-identity' }] },
    switchWallet: async () => { calls.switches++; if (switchFailure) throw new Error('switch failed') },
  })
  const mocks = {
    vue: { ...Vue, onUnmounted() {} },
    'vue-router': { useRouter: () => ({ go() {} }) },
    pinia: { storeToRefs: Vue.toRefs },
    '@/store': { useWalletStore: () => store, useApproveStore: () => ({ isVisible: Vue.ref(false) }) },
    '@/components/ui/toast-new': { useToast: () => ({ toast: value => calls.toasts.push(value) }) },
    '@/lib/walletPasswordPrompt': { withWalletPassword: async action => cancel ? undefined : action('fixture-password') },
    '@/utils/pwaVersionPolicy': { assertWalletWriteAllowed() {} },
    '@/lib/utils': { sendAccountsChangedEvent() {} },
    '@vueuse/core': { useDebounceFn: action => action },
    '@/lib/credential-rate-limit': { CredentialAttemptLimiter: class {} },
  }
  const mod = { exports: {} }
  const window = { location: { hash: '', reload: () => calls.reloads++ } }
  new Function('require', 'module', 'exports', 'window', 'safeSetTimeout', js)(name => {
    if (name in mocks) return mocks[name]
    assert.ok(name.startsWith('@/') || name === '@iconify/vue', `unexpected dependency ${name}`)
    return {}
  }, mod, mod.exports, window, () => {})
  const ui = mod.exports.default.setup({}, { expose() {} })
  ui.showImportWalletDialog()
  ui.importMnemonic.value = 'test-only phrase'
  return { ui, calls, window }
}

for (const failure of ['readFailure', 'switchFailure']) {
  test(`manager committed import offers reload and refuses replay after ${failure}`, async () => {
    const { ui, calls, window } = fixture({ [failure]: true })
    await ui.importWallet()
    assert.equal(calls.imports, 1)
    assert.equal(ui.importCommitted?.value, true, 'SDK identity means import has committed')
    assert.equal(ui.committedReadError?.value, true)
    assert.equal(ui.importMnemonic.value, '', 'clear the submitted recovery material')
    assert.equal(ui.importError.value, '', 'do not label a saved wallet as a failed import')
    assert.equal(ui.isImportWalletDialogOpen.value, true)
    assert.equal(calls.switches, failure === 'readFailure' ? 0 : 1)
    await ui.importWallet()
    assert.equal(calls.imports, 1, 'the committed import must never be replayed')
    ui.showImportWalletDialog()
    await ui.importWallet()
    assert.equal(calls.imports, 1, 'reopening must not discard the pending recovery state')
    assert.ok(calls.toasts.some(value => /saved|imported/i.test(value.description) && value.variant !== 'destructive'))
    ui.reloadCommittedWallet()
    assert.equal(window.location.hash, '#/unlock')
    assert.equal(calls.reloads, 1)
  })
}

test('manager uncommitted import can be retried and cancellation does not save a wallet', async () => {
  const failed = fixture({ importFailure: true })
  await failed.ui.importWallet()
  assert.ok(failed.ui.importError.value)
  await failed.ui.importWallet()
  assert.equal(failed.calls.imports, 2)
  const cancelled = fixture({ cancel: true })
  await cancelled.ui.importWallet()
  assert.equal(cancelled.calls.imports, 0)
})

test('manager successful import closes and permits a separate new import', async () => {
  const { ui, calls } = fixture()
  await ui.importWallet()
  assert.equal(calls.imports, 1)
  assert.equal(calls.switches, 1)
  assert.equal(ui.isImportWalletDialogOpen.value, false)
  ui.showImportWalletDialog()
  ui.importMnemonic.value = 'another test-only phrase'
  await ui.importWallet()
  assert.equal(calls.imports, 2)
})

test('manager import template disables replay and exposes the reload action', () => {
  const template = source.slice(source.indexOf('<!-- Import Wallet Dialog -->'), source.indexOf('<!-- Delete Confirmation Dialog -->'))
  assert.match(template, /type="submit"[^>]*:disabled="isImporting \|\| importCommitted"/)
  assert.match(template, /v-model="importMnemonic"[^>]*:disabled="isImporting \|\| importCommitted"/)
  assert.match(template, /v-if="importCommitted && committedReadError"/)
  assert.match(template, /@click="reloadCommittedWallet"/)
})
