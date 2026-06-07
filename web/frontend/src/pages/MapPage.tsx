import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import L from "leaflet";
import { MapPin, RefreshCw } from "lucide-react";
import { useWebSocket } from "@/hooks/useWebSocket";
import { Button } from "@/components/ui/button";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { PageHeader } from "@/components/PageHeader";
import { ConnectionPill, PEER_TYPE_HEX } from "@/components/StatusIndicator";
import { timeAgo } from "@/lib/format";
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

const TYPE_FILTERS = ["CHAT", "REPEATER", "ROOM", "SENSOR", "NONE"] as const;

const DARK_TILES =
  "https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png";
const LIGHT_TILES =
  "https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png";
const TILE_ATTRIBUTION =
  '&copy; <a href="https://www.openstreetmap.org/copyright">OSM</a> &copy; <a href="https://carto.com/attributions">CARTO</a>';

function isPeer(value: unknown): value is Peer {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return typeof v.pubkey === "string" && typeof v.name === "string";
}

function isDarkMode() {
  return document.documentElement.classList.contains("dark");
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function buildPopupHtml(p: Peer): string {
  const name = escapeHtml(p.name || "unknown");
  const type = escapeHtml(p.type || "NONE");
  const snr = p.snr == null ? "—" : `${p.snr.toFixed(1)}dB`;
  return `
    <div style="font-family: var(--font-sans); min-width: 160px;">
      <div style="font-weight: 600; font-size: 13px; margin-bottom: 4px;">${name}</div>
      <div style="display:flex; gap:6px; font-family: var(--font-mono); font-size: 10px; text-transform: uppercase; letter-spacing: 0.08em; color: var(--muted-foreground);">
        <span>${type}</span><span>·</span><span>${snr}</span><span>·</span><span>${escapeHtml(timeAgo(p.lastSeen))}</span>
      </div>
    </div>
  `;
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

export function MapPage() {
  const [peers, setPeers] = useState<Peer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const [dark, setDark] = useState<boolean>(() => isDarkMode());

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
  const fittedRef = useRef(false);

  const handleMessage = useCallback((topic: string, data: unknown) => {
    if (topic !== "peers") return;
    if (!isPeer(data)) return;
    setPeers((prev) => {
      const idx = prev.findIndex((p) => p.pubkey === data.pubkey);
      if (idx === -1) return [data, ...prev];
      const next = prev.slice();
      next[idx] = { ...next[idx], ...data };
      return next;
    });
  }, []);

  const { connected } = useWebSocket(["peers"], handleMessage);

  const load = useCallback(() => {
    setLoading(true);
    setError(null);
    fetch("/api/peers")
      .then((r) => {
        if (!r.ok) throw new Error("peers");
        return r.json();
      })
      .then((data: Peer[]) => setPeers(data || []))
      .catch(() => setError("Failed to load peers"))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    load();
  }, [load]);

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
    const layer = L.tileLayer(isDarkMode() ? DARK_TILES : LIGHT_TILES, {
      attribution: TILE_ATTRIBUTION,
      subdomains: "abcd",
      maxZoom: 19,
    }).addTo(map);
    mapRef.current = map;
    tileLayerRef.current = layer;

    return () => {
      map.remove();
      mapRef.current = null;
      tileLayerRef.current = null;
      markersRef.current.clear();
      focusMarkerRef.current = null;
      fittedRef.current = false;
    };
  }, []);

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
        icon: focusIcon(),
        zIndexOffset: 1000,
      })
        .addTo(map)
        .bindPopup(
          `<div style="font-family: var(--font-mono); font-size: 11px; text-transform: uppercase; letter-spacing: 0.08em;">shared location</div><div style="font-family: var(--font-mono); font-size: 11px; color: var(--muted-foreground);">${focus.lat}, ${focus.lon}</div>`,
        );
    }
    focusMarkerRef.current.openPopup();
  }, [focus]);

  // Watch for theme changes
  useEffect(() => {
    const observer = new MutationObserver(() => {
      const next = isDarkMode();
      setDark((prev) => (prev === next ? prev : next));
    });
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });
    return () => observer.disconnect();
  }, []);

  // Swap tile layer when theme changes
  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;
    if (tileLayerRef.current) {
      map.removeLayer(tileLayerRef.current);
    }
    tileLayerRef.current = L.tileLayer(dark ? DARK_TILES : LIGHT_TILES, {
      attribution: TILE_ATTRIBUTION,
      subdomains: "abcd",
      maxZoom: 19,
    }).addTo(map);
  }, [dark]);

  const located = useMemo(
    () => peers.filter((p) => p.lat !== 0 || p.lon !== 0),
    [peers],
  );

  const visible = useMemo(
    () => located.filter((p) => !hidden.has(p.type)),
    [located, hidden],
  );

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
      const lon = p.lon / 1e6;
      const color = PEER_TYPE_HEX[p.type] || PEER_TYPE_HEX.NONE;
      const existing = markers.get(p.pubkey);
      if (existing) {
        existing.setLatLng([lat, lon]);
        existing.setIcon(dotIcon(color));
        existing.setPopupContent(buildPopupHtml(p));
      } else {
        const marker = L.marker([lat, lon], { icon: dotIcon(color) })
          .addTo(map)
          .bindPopup(buildPopupHtml(p));
        markers.set(p.pubkey, marker);
      }
    }

    // Auto-fit on first load
    if (!fittedRef.current && visible.length > 0) {
      const bounds = L.latLngBounds(
        visible.map((p) => [p.lat / 1e6, p.lon / 1e6] as [number, number]),
      );
      if (bounds.isValid()) {
        map.fitBounds(bounds, { padding: [40, 40], maxZoom: 12 });
        fittedRef.current = true;
      }
    }
  }, [visible]);

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
              onClick={load}
              className="h-7 gap-1.5 px-2 font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground hover:text-primary"
            >
              <RefreshCw className={cn("size-3", loading && "animate-spin")} />
              refresh
            </Button>
            <ConnectionPill connected={connected} />
          </>
        }
      />

      {error && (
        <Alert variant="destructive">
          <AlertTitle className="font-mono uppercase tracking-[0.1em]">
            Error
          </AlertTitle>
          <AlertDescription>
            {error}
            <Button
              variant="ghost"
              size="sm"
              onClick={load}
              className="ml-2 h-7 text-xs uppercase tracking-[0.1em]"
            >
              retry
            </Button>
          </AlertDescription>
        </Alert>
      )}

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
          <div className="ml-auto flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
            <MapPin className="size-3" />
            <span className="tabular-nums">
              {visible.length}/{located.length}
            </span>
            <span className="text-muted-foreground/60">located</span>
          </div>
        </div>

        <div
          ref={containerRef}
          className="h-[calc(100vh-260px)] min-h-[420px] w-full"
        />
      </section>
    </div>
  );
}
