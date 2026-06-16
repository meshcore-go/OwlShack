import { useMemo, useState } from "react";
import type { PeerLike } from "@/components/PeerDetailSheet";

// Encapsulates the select-a-peer → open-PeerDetailSheet wiring repeated in
// PeersPage, MapPage and DashboardPage: a selected pubkey, the resolved peer,
// and the controlled-sheet props. Spread `sheetProps` onto <PeerDetailSheet>
// and add `companions` at the call site.
export function usePeerDetailSheet<T extends PeerLike>(peers: T[]) {
  const [selectedKey, setSelectedKey] = useState<string | null>(null);

  const selected = useMemo(
    () => peers.find((p) => p.pubkey === selectedKey) ?? null,
    [peers, selectedKey],
  );

  return {
    selectPeer: setSelectedKey,
    sheetProps: {
      peer: selected,
      open: !!selectedKey,
      onOpenChange: (open: boolean) => {
        if (!open) setSelectedKey(null);
      },
    },
  };
}
