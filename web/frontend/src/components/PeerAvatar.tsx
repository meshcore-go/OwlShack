import { cn } from "@/lib/utils";

const PALETTE = [
  ["bg-emerald-500/15", "text-emerald-400", "border-emerald-500/30"],
  ["bg-cyan-500/15", "text-cyan-400", "border-cyan-500/30"],
  ["bg-amber-500/15", "text-amber-400", "border-amber-500/30"],
  ["bg-rose-500/15", "text-rose-400", "border-rose-500/30"],
  ["bg-violet-500/15", "text-violet-400", "border-violet-500/30"],
  ["bg-sky-500/15", "text-sky-400", "border-sky-500/30"],
  ["bg-lime-500/15", "text-lime-400", "border-lime-500/30"],
  ["bg-orange-500/15", "text-orange-400", "border-orange-500/30"],
  ["bg-fuchsia-500/15", "text-fuchsia-400", "border-fuchsia-500/30"],
  ["bg-teal-500/15", "text-teal-400", "border-teal-500/30"],
] as const;

const EMOJI_RE = /(?:\p{Emoji_Presentation}|\p{Extended_Pictographic})/u;

function hashName(name: string): number {
  let h = 0;
  for (let i = 0; i < name.length; i++) {
    h = ((h << 5) - h + name.charCodeAt(i)) | 0;
  }
  return Math.abs(h);
}

const SIZES = {
  xs: "h-6 w-6 text-[10px]",
  sm: "h-7 w-7 text-[11px]",
  md: "h-9 w-9 text-xs",
  lg: "h-11 w-11 text-sm",
};

interface PeerAvatarProps {
  name: string;
  size?: keyof typeof SIZES;
  className?: string;
}

export function PeerAvatar({ name, size = "md", className }: PeerAvatarProps) {
  const safeName = name || "?";
  const emojiMatch = safeName.match(EMOJI_RE);
  const label = emojiMatch ? emojiMatch[0] : (safeName[0] || "?").toUpperCase();
  const [bg, fg, border] = PALETTE[hashName(safeName) % PALETTE.length];

  return (
    <div
      className={cn(
        "shrink-0 rounded-sm border flex items-center justify-center font-mono font-semibold uppercase select-none",
        SIZES[size],
        bg,
        fg,
        border,
        className,
      )}
    >
      {label}
    </div>
  );
}
