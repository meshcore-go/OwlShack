import { useMemo, useState } from "react";
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
import { configApi, type ConfigCompanion } from "@/lib/configApi";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { PageHeader } from "@/components/PageHeader";
import { InlineConfirm } from "@/components/InlineConfirm";
import { TextField } from "@/components/ConfigFields";
import { truncateMid } from "@/lib/format";

// Runtime roster (/api/companions) — keyed by the live identity, used only to
// enrich the config list with peer/channel counts.
interface RuntimeCompanion {
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
  } = useApiList<ConfigCompanion>(
    "/api/config/companions",
    "Failed to load companions",
  );
  const { items: runtime, reload: reloadRuntime } = useApiList<RuntimeCompanion>(
    "/api/companions",
    "Failed to load companion roster",
  );

  const [editing, setEditing] = useState<ConfigCompanion | "new" | null>(null);
  const [confirming, setConfirming] = useState<number | null>(null);

  const runtimeByPubkey = useMemo(() => {
    const m = new Map<string, RuntimeCompanion>();
    for (const r of runtime ?? []) m.set(r.pubkey, r);
    return m;
  }, [runtime]);

  const total = companions?.length ?? 0;
  const totalPeers =
    companions?.reduce(
      (s, c) => s + (runtimeByPubkey.get(c.pubkey)?.peerCount ?? 0),
      0,
    ) ?? 0;
  const totalChannels =
    companions?.reduce(
      (s, c) => s + (runtimeByPubkey.get(c.pubkey)?.channels?.length ?? 0),
      0,
    ) ?? 0;

  // A write reloads the bot; refresh both the config list and the runtime
  // roster (peer/channel counts) once the companions have restarted.
  const refresh = () => {
    reload();
    window.setTimeout(reloadRuntime, 1200);
  };

  const removeCompanion = async (c: ConfigCompanion) => {
    setConfirming(null);
    try {
      await configApi.deleteCompanion(c.id);
      toast.success(`Companion "${c.name}" removed`);
      refresh();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to remove companion");
    }
  };

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
              <h2 className="font-mono text-sm uppercase tracking-widest">
                Configured nodes
              </h2>
            </div>
          </div>

          {companions.length === 0 ? (
            <div className="px-6 py-16 text-center">
              <MessagesSquare className="size-8 mx-auto mb-3 text-muted-foreground/40" />
              <p className="font-mono text-sm uppercase tracking-widest text-muted-foreground">
                No companions configured
              </p>
              <p className="mt-2 text-xs text-muted-foreground/70">
                Observer-only — add a companion to appear on the mesh.
              </p>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {companions.map((c) => {
                const rt = runtimeByPubkey.get(c.pubkey);
                return (
                  <div
                    key={c.id}
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
                          value={(rt?.peerCount ?? 0).toString()}
                        />
                        <Stat
                          icon={<Hash className="size-3" strokeWidth={1.6} />}
                          label="ch"
                          value={(rt?.channels?.length ?? 0).toString()}
                        />
                      </div>

                      <Crosshair className="size-3.5 text-muted-foreground/40 group-hover:text-primary transition-colors shrink-0" />
                    </Link>

                    <div className="flex items-center gap-1 shrink-0">
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => setEditing(c)}
                        aria-label="Edit companion"
                        className="text-muted-foreground/60 hover:text-foreground"
                      >
                        <Pencil className="size-3.5" />
                      </Button>
                      <InlineConfirm
                        iconOnly
                        confirming={confirming === c.id}
                        onAskRemove={() => setConfirming(c.id)}
                        onCancel={() => setConfirming(null)}
                        onConfirm={() => removeCompanion(c)}
                        ariaLabel="Remove companion"
                      />
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>
      )}

      {editing !== null && (
        <CompanionEditor
          companion={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            refresh();
          }}
        />
      )}
    </div>
  );
}

function CompanionEditor({
  companion,
  onClose,
  onSaved,
}: {
  companion: ConfigCompanion | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(companion?.name ?? "");
  const [privateKey, setPrivateKey] = useState("");
  const [latitude, setLatitude] = useState(
    companion?.latitude != null ? String(companion.latitude) : "",
  );
  const [longitude, setLongitude] = useState(
    companion?.longitude != null ? String(companion.longitude) : "",
  );
  const [advertInterval, setAdvertInterval] = useState(
    companion?.advertInterval != null ? String(companion.advertInterval) : "",
  );
  const [saving, setSaving] = useState(false);

  const renamed = companion != null && name.trim() !== companion.name;

  const submit = async () => {
    setSaving(true);
    try {
      await configApi.saveCompanion(
        {
          name: name.trim(),
          // Only send a key on create (blank = generated). Edits keep the
          // stored identity (omitted → inherited server-side).
          ...(companion ? {} : { privateKey: privateKey.trim() || undefined }),
          latitude: latitude === "" ? null : parseFloat(latitude) || 0,
          longitude: longitude === "" ? null : parseFloat(longitude) || 0,
          advertInterval:
            advertInterval === "" ? null : parseInt(advertInterval, 10) || 0,
        },
        companion?.id,
      );
      toast.success(companion ? "Companion saved" : `Companion "${name.trim()}" added`);
      onSaved();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to save companion");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="rounded-none sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="font-mono text-sm uppercase tracking-widest">
            {companion ? `Edit companion — ${companion.name}` : "Add companion"}
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
                ? "renaming is safe — history, contacts and conversations follow the companion (keyed by its id)"
                : "shown on the mesh and in adverts"
            }
          />
          {!companion && (
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
