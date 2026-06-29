import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CircleDashed, RefreshCw, Search } from "lucide-react";
import { useWebSocket } from "@/hooks/useWebSocket";
import { useApiList } from "@/hooks/useApiList";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Sheet,
  SheetContent,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet";
import { PageHeader } from "@/components/PageHeader";
import { ConnectionPill } from "@/components/StatusIndicator";
import { snrTextClass } from "@/components/SignalStrength";
import { formatDateTime, formatShortTime, truncate, truncateMid } from "@/lib/format";
import { cn } from "@/lib/utils";

interface Packet {
  id?: number;
  receivedAt: string;
  direction: "rx" | "tx" | string;
  raw: string;
  routeType?: number;
  payloadType?: number;
  route?: string;
  pathHashSize?: number;
  hops?: number;
  path?: string;
  packetHash?: string;
  summary?: string;
  snr?: number;
  rssi?: number;
}

interface PacketGroup {
  key: string;
  latest: Packet;
  // Parsed once during grouping so the sort comparator doesn't re-allocate
  // Date objects for strings already seen.
  latestTs: number;
  observations: Packet[];
}

const PAYLOAD_TYPE_LABELS: Record<number, string> = {
  0: "Request",
  1: "Response",
  2: "Message",
  3: "ACK",
  4: "Advert",
  5: "Channel",
  6: "Ch Data",
  7: "Anon Req",
  8: "Path",
  9: "Trace",
  10: "Multi",
  11: "Control",
  12: "Raw",
};

function payloadLabel(pt: number | undefined): string {
  if (pt == null) return "—";
  return PAYLOAD_TYPE_LABELS[pt] ?? `T${pt}`;
}

function packetKey(p: Packet): string {
  if (p.packetHash && p.packetHash.length > 0) return p.packetHash;
  return `${p.direction}:${p.raw}`;
}

function dirGlyph(direction: string): string {
  if (direction === "tx") return "↑";
  if (direction === "rx") return "↓";
  return "·";
}

const MIN_SIDEBAR_WIDTH = 300;
const DEFAULT_SIDEBAR_WIDTH = 480;

// Cap on server-side filtered results pulled into the UI at once. A broad
// type filter (e.g. Advert) can match thousands of the ~10k stored rows; this
// bounds the payload and the client-side grouping cost. Surfaced in the UI
// when hit so it doesn't read as "this is all there is".
const FILTER_LIMIT = 500;

const NO_PACKETS: Packet[] = [];

// buildGroups collapses observations sharing a packet hash into one group,
// newest first. Shared by the live stream and the filtered result set.
function buildGroups(packets: Packet[]): PacketGroup[] {
  const map = new Map<string, PacketGroup>();
  for (const p of packets) {
    const k = packetKey(p);
    const ts = new Date(p.receivedAt).getTime();
    const existing = map.get(k);
    if (!existing) {
      map.set(k, { key: k, latest: p, latestTs: ts, observations: [p] });
    } else {
      existing.observations.push(p);
      if (ts > existing.latestTs) {
        existing.latest = p;
        existing.latestTs = ts;
      }
    }
  }
  return Array.from(map.values()).sort((a, b) => b.latestTs - a.latestTs);
}

export function PacketsPage() {
  const {
    items,
    setItems: setPackets,
    loading,
    error,
    reload,
  } = useApiList<Packet>("/api/packets?limit=100", "Failed to load packets");
  const livePackets = items ?? NO_PACKETS;
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [typeFilter, setTypeFilter] = useState<number | "ALL">("ALL");
  const [sidebarWidth, setSidebarWidth] = useState(DEFAULT_SIDEBAR_WIDTH);
  const draggingRef = useRef(false);
  // Read by onResizeStart so the drag handler stays referentially stable —
  // listing sidebarWidth as a dep would rebuild it on every drag pixel.
  const sidebarWidthRef = useRef(DEFAULT_SIDEBAR_WIDTH);
  sidebarWidthRef.current = sidebarWidth;

  // Debounce the search box so each keystroke doesn't fire a server query.
  const [debouncedSearch, setDebouncedSearch] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);

  const filtering = typeFilter !== "ALL" || debouncedSearch !== "";

  // When a filter/search is active, query the server across ALL stored history
  // (the live buffer only holds the last 200), capped at FILTER_LIMIT. The
  // payload hash and hop-path are indexed columns, so this is a cheap DB query.
  const filterUrl = filtering
    ? `/api/packets?limit=${FILTER_LIMIT}` +
      (typeFilter !== "ALL" ? `&payloadType=${typeFilter}` : "") +
      (debouncedSearch ? `&q=${encodeURIComponent(debouncedSearch)}` : "")
    : null;
  const { items: filteredItems, loading: filterLoading } = useApiList<Packet>(
    filterUrl,
    "Failed to search packets",
  );

  const onWsMessage = useCallback(
    (topic: string, data: unknown) => {
      if (topic !== "packets" || !data) return;
      const pkt = data as Packet;
      setPackets((prev) => [pkt, ...(prev ?? [])].slice(0, 200));
    },
    [setPackets],
  );

  const { connected } = useWebSocket(["packets"], onWsMessage);

  // Pills are derived from the live buffer so they stay stable while a search
  // narrows results; the live stream is also what we render when unfiltered.
  const liveGroups = useMemo(() => buildGroups(livePackets), [livePackets]);
  const typeFilters = useMemo(() => {
    const counts = new Map<number, number>();
    for (const g of liveGroups) {
      const pt = g.latest.payloadType;
      if (pt == null) continue;
      counts.set(pt, (counts.get(pt) ?? 0) + 1);
    }
    return Array.from(counts.entries()).sort((a, b) => a[0] - b[0]);
  }, [liveGroups]);

  const filteredGroups = useMemo(
    () => buildGroups(filteredItems ?? NO_PACKETS),
    [filteredItems],
  );
  const groups = filtering ? filteredGroups : liveGroups;
  const capped = filtering && (filteredItems?.length ?? 0) >= FILTER_LIMIT;

  const emptyMsg = filterLoading
    ? "Searching…"
    : filtering
      ? "No packets match the filter."
      : "Awaiting packets…";

  const selected = useMemo(() => {
    if (!selectedKey) return null;
    return groups.find((g) => g.key === selectedKey) || null;
  }, [groups, selectedKey]);

  const onResizeStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    draggingRef.current = true;
    const startX = e.clientX;
    const startW = sidebarWidthRef.current;
    const maxW = Math.floor(window.innerWidth * 0.6);

    const onMove = (ev: MouseEvent) => {
      if (!draggingRef.current) return;
      const dx = startX - ev.clientX;
      const next = Math.max(MIN_SIDEBAR_WIDTH, Math.min(maxW, startW + dx));
      setSidebarWidth(next);
    };
    const onUp = () => {
      draggingRef.current = false;
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }, []);

  const totalPackets = livePackets.length;
  const liveGroupCount = liveGroups.length;

  return (
    <div className="space-y-4">
      <PageHeader
        title="Packets"
        meta={
          <span className="font-mono text-sm text-muted-foreground tabular-nums">
            {filtering
              ? `${groups.length}${capped ? "+" : ""} match${groups.length === 1 ? "" : "es"}${filterLoading ? " · …" : ""}`
              : `${liveGroupCount} unique · ${totalPackets} obs`}
          </span>
        }
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={reload}
              className="h-7 gap-1.5 text-xs uppercase tracking-widest font-mono"
            >
              <RefreshCw className="size-3" /> reload
            </Button>
            <ConnectionPill connected={connected} />
          </div>
        }
      />

      {loading && <PacketsSkeleton />}
      {error && <LoadErrorAlert message={error} onRetry={reload} />}

      {!loading && !error && (
        <section className="panel overflow-hidden">
          <div className="flex flex-col gap-3 px-4 py-3 border-b border-border sm:flex-row sm:items-center sm:justify-between">
            <div className="space-y-0.5">
              <span className="label-overline block">
                {filtering ? "History · filtered" : "Live · last 200"}
              </span>
              <h2 className="font-mono text-sm uppercase tracking-widest">
                Packet stream
              </h2>
            </div>
            <div className="relative w-full sm:w-72">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 size-3.5 text-muted-foreground/60" />
              <Input
                type="search"
                placeholder="search hash or path…"
                value={search}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
                  setSearch(e.target.value)
                }
                className="pl-9 font-mono text-xs"
              />
            </div>
          </div>

          <div className="flex flex-wrap gap-1 px-4 py-3 border-b border-border">
            <button
              type="button"
              onClick={() => setTypeFilter("ALL")}
              className={cn(
                "inline-flex items-center gap-1.5 px-2 py-1 border font-mono text-[10px] uppercase tracking-[0.12em] transition-colors",
                typeFilter === "ALL"
                  ? "border-primary/50 bg-primary/10 text-primary"
                  : "border-border bg-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              ALL
              <span className="tabular-nums text-muted-foreground/60">
                {liveGroupCount}
              </span>
            </button>
            {typeFilters.map(([pt, count]) => {
              const active = typeFilter === pt;
              return (
                <button
                  key={pt}
                  type="button"
                  onClick={() => setTypeFilter(pt)}
                  className={cn(
                    "inline-flex items-center gap-1.5 px-2 py-1 border font-mono text-[10px] uppercase tracking-[0.12em] transition-colors",
                    active
                      ? "border-primary/50 bg-primary/10 text-primary"
                      : "border-border bg-transparent text-muted-foreground hover:text-foreground",
                  )}
                >
                  {payloadLabel(pt)}
                  <span className="tabular-nums text-muted-foreground/60">
                    {count}
                  </span>
                </button>
              );
            })}
          </div>
          {/* Mobile: compact list */}
          <div className="sm:hidden divide-y divide-border/60">
            {groups.length === 0 ? (
              <div className="px-6 py-12 text-center text-sm text-muted-foreground/60">
                <CircleDashed className="size-6 mx-auto mb-2 text-muted-foreground/40" />
                {emptyMsg}
              </div>
            ) : (
              groups.map((g) => {
                const p = g.latest;
                const isSelected = selectedKey === g.key;
                const heard = g.observations.length;
                return (
                  <div
                    key={g.key}
                    onClick={() => setSelectedKey(g.key)}
                    className={cn(
                      // content-visibility lets the browser skip layout/paint
                      // for offscreen rows of this live stream.
                      "px-4 py-2.5 cursor-pointer [content-visibility:auto] [contain-intrinsic-size:auto_58px]",
                      isSelected && "bg-primary/5",
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex items-center gap-2 min-w-0">
                        <span
                          className={cn(
                            "font-mono text-sm",
                            p.direction === "tx" ? "text-primary" : "text-muted-foreground",
                          )}
                        >
                          {dirGlyph(p.direction)}
                        </span>
                        <span className="font-mono text-[11px] uppercase tracking-[0.08em] text-foreground/80">
                          {payloadLabel(p.payloadType)}
                        </span>
                        <span className="text-sm truncate text-muted-foreground">
                          {p.summary ? truncate(p.summary, 40) : truncateMid(p.raw, 6, 4)}
                        </span>
                      </div>
                      {heard > 1 && (
                        <span className="shrink-0 inline-flex items-center justify-center min-w-5 px-1 py-0.5 border border-primary/30 bg-primary/5 text-primary font-mono text-[10px] tabular-nums">
                          ×{heard}
                        </span>
                      )}
                    </div>
                    <div className="flex items-center gap-3 mt-0.5 font-mono text-[10px] tabular-nums text-muted-foreground/60 pl-6">
                      <span>{formatShortTime(p.receivedAt)}</span>
                      {p.snr != null && (
                        <span className={snrTextClass(p.snr)}>{p.snr.toFixed(1)}dB</span>
                      )}
                      {p.hops != null && <span>{p.hops} hop{p.hops !== 1 ? "s" : ""}</span>}
                    </div>
                  </div>
                );
              })
            )}
          </div>

          {/* Desktop: full table */}
          <Table className="hidden sm:table">
            <TableHeader>
              <TableRow className="border-border hover:bg-transparent">
                <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em] w-[110px]">
                  Time
                </TableHead>
                <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em] w-[44px] text-center">
                  Dir
                </TableHead>
                <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em] w-[90px]">
                  Type
                </TableHead>
                <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em]">
                  Summary
                </TableHead>
                <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em]">
                  Route
                </TableHead>
                <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em] hidden md:table-cell text-right">
                  Path
                </TableHead>
                <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em] hidden lg:table-cell text-right">
                  Hops
                </TableHead>
                <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em] text-right w-[64px]">
                  Heard
                </TableHead>
                <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em] hidden xl:table-cell text-right w-[120px]">
                  Signal
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {groups.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={9}
                    className="text-center py-12 text-sm text-muted-foreground/60"
                  >
                    <CircleDashed className="size-6 mx-auto mb-2 text-muted-foreground/40" />
                    {emptyMsg}
                  </TableCell>
                </TableRow>
              ) : (
                groups.map((g) => {
                  const p = g.latest;
                  const isSelected = selectedKey === g.key;
                  const heard = g.observations.length;
                  return (
                    <TableRow
                      key={g.key}
                      onClick={() => setSelectedKey(g.key)}
                      className={cn(
                        "border-border/60 cursor-pointer group [content-visibility:auto] [contain-intrinsic-size:auto_44px]",
                        isSelected && "bg-primary/5",
                      )}
                    >
                      <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">
                        {formatShortTime(p.receivedAt)}
                      </TableCell>
                      <TableCell className="text-center">
                        <span
                          className={cn(
                            "font-mono text-sm",
                            p.direction === "tx"
                              ? "text-primary"
                              : "text-muted-foreground",
                          )}
                          title={p.direction}
                        >
                          {dirGlyph(p.direction)}
                        </span>
                      </TableCell>
                      <TableCell>
                        <span className="font-mono text-[11px] uppercase tracking-[0.08em] text-foreground/80">
                          {payloadLabel(p.payloadType)}
                        </span>
                      </TableCell>
                      <TableCell className="max-w-0">
                        <div className="text-sm truncate">
                          {p.summary ? (
                            truncate(p.summary, 80)
                          ) : (
                            <span className="text-muted-foreground/60 italic">
                              {truncateMid(p.raw, 8, 6)}
                            </span>
                          )}
                        </div>
                      </TableCell>
                      <TableCell>
                        <span className="font-mono text-xs uppercase tracking-[0.08em] text-muted-foreground">
                          {p.route || "—"}
                        </span>
                      </TableCell>
                      <TableCell className="hidden md:table-cell text-right font-mono text-xs tabular-nums text-muted-foreground">
                        {p.pathHashSize != null ? `${p.pathHashSize}B` : "—"}
                      </TableCell>
                      <TableCell className="hidden lg:table-cell text-right font-mono text-xs tabular-nums text-muted-foreground">
                        {p.hops != null ? p.hops : "—"}
                      </TableCell>
                      <TableCell className="text-right">
                        {heard > 1 ? (
                          <span className="inline-flex items-center justify-center min-w-6 px-1.5 py-0.5 border border-primary/30 bg-primary/5 text-primary font-mono text-[10px] tabular-nums">
                            ×{heard}
                          </span>
                        ) : (
                          <span className="font-mono text-xs text-muted-foreground/40">
                            —
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="hidden xl:table-cell text-right">
                        {p.snr != null ? (
                          <span
                            className={cn(
                              "font-mono text-xs tabular-nums",
                              snrTextClass(p.snr),
                            )}
                          >
                            {p.snr.toFixed(1)}dB
                            {p.rssi != null && (
                              <span className="text-muted-foreground/50">
                                {" "}
                                / {p.rssi}
                              </span>
                            )}
                          </span>
                        ) : (
                          <span className="font-mono text-xs text-muted-foreground/40">
                            ——
                          </span>
                        )}
                      </TableCell>
                    </TableRow>
                  );
                })
              )}
            </TableBody>
          </Table>
        </section>
      )}

      <Sheet
        open={!!selected}
        onOpenChange={(open) => {
          if (!open) setSelectedKey(null);
        }}
      >
        <SheetContent
          side="right"
          showCloseButton={false}
          className={cn(
            "p-0 bg-card border-l border-border sm:max-w-none w-full max-w-[100vw]",
          )}
          style={{ width: sidebarWidth }}
        >
          <div
            onMouseDown={onResizeStart}
            className="absolute top-0 left-0 h-full w-1 cursor-col-resize hover:bg-primary/40 active:bg-primary/60 transition-colors hidden sm:block"
            aria-hidden
          />
          {selected && (
            <PacketDetail
              group={selected}
              onClose={() => setSelectedKey(null)}
            />
          )}
        </SheetContent>
      </Sheet>
    </div>
  );
}

function PacketDetail({
  group,
  onClose,
}: {
  group: PacketGroup;
  onClose: () => void;
}) {
  const p = group.latest;
  const heard = group.observations.length;
  const sortedObs = useMemo(
    () =>
      [...group.observations].sort(
        (a, b) =>
          new Date(b.receivedAt).getTime() -
          new Date(a.receivedAt).getTime(),
      ),
    [group.observations],
  );
  const firstSeen = sortedObs[sortedObs.length - 1]?.receivedAt;
  const lastSeen = sortedObs[0]?.receivedAt;

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="flex items-start justify-between px-5 py-4 border-b border-border shrink-0">
        <div className="space-y-1 min-w-0">
          <span className="label-overline block">Packet · detail</span>
          <SheetTitle className="font-mono text-sm uppercase tracking-widest">
            {payloadLabel(p.payloadType)}
          </SheetTitle>
          <SheetDescription className="font-mono text-xs text-muted-foreground break-all">
            {p.packetHash || "no hash"}
          </SheetDescription>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={onClose}
          className="font-mono"
        >
          ×
        </Button>
      </div>

      <div className="flex-1 overflow-y-auto">
        <div className="px-5 py-4 border-b border-border space-y-2">
          <span className="label-overline block">Summary</span>
          <p className="text-sm leading-relaxed wrap-break-word">
            {p.summary || (
              <span className="text-muted-foreground/60 italic">
                no summary available
              </span>
            )}
          </p>
        </div>

        <div className="px-5 py-4 border-b border-border">
          <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 font-mono text-xs">
            <dt className="label-overline">Direction</dt>
            <dd className="tabular-nums">
              <span
                className={cn(
                  p.direction === "tx" ? "text-primary" : "text-foreground",
                )}
              >
                {dirGlyph(p.direction)} {p.direction}
              </span>
            </dd>

            <dt className="label-overline">Type</dt>
            <dd className="tabular-nums">
              {payloadLabel(p.payloadType)}
              {p.payloadType != null && (
                <span className="text-muted-foreground/60">
                  {" "}
                  · {p.payloadType}
                </span>
              )}
            </dd>

            <dt className="label-overline">Route</dt>
            <dd className="tabular-nums">
              {p.route || (
                <span className="text-muted-foreground/60">—</span>
              )}
            </dd>

            <dt className="label-overline">Path Size</dt>
            <dd className="tabular-nums">
              {p.pathHashSize != null ? `${p.pathHashSize}B` : "—"}
            </dd>

            <dt className="label-overline">Path</dt>
            <dd className="tabular-nums break-all">
              {p.path ? (
                p.path
              ) : (
                <span className="text-muted-foreground/60">—</span>
              )}
            </dd>

            <dt className="label-overline">Hops</dt>
            <dd className="tabular-nums">{p.hops != null ? p.hops : "—"}</dd>

            <dt className="label-overline">Signal</dt>
            <dd className="tabular-nums">
              {p.snr != null ? (
                <span className={snrTextClass(p.snr)}>
                  {p.snr.toFixed(1)}dB
                  {p.rssi != null && (
                    <span className="text-muted-foreground/60">
                      {" "}
                      · RSSI {p.rssi}
                    </span>
                  )}
                </span>
              ) : (
                <span className="text-muted-foreground/60">——</span>
              )}
            </dd>

            <dt className="label-overline">First Seen</dt>
            <dd className="tabular-nums text-muted-foreground">
              {firstSeen ? formatDateTime(firstSeen) : "—"}
            </dd>

            <dt className="label-overline">Last Seen</dt>
            <dd className="tabular-nums text-muted-foreground">
              {lastSeen ? formatDateTime(lastSeen) : "—"}
            </dd>

            <dt className="label-overline">Times Heard</dt>
            <dd className="tabular-nums">{heard}</dd>
          </dl>
        </div>

        {heard > 1 && (
          <div className="border-b border-border">
            <div className="flex items-center justify-between px-5 py-3 border-b border-border">
              <span className="label-overline">Observations</span>
              <span className="font-mono text-[10px] tabular-nums text-muted-foreground">
                {heard}
              </span>
            </div>
            <Table>
              <TableHeader>
                <TableRow className="border-border hover:bg-transparent">
                  <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em] pl-5">
                    Time
                  </TableHead>
                  <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em] w-[44px] text-center">
                    Dir
                  </TableHead>
                  <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em] text-right pr-5">
                    SNR
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sortedObs.map((o, i) => (
                  <TableRow
                    key={`${o.receivedAt}-${i}`}
                    className="border-border/60"
                  >
                    <TableCell className="font-mono text-xs tabular-nums pl-5 text-muted-foreground">
                      {formatDateTime(o.receivedAt)}
                    </TableCell>
                    <TableCell className="text-center">
                      <span
                        className={cn(
                          "font-mono text-sm",
                          o.direction === "tx"
                            ? "text-primary"
                            : "text-muted-foreground",
                        )}
                      >
                        {dirGlyph(o.direction)}
                      </span>
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs tabular-nums pr-5">
                      {o.snr != null ? (
                        <span className={snrTextClass(o.snr)}>
                          {o.snr.toFixed(1)}dB
                        </span>
                      ) : (
                        <span className="text-muted-foreground/40">——</span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}

        <div className="px-5 py-4 space-y-2">
          <div className="flex items-center justify-between">
            <span className="label-overline">Raw</span>
            <span className="font-mono text-[10px] tabular-nums text-muted-foreground">
              {Math.floor(p.raw.length / 2)} bytes
            </span>
          </div>
          <pre className="font-mono text-xs leading-relaxed bg-muted/40 border border-border p-3 break-all whitespace-pre-wrap">
            {p.raw || ""}
          </pre>
        </div>
      </div>
    </div>
  );
}

function PacketsSkeleton() {
  return (
    <div className="panel">
      <div className="px-4 py-3 border-b border-border">
        <Skeleton className="h-3 w-24 mb-2" />
        <Skeleton className="h-4 w-40" />
      </div>
      <div className="space-y-px p-4">
        {[...Array(10)].map((_, i) => (
          <div
            key={i}
            className="flex items-center gap-4 py-2 border-b border-border/60"
          >
            <Skeleton className="h-3 w-16" />
            <Skeleton className="h-3 w-6" />
            <Skeleton className="h-3 w-20" />
            <Skeleton className="h-3 flex-1" />
            <Skeleton className="h-3 w-16" />
          </div>
        ))}
      </div>
    </div>
  );
}
