import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { parse, compileScript } from '@vue/compiler-sfc'
import * as Vue from 'vue'
import * as z from 'zod'
import ts from 'typescript'
import { createI18n } from 'vue-i18n'

const messages = Object.fromEntries(['en', 'zh'].map(locale => [locale,
  JSON.parse(readFileSync(new URL(`../../locales/${locale}.json`, import.meta.url), 'utf8')),
]))

const source = readFileSync(new URL('../../entrypoints/popup/pages/wallet/settings/password.vue', import.meta.url), 'utf8')
const compiled = compileScript(parse(source).descriptor, { id: 'password-change-navigation' }).content
const js = ts.transpileModule(compiled, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText

function fixture({ locale = 'en', change = async () => [undefined], cleanup = { success: true } } = {}) {
  const events = [], routes = [], notices = []
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: false, messages })
  const mocks = {
    vue: Vue,
    'vue-i18n': { useI18n: () => i18n.global },
    zod: z,
    'vue-router': { useRouter: () => ({ replace: async path => { events.push('navigate'); routes.push(path) } }) },
    'vee-validate': { useForm: () => ({ handleSubmit: action => action, resetForm: () => events.push('reset') }) },
    '@vee-validate/zod': { toTypedSchema: schema => schema },
    '@/store/wallet': { useWalletStore: () => ({}) },
    '@/utils/sat20': { default: { changePassword: async () => { events.push('change'); return change() } } },
    '@/utils/biometricCredentials': { biometricCredentialManager: { clearAllCredentials: async () => {
      events.push('cleanup')
      return cleanup
    } } },
    '@/components/ui/toast-new': { useToast: () => ({ toast: notice => { events.push('toast'); notices.push(notice) } }) },
  }
  const mod = { exports: {} }
  new Function('require', 'module', 'exports', js)(name => {
    if (name in mocks) return mocks[name]
    assert.ok(name.startsWith('@/components/'), `Unexpected dependency: ${name}`)
    return {}
  }, mod, mod.exports)
  const ui = mod.exports.default.setup({}, { expose() {} })
  return { ui, events, routes, notices }
}

const synthetic = { oldPassword: 'synthetic-old', newPassword: 'synthetic-new' }

for (const locale of ['en', 'zh']) {

test(`${locale}: successful password change leaves the form only after the wallet confirms success`, async () => {
  let finish
  const f = fixture({ locale, change: () => new Promise(resolve => { finish = resolve }) })
  const pending = f.ui.onSubmit(synthetic)
  assert.equal(f.ui.isLoading.value, true)
  assert.deepEqual(f.routes, [], 'do not close a pending credential operation')
  finish([undefined])
  await pending
  assert.deepEqual(f.routes, ['/wallet/setting'])
  assert.deepEqual(f.events, ['change', 'cleanup', 'toast', 'navigate', 'reset'])
  assert.equal(f.notices[0].variant, 'success')
  assert.equal(f.ui.isLoading.value, false)
})

test(`${locale}: a wallet refusal retains the password form and does not clear biometric credentials`, async () => {
  const f = fixture({ locale, change: async () => [new Error('synthetic refusal')] })
  await f.ui.onSubmit(synthetic)
  assert.deepEqual(f.routes, [])
  assert.deepEqual(f.events, ['change', 'toast', 'reset'])
  assert.equal(f.notices[0].variant, 'destructive')
  assert.equal(f.ui.isLoading.value, false)
})

test(`${locale}: a thrown wallet error also retains the form`, async () => {
  const f = fixture({ locale, change: async () => { throw new Error('synthetic failure') } })
  await f.ui.onSubmit(synthetic)
  assert.deepEqual(f.routes, [])
  assert.equal(f.notices[0].variant, 'destructive')
  assert.equal(f.ui.isLoading.value, false)
})

test(`${locale}: successful password change with a biometric cleanup warning still returns to settings`, async () => {
  const f = fixture({ locale, cleanup: { success: false, error: 'synthetic cleanup failure' } })
  await f.ui.onSubmit(synthetic)
  assert.deepEqual(f.routes, ['/wallet/setting'])
  assert.equal(f.notices[0].variant, 'info')
  assert.equal(f.notices[0].description,
    messages[locale].settingsUi.biometricClearFailed.replace('{error}', 'synthetic cleanup failure'))
})

}
