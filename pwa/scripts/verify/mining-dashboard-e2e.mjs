// Browser integration of the real mining page. Only the wallet/API boundary
// is replaced with deterministic display data; no WASM or chain is started.
import assert from 'node:assert/strict'
import { mkdtemp, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium, expect } from '@playwright/test'
import { createServer } from 'vite'

const root = fileURLToPath(new URL('../../', import.meta.url))
const output = await mkdtemp(join(tmpdir(), 'pwa-mining-ui-'))
const target = '0'.repeat(19) + '3' + 'f'.repeat(44)
const difficulty = Number(BigInt('0x00000000ffff' + '0'.repeat(52))) / Number(BigInt('0x' + target))
let referenceDifficulty = difficulty
const cases = []
const mockWallet = [
  'const state = { status: { running: false, jobs: 0, hashesPerSecond: 0, bestShare: "", currentTarget: "", foundBlocks: [], lastError: "", rewardAddress: "display-fixture-only" }, actions: [], failRead: false };',
  'window.__miningTest = state;',
  'const result = () => [undefined, structuredClone(state.status)];',
  'export default {',
  ' getBTCLuckyMiningStatus: async () => state.failRead ? [new Error("display test: disconnected")] : result(),',
  ' getBTCLuckyRewards: async () => [undefined, []],',
  ' startBTCLuckyMining: async config => { state.actions.push({ name: "start", config }); Object.assign(state.status, { running: true, jobs: 1, hashesPerSecond: 0, bestShare: "", currentTarget: "", templateId: "" }); return result() },',
  ' stopBTCLuckyMining: async () => { state.actions.push({ name: "stop" }); state.status.running = false; state.status.hashesPerSecond = 0; return result() },',
  '};',
].join('\n')
const sources = {
  'virtual:mining-ui-wallet': mockWallet,
  'virtual:mining-ui-store': [
    'import { defineStore } from "pinia";',
    'export const useWalletStore = defineStore("mining-test-wallet", { state: () => ({ network: "mainnet" }) });',
    'export const useGlobalStore = defineStore("mining-test-global", { state: () => ({ env: "prd" }) });',
  ].join('\n'),
  'virtual:mining-ui-config': 'export const getConfig = () => ({ IndexerL1: { Scheme: "http", Host: "fixture.invalid", Proxy: "l1" }, IndexerL2: { Scheme: "http", Host: "fixture.invalid", Proxy: "l2" } });',
  'virtual:mining-ui-layout': 'import { h } from "vue"; export default { setup(_, { slots }) { return () => h("main", { class: "mx-auto w-full max-w-lg p-3" }, slots.default?.()) } };',
  'virtual:mining-ui-header': 'export default { render() { return null } };',
  'virtual:mining-ui-entry': [
    'import { createApp } from "vue";',
    'import { createPinia } from "pinia";',
    'import { createI18n } from "vue-i18n";',
    'import { createRouter, createMemoryHistory } from "vue-router";',
    'import Page from "/entrypoints/popup/pages/wallet/BTCLuckyMining.vue";',
    'import en from "/locales/en.json"; import zh from "/locales/zh.json";',
    'import "/assets/index.css";',
    'import { useWalletStore } from "virtual:mining-ui-store";',
    'const pinia = createPinia(); const i18n = createI18n({ legacy: false, locale: "en", messages: { en, zh } });',
    'const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/", component: { render: () => null } }] });',
    'createApp(Page).use(pinia).use(i18n).use(router).mount("#app");',
    'window.__miningTest.setNetwork = value => { useWalletStore(pinia).network = value };',
    'window.__miningTest.setLocale = value => { i18n.global.locale.value = value };',
  ].join('\n'),
}
const replacements = {
  '@/utils/sat20': 'virtual:mining-ui-wallet',
  '@/store': 'virtual:mining-ui-store',
  '@/config/wasm': 'virtual:mining-ui-config',
  '@/components/layout/LayoutHome.vue': 'virtual:mining-ui-layout',
  '@/components/wallet/HomeHeader.vue': 'virtual:mining-ui-header',
}
const server = await createServer({
  root,
  configFile: resolve(root, 'vite.config.ts'),
  logLevel: 'error',
  server: { host: '127.0.0.1', port: 0 },
  plugins: [{
    name: 'mining-ui-display-fixture', enforce: 'pre',
    resolveId(id) { if (sources[id]) return '\0' + id },
    load(id) { return sources[id.replace(/^\0/, '')] },
    transform(code, id) {
      if (id !== resolve(root, 'entrypoints/popup/pages/wallet/BTCLuckyMining.vue')) return
      for (const [from, to] of Object.entries(replacements)) code = code.replaceAll("from '" + from + "'", "from '" + to + "'")
      return code
    },
    configureServer(vite) {
      vite.middlewares.use(async (req, res, next) => {
        if (req.url !== '/__mining_ui__') return next()
        res.setHeader('Content-Type', 'text/html')
        res.end(await vite.transformIndexHtml(req.url, '<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"></head><body><div id="app"></div><script type="module">import "virtual:mining-ui-entry"</script></body></html>'))
      })
    },
  }],
})
let browser, page
const errors = []
const check = async (name, fn) => {
  try { await fn(); cases.push({ name, status: 'PASS' }) }
  catch (error) { cases.push({ name, status: 'FAIL', error: error.message }); throw error }
}
try {
  await server.listen()
  browser = await chromium.launch({ headless: true })
  page = await browser.newPage({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1, reducedMotion: 'reduce' })
  page.on('pageerror', error => errors.push(error.message))
  await page.route('**/*', async route => {
    const url = new URL(route.request().url())
    if (url.hostname === 'fixture.invalid') return route.fulfill({ headers: { 'access-control-allow-origin': '*' }, json: { code: 0, data: { height: 1 } } })
    if (url.hostname === 'mempool.space') return route.fulfill({ headers: { 'access-control-allow-origin': '*' }, json: { currentHashrate: 1e18, currentDifficulty: referenceDifficulty } })
    if (url.hostname !== '127.0.0.1') return route.abort()
    return route.continue()
  })
  await page.clock.install()
  await page.goto('http://127.0.0.1:' + server.httpServer.address().port + '/__mining_ui__')
  const refresh = async values => {
    await page.clock.runFor(2000)
    if (values) await page.evaluate(value => Object.assign(window.__miningTest.status, value), values)
    await page.getByTestId('mining-refresh').click()
  }
  await check('Stopped state is empty and does not fabricate a record', async () => {
    await expect(page.getByText('BTC Lucky Mining', { exact: true })).toBeVisible()
    await expect(page.getByTestId('mining-best-zeros')).toHaveText('—')
    await expect(page.getByTestId('mining-live-rate')).toHaveText('0 H/s')
    await expect(page.getByTestId('mining-network-ratio')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Start', exact: true })).toBeEnabled()
  })
  await check('Real page renders samples, binary zero-bit gaps and milestone feedback', async () => {
    await page.getByRole('button', { name: 'Start', exact: true }).click()
    await refresh({ hashesPerSecond: 500000, bestShare: '0'.repeat(5) + 'f'.repeat(59), currentTarget: target, templateId: 'display-template-a' })
    await expect(page.getByTestId('mining-best-zeros')).toHaveText('20')
    await refresh({ hashesPerSecond: 1000000, bestShare: '0'.repeat(6) + 'f'.repeat(58) })
    await expect(page.getByTestId('mining-best-zeros')).toHaveText('24')
    await expect(page.getByTestId('mining-zero-gap')).toContainText('54 leading zero bits')
    await expect(page.getByTestId('mining-record-message')).toContainText('First 24 leading zero bits')
    await expect(page.getByTestId('mining-milestone-24')).toHaveAttribute('data-reached', 'true')
    await expect(page.getByTestId('mining-milestone-28')).toHaveAttribute('data-reached', 'false')
    await expect(page.getByTestId('mining-network-ratio')).toBeVisible()
    assert.ok((await page.getByTestId('mining-chart-line').first().getAttribute('d')).includes('L'))
    assert.notEqual(await page.getByTestId('mining-average').innerText(), '—')
    await page.getByText('Inspect the full best hash', { exact: true }).click()
    await expect(page.getByTestId('mining-full-hash')).toHaveText('0'.repeat(6) + 'f'.repeat(58))
  })
  await check('Template changes retain the session record; zero gap does not claim a block', async () => {
    await refresh({ templateId: 'display-template-b', currentTarget: '0'.repeat(6) + '1' + 'f'.repeat(57) })
    await expect(page.getByTestId('mining-best-zeros')).toHaveText('24')
    await expect(page.getByTestId('mining-zero-gap')).toContainText('3 leading zero bits')
    await refresh({ currentTarget: '0'.repeat(6) + '8' + 'f'.repeat(57) })
    await expect(page.getByTestId('mining-zero-gap')).toContainText('0 leading zero bits')
    await expect(page.getByText('No records', { exact: true })).toBeVisible()
    await expect(page.getByTestId('mining-network-ratio')).toHaveCount(0)
    await refresh({ currentTarget: target })
  })
  await check('Stale sampling leaves a gap and resumes without bridging it', async () => {
    await page.evaluate(() => { window.__miningTest.failRead = true })
    await page.clock.runFor(10000)
    await expect(page.getByTestId('mining-live-rate')).toHaveText('—')
    await page.evaluate(() => { window.__miningTest.failRead = false })
    await refresh({ hashesPerSecond: 1500000 })
    await expect(page.getByTestId('mining-live-rate')).toHaveText('1.50 MH/s')
    await expect(page.getByTestId('mining-chart-line')).toHaveCount(2)
  })
  await check('Presets stage next-start settings without stopping the active miner', async () => {
    const before = await page.evaluate(() => window.__miningTest.actions.length)
    await page.getByTestId('mining-preset-performance').click()
    assert.equal(await page.evaluate(() => window.__miningTest.actions.length), before)
    await expect(page.getByRole('checkbox')).not.toBeChecked()
    await expect(page.locator('input[type="number"]')).toHaveValue('0')
    await expect(page.getByRole('button', { name: 'Stop', exact: true })).toBeEnabled()
    await page.getByRole('button', { name: 'Stop', exact: true }).click()
    await page.getByRole('button', { name: 'Start', exact: true }).click()
    const last = await page.evaluate(() => window.__miningTest.actions.at(-1))
    assert.deepEqual(last, { name: 'start', config: { jobs: '1', lowPriority: false, lowPrioritySleep: '0ms' } })
    await expect(page.getByTestId('mining-best-zeros')).toHaveText('—')
    await expect(page.getByTestId('mining-milestone-24')).toHaveAttribute('data-reached', 'false')
    await refresh({ hashesPerSecond: 1000000, bestShare: '0'.repeat(6) + 'f'.repeat(58), currentTarget: target, templateId: 'display-template-c' })
  })
  await check('Mobile and desktop layouts fit; Chinese labels and records are localized', async () => {
    await page.evaluate(() => window.__miningTest.setLocale('zh'))
    await expect(page.getByText('BTC 幸运挖矿', { exact: true })).toBeVisible()
    await expect(page.getByTestId('mining-record-message')).toContainText('本次首次达到 24 位前导零')
    await expect(page.getByTestId('mining-network-ratio')).toContainText('万亿')
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    await page.screenshot({ path: join(output, 'mobile.png'), fullPage: true })
    await page.setViewportSize({ width: 1024, height: 900 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    await page.screenshot({ path: join(output, 'desktop.png'), fullPage: true })
    assert.equal((await page.locator('body').innerText()).includes('pwaUi.mining.'), false)
  })
  await check('Regtest hides public reference data and mismatched networks show no ratio', async () => {
    await page.evaluate(() => window.__miningTest.setNetwork('regtest'))
    await expect(page.getByTestId('mining-network-ratio')).toHaveCount(0)
    await expect(page.getByTestId('mining-network-unavailable')).toContainText('Regtest')
    referenceDifficulty = 1
    await page.evaluate(() => window.__miningTest.setNetwork('mainnet'))
    await refresh()
    await expect(page.getByTestId('mining-network-ratio')).toHaveCount(0)
    await expect(page.getByTestId('mining-network-unavailable')).toContainText('私网')
  })
  assert.deepEqual(errors, [], 'browser runtime errors')
} catch (error) {
  process.exitCode = 1
  if (page) await page.screenshot({ path: join(output, 'failure.png'), fullPage: true }).catch(() => {})
  console.error(error.message)
} finally {
  const result = { scope: 'PWA display integration with deterministic wallet/API fixtures; no real mining or chain writes', cases, browserErrors: errors, output }
  await writeFile(join(output, 'results.json'), JSON.stringify(result, null, 2))
  console.log(JSON.stringify(result))
  await browser?.close()
  await server.close()
}
