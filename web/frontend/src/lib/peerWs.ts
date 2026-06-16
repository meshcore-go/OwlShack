// The "peers" WS topic carries either a full peer upsert or a delete signal
// ({ action: "delete", pubkeys }) broadcast when discovered peers are removed.
// This guard lets each peer view drop them from local state without a refetch.
export interface PeerDeleteMsg {
  action: "delete";
  pubkeys: string[];
}

export function isPeerDelete(value: unknown): value is PeerDeleteMsg {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return v.action === "delete" && Array.isArray(v.pubkeys);
}
