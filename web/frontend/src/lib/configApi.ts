// Typed client for the per-resource config REST API (internal/api/
// routes_config_rest.go). The frontend fetches only the slice a page needs
// instead of the whole config document.
//
// Secrets are never sent to the browser: companion private keys and broker
// passwords are redacted to `privateKeySet` / `passwordSet` booleans on read.
// On write, a secret field is omitted to keep the stored value, sent to set it,
// or sent as "" to clear it.
//
// Every write is validated + reloaded server-side before it returns, so a save
// that resolves means the running config already reflects it.

// --- read shapes (GET DTOs) ---

export interface Settings {
  logLevel: string | null;
  connectionType: string;
  connection: string | null;
  baudRate: number | null;
  freq: number | null;
  bw: number | null;
  sf: number | null;
  cr: number | null;
  tx: number | null;
  listenAddr: string | null;
  setupComplete: boolean;
}

export interface MqttSettings {
  enabled: boolean | null;
  nodeCompanionId: number | null;
  iataCode: string | null;
  statusInterval: number | null;
  owner: string | null;
  email: string | null;
}

export interface Broker {
  id: number;
  name: string;
  enabled: boolean;
  dedup: boolean;
  transport: string;
  host: string;
  port: number;
  packetTopic: string | null;
  statusTopic: string | null;
  disallowedPacketTypes: string[] | null;
  retainStatus: boolean;
  tlsEnabled: boolean;
  tlsInsecure: boolean;
  authType: string;
  username: string;
  passwordSet: boolean;
  path: string;
  audience: string;
}

export interface ConfigCompanion {
  id: number;
  name: string;
  pubkey: string;
  privateKeySet: boolean;
  latitude: number | null;
  longitude: number | null;
  advertInterval: number | null;
}

export interface ConfigChannel {
  id: number;
  companionId: number;
  name: string;
  privateKeySet: boolean;
}

export interface Trigger {
  id: number;
  companionId: number;
  type: string;
  template: string;
  charLimitBehaviour: string | null;
  match: string[] | null;
  contacts: string[] | null;
  channelIds: number[] | null;
  retryTimeout: number | null;
  maxRetries: number | null;
  pathHashSize: number | null;
  schedule: string | null;
}

// --- write shapes (request bodies). Omit a secret field to keep it. ---

export interface SettingsInput {
  logLevel?: string | null;
  connectionType?: string | null;
  connection?: string | null;
  baudRate?: number | null;
  freq?: number | null;
  bw?: number | null;
  sf?: number | null;
  cr?: number | null;
  tx?: number | null;
  listenAddr?: string | null;
  // Only set by the first-run wizard; omit elsewhere so a radio edit never
  // re-opens setup.
  setupComplete?: boolean;
}

export interface MqttInput {
  enabled?: boolean | null;
  nodeCompanionId?: number | null;
  iataCode?: string | null;
  statusInterval?: number | null;
  owner?: string | null;
  email?: string | null;
}

export interface BrokerInput {
  name: string;
  enabled: boolean;
  dedup: boolean;
  transport: string;
  host: string;
  port: number;
  packetTopic?: string | null;
  statusTopic?: string | null;
  disallowedPacketTypes?: string[] | null;
  retainStatus: boolean;
  tlsEnabled: boolean;
  tlsInsecure: boolean;
  authType: string;
  username: string;
  password?: string; // omit = keep existing
  path: string;
  audience: string;
}

export interface CompanionInput {
  name: string;
  privateKey?: string; // omit = keep (update) / generate (create)
  latitude?: number | null;
  longitude?: number | null;
  advertInterval?: number | null;
}

export interface ChannelInput {
  name: string;
  privateKey?: string; // omit = keep existing
}

export interface TriggerInput {
  companionId: number;
  type: string;
  template: string;
  charLimitBehaviour?: string | null;
  match?: string[] | null;
  contacts?: string[] | null;
  channelIds?: number[] | null;
  retryTimeout?: number | null;
  maxRetries?: number | null;
  pathHashSize?: number | null;
  schedule?: string | null;
}

// --- request helper ---

// request issues a JSON request and throws the server's error message on a
// non-2xx response (validation failures come back as 422 { error }).
async function request(
  url: string,
  method: string,
  body?: unknown,
): Promise<Response> {
  const res = await fetch(url, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const err = (await res.json().catch(() => null)) as { error?: string } | null;
    throw new Error(err?.error || `request failed (${res.status})`);
  }
  return res;
}

// Write handlers that create a resource return { id } of the new/affected row.
async function requestId(url: string, method: string, body?: unknown): Promise<number> {
  const res = await request(url, method, body);
  const json = (await res.json().catch(() => null)) as { id?: number } | null;
  return json?.id ?? 0;
}

// --- write functions ---

export const configApi = {
  putSettings: (input: SettingsInput) => request("/api/config/settings", "PUT", input),
  putMqtt: (input: MqttInput) => request("/api/config/mqtt", "PUT", input),

  saveBroker: (input: BrokerInput, id?: number) =>
    id
      ? requestId(`/api/config/mqtt/brokers/${id}`, "PUT", input)
      : requestId("/api/config/mqtt/brokers", "POST", input),
  deleteBroker: (id: number) => request(`/api/config/mqtt/brokers/${id}`, "DELETE"),

  saveCompanion: (input: CompanionInput, id?: number) =>
    id
      ? requestId(`/api/config/companions/${id}`, "PUT", input)
      : requestId("/api/config/companions", "POST", input),
  deleteCompanion: (id: number) => request(`/api/config/companions/${id}`, "DELETE"),

  createChannel: (companionId: number, input: ChannelInput) =>
    requestId(`/api/config/companions/${companionId}/channels`, "POST", input),
  saveChannel: (id: number, input: ChannelInput) =>
    request(`/api/config/channels/${id}`, "PUT", input),
  deleteChannel: (id: number) => request(`/api/config/channels/${id}`, "DELETE"),

  saveTrigger: (input: TriggerInput, id?: number) =>
    id
      ? requestId(`/api/config/triggers/${id}`, "PUT", input)
      : requestId("/api/config/triggers", "POST", input),
  deleteTrigger: (id: number) => request(`/api/config/triggers/${id}`, "DELETE"),
};
