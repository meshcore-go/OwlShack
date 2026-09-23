// Local sensors on this host, unrelated to the SENSOR peer type reached over the radio.
import { apiErrorMessage } from "@/lib/apiError";

export interface SensorReading {
  metric: string;
  label?: string;
  value: number;
  unit?: string;
  // flag is a yes or no carried as 1 or 0; count is a whole number.
  format: "number" | "flag" | "count";
}

// A Binding names one reading as an expression calls it, so renaming a sensor cannot break it.
export interface SensorBinding {
  name: string;
  sensorId: number;
  metric: string;
}

export interface Sensor {
  id: number;
  provider: string;
  kind: string;
  name: string;
  options: Record<string, string>;
  bindings: SensorBinding[];
  readings: SensorReading[];
  // null until read once, separating a first read pending from one that reported nothing.
  at: string | null;
  // empty while the latest attempt worked.
  error: string;
}

export interface SensorCandidate {
  kind: string;
  provider: string;
  label: string;
  detail?: string;
  // false for a part this build cannot drive; still listed, or an unknown part reads as an empty bus.
  addable: boolean;
  options: Record<string, string>;
}

export interface SensorField {
  key: string;
  label: string;
  help?: string;
  default?: string;
  // when set, the value must be one of these and the form offers a picker
  choices?: string[];
  required: boolean;
  // multiline means the value wants room to grow, such as an expression.
  multiline?: boolean;
  // identifies means the value says which part this is, so a card can name it without every option.
  identifies?: boolean;
}

// SensorKind carries description, category and metrics so the catalogue is searchable by part number or need.
export interface SensorKind {
  kind: string;
  provider: string;
  label: string;
  description?: string;
  category?: string;
  metrics?: string[];
  // binds means this kind reads other sensors, so the form offers a binding editor.
  binds?: boolean;
  fields: SensorField[];
}

// A provider that could not be scanned, kept apart so an unreadable bus never reads as an empty one.
export interface SensorProviderProblem {
  provider: string;
  label: string;
  reason: string;
}

export interface SensorScan {
  candidates: SensorCandidate[];
  problems: SensorProviderProblem[];
}

// An empty provider scans them all.
export async function discoverSensors(provider = ""): Promise<SensorScan> {
  const res = await fetch("/api/sensors/discover", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ provider }),
  });
  if (!res.ok) throw new Error(await apiErrorMessage(res));
  const body = (await res.json()) as Partial<SensorScan>;
  return { candidates: body.candidates || [], problems: body.problems || [] };
}

// SensorInput is what the add and edit form sends. Bindings are only set by derived sensors.
export interface SensorInput {
  provider: string;
  kind: string;
  name: string;
  options: Record<string, string>;
  bindings?: SensorBinding[];
}

export async function createSensor(input: SensorInput): Promise<void> {
  const res = await fetch("/api/sensors", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!res.ok) throw new Error(await apiErrorMessage(res));
}

export async function updateSensor(id: number, input: SensorInput): Promise<void> {
  const res = await fetch(`/api/sensors/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!res.ok) throw new Error(await apiErrorMessage(res));
}

export async function deleteSensor(id: number): Promise<void> {
  const res = await fetch(`/api/sensors/${id}`, { method: "DELETE" });
  if (!res.ok) throw new Error(await apiErrorMessage(res));
}
