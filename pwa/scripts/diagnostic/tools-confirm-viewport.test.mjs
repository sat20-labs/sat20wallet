import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import test from 'node:test'
import { chromium } from '@playwright/test'

const tools = readFileSync(new URL('../../entrypoints/popup/pages/wallet/Tools.vue', import.meta.url), 'utf8')
const dialog = readFileSync(new URL('../../components/ui/dialog/DialogContent.vue', import.meta.url), 'utf8')
const confirmClass = tools.match(/<DialogContent class="([^"]+)"[^>]*>\s*<DialogHeader>/)?.[1]
const baseClass = dialog.match(/'(fixed left-1\/2 top-1\/2[^']+)'/)?.[1]
assert.ok(confirmClass && baseClass, 'Tools confirmation or shared DialogContent class missing')
const builtCSS = readdirSync(new URL('../../dist/assets/', import.meta.url)).find((name) => /^index-.*\.css$/.test(name))
assert.ok(builtCSS, 'PWA built CSS missing; build the current candidate before viewport testing')
const css = readFileSync(new URL(`../../dist/assets/${builtCSS}`, import.meta.url), 'utf8')

test('Tools confirmation keeps Confirm reachable at a 375×564 viewport', async () => {
  let browser
  try {
    browser = await chromium.launch({ headless: true })
    const page = await browser.newPage({ viewport: { width: 375, height: 564 } })
    const rows = Array.from({ length: 14 }, (_, i) =>
      `<div class="grid grid-cols-[88px_1fr] gap-3"><span>Field ${i}</span><span class="break-all font-medium">0x${'a'.repeat(80)}</span></div>`
    ).join('')
    await page.setContent(`<style>${css}</style><body style="overflow:hidden"><div class="${baseClass} ${confirmClass}"><header>Transaction confirmation</header><div class="space-y-2 rounded-sm border border-border bg-muted/30 p-3 text-sm">${rows}</div><footer class="grid grid-cols-2 gap-2 sm:flex sm:justify-end"><button>Cancel</button><button>Confirm</button></footer></div></body>`)
    const confirm = page.getByRole('button', { name: 'Confirm' })
    await confirm.scrollIntoViewIfNeeded({ timeout: 2_000 })
    const bounds = await confirm.boundingBox()
    assert.ok(bounds && bounds.y >= 0 && bounds.y + bounds.height <= 564,
      `Confirm button remains outside the 564px viewport: ${JSON.stringify(bounds)}`)
  } finally {
    await browser?.close()
  }
})
