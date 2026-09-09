import { useEffect } from "react";
import L from "leaflet";
import markerIcon from "leaflet/dist/images/marker-icon.png";
import markerIcon2x from "leaflet/dist/images/marker-icon-2x.png";
import markerShadow from "leaflet/dist/images/marker-shadow.png";

// Leaflet's default marker icon URLs break under bundlers; rebind them once here.
type MarkerProto = L.Icon.Default & { _getIconUrl?: () => string };
delete (L.Icon.Default.prototype as MarkerProto)._getIconUrl;
L.Icon.Default.mergeOptions({
  iconUrl: markerIcon,
  iconRetinaUrl: markerIcon2x,
  shadowUrl: markerShadow,
});

// https://carto.com/basemaps/apikey/

export const TILE_ATTRIBUTION =
  '&copy; <a href="https://www.openstreetmap.org/copyright">OSM</a> &copy; <a href="https://carto.com/attributions">CARTO</a>';

let tileKey = "";
let tileKeySet = false; // a save beat the fetch below; don't let it win
const tileKeyReady: Promise<void> = fetch("/api/config/settings")
  .then((r) => (r.ok ? r.json() : null))
  .then((s: { mapTileKey?: string | null } | null) => {
    if (!tileKeySet) tileKey = s?.mapTileKey ?? "";
  })
  .catch(() => {});

export function setTileKey(key: string | null) {
  tileKey = key ?? "";
  tileKeySet = true;
}

export function tileUrlForTheme(): string {
  const isDark = document.documentElement.classList.contains("dark");
  const style = isDark ? "dark_all" : "light_all";
  const key = tileKey ? `?key=${encodeURIComponent(tileKey)}` : "";
  return `https://basemaps.cartocdn.com/rastertiles/${style}/{z}/{x}/{y}{r}.png${key}`;
}

export function wrapLon(lon: number): number {
  return (((lon + 180) % 360) + 360) % 360 - 180;
}

// Peer coordinates are firmware fixed-point at 1e6 degrees. Passing the raw values to Leaflet puts
// the marker nowhere at all.
export function peerLatLon(lat: number, lon: number): [number, number] {
  return [lat / 1e6, wrapLon(lon / 1e6)];
}

export function themeTileLayer(): L.TileLayer {
  const layer = L.tileLayer(tileUrlForTheme(), {
    attribution: TILE_ATTRIBUTION,
    maxZoom: 19,
  });
  tileKeyReady.then(() => layer.setUrl(tileUrlForTheme()));
  return layer;
}

export function useThemeTiles(
  mapRef: { current: L.Map | null },
  tileRef: { current: L.TileLayer | null },
) {
  useEffect(() => {
    const obs = new MutationObserver(() => {
      const map = mapRef.current;
      if (!map) return;
      tileRef.current?.remove();
      tileRef.current = themeTileLayer().addTo(map);
    });
    obs.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });
    return () => obs.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}
