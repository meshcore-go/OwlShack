// Server-side guard: a peer saved as any companion's contact is refused (single) or skipped (bulk).

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
