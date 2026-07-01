// OwlShack service worker — installability only. This is a live WebSocket ops
// console: nothing useful shows offline, so we don't precache the app. The
// strategy only avoids ever serving a stale build; each fetch branch below
// carries its own rule.
//
// VERSION is the build version, stamped into dist/sw.js by build.sh (replacing
// __BUILD_VERSION__). Changing every release is the point: a byte-identical
// sw.js is never re-installed, so this is what triggers the update toast. It
// also names the cache (old caches dropped on activate). A plain `vite build`
// leaves the literal token — harmless.
const VERSION = "__BUILD_VERSION__";
const CACHE = `owlshack-${VERSION}`;
const SHELL = "/";

self.addEventListener("install", (event) => {
  // No skipWaiting: an update stays "waiting" until the user accepts the toast
  // (the message listener below triggers it). First install still activates.
  event.waitUntil(
    caches.open(CACHE).then((c) => c.add(SHELL)).catch(() => {}), // warm shell
  );
});

self.addEventListener("message", (event) => {
  // Sent by registerSW.ts when the user clicks "reload" on the update toast.
  if (event.data === "SKIP_WAITING") self.skipWaiting();
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

  // Everything else (manifest, icons, fonts, sw.js): no respondWith → browser
  // fetches normally. Tiny + local, and staying uncached avoids cache growth.
});
