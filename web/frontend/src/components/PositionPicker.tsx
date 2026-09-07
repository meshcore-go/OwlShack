import { useEffect, useRef } from "react";
import L from "leaflet";
import { Label } from "@/components/ui/label";
import { themeTileLayer, useThemeTiles } from "@/lib/leaflet";
import { cn } from "@/lib/utils";

// lat/lon may be NaN (blank inputs) — the map then shows the world with no pin.
export function PositionPicker({
  lat,
  lon,
  onPick,
  className,
}: {
  lat: number;
  lon: number;
  onPick: (lat: number, lon: number) => void;
  className?: string;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const mapRef = useRef<L.Map | null>(null);
  const markerRef = useRef<L.Marker | null>(null);
  const tileRef = useRef<L.TileLayer | null>(null);
  const onPickRef = useRef(onPick);
  onPickRef.current = onPick;

  const valid = Number.isFinite(lat) && Number.isFinite(lon);

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;
    const map = L.map(containerRef.current, {
      zoomControl: true,
      attributionControl: true,
    }).setView(valid ? [lat, lon] : [0, 0], valid ? 13 : 2);
    tileRef.current = themeTileLayer().addTo(map);
    map.on("click", (e) => onPickRef.current(e.latlng.lat, e.latlng.lng));
    mapRef.current = map;
    return () => {
      map.remove();
      mapRef.current = null;
      tileRef.current = null;
      markerRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useThemeTiles(mapRef, tileRef);

  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;
    if (!valid) {
      markerRef.current?.remove();
      markerRef.current = null;
      return;
    }
    if (!markerRef.current) {
      markerRef.current = L.marker([lat, lon], { draggable: true })
        .addTo(map)
        .on("dragend", (e) => {
          const { lat: la, lng: ln } = (e.target as L.Marker).getLatLng();
          onPickRef.current(la, ln);
        });
      map.setView([lat, lon], Math.max(map.getZoom(), 12));
    } else {
      markerRef.current.setLatLng([lat, lon]);
    }
  }, [lat, lon, valid]);

  return (
    <div className={cn("space-y-1", className)}>
      <Label className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
        Pick from map · tap or drag pin
      </Label>
      <div ref={containerRef} className="h-56 sm:h-64 border border-border bg-muted" />
    </div>
  );
}

// 6 decimals (~11 cm) is what the inputs display, so a pick and a typed value round-trip identically.
export function round6(v: number): string {
  return (Math.round(v * 1e6) / 1e6).toString();
}
