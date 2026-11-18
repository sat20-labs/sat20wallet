import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = relative => readFileSync(new URL(relative, import.meta.url), 'utf8')

test('WASM runtime exit diagnostics retain exit code and safe wallet operation breadcrumbs', () => {
  // This test covers wallet runtime breadcrumbs, not account-facade diagnostics.
  const wasm = source('../../utils/wasm.ts')
  const sat20 = source('../../utils/sat20.ts')
  const diagnostics = source('../../utils/wasmRuntimeDiagnostics.ts')

  assert.match(wasm, /const originalExit = go\.exit\.bind\(go\)/)
  assert.match(wasm, /go-runtime-exit/)
  assert.match(wasm, /exit code/)
  assert.match(wasm, /snapshotWasmRuntimeDiagnostics\(\)/)
  assert.match(sat20, /noteWasmOperation\(methodName, 'dispatch'\)/)
  assert.match(sat20, /noteWasmOperation\(methodName, 'returned'\)/)
  assert.match(diagnostics, /const maxBreadcrumbs = 32/)
  assert.doesNotMatch(diagnostics, /args|payload|mnemonic|password|proof/i)
})
