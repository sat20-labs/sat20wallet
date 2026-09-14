import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import vm from 'node:vm'
import ts from 'typescript'
const compile = (source) => ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 } }).outputText
const read = (path) => readFile(new URL(path, import.meta.url), 'utf8')
// Load the real build plugin without invoking Vite/building any artifacts.
const pluginSource = compile(await read('../../build-plugins/solcModule.ts'))
const pluginContext = vm.createContext({})
const pluginModule = new vm.SourceTextModule(pluginSource, { context: pluginContext, initializeImportMeta: (meta) => { meta.url = new URL('../../build-plugins/solcModule.ts', import.meta.url).href } })
await pluginModule.link(async (id) => {
 const builtin = await import(id)
 return new vm.SyntheticModule(Object.keys(builtin), function () { for (const key of Object.keys(builtin)) this.setExport(key, builtin[key]) }, { context: pluginContext })
})
await pluginModule.evaluate()
const plugin = pluginModule.namespace.solcModulePlugin()
const adapter = plugin.load(plugin.resolveId(pluginModule.namespace.SOLC_MODULE_ID))
const original = await readFile(createRequire(import.meta.url).resolve('solc/soljson.js'), 'utf8')
assert.equal(adapter, original + '\nexport default Module;\n', 'compiler bytes remain intact')
assert.equal(plugin.resolveId('other'), null)
assert.equal(plugin.load('other'), null)

// Browser Worker-like module environment: deliberately no process/require/module.
const replies = []
let reply
const context = vm.createContext({ console, atob, btoa, TextDecoder, TextEncoder, setTimeout, clearTimeout,
 importScripts() { throw Error('Unexpected external worker script') },
 location: { href: 'https://wallet.example/pwa/assets/solc.worker.js' },
 postMessage(data) { replies.push(data); reply?.(data) },
})
vm.runInContext('self = globalThis', context)
assert.equal(vm.runInContext('typeof process', context), 'undefined')
const soljson = new vm.SourceTextModule(adapter, { context })
await soljson.link(() => { throw Error('Unexpected compiler import') })
const wrapper = new vm.SourceTextModule(compile(await read('../../utils/solc-browser.ts')), { context })
await wrapper.link((id) => { assert.equal(id, 'virtual:sat20-soljson'); return soljson })
const worker = new vm.SourceTextModule(compile(await read('../../workers/solc.worker.ts')), {
 context,
 importModuleDynamically: async (id) => { assert.equal(id, '../utils/solc-browser'); await wrapper.evaluate(); return wrapper },
})
await worker.link(() => { throw Error('Unexpected static worker import') })
await worker.evaluate()
const request = async (id, source) => {
 const response = new Promise((resolve, reject) => {
  const timer = setTimeout(() => reject(Error('Worker reply timed out')), 120000)
  reply = (data) => { clearTimeout(timer); resolve(data) }
 })
 context.onmessage({ data: { id, input: JSON.stringify({ language: 'Solidity', sources: { 'Tiny.sol': { content: source } }, settings: { outputSelection: { '*': { '*': ['abi', 'evm.bytecode.object'] } } } }) } })
 return response
}
const good = await request(11, 'pragma solidity ^0.8.36; contract Tiny {}')
assert.equal(good.id, 11)
assert.equal(good.error, undefined)
assert.match(good.version, /^0\.8\.36/)
assert.equal(typeof soljson.namespace.default.cwrap, 'function')
const output = JSON.parse(good.output)
assert.ok(output.contracts['Tiny.sol'].Tiny.evm.bytecode.object.length > 0)
assert.ok(Array.isArray(output.contracts['Tiny.sol'].Tiny.abi))
const bad = await request(12, 'pragma solidity ^0.8.36; contract {')
assert.equal(bad.id, 12)
assert.ok(JSON.parse(bad.output).errors.some((error) => error.severity === 'error'))
assert.equal(replies.length, 2)
console.log('Real soljson ESM export and worker protocol checks passed')
