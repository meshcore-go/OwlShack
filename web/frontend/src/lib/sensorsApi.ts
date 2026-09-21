// Local sensors attached to this host. Unrelated to the SENSOR peer type, which is a remote
// MeshCore node reached over the radio.
import { apiErrorMessage } from "@/lib/apiError";

export interface SensorReading {
  metric: string;
  label?: string;
  value: number;
  unit?: string;
}

export interface Sensor {
  id: number;
  provider: string;
  kind: string;
  name: string;
  options: Record<string, string>;
  readings: SensorReading[];
  // null until the sensor has been read once, which is what separates a sensor
  // waiting for its first read from one that read and reported nothing.
  at: string | null;
  error?: string;
}

export interface SensorProvider {
  id: string;
  label: string;
  available: boolean;
  reason?: string;
}

export interface SensorCandidate {
  kind: string;
  label: string;
  detail?: string;
  options: Record<string, string>;
}

export async function discoverSensors(provider: string): Promise<SensorCandidate[]> {
  const res = await fetch("/api/sensors/discover", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ provider }),
  });
  if (!res.ok) throw new Error(await apiErrorMessage(res));
  const body = (await res.json()) as { candidates: SensorCandidate[] | null };
  return body.candidates || [];
}

export async function createSensor(input: {
  provider: string;
  kind: string;
  name: string;
  options: Record<string, string>;
}): Promise<void> {
  const res = await fetch("/api/sensors", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!res.ok) throw new Error(await apiErrorMessage(res));
}

export async function deleteSensor(id: number): Promise<void> {
  const res = await fetch(`/api/sensors/${id}`, { method: "DELETE" });
  if (!res.ok) throw new Error(await apiErrorMessage(res));
}
