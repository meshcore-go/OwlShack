import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";

export interface ChannelRef {
  name: string;
  privateKey?: string;
}

export interface TriggerConfig {
  type: string;
  template: string;
  charLimitBehaviour?: string | null;
  match?: string[] | null;
  channels?: ChannelRef[] | null;
  contacts?: string[] | null;
  retryTimeout?: number | null;
  maxRetries?: number | null;
  pathHashSize?: number | null;
  schedule?: string;
}

export interface BrokerConfig {
  name: string;
  enabled: boolean;
  dedup: boolean;
  transport: string;
  host: string;
  port: number;
  packetTopic?: string;
  statusTopic?: string;
  disallowedPacketTypes: string[] | null;
  retainStatus: boolean;
  tlsEnabled: boolean;
  tlsInsecure: boolean;
  authType: string;
  username: string;
  password: string;
  path: string;
  audience: string;
}

export interface MqttConfig {
  node?: string | null;
  enabled?: boolean | null;
  iataCode?: string | null;
  statusInterval?: number | null;
  owner?: string | null;
  email?: string | null;
  brokers?: BrokerConfig[] | null;
}

export interface CompanionConfig {
  name: string;
  // Hex ed25519 seed. Omit/empty on create = the bot generates one. Edits
  // preserve the existing key server-side (inherited by companion name).
  privateKey?: string;
  latitude?: number | null;
  longitude?: number | null;
  advertInterval?: number | null;
  channels?: ChannelRef[] | null;
  triggers?: TriggerConfig[] | null;
  mqtt?: MqttConfig | null;
}

export interface AppConfig {
  logLevel?: string | null;
  connection?: string | null;
  baudRate?: number | null;
  freq?: number | null;
  bw?: number | null;
  sf?: number | null;
  cr?: number | null;
  tx?: number | null;
  listenAddr?: string | null;
  mqtt?: MqttConfig | null;
  companions: CompanionConfig[];
}

// useConfig loads the full bot config and saves it back whole — PUT
// /api/config is a full-document replace that validates, writes the file and
// hot-reloads (companions restart; the modem reconnects only when its
// settings changed). Always mutate a copy of `config` and pass it to save().
export function useConfig() {
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const reloadTimer = useRef<number | null>(null);

  const reload = useCallback(() => {
    setError(null);
    fetch("/api/config")
      .then((r) => {
        if (!r.ok) throw new Error(`config fetch failed (${r.status})`);
        return r.json();
      })
      .then((cfg: AppConfig) => setConfig(cfg))
      .catch(() => setError("Failed to load config"))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    reload();
    return () => {
      if (reloadTimer.current) window.clearTimeout(reloadTimer.current);
    };
  }, [reload]);

  const save = useCallback(
    async (next: AppConfig): Promise<boolean> => {
      setSaving(true);
      try {
        const r = await fetch("/api/config", {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(next),
        });
        if (!r.ok) {
          const err = (await r.json().catch(() => null)) as {
            error?: string;
          } | null;
          throw new Error(err?.error || `save failed (${r.status})`);
        }
        setConfig(next);
        toast.success("Config saved");
        // Re-fetch once the reload settles so defaults/migrations applied
        // server-side show up.
        if (reloadTimer.current) window.clearTimeout(reloadTimer.current);
        reloadTimer.current = window.setTimeout(reload, 1500);
        return true;
      } catch (e) {
        toast.error(e instanceof Error ? e.message : "Failed to save config");
        return false;
      } finally {
        setSaving(false);
      }
    },
    [reload],
  );

  return { config, loading, error, saving, save, reload };
}
