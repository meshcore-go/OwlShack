// Typed client for the signal-test REST (/api/signal-tests, /api/companions/{name}/signal-tests).
// Mirrors the shape of nodesApi.ts.

export type SignalTestStatus = "running" | "done" | "cancelled" | "interrupted";

export interface SignalTestSummary {
  id: number;
  companion: string;
  label: string;
  notes: string;
  path: string;
  pathHashSize: number;
  count: number;
  intervalSecs: number;
  status: SignalTestStatus;
  startedAt: number;
  finishedAt: number;
  runsDone: number;
  okCount: number;
}

export interface SignalTestRun {
  seq: number;
  sentAt: number;
  ok: boolean;
  hopSNRs: number[];
  snr?: number;
  elapsedMs: number;
}

export interface ValueStats {
  n: number;
  min: number;
  max: number;
  mean: number;
  stddev: number;
}

export interface HopStats extends ValueStats {
  hop: number;
}

export interface SignalTestStats {
  total: number;
  okCount: number;
  timeouts: number;
  successRate: number;
  perHop: HopStats[];
  finalSnr?: ValueStats;
  elapsedMs?: ValueStats;
}

export interface SignalTestDetail extends SignalTestSummary {
  runs: SignalTestRun[];
  stats: SignalTestStats;
}

export interface SignalTestRunWsMessage {
  action: "run";
  testId: number;
  seq: number;
  count: number;
  ok: boolean;
  hopSNRs: number[];
  snr?: number;
  elapsedMs: number;
  sentAt: number;
}

export interface SignalTestStatusWsMessage {
  action: "status";
  testId: number;
  status: SignalTestStatus;
  finishedAt: number;
}

export type SignalTestWsMessage =
  | SignalTestRunWsMessage
  | SignalTestStatusWsMessage;

async function readJSON<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const data = await res.json();
      if (data && typeof data.error === "string") msg = data.error;
    } catch {
      // ignore
    }
    throw new Error(msg);
  }
  return res.json();
}

export const signalTestApi = {
  start: (
    companion: string,
    body: {
      path: string;
      pathHashSize: number;
      count: number;
      intervalSecs: number;
      label?: string;
    },
  ) =>
    fetch(`/api/companions/${encodeURIComponent(companion)}/signal-tests`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).then((r) => readJSON<{ id: number }>(r)),

  list: (companion?: string) =>
    fetch(
      `/api/signal-tests${companion ? `?companion=${encodeURIComponent(companion)}` : ""}`,
    ).then((r) => readJSON<SignalTestSummary[]>(r)),

  get: (id: number) =>
    fetch(`/api/signal-tests/${id}`).then((r) => readJSON<SignalTestDetail>(r)),

  cancel: (id: number) =>
    fetch(`/api/signal-tests/${id}/cancel`, { method: "POST" }).then((r) =>
      readJSON<{ ok: boolean }>(r),
    ),

  update: (id: number, body: { label?: string; notes?: string }) =>
    fetch(`/api/signal-tests/${id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).then((r) => readJSON<{ ok: boolean }>(r)),

  remove: (id: number) =>
    fetch(`/api/signal-tests/${id}`, { method: "DELETE" }).then((r) => {
      if (!r.ok && r.status !== 204) throw new Error(`HTTP ${r.status}`);
    }),
};

// computeStats mirrors internal/signaltest.ComputeStats client-side, so the
// live progress panel doesn't need a round-trip after every run event.
export function computeStats(runs: SignalTestRun[]): SignalTestStats {
  const total = runs.length;
  let okCount = 0;
  const hopValues: number[][] = [];
  const finalSNRs: number[] = [];
  const elapsedMs: number[] = [];

  for (const run of runs) {
    if (run.ok) okCount++;
    // Defensive: older/corrupted rows (fixed server-side, but may still be
    // cached or already stored) can carry a null hopSNRs — a trace that
    // timed out with zero echoes ever heard.
    (run.hopSNRs || []).forEach((v, i) => {
      (hopValues[i] ??= []).push(v);
    });
    if (run.ok) {
      if (typeof run.snr === "number") finalSNRs.push(run.snr);
      elapsedMs.push(run.elapsedMs);
    }
  }

  const perHop: HopStats[] = [];
  hopValues.forEach((values, i) => {
    const vs = valueStats(values);
    if (vs) perHop.push({ hop: i + 1, ...vs });
  });

  return {
    total,
    okCount,
    timeouts: total - okCount,
    successRate: total > 0 ? okCount / total : 0,
    perHop,
    finalSnr: valueStats(finalSNRs) ?? undefined,
    elapsedMs: valueStats(elapsedMs) ?? undefined,
  };
}

function valueStats(values: number[]): ValueStats | null {
  if (values.length === 0) return null;
  let min = values[0];
  let max = values[0];
  let sum = 0;
  for (const v of values) {
    if (v < min) min = v;
    if (v > max) max = v;
    sum += v;
  }
  const mean = sum / values.length;
  let stddev = 0;
  if (values.length > 1) {
    const sq = values.reduce((acc, v) => acc + (v - mean) ** 2, 0);
    stddev = Math.sqrt(sq / (values.length - 1));
  }
  return { n: values.length, min, max, mean, stddev };
}
