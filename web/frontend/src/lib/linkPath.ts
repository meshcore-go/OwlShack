// Resolves a link monitor's saved path to "<source> → <destination>" hop
// labels for SNR tiles/charts.

export interface NamedPeer {
  pubkey: string;
  name: string;
}

export function hexToHopHashes(pathHex: string, hashSize: number): string[] {
  const step = hashSize * 2;
  if (step <= 0) return [];
  const out: string[] = [];
  for (let i = 0; i + step <= pathHex.length; i += step) {
    out.push(pathHex.slice(i, i + step).toLowerCase());
  }
  return out;
}

export function buildPeerByHash(
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

// Display-only toggles (collector still records both readings): the "you →
// first node" leg is the local radio, normally rock solid and just clutter.
export const FIRST_HOP_METRIC = "snr_hop1";
export const LAST_SNR_METRIC = "last_snr";

// Strips the first-hop/last-SNR readings per a link's ignoreFirstHop/hideLastSnr settings.
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

// Same as filterMetrics but for a name list (pages that drive charts off available names).
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

// Resolves a 1-indexed hop number to "<source> → <destination>", falling
// back to the hash prefix for an unresolved peer.
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
