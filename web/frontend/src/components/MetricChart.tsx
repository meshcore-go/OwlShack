import { useId, useMemo } from "react";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { cn } from "@/lib/utils";
import { formatMetric, metricDef, metricLabel } from "@/lib/metrics";

export interface SeriesPoint {
  ts: number;
  value: number;
}

// toDeltas turns a cumulative counter series into per-bucket deltas (rate of
// change). A counter reset (node reboot) yields a negative diff → clamped to 0.
function toDeltas(data: SeriesPoint[]): SeriesPoint[] {
  const out: SeriesPoint[] = [];
  for (let i = 1; i < data.length; i++) {
    const d = data[i].value - data[i - 1].value;
    out.push({ ts: data[i].ts, value: d > 0 ? d : 0 });
  }
  return out;
}

// yTickFormatter keeps axis labels readable across magnitudes: abbreviate only
// genuinely large values (so a tight 4029–4032 battery range doesn't collapse to
// "4k 4k 4k"), and trim noise from fractional buckets.
function yTickFormatter(v: number): string {
  const a = Math.abs(v);
  if (a >= 10000) return `${(v / 1000).toFixed(0)}k`;
  if (Number.isInteger(v)) return `${v}`;
  return v.toFixed(a < 10 ? 1 : 0);
}

function xTickFormatter(spanSecs: number) {
  return (ts: number) => {
    const d = new Date(ts * 1000);
    if (spanSecs <= 86400)
      return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
    if (spanSecs <= 30 * 86400)
      return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
    return d.toLocaleDateString(undefined, { month: "short", year: "2-digit" });
  };
}

// MetricChart renders one metric, self-scaled, with its own title + value.
// Gauges (instantaneous values) draw an area chart of the raw value; counters
// (monotonic totals) draw bars of the per-bucket delta — a raw counter line only
// ever climbs and tells you nothing. Presentational: the parent passes `data`.
export function MetricChart({
  metric,
  data,
  spanSecs,
  height = 180,
  variant = "compact",
  className,
}: {
  metric: string;
  data: SeriesPoint[];
  spanSecs: number;
  height?: number;
  variant?: "hero" | "compact";
  className?: string;
}) {
  const def = metricDef(metric);
  const gradId = useId();
  const hero = variant === "hero";
  const isCounter = def.kind === "counter";

  // Header shows the current value (gauge) or the cumulative total (counter).
  const lastRaw = data.length ? data[data.length - 1].value : undefined;
  // Counters are plotted as per-bucket deltas; gauges as the raw value.
  const chartData = useMemo(
    () => (isCounter ? toDeltas(data) : data),
    [isCounter, data],
  );

  // Axis + tooltip elements are shared by both chart types (recharts flattens
  // an array of children, so this keeps them in sync without duplication).
  // Memoized — a detail page renders 10+ charts and these four elements don't
  // depend on `data`, so unrelated re-renders shouldn't rebuild them.
  const sharedAxes = useMemo(() => [
    <CartesianGrid key="g" stroke="var(--border)" strokeDasharray="2 4" vertical={false} />,
    <XAxis
      key="x"
      dataKey="ts"
      tickFormatter={xTickFormatter(spanSecs)}
      tick={{ fill: "var(--muted-foreground)", fontSize: 9 }}
      stroke="var(--border)"
      minTickGap={hero ? 48 : 60}
      interval="preserveStartEnd"
    />,
    <YAxis
      key="y"
      tick={{ fill: "var(--muted-foreground)", fontSize: 9 }}
      stroke="var(--border)"
      width={46}
      domain={isCounter ? [0, "auto"] : ["auto", "auto"]}
      allowDecimals={!isCounter}
      tickFormatter={yTickFormatter}
    />,
    <Tooltip
      key="t"
      contentStyle={{
        background: "var(--popover)",
        border: "1px solid var(--border)",
        borderRadius: 0,
        fontFamily: "var(--font-mono, monospace)",
        fontSize: 11,
        padding: "4px 8px",
      }}
      labelFormatter={(ts) => new Date((ts as number) * 1000).toLocaleString()}
      formatter={(value: number) => [
        isCounter ? `+${formatMetric(metric, value)}` : formatMetric(metric, value),
        isCounter ? `${metricLabel(metric)} · Δ` : metricLabel(metric),
      ]}
    />,
  ], [spanSecs, hero, isCounter, metric]);

  return (
    <div className={cn("panel flex flex-col", className)}>
      <div className="flex items-baseline justify-between px-3 pt-2.5 pb-1.5">
        <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
          {metricLabel(metric)}
          {isCounter ? (
            <span className="text-muted-foreground/40"> · rate</span>
          ) : def.unit ? (
            <span className="text-muted-foreground/40"> · {def.unit}</span>
          ) : null}
        </span>
        <span
          className={cn("font-mono tabular-nums", hero ? "text-lg" : "text-sm")}
          style={{ color: def.color }}
        >
          {lastRaw !== undefined ? formatMetric(metric, lastRaw) : "—"}
        </span>
      </div>
      <div style={{ height }} className="px-1 pb-1">
        {chartData.length === 0 ? (
          <div className="h-full grid place-items-center">
            <span className="font-mono text-[10px] uppercase tracking-[0.1em] text-muted-foreground/50">
              {isCounter && data.length > 0 ? "awaiting samples" : "no data"}
            </span>
          </div>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            {isCounter ? (
              <BarChart data={chartData} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
                {sharedAxes}
                <Bar
                  dataKey="value"
                  fill={def.color}
                  fillOpacity={0.85}
                  maxBarSize={28}
                  isAnimationActive={false}
                />
              </BarChart>
            ) : (
              <AreaChart data={chartData} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
                <defs>
                  <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={def.color} stopOpacity={0.35} />
                    <stop offset="100%" stopColor={def.color} stopOpacity={0.02} />
                  </linearGradient>
                </defs>
                {sharedAxes}
                <Area
                  type="linear"
                  dataKey="value"
                  stroke={def.color}
                  strokeWidth={1.5}
                  fill={`url(#${gradId})`}
                  dot={chartData.length <= 12 ? { r: 1.5, fill: def.color } : false}
                  isAnimationActive={false}
                  connectNulls
                />
              </AreaChart>
            )}
          </ResponsiveContainer>
        )}
      </div>
    </div>
  );
}

// Sparkline is a tiny, axis-less trend line for use inside cards/rows.
export function Sparkline({
  data,
  color,
  height = 28,
}: {
  data: SeriesPoint[];
  color: string;
  height?: number;
}) {
  const gradId = useId();
  if (data.length < 2) {
    return <div style={{ height }} className="w-full" />;
  }
  return (
    <div style={{ height }} className="w-full">
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 2, right: 0, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={color} stopOpacity={0.4} />
              <stop offset="100%" stopColor={color} stopOpacity={0} />
            </linearGradient>
          </defs>
          <Area
            type="linear"
            dataKey="value"
            stroke={color}
            strokeWidth={1.25}
            fill={`url(#${gradId})`}
            dot={false}
            isAnimationActive={false}
            connectNulls
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
