const CACHE_NAME = 'sat20-wallet-pwa-v0.1.32'

const PRECACHE_URLS = [
  '/',
  '/index.html',
  '/wasm/wasm_exec.js',
  '/wasm/sat20wallet.wasm',
  '/wasm/stpd.wasm',
  '/icon/apple-touch-icon.png',
  '/icon/sat20-logo-app.png',
  '/icon/maskable-512.png',
  '/icon/maskable-192.png',
  '/icon/512.png',
  '/icon/192.png',
  '/icon/128.png',
  '/icon/48.png',
  '/icon/32.png',
  '/icon/16.png'
]

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME)
      .then((cache) => cache.addAll(PRECACHE_URLS))
      .then(() => self.skipWaiting())
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(
        keys
          .filter((key) => key !== CACHE_NAME)
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
      fetch(event.request).catch(() => caches.match('/index.html'))
    )
    return
  }

  if (requestUrl.pathname === '/manifest.webmanifest' || requestUrl.pathname === '/version.json') {
    event.respondWith(
      fetch(event.request).catch(() => caches.match(event.request))
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
