// emoji-mart's dataset is large; dynamic-importing it keeps it out of the initial bundle.
let initPromise: Promise<void> | null = null;

export function ensureEmojiInit(): Promise<void> {
  if (!initPromise) {
    initPromise = (async () => {
      const [{ init }, data] = await Promise.all([
        import("emoji-mart"),
        import("@emoji-mart/data"),
      ]);
      init({ data: data.default });
    })();
  }
  return initPromise;
}

export interface EmojiHit {
  id: string;
  native: string;
  name: string;
}

interface RawEmoji {
  id: string;
  name: string;
  skins?: { native?: string }[];
}

// The picker records its own selections; this covers the inline ":" autocomplete path.
export async function trackEmojiUse(id: string): Promise<void> {
  if (!id) return;
  await ensureEmojiInit();
  const { FrequentlyUsed } = await import("emoji-mart");
  FrequentlyUsed.add({ id });
}

export async function searchEmojis(query: string, limit = 8): Promise<EmojiHit[]> {
  await ensureEmojiInit();
  const { SearchIndex } = await import("emoji-mart");
  const results = (await SearchIndex.search(query)) as RawEmoji[] | null;
  if (!results) return [];
  return results
    .slice(0, limit)
    .map((e) => ({ id: e.id, native: e.skins?.[0]?.native ?? "", name: e.name }))
    .filter((e) => e.native);
}
