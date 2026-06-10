import { useEffect } from "react";
import L from "leaflet";

// Shared theme-aware CARTO tile plumbing for every Leaflet usage (MapPage,
// TrackMap, RepeaterDetailPage position picker). Tiles follow <html>'s dark
// class, swapped live via a MutationObserver.

export const TILE_ATTRIBUTION =
  '&copy; <a href="https://www.openstreetmap.org/copyright">OSM</a> &copy; <a href="https://carto.com/attributions">CARTO</a>';

export function tileUrlForTheme(): string {
  const isDark = document.documentElement.classList.contains("dark");
  return isDark
    ? "https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png"
    : "https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png";
}

export function themeTileLayer(): L.TileLayer {
  return L.tileLayer(tileUrlForTheme(), {
    attribution: TILE_ATTRIBUTION,
    subdomains: "abcd",
    maxZoom: 19,
  });
}

// Replaces the map's tile layer whenever the theme class flips.
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
    // Refs are stable containers; observing them in deps is unnecessary.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}
