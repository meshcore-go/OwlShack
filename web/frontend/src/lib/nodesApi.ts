// Client for the live-runtime node endpoints (`/api/nodes/...`). Sibling of
// lib/channelsApi.ts; see lib/configApi.ts for the relational config surface.

// Trigger an out-of-band poll of a monitored node. Resolves on success; throws
// an Error carrying the server's message (or `poll failed (<status>)`) on
// failure, so each caller owns its own busy state + toast. Success pushes fresh
// metrics over WS, which updates the monitoring views live.
export async function pollNode(pubkey: string): Promise<void> {
  const r = await fetch(`/api/nodes/${pubkey}/poll`, { method: "POST" });
  if (!r.ok) {
    const body = await r.json().catch(() => ({}));
    throw new Error(body.error || `poll failed (${r.status})`);
  }
}
