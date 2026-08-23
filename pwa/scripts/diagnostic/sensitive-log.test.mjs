import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const pwaRoot = path.resolve(here, '../..')
const phraseSource = await readFile(
  path.join(pwaRoot, 'entrypoints/popup/pages/wallet/settings/phrase.vue'),
  'utf8'
)
const wasmSource = await readFile(path.resolve(pwaRoot, '../sdk/wasm/main.go'), 'utf8')
const sentinel = 'sat20-sensitive-log-sentinel-do-not-emit'

const forbidden = [
  /console\.(?:log|debug|info|warn|error)\([^\n]*(?:mnemonic|result)/i,
  /Log\.[A-Za-z]+f?\([^\n]*ImportWallet[^\n]*(?:mnemonic|password)/,
]
const combined = `${phraseSource}\n${wasmSource}`
for (const pattern of forbidden) {
  if (pattern.test(combined)) {
    throw new Error(`Sensitive wallet logging pattern remains: ${pattern}`)
  }
}
if (combined.includes(sentinel)) {
  throw new Error('Sentinel mnemonic is embedded in production phrase/WASM sources')
}

console.log('Sensitive mnemonic logging guard passed')
