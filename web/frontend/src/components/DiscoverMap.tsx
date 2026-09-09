import { useEffect, useRef } from "react";
import L from "leaflet";
import { themeTileLayer, useThemeTiles } from "@/lib/leaflet";
import { snrFill } from "@/components/SignalStrength";
import { cn } from "@/lib/utils";

export interface DiscoverMapNode {
  pubkey: string;
  name: string;
  snr: number;
  reportedSnr: number;
  lat: number;
  lon: number;
}

// Sized to stay legible against a tile background, and coloured by how well we hear the node so the
// map answers the same question the table does.
function dotIcon(color: string): L.DivIcon {
  return L.divIcon({
    className: "meshcore-discover-dot",
    html: `<span style="display:block;width:14px;height:14px;border-radius:9999px;background:${color};box-shadow:0 0 0 2px rgba(0,0,0,0.55),0 0 8px ${color};"></span>`,
    iconSize: [14, 14],
    iconAnchor: [7, 7],
  });
}

export function DiscoverMap({
  nodes,
  onSelect,
  height = 460,
  className,
}: {
  nodes: DiscoverMapNode[];
  onSelect: (pubkey: string) => void;
  height?: number;
  className?: string;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const mapRef = useRef<L.Map | null>(null);
  const tileRef = useRef<L.TileLayer | null>(null);
  const layerRef = useRef<L.LayerGroup | null>(null);

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;
    const map = L.map(containerRef.current, {
      zoomControl: true,
      attributionControl: true,
    }).setView([0, 0], 2);
    tileRef.current = themeTileLayer().addTo(map);
    layerRef.current = L.layerGroup().addTo(map);
    mapRef.current = map;
    return () => {
      map.remove();
      mapRef.current = null;
      tileRef.current = null;
      layerRef.current = null;
    };
  }, []);

  useThemeTiles(mapRef, tileRef);

  useEffect(() => {
    const map = mapRef.current;
    const layer = layerRef.current;
    if (!map || !layer) return;
    layer.clearLayers();
    if (nodes.length === 0) return;

    for (const n of nodes) {
      L.marker([n.lat, n.lon], { icon: dotIcon(snrFill(n.snr)) })
        .addTo(layer)
        .bindTooltip(
          `${n.name || "unknown"} · we hear ${n.snr.toFixed(2)} dB · they hear ${n.reportedSnr.toFixed(2)} dB`,
        )
        .on("click", () => onSelect(n.pubkey));
    }

    const bounds = L.latLngBounds(nodes.map((n) => [n.lat, n.lon] as [number, number]));
    if (nodes.length === 1) {
      map.setView([nodes[0].lat, nodes[0].lon], 13);
    } else {
      map.fitBounds(bounds, { padding: [32, 32] });
    }
  }, [nodes, onSelect]);

  // Leaflet measures the container on creation, so a tab that mounts hidden comes back zero-height.
  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;
    const obs = new ResizeObserver(() => map.invalidateSize());
    if (containerRef.current) obs.observe(containerRef.current);
    return () => obs.disconnect();
  }, []);

  return (
    <div className={cn("relative border border-border rounded-md overflow-hidden", className)} style={{ height }}>
      <div ref={containerRef} className="absolute inset-0" />
    </div>
  );
}
