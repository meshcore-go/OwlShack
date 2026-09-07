// Live-runtime node endpoints; lib/configApi.ts is the relational config surface.

// A successful poll pushes fresh metrics over WS rather than returning them.
export async function pollNode(pubkey: string): Promise<void> {
  const r = await fetch(`/api/nodes/${pubkey}/poll`, { method: "POST" });
  if (!r.ok) {
    const body = await r.json().catch(() => ({}));
    throw new Error(body.error || `poll failed (${r.status})`);
  }
}
