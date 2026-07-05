import { formatBattery, formatUptime } from "@/lib/format";

// Shared catalogue of node-monitoring metrics. Cards and history charts both
// read from here so labels, units, formatting and chart-axis assignment stay
// consistent. Keys match the metric names the Go collector emits
// (statusReadings in internal/app/monitor_collector.go); telemetry metrics are
// dynamic, so unknown keys fall back to a humanized label.

export type MetricKind = "gauge" | "counter";

export interface MetricDef {
  label: string;
  unit?: string;
  format: (v: number) => string;
  // Chart token (--chart-1..5) used for the series colour.
  color: string;
  // gauge   = instantaneous value (battery, SNR, temperature) → area chart.
  // counter = monotonic-since-boot total (packets, airtime, dups, errors). A raw
  //           line only ever climbs and is meaningless, so we plot the per-bucket
  //           delta (rate of change) as bars instead.
  kind: MetricKind;
  // When true the metric is shown only as a current-value stat tile, never
  // charted — a time-series of it carries no information (e.g. uptime climbs
  // 1:1 with wall-clock, so a line is a 45° ramp and its per-bucket delta is
  // just the bucket size).
  noChart?: boolean;
}

const COUNT = (v: number) => Math.round(v).toLocaleString();

export const METRIC_DEFS: Record<string, MetricDef> = {
  // power
  battery_mv: { label: "Battery", unit: "V", format: formatBattery, color: "var(--chart-3)", kind: "gauge" },
  battery: { label: "Cell voltage", unit: "V", format: (v) => `${v.toFixed(2)} V`, color: "var(--chart-3)", kind: "gauge" },
  // signal
  last_snr: { label: "SNR", unit: "dB", format: (v) => `${v.toFixed(1)} dB`, color: "var(--chart-1)", kind: "gauge" },
  last_rssi: { label: "RSSI", unit: "dBm", format: (v) => `${Math.round(v)} dBm`, color: "var(--chart-2)", kind: "gauge" },
  noise_floor: { label: "Noise floor", unit: "dBm", format: (v) => `${Math.round(v)} dBm`, color: "var(--chart-4)", kind: "gauge" },
  chan_util: { label: "Channel util", unit: "%", format: (v) => `${v.toFixed(1)}%`, color: "var(--chart-5)", kind: "gauge" },
  // environment sensors (telemetry). External sensors use clean names; the
  // collector only appends "_chN" when a node has multiple of the same type, and
  // metricDef() resolves those back to these defs. (Firmware <=1.15 puts these on
  // the self channel, 1.16+ on their own — same key either way.)
  temperature: { label: "Temperature", unit: "°C", format: (v) => `${v.toFixed(1)}°C`, color: "var(--chart-5)", kind: "gauge" },
  humidity: { label: "Humidity", unit: "%", format: (v) => `${v.toFixed(1)}%`, color: "var(--chart-2)", kind: "gauge" },
  pressure: { label: "Pressure", unit: "hPa", format: (v) => `${v.toFixed(0)} hPa`, color: "var(--chart-1)", kind: "gauge" },
  mcu_temperature: { label: "MCU temp", unit: "°C", format: (v) => `${v.toFixed(1)}°C`, color: "var(--chart-4)", kind: "gauge" },
  // health / state
  uptime: { label: "Uptime", format: formatUptime, color: "var(--chart-3)", kind: "gauge", noChart: true },
  neighbor_count: { label: "Neighbors", format: COUNT, color: "var(--chart-3)", kind: "gauge" },
  queue_len: { label: "TX queue", format: COUNT, color: "var(--chart-2)", kind: "gauge" },
  // traffic (counters → rate bars)
  packets_recv: { label: "Packets recv", format: COUNT, color: "var(--chart-1)", kind: "counter" },
  packets_sent: { label: "Packets sent", format: COUNT, color: "var(--chart-2)", kind: "counter" },
  flood_rx: { label: "Flood RX", format: COUNT, color: "var(--chart-3)", kind: "counter" },
  flood_tx: { label: "Flood TX", format: COUNT, color: "var(--chart-1)", kind: "counter" },
  direct_rx: { label: "Direct RX", format: COUNT, color: "var(--chart-4)", kind: "counter" },
  direct_tx: { label: "Direct TX", format: COUNT, color: "var(--chart-2)", kind: "counter" },
  rx_air_secs: { label: "RX airtime", unit: "s", format: COUNT, color: "var(--chart-5)", kind: "counter" },
  tx_air_secs: { label: "TX airtime", unit: "s", format: COUNT, color: "var(--chart-4)", kind: "counter" },
  // errors (counters → rate bars)
  // _err_flags bitmask (sticky fatal-event flags) — an instantaneous flag set,
  // not a count, so kind is "gauge" and noChart suppresses it; it's decoded into
  // labels via lib/errEvents and shown as chips instead. See Dispatcher.h.
  err_events: { label: "Error events", format: COUNT, color: "var(--chart-2)", kind: "gauge", noChart: true },
  recv_errors: { label: "Recv errors", format: COUNT, color: "var(--chart-4)", kind: "counter" },
  flood_dups: { label: "Flood dups", format: COUNT, color: "var(--chart-5)", kind: "counter" },
  direct_dups: { label: "Direct dups", format: COUNT, color: "var(--chart-1)", kind: "counter" },
  // link monitoring (a saved trace path, polled on a schedule — see
  // internal/app's link collector). success is 0/1 per poll; snr_hopN is
  // resolved dynamically below (one series per hop of the monitored path).
  success: { label: "Delivery", format: (v) => (v >= 1 ? "OK" : "LOST"), color: "var(--chart-1)", kind: "gauge" },
  elapsed_ms: { label: "RTT", unit: "ms", format: (v) => `${Math.round(v)} ms`, color: "var(--chart-2)", kind: "gauge" },
};

// METRIC_ORDER is the canonical display order for charts/tiles, grouped
// logically (power → signal → sensors → health → traffic → errors). The detail
// page sorts available metrics by this; unknown keys sort to the end.
export const METRIC_ORDER: string[] = Object.keys(METRIC_DEFS);

// metricOrderIndex gives a metric's position in METRIC_ORDER, resolving a
// channel-suffixed external sensor ("temperature_ch2") to its base so it sorts
// alongside the catalogue instead of falling to the end. -1 if truly unknown.
export function metricOrderIndex(key: string): number {
  const i = METRIC_ORDER.indexOf(key);
  if (i >= 0) return i;
  if (/^snr_hop\d+$/.test(key)) return METRIC_ORDER.indexOf("last_snr");
  return METRIC_ORDER.indexOf(key.replace(/_ch\d+$/, ""));
}

// humanizeMetric turns a snake/suffix metric key into a readable label, used for
// telemetry metrics (temperature, humidity, voltage, …_ch2) that aren't in the
// static catalogue.
export function humanizeMetric(key: string): string {
  return key
    .replace(/_/g, " ")
    .replace(/\bch(\d+)\b/g, "ch $1")
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

export function metricDef(key: string): MetricDef {
  if (METRIC_DEFS[key]) return METRIC_DEFS[key];
  // A "_chN"-suffixed key is a disambiguated duplicate sensor (e.g. a second
  // temperature probe). Resolve it to the base def so it keeps the right unit /
  // colour / format, tagging the label with the channel.
  const ch = key.match(/^(.+)_ch(\d+)$/);
  if (ch && METRIC_DEFS[ch[1]]) {
    return { ...METRIC_DEFS[ch[1]], label: `${METRIC_DEFS[ch[1]].label} (ch${ch[2]})` };
  }
  // "snr_hopN" is one series per hop of a monitored link's path (see the link
  // collector); reuse last_snr's format/colour, just relabeled per hop.
  const hop = key.match(/^snr_hop(\d+)$/);
  if (hop) {
    return { ...METRIC_DEFS.last_snr, label: `SNR hop ${hop[1]}` };
  }
  return {
    label: humanizeMetric(key),
    format: (v: number) => (Number.isInteger(v) ? v.toString() : v.toFixed(2)),
    color: "var(--chart-1)",
    kind: "gauge",
  };
}

export function metricLabel(key: string): string {
  return metricDef(key).label;
}

export function formatMetric(key: string, v: number): string {
  return metricDef(key).format(v);
}

// ── threshold colouring (mirrors the reference dashboard's bands) ──

export type Band = "good" | "warn" | "bad" | "none";

export function batteryBand(mv: number): Band {
  if (mv <= 0) return "none";
  if (mv >= 3800) return "good";
  if (mv >= 3500) return "warn";
  return "bad";
}

// batteryPercent estimates Li-ion charge from millivolts (3.3 V empty → 4.2 V full).
export function batteryPercent(mv: number): number {
  const pct = ((mv / 1000 - 3.3) / (4.2 - 3.3)) * 100;
  return Math.max(0, Math.min(100, Math.round(pct)));
}

export function snrBand(snr: number): Band {
  if (snr >= 8) return "good";
  if (snr >= 0) return "warn";
  return "bad";
}

export function rssiBand(rssi: number): Band {
  if (rssi >= 0) return "none";
  if (rssi > -90) return "good";
  if (rssi >= -110) return "warn";
  return "bad";
}

export const BAND_TEXT: Record<Band, string> = {
  good: "text-success",
  warn: "text-warning",
  bad: "text-signal-weak",
  none: "text-muted-foreground",
};

export const BAND_FILL: Record<Band, string> = {
  good: "bg-success",
  warn: "bg-warning",
  bad: "bg-signal-weak",
  none: "bg-muted-foreground/40",
};
