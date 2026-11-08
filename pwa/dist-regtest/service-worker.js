const CACHE_PREFIX = 'sat20-wallet-pwa-'
const CONTROL_CACHE = 'sat20-wallet-control-v1'
const APP_BASE = new URL(self.registration.scope).pathname.replace(/\/$/, '')
const withBase = (path) => `${APP_BASE}${path}`

// Both markers are replaced in dist/service-worker.js after Vite has emitted
// the complete production bundle. A missing marker fails the build.
const BUILD_ASSET_PATHS = [
  "/assets/Tools-De_ay-xB.js",
  "/assets/index-B63biSUD.css",
  "/assets/index-D-j3A8Iw.js",
  "/assets/main-CC7y2itL.css",
  "/assets/main-CGOFWbGu.js",
  "/assets/solc-browser-bkLe_NDp.js",
  "/assets/solc.worker-Bt3AFwXp.js"
]
const RELEASE_MANIFEST = {
  "schemaVersion": 1,
  "releaseId": "0.1.46-20261106T225010Z-pwa",
  "version": "0.1.46",
  "buildId": "20261106T225010Z-pwa",
  "commit": "fde00ec629f6",
  "publishedAt": "2026-11-06T22:50:10.655Z",
  "serviceWorker": {
    "schemaVersion": 1,
    "releaseId": "0.1.46-20261106T225010Z-pwa",
    "killSwitch": false
  },
  "assets": [
    {
      "path": "/index.html",
      "mime": "text/html",
      "size": 1000,
      "sha256": "281b192b0a7a467be57650b0f2983649a9797d11daa9692d5bca84bbdda60196"
    },
    {
      "path": "/assets/Tools-De_ay-xB.js",
      "mime": "text/javascript",
      "size": 163157,
      "sha256": "8a8394c04f975f38540d89efa4446e6eb753c83c594fad56412435695ae4ee59"
    },
    {
      "path": "/assets/index-B63biSUD.css",
      "mime": "text/css",
      "size": 173,
      "sha256": "e3d6529e81276383036104ae0fe22fb0db2c38576e685d251aaecf9300d0be84"
    },
    {
      "path": "/assets/index-D-j3A8Iw.js",
      "mime": "text/javascript",
      "size": 256972,
      "sha256": "db0ea8e6dfae679e412c835c9d9d09426871155efc7f90b94fc52d4396fbcd59"
    },
    {
      "path": "/assets/main-CC7y2itL.css",
      "mime": "text/css",
      "size": 73010,
      "sha256": "2f64eb27b5218757f9706f25c0e095d2f4a82260f21ef99f5e4a84d1c1505db4"
    },
    {
      "path": "/assets/main-CGOFWbGu.js",
      "mime": "text/javascript",
      "size": 3427801,
      "sha256": "0c451416af299123a68949d1ec414be0d5bd9135581e9202d3d7b72614bfec3c"
    },
    {
      "path": "/assets/solc-browser-bkLe_NDp.js",
      "mime": "text/javascript",
      "size": 9332417,
      "sha256": "575d4ddf13c3e7c35c665e266acaf0985de96be12e78b485b0c9f2477a0a4125"
    },
    {
      "path": "/assets/solc.worker-Bt3AFwXp.js",
      "mime": "text/javascript",
      "size": 354,
      "sha256": "67ec366e967fdbc572f9cdfc9b6a0f1d149a72409f32d9782d90bc860bf3979b"
    },
    {
      "path": "/wasm/wasm_exec.js",
      "mime": "text/javascript",
      "size": 16992,
      "sha256": "0c949f4996f9a89698e4b5c586de32249c3b69b7baadb64d220073cc04acba14"
    },
    {
      "path": "/wasm/sat20wallet.wasm",
      "mime": "application/wasm",
      "size": 39305847,
      "sha256": "830a890ae161aab5912d2a74e4fc0d2aaf89e57fb14584acf0361315ad76c6c8"
    }
  ]
}
const RELEASE_ID = RELEASE_MANIFEST.releaseId || 'unbuilt'
const CURRENT_CACHE = `${CACHE_PREFIX}${RELEASE_ID}`
const ACTIVE_RELEASE_KEY = withBase('/__sat20_active_release__')
const PENDING_RELEASE_KEY = withBase('/__sat20_pending_release__')

const STATIC_PATHS = [
  '/manifest.webmanifest',
  '/version.json',
  '/integrity-manifest.json',
  '/release-manifest.json',
  '/icon/apple-touch-icon.png',
  '/icon/sat20-logo-app.png',
  '/icon/maskable-512.png',
  '/icon/maskable-192.png',
  '/icon/512.png',
  '/icon/192.png',
  '/icon/128.png',
  '/icon/48.png',
  '/icon/32.png',
  '/icon/16.png',
]

const normalizeMime = (value) => (value || '').split(';', 1)[0].trim().toLowerCase()

const sha256Hex = async (bytes) => {
  const digest = await crypto.subtle.digest('SHA-256', bytes)
  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('')
}

const versionedUrl = (path) => {
  const url = new URL(withBase(path), self.location.origin)
  url.searchParams.set('release', RELEASE_ID)
  return url.toString()
}

const cacheKey = (path) => new Request(new URL(withBase(path), self.location.origin))

const fetchVerifiedReleaseAsset = async (asset) => {
  const response = await fetch(new Request(versionedUrl(asset.path), { cache: 'reload' }))
  if (!response.ok) throw new Error(`${asset.path} returned HTTP ${response.status}`)
  if (normalizeMime(response.headers.get('content-type')) !== asset.mime) {
    throw new Error(`${asset.path} returned an unexpected MIME type`)
  }
  const bytes = await response.arrayBuffer()
  if (bytes.byteLength !== asset.size) throw new Error(`${asset.path} size mismatch`)
  if (await sha256Hex(bytes) !== asset.sha256) throw new Error(`${asset.path} SHA-256 mismatch`)
  return new Response(bytes, {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers,
  })
}

const fetchRequiredStaticAsset = async (path) => {
  const response = await fetch(new Request(versionedUrl(path), { cache: 'reload' }))
  if (!response.ok) throw new Error(`${path} returned HTTP ${response.status}`)
  return response
}

const precacheRelease = async () => {
  if (!RELEASE_MANIFEST.releaseId || !Array.isArray(RELEASE_MANIFEST.assets)) {
    throw new Error('Service worker release manifest was not injected')
  }
  const cache = await caches.open(CURRENT_CACHE)
  await Promise.all(RELEASE_MANIFEST.assets.map(async (asset) => {
    const response = await fetchVerifiedReleaseAsset(asset)
    await cache.put(cacheKey(asset.path), response.clone())
    if (asset.path === '/index.html') {
      await cache.put(cacheKey('/'), response)
    }
  }))
  await Promise.all(STATIC_PATHS.map(async (path) => {
    const response = await fetchRequiredStaticAsset(path)
    await cache.put(cacheKey(path), response)
  }))
}

const setActiveRelease = async () => {
  if (!(await caches.has(CURRENT_CACHE))) throw new Error(`Release cache ${RELEASE_ID} is unavailable`)
  const control = await caches.open(CONTROL_CACHE)
  await control.put(ACTIVE_RELEASE_KEY, new Response(JSON.stringify({ releaseId: RELEASE_ID })))
}

const readControlState = async (key) => {
  const control = await caches.open(CONTROL_CACHE)
  const response = await control.match(key)
  return response ? response.json().catch(() => null) : null
}

const setPendingRelease = async (previousReleaseId) => {
  const control = await caches.open(CONTROL_CACHE)
  await control.put(PENDING_RELEASE_KEY, new Response(JSON.stringify({
    releaseId: RELEASE_ID,
    previousReleaseId,
  })))
}

const clearPendingRelease = async () => {
  const control = await caches.open(CONTROL_CACHE)
  await control.delete(PENDING_RELEASE_KEY)
}

const cleanupReleaseCaches = async (previousReleaseId) => {
  if (typeof previousReleaseId !== 'string' || !previousReleaseId) return
  const previousCache = `${CACHE_PREFIX}${previousReleaseId}`
  if (previousCache === CURRENT_CACHE) return
  // Only retire the release explicitly replaced by this transition. Other
  // caches may belong to a newer worker that is still installing or waiting.
  await caches.delete(previousCache)
}

const disableServiceWorker = async () => {
  const keys = await caches.keys()
  await Promise.all(keys.filter((key) => key.startsWith(CACHE_PREFIX)).map((key) => caches.delete(key)))
  await caches.delete(CONTROL_CACHE)
  await self.registration.unregister()
  const clients = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
  clients.forEach((client) => client.postMessage({ type: 'SAT20_SW_DISABLED' }))
}

const applyRemoteControl = async () => {
  try {
    const response = await fetch(new Request(versionedUrl('/version.json'), { cache: 'no-store' }))
    if (!response.ok) return 'none'
    const version = await response.json()
    const policy = version?.serviceWorker
    if (policy?.killSwitch === true) {
      await disableServiceWorker()
      return 'disabled'
    }
  } catch {
    // Offline operation continues with the last atomically installed release.
  }
  return 'none'
}

self.addEventListener('install', (event) => {
  if (RELEASE_MANIFEST.serviceWorker?.killSwitch === true) {
    event.waitUntil(self.skipWaiting())
    return
  }
  event.waitUntil(precacheRelease())
})

self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    if (RELEASE_MANIFEST.serviceWorker?.killSwitch === true) {
      await disableServiceWorker()
      return
    }
    const previous = await readControlState(ACTIVE_RELEASE_KEY)
    const previousReleaseId = typeof previous?.releaseId === 'string' ? previous.releaseId : ''
    const previousCacheAvailable = previousReleaseId && await caches.has(`${CACHE_PREFIX}${previousReleaseId}`)
    if (!previousCacheAvailable || previousReleaseId === RELEASE_ID) {
      await setActiveRelease()
      await clearPendingRelease()
      await cleanupReleaseCaches(previousReleaseId)
      await self.clients.claim()
      return
    }

    // An upgrade may activate after an explicit user request, but it must not
    // take control of an already running page. The first page that boots this
    // release reports SAT20_PAGE_READY after WASM and Vue are both ready.
    await setPendingRelease(previousReleaseId)
  })())
})

self.addEventListener('message', (event) => {
  const reply = (payload) => event.ports?.[0]?.postMessage(payload)
  if (event.data?.type === 'SAT20_ACTIVATE_UPDATE' || event.data?.type === 'SKIP_WAITING') {
    event.waitUntil(self.skipWaiting())
    return
  }
  if (event.data?.type === 'SAT20_PAGE_READY') {
    event.waitUntil((async () => {
      if (event.data.releaseId !== RELEASE_ID) {
        throw new Error(`READY release ${event.data.releaseId || '<missing>'} does not match ${RELEASE_ID}`)
      }
      const sourceUrl = event.source?.url ? new URL(event.source.url) : null
      if (sourceUrl && (sourceUrl.origin !== self.location.origin || !sourceUrl.pathname.startsWith(`${APP_BASE}/`))) {
        throw new Error('READY source is outside the service worker scope')
      }
      const pending = await readControlState(PENDING_RELEASE_KEY)
      const previousReleaseId = pending?.releaseId === RELEASE_ID
        ? pending.previousReleaseId
        : ''
      await setActiveRelease()
      await clearPendingRelease()
      await cleanupReleaseCaches(previousReleaseId)
      await self.clients.claim()
      reply({ ok: true, releaseId: RELEASE_ID })
    })().catch((error) => reply({ ok: false, error: error.message })))
    return
  }
  if (event.data?.type === 'GET_RELEASE_STATE') {
    reply({
      ok: true,
      releaseId: RELEASE_ID,
      currentReleaseId: RELEASE_ID,
    })
  }
})

// Each worker serves one complete release, including while an upgrade is
// waiting for the new page's READY message. Never mix another release's HTML.
const cachedResponseFor = async (requestUrl) => {
  const cache = await caches.open(CURRENT_CACHE)
  return cache.match(new Request(new URL(requestUrl.pathname, self.location.origin)))
}

const releaseAssetPaths = new Set([
  ...(RELEASE_MANIFEST.assets || []).map((asset) => withBase(asset.path)),
  ...BUILD_ASSET_PATHS.map((path) => withBase(path)),
])

const protectedReleasePaths = new Set([
  withBase('/integrity-manifest.json'),
  withBase('/release-manifest.json'),
])

const isHashedReleaseNamespace = (pathname) =>
  pathname.startsWith(`${withBase('/assets')}/`) || pathname.startsWith(`${withBase('/wasm')}/`)

const networkFirstNavigation = async (request) => {
  const cache = await caches.open(CURRENT_CACHE)
  try {
    const response = await fetch(new Request(request, { cache: 'no-store' }))
    if (!response.ok) throw new Error(`navigation returned HTTP ${response.status}`)
    const html = await response.clone().text()
    const releaseMarker = `name="sat20-release" content="${RELEASE_ID}"`
    if (!html.includes(releaseMarker)) {
      throw new Error('network HTML belongs to a different release')
    }
    await cache.put(cacheKey('/index.html'), response.clone())
    await cache.put(cacheKey('/'), response.clone())
    return response
  } catch {
    const cached = await cache.match(cacheKey('/index.html')) || await cache.match(cacheKey('/'))
    if (cached) return cached
    return new Response('SAT20 Wallet release is unavailable', {
      status: 503,
      headers: { 'Content-Type': 'text/plain; charset=utf-8' },
    })
  }
}

self.addEventListener('fetch', (event) => {
  if (event.request.method !== 'GET') return
  const requestUrl = new URL(event.request.url)
  if (requestUrl.origin !== self.location.origin) {
    event.respondWith(fetch(event.request))
    return
  }

  if (event.request.mode === 'navigate') {
    event.respondWith((async () => {
      const control = await applyRemoteControl()
      if (control === 'disabled') return fetch(event.request)
      return networkFirstNavigation(event.request)
    })())
    return
  }

  if (requestUrl.pathname === withBase('/version.json')) {
    event.respondWith(
      fetch(new Request(event.request, { cache: 'no-store' }))
        .catch(() => cachedResponseFor(requestUrl))
    )
    return
  }

  if (releaseAssetPaths.has(requestUrl.pathname) || protectedReleasePaths.has(requestUrl.pathname)) {
    event.respondWith(
      cachedResponseFor(requestUrl)
        .then((cached) => cached || new Response('Release asset unavailable', { status: 503 }))
    )
    return
  }

  if (isHashedReleaseNamespace(requestUrl.pathname)) {
    event.respondWith(new Response('Asset is not part of this release', {
      status: 404,
      headers: { 'Content-Type': 'text/plain; charset=utf-8' },
    }))
    return
  }

  event.respondWith(
    cachedResponseFor(requestUrl).then((cached) => cached || fetch(event.request))
  )
})
