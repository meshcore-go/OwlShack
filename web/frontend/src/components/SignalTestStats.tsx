import type { SignalTestStats as Stats } from "@/lib/signalTestApi";

// Per-hop stats table shared by the live test panel, the saved-test detail
// view, and the multi-test compare table. hopLabel lets callers resolve a hop
// index to a repeater name (falls back to "Hop N").
export function SignalTestStats({
  stats,
  hopLabel,
}: {
  stats: Stats;
  hopLabel?: (hop: number) => string;
}) {
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-3 gap-px bg-border border border-border font-mono text-[11px]">
        <MiniStat label="Runs" value={String(stats.total)} />
        <MiniStat
          label="Success"
          value={`${(stats.successRate * 100).toFixed(0)}%`}
        />
        <MiniStat label="Timeouts" value={String(stats.timeouts)} />
      </div>

      {stats.perHop.length > 0 ? (
        <table className="w-full font-mono text-[11px] border border-border">
          <thead>
            <tr className="bg-muted/40 text-muted-foreground/70 uppercase tracking-[0.08em]">
              <th className="text-left px-2 py-1 font-normal">Hop</th>
              <th className="text-right px-2 py-1 font-normal">N</th>
              <th className="text-right px-2 py-1 font-normal">Mean</th>
              <th className="text-right px-2 py-1 font-normal">Stddev</th>
              <th className="text-right px-2 py-1 font-normal">Min</th>
              <th className="text-right px-2 py-1 font-normal">Max</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border/60">
            {stats.perHop.map((h) => (
              <tr key={h.hop}>
                <td className="px-2 py-1 truncate max-w-40">
                  {hopLabel ? hopLabel(h.hop) : `Hop ${h.hop}`}
                </td>
                <td className="px-2 py-1 text-right tabular-nums">{h.n}</td>
                <td className="px-2 py-1 text-right tabular-nums">
                  {h.mean.toFixed(1)}
                </td>
                <td className="px-2 py-1 text-right tabular-nums">
                  {h.stddev.toFixed(1)}
                </td>
                <td className="px-2 py-1 text-right tabular-nums">
                  {h.min.toFixed(1)}
                </td>
                <td className="px-2 py-1 text-right tabular-nums">
                  {h.max.toFixed(1)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <div className="text-center py-4 text-sm text-muted-foreground/60">
          No hop data yet
        </div>
      )}

      {(stats.finalSnr || stats.elapsedMs) && (
        <div className="grid grid-cols-2 gap-px bg-border border border-border font-mono text-[11px]">
          {stats.finalSnr && (
            <MiniStat
              label="Final SNR · mean"
              value={`${stats.finalSnr.mean.toFixed(1)} dB`}
            />
          )}
          {stats.elapsedMs && (
            <MiniStat
              label="Elapsed · mean"
              value={`${stats.elapsedMs.mean.toFixed(0)} ms`}
            />
          )}
        </div>
      )}
    </div>
  );
}

function MiniStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-card px-2 py-2 space-y-0.5">
      <div className="label-overline text-[9px]">{label}</div>
      <div className="tabular-nums">{value}</div>
    </div>
  );
}
