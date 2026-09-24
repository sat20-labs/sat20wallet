import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import ts from 'typescript'

const component = new URL('../../components/wallet/RGB11InvoiceDialog.vue', import.meta.url)

const componentInit = async (name) => {
  const source = await readFile(component, 'utf8')
  const script = source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)?.[1]
  assert.ok(script, 'script setup block missing')
  const file = ts.createSourceFile('dialog.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  let initializer = ''
  file.forEachChild((node) => {
    if (!ts.isVariableStatement(node)) return
    for (const declaration of node.declarationList.declarations) {
      if (declaration.name.getText(file) === name) initializer = declaration.initializer?.getText(file) || ''
    }
  })
  assert.ok(initializer, `${name} missing`)
  return initializer
}

const invoiceContractID = async (asset) => {
  const initializer = await componentInit('assetContractID')
  const js = ts.transpileModule(`globalThis.value = ${initializer}`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText
  const computed = (factory) => ({ value: factory() })
  return Function('props', 'computed', `${js}; return globalThis.value.value`)({ asset }, computed)
}

test('invoice uses only contract ID', async () => {
  const valid = 'rgb:z3e7NRle-UqSW2WZ-5LYn8wm-3xghXWw-9ZX6c9P-gTVSI4E'
  assert.equal(await invoiceContractID({ ticker: 'r2direct@gvml24je', contract_id: valid }), valid)
  assert.equal(await invoiceContractID({ ticker: 'r2direct@gvml24je' }), '')
})

test('missing ID blocks invoice call', async () => {
  const initializer = await componentInit('generateInvoice')
  const js = ts.transpileModule(`globalThis.generate = ${initializer}`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText
  let calls = 0
  const mocks = {
    restoring: { value: false }, restoreFailed: { value: false }, scopeKey: { value: 'scope' }, requestId: { value: '' },
    assetContractID: { value: '' }, errorMessage: { value: '' }, t: (key) => key,
    props: { asset: { ticker: 'r2direct@gvml24je', precision: 0 } }, amount: { value: '1' },
    decimalToRaw: () => '1', proxyEndpoint: { value: '' }, transportMode: { value: 'out-of-band' },
    loading: { value: false }, receiveMode: { value: 'blind' }, Storage: { set: async () => {} },
    walletManager: { createRGB11Invoice: async () => { calls++; return [null, { invoice: 'unexpected' }] } },
  }
  const generate = Function(...Object.keys(mocks), `${js}; return globalThis.generate`)(...Object.values(mocks))
  await generate()
  assert.equal(calls, 0)
  assert.equal(mocks.errorMessage.value, 'rgb11Invoice.contractIdMissing')
})
