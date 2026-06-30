import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import L from "leaflet";
import { MapPin, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { useWebSocket } from "@/hooks/useWebSocket";
import { useApiList } from "@/hooks/useApiList";
import { useCompanions } from "@/hooks/useCompanions";
import { usePeerDetailSheet } from "@/hooks/usePeerDetailSheet";
import { isPeerDelete } from "@/lib/peerWs";
import { Button } from "@/components/ui/button";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { PageHeader } from "@/components/PageHeader";
import { ConnectionPill, PEER_TYPE_HEX } from "@/components/StatusIndicator";
import { InlineConfirm } from "@/components/InlineConfirm";
import { PeerDetailSheet } from "@/components/PeerDetailSheet";
import { deletePeers, deletedPeersMessage } from "@/lib/peerApi";
import { themeTileLayer, useThemeTiles } from "@/lib/leaflet";
import { snrFill } from "@/components/SignalStrength";
import { cn } from "@/lib/utils";

interface Peer {
  pubkey: string;
  name: string;
  type: string;
  lat: number;
  lon: number;
  lastSeen: string;
  snr: number | null;
  rssi: number | null;
}

interface NeighborLink {
  aPubkey: string;
  aName: string;
  aLat: number;
  aLon: number;
  bPubkey: string;
  bName: string;
  bLat: number;
  bLon: number;
  snrAtoB?: number;
  snrBtoA?: number;
  ts: number;
}

const TYPE_FILTERS = ["CHAT", "REPEATER", "ROOM", "SENSOR", "NONE"] as const;

function isPeer(value: unknown): value is Peer {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return typeof v.pubkey === "string" && typeof v.name === "string";
}

function wrapLon(lon: number): number {
  return ((lon + 180) % 360 + 360) % 360 - 180;
}

function dotIcon(color: string): L.DivIcon {
  return L.divIcon({
    className: "meshcore-peer-dot",
    html: `<span style="display:block; width:10px; height:10px; border-radius:9999px; background:${color}; box-shadow: 0 0 0 2px rgba(0,0,0,0.55), 0 0 6px ${color};"></span>`,
    iconSize: [10, 10],
    iconAnchor: [5, 5],
    popupAnchor: [0, -6],
  });
}

// A distinct target marker for a deep-linked coordinate (e.g. one shared in
// chat), so it stands out from the peer dots.
function focusIcon(): L.DivIcon {
  return L.divIcon({
    className: "meshcore-focus-pin",
    html: `<span style="display:flex; align-items:center; justify-content:center; width:20px; height:20px; border-radius:9999px; border:2px solid var(--primary); box-shadow: 0 0 0 2px rgba(0,0,0,0.55), 0 0 12px var(--primary);"><span style="display:block; width:6px; height:6px; border-radius:9999px; background:var(--primary);"></span></span>`,
    iconSize: [20, 20],
    iconAnchor: [10, 10],
    popupAnchor: [0, -12],
  });
}

// One DivIcon per peer type, built once — the markers-sync effect runs on every
// WS peer update, so per-marker icon construction there is wasted work.
const PEER_ICONS: Record<string, L.DivIcon> = Object.fromEntries(
  Object.entries(PEER_TYPE_HEX).map(([type, color]) => [type, dotIcon(color)]),
);
const FOCUS_ICON = focusIcon();

const NO_PEERS: Peer[] = [];

export function MapPage() {
  const {
    items,
    setItems: setPeers,
    loading,
    error,
    reload,
  } = useApiList<Peer>("/api/peers", "Failed to load peers");
  const peers = items ?? NO_PEERS;
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const companions = useCompanions();
  const { selectPeer, sheetProps } = usePeerDetailSheet(peers);

  const [showLinks, setShowLinks] = useState(false);
  const { items: links } = useApiList<NeighborLink>(
    showLinks ? "/api/nodes/neighbor-links" : null,
    "Failed to load neighbor links",
  );

  const [searchParams] = useSearchParams();
  const focus = useMemo(() => {
    const la = parseFloat(searchParams.get("lat") ?? "");
    const lo = parseFloat(searchParams.get("lon") ?? "");
    if (!Number.isFinite(la) || !Number.isFinite(lo)) return null;
    if (la < -90 || la > 90 || lo < -180 || lo > 180) return null;
    return { lat: la, lon: lo };
  }, [searchParams]);

  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<L.Map | null>(null);
  const tileLayerRef = useRef<L.TileLayer | null>(null);
  const markersRef = useRef<Map<string, L.Marker>>(new Map());
  const focusMarkerRef = useRef<L.Marker | null>(null);
  const linksLayerRef = useRef<L.LayerGroup | null>(null);
  const fittedRef = useRef(false);

  const handleMessage = useCallback(
    (topic: string, data: unknown) => {
      if (topic !== "peers") return;
      if (isPeerDelete(data)) {
        const gone = new Set(data.pubkeys.map((k) => k.toLowerCase()));
        setPeers((prev) =>
          (prev ?? []).filter((p) => !gone.has(p.pubkey.toLowerCase())),
        );
        return;
      }
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

  // Initialize map once
  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;
    const map = L.map(containerRef.current, {
      center: [0, 0],
      zoom: 2,
      worldCopyJump: true,
      zoomControl: true,
      attributionControl: true,
    });
    tileLayerRef.current = themeTileLayer().addTo(map);
    linksLayerRef.current = L.layerGroup().addTo(map);
    mapRef.current = map;

    return () => {
      map.remove();
      mapRef.current = null;
      tileLayerRef.current = null;
      linksLayerRef.current = null;
      markersRef.current.clear();
      focusMarkerRef.current = null;
      fittedRef.current = false;
    };
  }, []);

  useThemeTiles(mapRef, tileLayerRef);

  // Center + drop a highlight pin when a coordinate is deep-linked via
  // ?lat=&lon= (e.g. "Open in MeshCore Map" from a chat coordinate). Marks the
  // view as fitted so the peer auto-fit doesn't yank it away once peers load.
  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;
    if (!focus) {
      if (focusMarkerRef.current) {
        map.removeLayer(focusMarkerRef.current);
        focusMarkerRef.current = null;
      }
      return;
    }
    map.setView([focus.lat, focus.lon], 15);
    fittedRef.current = true;
    if (focusMarkerRef.current) {
      focusMarkerRef.current.setLatLng([focus.lat, focus.lon]);
    } else {
      focusMarkerRef.current = L.marker([focus.lat, focus.lon], {
        icon: FOCUS_ICON,
        zIndexOffset: 1000,
      })
        .addTo(map)
        .bindPopup(
          `<div style="font-family: var(--font-mono); font-size: 11px; text-transform: uppercase; letter-spacing: 0.08em;">shared location</div><div style="font-family: var(--font-mono); font-size: 11px; color: var(--muted-foreground);">${focus.lat}, ${focus.lon}</div>`,
        );
    }
    focusMarkerRef.current.openPopup();
  }, [focus]);

  const located = useMemo(
    () => peers.filter((p) => p.lat !== 0 || p.lon !== 0),
    [peers],
  );

  const visible = useMemo(
    () => located.filter((p) => !hidden.has(p.type)),
    [located, hidden],
  );

  const [confirmClear, setConfirmClear] = useState(false);
  const [clearing, setClearing] = useState(false);

  // Deletes exactly what's currently plotted (located + passing the type
  // filter), so the type pills double as a scoping tool for cleanup.
  const deleteShown = useCallback(async () => {
    setClearing(true);
    try {
      const result = await deletePeers(visible.map((p) => p.pubkey));
      toast.success(deletedPeersMessage(result));
      setConfirmClear(false);
    } catch (e) {
      toast.error(`Delete failed: ${e instanceof Error ? e.message : "error"}`);
    } finally {
      setClearing(false);
    }
  }, [visible]);

  // Sync markers with visible peers
  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;

    const next = new Set(visible.map((p) => p.pubkey));
    const markers = markersRef.current;

    // Remove markers no longer visible
    for (const [key, marker] of markers.entries()) {
      if (!next.has(key)) {
        map.removeLayer(marker);
        markers.delete(key);
      }
    }

    // Add or update markers
    for (const p of visible) {
      const lat = p.lat / 1e6;
      const lon = wrapLon(p.lon / 1e6);
      const icon = PEER_ICONS[p.type] || PEER_ICONS.NONE;
      const existing = markers.get(p.pubkey);
      if (existing) {
        existing.setLatLng([lat, lon]);
        // setIcon rebuilds the marker's DOM element — skip when unchanged.
        if (existing.getIcon() !== icon) existing.setIcon(icon);
      } else {
        const marker = L.marker([lat, lon], { icon }).addTo(map);
        // Click opens the same detail sheet as the Peers page (pubkey is
        // stable for a marker, so the closure never goes stale).
        marker.on("click", () => selectPeer(p.pubkey));
        markers.set(p.pubkey, marker);
      }
    }

    // Auto-fit on first load
    if (!fittedRef.current && visible.length > 0) {
      const bounds = L.latLngBounds(
        visible.map((p) => [p.lat / 1e6, wrapLon(p.lon / 1e6)] as [number, number]),
      );
      if (bounds.isValid()) {
        map.fitBounds(bounds, { padding: [40, 40], maxZoom: 12 });
        fittedRef.current = true;
      }
    }
  }, [visible]);

  // Redraw neighbor-link lines whenever the data or the toggle changes. Full
  // clear-and-redraw (not incremental) since links are cheap to rebuild and
  // there's no per-link identity worth diffing against.
  useEffect(() => {
    const layer = linksLayerRef.current;
    if (!layer) return;
    layer.clearLayers();
    if (!showLinks || !links) return;

    // Three stagger positions along the line (35 / 50 / 65 %). Cycling by
    // index means parallel lines sharing a common endpoint — the most common
    // overlap case — get labels at visually distinct distances from that node.
    const STAGGER = [0.35, 0.5, 0.65] as const;

    links.forEach((l, i) => {
      const a: [number, number] = [l.aLat / 1e6, wrapLon(l.aLon / 1e6)];
      const b: [number, number] = [l.bLat / 1e6, wrapLon(l.bLon / 1e6)];
      const worstSnr = Math.min(
        l.snrAtoB ?? Infinity,
        l.snrBtoA ?? Infinity,
      );

      // Mercator-corrected screen-space angle so the label aligns with the
      // rendered line regardless of latitude.
      const cosLat = Math.cos(((a[0] + b[0]) / 2) * (Math.PI / 180));
      const dxScreen = (b[1] - a[1]) * cosLat;
      const dyScreen = -(b[0] - a[0]); // screen Y is inverted vs latitude
      let angleDeg = Math.atan2(dyScreen, dxScreen) * (180 / Math.PI);

      // Keep text readable: if the line runs "right to left" the label would
      // render upside-down, so flip 180°. Track the flip so we can swap the
      // directional arrows to stay geographically correct.
      let flipped = false;
      if (angleDeg > 90 || angleDeg < -90) {
        angleDeg += angleDeg > 0 ? -180 : 180;
        flipped = true;
      }

      // After a flip the label's local "→" points toward A, not B, so swap.
      const snrFwd = flipped ? l.snrBtoA : l.snrAtoB;
      const snrBwd = flipped ? l.snrAtoB : l.snrBtoA;

      let rows: string;
      if (snrFwd != null && snrBwd != null) {
        rows = `<div>→ ${snrFwd.toFixed(1)} dB</div><div>← ${snrBwd.toFixed(1)} dB</div>`;
      } else if (snrFwd != null) {
        rows = `<div>→ ${snrFwd.toFixed(1)} dB</div>`;
      } else {
        rows = `<div>← ${(snrBwd ?? 0).toFixed(1)} dB</div>`;
      }

      L.polyline([a, b], {
        color: snrFill(worstSnr),
        weight: 2,
        opacity: 0.7,
        interactive: false,
      }).addTo(layer);

      const t = STAGGER[i % 3];
      const labelLat = a[0] + (b[0] - a[0]) * t;
      const labelLon = wrapLon(a[1] + (b[1] - a[1]) * t);

      L.marker([labelLat, labelLon], {
        icon: L.divIcon({
          className: "",
          html: `<div class="meshcore-link-label" style="transform:translate(-50%,-50%) rotate(${angleDeg.toFixed(1)}deg)">${rows}</div>`,
          iconSize: [0, 0],
          iconAnchor: [0, 0],
        }),
        interactive: false,
      }).addTo(layer);
    });
  }, [links, showLinks]);

  const toggleType = useCallback((type: string) => {
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(type)) next.delete(type);
      else next.add(type);
      return next;
    });
  }, []);

  const typeCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const p of located) counts[p.type] = (counts[p.type] || 0) + 1;
    return counts;
  }, [located]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Map"
        meta={
          <span className="font-mono text-sm text-muted-foreground tabular-nums">
            {visible.length}/{peers.length} with location
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

      <section className="panel overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 px-4 py-3 border-b border-border">
          <span className="label-overline mr-2">Filter</span>
          {TYPE_FILTERS.map((t) => {
            const isHidden = hidden.has(t);
            const color = PEER_TYPE_HEX[t] || PEER_TYPE_HEX.NONE;
            const count = typeCounts[t] || 0;
            return (
              <button
                key={t}
                type="button"
                onClick={() => toggleType(t)}
                className={cn(
                  "inline-flex items-center gap-1.5 px-2 py-1 border border-border bg-card font-mono text-[10px] uppercase tracking-[0.12em] transition-all hover:border-foreground/40",
                  isHidden && "opacity-40 line-through",
                )}
              >
                <span
                  className="size-2 rounded-full"
                  style={{ background: color }}
                  aria-hidden
                />
                {t}
                <span className="tabular-nums text-muted-foreground/70">
                  {count}
                </span>
              </button>
            );
          })}
          <button
            type="button"
            onClick={() => setShowLinks((v) => !v)}
            className={cn(
              "ml-1 inline-flex items-center gap-1.5 border border-border bg-card px-2 py-1 pl-3 font-mono text-[10px] uppercase tracking-[0.12em] transition-all hover:border-foreground/40 border-l-2",
              showLinks ? "border-primary/60 text-primary" : "text-muted-foreground",
            )}
          >
            links
          </button>
          <div className="ml-auto flex items-center gap-3">
            {visible.length > 0 &&
              (clearing ? (
                <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
                  deleting…
                </span>
              ) : (
                <InlineConfirm
                  confirming={confirmClear}
                  onAskRemove={() => setConfirmClear(true)}
                  onCancel={() => setConfirmClear(false)}
                  onConfirm={deleteShown}
                  triggerLabel={`delete ${visible.length} shown`}
                />
              ))}
            <span className="flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
              <MapPin className="size-3" />
              <span className="tabular-nums">
                {visible.length}/{located.length}
              </span>
              <span className="text-muted-foreground/60">located</span>
            </span>
          </div>
        </div>

        <div
          ref={containerRef}
          className="h-[calc(100vh-260px)] min-h-105 w-full"
        />
      </section>

      <PeerDetailSheet {...sheetProps} companions={companions} />
    </div>
  );
}
