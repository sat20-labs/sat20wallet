import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'

const origin = 'https://wallet.test'
const scope = `${origin}/pwa/`
const releaseId = '0.1.38-new-build'
const currentCacheName = `sat20-wallet-pwa-${releaseId}`
const oldCacheName = 'sat20-wallet-pwa-0.1.37-old-build'
const obsoleteCacheName = 'sat20-wallet-pwa-0.1.36-obsolete-build'

const normalizeCacheKey = (key) => {
  if (key instanceof Request) return key.url
  return new URL(String(key), origin).toString()
}

const createCacheStorage = () => {
  const stores = new Map()
  const open = async (name) => {
    if (!stores.has(name)) stores.set(name, new Map())
    const entries = stores.get(name)
    return {
      put: async (key, response) => entries.set(normalizeCacheKey(key), response.clone()),
      match: async (key) => entries.get(normalizeCacheKey(key))?.clone(),
      delete: async (key) => entries.delete(normalizeCacheKey(key)),
    }
  }
  return {
    stores,
    api: {
      open,
      has: async (name) => stores.has(name),
      keys: async () => [...stores.keys()],
      delete: async (name) => stores.delete(name),
    },
  }
}

const loadWorker = async ({ manifest: customManifest, cacheStorage = createCacheStorage(), fetch: customFetch } = {}) => {
  const sourcePath = new URL('../../public/service-worker.js', import.meta.url)
  let source = await readFile(sourcePath, 'utf8')
  const manifest = customManifest || {
    releaseId,
    assets: [
      { path: '/index.html', mime: 'text/html', size: 1, sha256: 'unused' },
      { path: '/assets/new.js', mime: 'text/javascript', size: 1, sha256: 'unused' },
      { path: '/wasm/sat20wallet.wasm', mime: 'application/wasm', size: 1, sha256: 'unused' },
    ],
  }
  source = source
    .replace('/* __SAT20_BUILD_ASSETS__ */ []', `/* __SAT20_BUILD_ASSETS__ */ ${JSON.stringify(manifest.assets.map((asset) => asset.path))}`)
    .replace('/* __SAT20_RELEASE_MANIFEST__ */ {}', `/* __SAT20_RELEASE_MANIFEST__ */ ${JSON.stringify(manifest)}`)

  const listeners = new Map()
  let claimCalls = 0
  let networkFetchCalls = 0
  const self = {
    registration: {
      scope,
      unregister: async () => true,
    },
    location: { origin },
    clients: {
      claim: async () => { claimCalls++ },
      matchAll: async () => [],
    },
    skipWaiting: async () => {},
    addEventListener: (type, listener) => listeners.set(type, listener),
  }
  const context = vm.createContext({
    self,
    caches: cacheStorage.api,
    fetch: async (request) => {
      networkFetchCalls++
      if (customFetch) return customFetch(request)
      if (new URL(request.url).pathname === '/pwa/') {
        return new Response(`<meta name="sat20-release" content="${releaseId}">new page`, {
          status: 200,
          headers: { 'Content-Type': 'text/html' },
        })
      }
      return new Response('network fallback', { status: 200 })
    },
    crypto: globalThis.crypto,
    URL,
    Request,
    Response,
    setTimeout,
    clearTimeout,
    console,
  })
  vm.runInContext(source, context, { filename: sourcePath.pathname })

  const dispatch = async (type, event = {}) => {
    const waits = []
    let response
    event.waitUntil = (promise) => waits.push(Promise.resolve(promise))
    event.respondWith = (promise) => { response = Promise.resolve(promise) }
    listeners.get(type)?.(event)
    await Promise.all(waits)
    return response ? response : undefined
  }

  return {
    cacheStorage,
    dispatch,
    claimCalls: () => claimCalls,
    networkFetchCalls: () => networkFetchCalls,
  }
}

const seedReleaseCache = async (cacheStorage, name, release = releaseId, assetBody = 'new asset') => {
  const cache = await cacheStorage.api.open(name)
  await cache.put(`${origin}/pwa/index.html`, new Response(`<meta name="sat20-release" content="${release}">`))
  await cache.put(`${origin}/pwa/assets/new.js`, new Response(assetBody, {
    headers: { 'Content-Type': 'text/javascript' },
  }))
}

test('upgrade keeps the old page and cache until the new page reports READY', async () => {
  const worker = await loadWorker()
  await seedReleaseCache(worker.cacheStorage, oldCacheName, '0.1.37-old-build', 'old asset')
  await seedReleaseCache(worker.cacheStorage, currentCacheName, releaseId, 'new asset')
  await seedReleaseCache(worker.cacheStorage, obsoleteCacheName, '0.1.36-obsolete-build', 'obsolete asset')
  const control = await worker.cacheStorage.api.open('sat20-wallet-control-v1')
  await control.put(`${origin}/pwa/__sat20_active_release__`, new Response(JSON.stringify({
    releaseId: '0.1.37-old-build',
  })))

  await worker.dispatch('activate')
  assert.equal(worker.claimCalls(), 0, 'activation must not claim the running old page')
  assert.equal(await worker.cacheStorage.api.has(oldCacheName), true, 'old release cache must remain before READY')
  assert.equal(await worker.cacheStorage.api.has(obsoleteCacheName), true, 'no old cache is cleaned before READY')
  assert.ok(await control.match(`${origin}/pwa/__sat20_pending_release__`), 'upgrade transition must be recorded')

  const navigationRequest = new Request(`${origin}/pwa/`)
  Object.defineProperty(navigationRequest, 'mode', { value: 'navigate' })
  const navigation = await worker.dispatch('fetch', { request: navigationRequest })
  assert.match(await (await navigation).text(), /new page/, 'pending reload must boot the target release')
  const targetAsset = await worker.dispatch('fetch', {
    request: new Request(`${origin}/pwa/assets/new.js`),
  })
  assert.equal(await (await targetAsset).text(), 'new asset', 'pending worker must select target assets')
  assert.equal(await worker.cacheStorage.api.has(oldCacheName), true, 'pending reload must not clean the running old release')
  assert.equal((await (await control.match(`${origin}/pwa/__sat20_active_release__`)).json()).releaseId,
    '0.1.37-old-build', 'pending reload must not commit active release before READY')

  const replies = []
  await worker.dispatch('message', {
    data: { type: 'SAT20_PAGE_READY', releaseId },
    source: { url: `${origin}/pwa/` },
    ports: [{ postMessage: (message) => replies.push(message) }],
  })
  assert.equal(replies.length, 1)
  assert.equal(replies[0].ok, true)
  assert.equal(replies[0].releaseId, releaseId)
  assert.equal(worker.claimCalls(), 1, 'READY may claim the verified new page')
  assert.equal(await worker.cacheStorage.api.has(oldCacheName), false, 'replaced release is retired after READY')
  assert.equal(await worker.cacheStorage.api.has(obsoleteCacheName), true, 'unrelated cache is not an explicit retirement candidate')
  assert.equal(await control.match(`${origin}/pwa/__sat20_pending_release__`), undefined)
  assert.equal((await (await control.match(`${origin}/pwa/__sat20_active_release__`)).json()).releaseId, releaseId)
})

test('current worker serves its own hashed assets before the active pointer is committed', async () => {
  const worker = await loadWorker()
  await seedReleaseCache(worker.cacheStorage, oldCacheName, '0.1.37-old-build', 'old asset')
  await seedReleaseCache(worker.cacheStorage, currentCacheName, releaseId, 'new asset')
  const control = await worker.cacheStorage.api.open('sat20-wallet-control-v1')
  await control.put(`${origin}/pwa/__sat20_active_release__`, new Response(JSON.stringify({
    releaseId: '0.1.37-old-build',
  })))

  const targetAsset = await worker.dispatch('fetch', {
    request: new Request(`${origin}/pwa/assets/new.js`),
  })
  assert.equal(await (await targetAsset).text(), 'new asset')
})

test('first install activates normally without an upgrade handshake', async () => {
  const worker = await loadWorker()
  await seedReleaseCache(worker.cacheStorage, currentCacheName)
  await seedReleaseCache(worker.cacheStorage, obsoleteCacheName)
  await worker.dispatch('activate')
  assert.equal(worker.claimCalls(), 1)
  assert.equal(await worker.cacheStorage.api.has(obsoleteCacheName), true)
  const control = await worker.cacheStorage.api.open('sat20-wallet-control-v1')
  const active = await control.match(`${origin}/pwa/__sat20_active_release__`)
  assert.equal((await active.json()).releaseId, releaseId)
})

test('missing hashed and manifest resources fail without network fallback', async () => {
  const worker = await loadWorker()
  await seedReleaseCache(worker.cacheStorage, currentCacheName)
  await worker.dispatch('activate')

  const unknown = await worker.dispatch('fetch', {
    request: new Request(`${origin}/pwa/assets/old-hash.js`),
  })
  assert.equal((await unknown).status, 404)

  const missingManifest = await worker.dispatch('fetch', {
    request: new Request(`${origin}/pwa/integrity-manifest.json`),
  })
  assert.equal((await missingManifest).status, 503)
  const missingWasm = await worker.dispatch('fetch', {
    request: new Request(`${origin}/pwa/wasm/sat20wallet.wasm`),
  })
  assert.equal((await missingWasm).status, 503)
  assert.equal(worker.networkFetchCalls(), 0)
})

const deferred = () => {
  let resolve
  const promise = new Promise((done) => { resolve = done })
  return { promise, resolve }
}

const releaseFixture = (id, beforeScript = async () => {}) => {
  const scriptPath = '/assets/index-C0KD_h7c.js'
  const html = `<meta name="sat20-release" content="${id}"><script type="module" src="/pwa${scriptPath}"></script>`
  const script = `globalThis.fixtureRelease = ${JSON.stringify(id)};`
  const bodies = new Map([['/index.html', html], [scriptPath, script]])
  const manifest = {
    releaseId: id,
    assets: [...bodies].map(([path, body]) => ({
      path,
      mime: path.endsWith('.js') ? 'text/javascript' : 'text/html',
      size: Buffer.byteLength(body),
      sha256: createHash('sha256').update(body).digest('hex'),
    })),
  }
  const fetch = async (request) => {
    const path = new URL(request.url).pathname.slice('/pwa'.length)
    if (path === scriptPath) await beforeScript()
    if (path === '/') return new Response(html, { headers: { 'Content-Type': 'text/html' } })
    const asset = manifest.assets.find((item) => item.path === path)
    return new Response(bodies.get(path) || '{}', {
      headers: { 'Content-Type': asset?.mime || 'application/json' },
    })
  }
  return { manifest, fetch, scriptPath, script }
}

test('installed release serves matching HTML, JS and WASM online and offline without rollback controls', async () => {
  const oldId = '0.1.37-old-build'
  const fixture = releaseFixture(releaseId)
  const legacyPolicy = { rollbackReleaseId: oldId, activateRollback: true }
  fixture.manifest.serviceWorker = legacyPolicy
  const wasmPath = '/wasm/sat20wallet.wasm'
  const wasmBody = 'new wasm fixture'
  fixture.manifest.assets.push({
    path: wasmPath, mime: 'application/wasm', size: Buffer.byteLength(wasmBody),
    sha256: createHash('sha256').update(wasmBody).digest('hex'),
  })
  let offline = false
  const worker = await loadWorker({
    manifest: fixture.manifest,
    fetch: async (request) => {
      if (offline) throw new Error('offline')
      const path = new URL(request.url).pathname.slice('/pwa'.length)
      if (path === '/version.json') return Response.json({ serviceWorker: legacyPolicy })
      if (path === '/integrity-manifest.json') return Response.json({ releaseId })
      if (path === wasmPath) return new Response(wasmBody, { headers: { 'Content-Type': 'application/wasm' } })
      if (path === '/') return new Response(`<meta name="sat20-release" content="${oldId}">old page`)
      return fixture.fetch(request)
    },
  })
  await seedReleaseCache(worker.cacheStorage, oldCacheName, oldId, 'old asset')
  const control = await worker.cacheStorage.api.open('sat20-wallet-control-v1')
  const activeKey = `${scope}__sat20_active_release__`
  await control.put(activeKey, Response.json({ releaseId: oldId }))
  await worker.dispatch('install')
  await worker.dispatch('activate')
  const pending = await (await control.match(`${scope}__sat20_pending_release__`)).json()
  assert.deepEqual(pending, { releaseId, previousReleaseId: oldId })

  const replies = []
  await worker.dispatch('message', {
    data: { type: 'ROLLBACK_RELEASE' }, ports: [{ postMessage: (message) => replies.push(message) }],
  })
  assert.equal(replies.length, 0, 'removed command must not select an older release')
  await worker.dispatch('message', {
    data: { type: 'GET_RELEASE_STATE' }, ports: [{ postMessage: (message) => replies.push(message) }],
  })
  assert.equal(replies[0].releaseId, releaseId)
  assert.equal('rollbackReleaseId' in replies[0], false)

  for (offline of [false, true]) {
    const request = new Request(scope)
    Object.defineProperty(request, 'mode', { value: 'navigate' })
    const html = await worker.dispatch('fetch', { request })
    assert.equal(html.status, 200)
    assert.match(await html.text(), new RegExp(`content="${releaseId}"`))
    const networkBefore = worker.networkFetchCalls()
    for (const [path, expected] of [
      [fixture.scriptPath, fixture.script],
      [`${wasmPath}?release=${releaseId}`, wasmBody],
      ['/integrity-manifest.json', JSON.stringify({ releaseId })],
    ]) {
      const response = await worker.dispatch('fetch', { request: new Request(`${origin}/pwa${path}`) })
      assert.equal(response.status, 200)
      assert.equal(await response.text(), expected)
    }
    assert.equal(worker.networkFetchCalls(), networkBefore, 'release assets remain cache-only')
  }
  assert.equal((await (await control.match(activeKey)).json()).releaseId, oldId, 'only READY commits the upgrade')
  await worker.dispatch('message', { data: { type: 'SAT20_PAGE_READY', releaseId }, source: { url: scope } })
  assert.equal((await (await control.match(activeKey)).json()).releaseId, releaseId)
  assert.equal(await worker.cacheStorage.api.has(oldCacheName), false)
})

test('install retains MIME, size and SHA-256 verification', async (t) => {
  for (const [field, value, error] of [
    ['mime', 'application/wasm', /unexpected MIME type/],
    ['size', 0, /size mismatch/],
    ['sha256', 'invalid', /SHA-256 mismatch/],
  ]) {
    await t.test(field, async () => {
      const fixture = releaseFixture(releaseId)
      const manifest = structuredClone(fixture.manifest)
      manifest.assets[1][field] = value
      const worker = await loadWorker({ manifest, fetch: fixture.fetch })
      await seedReleaseCache(worker.cacheStorage, oldCacheName, '0.1.37-old-build', 'old asset')
      const control = await worker.cacheStorage.api.open('sat20-wallet-control-v1')
      const activeKey = `${scope}__sat20_active_release__`
      await control.put(activeKey, Response.json({ releaseId: '0.1.37-old-build' }))
      await assert.rejects(worker.dispatch('install'), error)
      assert.equal((await (await control.match(activeKey)).json()).releaseId, '0.1.37-old-build')
      assert.equal(await worker.cacheStorage.api.has(oldCacheName), true, 'failed download must not retire the running release')
      assert.equal(worker.claimCalls(), 0)
    })
  }
})

test('170258 READY preserves 171805 installation and subsequent HTML/JS across reloads', { timeout: 5000 }, async (t) => {
  const middleId = '0.1.38-20260827T170258Z'
  const latestId = '0.1.38-20260827T171805Z'
  const previousId = '0.1.38-20260827T084520Z'
  const cacheName = (id) => `sat20-wallet-pwa-${id}`
  const storage = createCacheStorage()
  const middleFixture = releaseFixture(middleId)
  const middle = await loadWorker({ ...middleFixture, cacheStorage: storage })
  await middle.dispatch('install')
  await seedReleaseCache(storage, cacheName(previousId), previousId)
  const control = await storage.api.open('sat20-wallet-control-v1')
  const activeKey = `${scope}__sat20_active_release__`
  const pendingKey = `${scope}__sat20_pending_release__`
  await control.put(activeKey, new Response(JSON.stringify({ releaseId: previousId })))
  await middle.dispatch('activate')

  const downloadStarted = deferred()
  const resumeDownload = deferred()
  t.after(() => resumeDownload.resolve())
  const latestFixture = releaseFixture(latestId, async () => {
    downloadStarted.resolve()
    await resumeDownload.promise
  })
  const latest = await loadWorker({ ...latestFixture, cacheStorage: storage })
  const installing = latest.dispatch('install')
  await downloadStarted.promise
  assert.equal(await storage.api.has(cacheName(latestId)), true)
  const ready = (worker, id) => worker.dispatch('message', {
    data: { type: 'SAT20_PAGE_READY', releaseId: id }, source: { url: scope },
  })
  await ready(middle, middleId)
  assert.equal(await storage.api.has(cacheName(previousId)), false, 'explicitly replaced release is retired')
  assert.equal(await storage.api.has(cacheName(latestId)), true, 'installing release must survive old READY')
  resumeDownload.resolve()
  await installing
  assert.equal(await storage.api.has(cacheName(latestId)), true)
  // A repeated READY while the next worker is waiting must also be harmless.
  await ready(middle, middleId)
  await latest.dispatch('activate')
  const pendingBefore = await (await control.match(pendingKey)).text()
  await ready(latest, middleId)
  assert.equal(await (await control.match(pendingKey)).text(), pendingBefore, 'wrong-version READY cannot clear pending')

  for (let i = 0; i < 3; i++) {
    const request = new Request(scope)
    Object.defineProperty(request, 'mode', { value: 'navigate' })
    const html = await latest.dispatch('fetch', { request })
    assert.equal(html.status, 200)
    assert.ok((await html.text()).includes(latestFixture.scriptPath))
    const networkBefore = latest.networkFetchCalls()
    const js = await latest.dispatch('fetch', { request: new Request(`${origin}/pwa${latestFixture.scriptPath}`) })
    assert.equal(js.status, 200)
    assert.equal(await js.text(), latestFixture.script)
    assert.equal(latest.networkFetchCalls(), networkBefore, 'JS remains cache-only')
  }
  await ready(latest, latestId)
  assert.equal((await (await control.match(activeKey)).json()).releaseId, latestId)
  assert.equal(await control.match(pendingKey), undefined)
  assert.equal(await storage.api.has(cacheName(middleId)), false)
  assert.equal(await storage.api.has(cacheName(latestId)), true)
})

const updateWorker = (state) => {
  const listeners = new Set()
  const observed = deferred()
  const requested = deferred()
  const worker = {
    state,
    addEventListener: (_, listener) => {
      listeners.add(listener)
      observed.resolve()
    },
    removeEventListener: (_, listener) => listeners.delete(listener),
    postMessage: (message) => {
      assert.equal(message.type, 'SAT20_ACTIVATE_UPDATE')
      requested.resolve()
    },
  }
  const change = (next) => {
    worker.state = next
    for (const listener of [...listeners]) listener()
  }
  return { worker, observed, requested, change, listeners }
}

const loadUpdater = async (registration) => {
  const source = await readFile(new URL('../../composables/usePwaUpdate.ts', import.meta.url), 'utf8')
  const { outputText } = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  })
  const exports = {}
  const document = { title: '', body: { innerHTML: '' } }
  let closeCalls = 0
  vm.runInNewContext(outputText, {
    exports,
    require: (name) => {
      if (name === 'vue') return { ref: (value) => ({ value }) }
      if (name === '@/components/ui/toast-new/use-toast') return { useToast: () => ({ toast: () => {} }) }
      throw new Error(`Unexpected import: ${name}`)
    },
    navigator: { language: 'en', serviceWorker: { getRegistration: async () => registration } },
    window: { close: () => { closeCalls++ } },
    document,
    console,
  })
  return { updater: exports.usePwaUpdate(), document, closeCalls: () => closeCalls }
}

test('update waits for the selected installing worker to activate before showing restart', { timeout: 5000 }, async () => {
  const selected = updateWorker('installing')
  const registration = { installing: selected.worker, waiting: null, update: async () => {} }
  const ui = await loadUpdater(registration)
  const updating = ui.updater.reloadApp()
  await selected.observed.promise
  // Do not silently switch to a different worker while awaiting installation.
  registration.waiting = updateWorker('activated').worker
  selected.change('installed')
  await selected.requested.promise
  assert.equal(ui.document.body.innerHTML, '')
  assert.equal(ui.closeCalls(), 0)
  selected.change('activating')
  await new Promise(setImmediate)
  assert.equal(ui.document.body.innerHTML, '')
  assert.equal(ui.updater.isUpdating.value, true)
  selected.change('activated')
  await updating
  assert.match(ui.document.body.innerHTML, /Update ready/)
  assert.equal(ui.closeCalls(), 1)
  assert.equal(selected.listeners.size, 0)
})

test('redundant update worker fails without showing restart', async (t) => {
  for (const state of ['installing', 'installed']) {
    await t.test(state, { timeout: 5000 }, async () => {
      const selected = updateWorker(state)
      const registration = {
        installing: state === 'installing' ? selected.worker : null,
        waiting: state === 'installed' ? selected.worker : null,
        update: async () => {},
      }
      const ui = await loadUpdater(registration)
      const failure = assert.rejects(ui.updater.reloadApp(), /redundant/)
      await (state === 'installed' ? selected.requested.promise : selected.observed.promise)
      selected.change('redundant')
      await failure
      assert.equal(ui.updater.isUpdating.value, false)
      assert.equal(ui.document.body.innerHTML, '')
      assert.equal(ui.closeCalls(), 0)
      assert.equal(selected.listeners.size, 0)
    })
  }
})

test('update observes activation before posting the activation request', { timeout: 5000 }, async () => {
  const selected = updateWorker('installed')
  selected.worker.postMessage = () => selected.change('activated')
  const ui = await loadUpdater({ waiting: selected.worker, update: async () => {} })
  await ui.updater.reloadApp()
  assert.equal(ui.closeCalls(), 1)
  assert.equal(selected.listeners.size, 0)
})
