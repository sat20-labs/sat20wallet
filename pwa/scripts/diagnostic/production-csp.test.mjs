import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const decodeAttribute = (value) => value
  .replaceAll('&#39;', "'")
  .replaceAll('&quot;', '"')
  .replaceAll('&amp;', '&')

test('production CSP permits only the exact Iris proxy origin', async () => {
  const indexPath = new URL('../../dist/index.html', import.meta.url)
  const html = await readFile(indexPath, 'utf8')
  const match = html.match(/<meta http-equiv="Content-Security-Policy" content="([^"]+)">/)
  assert.ok(match, 'production index must contain a Content-Security-Policy meta tag')

  const directives = decodeAttribute(match[1]).split(';').map((value) => value.trim())
  const connect = directives.find((value) => value.startsWith('connect-src '))
  assert.ok(connect, 'production CSP must contain connect-src')
  const sources = connect.split(/\s+/).slice(1)

  assert.ok(sources.includes('https://proxy.iriswallet.com'))
  assert.equal(sources.filter((value) => value === 'https://proxy.iriswallet.com').length, 1)
  assert.equal(sources.includes('*'), false)
  assert.equal(sources.includes('https:'), false)
  assert.equal(sources.some((value) => value.startsWith('http:')), false)
})
