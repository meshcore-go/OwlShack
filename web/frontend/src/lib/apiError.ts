// The Go API writes errors as {"error":"..."}.
export async function apiErrorMessage(r: Response, fallback?: string): Promise<string> {
  const txt = await r.text().catch(() => "");
  try {
    const j = JSON.parse(txt) as { error?: unknown };
    if (typeof j?.error === "string" && j.error) return j.error;
  } catch {
    /* not JSON */
  }
  return txt.trim() || fallback || `HTTP ${r.status}`;
}
