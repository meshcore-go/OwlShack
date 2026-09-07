// Live-runtime channel endpoints, keyed by companion name; lib/configApi.ts is the id-keyed config surface.

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
