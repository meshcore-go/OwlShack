import { useEffect, useState } from "react";
import {
  CircleDashed,
  Loader2,
  Pencil,
  Plus,
  Rss,
  Save,
} from "lucide-react";
import { PageHeader } from "@/components/PageHeader";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { SectionTitle } from "@/components/SectionTitle";
import { InlineConfirm } from "@/components/InlineConfirm";
import { SelectField, SwitchRow, TextField } from "@/components/ConfigFields";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  useConfig,
  type AppConfig,
  type BrokerConfig,
} from "@/hooks/useConfig";

const EMPTY_BROKER: BrokerConfig = {
  name: "",
  enabled: true,
  dedup: true,
  transport: "tcp",
  host: "",
  port: 8883,
  packetTopic: "",
  statusTopic: "",
  disallowedPacketTypes: null,
  retainStatus: true,
  tlsEnabled: true,
  tlsInsecure: false,
  authType: "token",
  username: "",
  password: "",
  path: "/",
  audience: "",
};

// Mirrors meshcoretomqtt's broker presets (github.com/Cisien/meshcoretomqtt
// presets/): websockets on 443, verified TLS, signed-token auth with the
// audience pinned to the broker host, explicit meshcoretomqtt topic paths.
function tokenBrokerPreset(name: string, host: string): BrokerConfig {
  return {
    ...EMPTY_BROKER,
    name,
    host,
    port: 443,
    transport: "websockets",
    audience: host,
    packetTopic: "meshcore/{IATA}/{PUBLIC_KEY}/packets",
    statusTopic: "meshcore/{IATA}/{PUBLIC_KEY}/status",
  };
}

const BROKER_PRESETS: { value: string; label: string; broker: BrokerConfig }[] =
  [
    {
      value: "letsmesh-us",
      label: "LetsMesh US",
      broker: tokenBrokerPreset("letsmesh-us", "mqtt-us-v1.letsmesh.net"),
    },
    {
      value: "letsmesh-eu",
      label: "LetsMesh EU",
      broker: tokenBrokerPreset("letsmesh-eu", "mqtt-eu-v1.letsmesh.net"),
    },
    {
      value: "waev-a",
      label: "Waev App (A)",
      broker: tokenBrokerPreset("waev-a", "mqtt-a.waev.app"),
    },
    {
      value: "waev-b",
      label: "Waev App (B)",
      broker: tokenBrokerPreset("waev-b", "mqtt-b.waev.app"),
    },
    {
      value: "meshmapper",
      label: "MeshMapper",
      broker: tokenBrokerPreset("meshmapper", "mqtt.meshmapper.net"),
    },
  ];

export function MqttPage() {
  const { config, loading, error, saving, save, reload } = useConfig();

  const [enabled, setEnabled] = useState(true);
  const [node, setNode] = useState("");
  const [iataCode, setIataCode] = useState("");
  const [owner, setOwner] = useState("");
  const [email, setEmail] = useState("");
  const [statusInterval, setStatusInterval] = useState("300");
  const [brokers, setBrokers] = useState<BrokerConfig[]>([]);
  const [editingBroker, setEditingBroker] = useState<number | "new" | null>(
    null,
  );
  const [confirming, setConfirming] = useState<number | null>(null);

  useEffect(() => {
    if (!config) return;
    const m = config.mqtt;
    setEnabled(m?.enabled !== false);
    setNode(m?.node ?? config.companions[0]?.name ?? "");
    setIataCode(m?.iataCode ?? "");
    setOwner(m?.owner ?? "");
    setEmail(m?.email ?? "");
    setStatusInterval(String(m?.statusInterval ?? 300));
    setBrokers(m?.brokers ?? []);
  }, [config]);

  const buildNext = (nextBrokers: BrokerConfig[]): AppConfig | null => {
    if (!config) return null;
    const next = structuredClone(config) as AppConfig;
    if (nextBrokers.length === 0) {
      next.mqtt = null;
      return next;
    }
    next.mqtt = {
      ...(config.mqtt ?? {}),
      enabled,
      node: node || null,
      iataCode: iataCode || null,
      owner: owner || null,
      email: email || null,
      statusInterval: parseInt(statusInterval, 10) || 300,
      brokers: nextBrokers,
    };
    return next;
  };

  const onSave = async (nextBrokers?: BrokerConfig[]) => {
    const next = buildNext(nextBrokers ?? brokers);
    if (next) await save(next);
  };

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow="system"
        title="MQTT"
        meta={
          config && (
            <span className="font-mono text-sm text-muted-foreground tabular-nums">
              {brokers.length} broker{brokers.length === 1 ? "" : "s"}
            </span>
          )
        }
        actions={
          <Button
            size="sm"
            onClick={() => onSave()}
            disabled={saving || loading || !config}
            className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
          >
            {saving ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <Save className="size-3.5" />
            )}
            save
          </Button>
        }
      />

      {error && <LoadErrorAlert message={error} onRetry={reload} />}

      {loading ? (
        <Skeleton className="h-64 w-full rounded-none" />
      ) : config ? (
        <>
          <section className="panel">
            <SectionTitle eyebrow="observer" title="Feed" />
            <div className="p-4 space-y-4">
              <SwitchRow
                label="Enabled"
                hint="publish packets & status to the configured brokers"
                checked={enabled}
                onChange={setEnabled}
              />
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <SelectField
                  label="Node"
                  value={node}
                  options={config.companions.map((c) => ({
                    value: c.name,
                    label: c.name,
                  }))}
                  onChange={setNode}
                  hint="one node feeds MQTT — its identity signs the feed"
                />
                <TextField
                  label="IATA code"
                  value={iataCode}
                  onChange={setIataCode}
                  placeholder="AKL"
                  hint="region tag in the topic path"
                />
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
                <TextField label="Owner" value={owner} onChange={setOwner} />
                <TextField
                  label="Email"
                  value={email}
                  onChange={setEmail}
                  hint="included in the auth token (LetsMesh)"
                />
                <TextField
                  label="Status interval (s)"
                  value={statusInterval}
                  onChange={setStatusInterval}
                  placeholder="300"
                />
              </div>
            </div>
          </section>

          <section className="panel overflow-hidden">
            <SectionTitle
              eyebrow="endpoints"
              title="Brokers"
              trailing={
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setEditingBroker("new")}
                  className="rounded-none font-mono text-[10px] uppercase tracking-[0.12em]"
                >
                  <Plus className="size-3" />
                  add broker
                </Button>
              }
            />
            {brokers.length === 0 ? (
              <div className="px-6 py-16 text-center space-y-3">
                <CircleDashed className="size-8 mx-auto text-muted-foreground/40" />
                <p className="font-mono text-xs uppercase tracking-[0.12em] text-muted-foreground">
                  No brokers configured
                </p>
              </div>
            ) : (
              <div className="divide-y divide-border">
                {brokers.map((b, i) => (
                  <div key={i} className="flex items-center gap-4 px-4 py-3">
                    <div
                      className={
                        b.enabled
                          ? "size-9 grid place-items-center rounded-sm border border-primary/30 bg-primary/10 text-primary shrink-0"
                          : "size-9 grid place-items-center rounded-sm border border-border bg-muted/40 text-muted-foreground/50 shrink-0"
                      }
                    >
                      <Rss className="size-4" strokeWidth={1.6} />
                    </div>
                    <div className="min-w-0 flex-1 space-y-0.5">
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-sm font-semibold truncate">
                          {b.name}
                        </span>
                        {!b.enabled && (
                          <span className="font-mono text-[10px] uppercase tracking-[0.1em] text-muted-foreground/60">
                            disabled
                          </span>
                        )}
                      </div>
                      <code className="font-mono text-xs text-muted-foreground block truncate">
                        {b.transport === "websockets" ? "ws" : "tcp"}
                        {b.tlsEnabled ? "+tls" : ""}://{b.host}:{b.port} ·{" "}
                        {b.packetTopic || "meshcore/{iata}/{pubkey}/…"} · auth{" "}
                        {b.authType || "none"}
                      </code>
                    </div>
                    <div className="flex items-center gap-1 shrink-0">
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => setEditingBroker(i)}
                        aria-label="Edit broker"
                        className="text-muted-foreground/60 hover:text-foreground"
                      >
                        <Pencil className="size-3.5" />
                      </Button>
                      <InlineConfirm
                        iconOnly
                        confirming={confirming === i}
                        onAskRemove={() => setConfirming(i)}
                        onCancel={() => setConfirming(null)}
                        onConfirm={() => {
                          setConfirming(null);
                          const nextBrokers = brokers.filter(
                            (_, j) => j !== i,
                          );
                          setBrokers(nextBrokers);
                          void onSave(nextBrokers);
                        }}
                        ariaLabel="Remove broker"
                      />
                    </div>
                  </div>
                ))}
              </div>
            )}
          </section>
        </>
      ) : null}

      {editingBroker !== null && (
        <BrokerEditor
          broker={
            editingBroker === "new" ? EMPTY_BROKER : brokers[editingBroker]
          }
          isNew={editingBroker === "new"}
          saving={saving}
          onClose={() => setEditingBroker(null)}
          onSave={async (b) => {
            const nextBrokers =
              editingBroker === "new"
                ? [...brokers, b]
                : brokers.map((x, i) => (i === editingBroker ? b : x));
            setBrokers(nextBrokers);
            await onSave(nextBrokers);
            setEditingBroker(null);
          }}
        />
      )}
    </div>
  );
}

function BrokerEditor({
  broker,
  isNew,
  saving,
  onClose,
  onSave,
}: {
  broker: BrokerConfig;
  isNew: boolean;
  saving: boolean;
  onClose: () => void;
  onSave: (b: BrokerConfig) => Promise<void>;
}) {
  const [b, setB] = useState<BrokerConfig>({ ...broker });
  const set = <K extends keyof BrokerConfig>(k: K, v: BrokerConfig[K]) =>
    setB((prev) => ({ ...prev, [k]: v }));

  const [disallowed, setDisallowed] = useState(
    (broker.disallowedPacketTypes ?? []).join(", "),
  );
  const [preset, setPreset] = useState("custom");

  const applyPreset = (value: string) => {
    setPreset(value);
    const p = BROKER_PRESETS.find((x) => x.value === value);
    if (!p) return;
    setB({ ...p.broker });
    setDisallowed((p.broker.disallowedPacketTypes ?? []).join(", "));
  };

  const valid = b.name.trim() !== "" && b.host.trim() !== "" && b.port > 0;

  const submit = async () => {
    const types = disallowed
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    await onSave({
      ...b,
      disallowedPacketTypes: types.length > 0 ? types : null,
    });
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="rounded-none sm:max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="font-mono text-sm uppercase tracking-[0.1em]">
            {broker.name ? `Edit broker — ${broker.name}` : "Add broker"}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          {isNew && (
            <SelectField
              label="Preset"
              value={preset}
              options={[
                { value: "custom", label: "Custom" },
                ...BROKER_PRESETS.map((p) => ({
                  value: p.value,
                  label: p.label,
                })),
              ]}
              onChange={applyPreset}
              hint="prefills the form — fields stay editable"
            />
          )}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <TextField
              label="Name"
              value={b.name}
              onChange={(v) => set("name", v)}
              placeholder="LetsMesh"
            />
            <SelectField
              label="Transport"
              value={b.transport || "tcp"}
              options={[
                { value: "tcp", label: "TCP" },
                { value: "websockets", label: "WebSockets" },
              ]}
              onChange={(v) => set("transport", v)}
            />
          </div>
          <div className="grid grid-cols-[1fr_8rem] gap-4">
            <TextField
              label="Host"
              value={b.host}
              onChange={(v) => set("host", v)}
              placeholder="mqtt.letsmesh.org"
            />
            <TextField
              label="Port"
              value={String(b.port || "")}
              onChange={(v) => set("port", parseInt(v, 10) || 0)}
              placeholder="8883"
            />
          </div>
          {b.transport === "websockets" && (
            <TextField
              label="WebSocket path"
              value={b.path}
              onChange={(v) => set("path", v)}
              placeholder="/"
            />
          )}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <TextField
              label="Packet topic"
              value={b.packetTopic ?? ""}
              onChange={(v) => set("packetTopic", v)}
              placeholder="meshcore/{iata}/{pubkey}/packets"
              hint="placeholders: {iata} {pubkey} {name} — {IATA}/{PUBLIC_KEY} also work"
            />
            <TextField
              label="Status topic"
              value={b.statusTopic ?? ""}
              onChange={(v) => set("statusTopic", v)}
              placeholder="meshcore/{iata}/{pubkey}/status"
              hint="blank = default structure"
            />
          </div>
          <SelectField
            label="Auth"
            value={b.authType || "none"}
            options={[
              { value: "token", label: "Token (signed JWT)" },
              { value: "basic", label: "Basic (user/pass)" },
              { value: "none", label: "None" },
            ]}
            onChange={(v) => set("authType", v)}
          />
          {b.authType === "basic" && (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <TextField
                label="Username"
                value={b.username}
                onChange={(v) => set("username", v)}
              />
              <TextField
                label="Password"
                type="password"
                value={b.password}
                onChange={(v) => set("password", v)}
              />
            </div>
          )}
          {b.authType === "token" && (
            <TextField
              label="Audience"
              value={b.audience}
              onChange={(v) => set("audience", v)}
              hint="JWT aud claim — defaults to the broker host"
            />
          )}
          <TextField
            label="Disallowed packet types"
            value={disallowed}
            onChange={setDisallowed}
            placeholder="ADVERT, TRACE"
            hint="comma-separated; these are never published"
          />
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            <SwitchRow
              label="Enabled"
              checked={b.enabled}
              onChange={(v) => set("enabled", v)}
            />
            <SwitchRow
              label="Dedup"
              hint="skip already-seen packets"
              checked={b.dedup}
              onChange={(v) => set("dedup", v)}
            />
            <SwitchRow
              label="TLS"
              checked={b.tlsEnabled}
              onChange={(v) => set("tlsEnabled", v)}
            />
            <SwitchRow
              label="TLS insecure"
              hint="skip certificate verification"
              checked={b.tlsInsecure}
              onChange={(v) => set("tlsInsecure", v)}
            />
            <SwitchRow
              label="Retain status"
              hint="broker keeps the last status message"
              checked={b.retainStatus}
              onChange={(v) => set("retainStatus", v)}
            />
          </div>

          <div className="flex justify-end gap-2 pt-1">
            <Button
              variant="ghost"
              size="sm"
              onClick={onClose}
              className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
            >
              cancel
            </Button>
            <Button
              size="sm"
              onClick={submit}
              disabled={saving || !valid}
              className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
            >
              {saving ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : (
                <Save className="size-3.5" />
              )}
              save
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
