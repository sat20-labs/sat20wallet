import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const read = (relativePath) => fs.readFileSync(path.join(root, relativePath), 'utf8')

const tabs = read('components/asset/L2AssetsTabs.vue')
const card = read('components/wallet/L2Card.vue')
const list = read('components/wallet/AssetList.vue')

assert.match(tabs, /@click="handlerRefresh"/)
assert.match(tabs, /emit\('refresh'\)/)
assert.match(card, /@refresh="\$emit\('refresh'\)"/)
assert.match(list, /<L2Card[\s\S]*?@refresh="refreshL2Assets"[\s\S]*?\/>/)
assert.match(list, /queryClient\.invalidateQueries\(\{ queryKey: \['summary-l2'\] \}\)/)

console.log('L2 refresh event reaches the active summary-l2 query')
