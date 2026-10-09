import assert from 'node:assert/strict'
import { cp, copyFile, mkdir, readFile, stat, symlink } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { createServer } from 'node:http'
import { spawn } from 'node:child_process'
import { dirname, join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium, expect } from '@playwright/test'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const [wasmPath, runtimePath, temporary] = process.argv.slice(2)
assert.ok(wasmPath && runtimePath && temporary, 'launch via TestPWAProductionReleaseE2E')
const copied = join(temporary, 'pwa')
const required = [
  'Release PWA: current production WASM passes the ordinary typechecked release build',
  'Release PWA: all emitted release assets match size hash MIME and release metadata',
  'Release PWA: production onboarding boots with CSP and without development hooks',
  'Release PWA: real service worker installs a complete verified release cache',
  'Release PWA: offline cold page boots the same release and persisted local catalog',
  'Release PWA: update preserves the running page then switches and retires the old cache after READY',
  'Release PWA: corrupted WASM rejects startup and cannot activate an incomplete release',
  'Release PWA: production online offline and update flows have no unhandled page errors',
]
const verdicts = new Map()
const errors = []
const hash = bytes => createHash('sha256').update(bytes).digest('hex')
let browser, server, dist, tamper = false, tamperedWasmRequests = 0
const mime = path => path.endsWith('.wasm') ? 'application/wasm' : path.endsWith('.js') ? 'text/javascript' :
  path.endsWith('.css') ? 'text/css' : path.endsWith('.html') ? 'text/html' :
  path.endsWith('.json') || path.endsWith('.webmanifest') ? 'application/json' :
  path.endsWith('.png') ? 'image/png' : path.endsWith('.svg') ? 'image/svg+xml' : 'application/octet-stream'
const check = async (index, action) => {
  const name = required[index], started = Date.now()
  console.log(JSON.stringify({ case: name, status: 'running', started_at: new Date(started).toISOString() }))
  try {
    await action(); verdicts.set(name, 'pass')
    console.log(JSON.stringify({ case: name, status: 'pass', duration_ms: Date.now() - started }))
  } catch (error) {
    verdicts.set(name, 'fail')
    console.error(JSON.stringify({ case: name, status: 'fail', duration_ms: Date.now() - started, message: error.stack }))
    for (const context of browser?.contexts() ?? []) for (const page of context.pages()) {
      const observation = await page.evaluate(async () => {
        const registration = await navigator.serviceWorker.getRegistration()
        const describe = worker => worker ? { url: worker.scriptURL, state: worker.state } : null
        return { release: document.querySelector('meta[name="sat20-release"]')?.content,
          controller: describe(navigator.serviceWorker.controller), active: describe(registration?.active),
          installing: describe(registration?.installing), waiting: describe(registration?.waiting),
          selected: describe(window.__sat20ReleaseTestWorker), caches: await caches.keys(),
          alerts: [...document.querySelectorAll('[role="alert"]')].map(node => node.textContent) }
      }).catch(error => ({ unavailable: error.message }))
      console.error(JSON.stringify({ observation: 'release failure state', ...observation }))
    }
    throw error
  }
}
const build = async label => {
  await new Promise((resolve, reject) => {
    const child = spawn(process.execPath, ['scripts/build-with-status.cjs'], {
      cwd: copied, stdio: 'inherit', env: { ...process.env, SAT20_PWA_BUILD_ID: `e2e-${process.pid}-${label}`,
        SAT20_PWA_BUILD_STATUS_DIR: join(temporary, `build-status-${label}`), VITE_PWA_BASE_PATH: '/pwa/' },
    })
    child.once('error', reject)
    child.once('exit', (code, signal) => code === 0 ? resolve() : reject(new Error(`production build ${label}: ${signal || code}`)))
  })
  const output = join(temporary, `release-${label}`)
  await cp(join(copied, 'dist'), output, { recursive: true })
  return output
}
const manifests = async directory => {
  const load = async name => JSON.parse(await readFile(join(directory, name), 'utf8'))
  const [release, version, integrity] = await Promise.all(['release-manifest.json', 'version.json', 'integrity-manifest.json'].map(load))
  assert.equal(release.releaseId, `${version.version}-${version.buildId}`)
  assert.equal(integrity.releaseId, release.releaseId)
  for (const asset of release.assets) {
    const path = join(directory, asset.path.replace(/^\//, ''))
    const bytes = await readFile(path)
    assert.equal(bytes.length, asset.size, asset.path); assert.equal(hash(bytes), asset.sha256, asset.path)
    assert.equal(mime(path), asset.mime)
  }
  const html = await readFile(join(directory, 'index.html'), 'utf8')
  assert.ok(html.includes(`name="sat20-release" content="${release.releaseId}"`))
  assert.ok(html.includes('Content-Security-Policy') && html.includes("wasm-unsafe-eval"))
  const sw = await readFile(join(directory, 'service-worker.js'), 'utf8')
  assert.ok(sw.includes(release.releaseId)); assert.ok(!sw.includes('__SAT20_RELEASE_MANIFEST__'))
  const manifest = await load('manifest.webmanifest')
  assert.equal(manifest.display, 'standalone'); assert.ok(manifest.icons.some(icon => icon.purpose?.includes('maskable')))
  return release
}
const cacheName = release => `sat20-wallet-pwa-${release.releaseId}`
const boot = async (page, release) => {
  await page.waitForFunction(() => Boolean(window.sat20), undefined, { timeout: 120000 })
  assert.equal(await page.locator('meta[name="sat20-release"]').getAttribute('content'), release.releaseId)
  assert.equal(await page.evaluate(() => typeof window.__SAT20_PWA_VERIFY__), 'undefined')
  await expect(page.locator('#app')).not.toBeEmpty()
}
const newContext = async () => {
  const context = await browser.newContext()
  context.setDefaultTimeout(60000)
  await context.route('**/*', route => ['localhost', '127.0.0.1'].includes(new URL(route.request().url()).hostname)
    ? route.continue() : route.abort('blockedbyclient'))
  context.on('page', page => page.on('pageerror', error => errors.push(error.message)))
  return context
}
const cacheKeys = page => page.evaluate(() => caches.keys())
const state = (page, selected = false) => page.evaluate(async selected => {
  // getRegistration can return an installing registration with active=null.
  // First await initial activation, then obtain the latest registration.
  const ready = await navigator.serviceWorker.ready
  const registration = await navigator.serviceWorker.getRegistration() ?? ready
  const worker = selected ? window.__sat20ReleaseTestWorker : registration.active
  assertWorker(worker)
  function assertWorker(worker) { if (!worker) throw new Error('selected SW is unavailable') }
  return await new Promise((resolve, reject) => {
    const channel = new MessageChannel()
    const timeout = setTimeout(() => reject(new Error('SW release state response timeout')), 10000)
    channel.port1.onmessage = event => { clearTimeout(timeout); resolve(event.data) }
    worker.postMessage({ type: 'GET_RELEASE_STATE' }, [channel.port2])
  })
}, selected)

try {
  let releaseA, context, page, origin, catalog
  await check(0, async () => {
    await cp(root, copied, { recursive: true, filter: path => {
      const relative = path.slice(root.length).split(sep).filter(Boolean)
      return !relative.some(part => ['node_modules', 'dist', '.git', '.build-status'].includes(part))
    } })
    await symlink(join(root, 'node_modules'), join(copied, 'node_modules'), 'dir')
    await mkdir(join(copied, 'public/wasm'), { recursive: true })
    await copyFile(wasmPath, join(copied, 'public/wasm/sat20wallet.wasm'))
    await copyFile(runtimePath, join(copied, 'public/wasm/wasm_exec.js'))
    dist = await build('a')
  })
  await check(1, async () => { releaseA = await manifests(dist) })
  server = createServer(async (request, response) => {
    try {
      const url = new URL(request.url, 'http://127.0.0.1')
      const name = decodeURIComponent(url.pathname).replace(/^\/pwa\/?/, '') || 'index.html'
      const path = resolve(dist, name)
      if (!path.startsWith(dist + sep)) { response.writeHead(403).end(); return }
      const info = await stat(path)
      assert.ok(info.isFile())
      let bytes = await readFile(path)
      if (tamper && name === 'wasm/sat20wallet.wasm') { tamperedWasmRequests++; bytes = Buffer.from(bytes); bytes[0] ^= 1 }
      response.writeHead(200, { 'Content-Type': mime(path), 'Cache-Control': 'no-store' }); response.end(bytes)
    } catch { response.writeHead(404).end() }
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  origin = `http://127.0.0.1:${server.address().port}/pwa/`
  const executable = process.env.SAT20_BROWSER_EXECUTABLE || [
    '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge',
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  ].find(existsSync)
  browser = await chromium.launch({ headless: true, ...(executable ? { executablePath: executable } : {}) })
  await check(2, async () => {
    context = await newContext(); page = await context.newPage(); await page.goto(origin); await boot(page, releaseA)
    const result = await page.evaluate(async () => await window.sat20wallet_wasm.getWalletCatalog())
    assert.equal(result.code, 0); assert.deepEqual(result.data.wallets, [])
    catalog = result.data
  })
  await check(3, async () => {
    await expect.poll(async () => (await cacheKeys(page)).includes(cacheName(releaseA)), { timeout: 120000 }).toBe(true)
    await expect.poll(async () => (await state(page)).releaseId, { timeout: 120000 }).toBe(releaseA.releaseId)
    await expect.poll(() => page.evaluate(() => Boolean(navigator.serviceWorker.controller)), { timeout: 60000 }).toBe(true)
    const cached = await page.evaluate(async ({ cache, assets }) => {
      const store = await caches.open(cache)
      return await Promise.all(assets.map(async asset => {
        const response = await store.match(`/pwa${asset.path}`)
        if (!response) return { path: asset.path, missing: true }
        const bytes = await response.arrayBuffer()
        const sha256 = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))].map(byte => byte.toString(16).padStart(2, '0')).join('')
        return { path: asset.path, size: bytes.byteLength, sha256 }
      }))
    }, { cache: cacheName(releaseA), assets: releaseA.assets })
    for (const [index, asset] of releaseA.assets.entries()) assert.deepEqual(cached[index], { path: asset.path, size: asset.size, sha256: asset.sha256 })
  })
  await check(4, async () => {
    await context.setOffline(true); await page.close()
    page = await context.newPage(); await page.goto(origin); await boot(page, releaseA)
    const result = await page.evaluate(async () => await window.sat20wallet_wasm.getWalletCatalog())
    assert.equal(result.code, 0); assert.deepEqual(result.data, catalog)
    assert.equal((await state(page)).releaseId, releaseA.releaseId)
    await context.setOffline(false)
  })
  await check(5, async () => {
    const next = await build('b'); const releaseB = await manifests(next); dist = next
    assert.notEqual(releaseB.releaseId, releaseA.releaseId)
    await page.evaluate(async () => { const registration = await navigator.serviceWorker.ready; await registration.update() })
    await expect.poll(() => page.evaluate(async () => Boolean((await navigator.serviceWorker.ready).waiting)), { timeout: 120000 }).toBe(true)
    assert.equal(await page.locator('meta[name="sat20-release"]').getAttribute('content'), releaseA.releaseId)
    // Follow the same worker object observed by usePwaUpdate. The old page's
    // controller deliberately remains the previous release until restart.
    await page.evaluate(async () => {
      const registration = await navigator.serviceWorker.getRegistration()
      window.__sat20ReleaseTestWorker = registration.waiting
      if (!window.__sat20ReleaseTestWorker) throw new Error('waiting update worker missing')
      window.__sat20ReleaseTestWorker.postMessage({ type: 'SAT20_ACTIVATE_UPDATE' })
    })
    await expect.poll(() => page.evaluate(() => window.__sat20ReleaseTestWorker.state), { timeout: 120000 }).toBe('activated')
    await expect.poll(async () => (await state(page, true)).releaseId, { timeout: 120000 }).toBe(releaseB.releaseId)
    assert.equal(await page.locator('meta[name="sat20-release"]').getAttribute('content'), releaseA.releaseId)
    assert.ok((await cacheKeys(page)).includes(cacheName(releaseA)), 'old cache retired before new page READY')
    await page.close(); page = await context.newPage(); await page.goto(origin); await boot(page, releaseB)
    await expect.poll(async () => (await cacheKeys(page)).includes(cacheName(releaseA)), { timeout: 60000 }).toBe(false)
    assert.ok((await cacheKeys(page)).includes(cacheName(releaseB)))
    const result = await page.evaluate(async () => await window.sat20wallet_wasm.getWalletCatalog())
    assert.equal(result.code, 0); assert.deepEqual(result.data, catalog)
    await context.setOffline(true); await page.close(); page = await context.newPage()
    await page.goto(origin); await boot(page, releaseB); await context.setOffline(false)
  })
  await check(6, async () => {
    tamper = true
    const broken = await newContext(); const failed = await broken.newPage()
    try {
      await failed.goto(origin)
      await expect(failed.getByRole('alert')).toBeVisible({ timeout: 120000 })
      assert.equal(await failed.evaluate(() => typeof window.sat20), 'undefined')
      // Require both the actual loader and actual worker to fetch the bad
      // artifact before checking that installation reached a failed state.
      await expect.poll(() => tamperedWasmRequests, { timeout: 120000 }).toBeGreaterThanOrEqual(2)
      await expect.poll(() => failed.evaluate(async () => {
        const registrations = await navigator.serviceWorker.getRegistrations()
        return registrations.some(registration => Boolean(registration.active || registration.installing || registration.waiting))
      }), { timeout: 120000 }).toBe(false)
    } finally { await broken.close(); tamper = false }
  })
  await check(7, async () => assert.deepEqual(errors, []))
} finally {
  for (const name of required) if (!verdicts.has(name)) console.log(JSON.stringify({ case: name, status: 'not-run' }))
  console.log(JSON.stringify({ caseSummary: { required: required.length, pass: [...verdicts.values()].filter(value => value === 'pass').length,
    fail: [...verdicts.values()].filter(value => value === 'fail').length, not_run: required.filter(name => !verdicts.has(name)).length } }))
  await browser?.close()
  await new Promise(resolve => { if (!server) return resolve(); server.close(resolve); server.closeAllConnections() })
  if (required.some(name => verdicts.get(name) !== 'pass')) process.exitCode = 1
}
