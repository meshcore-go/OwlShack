// Client for the live-runtime channel endpoints (`/api/companions/{name}/...`,
// keyed by companion name). Distinct from lib/configApi.ts, which drives the
// relational *config* surface (`/api/config/...`, keyed by surrogate id).

// Subscribe a companion to a channel. Resolves on success; throws an Error
// carrying the server's message (or `HTTP <status>`) on failure, so each caller
// owns its own toast + post-add behaviour (reload, navigate, …).
export async function postChannel(
  companion: string,
  name: string,
  privateKey?: string,
): Promise<void> {
  const body: { name: string; privateKey?: string } = { name };
  if (privateKey) body.privateKey = privateKey;
  const res = await fetch(
    `/api/companions/${encodeURIComponent(companion)}/channels`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(err.error || `HTTP ${res.status}`);
  }
}
