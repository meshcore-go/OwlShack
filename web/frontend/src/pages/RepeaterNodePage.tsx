import { useCallback, useEffect, useState } from "react";
import {
  AlertTriangle,
  CircleDashed,
  Loader2,
  Plus,
  RefreshCw,
  Save,
  Send,
} from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/PageHeader";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { SectionTitle } from "@/components/SectionTitle";
import { Field, TextField, SelectField, SwitchRow } from "@/components/ConfigFields";
import { Switch } from "@/components/ui/switch";
import { InlineConfirm } from "@/components/InlineConfirm";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useApiObject } from "@/hooks/useApiObject";
import {
  configApi,
  type ConfigRepeater,
  type RepeaterNodeNeighbor,
  type RepeaterNodeStats,
  type RepeaterAclEntry,
} from "@/lib/configApi";
import { formatSecsAgo, formatUptime } from "@/lib/format";

const LOOP_LEVELS = ["off", "minimal", "moderate", "strict"];
// ACL permission levels, indexed by the firmware role value.
const PERM_LABELS = ["guest", "read-only", "read-write", "admin"];

// StatCell mirrors DashboardPage's divided-grid tile.
function StatCell({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-card p-3">
      <div className="label-overline text-muted-foreground/70">{label}</div>
      <div className="mt-1 font-mono text-lg tabular-nums">{value}</div>
    </div>
  );
}

export function RepeaterNodePage() {
  const { item: rep, loading, error, reload } = useApiObject<ConfigRepeater>(
    "/api/config/repeater",
    "Failed to load repeater",
  );

  // One save runs at a time; only its section's button spins. Region item-ops
  // and page actions have their own busy state.
  const [saving, setSaving] = useState<"create" | "node" | "relay" | "admin" | null>(null);
  const [busy, setBusy] = useState<"advert" | "delete" | null>(null);
  const [regionBusy, setRegionBusy] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [confirmRegion, setConfirmRegion] = useState<string | null>(null);

  // Config form state.
  const [name, setName] = useState("");
  // Only used on create (import an existing identity); blank = generate.
  const [privateKey, setPrivateKey] = useState("");
  const [lat, setLat] = useState("");
  const [lon, setLon] = useState("");
  const [advertInterval, setAdvertInterval] = useState("");
  const [floodAdvertInterval, setFloodAdvertInterval] = useState("");
  const [floodMax, setFloodMax] = useState("");
  const [floodMaxUnscoped, setFloodMaxUnscoped] = useState("");
  const [floodMaxAdvert, setFloodMaxAdvert] = useState("");
  const [loopDetect, setLoopDetect] = useState("off");
  const [defaultRegion, setDefaultRegion] = useState("");
  const [pathHashMode, setPathHashMode] = useState("0");
  const [disableFwd, setDisableFwd] = useState(false);
  const [newRegion, setNewRegion] = useState("");
  const [ownerInfo, setOwnerInfo] = useState("");
  // Passwords are write-only (never read back). `dirty` distinguishes an
  // untouched empty field ("keep") from a deliberately-emptied one ("clear"),
  // so a blank value is only sent when the user actually changed it.
  const [adminPw, setAdminPw] = useState({ value: "", dirty: false });
  const [guestPw, setGuestPw] = useState({ value: "", dirty: false });

  // Live relay stats (local counters, polled while running).
  const [stats, setStats] = useState<RepeaterNodeStats | null>(null);
  const [neighbors, setNeighbors] = useState<RepeaterNodeNeighbor[]>([]);
  const [acl, setAcl] = useState<RepeaterAclEntry[]>([]);
  const [revoking, setRevoking] = useState<string | null>(null);
  const [confirmRevoke, setConfirmRevoke] = useState<string | null>(null);

  // Reset every section's fields from the loaded config. Runs on load and after
  // any section/region save (which reload the config), so an edit left unsaved
  // in another section is discarded — save each section before moving on.
  // Regions render straight from rep (server-owned; edited via item-ops), so
  // they aren't mirrored into local state here.
  useEffect(() => {
    if (!rep || !rep.configured) return;
    setName(rep.name);
    setLat(rep.latitude != null ? String(rep.latitude) : "");
    setLon(rep.longitude != null ? String(rep.longitude) : "");
    setAdvertInterval(rep.advertInterval != null ? String(rep.advertInterval) : "");
    setFloodAdvertInterval(rep.floodAdvertInterval != null ? String(rep.floodAdvertInterval) : "");
    setFloodMax(rep.floodMax != null ? String(rep.floodMax) : "");
    setFloodMaxUnscoped(rep.floodMaxUnscoped != null ? String(rep.floodMaxUnscoped) : "");
    setFloodMaxAdvert(rep.floodMaxAdvert != null ? String(rep.floodMaxAdvert) : "");
    setLoopDetect(rep.loopDetect ?? "off");
    setDefaultRegion(rep.defaultRegion ?? "");
    setPathHashMode(rep.pathHashMode != null ? String(rep.pathHashMode) : "0");
    setDisableFwd(rep.disableFwd ?? false);
    setNewRegion("");
    setOwnerInfo(rep.ownerInfo);
    setPrivateKey(""); // key field is blank = keep; only a typed value rotates
    setAdminPw({ value: "", dirty: false });
    setGuestPw({ value: "", dirty: false });
  }, [rep]);

  const refreshStatus = useCallback(() => {
    fetch("/api/repeater/status")
      .then((r) => (r.ok ? r.json() : null))
      .then((s: RepeaterNodeStats | null) => setStats(s))
      .catch(() => setStats(null));
    fetch("/api/repeater/neighbors")
      .then((r) => (r.ok ? r.json() : []))
      .then((n: RepeaterNodeNeighbor[]) => setNeighbors(n ?? []))
      .catch(() => setNeighbors([]));
    fetch("/api/repeater/acl")
      .then((r) => (r.ok ? r.json() : []))
      .then((a: RepeaterAclEntry[]) => setAcl(a ?? []))
      .catch(() => setAcl([]));
  }, []);

  // Poll local counters every 5s while the node is running (cheap — no radio).
  const running = rep?.running ?? false;
  useEffect(() => {
    if (!running) {
      setStats(null);
      setNeighbors([]);
      setAcl([]);
      return;
    }
    refreshStatus();
    const id = setInterval(refreshStatus, 5000);
    return () => clearInterval(id);
  }, [running, refreshStatus]);

  const num = (s: string): number | null => {
    const t = s.trim();
    if (t === "") return null;
    const n = Number(t);
    return Number.isFinite(n) ? n : null;
  };

  // section runs a single-section save: spins only that section, reloads the
  // config on success so derived read-only fields (pubkey, *Set) refresh.
  const section = async (
    which: "create" | "node" | "relay" | "admin",
    fn: () => Promise<unknown>,
    ok: string,
  ) => {
    setSaving(which);
    try {
      await fn();
      toast.success(ok);
      reload();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(null);
    }
  };

  const onCreate = () =>
    section(
      "create",
      () =>
        configApi.createRepeater({
          name: name.trim(),
          ...(privateKey.trim() !== "" ? { privateKey: privateKey.trim() } : {}),
        }),
      "Repeater created",
    );

  const saveNode = () =>
    section(
      "node",
      () =>
        configApi.updateRepeaterNode({
          name: name.trim(),
          // A typed key ROTATES the identity; blank keeps it.
          ...(privateKey.trim() !== "" ? { privateKey: privateKey.trim() } : {}),
          latitude: num(lat),
          longitude: num(lon),
        }),
      "Node saved",
    );

  const saveRelay = () =>
    section(
      "relay",
      () =>
        configApi.updateRepeaterRelay({
          disableFwd,
          floodMax: num(floodMax),
          floodMaxUnscoped: num(floodMaxUnscoped),
          floodMaxAdvert: num(floodMaxAdvert),
          loopDetect,
          pathHashMode: num(pathHashMode),
          defaultRegion,
          advertInterval: num(advertInterval),
          floodAdvertInterval: num(floodAdvertInterval),
        }),
      "Relay policy saved",
    );

  const saveAdmin = () =>
    section(
      "admin",
      () =>
        configApi.updateRepeaterAdmin({
          ownerInfo,
          // Only send a password the user changed: dirty-empty clears (""),
          // untouched is omitted (server keeps the stored secret).
          ...(adminPw.dirty ? { adminPassword: adminPw.value } : {}),
          ...(guestPw.dirty ? { guestPassword: guestPw.value } : {}),
        }),
      "Owner & access saved",
    );

  const onAdvert = async (flood: boolean) => {
    setBusy("advert");
    try {
      await configApi.repeaterAdvert(flood);
      toast.success(flood ? "Flood advert sent" : "Zero-hop advert sent");
      setTimeout(refreshStatus, 500);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to advertise");
    } finally {
      setBusy(null);
    }
  };

  const onDelete = async () => {
    setBusy("delete");
    try {
      await configApi.deleteRepeater();
      toast.success("Repeater removed");
      setConfirmDelete(false);
      reload();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to remove repeater");
    } finally {
      setBusy(null);
    }
  };

  // Region item-ops apply immediately (each reloads the node) — no bulk array.
  const regionOp = async (rn: string, fn: () => Promise<unknown>, ok?: string) => {
    setRegionBusy(rn);
    try {
      await fn();
      if (ok) toast.success(ok);
      reload();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Region update failed");
    } finally {
      setRegionBusy(null);
    }
  };

  const regions = rep?.regions ?? [];
  const addRegion = () => {
    const rn = newRegion.trim();
    if (rn === "" || regions.some((r) => r.name === rn)) return;
    regionOp(rn, () => configApi.addRepeaterRegion(rn, false), "Region added");
  };
  const removeRegion = (rn: string) =>
    regionOp(rn, () => configApi.removeRepeaterRegion(rn), "Region removed");
  const toggleDenyFlood = (rn: string, cur: boolean) =>
    regionOp(rn, () => configApi.setRepeaterRegionFlood(rn, !cur));

  const revokeAcl = async (pubkey: string) => {
    setRevoking(pubkey);
    try {
      await configApi.revokeRepeaterAcl(pubkey);
      toast.success("Access revoked");
      setConfirmRevoke(null);
      refreshStatus();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to revoke access");
    } finally {
      setRevoking(null);
    }
  };

  const configured = rep?.configured ?? false;

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow="system"
        title="Repeater"
        actions={
          configured ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!running || busy !== null}
                  className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
                >
                  {busy === "advert" ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : (
                    <Send className="size-3.5" />
                  )}
                  advertise
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="rounded-sm">
                <DropdownMenuItem
                  onClick={() => onAdvert(true)}
                  className="font-mono text-xs uppercase tracking-[0.08em]"
                >
                  Flood advert
                </DropdownMenuItem>
                <DropdownMenuItem
                  onClick={() => onAdvert(false)}
                  className="font-mono text-xs uppercase tracking-[0.08em]"
                >
                  Zero-hop / direct
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : undefined
        }
      />

      {error && <LoadErrorAlert message={error} onRetry={reload} />}

      {loading ? (
        <Skeleton className="h-64 w-full rounded-none" />
      ) : !configured ? (
        <EmptyState
          name={name}
          setName={setName}
          privateKey={privateKey}
          setPrivateKey={setPrivateKey}
          saving={saving === "create"}
          onCreate={onCreate}
        />
      ) : (
        <>
          {!running && (
            <div className="flex items-start gap-2 border border-warning/40 bg-warning/5 px-3 py-2 font-mono text-[11px] text-warning">
              <AlertTriangle className="size-3.5 shrink-0 mt-0.5" />
              <span>
                Repeater is configured but not running. Check the logs — a bad
                identity or radio error prevents startup.
              </span>
            </div>
          )}

          {/* Live relay stats */}
          <section className="panel">
            <SectionTitle
              eyebrow="live"
              title="Relay activity"
              trailing={
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={refreshStatus}
                  disabled={!running}
                  className="font-mono text-[10px] uppercase tracking-widest"
                >
                  <RefreshCw className="size-3" /> refresh
                </Button>
              }
            />
            {running && stats ? (
              <div className="grid grid-cols-2 gap-px bg-border border-t border-border sm:grid-cols-3 lg:grid-cols-5">
                <StatCell label="uptime" value={formatUptime(stats.uptimeSecs)} />
                <StatCell label="received" value={String(stats.packetsReceived)} />
                <StatCell label="relayed" value={String(stats.packetsForwarded)} />
                <StatCell label="tx queue" value={String(stats.txQueueLen)} />
                <StatCell label="neighbours" value={String(stats.neighbors)} />
              </div>
            ) : (
              <div className="flex flex-col items-center gap-2 py-10 text-muted-foreground">
                <CircleDashed className="size-5" />
                <span className="font-mono text-[11px] uppercase tracking-[0.12em]">
                  {running ? "waiting for stats" : "node not running"}
                </span>
              </div>
            )}
          </section>

          {/* Neighbours */}
          <section className="panel">
            <SectionTitle eyebrow="mesh" title="Neighbours" />
            {neighbors.length > 0 ? (
              <div className="divide-y divide-border border-t border-border">
                {neighbors.map((n) => (
                  <div
                    key={n.pubkey}
                    className="flex items-center justify-between gap-3 px-4 py-2"
                  >
                    <div className="min-w-0">
                      <div className="truncate font-mono text-sm">
                        {n.name || n.pubkey.slice(0, 12)}
                      </div>
                      <div className="font-mono text-[10px] text-muted-foreground/60">
                        {n.pubkey.slice(0, 16)}…
                      </div>
                    </div>
                    <div className="flex items-center gap-4 font-mono text-[11px] tabular-nums text-muted-foreground">
                      <span>{n.snr.toFixed(1)} dB</span>
                      <span>{formatSecsAgo(n.secsAgo)}</span>
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="flex flex-col items-center gap-2 py-10 text-muted-foreground">
                <CircleDashed className="size-5" />
                <span className="font-mono text-[11px] uppercase tracking-[0.12em]">
                  no repeater neighbours heard yet
                </span>
              </div>
            )}
          </section>

          {/* Access control (admin clients in the ACL) */}
          <section className="panel">
            <SectionTitle eyebrow="admin" title="Access control" />
            {acl.length > 0 ? (
              <div className="divide-y divide-border border-t border-border">
                {acl.map((c) => (
                  <div
                    key={c.pubkey}
                    className="flex items-center justify-between gap-3 px-4 py-2"
                  >
                    <div className="min-w-0">
                      <div className="truncate font-mono text-sm">
                        {c.name || c.pubkey.slice(0, 12)}
                      </div>
                      <div className="font-mono text-[10px] text-muted-foreground/60">
                        {c.pubkey.slice(0, 16)}…
                      </div>
                    </div>
                    <div className="flex items-center gap-4">
                      <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-muted-foreground">
                        {PERM_LABELS[c.permission] ?? `perm ${c.permission}`}
                      </span>
                      <span className="font-mono text-[11px] tabular-nums text-muted-foreground">
                        {c.lastSeen > 0
                          ? formatSecsAgo(Math.max(0, Math.floor(Date.now() / 1000) - c.lastSeen))
                          : "—"}
                      </span>
                      {revoking === c.pubkey ? (
                        <Loader2 className="size-3.5 animate-spin text-muted-foreground" />
                      ) : (
                        <InlineConfirm
                          confirming={confirmRevoke === c.pubkey}
                          onAskRemove={() => setConfirmRevoke(c.pubkey)}
                          onCancel={() => setConfirmRevoke(null)}
                          onConfirm={() => revokeAcl(c.pubkey)}
                          iconOnly
                          ariaLabel={`revoke ${c.name || c.pubkey.slice(0, 12)}`}
                        />
                      )}
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="flex flex-col items-center gap-2 py-10 text-muted-foreground">
                <CircleDashed className="size-5" />
                <span className="font-mono text-[11px] uppercase tracking-[0.12em]">
                  {running ? "no admin clients yet" : "node not running"}
                </span>
              </div>
            )}
          </section>

          {/* Identity + position */}
          <section className="panel">
            <SectionTitle eyebrow="identity" title="Node" />
            <div className="grid grid-cols-1 gap-4 p-4 sm:grid-cols-2">
              <TextField label="Name" value={name} onChange={setName} placeholder="repeater" />
              <TextField
                label="Public key"
                value={rep?.pubkey ?? ""}
                onChange={() => {}}
                disabled
                hint="derived from the identity seed (read-only)"
              />
              <TextField label="Latitude" value={lat} onChange={setLat} placeholder="-27.47" />
              <TextField label="Longitude" value={lon} onChange={setLon} placeholder="153.02" />
              <div className="sm:col-span-2">
                <TextField
                  label="Rotate private key"
                  type="password"
                  value={privateKey}
                  onChange={setPrivateKey}
                  placeholder="blank = keep current identity"
                  hint={
                    <>
                      changing this <strong>rotates the identity</strong> — the
                      current pubkey is abandoned and the mesh relearns a path
                      (use to resolve a key clash). Paste a MeshCore private key
                      (128 hex) or a 32-hex seed
                    </>
                  }
                />
              </div>
              <div className="sm:col-span-2">
                <SectionSave
                  busy={saving === "node"}
                  disabled={saving !== null}
                  onClick={saveNode}
                />
              </div>
            </div>
          </section>

          {/* Relay policy */}
          <section className="panel">
            <SectionTitle eyebrow="routing" title="Relay policy" />
            <div className="space-y-4 p-4">
              <SwitchRow
                label="Disable forwarding"
                hint="stop relaying entirely (still adverts + tracks neighbours)"
                checked={disableFwd}
                onChange={setDisableFwd}
              />
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <TextField
                  label="Flood max hops"
                  value={floodMax}
                  onChange={setFloodMax}
                  placeholder="64"
                  hint="default 64 · relay flood up to N hops"
                />
                <TextField
                  label="Plain flood max hops"
                  value={floodMaxUnscoped}
                  onChange={setFloodMaxUnscoped}
                  placeholder="64"
                  hint="default 64 · extra cap for unscoped floods only (0 = never)"
                />
                <TextField
                  label="Advert max hops"
                  value={floodMaxAdvert}
                  onChange={setFloodMaxAdvert}
                  placeholder="8"
                  hint="default 8 · lower cap for adverts"
                />
                <SelectField
                  label="Loop detect"
                  value={loopDetect}
                  options={LOOP_LEVELS.map((l) => ({ value: l, label: l }))}
                  onChange={setLoopDetect}
                />
                <SelectField
                  label="Advert path hash size"
                  value={pathHashMode}
                  options={[
                    { value: "0", label: "1-byte" },
                    { value: "1", label: "2-byte" },
                    { value: "3", label: "4-byte" },
                  ]}
                  onChange={setPathHashMode}
                  hint="path hash width in our flood adverts"
                />
              </div>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <TextField
                  label="Zero-hop advert interval (seconds)"
                  value={advertInterval}
                  onChange={setAdvertInterval}
                  placeholder="0"
                  hint="local, direct neighbours only · 0 = off (default)"
                />
                <TextField
                  label="Flood advert interval (seconds)"
                  value={floodAdvertInterval}
                  onChange={setFloodAdvertInterval}
                  placeholder="169200"
                  hint="mesh-wide · 0 = off · default 169200 (47 h)"
                />
                <SelectField
                  label="Advert scope"
                  value={defaultRegion}
                  options={[
                    { value: "", label: "(unscoped)" },
                    ...regions
                      .filter((rg) => rg.name !== "*" && !rg.denyFlood)
                      .map((rg) => ({ value: rg.name, label: rg.name })),
                  ]}
                  onChange={setDefaultRegion}
                  hint="region our flood adverts are scoped to"
                />
              </div>
              <SectionSave
                busy={saving === "relay"}
                disabled={saving !== null}
                onClick={saveRelay}
              />
            </div>
          </section>

          {/* Regions */}
          <section className="panel">
            <SectionTitle eyebrow="scoping" title="Regions" />
            <div className="space-y-4 p-4">
              <p className="font-mono text-[11px] leading-relaxed text-muted-foreground">
                Transport scopes this repeater relays: it re-floods scoped
                packets whose region matches one of these (the key is derived
                from the region name). The <span className="text-foreground">*</span>{" "}
                scope is plain unscoped flood — remove it to stop relaying
                unscoped traffic, add it back to resume; deny-flood toggles it
                without removing.
              </p>
              {regions.length > 0 && (
                <div className="divide-y divide-border border border-border">
                  {regions.map((rg) => (
                    <div
                      key={rg.name}
                      className="flex items-center justify-between gap-3 px-3 py-2"
                    >
                      <span className="truncate font-mono text-sm">{rg.name}</span>
                      <div className="flex items-center gap-4">
                        {regionBusy === rg.name && (
                          <Loader2 className="size-3.5 animate-spin text-muted-foreground" />
                        )}
                        <label className="flex cursor-pointer items-center gap-2 font-mono text-[10px] uppercase tracking-[0.08em] text-muted-foreground">
                          deny flood
                          <Switch
                            checked={rg.denyFlood}
                            disabled={regionBusy !== null}
                            onCheckedChange={() => toggleDenyFlood(rg.name, rg.denyFlood)}
                          />
                        </label>
                        <InlineConfirm
                          confirming={confirmRegion === rg.name}
                          onAskRemove={() => setConfirmRegion(rg.name)}
                          onCancel={() => setConfirmRegion(null)}
                          onConfirm={() => {
                            removeRegion(rg.name);
                            setConfirmRegion(null);
                          }}
                          iconOnly
                          ariaLabel={`remove region ${rg.name}`}
                        />
                      </div>
                    </div>
                  ))}
                </div>
              )}
              <div className="flex items-end gap-2">
                <div className="flex-1">
                  <TextField
                    label="Add region"
                    value={newRegion}
                    onChange={setNewRegion}
                    placeholder="region name"
                    hint="the transport key derives from this name"
                  />
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={addRegion}
                  disabled={newRegion.trim() === "" || regionBusy !== null}
                  className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
                >
                  {regionBusy === newRegion.trim() ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : (
                    <Plus className="size-3.5" />
                  )}
                  add
                </Button>
              </div>
            </div>
          </section>

          {/* Owner + admin */}
          <section className="panel">
            <SectionTitle eyebrow="admin" title="Owner & access" />
            <div className="grid grid-cols-1 gap-4 p-4 sm:grid-cols-2">
              <TextField
                label="Owner info"
                value={ownerInfo}
                onChange={setOwnerInfo}
                placeholder="contact / location"
              />
              <div />
              <PasswordField
                label="Admin password"
                isSet={rep?.adminPasswordSet ?? false}
                state={adminPw}
                onState={setAdminPw}
              />
              <PasswordField
                label="Guest password"
                isSet={rep?.guestPasswordSet ?? false}
                state={guestPw}
                onState={setGuestPw}
              />
              <div className="sm:col-span-2">
                <SectionSave
                  busy={saving === "admin"}
                  disabled={saving !== null}
                  onClick={saveAdmin}
                />
              </div>
            </div>
          </section>

          {/* Danger zone */}
          <section className="panel">
            <SectionTitle eyebrow="danger" title="Remove repeater" />
            <div className="flex items-center justify-between gap-3 p-4">
              <p className="font-mono text-[11px] text-muted-foreground">
                Stops relaying and deletes the repeater identity and settings.
              </p>
              <InlineConfirm
                confirming={confirmDelete}
                onAskRemove={() => setConfirmDelete(true)}
                onCancel={() => setConfirmDelete(false)}
                onConfirm={onDelete}
                triggerLabel="remove repeater"
              />
            </div>
          </section>
        </>
      )}
    </div>
  );
}

// SectionSave is the per-section save button: spins when this section is
// saving, disabled while any section save runs (one at a time).
function SectionSave({
  busy,
  disabled,
  onClick,
}: {
  busy: boolean;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <div className="flex justify-end">
      <Button
        size="sm"
        onClick={onClick}
        disabled={disabled}
        className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
      >
        {busy ? <Loader2 className="size-3.5 animate-spin" /> : <Save className="size-3.5" />}
        save
      </Button>
    </div>
  );
}

type PwState = { value: string; dirty: boolean };

// PasswordField edits a write-only secret. Empty + untouched = keep; a typed
// value = set; the explicit "clear" action marks it dirty-empty = remove.
function PasswordField({
  label,
  isSet,
  state,
  onState,
}: {
  label: string;
  isSet: boolean;
  state: PwState;
  onState: (s: PwState) => void;
}) {
  const willClear = state.dirty && state.value === "";
  const hint = willClear
    ? "will be removed on save"
    : isSet
      ? "leave blank to keep the current password"
      : "optional";
  return (
    <Field label={label} hint={hint}>
      <div className="flex items-center gap-2">
        <Input
          type="password"
          value={state.value}
          onChange={(e) => onState({ value: e.target.value, dirty: true })}
          placeholder={isSet ? "•••••• set" : "none"}
          className="h-9 font-mono text-sm rounded-none border-border bg-background"
        />
        {isSet && !willClear && (
          <button
            type="button"
            onClick={() => onState({ value: "", dirty: true })}
            className="shrink-0 font-mono text-[10px] uppercase tracking-widest text-muted-foreground hover:text-destructive"
          >
            clear
          </button>
        )}
      </div>
    </Field>
  );
}

function EmptyState({
  name,
  setName,
  privateKey,
  setPrivateKey,
  saving,
  onCreate,
}: {
  name: string;
  setName: (v: string) => void;
  privateKey: string;
  setPrivateKey: (v: string) => void;
  saving: boolean;
  onCreate: () => void;
}) {
  return (
    <section className="panel">
      <SectionTitle eyebrow="setup" title="No repeater configured" />
      <div className="space-y-4 p-4">
        <p className="font-mono text-[11px] leading-relaxed text-muted-foreground">
          Run this node as a MeshCore repeater: it relays flood and direct
          packets across the mesh, advertises itself as a REPEATER, and tracks
          its RF neighbours.
        </p>
        <div className="max-w-md space-y-4">
          <TextField
            label="Name"
            value={name}
            onChange={setName}
            placeholder="repeater"
            hint="shown on the mesh and in adverts"
          />
          <TextField
            label="Private key"
            type="password"
            value={privateKey}
            onChange={setPrivateKey}
            placeholder="blank = generate a new identity"
            hint={
              <>
                the node identity — paste a MeshCore private key (128 hex, from{" "}
                <code>get prv.key</code>) to import an existing repeater, or a
                32-hex seed. Blank generates a new one; for a vanity pubkey use{" "}
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
        </div>
        <Button
          size="sm"
          onClick={onCreate}
          disabled={saving || name.trim() === ""}
          className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
        >
          {saving ? <Loader2 className="size-3.5 animate-spin" /> : null}
          create repeater
        </Button>
      </div>
    </section>
  );
}
