// Shared retry dropdown options for the node-monitoring poller
// (internal/monitor), used by both MonitoringSettings and LinkMonitorSettings.
export const RETRY_OPTS: { value: string; label: string }[] = [
  { value: "0", label: "Default (5m)" },
  { value: "60", label: "1 min" },
  { value: "300", label: "5 min" },
  { value: "900", label: "15 min" },
  { value: "1800", label: "30 min" },
];

// "0" = use the poller default, "-1" = no retries at all (distinct sentinels).
export const MAX_RETRIES_OPTS: { value: string; label: string }[] = [
  { value: "0", label: "Default (3)" },
  { value: "-1", label: "None (no retry)" },
  { value: "1", label: "1" },
  { value: "2", label: "2" },
  { value: "3", label: "3" },
  { value: "5", label: "5" },
  { value: "10", label: "10" },
];
