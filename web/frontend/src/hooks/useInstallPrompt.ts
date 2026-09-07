import { useCallback, useEffect, useState } from "react";

// The non-standard beforeinstallprompt event (Chromium only), captured to replay from our own button.
interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed" }>;
}

// `canInstall` stays false wherever the event never fires (already installed, iOS Safari).
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
    // The event is single-use; a dismissal re-fires a fresh one on the next engagement.
    setDeferred(null);
  }, [deferred]);

  return { canInstall: deferred !== null, promptInstall };
}
