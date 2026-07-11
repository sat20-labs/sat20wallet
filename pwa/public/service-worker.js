const CACHE_NAME = 'sat20-wallet-pwa-v0.1.37-20260710T062059Z'
const CACHE_PREFIX = 'sat20-wallet-pwa-'
const APP_BASE = new URL(self.registration.scope).pathname.replace(/\/$/, '')
const withBase = (path) => `${APP_BASE}${path}`

const PRECACHE_URLS = [
  withBase('/'),
  withBase('/index.html'),
  withBase('/wasm/wasm_exec.js'),
  withBase('/wasm/sat20wallet.wasm'),
  withBase('/icon/apple-touch-icon.png'),
  withBase('/icon/sat20-logo-app.png'),
  withBase('/icon/maskable-512.png'),
  withBase('/icon/maskable-192.png'),
  withBase('/icon/512.png'),
  withBase('/icon/192.png'),
  withBase('/icon/128.png'),
  withBase('/icon/48.png'),
  withBase('/icon/32.png'),
  withBase('/icon/16.png')
]

const cacheNetworkResponse = async (request, response) => {
  if (!response || !response.ok) {
    return
  }

  const cache = await caches.open(CACHE_NAME)
  await cache.put(request, response.clone())
}

const fetchAndCache = async (request, cacheKey = request) => {
  const response = await fetch(new Request(request, { cache: 'reload' }))
  await cacheNetworkResponse(cacheKey, response)
  return response
}

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME)
      .then((cache) => cache.addAll(
        PRECACHE_URLS.map((url) => new Request(url, { cache: 'reload' }))
      ))
      .then(() => self.skipWaiting())
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(
        keys
          .filter((key) => key.startsWith(CACHE_PREFIX) && key !== CACHE_NAME)
          .map((key) => caches.delete(key))
      ))
      .then(() => self.clients.claim())
  )
})

self.addEventListener('message', (event) => {
  if (event.data?.type === 'SKIP_WAITING') {
    self.skipWaiting()
  }
})

self.addEventListener('fetch', (event) => {
  if (event.request.method !== 'GET') {
    return
  }

  const requestUrl = new URL(event.request.url)

  if (requestUrl.origin !== self.location.origin) {
    event.respondWith(fetch(event.request))
    return
  }

  if (event.request.mode === 'navigate') {
    event.respondWith(
      caches.match(withBase('/index.html'))
        .then((cachedResponse) => {
          if (cachedResponse) {
            return cachedResponse
          }

          return fetchAndCache(
            new Request(event.request, { cache: 'no-store' }),
            withBase('/index.html')
          )
        })
    )
    return
  }

  if (
    requestUrl.pathname === withBase('/manifest.webmanifest') ||
    requestUrl.pathname === withBase('/version.json')
  ) {
    event.respondWith(
      fetch(new Request(event.request, { cache: 'no-store' }))
        .catch(() => caches.match(event.request))
    )
    return
  }

  if (
    requestUrl.pathname === withBase('/wasm/wasm_exec.js') ||
    requestUrl.pathname === withBase('/wasm/sat20wallet.wasm')
  ) {
    event.respondWith(
      caches.match(event.request, { ignoreSearch: true })
        .then((cachedResponse) => cachedResponse || fetchAndCache(event.request))
    )
    return
  }

  event.respondWith(
    caches.match(event.request).then((cachedResponse) => {
      if (cachedResponse) {
        return cachedResponse
      }

      return fetch(event.request).then((networkResponse) => {
        const responseClone = networkResponse.clone()
        caches.open(CACHE_NAME).then((cache) => {
          cache.put(event.request, responseClone)
        })
        return networkResponse
      })
    })
  )
})
