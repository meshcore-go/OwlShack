import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft, SlidersHorizontal } from "lucide-react";
import { useWebSocket } from "@/hooks/useWebSocket";
import { PageHeader } from "@/components/PageHeader";
import { ConnectionPill } from "@/components/StatusIndicator";
import { NodeStatGrid } from "@/components/NodeStatTiles";
import { MetricChart, type SeriesPoint } from "@/components/MetricChart";
import { TrackMap, type TrackPoint } from "@/components/TrackMap";
import { MonitoringSettings } from "@/components/MonitoringSettings";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { formatSecsAgo } from "@/lib/format";
import { metricDef, metricOrderIndex } from "@/lib/metrics";

interface MonitoredNode {
  pubkey: string;
  companionId: string;
  kind: string;
  name: string;
  lastPollTs: number;
  lastOkTs: number;
  lastError?: string;
  metrics: Record<string, number>;
}

interface WsMetrics {
  pubkey: string;
  companionId: string;
  kind: string;
  name: string;
  ts: number;
  metrics: Record<string, number>;
}

interface RangePreset {
  key: string;
  label: string;
  spanSecs: number;
  bucketSecs: number;
}

const RANGES: RangePreset[] = [
  { key: "24h", label: "24h", spanSecs: 86400, bucketSecs: 300 },
  { key: "7d", label: "7d", spanSecs: 7 * 86400, bucketSecs: 3600 },
  { key: "30d", label: "30d", spanSecs: 30 * 86400, bucketSecs: 21600 },
  { key: "1y", label: "1y", spanSecs: 365 * 86400, bucketSecs: 86400 },
];

const nowSecs = () => Math.floor(Date.now() / 1000);

// The two headline metrics get large hero charts; everything else lands in the
// grid below, in the canonical METRIC_ORDER (power → signal → sensors → health
// → traffic → errors). Unknown keys sort to the end, alphabetically, so the
// order is always stable and sensible.
const HERO_METRICS = ["battery_mv", "last_snr"];

// lat/lon are consumed by the track map, not charted (a lat-vs-time line is
// meaningless). Altitude stays — it's a real elevation profile over time.
const TRACK_METRICS = new Set(["location_lat", "location_lon"]);

function orderGridMetrics(available: string[]): string[] {
  const hero = new Set(HERO_METRICS);
  return available
    // noChart metrics (e.g. uptime) live only as stat tiles in NodeStatGrid.
    .filter((m) => !hero.has(m) && !TRACK_METRICS.has(m) && !metricDef(m).noChart)
    .sort((a, b) => {
      const ia = metricOrderIndex(a);
      const ib = metricOrderIndex(b);
      if (ia === ib) return a.localeCompare(b);
      return (ia < 0 ? Infinity : ia) - (ib < 0 ? Infinity : ib);
    });
}

export function MonitoringDetailPage() {
  const { pubkey = "" } = useParams();
  const [node, setNode] = useState<MonitoredNode | null>(null);
  const [available, setAvailable] = useState<string[]>([]);
  const [series, setSeries] = useState<Record<string, SeriesPoint[]>>({});
  const [range, setRange] = useState<RangePreset>(RANGES[0]);
  const [loading, setLoading] = useState(true);
  const [showSettings, setShowSettings] = useState(false);

  const loadMeta = useCallback(() => {
    Promise.all([
      fetch("/api/nodes/monitored").then((r) => (r.ok ? r.json() : [])),
      fetch(`/api/nodes/${pubkey}/metrics`).then((r) => (r.ok ? r.json() : [])),
    ])
      .then(([nodes, names]: [MonitoredNode[], string[]]) => {
        setNode((nodes || []).find((n) => n.pubkey === pubkey) || null);
        setAvailable(names || []);
      })
      .catch(() => {});
  }, [pubkey]);

  useEffect(() => {
    loadMeta();
  }, [loadMeta]);

  const loadSeries = useCallback(() => {
    if (available.length === 0) {
      setLoading(false);
      return;
    }
    setLoading(true);
    const to = nowSecs();
    const from = to - range.spanSecs;
    Promise.all(
      available.map((metric) =>
        fetch(
          `/api/nodes/${pubkey}/history?metric=${encodeURIComponent(metric)}&from=${from}&to=${to}&bucket=${range.bucketSecs}`,
        )
          .then((r) => (r.ok ? r.json() : []))
          .then((pts: SeriesPoint[]) => [metric, pts || []] as const)
          .catch(() => [metric, []] as const),
      ),
    )
      .then((entries) => setSeries(Object.fromEntries(entries)))
      .finally(() => setLoading(false));
  }, [pubkey, available, range]);

  useEffect(() => {
    loadSeries();
  }, [loadSeries]);

  const onWs = useCallback(
    (topic: string, payload: unknown) => {
      if (topic !== "metrics" || !payload) return;
      const m = payload as WsMetrics;
      if (m.pubkey !== pubkey) return;
      setNode((prev) => ({
        pubkey: m.pubkey,
        companionId: m.companionId || prev?.companionId || "",
        kind: m.kind,
        name: m.name || prev?.name || "",
        lastPollTs: m.ts,
        lastOkTs: m.ts,
        lastError: "",
        metrics: { ...(prev?.metrics || {}), ...m.metrics },
      }));
    },
    [pubkey],
  );

  const { connected } = useWebSocket(["metrics"], onWs);

  const { heroMetrics, gridMetrics } = useMemo(() => {
    const avail = new Set(available);
    return {
      heroMetrics: HERO_METRICS.filter((m) => avail.has(m)),
      gridMetrics: orderGridMetrics(available),
    };
  }, [available]);
  const name = node?.name || pubkey.slice(0, 12);

  // Zip the lat/lon (and optional alt) series — recorded together each poll, so
  // they share bucket timestamps — into ordered track points for the map.
  const trackPoints = useMemo<TrackPoint[]>(() => {
    const lat = series["location_lat"] || [];
    const lon = series["location_lon"] || [];
    if (!lat.length || !lon.length) return [];
    const lonByTs = new Map(lon.map((p) => [p.ts, p.value]));
    const altByTs = new Map((series["location_alt"] || []).map((p) => [p.ts, p.value]));
    const pts: TrackPoint[] = [];
    for (const p of lat) {
      const lo = lonByTs.get(p.ts);
      if (lo === undefined) continue;
      pts.push({ ts: p.ts, lat: p.value, lon: lo, alt: altByTs.get(p.ts) });
    }
    return pts;
  }, [series]);

  return (
    <div className="space-y-4">
      <Link
        to="/monitoring"
        className="inline-flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground hover:text-primary transition-colors"
      >
        <ArrowLeft className="size-3" /> monitoring
      </Link>

      <PageHeader
        title={name}
        meta={
          <span className="font-mono text-xs text-muted-foreground tabular-nums">
            {node?.kind || "node"} ·{" "}
            <LiveAgo ts={node?.lastOkTs ?? 0} prefix="polled" empty="never polled" />
          </span>
        }
        actions={
          <div className="flex items-center gap-2">
            <ConnectionPill connected={connected} />
            <RangeSelector range={range} onChange={setRange} />
            {node?.companionId && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => setShowSettings((s) => !s)}
                className={cn(
                  "h-auto rounded-none py-1 font-mono text-[10px] uppercase tracking-[0.12em]",
                  showSettings && "border-primary text-primary",
                )}
              >
                <SlidersHorizontal className="size-3" />
                <span className="hidden sm:inline">configure</span>
              </Button>
            )}
          </div>
        }
      />

      {showSettings && node?.companionId && (
        <MonitoringSettings
          companionName={node.companionId}
          pubkey={pubkey}
          kind={node.kind === "companion" ? "companion" : "repeater"}
          onSaved={() => loadMeta()}
        />
      )}

      {node && (
        <NodeStatGrid
          metrics={node.metrics || {}}
          className="grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 border border-border"
        />
      )}

      {trackPoints.length > 0 && (
        <section className="panel flex flex-col">
          <div className="flex items-baseline justify-between px-3 pt-2.5 pb-1.5">
            <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
              Track <span className="text-muted-foreground/40">· {trackPoints.length} pts</span>
            </span>
            <span className="font-mono text-sm tabular-nums" style={{ color: "var(--primary)" }}>
              {trackPoints[trackPoints.length - 1].lat.toFixed(5)},{" "}
              {trackPoints[trackPoints.length - 1].lon.toFixed(5)}
            </span>
          </div>
          <div className="px-1 pb-1">
            <TrackMap points={trackPoints} height={320} />
          </div>
        </section>
      )}

      {loading && Object.keys(series).length === 0 ? (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {[...Array(4)].map((_, i) => (
            <Skeleton key={i} className="h-56 w-full" />
          ))}
        </div>
      ) : available.length === 0 ? (
        <div className="panel py-16 text-center font-mono text-xs uppercase tracking-widest text-muted-foreground/60">
          No history recorded yet — first poll pending.
        </div>
      ) : (
        <>
          {heroMetrics.length > 0 && (
            <section
              className={cn(
                "grid gap-4",
                heroMetrics.length > 1 ? "lg:grid-cols-2" : "grid-cols-1",
              )}
            >
              {heroMetrics.map((metric) => (
                <MetricChart
                  key={metric}
                  metric={metric}
                  data={series[metric] || []}
                  spanSecs={range.spanSecs}
                  variant="hero"
                  height={260}
                />
              ))}
            </section>
          )}

          {gridMetrics.length > 0 && (
            <section className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
              {gridMetrics.map((metric) => (
                <MetricChart
                  key={metric}
                  metric={metric}
                  data={series[metric] || []}
                  spanSecs={range.spanSecs}
                  height={160}
                />
              ))}
            </section>
          )}
        </>
      )}
    </div>
  );
}

// Live, self-ticking "X ago" label for a node's last successful poll. Holds its
// own interval so the relative time counts up in real time without re-rendering
// the charts; when a fresh poll lands over WS, `ts` changes and it snaps back.
function LiveAgo({
  ts,
  prefix,
  empty,
}: {
  ts: number;
  prefix: string;
  empty: string;
}) {
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(id);
  }, []);
  if (!ts) return <>{empty}</>;
  const ageSecs = Math.max(0, nowSecs() - ts);
  return (
    <>
      {prefix} {formatSecsAgo(ageSecs)}
    </>
  );
}

function RangeSelector({
  range,
  onChange,
}: {
  range: RangePreset;
  onChange: (r: RangePreset) => void;
}) {
  return (
    <div className="flex items-center gap-px bg-border border border-border">
      {RANGES.map((r) => (
        <button
          key={r.key}
          type="button"
          onClick={() => onChange(r)}
          className={cn(
            "px-2.5 py-1 font-mono text-[10px] uppercase tracking-widest bg-card transition-colors",
            range.key === r.key
              ? "text-primary bg-primary/10"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {r.label}
        </button>
      ))}
    </div>
  );
}
