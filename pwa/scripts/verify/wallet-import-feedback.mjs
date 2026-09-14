import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'

const source = readFileSync('components/wallet/WalletManager.vue', 'utf8')
const start = source.indexOf('const importWallet = async () => {')
const end = source.indexOf('const showEditNameDialog', start)
const code = ts.transpileModule(source.slice(start, end) + '\nglobalThis.run = importWallet;', {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None },
}).outputText
const policySource = readFileSync('utils/versionPolicy.ts', 'utf8').replaceAll('export ', '')
const policyContext = {}
vm.runInNewContext(ts.transpileModule(policySource + '\nglobalThis.Policy=VersionPolicy', {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText, policyContext)
function fixture() {
  const policy = new policyContext.Policy('0.1.38', '20260913T175820Z')
  let prompts = 0
  let authorize, finish
  const calls = []
  const context = {
    isImporting: { value: false }, importMnemonic: { value: 'approved phrase' },
    importError: { value: '' }, importStage: { value: '' }, isImportWalletDialogOpen: { value: true },
    assertWalletWriteAllowed: () => policy.assertAllowed(false),
    withWalletPassword: async operation => {
      prompts++
      const result = await new Promise(resolve => { authorize = resolve })
      return result ? operation('test-only password') : undefined
    },
    walletStore: {
      walletId: 'wallet-id',
      importWallet: async (mnemonic, _password, progress) => {
        calls.push(mnemonic); progress('validating'); progress('importing')
        return new Promise(resolve => { finish = resolve })
      },
      switchWallet: async () => {},
    },
    toast: () => {}, safeSetTimeout: () => {}, router: { go() {} }, sendAccountsChangedEvent() {}, wallets: { value: [] },
  }
  vm.runInNewContext(code, context)
  return { context, calls, policy, prompts: () => prompts, authorize: result => authorize(result), finish: result => finish(result) }
}
const settle = async () => { for (let i = 0; i < 8; i++) await Promise.resolve() }
const f = fixture(); const pending = f.context.run()
assert.equal(f.context.importStage.value, 'authorizing')
f.context.importMnemonic.value = 'edited after authorization started'
f.authorize(true); await settle()
assert.deepEqual(f.calls, ['approved phrase'], 'only the submitted phrase is imported')
assert.equal(f.context.importStage.value, 'importing')
await f.context.run(); assert.equal(f.calls.length, 1, 'pending import is not repeated')
f.finish([undefined, {}]); await pending
assert.equal(f.context.isImportWalletDialogOpen.value, false)
assert.equal(f.context.importMnemonic.value, '')
assert.equal(f.context.isImporting.value, false)
const cancelled = fixture(); const cancelledRun = cancelled.context.run(); cancelled.authorize(false); await cancelledRun
assert.equal(cancelled.calls.length, 0)
assert.equal(cancelled.context.importStage.value, 'authorizationCancelled')
const failed = fixture(); const failedRun = failed.context.run(); failed.authorize(true); await settle()
failed.finish([new Error('validation failed')]); await failedRun
assert.equal(failed.context.importError.value, 'validation failed')
assert.equal(failed.context.isImporting.value, false)
assert.equal(failed.context.isImportWalletDialogOpen.value, true)
const dialog = source.slice(source.indexOf('<Dialog :open="isImportWalletDialogOpen"'), source.indexOf('<!-- Delete Confirmation Dialog -->'))
assert.ok(dialog.includes('if (!isImporting) isImportWalletDialogOpen = value'))
assert.ok(dialog.includes('v-model="importMnemonic" :disabled="isImporting"'))
assert.ok(dialog.includes('role="status"') && dialog.includes('role="alert"'))
assert.ok(dialog.includes('@escape-key-down') && dialog.includes('@interact-outside'))
const store = readFileSync('store/wallet.ts', 'utf8')
for (const stage of ['validating', 'recovering', 'importing', 'catalog']) assert.ok(store.includes(`onProgress?.('${stage}')`))
console.log('wallet import feedback: PASS (immutable phrase, stages, cancel, failure, no duplicate dispatch, input/close guards)')

const blocked = fixture(); blocked.policy.blocked = true; await blocked.context.run()
assert.equal(blocked.prompts(), 0, 'blocked import must not start password unlock/logging')
assert.equal(blocked.calls.length, 0)
assert.match(blocked.context.importError.value, /update required/)
assert.equal(blocked.context.isImporting.value, false)
assert.equal(blocked.policy.active, 0, 'preflight does not create an in-flight operation')
const racing = fixture(); const raceRun = racing.context.run(); racing.policy.updating = true
racing.authorize(true); await raceRun
assert.equal(racing.calls.length, 0, 'policy changed during password prompt rejects before validation/import')
assert.match(racing.context.importError.value, /update required/)
console.log('Import version preflight and authorization race passed')
