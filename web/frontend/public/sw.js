// OwlShack service worker — installability + light load resilience only.
//
// This is a live, WebSocket-driven ops console: there is nothing useful to show
// fully offline, so we deliberately do NOT precache the whole app or try to be
// an offline app. The strategy is tuned to never serve a stale build or stale
// data:
//   - navigations  -> network-first (always the fresh app when online; the
//                     cached shell is only a last-resort offline fallback)
//   - /assets/*     -> cache-first (Vite content-hashes these, so they're
//                     immutable; a new build = new URLs = cache miss)
//   - everything else (incl. /api/* + WS, manifest, icons, fonts) is left
//     alone: a pure network passthrough. The app is served by a local Go
//     binary, so caching those buys nothing and only risks staleness/growth.
//
// Bump VERSION only when changing THIS FILE's logic (it drops the old cache on
// activate). App/asset freshness does not depend on it — hashed /assets/ URLs
// change per build and navigations are network-first.
const VERSION = "v1";
const CACHE = `owlshack-${VERSION}`;
const SHELL = "/";

self.addEventListener("install", (event) => {
  // Activate this SW immediately rather than waiting for all tabs to close.
  self.skipWaiting();
  // Warm the app-shell so a first-load-then-offline still renders.
  event.waitUntil(
    caches.open(CACHE).then((c) => c.add(SHELL)).catch(() => {}),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys();
      await Promise.all(
        keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)),
      );
      await self.clients.claim();
    })(),
  );
});

function cachePut(request, response) {
  const copy = response.clone();
  caches.open(CACHE).then((c) => c.put(request, copy)).catch(() => {});
  return response;
}

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return;

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return; // 3rd-party (e.g. font CDN)
  if (url.pathname.startsWith("/api/")) return; // live data + WS: passthrough

  // SPA navigations: fresh app when online, cached shell as offline fallback.
  if (request.mode === "navigate") {
    event.respondWith(
      fetch(request)
        .then((res) => cachePut(SHELL, res))
        .catch(() => caches.match(SHELL).then((r) => r || Response.error())),
    );
    return;
  }

  // Immutable, content-hashed build assets: cache-first.
  if (url.pathname.startsWith("/assets/")) {
    event.respondWith(
      caches.match(request).then(
        (hit) => hit || fetch(request).then((res) => cachePut(request, res)),
      ),
    );
    return;
  }

  // Everything else (manifest, icons, fonts, sw.js): no respondWith → the
  // browser fetches normally. They're tiny and served locally; not worth
  // caching, and leaving them uncached avoids unbounded cache growth.
});
