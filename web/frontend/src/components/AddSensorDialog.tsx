import { useCallback, useEffect, useState } from "react";
import { Loader2, Search } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  createSensor,
  discoverSensors,
  type SensorCandidate,
  type SensorProvider,
} from "@/lib/sensorsApi";

export function AddSensorDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: () => void;
}) {
  const [providers, setProviders] = useState<SensorProvider[] | null>(null);
  const [provider, setProvider] = useState<string | null>(null);
  const [candidates, setCandidates] = useState<SensorCandidate[] | null>(null);
  const [picked, setPicked] = useState<SensorCandidate | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState<"scan" | "save" | null>(null);
  const [scanError, setScanError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setProviders(null);
    setProvider(null);
    setCandidates(null);
    setPicked(null);
    setName("");
    setScanError(null);
    fetch("/api/sensors/providers")
      .then((r) => (r.ok ? r.json() : []))
      .then((ps: SensorProvider[]) => setProviders(ps || []))
      .catch(() => setProviders([]));
  }, [open]);

  const scan = useCallback(async (id: string) => {
    setProvider(id);
    setPicked(null);
    setCandidates(null);
    setScanError(null);
    setBusy("scan");
    try {
      setCandidates(await discoverSensors(id));
    } catch (e) {
      setScanError(e instanceof Error ? e.message : "Scan failed");
    } finally {
      setBusy(null);
    }
  }, []);

  const pick = useCallback((c: SensorCandidate) => {
    setPicked(c);
    setName(c.label);
  }, []);

  const save = useCallback(async () => {
    if (!provider || !picked) return;
    setBusy("save");
    try {
      await createSensor({
        provider,
        kind: picked.kind,
        name: name.trim(),
        options: picked.options,
      });
      toast.success(`Added ${name.trim()}`);
      onAdded();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to add sensor");
    } finally {
      setBusy(null);
    }
  }, [provider, picked, name, onAdded]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="rounded-none border-border bg-card max-w-md">
        <DialogHeader>
          <DialogTitle className="font-mono text-sm uppercase tracking-[0.12em]">
            Add sensor
          </DialogTitle>
          <DialogDescription className="text-xs">
            Pick where the sensor comes from, then choose one of the parts found there.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <section className="space-y-2">
            <span className="label-overline block">Source</span>
            {providers === null ? (
              <p className="text-mono-xs text-muted-foreground/70">Loading sources...</p>
            ) : providers.length === 0 ? (
              <p className="text-mono-xs text-muted-foreground/70">
                This build has no sensor sources.
              </p>
            ) : (
              <div className="grid gap-px bg-border border border-border">
                {providers.map((p) => (
                  <button
                    key={p.id}
                    type="button"
                    disabled={!p.available}
                    onClick={() => scan(p.id)}
                    className="bg-card px-3 py-2 text-left min-h-10 hover:bg-muted/50 disabled:cursor-not-allowed disabled:opacity-60 data-[picked=true]:bg-muted/60"
                    data-picked={provider === p.id}
                  >
                    <span className="font-mono text-xs uppercase tracking-[0.08em] block">
                      {p.label}
                    </span>
                    {/* An unavailable source says why, so an empty result is never mistaken
                        for "nothing is attached". */}
                    <span className="text-mono-xs text-muted-foreground/70 block">
                      {p.available ? p.id : p.reason || "unavailable"}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </section>

          {provider ? (
            <section className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <span className="label-overline">Found</span>
                <Button
                  variant="ghost"
                  size="xs"
                  disabled={busy !== null}
                  onClick={() => scan(provider)}
                  className="font-mono text-[11px] uppercase tracking-[0.12em]"
                >
                  {busy === "scan" ? (
                    <Loader2 className="size-3 animate-spin" />
                  ) : (
                    <Search className="size-3" />
                  )}
                  Scan again
                </Button>
              </div>

              {busy === "scan" ? (
                <p className="text-mono-xs text-muted-foreground/70">Scanning...</p>
              ) : scanError ? (
                <p className="text-mono-xs text-warning">{scanError}</p>
              ) : candidates && candidates.length === 0 ? (
                <p className="text-mono-xs text-muted-foreground/70">
                  This source is reachable but has nothing to offer.
                </p>
              ) : candidates ? (
                <div className="grid gap-px bg-border border border-border max-h-56 overflow-y-auto">
                  {candidates.map((c) => (
                    <button
                      key={`${c.kind}-${c.detail ?? c.label}`}
                      type="button"
                      onClick={() => pick(c)}
                      className="bg-card px-3 py-2 text-left min-h-10 hover:bg-muted/50 data-[picked=true]:bg-muted/60"
                      data-picked={picked === c}
                    >
                      <span className="font-mono text-xs block truncate">{c.label}</span>
                      {c.detail ? (
                        <code className="font-mono text-[11px] sm:text-[10px] leading-none text-muted-foreground/70 block truncate">
                          {c.detail}
                        </code>
                      ) : null}
                    </button>
                  ))}
                </div>
              ) : null}
            </section>
          ) : null}

          {picked ? (
            <section className="space-y-2">
              <label
                htmlFor="sensor-name"
                className="label-overline block"
              >
                Name
              </label>
              <Input
                id="sensor-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="What to call it on this page"
                className="rounded-none border-border font-mono text-base md:text-xs"
              />
            </section>
          ) : null}
        </div>

        <div className="flex justify-end gap-2">
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
            disabled={!picked || name.trim() === "" || busy !== null}
            onClick={save}
            className="font-mono text-xs uppercase tracking-widest"
          >
            {busy === "save" ? <Loader2 className="size-3.5 animate-spin" /> : null}
            Add sensor
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
