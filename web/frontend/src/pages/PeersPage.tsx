import { useCallback, useEffect, useMemo, useState } from "react";
import { CircleDashed, RefreshCw, Search, Users } from "lucide-react";
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
import { PageHeader } from "@/components/PageHeader";
import { ConnectionPill, PeerTypePill } from "@/components/StatusIndicator";
import { SignalStrength } from "@/components/SignalStrength";
import { PeerAvatar } from "@/components/PeerAvatar";
import { PeerDetailSheet, advertPathInfo } from "@/components/PeerDetailSheet";
import { timeAgo, truncateMid } from "@/lib/format";
import { cn } from "@/lib/utils";

interface Peer {
  pubkey: string;
  name: string;
  type: string;
  lat: number;
  lon: number;
  lastSeen: string;
  lastAdvertTs?: number;
  snr: number | null;
  rssi: number | null;
  outPath?: string;
  outPathHashSize?: number;
}

interface CompanionRef {
  name: string;
  pubkey?: string;
}

function HopsBadge({ peer }: { peer: Peer }) {
  const { hops } = advertPathInfo(peer.outPath, peer.outPathHashSize);
  if (hops === 0) {
    return (
      <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-success/80">
        direct
      </span>
    );
  }
  return (
    <span className="font-mono text-xs tabular-nums text-muted-foreground">
      {hops} hop{hops > 1 ? "s" : ""}
    </span>
  );
}

const TYPE_FILTERS = ["ALL", "CHAT", "REPEATER", "ROOM", "SENSOR", "NONE"] as const;
type TypeFilter = (typeof TYPE_FILTERS)[number];

const NO_PEERS: Peer[] = [];

function isPeer(value: unknown): value is Peer {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return typeof v.pubkey === "string" && typeof v.name === "string";
}

export function PeersPage() {
  const {
    items,
    setItems: setPeers,
    loading,
    error,
    reload,
  } = useApiList<Peer>("/api/peers", "Failed to load peers");
  const peers = items ?? NO_PEERS;
  const [companions, setCompanions] = useState<CompanionRef[]>([]);
  const [search, setSearch] = useState("");
  const [typeFilter, setTypeFilter] = useState<TypeFilter>("ALL");
  const [selectedKey, setSelectedKey] = useState<string | null>(null);

  const handleMessage = useCallback(
    (topic: string, data: unknown) => {
      if (topic !== "peers") return;
      if (!isPeer(data)) return;
      setPeers((prev) => {
        const arr = prev ?? [];
        const idx = arr.findIndex((p) => p.pubkey === data.pubkey);
        if (idx === -1) return [data, ...arr];
        const next = arr.slice();
        next[idx] = { ...next[idx], ...data };
        return next;
      });
    },
    [setPeers],
  );

  const { connected } = useWebSocket(["peers"], handleMessage);

  useEffect(() => {
    fetch("/api/companions")
      .then((r) => (r.ok ? r.json() : []))
      .then((cs: CompanionRef[]) => setCompanions(cs || []))
      .catch(() => {});
  }, []);

  const selected = useMemo(
    () => peers.find((p) => p.pubkey === selectedKey) ?? null,
    [peers, selectedKey],
  );

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return peers
      .filter((p) => {
        if (typeFilter !== "ALL" && p.type !== typeFilter) return false;
        if (!q) return true;
        return (
          (p.name || "").toLowerCase().includes(q) ||
          (p.type || "").toLowerCase().includes(q)
        );
      })
      .sort(
        (a, b) =>
          new Date(b.lastSeen).getTime() - new Date(a.lastSeen).getTime(),
      );
  }, [peers, search, typeFilter]);

  const typeCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const p of peers) counts[p.type] = (counts[p.type] || 0) + 1;
    return counts;
  }, [peers]);

  const withLocation = useMemo(
    () => peers.filter((p) => p.lat !== 0 || p.lon !== 0).length,
    [peers],
  );

  return (
    <div className="space-y-4">
      <PageHeader
        title="Peers"
        meta={
          <span className="font-mono text-sm text-muted-foreground tabular-nums">
            {filtered.length}/{peers.length} · {withLocation} located
          </span>
        }
        actions={
          <>
            <Button
              variant="ghost"
              size="sm"
              onClick={reload}
              className="h-7 gap-1.5 px-2 font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground hover:text-primary"
            >
              <RefreshCw className={cn("size-3", loading && "animate-spin")} />
              refresh
            </Button>
            <ConnectionPill connected={connected} />
          </>
        }
      />

      {error && <LoadErrorAlert message={error} onRetry={reload} />}

      <section className="panel">
        <div className="flex flex-col gap-3 px-4 py-3 border-b border-border sm:flex-row sm:items-center sm:justify-between">
          <div className="space-y-0.5">
            <span className="label-overline block">Roster</span>
            <h2 className="font-mono text-sm uppercase tracking-widest">
              Discovered peers
            </h2>
          </div>
          <div className="relative w-full sm:w-72">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 size-3.5 text-muted-foreground/60" />
            <Input
              type="search"
              placeholder="search name or type…"
              value={search}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
                setSearch(e.target.value)
              }
              className="pl-9 font-mono text-xs"
            />
          </div>
        </div>

        <div className="flex flex-wrap gap-1 px-4 py-3 border-b border-border">
          {TYPE_FILTERS.map((t) => {
            const active = typeFilter === t;
            const count = t === "ALL" ? peers.length : typeCounts[t] || 0;
            return (
              <button
                key={t}
                type="button"
                onClick={() => setTypeFilter(t)}
                className={cn(
                  "inline-flex items-center gap-1.5 px-2 py-1 border font-mono text-[10px] uppercase tracking-[0.12em] transition-colors",
                  active
                    ? "border-primary/50 bg-primary/10 text-primary"
                    : "border-border bg-transparent text-muted-foreground hover:text-foreground hover:border-border",
                )}
              >
                {t}
                <span className="tabular-nums text-muted-foreground/60">
                  {count}
                </span>
              </button>
            );
          })}
        </div>

        {/* Mobile: compact list */}
        <div className="sm:hidden divide-y divide-border/60">
          {loading ? (
            [...Array(6)].map((_, i) => (
              <div key={i} className="px-4 py-3 space-y-1.5">
                <div className="flex items-center gap-2">
                  <Skeleton className="size-7 rounded-sm" />
                  <Skeleton className="h-3 w-24" />
                </div>
                <Skeleton className="h-2.5 w-32 ml-9" />
              </div>
            ))
          ) : filtered.length === 0 ? (
            <div className="px-6 py-16 text-center text-sm text-muted-foreground/60">
              {peers.length === 0 ? (
                <>
                  <CircleDashed className="size-6 mx-auto mb-2 text-muted-foreground/40" />
                  Awaiting first contact…
                </>
              ) : (
                <>
                  <Users className="size-6 mx-auto mb-2 text-muted-foreground/40" />
                  No peers match the current filter.
                </>
              )}
            </div>
          ) : (
            filtered.map((p) => (
              <button
                key={p.pubkey}
                type="button"
                onClick={() => setSelectedKey(p.pubkey)}
                className="w-full px-4 py-2.5 flex items-center gap-3 text-left hover:bg-muted/40 transition-colors"
              >
                <PeerAvatar name={p.name || "?"} size="sm" />
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-sm truncate">
                      {p.name || (
                        <span className="text-muted-foreground italic">unknown</span>
                      )}
                    </span>
                    <PeerTypePill type={p.type} />
                  </div>
                  <div className="flex items-center gap-3 mt-0.5 font-mono text-[10px] tabular-nums text-muted-foreground">
                    <SignalStrength snr={p.snr} size="sm" />
                    <HopsBadge peer={p} />
                    <span>{timeAgo(p.lastSeen)}</span>
                  </div>
                </div>
              </button>
            ))
          )}
        </div>

        {/* Desktop: full table */}
        <Table className="hidden sm:table">
          <TableHeader>
            <TableRow className="border-border hover:bg-transparent">
              <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em]">
                Name
              </TableHead>
              <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em]">
                Type
              </TableHead>
              <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em]">
                Pubkey
              </TableHead>
              <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em]">
                Signal
              </TableHead>
              <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em]">
                Hops
              </TableHead>
              <TableHead className="font-mono text-[10px] uppercase tracking-[0.12em] text-right">
                Last seen
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              [...Array(6)].map((_, i) => (
                <TableRow key={i} className="border-border/60">
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <Skeleton className="size-7 rounded-sm" />
                      <Skeleton className="h-3 w-24" />
                    </div>
                  </TableCell>
                  <TableCell>
                    <Skeleton className="h-4 w-14" />
                  </TableCell>
                  <TableCell>
                    <Skeleton className="h-3 w-28" />
                  </TableCell>
                  <TableCell>
                    <Skeleton className="h-3 w-16" />
                  </TableCell>
                  <TableCell>
                    <Skeleton className="h-3 w-12" />
                  </TableCell>
                  <TableCell className="text-right">
                    <Skeleton className="h-3 w-10 ml-auto" />
                  </TableCell>
                </TableRow>
              ))
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className="text-center py-16 text-sm text-muted-foreground/60"
                >
                  {peers.length === 0 ? (
                    <>
                      <CircleDashed className="size-6 mx-auto mb-2 text-muted-foreground/40" />
                      Awaiting first contact…
                    </>
                  ) : (
                    <>
                      <Users className="size-6 mx-auto mb-2 text-muted-foreground/40" />
                      No peers match the current filter.
                    </>
                  )}
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((p) => (
                <TableRow
                  key={p.pubkey}
                  onClick={() => setSelectedKey(p.pubkey)}
                  className="border-border/60 group cursor-pointer"
                >
                  <TableCell className="font-medium">
                    <div className="flex items-center gap-2.5">
                      <PeerAvatar name={p.name || "?"} size="sm" />
                      <span className="truncate">
                        {p.name || (
                          <span className="text-muted-foreground italic">
                            unknown
                          </span>
                        )}
                      </span>
                    </div>
                  </TableCell>
                  <TableCell>
                    <PeerTypePill type={p.type} />
                  </TableCell>
                  <TableCell>
                    <code className="font-mono text-xs text-muted-foreground">
                      {truncateMid(p.pubkey, 6, 4)}
                    </code>
                  </TableCell>
                  <TableCell>
                    <SignalStrength snr={p.snr} />
                  </TableCell>
                  <TableCell>
                    <HopsBadge peer={p} />
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums text-muted-foreground">
                    {timeAgo(p.lastSeen)}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </section>

      <PeerDetailSheet
        peer={selected}
        open={!!selectedKey}
        onOpenChange={(o) => {
          if (!o) setSelectedKey(null);
        }}
        companions={companions}
      />
    </div>
  );
}
