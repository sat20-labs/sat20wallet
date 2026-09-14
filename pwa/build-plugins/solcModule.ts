import fs from 'node:fs'
import { createRequire } from 'node:module'

export const SOLC_MODULE_ID = 'virtual:sat20-soljson'
const resolvedID = '\0sat20-soljson-esm'
const require = createRequire(import.meta.url)

// Build-time adaptation only: keep the locked compiler source intact and
// export its browser Module, rather than its Node-only CommonJS export.
export function solcModulePlugin() {
  return {
    name: 'sat20-soljson-esm',
    enforce: 'pre' as const,
    resolveId(id: string) { return id === SOLC_MODULE_ID ? resolvedID : null },
    load(id: string) {
      if (id !== resolvedID) return null
      const source = fs.readFileSync(require.resolve('solc/soljson.js'), 'utf8')
      return `${source}\nexport default Module;\n`
    },
  }
}
