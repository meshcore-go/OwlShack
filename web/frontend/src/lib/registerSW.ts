// Registers the PWA service worker (public/sw.js). Safe to call once at startup;
// no-ops where unsupported. When a newer SW installs behind the current one (an
// update, not a first install), shows a "reload to update" toast that takes it.
import { toast } from "sonner";

export function registerServiceWorker(): void {
  if (!("serviceWorker" in navigator)) return;

  let updating = false;
  navigator.serviceWorker.addEventListener("controllerchange", () => {
    // Only reload when WE triggered the takeover via the toast. The new SW's
    // clients.claim() on a first install also fires this and must not reload.
    if (updating) window.location.reload();
  });

  window.addEventListener("load", () => {
    navigator.serviceWorker
      .register("/sw.js")
      .then((reg) => {
        reg.addEventListener("updatefound", () => {
          const sw = reg.installing;
          if (!sw) return;
          sw.addEventListener("statechange", () => {
            // installed + an existing controller => an update is waiting.
            if (sw.state === "installed" && navigator.serviceWorker.controller) {
              toast("New version available", {
                description: "Reload to update OwlShack.",
                duration: Infinity,
                action: {
                  label: "Reload",
                  onClick: () => {
                    updating = true;
                    sw.postMessage("SKIP_WAITING");
                  },
                },
              });
            }
          });
        });
      })
      .catch((err) => {
        // Non-fatal: the app works fine without the SW (just not installable).
        console.warn("service worker registration failed", err);
      });
  });
}
