import { useMemo } from "react";
import { MapPin } from "lucide-react";
import { cn } from "@/lib/utils";
import {
  BAND_TEXT,
  batteryBand,
  batteryPercent,
  metricDef,
  rssiBand,
  snrBand,
  type Band,
} from "@/lib/metrics";
import { Sparkline, type SeriesPoint } from "@/components/MetricChart";

// compactUptime renders seconds as the two most-significant units (e.g. "5d 15h").
function compactUptime(secs: number): string {
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  const m = Math.floor((secs % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

// Coordinate formatting with hemisphere suffixes — reads clearer than a bare
// minus sign and matches how coordinates are conventionally shown.
function fmtLat(v: number): string {
  return `${Math.abs(v).toFixed(4)}°${v >= 0 ? "N" : "S"}`;
}
function fmtLon(v: number): string {
  return `${Math.abs(v).toFixed(4)}°${v >= 0 ? "E" : "W"}`;
}

export function StatTile({
  label,
  value,
  unit,
  band = "none",
  extra,
  content,
  className,
}: {
  label: string;
  value?: string;
  unit?: string;
  band?: Band;
  extra?: React.ReactNode;
  // content replaces the single big-value layout for tiles that don't fit it
  // (e.g. a coordinate pair). When set, value/unit/extra are ignored.
  content?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("bg-card px-3 py-2.5 flex flex-col gap-1 min-w-0", className)}>
      <span className="font-mono text-[9px] uppercase tracking-[0.12em] text-muted-foreground/70 truncate">
        {label}
      </span>
      {content ?? (
        <>
          <div className="flex items-baseline gap-1">
            <span
              className={cn(
                "font-mono text-xl font-semibold tabular-nums leading-none",
                BAND_TEXT[band],
              )}
            >
              {value}
            </span>
            {unit && (
              <span className="font-mono text-[10px] text-muted-foreground/60">
                {unit}
              </span>
            )}
          </div>
          {extra}
        </>
      )}
    </div>
  );
}

interface TileSpec {
  key: string;
  label: string;
  value?: string;
  unit?: string;
  band?: Band;
  extra?: React.ReactNode;
  // content: custom tile body (overrides value/unit/extra). colSpan widens the
  // tile in the grid. metric: source key for the trend sparkline.
  content?: React.ReactNode;
  colSpan?: number;
  metric?: string;
}

// channelOf extracts the channel from a "<base>_chN" metric key (0 if none).
function channelOf(key: string): number {
  const m = key.match(/_ch(\d+)$/);
  return m ? parseInt(m[1], 10) : 0;
}

// metricKeysOfType returns the keys in `m` of a given LPP base type — its bare
// and channel-suffixed variants — lowest channel first. External sensors are
// stored channel-suffixed, so this finds them on whatever channel the firmware
// used, and adapts to boards with any number of sensors.
const typeKeyRes = new Map<string, RegExp>();

function metricKeysOfType(m: Record<string, number>, base: string): string[] {
  let re = typeKeyRes.get(base);
  if (!re) {
    re = new RegExp(`^${base}(_ch\\d+)?$`);
    typeKeyRes.set(base, re);
  }
  return Object.keys(m)
    .filter((k) => re.test(k))
    .sort((a, b) => channelOf(a) - channelOf(b));
}

// nodeTiles maps a metrics snapshot to the ordered tiles shown on a node, in the
// reference dashboard's layout: battery, signal, then optional sensor tiles
// (only rendered when the reading exists).
export function nodeTiles(m: Record<string, number>): TileSpec[] {
  const tiles: TileSpec[] = [];

  // Battery: repeaters report millivolts via status (battery_mv); companions /
  // sensors report volts via telemetry (battery). Resolve to mV for the % + band.
  const battMv = m.battery_mv ?? (m.battery !== undefined ? m.battery * 1000 : undefined);
  if (battMv !== undefined) {
    const band = batteryBand(battMv);
    const pct = batteryPercent(battMv);
    tiles.push({
      key: "battery",
      label: "Battery",
      value: `${pct}`,
      unit: "%",
      band,
      metric: m.battery_mv !== undefined ? "battery_mv" : "battery",
      extra: (
        <span className="font-mono text-[10px] text-muted-foreground/60 tabular-nums">
          {(battMv / 1000).toFixed(2)} V
        </span>
      ),
    });
  }
  if (m.last_rssi !== undefined)
    tiles.push({ key: "rssi", label: "RSSI", value: `${Math.round(m.last_rssi)}`, unit: "dBm", band: rssiBand(m.last_rssi), metric: "last_rssi" });
  if (m.last_snr !== undefined)
    tiles.push({ key: "snr", label: "SNR", value: m.last_snr.toFixed(1), unit: "dB", band: snrBand(m.last_snr), metric: "last_snr" });
  if (m.noise_floor !== undefined)
    tiles.push({ key: "noise", label: "Noise floor", value: `${Math.round(m.noise_floor)}`, unit: "dBm", metric: "noise_floor" });
  if (m.uptime !== undefined)
    tiles.push({ key: "uptime", label: "Uptime", value: compactUptime(m.uptime) });
  if (m.neighbor_count !== undefined)
    tiles.push({ key: "neighbors", label: "Neighbors", value: `${Math.round(m.neighbor_count)}` });
  if (m.chan_util !== undefined)
    tiles.push({ key: "util", label: "Chan util", value: m.chan_util.toFixed(1), unit: "%", metric: "chan_util" });

  // Prefer an external temperature sensor (any channel); fall back to the MCU's
  // own temperature, labelled "MCU temp" so it's clear it isn't ambient. Humidity
  // is always an external sensor. The detail page shows every channel; the card
  // shows one representative.
  const tempKey =
    metricKeysOfType(m, "temperature")[0] ??
    (m.mcu_temperature !== undefined ? "mcu_temperature" : undefined);
  if (tempKey !== undefined)
    tiles.push({ key: "temp", label: tempKey === "mcu_temperature" ? "MCU temp" : "Temp", value: m[tempKey].toFixed(1), unit: "°C", metric: tempKey });
  const humKey = metricKeysOfType(m, "humidity")[0];
  if (humKey !== undefined)
    tiles.push({ key: "humidity", label: "Humidity", value: m[humKey].toFixed(1), unit: "%", metric: humKey });

  // Location: a coordinate pair shown at equal weight (NOT one big + one small),
  // with hemisphere notation, altitude beneath, and a pin hinting it opens the
  // track map on the detail page. Spans 2 columns for room. No sparkline — lat/lon
  // over time is meaningless; the detail page draws the actual track on a map.
  if (m.location_lat !== undefined && m.location_lon !== undefined) {
    tiles.push({
      key: "location",
      label: "Location",
      colSpan: 2,
      content: (
        <div className="flex flex-col gap-1">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 font-mono text-sm font-semibold tabular-nums leading-none">
            <span>{fmtLat(m.location_lat)}</span>
            <span>{fmtLon(m.location_lon)}</span>
          </div>
          <span className="flex items-center gap-1 font-mono text-[10px] tabular-nums text-muted-foreground/60">
            <MapPin className="size-3 shrink-0" />
            {m.location_alt !== undefined ? `${m.location_alt.toFixed(0)} m` : "view on map"}
          </span>
        </div>
      ),
    });
  }

  return tiles;
}

// sparklineMetricKeys returns the metric keys backing a node's tiles — i.e. the
// ones that get a trend sparkline. The overview fetches history for exactly
// these, so it adapts to whatever channels a board exposes without a hardcoded
// list.
export function sparklineMetricKeys(m: Record<string, number>): string[] {
  return nodeTiles(m)
    .map((t) => t.metric)
    .filter((k): k is string => !!k);
}

// NodeStatGrid renders a node's current-value tiles in a 1px-separated grid.
// `className` should supply the grid-cols (defaults to 3 columns). When `history`
// (metric → series) is supplied, a trend sparkline is drawn under each tile that
// has a matching series — the overview cards pass it; the detail page omits it
// (it has full charts below).
export function NodeStatGrid({
  metrics,
  className,
  history,
}: {
  metrics: Record<string, number>;
  className?: string;
  history?: Record<string, SeriesPoint[]>;
}) {
  const tiles = useMemo(() => nodeTiles(metrics), [metrics]);
  if (tiles.length === 0) {
    return (
      <div className="px-3 py-6 text-center">
        <span className="font-mono text-[10px] uppercase tracking-[0.1em] text-muted-foreground/50">
          awaiting first reading
        </span>
      </div>
    );
  }
  return (
    <div className={cn("grid gap-px bg-border", className || "grid-cols-3")}>
      {tiles.map((t) => {
        const series = t.metric && history ? history[t.metric] : undefined;
        const spark =
          t.metric && series && series.length >= 2 ? (
            <Sparkline data={series} color={metricDef(t.metric).color} height={24} />
          ) : null;
        return (
          <StatTile
            key={t.key}
            label={t.label}
            value={t.value}
            unit={t.unit}
            band={t.band}
            content={t.content}
            className={t.colSpan === 2 ? "col-span-2" : undefined}
            extra={
              t.extra || spark ? (
                <>
                  {t.extra}
                  {spark}
                </>
              ) : undefined
            }
          />
        );
      })}
    </div>
  );
}
