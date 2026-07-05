import { useCallback, useEffect, useMemo, useState } from "react";
import { Loader2, Save } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { InlineConfirm } from "@/components/InlineConfirm";
import { cn } from "@/lib/utils";
import { RETRY_OPTS, MAX_RETRIES_OPTS } from "@/lib/monitorOptions";

export interface LinkMonitorInfo {
  id: number;
  key: string;
  companion: string;
  label: string;
  path: string;
  pathHashSize: number;
  intervalSecs: number;
  enabled: boolean;
  ignoreFirstHop: boolean;
  retrySecs: number;
  maxRetries: number;
  hideLastSnr: boolean;
}

const INTERVAL_OPTS: { value: string; label: string }[] = [
  { value: "300", label: "5 min" },
  { value: "900", label: "15 min" },
  { value: "1800", label: "30 min" },
  { value: "3600", label: "1 hour" },
  { value: "10800", label: "3 hours" },
  { value: "21600", label: "6 hours" },
  { value: "43200", label: "12 hours" },
  { value: "86400", label: "24 hours" },
];

// Mirrors internal/monitor.DefaultRetrySecs — the poller's built-in retry
// delay when a link doesn't override it (value "0").
const DEFAULT_RETRY_SECS = 300;

// LinkMonitorSettings is the per-link config form on the monitoring detail
// page for kind === "link" nodes — the link-monitor sibling of
// MonitoringSettings (which only applies to contact-derived node/companion
// monitors). It fetches the link by its synthetic key, since the detail page
// only has the key (used as the pubkey URL param) not the DB row.
export function LinkMonitorSettings({
  linkKey,
  onSaved,
  onDeleted,
  className,
}: {
  linkKey: string;
  onSaved?: () => void;
  onDeleted?: () => void;
  className?: string;
}) {
  const [link, setLink] = useState<LinkMonitorInfo | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [label, setLabel] = useState("");
  const [intervalSecs, setIntervalSecs] = useState("900");
  const [enabled, setEnabled] = useState(true);
  const [ignoreFirstHop, setIgnoreFirstHop] = useState(false);
  const [retrySecs, setRetrySecs] = useState("0");
  const [maxRetries, setMaxRetries] = useState("0");
  const [hideLastSnr, setHideLastSnr] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  const load = useCallback(() => {
    fetch("/api/links")
      .then((r) => (r.ok ? r.json() : []))
      .then((links: LinkMonitorInfo[]) => {
        const l = (links || []).find(
          (x) => x.key.toLowerCase() === linkKey.toLowerCase(),
        );
        if (l) {
          setLink(l);
          setLabel(l.label);
          setIntervalSecs(String(l.intervalSecs));
          setEnabled(l.enabled);
          setIgnoreFirstHop(l.ignoreFirstHop);
          setRetrySecs(String(l.retrySecs));
          setMaxRetries(String(l.maxRetries));
          setHideLastSnr(l.hideLastSnr);
        }
        setLoaded(true);
      })
      .catch(() => setLoaded(true));
  }, [linkKey]);

  useEffect(() => {
    load();
  }, [load]);

  // A link only fast-retries a timed-out trace when Max retries is
  // explicitly positive (internal/app.linkCollector.Collect) — unlike node
  // monitoring, "0/Default" is a no-op for links here (it still applies to
  // genuine send/companion-down failures via the poller's own fallback, but
  // that's not the timeout case this setting is about). "-1/None" is
  // likewise a no-op. Either way the retry delay is moot.
  const noRetries = Number(maxRetries) <= 0;

  // Mirrors the backend's validateLinkRetry: only positive Max retries is
  // validated against the interval (see noRetries above) — the retry span
  // (delay × max retries) must fit within the poll interval, or a retry
  // could still be in flight when the next scheduled poll comes due.
  const retryTooLong = useMemo(() => {
    const numMax = Number(maxRetries);
    if (numMax <= 0) return false;
    const effRetry = Number(retrySecs) || DEFAULT_RETRY_SECS;
    return effRetry * numMax > Number(intervalSecs);
  }, [retrySecs, maxRetries, intervalSecs]);

  const save = async () => {
    if (!link || retryTooLong) return;
    setSaving(true);
    try {
      const r = await fetch(`/api/links/${link.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          label,
          intervalSecs: Number(intervalSecs),
          enabled,
          ignoreFirstHop,
          retrySecs: Number(retrySecs),
          maxRetries: Number(maxRetries),
          hideLastSnr,
        }),
      });
      if (!r.ok) {
        const body = await r.json().catch(() => null);
        throw new Error(body?.error || "Failed to save link monitor settings");
      }
      toast.success("Link monitor settings saved");
      onSaved?.();
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : "Failed to save link monitor settings",
      );
    } finally {
      setSaving(false);
    }
  };

  const doDelete = async () => {
    if (!link) return;
    setDeleting(true);
    try {
      const r = await fetch(`/api/links/${link.id}`, { method: "DELETE" });
      if (!r.ok && r.status !== 204) throw new Error(await r.text());
      toast.success("Link monitor deleted");
      onDeleted?.();
    } catch {
      toast.error("Failed to delete link monitor");
      setDeleting(false);
    }
  };

  return (
    <div className={cn("panel", className)}>
      <div className="flex items-center justify-between px-4 py-3 border-b border-border">
        <div className="space-y-0.5">
          <span className="label-overline block">Link monitor</span>
          <h2 className="font-mono text-sm uppercase tracking-widest">
            Schedule &amp; label
          </h2>
        </div>
        <label className="flex items-center gap-2 cursor-pointer">
          <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
            {enabled ? "on" : "off"}
          </span>
          <Switch
            checked={enabled}
            disabled={!loaded || !link}
            onCheckedChange={setEnabled}
          />
        </label>
      </div>

      <div
        className={cn(
          "p-4 space-y-4 transition-opacity",
          !enabled && "opacity-60",
        )}
      >
        {!loaded ? (
          <p className="font-mono text-xs text-muted-foreground/60">
            loading…
          </p>
        ) : !link ? (
          <p className="font-mono text-xs text-muted-foreground/60">
            Link monitor not found — it may have been deleted.
          </p>
        ) : (
          <>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div className="space-y-1">
                <Label className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
                  Label
                </Label>
                <Input
                  value={label}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
                    setLabel(e.target.value)
                  }
                  placeholder="e.g. rooftop A ↔ rooftop B"
                  className="rounded-none font-mono text-xs border-border bg-background"
                />
              </div>
              <div className="space-y-1">
                <Label className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
                  Poll interval
                </Label>
                <Select value={intervalSecs} onValueChange={setIntervalSecs}>
                  <SelectTrigger className="rounded-none font-mono text-xs border-border bg-background w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className="rounded-none font-mono text-xs">
                    {INTERVAL_OPTS.map((o) => (
                      <SelectItem
                        key={o.value}
                        value={o.value}
                        className="rounded-none font-mono text-xs"
                      >
                        {o.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div className="space-y-1">
                <Label className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
                  Retry delay
                </Label>
                <Select
                  value={retrySecs}
                  onValueChange={setRetrySecs}
                  disabled={noRetries}
                >
                  <SelectTrigger className="rounded-none font-mono text-xs border-border bg-background w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className="rounded-none font-mono text-xs">
                    {RETRY_OPTS.map((o) => (
                      <SelectItem
                        key={o.value}
                        value={o.value}
                        className="rounded-none font-mono text-xs"
                      >
                        {o.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1">
                <Label className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
                  Max retries
                </Label>
                <Select value={maxRetries} onValueChange={setMaxRetries}>
                  <SelectTrigger className="rounded-none font-mono text-xs border-border bg-background w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className="rounded-none font-mono text-xs">
                    {MAX_RETRIES_OPTS.map((o) => (
                      <SelectItem
                        key={o.value}
                        value={o.value}
                        className="rounded-none font-mono text-xs"
                      >
                        {o.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <p
              className={cn(
                "font-mono text-[10px] -mt-2",
                retryTooLong
                  ? "text-destructive"
                  : "text-muted-foreground/60",
              )}
            >
              {retryTooLong
                ? "Retry delay × max retries must fit within the poll interval — lower one of them to save."
                : noRetries
                  ? "A timed-out trace (no reply) or a failed poll goes straight back to the normal Poll interval — no fast re-attempt."
                  : "A timed-out trace (no reply) or a failed poll re-attempts up to Max retries times at the Retry delay, then resumes the normal Poll interval — lets you tell an ephemeral drop from a consistent one."}
            </p>

            <label className="flex items-center justify-between gap-3 px-3 py-2 border border-border bg-background cursor-pointer">
              <div className="min-w-0">
                <div className="font-mono text-xs uppercase tracking-[0.08em]">
                  Ignore first hop
                </div>
                <div className="font-mono text-[10px] text-muted-foreground/60 leading-relaxed">
                  Hides the "you → first node" reading — that leg is your
                  local companion-to-base-station radio, usually rock solid,
                  so its SNR just clutters the charts.
                </div>
              </div>
              <Switch
                checked={ignoreFirstHop}
                onCheckedChange={setIgnoreFirstHop}
              />
            </label>

            <label className="flex items-center justify-between gap-3 px-3 py-2 border border-border bg-background cursor-pointer">
              <div className="min-w-0">
                <div className="font-mono text-xs uppercase tracking-[0.08em]">
                  Hide SNR graph
                </div>
                <div className="font-mono text-[10px] text-muted-foreground/60 leading-relaxed">
                  Hides the "SNR" tile/chart — on an out-and-back path that's
                  the same local companion-to-base-station leg as the first
                  hop, so it's just as static and uninteresting, for the
                  same reason.
                </div>
              </div>
              <Switch checked={hideLastSnr} onCheckedChange={setHideLastSnr} />
            </label>

            <div className="flex items-center justify-between pt-1">
              <InlineConfirm
                confirming={confirmingDelete}
                onAskRemove={() => setConfirmingDelete(true)}
                onCancel={() => setConfirmingDelete(false)}
                onConfirm={doDelete}
                triggerLabel="delete link monitor"
              />
              <Button
                size="sm"
                onClick={save}
                disabled={saving || deleting || retryTooLong}
                className="rounded-none font-mono text-[10px] uppercase tracking-[0.12em]"
              >
                {saving ? (
                  <Loader2 className="size-3 animate-spin" />
                ) : (
                  <Save className="size-3" />
                )}
                save
              </Button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
