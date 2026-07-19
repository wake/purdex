// spa/public/sw.js
// Purdex web PWA service worker — minimal, network-first, version-aware.
// Purpose: installability + courteous offline shell load. It NEVER caches API
// or WS traffic and always prefers the network, so the SPA stays version-
// matched to the daemon (no stale bundle). Bump CACHE to invalidate old shells.
const CACHE = 'purdex-shell-v1'
const SHELL = '/'

self.addEventListener('install', (event) => {
  self.skipWaiting()
  event.waitUntil(
    caches.open(CACHE).then((c) => c.add(SHELL)).catch(() => {}),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  const url = new URL(req.url)

  // Only same-origin GET. Never touch API/WS — let them hit the network directly.
  if (req.method !== 'GET' || url.origin !== self.location.origin) return
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/ws/')) return

  // Navigations: network-first; fall back to the cached shell only when offline.
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req)
        .then((res) => {
          // Only persist a healthy HTML shell — never cache a 500 / error page
          // / redirect body as the offline fallback.
          const ct = res.headers.get('content-type') || ''
          if (res.status === 200 && ct.includes('text/html')) {
            const clone = res.clone()
            caches.open(CACHE).then((c) => c.put(SHELL, clone)).catch(() => {})
          }
          return res
        })
        .catch(() =>
          caches.open(CACHE).then((c) => c.match(SHELL)).then((r) => r || Response.error()),
        ),
    )
    return
  }

  // Static assets: network-first with opportunistic cache for offline shell.
  // Skip Range requests and non-200 (e.g. 206 Partial Content) — only full,
  // successful same-origin static responses are cacheable.
  if (req.headers.has('range')) return
  event.respondWith(
    fetch(req)
      .then((res) => {
        if (res.status === 200) {
          const clone = res.clone()
          caches.open(CACHE).then((c) => c.put(req, clone)).catch(() => {})
        }
        return res
      })
      .catch(() => caches.open(CACHE).then((c) => c.match(req)).then((r) => r || Response.error())),
  )
})
