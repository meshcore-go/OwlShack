import { useState } from "react";
import { Hash, Plus, X } from "lucide-react";
import { Field } from "@/components/ConfigFields";
import {
  Popover,
  PopoverAnchor,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";

// Token-style channel picker. The list is limited to channels already known to
// the selected companion (its standalone channels + any channel its triggers
// use + Public). Creating a brand-new channel inline is deliberately not
// offered here: a free-typed name would silently become a public hashtag
// channel (key derived from the name), which is confusing without explaining
// it on screen. Define new channels on the companion's Channels page instead.
export function ChannelMultiSelect({
  label,
  hint,
  selected,
  options,
  onChange,
}: {
  label: string;
  hint?: React.ReactNode;
  selected: string[];
  options: string[];
  onChange: (next: string[]) => void;
}) {
  const [open, setOpen] = useState(false);

  // Channel names are case-sensitive (a hashtag channel's key is derived from
  // the exact name), so compare exactly — "#Foo" and "#foo" are different
  // channels and must not be merged.
  const has = (name: string) => selected.some((c) => c === name);

  const add = (name: string) => {
    if (has(name)) return;
    onChange([...selected, name]);
  };
  const remove = (name: string) =>
    onChange(selected.filter((c) => c !== name));

  const available = options.filter((o) => !has(o));

  return (
    <Field label={label} hint={hint}>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverAnchor asChild>
          <div className="flex flex-wrap items-center gap-1.5 min-h-9 border border-border bg-background p-1.5">
            {selected.length === 0 && (
              <span className="font-mono text-xs text-muted-foreground/50 px-1">
                no channels selected
              </span>
            )}
            {selected.map((c) => (
              <span
                key={c}
                className="inline-flex items-center gap-1 rounded-sm border border-primary/30 bg-primary/10 px-1.5 py-0.5 font-mono text-xs text-primary"
              >
                {c}
                <button
                  type="button"
                  onClick={() => remove(c)}
                  aria-label={`Remove ${c}`}
                  className="text-primary/60 hover:text-primary"
                >
                  <X className="size-3" />
                </button>
              </span>
            ))}
            <PopoverTrigger asChild>
              <button
                type="button"
                disabled={available.length === 0}
                className="inline-flex items-center gap-1 rounded-sm border border-dashed border-border px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-widest text-muted-foreground hover:border-primary/40 hover:text-foreground disabled:opacity-40 disabled:hover:border-border disabled:hover:text-muted-foreground"
              >
                <Plus className="size-3" />
                add channel
              </button>
            </PopoverTrigger>
          </div>
        </PopoverAnchor>

        <PopoverContent
          align="start"
          className="w-64 rounded-none p-0"
          onOpenAutoFocus={(e) => e.preventDefault()}
        >
          <div className="max-h-56 overflow-y-auto py-1">
            {available.map((o) => (
              <button
                type="button"
                key={o}
                onClick={() => add(o)}
                className="flex w-full items-center gap-2 px-2 py-1.5 text-left font-mono text-sm text-foreground/90 hover:bg-muted/50"
              >
                <Hash className="size-3.5 shrink-0 text-muted-foreground/60" />
                <span className="truncate">{o}</span>
              </button>
            ))}
            {available.length === 0 && (
              <div className="px-2 py-3 font-mono text-xs text-muted-foreground/50">
                all channels added
              </div>
            )}
          </div>
        </PopoverContent>
      </Popover>
    </Field>
  );
}
