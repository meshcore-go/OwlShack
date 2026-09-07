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
import { ErrEventBadges } from "@/components/ErrEventBadges";

// compactUptime renders seconds as the two most-significant units (e.g. "5d 15h").
function compactUptime(secs: number): string {
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  const m = Math.floor((secs % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

// Hemisphere suffix instead of a bare minus sign.
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
  // When set, value/unit/extra are ignored.
  content?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("bg-card px-3 py-2.5 flex flex-col gap-1 min-w-0", className)}>
      <span
        title={label}
        className="font-mono text-[9px] uppercase tracking-[0.12em] text-muted-foreground/70 truncate"
      >
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
  // content overrides value/unit/extra; metric is the sparkline's source key.
  content?: React.ReactNode;
  colSpan?: number;
  metric?: string;
}

// channelOf extracts the channel from a "<base>_chN" metric key (0 if none).
function channelOf(key: string): number {
  const m = key.match(/_ch(\d+)$/);
  return m ? parseInt(m[1], 10) : 0;
}

// External sensors are stored channel-suffixed, so match both bare and `_chN` keys.
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

// hopSNRKeys returns a link monitor's "snr_hopN" keys, sorted by hop number.
function hopSNRKeys(m: Record<string, number>): string[] {
  return Object.keys(m)
    .filter((k) => /^snr_hop\d+$/.test(k))
    .sort(
      (a, b) =>
        parseInt(a.slice("snr_hop".length), 10) -
        parseInt(b.slice("snr_hop".length), 10),
    );
}

// hopLabel (link monitors only) resolves a hop number to "<source> → <destination>".
export function nodeTiles(
  m: Record<string, number>,
  hopLabel?: (hop: number) => string,
): TileSpec[] {
  const tiles: TileSpec[] = [];

  // Repeaters report millivolts via status; companions and sensors report volts via telemetry.
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

  // Link monitors only (a saved trace path polled on a schedule); absent for node monitors.
  if (m.success !== undefined)
    tiles.push({
      key: "success",
      label: "Delivery",
      value: m.success >= 1 ? "OK" : "LOST",
      band: m.success >= 1 ? "good" : "bad",
      metric: "success",
    });
  for (const k of hopSNRKeys(m)) {
    const hopNum = parseInt(k.slice("snr_hop".length), 10);
    const label = hopLabel ? hopLabel(hopNum) : metricDef(k).label;
    tiles.push({ key: k, label, value: m[k].toFixed(1), unit: "dB", band: snrBand(m[k]), metric: k });
  }
  if (m.elapsed_ms !== undefined)
    tiles.push({ key: "elapsed_ms", label: "RTT", value: `${Math.round(m.elapsed_ms)}`, unit: "ms", metric: "elapsed_ms" });

  if (m.noise_floor !== undefined)
    tiles.push({ key: "noise", label: "Noise floor", value: `${Math.round(m.noise_floor)}`, unit: "dBm", metric: "noise_floor" });
  if (m.uptime !== undefined)
    tiles.push({ key: "uptime", label: "Uptime", value: compactUptime(m.uptime) });
  if (m.neighbor_count !== undefined)
    tiles.push({ key: "neighbors", label: "Neighbors", value: `${Math.round(m.neighbor_count)}` });
  if (m.chan_util !== undefined)
    tiles.push({ key: "util", label: "Chan util", value: m.chan_util.toFixed(1), unit: "%", metric: "chan_util" });

  // _err_flags is a sticky bitmask, not a count — no `metric`, a bitmask trend is meaningless.
  if (m.err_events !== undefined && m.err_events > 0) {
    tiles.push({
      key: "err_events",
      label: "Errors",
      colSpan: 2,
      content: <ErrEventBadges mask={m.err_events} />,
    });
  }

  // MCU temperature is a fallback and isn't ambient; the card shows one channel, the detail page all.
  const tempKey =
    metricKeysOfType(m, "temperature")[0] ??
    (m.mcu_temperature !== undefined ? "mcu_temperature" : undefined);
  if (tempKey !== undefined)
    tiles.push({ key: "temp", label: tempKey === "mcu_temperature" ? "MCU temp" : "Temp", value: m[tempKey].toFixed(1), unit: "°C", metric: tempKey });
  const humKey = metricKeysOfType(m, "humidity")[0];
  if (humKey !== undefined)
    tiles.push({ key: "humidity", label: "Humidity", value: m[humKey].toFixed(1), unit: "%", metric: humKey });

  // No sparkline — lat/lon over time is meaningless; the detail page maps the track.
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

// The overview fetches history for exactly these keys, so it adapts to any channel set.
export function sparklineMetricKeys(m: Record<string, number>): string[] {
  return nodeTiles(m)
    .map((t) => t.metric)
    .filter((k): k is string => !!k);
}

// `className` supplies the grid-cols; `history` adds a sparkline per tile (overview only).
export function NodeStatGrid({
  metrics,
  className,
  history,
  hopLabel,
}: {
  metrics: Record<string, number>;
  className?: string;
  history?: Record<string, SeriesPoint[]>;
  hopLabel?: (hop: number) => string;
}) {
  const tiles = useMemo(
    () => nodeTiles(metrics, hopLabel),
    [metrics, hopLabel],
  );
  if (tiles.length === 0) {
    return (
      <div className="px-3 py-6 text-center">
        <span className="font-mono text-[10px] uppercase tracking-widest text-muted-foreground/50">
          awaiting first reading
        </span>
      </div>
    );
  }
  return (
    <div className={cn("grid gap-px bg-border", className || "grid-cols-2 sm:grid-cols-3")}>
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
