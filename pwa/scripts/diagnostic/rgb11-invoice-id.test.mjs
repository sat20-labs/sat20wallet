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

const evaluateInit = async (name, mocks, dependencies = []) => {
  const initializers = await Promise.all([...dependencies, name].map(async key =>
    `const ${key} = ${await componentInit(key)}`))
  const js = ts.transpileModule(initializers.join('\n'), {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText
  return Function(...Object.keys(mocks), `${js}; return ${name}`)(...Object.values(mocks))
}

test('invoice selection uses the full contract key, never the display ticker', async () => {
  const key = `rgb11:f:${'01'.repeat(32)}`
  for (const [asset, expected] of [[{key, ticker: 'USDT'}, key], [{asset:key, ticker:'USDT'}, key], [{ticker:'USDT'}, '']]) {
    const value = await evaluateInit('assetKey', {props: {asset}, computed: factory => ({value:factory()})})
    assert.equal(value.value, expected)
  }
})

test('invoice sends the registered name when available and keeps the full contract key for the result', async () => {
  // SDK validation is not duplicated here. The real WASM negative cases are in
  // sdk-wasm-e2e.mjs (createRGB11Invoice: missing asset, zero amount, invalid transport).
  const key = `rgb11:f:${'01'.repeat(32)}`
  const shortName = 'rgb11:f:usdt@0101'
  const invoice = {id: 'invoice', amount: {asset: key}}
  const cases = [
    {asset: '', result: null},
    {asset: key, result: null},
    {asset: key, registered: shortName, result: invoice},
  ]
  for (const {asset, registered, result} of cases) {
    const calls = [], message = {value:''}, loading = {value:false}, receive = {value:null}
    let loads = 0
    const mocks = {props: {asset: asset ? {key: asset, chain_asset: registered} : undefined},
      assetKey:{value:asset}, amount:{value:' 1 '}, receiveMode:{value:'witness'},
      transport:{value:'manual'}, proxyEndpoint:{value:' '}, message, loading, receive,
      isOpen:{value:true}, generation:0, createWaitTimer:undefined,
      invoiceCreationPending:{value:false}, createWaiting:{value:false}, createTimedOut:{value:false},
      t:key=>key, load:async()=>{loads++},
      walletManager:{createRGB11Invoice:async input=>{
        calls.push(input)
        return result ? [null, result] : [new Error('SDK rejected invoice'), null]
      }},
    }
    // Keep the real request guards and timer cleanup in the same closure as generate.
    const generate = await evaluateInit('generate', mocks, ['createPending', 'isCurrent', 'clearCreateWait', 'run'])
    await generate()
    assert.deepEqual(calls, [{asset:registered || asset,amount:'1',receive_mode:'witness',transport:'manual',proxy_endpoint:''}])
    assert.equal(message.value,result ? '' : 'SDK rejected invoice')
    assert.equal(receive.value,result)
    assert.equal(loads,result ? 1 : 0)
    assert.equal(loading.value,false)
  }
})
