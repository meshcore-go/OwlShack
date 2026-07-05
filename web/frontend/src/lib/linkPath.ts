// Resolves a link monitor's saved path (hex hashes) to human-readable
// "<source> → <destination>" hop labels, so SNR tiles/charts read as "who
// heard whom" instead of an opaque "SNR hop N". Hop N's SNR is the strength
// at which the destination (hashes[N-1]) received the packet from the source
// (hashes[N-2], or the local companion for hop 1) — see the link collector's
// snr_hopN reading semantics in internal/app/link_collector.go.

export interface NamedPeer {
  pubkey: string;
  name: string;
}

function hexToHopHashes(pathHex: string, hashSize: number): string[] {
  const step = hashSize * 2;
  if (step <= 0) return [];
  const out: string[] = [];
  for (let i = 0; i + step <= pathHex.length; i += step) {
    out.push(pathHex.slice(i, i + step).toLowerCase());
  }
  return out;
}

function buildPeerByHash(
  peers: NamedPeer[],
  hashSize: number,
): Map<string, NamedPeer> {
  const map = new Map<string, NamedPeer>();
  for (const p of peers) {
    const k = p.pubkey.slice(0, hashSize * 2).toLowerCase();
    if (!map.has(k)) map.set(k, p);
  }
  return map;
}

// FIRST_HOP_METRIC is the "you → <first node>" SNR reading — a local
// companion-to-base-station radio link that's normally rock solid, so users
// can opt to hide it from monitoring charts/tiles via a link's
// ignoreFirstHop setting. Display-only: the collector keeps recording it.
export const FIRST_HOP_METRIC = "snr_hop1";

// LAST_SNR_METRIC is the "SNR" tile/chart (last_snr — the local radio's RX
// SNR of the final returning packet). For an out-and-back path that's the
// same physical leg as the first hop (the return trip is relayed back
// through that same nearby node), so it's exactly as static and
// uninteresting as FIRST_HOP_METRIC, for the same reason — hideable via a
// link's hideLastSnr setting. Display-only: the collector keeps recording it.
export const LAST_SNR_METRIC = "last_snr";

// filterMetrics strips the first-hop and/or last-SNR readings from a metrics
// snapshot per a link's ignoreFirstHop/hideLastSnr settings, for tiles that
// render straight off a metrics map.
export function filterMetrics(
  metrics: Record<string, number>,
  ignoreFirstHop: boolean,
  hideLastSnr: boolean,
): Record<string, number> {
  let out = metrics;
  if (ignoreFirstHop && FIRST_HOP_METRIC in out) {
    const { [FIRST_HOP_METRIC]: _omit, ...rest } = out;
    out = rest;
  }
  if (hideLastSnr && LAST_SNR_METRIC in out) {
    const { [LAST_SNR_METRIC]: _omit, ...rest } = out;
    out = rest;
  }
  return out;
}

// filterMetricNames strips the first-hop and/or last-SNR metric keys from a
// list of available metric names, for pages that drive charts off a name
// list rather than a values snapshot.
export function filterMetricNames(
  names: string[],
  ignoreFirstHop: boolean,
  hideLastSnr: boolean,
): string[] {
  if (!ignoreFirstHop && !hideLastSnr) return names;
  return names.filter(
    (n) =>
      !(ignoreFirstHop && n === FIRST_HOP_METRIC) &&
      !(hideLastSnr && n === LAST_SNR_METRIC),
  );
}

// hopDirectionLabel returns a resolver from 1-indexed hop number to
// "<source> → <destination>", falling back to the hash prefix for an
// unresolved (not-yet-seen) peer.
export function hopDirectionLabel(
  pathHex: string,
  hashSize: number,
  peers: NamedPeer[],
  originName = "you",
): (hop: number) => string {
  const hashes = hexToHopHashes(pathHex, hashSize);
  const byHash = buildPeerByHash(peers, hashSize);
  const nameFor = (hash: string) => byHash.get(hash)?.name || hash;
  return (hop: number) => {
    const toHash = hashes[hop - 1];
    if (!toHash) return `Hop ${hop}`;
    const to = nameFor(toHash);
    const from = hop === 1 ? originName : nameFor(hashes[hop - 2]);
    return `${from} → ${to}`;
  };
}
