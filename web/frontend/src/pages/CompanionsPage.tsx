import { useState } from "react";
import { Link } from "react-router-dom";
import {
  Crosshair,
  Hash,
  Loader2,
  MessagesSquare,
  Pencil,
  Plus,
  Save,
  Users,
} from "lucide-react";
import { toast } from "sonner";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useApiList } from "@/hooks/useApiList";
import {
  useConfig,
  type AppConfig,
  type CompanionConfig,
} from "@/hooks/useConfig";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { PageHeader } from "@/components/PageHeader";
import { InlineConfirm } from "@/components/InlineConfirm";
import { TextField } from "@/components/ConfigFields";
import { truncateMid } from "@/lib/format";

interface Companion {
  name: string;
  pubkey: string;
  peerCount: number;
  channels?: { name: string }[];
}

export function CompanionsPage() {
  const {
    items: companions,
    loading,
    error,
    reload,
  } = useApiList<Companion>("/api/companions", "Failed to load companions");
  const {
    config,
    saving,
    save,
    reload: reloadConfig,
  } = useConfig();

  const [editing, setEditing] = useState<number | "new" | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);

  const total = companions?.length ?? 0;
  const totalChannels =
    companions?.reduce((s, c) => s + (c.channels?.length || 0), 0) ?? 0;
  const totalPeers =
    companions?.reduce((s, c) => s + (c.peerCount || 0), 0) ?? 0;

  // The save triggers a bot reload; re-fetch the runtime roster once the
  // companions have restarted.
  const saveAndRefresh = async (next: AppConfig): Promise<boolean> => {
    const ok = await save(next);
    if (ok) window.setTimeout(reload, 1800);
    return ok;
  };

  const removeCompanion = async (name: string) => {
    if (!config) return;
    setConfirming(null);
    if (config.companions.length <= 1) {
      toast.error("At least one companion must remain configured");
      return;
    }
    const next = structuredClone(config) as AppConfig;
    next.companions = next.companions.filter((c) => c.name !== name);
    if (next.mqtt?.node === name) next.mqtt.node = next.companions[0]?.name;
    await saveAndRefresh(next);
  };

  const configIdx = (name: string) =>
    config?.companions.findIndex((c) => c.name === name) ?? -1;

  return (
    <div className="space-y-4">
      <PageHeader
        title="Companions"
        meta={
          companions && (
            <span className="font-mono text-sm text-muted-foreground tabular-nums">
              {total} configured · {totalPeers} peers · {totalChannels} ch
            </span>
          )
        }
        actions={
          <Button
            size="sm"
            onClick={() => setEditing("new")}
            disabled={!config}
            className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
          >
            <Plus className="size-3.5" />
            add companion
          </Button>
        }
      />

      {loading && <CompanionsSkeleton />}

      {error && <LoadErrorAlert message={error} onRetry={reload} />}

      {!loading && !error && companions && (
        <section className="panel overflow-hidden">
          <div className="flex items-center justify-between px-4 py-3 border-b border-border">
            <div className="space-y-0.5">
              <span className="label-overline block">Roster</span>
              <h2 className="font-mono text-sm uppercase tracking-[0.1em]">
                Configured nodes
              </h2>
            </div>
          </div>

          {companions.length === 0 ? (
            <div className="px-6 py-16 text-center">
              <MessagesSquare className="size-8 mx-auto mb-3 text-muted-foreground/40" />
              <p className="font-mono text-sm uppercase tracking-[0.1em] text-muted-foreground">
                No companions configured
              </p>
              <p className="mt-2 text-xs text-muted-foreground/70">
                Add a companion to begin.
              </p>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {companions.map((c) => (
                <div
                  key={c.name}
                  className="group flex items-center gap-4 px-4 py-4 hover:bg-muted/40 transition-colors"
                >
                  <Link
                    to={`/companions/${encodeURIComponent(c.name)}`}
                    className="flex items-center gap-4 min-w-0 flex-1"
                  >
                    <div className="size-10 grid place-items-center rounded-sm border border-primary/30 bg-primary/10 text-primary shrink-0">
                      <MessagesSquare className="size-4" strokeWidth={1.6} />
                    </div>

                    <div className="min-w-0 flex-1 space-y-1">
                      <div className="flex items-baseline gap-2">
                        <h3 className="font-mono text-sm font-semibold uppercase tracking-[0.08em] truncate">
                          {c.name}
                        </h3>
                      </div>
                      <code className="font-mono text-xs text-muted-foreground block truncate">
                        {c.pubkey ? truncateMid(c.pubkey, 8, 6) : "—"}
                      </code>
                    </div>

                    <div className="hidden sm:flex items-center gap-5 shrink-0">
                      <Stat
                        icon={<Users className="size-3" strokeWidth={1.6} />}
                        label="peers"
                        value={c.peerCount.toString()}
                      />
                      <Stat
                        icon={<Hash className="size-3" strokeWidth={1.6} />}
                        label="ch"
                        value={(c.channels?.length || 0).toString()}
                      />
                    </div>

                    <Crosshair className="size-3.5 text-muted-foreground/40 group-hover:text-primary transition-colors shrink-0" />
                  </Link>

                  <div className="flex items-center gap-1 shrink-0">
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      onClick={() => {
                        const idx = configIdx(c.name);
                        if (idx >= 0) setEditing(idx);
                        else toast.error("Companion not found in config");
                      }}
                      disabled={!config}
                      aria-label="Edit companion"
                      className="text-muted-foreground/60 hover:text-foreground"
                    >
                      <Pencil className="size-3.5" />
                    </Button>
                    <InlineConfirm
                      iconOnly
                      confirming={confirming === c.name}
                      onAskRemove={() => setConfirming(c.name)}
                      onCancel={() => setConfirming(null)}
                      onConfirm={() => removeCompanion(c.name)}
                      ariaLabel="Remove companion"
                    />
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      )}

      {editing !== null && config && (
        <CompanionEditor
          config={config}
          idx={editing === "new" ? null : editing}
          saving={saving}
          onClose={() => setEditing(null)}
          onSave={async (next) => {
            if (await saveAndRefresh(next)) {
              setEditing(null);
              reloadConfig();
            }
          }}
        />
      )}
    </div>
  );
}

function CompanionEditor({
  config,
  idx,
  saving,
  onClose,
  onSave,
}: {
  config: AppConfig;
  idx: number | null;
  saving: boolean;
  onClose: () => void;
  onSave: (next: AppConfig) => Promise<void>;
}) {
  const existing = idx != null ? config.companions[idx] : null;
  const [name, setName] = useState(existing?.name ?? "");
  const [privateKey, setPrivateKey] = useState("");
  const [latitude, setLatitude] = useState(
    existing?.latitude != null ? String(existing.latitude) : "",
  );
  const [longitude, setLongitude] = useState(
    existing?.longitude != null ? String(existing.longitude) : "",
  );
  const [advertInterval, setAdvertInterval] = useState(
    existing?.advertInterval != null ? String(existing.advertInterval) : "",
  );

  const renamed = existing != null && name.trim() !== existing.name;

  const submit = async () => {
    const next = structuredClone(config) as AppConfig;
    const trimmed = name.trim();
    const built: CompanionConfig = {
      ...(existing ?? {}),
      name: trimmed,
      ...(existing ? {} : { privateKey: privateKey.trim() || undefined }),
      latitude: latitude === "" ? null : parseFloat(latitude) || 0,
      longitude: longitude === "" ? null : parseFloat(longitude) || 0,
      advertInterval:
        advertInterval === "" ? null : parseInt(advertInterval, 10) || 0,
    };
    if (idx != null) {
      const oldName = next.companions[idx].name;
      next.companions[idx] = built;
      if (next.mqtt?.node === oldName) next.mqtt.node = built.name;
    } else {
      next.companions.push(built);
    }
    await onSave(next);
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="rounded-none sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="font-mono text-sm uppercase tracking-[0.1em]">
            {existing ? `Edit companion — ${existing.name}` : "Add companion"}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <TextField
            label="Name"
            value={name}
            onChange={setName}
            placeholder="OwlShack"
            hint={
              renamed
                ? "⚠ message history, contacts and conversations are keyed by name — renaming orphans them"
                : "shown on the mesh and in adverts"
            }
          />
          {!existing && (
            <TextField
              label="Private key"
              type="password"
              value={privateKey}
              onChange={setPrivateKey}
              placeholder="blank = generate a new identity"
              hint={
                <>
                  the node identity (64-hex ed25519 seed) — want a specific
                  pubkey prefix? generate one with{" "}
                  <a
                    href="https://gessaman.com/mc-keygen/"
                    target="_blank"
                    rel="noreferrer"
                    className="text-primary underline underline-offset-2 hover:text-primary/80"
                  >
                    mc-keygen
                  </a>
                </>
              }
            />
          )}
          <div className="grid grid-cols-2 gap-4">
            <TextField
              label="Latitude"
              value={latitude}
              onChange={setLatitude}
              placeholder="blank = no position"
              hint="advertised position (optional)"
            />
            <TextField
              label="Longitude"
              value={longitude}
              onChange={setLongitude}
              placeholder="blank = no position"
            />
          </div>
          <TextField
            label="Advert interval (s)"
            value={advertInterval}
            onChange={setAdvertInterval}
            placeholder="blank = 86400 (daily)"
            hint="0 = never advertise"
          />

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
              disabled={saving || name.trim() === ""}
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

function Stat({
  icon,
  label,
  value,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
}) {
  return (
    <div className="flex flex-col items-end gap-0.5">
      <span className="font-mono text-sm font-semibold tabular-nums leading-none">
        {value}
      </span>
      <span className="inline-flex items-center gap-1 font-mono text-[9px] uppercase tracking-[0.14em] text-muted-foreground/70">
        {icon}
        {label}
      </span>
    </div>
  );
}

function CompanionsSkeleton() {
  return (
    <section className="panel overflow-hidden">
      <div className="px-4 py-3 border-b border-border">
        <Skeleton className="h-4 w-32 rounded-none" />
      </div>
      <div className="divide-y divide-border">
        {Array.from({ length: 2 }).map((_, i) => (
          <div key={i} className="flex items-center gap-4 px-4 py-4">
            <Skeleton className="size-10 rounded-sm shrink-0" />
            <div className="flex-1 space-y-2">
              <Skeleton className="h-4 w-40 rounded-none" />
              <Skeleton className="h-3 w-28 rounded-none" />
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}
