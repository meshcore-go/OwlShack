import { useCallback, useEffect, useState } from "react";
import {
  AlertTriangle,
  Check,
  ChevronLeft,
  ChevronRight,
  Download,
  HardDriveDownload,
  KeyRound,
  Loader2,
} from "lucide-react";
import { toast } from "sonner";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { SelectField } from "@/components/ConfigFields";
import { useApiList } from "@/hooks/useApiList";
import { apiErrorMessage } from "@/lib/apiError";
import { type ConfigCompanion } from "@/lib/configApi";
import { cn } from "@/lib/utils";

// Day windows. -1 is everything, 0 is nothing.
const WINDOWS = [
  { value: "0", label: "Don't include" },
  { value: "7", label: "Last 7 days" },
  { value: "30", label: "Last 30 days" },
  { value: "90", label: "Last 90 days" },
  { value: "-1", label: "Everything" },
];

type Options = {
  // Exactly which companions to include: [] is none, every id is all. The
  // server rejects the request if it is missing, so never omit it.
  companionIds?: number[];
  contacts: boolean;
  triggers: boolean;
  mqtt: boolean;
  repeater: boolean;
  peers: boolean;
  messageDays: number;
  packetDays: number;
  metricDays: number;
  identityKeys: boolean;
};

// Defaults suit the common case: the whole setup, none of the bulk history.
const DEFAULTS: Options = {
  contacts: true,
  triggers: true,
  mqtt: true,
  repeater: true,
  peers: false,
  messageDays: 0,
  packetDays: 0,
  metricDays: 0,
  identityKeys: false,
};

type Estimate = {
  companions: number;
  contacts: number;
  messages: number;
  packets: number;
  peers: number;
  metrics: number;
  bytes: number;
};

type Step = "contents" | "history" | "keys" | "review";
const STEPS: { id: Step; label: string }[] = [
  { id: "contents", label: "Contents" },
  { id: "history", label: "History" },
  { id: "keys", label: "Keys" },
  { id: "review", label: "Review" },
];

function StepDots({ current }: { current: Step }) {
  const idx = STEPS.findIndex((s) => s.id === current);
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[10px] uppercase tracking-[0.12em]">
      {STEPS.map((s, i) => (
        <div key={s.id} className="flex items-center gap-2">
          <span
            className={cn(
              "inline-flex items-center gap-1.5",
              i === idx
                ? "text-primary"
                : i < idx
                  ? "text-muted-foreground"
                  : "text-muted-foreground/40",
            )}
          >
            {i < idx ? <Check className="size-3" /> : null}
            {s.label}
          </span>
          {i < STEPS.length - 1 && (
            <ChevronRight className="size-3 text-muted-foreground/30" />
          )}
        </div>
      ))}
    </div>
  );
}

// Toggle is a labelled switch row with room for a consequence line.
function Toggle({
  label,
  hint,
  checked,
  disabled,
  onChange,
}: {
  label: string;
  hint: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="flex items-start gap-3 min-h-10 sm:min-h-0 cursor-pointer">
      <Switch
        checked={checked}
        disabled={disabled}
        onCheckedChange={onChange}
        aria-label={label}
      />
      <span className="min-w-0 space-y-0.5">
        <span className="block font-mono text-xs uppercase tracking-[0.08em]">
          {label}
        </span>
        <span className="block font-mono text-[11px] leading-relaxed text-muted-foreground">
          {hint}
        </span>
      </span>
    </label>
  );
}

function formatBytes(n: number) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

// BackupWizard walks the operator through what a backup should contain. It is
// export only: restoring is offered in the setup wizard, because merging a
// backup into a node that is already running is what breaks things.
export function BackupWizard({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const [step, setStep] = useState<Step>("contents");
  const [opts, setOpts] = useState<Options>(DEFAULTS);
  const [busy, setBusy] = useState(false);
  const [est, setEst] = useState<Estimate | null>(null);
  const [estimating, setEstimating] = useState(false);

  const { items: companions } = useApiList<ConfigCompanion>(
    open ? "/api/config/companions" : null,
    "Failed to load companions",
  );
  const all = companions ?? [];

  // The selection lives here, not in opts, and is always sent in full. null
  // means the companion list has not loaded yet, which is the only reason to
  // hold off on a request — it never travels as a meaning.
  const [picked, setPicked] = useState<number[] | null>(null);
  useEffect(() => {
    if (companions && picked === null) setPicked(companions.map((c) => c.id));
  }, [companions, picked]);

  const isSelected = (id: number) => (picked ?? []).includes(id);
  const toggleCompanion = (id: number) =>
    setPicked((p) => {
      const cur = p ?? all.map((c) => c.id);
      return cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id];
    });

  const payload = useCallback(
    (o: Options): Options => ({ ...o, companionIds: picked ?? [] }),
    [picked],
  );

  const set = <K extends keyof Options>(k: K, v: Options[K]) =>
    setOpts((p) => ({ ...p, [k]: v }));

  const refreshEstimate = useCallback(async (o: Options) => {
    setEstimating(true);
    try {
      const r = await fetch("/api/backup/estimate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload(o)),
      });
      if (!r.ok) throw new Error(await apiErrorMessage(r));
      setEst((await r.json()) as Estimate);
    } catch {
      setEst(null);
    } finally {
      setEstimating(false);
    }
  }, [payload]);

  // Only the review step shows the numbers, so only fetch them there.
  useEffect(() => {
    if (open && step === "review" && picked !== null) refreshEstimate(opts);
  }, [open, step, opts, picked, refreshEstimate]);

  useEffect(() => {
    if (!open) {
      setStep("contents");
      setOpts(DEFAULTS);
      setPicked(null);
      setEst(null);
    }
  }, [open]);

  const download = async () => {
    if (picked === null) return; // companion list still loading
    setBusy(true);
    try {
      const r = await fetch("/api/backup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload(opts)),
      });
      if (!r.ok) throw new Error(await apiErrorMessage(r));
      const blob = await r.blob();
      const disp = r.headers.get("Content-Disposition") ?? "";
      const named = /filename="?([^";]+)"?/.exec(disp);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = named?.[1] ?? "owlshack-backup.db";
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      toast.success(`Backup downloaded (${formatBytes(blob.size)})`);
      onOpenChange(false);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Backup failed");
    } finally {
      setBusy(false);
    }
  };

  const idx = STEPS.findIndex((s) => s.id === step);
  const goBack = () => setStep(STEPS[Math.max(0, idx - 1)].id);
  const goNext = () => setStep(STEPS[Math.min(STEPS.length - 1, idx + 1)].id);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <div className="flex items-center gap-3">
            <span className="grid size-9 shrink-0 place-items-center rounded-sm border border-primary/30 bg-primary/10 text-primary">
              <HardDriveDownload className="size-4" strokeWidth={1.6} />
            </span>
            <DialogTitle className="font-mono text-base uppercase tracking-[0.08em]">
              Create a backup
            </DialogTitle>
          </div>
          <StepDots current={step} />
        </DialogHeader>

        {step === "contents" && (
          <div className="space-y-5">
            <p className="font-mono text-xs leading-relaxed text-muted-foreground">
              Your radio settings are always included. Choose what else to bring.
            </p>

            {all.length > 0 && (
              <div className="space-y-2">
                <span className="label-overline">Companions</span>
                <div className="border border-border divide-y divide-border">
                  {all.map((c) => (
                    <label
                      key={c.id}
                      className="flex items-center gap-3 px-3 py-2 min-h-10 sm:min-h-0 cursor-pointer"
                    >
                      <Switch
                        checked={isSelected(c.id)}
                        onCheckedChange={() => toggleCompanion(c.id)}
                        aria-label={c.name}
                      />
                      <span className="min-w-0 flex-1 truncate font-mono text-xs">
                        {c.name}
                      </span>
                    </label>
                  ))}
                </div>
                <p className="font-mono text-[11px] text-muted-foreground">
                  Leaving a companion out also leaves out its channels, contacts
                  and messages. Turn them all off for a radio-settings-only
                  backup.
                </p>
                {picked !== null && picked.length === 0 && (
                  <p className="font-mono text-[11px] text-warning">
                    No companions selected: this backup will only carry the radio
                    settings, and the restored node will need a new companion.
                  </p>
                )}
              </div>
            )}

            <div className="space-y-3">
              <span className="label-overline">Also include</span>
              <Toggle
                label="Contacts"
                hint="Your address book, saved repeater passwords and learned routes."
                checked={opts.contacts}
                onChange={(v) => set("contacts", v)}
              />
              <Toggle
                label="Bots"
                hint="Automated replies and scheduled messages."
                checked={opts.triggers}
                onChange={(v) => set("triggers", v)}
              />
              <Toggle
                label="MQTT brokers"
                hint="Uplink servers and their credentials."
                checked={opts.mqtt}
                onChange={(v) => set("mqtt", v)}
              />
              <Toggle
                label="Repeater node"
                hint="This node's repeater settings and its access list."
                checked={opts.repeater}
                onChange={(v) => set("repeater", v)}
              />
            </div>
          </div>
        )}

        {step === "history" && (
          <div className="space-y-5">
            <p className="font-mono text-xs leading-relaxed text-muted-foreground">
              History is optional. It makes the file bigger and is not needed to
              get a new node working — skip it unless you want to keep the record.
            </p>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <SelectField
                label="Chat history"
                value={String(opts.messageDays)}
                options={WINDOWS}
                onChange={(v) => set("messageDays", Number(v))}
                hint="Messages in your channels and DMs."
              />
              <SelectField
                label="Packet log"
                value={String(opts.packetDays)}
                options={WINDOWS}
                onChange={(v) => set("packetDays", Number(v))}
                hint="Raw mesh traffic. This is usually the largest part by far."
              />
              <SelectField
                label="Monitoring history"
                value={String(opts.metricDays)}
                options={WINDOWS}
                onChange={(v) => set("metricDays", Number(v))}
                hint="Battery, temperature and signal readings over time."
              />
              <div className="space-y-2">
                <span className="label-overline">Discovered peers</span>
                <Toggle
                  label="Nodes seen on the mesh"
                  hint="Rebuilds on its own within minutes of going on the air."
                  checked={opts.peers}
                  onChange={(v) => set("peers", v)}
                />
              </div>
            </div>
          </div>
        )}

        {step === "keys" && (
          <div className="space-y-5">
            <div className="flex items-start gap-3">
              <span className="grid size-9 shrink-0 place-items-center rounded-sm border border-border bg-muted/40 text-muted-foreground">
                <KeyRound className="size-4" strokeWidth={1.6} />
              </span>
              <p className="font-mono text-xs leading-relaxed text-muted-foreground">
                Your identity keys are this node's address on the mesh. Include
                them only if you are <em>replacing</em> this node — two nodes
                sharing an identity will clash.
              </p>
            </div>
            <Toggle
              label="Include identity keys"
              hint={
                opts.identityKeys
                  ? "The restored node will be this same node on the mesh. Do not run both at once."
                  : "The restored node gets a new address on the mesh, and repeater logins must be redone."
              }
              checked={opts.identityKeys}
              onChange={(v) => set("identityKeys", v)}
            />
            <p className="flex items-start gap-2 font-mono text-[11px] leading-relaxed text-warning">
              <AlertTriangle className="size-3.5 shrink-0 mt-px" />
              <span>
                Either way this file contains your channel keys and saved
                passwords. Keep it somewhere private — treat it like a password.
              </span>
            </p>
          </div>
        )}

        {step === "review" && (
          <div className="space-y-4">
            <span className="label-overline">This backup will contain</span>
            <div className="grid grid-cols-2 sm:grid-cols-3 gap-px bg-border border border-border">
              {[
                ["Companions", est?.companions],
                ["Contacts", est?.contacts],
                ["Messages", est?.messages],
                ["Packets", est?.packets],
                ["Peers", est?.peers],
                ["Readings", est?.metrics],
              ].map(([label, value]) => (
                <div key={String(label)} className="bg-card p-3">
                  <span className="label-overline block">{label}</span>
                  <span className="font-mono text-lg tabular-nums">
                    {estimating ? "…" : (value ?? 0).toLocaleString()}
                  </span>
                </div>
              ))}
            </div>
            <p className="font-mono text-[11px] leading-relaxed text-muted-foreground">
              {opts.identityKeys
                ? "Identity keys are included: restoring this makes the other node into this node."
                : "Identity keys are not included: the restored node will have a new address on the mesh."}
              {est ? ` Current database is ${formatBytes(est.bytes)}; the backup will be at most that.` : ""}
            </p>
            <p className="font-mono text-[11px] leading-relaxed text-muted-foreground/70">
              To use it, run OwlShack on the new machine and choose “Restore a
              backup” on its first-run screen.
            </p>
          </div>
        )}

        <div className="flex flex-wrap items-center justify-between gap-2 pt-2">
          <div>
            {idx > 0 && (
              <Button
                variant="ghost"
                size="sm"
                onClick={goBack}
                className="font-mono uppercase tracking-widest"
              >
                <ChevronLeft className="size-4" /> back
              </Button>
            )}
          </div>
          <div className="flex items-center gap-2">
            {step !== "review" ? (
              <Button
                size="sm"
                onClick={goNext}
                className="font-mono uppercase tracking-widest"
              >
                next <ChevronRight className="size-4" />
              </Button>
            ) : (
              <Button
                size="sm"
                onClick={download}
                disabled={busy}
                className="font-mono uppercase tracking-widest"
              >
                {busy ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  <Download className="size-4" />
                )}
                download
              </Button>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
