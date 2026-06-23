import { useCallback, useEffect, useState } from "react";

// The non-standard beforeinstallprompt event (Chromium only). We capture it,
// suppress the default mini-infobar, and replay it from our own button.
interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed" }>;
}

/**
 * Drives the optional "Install app" button. `canInstall` is true only while the
 * browser has offered an install prompt we can replay (Chromium, installable,
 * not already installed). `promptInstall` shows the native chooser. Everywhere
 * else — already installed, or a browser without the event (e.g. iOS Safari) —
 * the browser never fires `beforeinstallprompt`, so `canInstall` stays false and
 * the button hides itself.
 */
export function useInstallPrompt() {
  const [deferred, setDeferred] = useState<BeforeInstallPromptEvent | null>(
    null,
  );

  useEffect(() => {
    const onPrompt = (e: Event) => {
      e.preventDefault(); // stop the default mini-infobar; we show our own UI
      setDeferred(e as BeforeInstallPromptEvent);
    };
    const onInstalled = () => setDeferred(null);

    window.addEventListener("beforeinstallprompt", onPrompt);
    window.addEventListener("appinstalled", onInstalled);
    return () => {
      window.removeEventListener("beforeinstallprompt", onPrompt);
      window.removeEventListener("appinstalled", onInstalled);
    };
  }, []);

  const promptInstall = useCallback(async () => {
    if (!deferred) return;
    await deferred.prompt();
    // The event is single-use; drop it (accepted → appinstalled also clears,
    // dismissed → the browser re-fires a fresh event on the next engagement).
    setDeferred(null);
  }, [deferred]);

  return { canInstall: deferred !== null, promptInstall };
}
