import { toast } from "sonner";

export function registerServiceWorker(): void {
  if (!("serviceWorker" in navigator)) return;

  let updating = false;
  navigator.serviceWorker.addEventListener("controllerchange", () => {
    // A first install's clients.claim() fires this too and must not reload.
    if (updating) window.location.reload();
  });

  window.addEventListener("load", () => {
    navigator.serviceWorker
      .register("/sw.js")
      .then((reg) => {
        const offer = (sw: ServiceWorker) =>
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
        if (reg.waiting && navigator.serviceWorker.controller) offer(reg.waiting);
        reg.addEventListener("updatefound", () => {
          const sw = reg.installing;
          if (!sw) return;
          sw.addEventListener("statechange", () => {
            if (sw.state === "installed" && navigator.serviceWorker.controller) offer(sw);
          });
        });
      })
      .catch((err) => {
        console.warn("service worker registration failed", err);
      });
  });
}
