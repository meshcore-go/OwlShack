import { cn } from "@/lib/utils";
import { decodeErrEvents, unknownErrBits } from "@/lib/errEvents";

const CHIP =
  "font-mono text-[10px] uppercase tracking-widest px-1.5 py-0.5 border border-warning/40 text-warning bg-warning/5";

// Renders the active flags of a repeater status `errEvents` bitmask as warning
// chips. Use anywhere the raw _err_flags value would otherwise be shown as a
// number. Renders null when the mask is 0 — callers decide whether to show a
// "none" state in that case.
export function ErrEventBadges({
  mask,
  className,
}: {
  mask: number;
  className?: string;
}) {
  const flags = decodeErrEvents(mask);
  const unknown = unknownErrBits(mask);
  if (flags.length === 0 && unknown === 0) return null;
  return (
    <div className={cn("flex flex-wrap gap-1", className)}>
      {flags.map((f) => (
        <span key={f.name} title={`${f.name} — ${f.description}`} className={CHIP}>
          {f.label}
        </span>
      ))}
      {unknown !== 0 && (
        <span title={`Unknown error bits: 0x${unknown.toString(16)}`} className={CHIP}>
          0x{unknown.toString(16)}
        </span>
      )}
    </div>
  );
}
