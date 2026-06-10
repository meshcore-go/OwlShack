import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  Check,
  CircleDashed,
  Plus,
  Radio,
  Send,
  Trash2,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { useWebSocket } from "@/hooks/useWebSocket";
import { Button } from "@/components/ui/button";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { SectionTitle } from "@/components/SectionTitle";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/PageHeader";
import { ConnectionPill } from "@/components/StatusIndicator";
import { SignalStrength } from "@/components/SignalStrength";
import { truncateMid } from "@/lib/format";
import { cn } from "@/lib/utils";

interface Companion {
  name: string;
  pubkey: string;
  peerCount: number;
}

interface Peer {
  pubkey: string;
  name: string;
  type: string;
  lastSeen: string;
  snr: number | null;
  rssi: number | null;
}

interface TraceWsMessage {
  companion: string;
  tag: number;
  hops: number;
  path: string[];
  hopSNRs: number[];
  snr?: number;
}

interface TraceResult {
  companion: string;
  tag: number;
  path: string[];
  hopSNRs: number[];
  snr?: number;
  elapsedMs: number;
}

type HashSize = 1 | 2 | 4;
type BuilderMode = "select" | "manual";
type PeerSort = "name" | "recent" | "signal";

// Silence window: the final packet (last repeater in the path) must arrive
// within this long after the trace was sent OR after the most recent echo.
// Each progressive echo we hear resets the window; if it elapses with no
// further progress we declare a timeout.
const TRACE_TIMEOUT_MS = 5000;

const HEX_RE = /^[0-9a-f]+$/i;

export function TracesPage() {
  const [companions, setCompanions] = useState<Companion[]>([]);
  const [peers, setPeers] = useState<Peer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [companionName, setCompanionName] = useState<string>("");
  const [hashSize, setHashSize] = useState<HashSize>(1);
  const [mode, setMode] = useState<BuilderMode>("select");
  const [selectedPath, setSelectedPath] = useState<Peer[]>([]);
  const [manualHex, setManualHex] = useState<string>("");
  const [filter, setFilter] = useState<string>("");
  const [sort, setSort] = useState<PeerSort>("name");

  const [activeTag, setActiveTag] = useState<number | null>(null);
  const [waitStartedAt, setWaitStartedAt] = useState<number | null>(null);
  const [deadlineAt, setDeadlineAt] = useState<number | null>(null);
  const [liveSNRs, setLiveSNRs] = useState<number[]>([]);
  const [result, setResult] = useState<TraceResult | null>(null);
  const [waitError, setWaitError] = useState<string | null>(null);
  const [tickNow, setTickNow] = useState(Date.now());
  const timeoutRef = useRef<number | null>(null);
  // Updated each render to the number of hops in the path we sent, so the WS
  // handler can tell a partial echo from the final packet without re-creating
  // the callback whenever the path changes.
  const plannedHopCountRef = useRef(0);
  // Same render-mirror trick for the trace-in-flight state: the WS callback
  // reads these so it stays referentially stable during an active trace
  // instead of resubscribing on every tag/timer change.
  const activeTagRef = useRef<number | null>(null);
  activeTagRef.current = activeTag;
  const waitStartedAtRef = useRef<number | null>(null);
  waitStartedAtRef.current = waitStartedAt;

  // (Re)arm the silence timer. Called on send and on every partial echo.
  const armTimeout = useCallback(() => {
    if (timeoutRef.current) window.clearTimeout(timeoutRef.current);
    setDeadlineAt(Date.now() + TRACE_TIMEOUT_MS);
    timeoutRef.current = window.setTimeout(() => {
      setActiveTag(null);
      setWaitStartedAt(null);
      setDeadlineAt(null);
      setWaitError("No response from final repeater · timed out");
      timeoutRef.current = null;
    }, TRACE_TIMEOUT_MS);
  }, []);

  const onWsMessage = useCallback(
    (topic: string, data: unknown) => {
      if (topic === "peers" && data) {
        const p = data as Peer;
        if (p.type === "REPEATER") {
          setPeers((prev) => {
            const idx = prev.findIndex((x) => x.pubkey === p.pubkey);
            if (idx === -1) return [...prev, p];
            const next = prev.slice();
            next[idx] = { ...next[idx], ...p };
            return next;
          });
        }
        return;
      }
      if (topic !== "traces" || !data) return;
      const msg = data as TraceWsMessage;
      const activeTag = activeTagRef.current;
      const waitStartedAt = waitStartedAtRef.current;
      if (activeTag == null || msg.tag !== activeTag || waitStartedAt == null) {
        return;
      }

      // Each echo we hear carries the SNRs accumulated so far; the path is
      // complete once we have one SNR per planned hop (the last repeater
      // replied). msg.hops is the full path length from the firmware.
      const snrs = Array.isArray(msg.hopSNRs) ? msg.hopSNRs : [];
      const totalHops =
        msg.hops && msg.hops > 0 ? msg.hops : plannedHopCountRef.current;
      const isFinal = totalHops > 0 && snrs.length >= totalHops;

      if (isFinal) {
        const elapsed = Date.now() - waitStartedAt;
        setResult({
          companion: msg.companion,
          tag: msg.tag,
          path: msg.path || [],
          hopSNRs: snrs,
          snr: msg.snr,
          elapsedMs: elapsed,
        });
        setLiveSNRs(snrs);
        setActiveTag(null);
        setWaitStartedAt(null);
        setDeadlineAt(null);
        setWaitError(null);
        if (timeoutRef.current) {
          window.clearTimeout(timeoutRef.current);
          timeoutRef.current = null;
        }
      } else {
        // Partial echo: a repeater along the path repeated the packet but the
        // final hop hasn't replied yet. Show the progress and reset the timer.
        setLiveSNRs((prev) => (snrs.length > prev.length ? snrs : prev));
        armTimeout();
      }
    },
    [armTimeout],
  );

  const { connected } = useWebSocket(["traces", "peers"], onWsMessage);

  useEffect(() => {
    if (activeTag == null) return;
    const id = window.setInterval(() => setTickNow(Date.now()), 200);
    return () => window.clearInterval(id);
  }, [activeTag]);

  useEffect(() => {
    return () => {
      if (timeoutRef.current) window.clearTimeout(timeoutRef.current);
    };
  }, []);

  const load = useCallback(() => {
    setLoading(true);
    setError(null);
    Promise.all([
      fetch("/api/companions").then((r) => {
        if (!r.ok) throw new Error("companions");
        return r.json();
      }),
      fetch("/api/peers").then((r) => {
        if (!r.ok) throw new Error("peers");
        return r.json();
      }),
    ])
      .then(([cs, ps]: [Companion[] | null, Peer[] | null]) => {
        const list = cs || [];
        setCompanions(list);
        setPeers((ps || []).filter((p) => p.type === "REPEATER"));
        if (list.length > 0 && !companionName) setCompanionName(list[0].name);
      })
      .catch(() => setError("Failed to load companions or peers"))
      .finally(() => setLoading(false));
  }, [companionName]);

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const filteredPeers = useMemo(() => {
    const f = filter.trim().toLowerCase();
    // Note: already-selected repeaters intentionally stay in the list so the
    // same repeater can be added to the path more than once (e.g. b8, e6, b8).
    const matched = f
      ? peers.filter(
          (p) =>
            p.name.toLowerCase().includes(f) ||
            p.pubkey.toLowerCase().includes(f),
        )
      : peers;
    return matched.slice().sort((a, b) => {
      if (sort === "signal") {
        // Strongest first; repeaters with no recent signal sink to the bottom.
        return (b.snr ?? -Infinity) - (a.snr ?? -Infinity);
      }
      if (sort === "recent") {
        // RFC3339 strings sort chronologically; most-recent first.
        return (b.lastSeen || "").localeCompare(a.lastSeen || "");
      }
      // Alphabetical (default); unnamed repeaters sink to the bottom, then
      // pubkey as a stable tiebreaker.
      const an = a.name.trim().toLowerCase();
      const bn = b.name.trim().toLowerCase();
      if (!an !== !bn) return an ? -1 : 1;
      return an.localeCompare(bn) || a.pubkey.localeCompare(b.pubkey);
    });
  }, [peers, filter, sort]);

  const peerByHash = useMemo(() => {
    const map = new Map<string, Peer>();
    for (const p of peers) {
      const k = p.pubkey.slice(0, hashSize * 2).toLowerCase();
      if (!map.has(k)) map.set(k, p);
    }
    return map;
  }, [peers, hashSize]);

  const computedPathHex = useMemo(() => {
    if (mode === "manual") return manualHex.trim().toLowerCase();
    return selectedPath
      .map((p) => p.pubkey.slice(0, hashSize * 2))
      .join("")
      .toLowerCase();
  }, [mode, manualHex, selectedPath, hashSize]);

  const pathHexValid = useMemo(() => {
    if (!computedPathHex) return mode === "select";
    if (!HEX_RE.test(computedPathHex)) return false;
    if (computedPathHex.length % (hashSize * 2) !== 0) return false;
    return true;
  }, [computedPathHex, hashSize, mode]);

  const pathByteLen = computedPathHex.length / 2;
  const pathHopCount = hashSize > 0 ? pathByteLen / hashSize : 0;

  // Per-hop hashes of the path we're about to send / are waiting on. Works for
  // both builder modes since it's derived from the computed hex. Drives the
  // live timeline and tells the WS handler how many hops to expect.
  const routeHashes = useMemo(() => {
    const step = hashSize * 2;
    if (step <= 0) return [];
    const out: string[] = [];
    for (let i = 0; i + step <= computedPathHex.length; i += step) {
      out.push(computedPathHex.slice(i, i + step));
    }
    return out;
  }, [computedPathHex, hashSize]);
  plannedHopCountRef.current = routeHashes.length;

  const onChangeHashSize = (v: string) => {
    const n = Number(v) as HashSize;
    setHashSize(n);
    setSelectedPath([]);
  };

  const addPeer = (peer: Peer) => {
    setSelectedPath((prev) => [...prev, peer]);
  };

  const removeAt = (idx: number) => {
    setSelectedPath((prev) => prev.filter((_, i) => i !== idx));
  };

  const moveAt = (idx: number, dir: -1 | 1) => {
    setSelectedPath((prev) => {
      const next = [...prev];
      const j = idx + dir;
      if (j < 0 || j >= next.length) return prev;
      [next[idx], next[j]] = [next[j], next[idx]];
      return next;
    });
  };

  const clearPath = () => setSelectedPath([]);

  const sendTrace = async () => {
    if (!companionName) {
      toast.error("Select a companion first");
      return;
    }
    if (mode === "manual" && !computedPathHex) {
      toast.error("Path is empty");
      return;
    }
    if (!pathHexValid) {
      toast.error("Path is not valid hex for the selected hash size");
      return;
    }

    setResult(null);
    setWaitError(null);
    setLiveSNRs([]);

    try {
      const res = await fetch(
        `/api/companions/${encodeURIComponent(companionName)}/trace`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            path: computedPathHex,
            pathHashSize: hashSize,
          }),
        },
      );
      if (!res.ok) {
        let msg = `HTTP ${res.status}`;
        try {
          const data = await res.json();
          if (data && typeof data.error === "string") msg = data.error;
        } catch {
          // ignore
        }
        toast.error(`Trace failed: ${msg}`);
        return;
      }
      const data = (await res.json()) as { tag: number };
      setActiveTag(data.tag);
      setWaitStartedAt(Date.now());
      armTimeout();
      toast.success(`Trace sent · tag ${data.tag.toString(16).padStart(8, "0")}`);
    } catch (err) {
      toast.error(`Trace failed: ${(err as Error).message}`);
    }
  };

  const isWaiting = activeTag != null && waitStartedAt != null;
  const remainingMs =
    isWaiting && deadlineAt != null ? Math.max(0, deadlineAt - tickNow) : 0;

  return (
    <div className="space-y-4">
      <PageHeader
        title="Traces"
        meta={
          <span className="font-mono text-sm text-muted-foreground tabular-nums">
            {peers.length} repeaters · {companions.length} companions
          </span>
        }
        actions={<ConnectionPill connected={connected} />}
      />

      {loading && <TracesSkeleton />}
      {error && <LoadErrorAlert message={error} onRetry={load} />}

      {!loading && !error && (
        <section className="grid grid-cols-1 lg:grid-cols-5 gap-6">
          <div className="lg:col-span-3 space-y-6">
            <div className="panel">
              <SectionTitle eyebrow="Path · builder" title="Compose route" />
              <div className="px-4 py-4 space-y-4">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                  <div className="space-y-1.5">
                    <label className="label-overline block">Companion</label>
                    <Select
                      value={companionName}
                      onValueChange={setCompanionName}
                    >
                      <SelectTrigger className="w-full font-mono text-xs uppercase tracking-[0.06em] rounded-none">
                        <SelectValue placeholder="Select companion" />
                      </SelectTrigger>
                      <SelectContent className="rounded-none font-mono text-xs">
                        {companions.length === 0 ? (
                          <div className="px-3 py-2 text-muted-foreground/60">
                            none configured
                          </div>
                        ) : (
                          companions.map((c) => (
                            <SelectItem
                              key={c.name}
                              value={c.name}
                              className="font-mono text-xs uppercase tracking-[0.06em]"
                            >
                              {c.name}
                            </SelectItem>
                          ))
                        )}
                      </SelectContent>
                    </Select>
                  </div>
                  <div className="space-y-1.5">
                    <label className="label-overline block">
                      Path Hash Size
                    </label>
                    <Select
                      value={String(hashSize)}
                      onValueChange={onChangeHashSize}
                    >
                      <SelectTrigger className="w-full font-mono text-xs uppercase tracking-[0.06em] rounded-none">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent className="rounded-none font-mono text-xs">
                        <SelectItem value="1">1 byte</SelectItem>
                        <SelectItem value="2">2 bytes</SelectItem>
                        <SelectItem value="4">4 bytes</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                </div>

                <div className="flex items-center gap-1 border border-border bg-muted/30 p-0.5 w-fit">
                  <ModeToggle
                    active={mode === "select"}
                    onClick={() => setMode("select")}
                  >
                    pick
                  </ModeToggle>
                  <ModeToggle
                    active={mode === "manual"}
                    onClick={() => setMode("manual")}
                  >
                    manual hex
                  </ModeToggle>
                </div>

                {mode === "select" ? (
                  <div className="space-y-3">
                    <div className="flex items-center justify-between">
                      <span className="label-overline">Selected route</span>
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-[10px] tabular-nums text-muted-foreground">
                          {selectedPath.length} hops · {pathByteLen}B
                        </span>
                        {selectedPath.length > 0 && (
                          <Button
                            variant="ghost"
                            size="xs"
                            onClick={clearPath}
                            className="font-mono uppercase tracking-[0.08em]"
                          >
                            <Trash2 className="size-3" /> clear
                          </Button>
                        )}
                      </div>
                    </div>
                    {selectedPath.length === 0 ? (
                      <div className="border border-dashed border-border/60 bg-muted/20 px-4 py-6 text-center">
                        <CircleDashed className="size-5 mx-auto mb-2 text-muted-foreground/40" />
                        <p className="text-sm text-muted-foreground/60">
                          No hops selected · pick repeaters below
                        </p>
                      </div>
                    ) : (
                      <ol className="flex flex-wrap gap-2">
                        {selectedPath.map((peer, idx) => (
                          <li
                            key={`${peer.pubkey}-${idx}`}
                            className="inline-flex items-center gap-1.5 border border-border bg-muted/40 px-2 py-1 font-mono text-xs hover:border-primary/40 transition-colors"
                          >
                            <span className="text-muted-foreground/60 tabular-nums">
                              {idx + 1}
                            </span>
                            <span className="text-foreground">
                              {peer.name || (
                                <span className="italic text-muted-foreground">
                                  unknown
                                </span>
                              )}
                            </span>
                            <span className="text-muted-foreground/60">
                              {peer.pubkey.slice(0, hashSize * 2)}
                            </span>
                            <span className="ml-1 inline-flex items-center gap-0.5">
                              <button
                                type="button"
                                onClick={() => moveAt(idx, -1)}
                                disabled={idx === 0}
                                className="inline-flex items-center justify-center size-4 hover:text-primary disabled:opacity-30 disabled:cursor-not-allowed"
                                aria-label="move up"
                              >
                                <ArrowUp className="size-3" />
                              </button>
                              <button
                                type="button"
                                onClick={() => moveAt(idx, 1)}
                                disabled={idx === selectedPath.length - 1}
                                className="inline-flex items-center justify-center size-4 hover:text-primary disabled:opacity-30 disabled:cursor-not-allowed"
                                aria-label="move down"
                              >
                                <ArrowDown className="size-3" />
                              </button>
                              <button
                                type="button"
                                onClick={() => removeAt(idx)}
                                className="inline-flex items-center justify-center size-4 hover:text-destructive"
                                aria-label="remove"
                              >
                                <X className="size-3" />
                              </button>
                            </span>
                          </li>
                        ))}
                      </ol>
                    )}

                    <div className="space-y-2 pt-2 border-t border-border">
                      <div className="flex items-center justify-between gap-2">
                        <span className="label-overline shrink-0">
                          Repeaters
                        </span>
                        <div className="flex items-center gap-2">
                          <Select
                            value={sort}
                            onValueChange={(v) => setSort(v as PeerSort)}
                          >
                            <SelectTrigger className="h-7 w-36 rounded-none font-mono text-[11px] uppercase tracking-[0.06em]">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="rounded-none font-mono text-xs">
                              <SelectItem
                                value="name"
                                className="font-mono text-xs uppercase tracking-[0.06em]"
                              >
                                Alphabetical
                              </SelectItem>
                              <SelectItem
                                value="recent"
                                className="font-mono text-xs uppercase tracking-[0.06em]"
                              >
                                Last seen
                              </SelectItem>
                              <SelectItem
                                value="signal"
                                className="font-mono text-xs uppercase tracking-[0.06em]"
                              >
                                Signal
                              </SelectItem>
                            </SelectContent>
                          </Select>
                          <Input
                            value={filter}
                            onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
                              setFilter(e.target.value)
                            }
                            placeholder="filter…"
                            className="h-7 w-32 rounded-none font-mono text-xs"
                          />
                        </div>
                      </div>
                      <div className="border border-border max-h-72 overflow-y-auto divide-y divide-border/60">
                        {filteredPeers.length === 0 ? (
                          <div className="px-3 py-6 text-center text-sm text-muted-foreground/60">
                            <Radio className="size-5 mx-auto mb-2 text-muted-foreground/30" />
                            No matching repeaters
                          </div>
                        ) : (
                          filteredPeers.map((peer) => (
                            <button
                              key={peer.pubkey}
                              type="button"
                              onClick={() => addPeer(peer)}
                              className="w-full flex items-center justify-between gap-3 px-3 py-2 text-left hover:bg-muted/40 transition-colors group"
                            >
                              <div className="min-w-0 flex-1">
                                <div className="text-sm font-medium truncate">
                                  {peer.name || (
                                    <span className="italic text-muted-foreground">
                                      unknown
                                    </span>
                                  )}
                                </div>
                                <code className="font-mono text-[10px] text-muted-foreground tabular-nums">
                                  {truncateMid(peer.pubkey, 8, 4)}
                                </code>
                              </div>
                              <Plus className="size-3.5 text-muted-foreground/40 group-hover:text-primary transition-colors" />
                            </button>
                          ))
                        )}
                      </div>
                    </div>
                  </div>
                ) : (
                  <div className="space-y-2">
                    <label className="label-overline block">
                      Manual path · hex
                    </label>
                    <Textarea
                      value={manualHex}
                      onChange={(
                        e: React.ChangeEvent<HTMLTextAreaElement>,
                      ) => setManualHex(e.target.value)}
                      placeholder={`example: ${"ab".repeat(hashSize)} ${"cd".repeat(hashSize)}`}
                      className="font-mono text-xs rounded-none min-h-24 break-all"
                    />
                    <div className="flex items-center justify-between font-mono text-[10px] tabular-nums text-muted-foreground">
                      <span>
                        {pathByteLen}B · {pathHopCount} hops
                      </span>
                      <span>
                        {pathHexValid ? (
                          <span className="text-success">valid</span>
                        ) : (
                          <span className="text-destructive">
                            invalid · must be hex divisible by {hashSize * 2}
                          </span>
                        )}
                      </span>
                    </div>
                  </div>
                )}

                <div className="pt-2 border-t border-border space-y-3">
                  <div className="flex items-center justify-between gap-3">
                    <div className="space-y-0.5 min-w-0 flex-1">
                      <span className="label-overline block">Path · hex</span>
                      <code className="font-mono text-xs text-muted-foreground break-all block">
                        {computedPathHex || (
                          <span className="text-muted-foreground/40">
                            (empty)
                          </span>
                        )}
                      </code>
                    </div>
                    <Button
                      onClick={sendTrace}
                      disabled={
                        isWaiting ||
                        !companionName ||
                        !pathHexValid ||
                        (mode === "manual" && !computedPathHex)
                      }
                      className="rounded-none font-mono uppercase tracking-[0.1em] gap-2"
                    >
                      <Send className="size-3.5" />
                      {isWaiting ? "waiting…" : "send trace"}
                    </Button>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <div className="lg:col-span-2 space-y-6">
            <div className="panel">
              <SectionTitle
                eyebrow={
                  isWaiting
                    ? "Awaiting · live"
                    : result
                      ? "Result"
                      : "Result · idle"
                }
                title="Trace timeline"
                trailing={
                  isWaiting && (
                    <span className="font-mono text-[10px] tabular-nums text-primary">
                      {(remainingMs / 1000).toFixed(1)}s
                    </span>
                  )
                }
              />
              <div className="px-4 py-4">
                {!result && !isWaiting && !waitError && (
                  <div className="text-center py-10">
                    <CircleDashed className="size-6 mx-auto mb-2 text-muted-foreground/40" />
                    <p className="text-sm text-muted-foreground/60">
                      No trace sent yet
                    </p>
                  </div>
                )}

                {(isWaiting || result || waitError) && (
                  <TraceTimeline
                    isWaiting={isWaiting}
                    waitError={waitError}
                    result={result}
                    companionName={companionName}
                    routeHashes={routeHashes}
                    liveSNRs={liveSNRs}
                    peerByHash={peerByHash}
                  />
                )}
              </div>
            </div>
          </div>
        </section>
      )}
    </div>
  );
}

function ModeToggle({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "px-3 py-1 font-mono text-[10px] uppercase tracking-[0.12em] transition-colors",
        active
          ? "bg-primary/10 text-primary border border-primary/30"
          : "border border-transparent text-muted-foreground hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}

interface TimelineStep {
  badge: React.ReactNode;
  label: string;
  name?: string;
  hashHex?: string;
  snr?: number;
  showSignal: boolean;
  meta?: string;
  metaError?: boolean;
  state: "active" | "pending" | "done" | "error";
}

function TraceTimeline({
  isWaiting,
  waitError,
  result,
  companionName,
  routeHashes,
  liveSNRs,
  peerByHash,
}: {
  isWaiting: boolean;
  waitError: string | null;
  result: TraceResult | null;
  companionName: string;
  routeHashes: string[];
  liveSNRs: number[];
  peerByHash: Map<string, Peer>;
}) {
  const steps = useMemo<TimelineStep[]>(() => {
    const out: TimelineStep[] = [];

    // Prefer the hashes the firmware echoed back once we have the final
    // packet; otherwise show the path we're sending / waiting on.
    const hashes =
      result && result.path.length
        ? result.path.map((h) => h.toLowerCase())
        : routeHashes;
    const snrs = result ? result.hopSNRs : liveSNRs;

    // 1. Us — the trace originator. The ↓SNR here is the strength at which we
    // received the FINAL packet back from the last repeater in the path.
    out.push({
      badge: "TX",
      label: "Started the trace",
      name: companionName || "—",
      snr: result?.snr ?? undefined,
      showSignal: result != null && result.snr != null,
      state: "done",
    });

    // 2. One node per hop. SNRs fill in progressively as each repeater repeats
    // the packet; the next-expected hop pulses while we wait.
    hashes.forEach((hash, i) => {
      const snr = snrs[i];
      const hasSnr = typeof snr === "number";
      let state: TimelineStep["state"];
      if (hasSnr) state = "done";
      else if (isWaiting && i === snrs.length) state = "active";
      else state = "pending";
      out.push({
        badge: hash.slice(0, 2).toUpperCase(),
        label: `Hop ${i + 1} · Repeated the packet`,
        name: peerByHash.get(hash)?.name,
        hashHex: hash,
        snr: hasSnr ? snr : undefined,
        showSignal: hasSnr,
        state,
      });
    });

    // 3. Completion marker — final packet received, timed out, or still waiting.
    if (result) {
      out.push({
        badge: <Check className="size-4" />,
        label: "Received trace response",
        meta: `Duration: ${result.elapsedMs}ms`,
        showSignal: false,
        state: "done",
      });
    } else if (waitError) {
      out.push({
        badge: <X className="size-4" />,
        label: "No trace response",
        meta: waitError,
        metaError: true,
        showSignal: false,
        state: "error",
      });
    } else {
      out.push({
        badge: "RX",
        label: "Awaiting trace response",
        name: "listening…",
        showSignal: false,
        state: "active",
      });
    }

    return out;
  }, [companionName, result, routeHashes, liveSNRs, isWaiting, waitError, peerByHash]);

  return (
    <ol className="relative">
      {steps.map((step, idx) => (
        <li key={idx} className="flex gap-3 relative pb-3 last:pb-0">
          <div className="relative flex flex-col items-center">
            <div
              className={cn(
                "size-9 border flex items-center justify-center font-mono text-[10px] uppercase tabular-nums shrink-0",
                step.state === "done" &&
                  "border-primary/40 bg-primary/10 text-primary",
                step.state === "active" &&
                  "border-primary/40 bg-primary/10 text-primary scan-pulse",
                step.state === "pending" &&
                  "border-border bg-muted/40 text-muted-foreground/60",
                step.state === "error" &&
                  "border-destructive/40 bg-destructive/10 text-destructive",
              )}
            >
              {step.badge}
            </div>
            {idx < steps.length - 1 && (
              <div
                className={cn(
                  "w-px flex-1 mt-1",
                  step.state === "done" ? "bg-primary/30" : "bg-border",
                )}
              />
            )}
          </div>
          <div className="flex-1 min-w-0 pb-2 pt-0.5 space-y-0.5">
            <div className="flex items-center justify-between gap-2">
              <span className="label-overline">{step.label}</span>
              {step.showSignal && step.snr != null && (
                <SignalStrength snr={step.snr} size="sm" />
              )}
            </div>
            {(step.name || step.hashHex) && (
              <div className="font-mono text-xs truncate">
                {step.name || (
                  <span className="text-muted-foreground/60 italic">
                    unknown
                  </span>
                )}
              </div>
            )}
            {step.hashHex && (
              <code className="font-mono text-[10px] tabular-nums text-muted-foreground">
                {step.hashHex}
              </code>
            )}
            {step.meta && (
              <div
                className={cn(
                  "font-mono text-[11px] tabular-nums",
                  step.metaError ? "text-destructive" : "text-success",
                )}
              >
                {step.meta}
              </div>
            )}
          </div>
        </li>
      ))}
    </ol>
  );
}

function TracesSkeleton() {
  return (
    <div className="grid grid-cols-1 lg:grid-cols-5 gap-6">
      <div className="lg:col-span-3 panel p-4 space-y-4">
        <Skeleton className="h-4 w-32" />
        <div className="grid grid-cols-2 gap-4">
          <Skeleton className="h-9" />
          <Skeleton className="h-9" />
        </div>
        <Skeleton className="h-32" />
        <Skeleton className="h-9 w-32" />
      </div>
      <div className="lg:col-span-2 panel p-4 space-y-3">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-9" />
        <Skeleton className="h-9" />
        <Skeleton className="h-9" />
      </div>
    </div>
  );
}
