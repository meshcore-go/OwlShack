import { useCallback, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { searchEmojis, trackEmojiUse, type EmojiHit } from "@/lib/emoji";
import { cn } from "@/lib/utils";

// Matches a ":shortcode" being typed at the caret (after start or whitespace).
const TRIGGER = /(?:^|\s)(:[a-zA-Z0-9_+-]{2,})$/;

/**
 * Signal-style inline emoji autocomplete for a textarea. Wire handleChange into
 * onChange, handleKeyDown ahead of the textarea's own key handling, and render
 * `dropdown` inside a relatively-positioned composer container.
 */
export function useEmojiAutocomplete({
  textareaRef,
  setText,
}: {
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  setText: (next: string) => void;
}) {
  const [items, setItems] = useState<EmojiHit[]>([]);
  const [index, setIndex] = useState(0);
  const tokenRef = useRef<{ start: number; end: number } | null>(null);
  const seqRef = useRef(0);

  const close = useCallback(() => {
    tokenRef.current = null;
    seqRef.current++;
    setItems([]);
    setIndex(0);
  }, []);

  const handleChange = useCallback(
    (value: string, caret: number) => {
      const m = TRIGGER.exec(value.slice(0, caret));
      if (!m) {
        close();
        return;
      }
      const token = m[1]; // includes leading ':'
      tokenRef.current = { start: caret - token.length, end: caret };
      const seq = ++seqRef.current;
      searchEmojis(token.slice(1), 8).then((hits) => {
        if (seq !== seqRef.current) return; // a newer keystroke superseded this
        setItems(hits);
        setIndex(0);
      });
    },
    [close],
  );

  const accept = useCallback(
    (hit: EmojiHit) => {
      const tok = tokenRef.current;
      const el = textareaRef.current;
      if (!tok || !el) return;
      const v = el.value;
      setText(v.slice(0, tok.start) + hit.native + v.slice(tok.end));
      const pos = tok.start + hit.native.length;
      void trackEmojiUse(hit.id);
      close();
      requestAnimationFrame(() => {
        el.focus();
        el.setSelectionRange(pos, pos);
      });
    },
    [textareaRef, setText, close],
  );

  const handleKeyDown = useCallback(
    (e: KeyboardEvent): boolean => {
      if (items.length === 0) return false;
      switch (e.key) {
        case "ArrowDown":
          e.preventDefault();
          setIndex((i) => (i + 1) % items.length);
          return true;
        case "ArrowUp":
          e.preventDefault();
          setIndex((i) => (i - 1 + items.length) % items.length);
          return true;
        case "Enter":
        case "Tab":
          e.preventDefault();
          accept(items[index]);
          return true;
        case "Escape":
          e.preventDefault();
          close();
          return true;
        default:
          return false;
      }
    },
    [items, index, accept, close],
  );

  const dropdown =
    items.length > 0 ? (
      <div className="absolute bottom-full left-3 mb-1 z-50 max-h-56 w-72 overflow-y-auto border border-border bg-popover shadow-md">
        {items.map((hit, i) => (
          <button
            key={hit.id}
            type="button"
            // mousedown (not click) so accepting doesn't blur the textarea first
            onMouseDown={(e) => {
              e.preventDefault();
              accept(hit);
            }}
            onMouseEnter={() => setIndex(i)}
            className={cn(
              "flex w-full items-center gap-2 px-2.5 py-1.5 text-left",
              i === index ? "bg-muted" : "hover:bg-muted/50",
            )}
          >
            <span className="text-lg leading-none">{hit.native}</span>
            <span className="font-mono text-xs text-muted-foreground truncate">
              :{hit.id}:
            </span>
          </button>
        ))}
      </div>
    ) : null;

  return { handleChange, handleKeyDown, dropdown, close };
}
