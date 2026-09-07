import {
  useCallback,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type RefObject,
} from "react";
import { PeerAvatar } from "@/components/PeerAvatar";
import { cn } from "@/lib/utils";

// Guards against firing inside "foo@bar" or on an already-inserted "@[name]" token.
const TRIGGER = /(?:^|\s)@([^\s@[\]]*)$/;
const MAX_RESULTS = 8;

// Prefix matches rank ahead of substring matches; empty query lists everyone.
function rankNames(names: string[], query: string): string[] {
  if (!query) return names.slice(0, MAX_RESULTS);
  const q = query.toLowerCase();
  const prefix: string[] = [];
  const substr: string[] = [];
  for (const n of names) {
    const i = n.toLowerCase().indexOf(q);
    if (i === 0) prefix.push(n);
    else if (i > 0) substr.push(n);
  }
  return [...prefix, ...substr].slice(0, MAX_RESULTS);
}

// Inserts the renderer's "@[name] " token; wire it up like useEmojiAutocomplete.
export function useMentionAutocomplete({
  textareaRef,
  setText,
  names,
}: {
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  setText: (next: string) => void;
  names: string[];
}) {
  const [items, setItems] = useState<string[]>([]);
  const [index, setIndex] = useState(0);
  const tokenRef = useRef<{ start: number; end: number } | null>(null);

  const close = useCallback(() => {
    tokenRef.current = null;
    setItems([]);
    setIndex(0);
  }, []);

  const handleChange = useCallback(
    (value: string, caret: number) => {
      if (names.length === 0) {
        close();
        return;
      }
      const m = TRIGGER.exec(value.slice(0, caret));
      if (!m) {
        close();
        return;
      }
      const query = m[1];
      const hits = rankNames(names, query);
      if (hits.length === 0) {
        close();
        return;
      }
      // +1 for the leading '@' the query excludes.
      tokenRef.current = { start: caret - (query.length + 1), end: caret };
      setItems(hits);
      setIndex(0);
    },
    [names, close],
  );

  const accept = useCallback(
    (name: string) => {
      const tok = tokenRef.current;
      const el = textareaRef.current;
      if (!tok || !el) return;
      const v = el.value;
      const insert = `@[${name}] `;
      setText(v.slice(0, tok.start) + insert + v.slice(tok.end));
      const pos = tok.start + insert.length;
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

  // Memoized: the chat page re-renders on every WS message.
  const dropdown = useMemo(() => {
    if (items.length === 0) return null;
    return (
      <div className="absolute bottom-full left-3 mb-1 z-50 max-h-56 w-64 overflow-y-auto border border-border bg-popover shadow-md">
        {items.map((name, i) => (
          <button
            key={name}
            type="button"
            // mousedown (not click) so accepting doesn't blur the textarea first
            onMouseDown={(e) => {
              e.preventDefault();
              accept(name);
            }}
            onMouseEnter={() => setIndex(i)}
            className={cn(
              "flex w-full items-center gap-2 px-2.5 py-1.5 text-left",
              i === index ? "bg-muted" : "hover:bg-muted/50",
            )}
          >
            <PeerAvatar name={name} size="xs" />
            <span className="font-mono text-xs truncate">{name}</span>
          </button>
        ))}
      </div>
    );
  }, [items, index, accept]);

  return { handleChange, handleKeyDown, dropdown, close };
}
