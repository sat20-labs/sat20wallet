const CACHE_PREFIX = 'sat20-wallet-pwa-'
const CONTROL_CACHE = 'sat20-wallet-control-v1'
const APP_BASE = new URL(self.registration.scope).pathname.replace(/\/$/, '')
const withBase = (path) => `${APP_BASE}${path}`

// Both markers are replaced in dist/service-worker.js after Vite has emitted
// the complete production bundle. A missing marker fails the build.
const BUILD_ASSET_PATHS = /* __SAT20_BUILD_ASSETS__ */ []
const RELEASE_MANIFEST = /* __SAT20_RELEASE_MANIFEST__ */ {}
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
