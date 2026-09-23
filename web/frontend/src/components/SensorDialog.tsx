import { useCallback, useEffect, useState } from "react";
import { ChevronLeft, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { SensorCatalogue, type Entry } from "@/components/SensorCatalogue";
import { BindingEditor } from "@/components/BindingEditor";
import {
  createSensor,
  discoverSensors,
  updateSensor,
  type Sensor,
  type SensorBinding,
  type SensorKind,
  type SensorScan,
} from "@/lib/sensorsApi";

// A pick is the part being configured, whether it came from the scan or the catalogue.
interface Pick {
  kind: SensorKind;
  name: string;
  options: Record<string, string>;
  // bindings are the other sensors this one reads; empty for anything that talks to hardware.
  bindings: SensorBinding[];
}

function seedOptions(
  kind: SensorKind,
  from: Record<string, string> | undefined,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const f of kind.fields) out[f.key] = from?.[f.key] ?? f.default ?? "";
  return out;
}

// seedPick fills the form to edit a sensor; a kind this build no longer carries still opens.
function seedPick(editing: Sensor, kinds: SensorKind[]): Pick {
  const k = kinds.find((x) => x.provider === editing.provider && x.kind === editing.kind);
  return {
    kind: k ?? {
      kind: editing.kind,
      provider: editing.provider,
      label: editing.kind,
      fields: [],
    },
    name: editing.name,
    options: k ? seedOptions(k, editing.options) : { ...editing.options },
    bindings: editing.bindings,
  };
}

// snapshot is what an edit would save, so an unchanged form can say so.
const snapshot = (p: Pick) => JSON.stringify([p.name.trim(), p.options, p.bindings]);

// blockedBy says why the form cannot be saved yet, in the operator's words, or null when it can.
function blockedBy(pick: Pick | null): string | null {
  if (!pick) return "Choose a part first.";
  if (!pick.name.trim()) return "Give it a name.";
  // In the order the form asks, so the reason points at the first thing to fix.
  if (pick.kind.binds && pick.bindings.length === 0) return "Choose at least one sensor for it to read.";
  if (pick.kind.binds && pick.bindings.some((b) => !b.name.trim() || !b.sensorId)) {
    return "Finish every reading it uses.";
  }
  const missing = pick.kind.fields.find((f) => f.required && !pick.options[f.key]?.trim());
  return missing ? `Fill in ${missing.label}.` : null;
}

export function SensorDialog({
  open,
  onOpenChange,
  editing,
  sensors,
  kinds,
  kindsError,
  onRetryKinds,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // editing is the sensor being changed, or null to add a new one.
  editing: Sensor | null;
  // sensors is what a derived sensor can read; the picker is built from it.
  sensors: Sensor[];
  // kinds is the catalogue, loaded once by the page; null until it lands or when it failed.
  kinds: SensorKind[] | null;
  kindsError: string | null;
  onRetryKinds: () => void;
  onSaved: () => void;
}) {
  const [scan, setScan] = useState<SensorScan | null>(null);
  const [query, setQuery] = useState("");
  const [pick, setPick] = useState<Pick | null>(null);
  const [scanning, setScanning] = useState(false);
  const [saving, setSaving] = useState(false);
  // Kept on the form rather than in a toast, which flattens an expression error's caret line.
  const [saveError, setSaveError] = useState<string | null>(null);
  // seed is the edited sensor as it opened, so Save stays off until something changes.
  const [seed, setSeed] = useState("");

  const rescan = useCallback(async () => {
    setScanning(true);
    try {
      setScan(await discoverSensors());
    } catch (e) {
      // A scan that failed outright is a problem to show, not an empty bus.
      setScan({
        candidates: [],
        problems: [{ provider: "", label: "Sensors", reason: e instanceof Error ? e.message : "the scan failed" }],
      });
    } finally {
      setScanning(false);
    }
  }, []);

  // Editing opens on the form, even without the catalogue; adding opens on the picker.
  useEffect(() => {
    if (!open) return;
    setQuery("");
    setSaving(false);
    setSaveError(null);
    const seeded = editing ? seedPick(editing, kinds ?? []) : null;
    setPick(seeded);
    setSeed(seeded ? snapshot(seeded) : "");
  }, [open, editing, kinds]);

  // The scan answers on its own clock; a failed one must not hold up adding a part by hand.
  useEffect(() => {
    if (!open) return;
    setScan(null);
    if (!editing) void rescan();
  }, [open, editing, rescan]);

  const save = useCallback(async () => {
    if (!pick) return;
    setSaving(true);
    setSaveError(null);
    const body = {
      provider: pick.kind.provider,
      kind: pick.kind.kind,
      name: pick.name.trim(),
      options: pick.options,
      bindings: pick.kind.binds ? pick.bindings : undefined,
    };
    try {
      if (editing) {
        await updateSensor(editing.id, body);
        toast.success(`Saved ${body.name}`);
      } else {
        await createSensor(body);
        toast.success(`Added ${body.name}`);
      }
      onSaved();
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : "Failed to save sensor");
    } finally {
      setSaving(false);
    }
  }, [pick, editing, onSaved]);

  const choose = useCallback((e: Entry) => {
    setSaveError(null);
    setPick({
      kind: e.kind,
      name: e.kind.label,
      options: seedOptions(e.kind, e.options),
      bindings: [],
    });
  }, []);

  const blocked = blockedBy(pick);
  const unchanged = editing !== null && pick !== null && snapshot(pick) === seed;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* sm:max-w-md, because the base class is sm:max-w-lg and a bare max-w-md loses to it from 640px up. */}
      <DialogContent className="rounded-none border-border bg-card sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="font-mono text-sm uppercase tracking-[0.12em]">
            {editing ? "Edit sensor" : "Add sensor"}
          </DialogTitle>
          <DialogDescription className="text-xs">
            {editing
              ? "Change its name or its settings."
              : pick
                ? "Name it and fill in its settings."
                : "Pick a part a scan matched, or search for one and set it up by hand."}
          </DialogDescription>
        </DialogHeader>

        {pick ? (
          <SensorForm
            pick={pick}
            onChange={setPick}
            onBack={
              editing
                ? null
                : () => {
                    setSaveError(null);
                    setPick(null);
                  }
            }
            sensors={sensors}
            editingId={editing?.id}
          />
        ) : (
          <SensorCatalogue
            scan={scan}
            catalogue={kinds}
            catalogueError={kindsError}
            onRetryCatalogue={onRetryKinds}
            query={query}
            onQuery={setQuery}
            scanning={scanning}
            onRescan={() => void rescan()}
            onPick={choose}
          />
        )}

        {saveError ? (
          <p
            role="alert"
            className="border border-warning/40 bg-warning/5 px-3 py-2 font-mono text-[11px] sm:text-[10px] leading-relaxed text-warning whitespace-pre-wrap wrap-break-word"
          >
            {saveError}
          </p>
        ) : null}

        <div className="flex items-center justify-end gap-2">
          {pick && blocked ? (
            <span className="mr-auto font-mono text-[11px] sm:text-[10px] text-muted-foreground/70">
              {blocked}
            </span>
          ) : null}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onOpenChange(false)}
            className="font-mono text-xs uppercase tracking-widest"
          >
            Cancel
          </Button>
          <Button
            size="sm"
            disabled={blocked !== null || unchanged || saving}
            onClick={save}
            className="font-mono text-xs uppercase tracking-widest"
          >
            {saving ? <Loader2 className="size-3.5 animate-spin" /> : null}
            {editing ? "Save changes" : "Add sensor"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function SensorForm({
  pick,
  onChange,
  onBack,
  sensors,
  editingId,
}: {
  pick: Pick;
  onChange: (p: Pick) => void;
  onBack: (() => void) | null;
  sensors: Sensor[];
  editingId?: number;
}) {
  return (
    // min-w-0: a grid item in DialogContent, or the widest field sets the column and carries the buttons past the padding.
    <section className="space-y-3 min-w-0">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <span className="label-overline block">{pick.kind.label}</span>
          {pick.kind.description ? (
            <span className="font-mono text-[11px] sm:text-[10px] text-muted-foreground/70 truncate block">
              {pick.kind.description}
            </span>
          ) : null}
        </div>
        {onBack ? (
          <Button
            variant="ghost"
            size="xs"
            onClick={onBack}
            className="shrink-0 font-mono text-[11px] uppercase tracking-[0.12em]"
          >
            <ChevronLeft className="size-3" />
            Back
          </Button>
        ) : null}
      </div>

      <div className="space-y-1.5">
        <label htmlFor="sensor-name" className="label-overline block">
          Name<span aria-hidden> *</span>
        </label>
        <Input
          id="sensor-name"
          value={pick.name}
          onChange={(e) => onChange({ ...pick, name: e.target.value })}
          placeholder="What to call it on this page"
          autoFocus
          aria-required
          className="rounded-none border-border font-mono text-base md:text-xs"
        />
      </div>

      {pick.kind.binds ? (
        <BindingEditor
          bindings={pick.bindings}
          onChange={(bindings) => onChange({ ...pick, bindings })}
          sensors={sensors}
          excludeId={editingId}
        />
      ) : null}

      {pick.kind.fields.map((f) => (
        <div key={f.key} className="space-y-1.5">
          <label htmlFor={`sensor-${f.key}`} className="label-overline block">
            {f.label}
            {f.required ? <span aria-hidden> *</span> : null}
          </label>
          {f.choices && f.choices.length > 0 ? (
            <Select
              value={pick.options[f.key] || ""}
              onValueChange={(v) =>
                onChange({ ...pick, options: { ...pick.options, [f.key]: v } })
              }
            >
              <SelectTrigger
                id={`sensor-${f.key}`}
                aria-required={f.required}
                className="rounded-none border-border font-mono text-xs w-full"
              >
                <SelectValue placeholder="Choose one" />
              </SelectTrigger>
              <SelectContent className="rounded-none">
                {f.choices.map((c) => (
                  <SelectItem key={c} value={c} className="font-mono text-xs rounded-none">
                    {c}
                  </SelectItem>
                ))}
                {/* A stored value this host no longer offers, such as a bus that has gone, still shows. */}
                {pick.options[f.key] && !f.choices.includes(pick.options[f.key]) ? (
                  <SelectItem value={pick.options[f.key]} className="font-mono text-xs rounded-none">
                    {pick.options[f.key]} (not on this host)
                  </SelectItem>
                ) : null}
              </SelectContent>
            </Select>
          ) : f.multiline ? (
            // field-sizing-content grows the Textarea, so a long expression needs no scrollbar.
            <Textarea
              id={`sensor-${f.key}`}
              value={pick.options[f.key] ?? ""}
              onChange={(e) =>
                onChange({ ...pick, options: { ...pick.options, [f.key]: e.target.value } })
              }
              placeholder={f.default}
              rows={2}
              spellCheck={false}
              aria-required={f.required}
              className="rounded-none border-border bg-transparent font-mono text-base md:text-xs min-h-0 leading-relaxed"
            />
          ) : (
            <Input
              id={`sensor-${f.key}`}
              value={pick.options[f.key] ?? ""}
              onChange={(e) =>
                onChange({ ...pick, options: { ...pick.options, [f.key]: e.target.value } })
              }
              placeholder={f.default}
              aria-required={f.required}
              className="rounded-none border-border font-mono text-base md:text-xs"
            />
          )}
          {f.help ? (
            <p className="font-mono text-[11px] sm:text-[10px] leading-relaxed text-muted-foreground/70">
              {f.help}
            </p>
          ) : null}
        </div>
      ))}
    </section>
  );
}
