// A peer of type REPEATER is managed on the repeater admin page, not a contact page.
export function contactDetailPath(
  companion: string,
  pubkey: string,
  isRepeater: boolean,
): string {
  const base = `/companions/${encodeURIComponent(companion)}`;
  return isRepeater ? `${base}/repeaters/${pubkey}` : `${base}/contacts/${pubkey}`;
}
