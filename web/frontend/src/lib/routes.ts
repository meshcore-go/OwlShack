// Shared SPA route builders, so the "where does a peer live in a companion"
// rule has one source of truth instead of being inlined at each call site.

// A peer of type REPEATER is managed on the repeater admin page; everything
// else opens its contact detail page. Companion name is URL-encoded; pubkeys
// are hex and need no encoding.
export function contactDetailPath(
  companion: string,
  pubkey: string,
  isRepeater: boolean,
): string {
  const base = `/companions/${encodeURIComponent(companion)}`;
  return isRepeater ? `${base}/repeaters/${pubkey}` : `${base}/contacts/${pubkey}`;
}
