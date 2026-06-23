// Registers the PWA service worker (public/sw.js) for installability + light
// load resilience. Kept tiny and dependency-free on purpose — see sw.js for the
// caching strategy. Safe to call once at startup; no-ops where unsupported.
export function registerServiceWorker(): void {
  if (!("serviceWorker" in navigator)) return;
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch((err) => {
      // Non-fatal: the app works fine without the SW (just not installable).
      console.warn("service worker registration failed", err);
    });
  });
}
