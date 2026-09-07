// The "peers" WS topic carries either a full peer upsert or this delete signal.
export interface PeerDeleteMsg {
  action: "delete";
  pubkeys: string[];
}

export function isPeerDelete(value: unknown): value is PeerDeleteMsg {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return v.action === "delete" && Array.isArray(v.pubkeys);
}
