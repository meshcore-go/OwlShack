import { useCallback, useEffect, useMemo, useState, type MouseEvent } from "react";
import { Link } from "react-router-dom";
import { AlertTriangle, ArrowUpRight, CircleDashed, RefreshCw } from "lucide-react";
import { useWebSocket } from "@/hooks/useWebSocket";
import { PageHeader } from "@/components/PageHeader";
import { ConnectionPill } from "@/components/StatusIndicator";
import { toast } from "sonner";
import { NodeStatGrid, sparklineMetricKeys } from "@/components/NodeStatTiles";
import { type SeriesPoint } from "@/components/MetricChart";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { formatSecsAgo } from "@/lib/format";

interface MonitoredNode {
  pubkey: string;
  kind: string;
  name: string;
  lastPollTs: number;
  lastOkTs: number;
  lastError?: string;
  intervalSecs: number; // effective poll cadence; drives the staleness threshold
  metrics: Record<string, number>;
}

interface WsMetrics {
  pubkey: string;
  kind: string;
  name: string;
  ts: number;
  metrics: Record<string, number>;
}

const nowSecs = () => Math.floor(Date.now() / 1000);

// staleThreshold: how long without a successful poll before a node is flagged
// stale (amber). Derived from the node's own poll cadence — one interval plus a
// 25% grace (min 5 min) to absorb scheduler tick + stagger — rather than a fixed
// constant, since the interval is configurable per node.
function staleThreshold(intervalSecs: number): number {
  if (intervalSecs <= 0) return 0; // unknown cadence → don't flag on time
  return intervalSecs + Math.max(intervalSecs * 0.25, 300);
}

// Which metrics get a trend sparkline is decided per-node by sparklineMetricKeys
// (the keys backing the tiles), so it adapts to whatever sensors/channels a board
// exposes rather than a hardcoded list.
const SPARK_SPAN_SECS = 24 * 3600; // recent-trend window for the cards
const SPARK_BUCKET_SECS = 900; // 15-min buckets → ~smooth, light payload

type NodeHistory = Record<string, SeriesPoint[]>; // metric → series

export function MonitoringPage() {
  const [nodes, setNodes] = useState<MonitoredNode[]>([]);
  const [history, setHistory] = useState<Record<string, NodeHistory>>({});
  const [loading, setLoading] = useState(true);

  // Fetch a recent window of each sparkline metric the node actually reports.
  const loadHistories = useCallback((list: MonitoredNode[]) => {
    const to = nowSecs();
    const from = to - SPARK_SPAN_SECS;
    Promise.all(
      list.flatMap((n) =>
        sparklineMetricKeys(n.metrics || {}).map((metric) =>
          fetch(
            `/api/nodes/${n.pubkey}/history?metric=${encodeURIComponent(metric)}&from=${from}&to=${to}&bucket=${SPARK_BUCKET_SECS}`,
          )
            .then((r) => (r.ok ? r.json() : []))
            .then((pts: SeriesPoint[]) => ({ pubkey: n.pubkey, metric, pts: pts || [] }))
            .catch(() => ({ pubkey: n.pubkey, metric, pts: [] as SeriesPoint[] })),
        ),
      ),
    ).then((rows) => {
      const next: Record<string, NodeHistory> = {};
      for (const { pubkey, metric, pts } of rows) {
        (next[pubkey] ||= {})[metric] = pts;
      }
      setHistory(next);
    });
  }, []);

  const load = useCallback(() => {
    fetch("/api/nodes/monitored")
      .then((r) => (r.ok ? r.json() : []))
      .then((data: MonitoredNode[]) => {
        const list = data || [];
        setNodes(list);
        loadHistories(list);
      })
      .catch(() => setNodes([]))
      .finally(() => setLoading(false));
  }, [loadHistories]);

  useEffect(() => {
    load();
  }, [load]);

  const onWs = useCallback((topic: string, payload: unknown) => {
    if (topic !== "metrics" || !payload) return;
    const m = payload as WsMetrics;
    setNodes((prev) => {
      const idx = prev.findIndex((n) => n.pubkey === m.pubkey);
      const next = [...prev];
      const merged: MonitoredNode = {
        pubkey: m.pubkey,
        kind: m.kind,
        name: m.name || (idx >= 0 ? prev[idx].name : ""),
        lastPollTs: m.ts,
        lastOkTs: m.ts,
        lastError: "",
        // WS payload carries no interval; keep the value from the last fetch
        // (0 for a node first seen over WS — it's fresh, so never stale anyway).
        intervalSecs: idx >= 0 ? prev[idx].intervalSecs : 0,
        metrics: { ...(idx >= 0 ? prev[idx].metrics : {}), ...m.metrics },
      };
      if (idx >= 0) next[idx] = merged;
      else next.push(merged);
      return next;
    });
    // Extend the trend sparklines live so they don't go stale between fetches.
    setHistory((prev) => {
      const nodeHist: NodeHistory = { ...(prev[m.pubkey] || {}) };
      let changed = false;
      for (const metric of sparklineMetricKeys(m.metrics || {})) {
        const v = m.metrics?.[metric];
        if (v === undefined) continue;
        const series = nodeHist[metric] ? nodeHist[metric].slice() : [];
        const last = series[series.length - 1];
        if (!last || last.ts !== m.ts) {
          series.push({ ts: m.ts, value: v });
          nodeHist[metric] = series;
          changed = true;
        }
      }
      return changed ? { ...prev, [m.pubkey]: nodeHist } : prev;
    });
  }, []);

  const { connected } = useWebSocket(["metrics"], onWs);

  // Stable, name-ordered card layout (falls back to pubkey for unnamed nodes) so
  // cards don't jump around as WS updates arrive.
  const sortedNodes = useMemo(
    () =>
      [...nodes].sort((a, b) =>
        (a.name || a.pubkey).localeCompare(b.name || b.pubkey, undefined, {
          sensitivity: "base",
        }),
      ),
    [nodes],
  );

  return (
    <div className="space-y-4">
      <PageHeader
        title="Monitoring"
        meta={
          <span className="font-mono text-sm text-muted-foreground tabular-nums">
            {nodes.length} node{nodes.length === 1 ? "" : "s"}
          </span>
        }
        trailing={<ConnectionPill connected={connected} />}
      />

      {loading ? (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {[...Array(3)].map((_, i) => (
            <Skeleton key={i} className="h-64 w-full" />
          ))}
        </div>
      ) : nodes.length === 0 ? (
        <EmptyState />
      ) : (
        <section className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {sortedNodes.map((n) => (
            <NodeCard key={n.pubkey} node={n} history={history[n.pubkey]} />
          ))}
        </section>
      )}
    </div>
  );
}

function EmptyState() {
  return (
    <div className="panel py-16 flex flex-col items-center gap-3 text-center">
      <CircleDashed className="size-7 text-muted-foreground/40" />
      <p className="font-mono text-sm uppercase tracking-widest text-muted-foreground">
        No nodes monitored yet
      </p>
      <p className="text-sm text-muted-foreground/60 max-w-md">
        Open a repeater and toggle <span className="text-foreground">Monitor</span>{" "}
        to start collecting analytics &amp; history. Enabled nodes show up here
        right away and fill in after their first poll.
      </p>
    </div>
  );
}

function NodeCard({
  node,
  history,
}: {
  node: MonitoredNode;
  history?: NodeHistory;
}) {
  const ageSecs = node.lastOkTs ? nowSecs() - node.lastOkTs : -1;
  // Never polled: enrolled (toggle on) but the scheduler hasn't reached it yet.
  const pending = node.lastPollTs === 0;
  const errored = !!node.lastError && node.lastOkTs === 0;
  const threshold = staleThreshold(node.intervalSecs);
  const stale = threshold > 0 && ageSecs >= 0 && ageSecs > threshold;
  const dot = pending
    ? "bg-muted-foreground/40"
    : errored
      ? "bg-signal-weak"
      : stale
        ? "bg-warning"
        : "bg-success scan-pulse";

  const label = node.name || node.pubkey.slice(0, 12);
  const [polling, setPolling] = useState(false);

  // The card is a <Link>; stop the click bubbling so polling doesn't navigate.
  const handlePoll = (e: MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (polling) return;
    setPolling(true);
    fetch(`/api/nodes/${node.pubkey}/poll`, { method: "POST" })
      .then(async (r) => {
        if (!r.ok) {
          const body = await r.json().catch(() => ({}));
          throw new Error(body.error || `poll failed (${r.status})`);
        }
        // Success pushes fresh metrics over WS, which updates the card live.
        toast.success(`Polled ${label}`);
      })
      .catch((err) => toast.error(`${label}: ${err.message || "poll failed"}`))
      .finally(() => setPolling(false));
  };

  return (
    <Link
      to={`/monitoring/${node.pubkey}`}
      className="panel flex flex-col transition-colors hover:border-primary/50 group"
    >
      <header className="flex items-center justify-between gap-2 px-3 py-2.5 border-b border-border">
        <div className="min-w-0">
          <div className="font-mono text-sm font-semibold truncate">{label}</div>
          <code className="font-mono text-[10px] text-muted-foreground/60">
            {node.pubkey.slice(0, 12)} · {node.kind || "node"}
          </code>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <button
            type="button"
            onClick={handlePoll}
            disabled={polling}
            title="Poll this node now"
            className="inline-flex items-center gap-1 border border-border px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.12em] text-muted-foreground transition-colors hover:border-primary/50 hover:text-primary disabled:opacity-50"
          >
            <RefreshCw className={cn("size-2.5", polling && "animate-spin")} />
            {polling ? "polling" : "poll"}
          </button>
          <span className={cn("size-2 rounded-full", dot)} />
        </div>
      </header>

      {errored ? (
        <div className="px-3 py-6 flex items-start gap-2 text-signal-weak">
          <AlertTriangle className="size-4 shrink-0 mt-0.5" />
          <span className="font-mono text-xs wrap-break-word">{node.lastError}</span>
        </div>
      ) : (
        <NodeStatGrid metrics={node.metrics || {}} history={history} />
      )}

      <footer className="flex items-center justify-between px-3 py-2 border-t border-border font-mono text-[10px] uppercase tracking-widest text-muted-foreground">
        <span className={cn(stale && "text-warning")}>
          {pending
            ? "waiting for first poll"
            : ageSecs < 0
              ? "never polled"
              : `polled ${formatSecsAgo(ageSecs)}`}
        </span>
        <span className="inline-flex items-center gap-1 group-hover:text-primary transition-colors">
          charts <ArrowUpRight className="size-3" />
        </span>
      </footer>
    </Link>
  );
}
