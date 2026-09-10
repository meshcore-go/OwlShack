// Resolves a link monitor's saved path to "<source> → <destination>" hop labels.

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

// buildPeerCandidatesByHash keeps every peer sharing a hop hash. A hash is a pubkey prefix, so it
// is not unique: on a 338-peer mesh, 92 one-byte hashes match more than one node and the worst
// matches five. A caller that renders one name is asserting a node the packet never identified.
export function buildPeerCandidatesByHash(
  peers: NamedPeer[],
  hashSize: number,
): Map<string, NamedPeer[]> {
  const map = new Map<string, NamedPeer[]>();
  for (const p of peers) {
    const k = p.pubkey.slice(0, hashSize * 2).toLowerCase();
    const at = map.get(k);
    if (at) at.push(p);
    else map.set(k, [p]);
  }
  return map;
}

// buildPeerByHash keeps the first peer per hash, discarding the rest. Prefer
// buildPeerCandidatesByHash where the display can show that a hash was ambiguous.
export function buildPeerByHash(
  peers: NamedPeer[],
  hashSize: number,
): Map<string, NamedPeer> {
  const map = new Map<string, NamedPeer>();
  for (const [k, v] of buildPeerCandidatesByHash(peers, hashSize)) {
    map.set(k, v[0]);
  }
  return map;
}

// Display-only toggles; the collector still records both readings.
export const FIRST_HOP_METRIC = "snr_hop1";
export const LAST_SNR_METRIC = "last_snr";

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

// Hop numbers are 1-indexed.
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
