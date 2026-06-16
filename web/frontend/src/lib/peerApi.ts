// Discovered-peer deletion. Deletes are guarded server-side: a peer saved as a
// contact of any companion is refused (single) or skipped (bulk) — contacts are
// removed from the Contacts page, never by peer cleanup. The "peers" WS topic
// broadcasts the removal so live views prune without a refetch.

export interface DeletePeersResult {
  deleted: number;
  skipped: number;
}

export async function deletePeer(pubkey: string): Promise<void> {
  const res = await fetch(`/api/peers/${pubkey.toLowerCase()}`, {
    method: "DELETE",
  });
  if (!res.ok) {
    const e = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(e.error || `HTTP ${res.status}`);
  }
}

export async function deletePeers(
  pubkeys: string[],
): Promise<DeletePeersResult> {
  const res = await fetch("/api/peers/delete", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ pubkeys }),
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const j = (await res.json().catch(() => ({}))) as Partial<DeletePeersResult>;
  return { deleted: j.deleted ?? pubkeys.length, skipped: j.skipped ?? 0 };
}

export function deletedPeersMessage({
  deleted,
  skipped,
}: DeletePeersResult): string {
  return (
    `Deleted ${deleted} peer${deleted === 1 ? "" : "s"}` +
    (skipped > 0
      ? ` · skipped ${skipped} saved contact${skipped === 1 ? "" : "s"}`
      : "")
  );
}
