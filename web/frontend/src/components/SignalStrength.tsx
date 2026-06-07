import { cn } from "@/lib/utils";

export function snrLevel(snr: number): 0 | 1 | 2 | 3 | 4 {
  if (snr >= 10) return 4;
  if (snr >= 5) return 3;
  if (snr >= 0) return 2;
  if (snr >= -10) return 1;
  return 0;
}

export function snrTextClass(snr: number): string {
  if (snr >= 0) return "text-success";
  if (snr >= -10) return "text-warning";
  return "text-destructive";
}

export function snrFill(snr: number): string {
  if (snr >= 0) return "var(--signal-strong)";
  if (snr >= -10) return "var(--signal-weak)";
  return "var(--signal-dead)";
}

interface SignalStrengthProps {
  snr?: number | null;
  size?: "sm" | "md";
  showLabel?: boolean;
  className?: string;
}

export function SignalStrength({
  snr,
  size = "sm",
  showLabel = true,
  className,
}: SignalStrengthProps) {
  if (snr == null) {
    return (
      <span className={cn("text-muted-foreground/50 font-mono text-xs", className)}>
        ——
      </span>
    );
  }

  const level = snrLevel(snr);
  const fill = snrFill(snr);
  const dim = "color-mix(in oklch, var(--foreground) 12%, transparent)";

  const isSm = size === "sm";
  const w = isSm ? 14 : 18;
  const h = isSm ? 12 : 16;
  const barW = isSm ? 2 : 2.5;
  const gap = isSm ? 3.5 : 4.5;
  const bars = [
    { x: 0, h: isSm ? 3 : 4 },
    { x: gap, h: isSm ? 5.5 : 7 },
    { x: gap * 2, h: isSm ? 8 : 11 },
    { x: gap * 3, h: isSm ? 12 : 16 },
  ];

  return (
    <div
      className={cn(
        "inline-flex items-center font-mono",
        isSm ? "gap-1" : "gap-1.5",
        className,
      )}
    >
      <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} aria-hidden>
        {bars.map((bar, i) => (
          <rect
            key={i}
            x={bar.x}
            y={h - bar.h}
            width={barW}
            height={bar.h}
            fill={i < level ? fill : dim}
          />
        ))}
      </svg>
      {showLabel && (
        <span className={cn(isSm ? "text-[10px]" : "text-xs", snrTextClass(snr))}>
          {snr.toFixed(1)}dB
        </span>
      )}
    </div>
  );
}
