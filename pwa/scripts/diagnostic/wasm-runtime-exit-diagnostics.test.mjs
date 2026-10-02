import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = relative => readFileSync(new URL(relative, import.meta.url), 'utf8')

test('WASM runtime exit diagnostics retain exit code and safe operation breadcrumbs', () => {
  const wasm = source('../../utils/wasm.ts')
  const sat20 = source('../../utils/sat20.ts')
  const account = source('../../utils/accountManagement.ts')
  const diagnostics = source('../../utils/wasmRuntimeDiagnostics.ts')

  assert.match(wasm, /const originalExit = go\.exit\.bind\(go\)/)
  assert.match(wasm, /go-runtime-exit/)
  assert.match(wasm, /exit code/)
  assert.match(wasm, /snapshotWasmRuntimeDiagnostics\(\)/)
  assert.match(sat20, /noteWasmOperation\(methodName, 'dispatch'\)/)
  assert.match(sat20, /noteWasmOperation\(methodName, 'returned'\)/)
  assert.match(account, /const diagnosticMethod = `account\.\$\{methodName\}`/)
  assert.match(account, /noteWasmOperation\(diagnosticMethod, 'dispatch'\)/)
  assert.match(account, /noteWasmOperation\(diagnosticMethod, 'returned'\)/)
  assert.match(diagnostics, /const maxBreadcrumbs = 32/)
  assert.doesNotMatch(diagnostics, /args|payload|mnemonic|password|proof/i)
})
