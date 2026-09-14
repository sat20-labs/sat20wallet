import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { parse, compileTemplate } from '@vue/compiler-sfc'

const root = new URL('../../', import.meta.url)
const load = path => readFile(new URL(path, root), 'utf8')
const components = [
  ['components/asset/L1AssetsTabs.vue', `selectedType !== 'RGB11' && !filteredAssets.length`, 'l1AssetsTabs.noAssets'],
  ['components/asset/L2AssetsTabs.vue', '!filteredAssets.length', 'l2AssetsTabs.noAssets'],
]

for (const [path, condition, key] of components) {
  const source = await load(path)
  const { descriptor, errors } = parse(source, { filename: path })
  assert.deepEqual(errors, [], `${path} parses`)
  assert.ok(descriptor.template?.content.includes(`v-if="${condition}"`), `${path} gates its empty state on the selected list`)
  assert.ok(descriptor.template?.content.includes(`$t('${key}'`), `${path} renders the scoped empty-state locale`)
  assert.ok(descriptor.template?.content.includes('data-testid="asset-empty-state"'))
  const compiled = compileTemplate({ source: descriptor.template.content, filename: path, id: path })
  assert.deepEqual(compiled.errors, [], `${path} template compiles`)
}

for (const language of ['en', 'zh']) {
  const locale = JSON.parse(await load(`locales/${language}.json`))
  for (const [section, chain] of [['l1AssetsTabs', 'Bitcoin'], ['l2AssetsTabs', 'SatoshiNet']]) {
    const message = locale[section].noAssets
    assert.match(message, /\{type\}/)
    assert.match(message, /\{network\}/)
    assert.ok(message.includes(chain))
    for (const type of ['ORDX', 'Runes', 'BRC20']) {
      const rendered = message.replace('{type}', locale[section].assetType[type]).replace('{network}', 'testnet')
      assert.ok(rendered.includes(type) && rendered.includes('testnet'))
    }
  }
}

console.log('L1/L2 ORDX, Runes and BRC20 scoped empty-state checks passed')
