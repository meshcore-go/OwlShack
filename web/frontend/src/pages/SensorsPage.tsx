import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CircleDashed, Pencil, Plus, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { InlineConfirm } from "@/components/InlineConfirm";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { PageHeader } from "@/components/PageHeader";
import { SensorDialog } from "@/components/SensorDialog";
import { useApiList } from "@/hooks/useApiList";
import { useWebSocket } from "@/hooks/useWebSocket";
import { timeAgo } from "@/lib/format";
import { useResume } from "@/lib/resume";
import { cn } from "@/lib/utils";
import { deleteSensor, type Sensor, type SensorKind, type SensorReading } from "@/lib/sensorsApi";

export function SensorsPage() {
  const {
    items: sensors,
    loading,
    error,
    reload,
    refresh,
    setItems,
  } = useApiList<Sensor>("/api/sensors", "Failed to load sensors");
  // The catalogue is fixed for the process, so it is loaded once here and the dialog reads it.
  const {
    items: kinds,
    error: kindsError,
    reload: reloadKinds,
  } = useApiList<SensorKind>("/api/sensors/kinds", "Failed to load the parts catalogue");
  const [confirmRemove, setConfirmRemove] = useState<number | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  // null while adding; the sensor being changed while editing.
  const [editing, setEditing] = useState<Sensor | null>(null);

  // A socket that slept missed the pushes, so the page asks again rather than show what it last heard.
  useResume(refresh);

  // Every poll pass pushes the whole set, so the page replaces rather than merges.
  useWebSocket(
    ["sensors"],
    useCallback(
      (topic: string, data: unknown) => {
        if (topic === "sensors") setItems((data as Sensor[]) || []);
      },
      [setItems],
    ),
  );

  // A double click on "yes" would otherwise send the delete twice.
  const removing = useRef(false);
  const remove = useCallback(
    async (s: Sensor) => {
      if (removing.current) return;
      removing.current = true;
      try {
        await deleteSensor(s.id);
        toast.success(`Removed ${s.name}`);
        setConfirmRemove(null);
        refresh();
      } catch (e) {
        toast.error(e instanceof Error ? e.message : "Failed to remove sensor");
      } finally {
        removing.current = false;
      }
    },
    [refresh],
  );

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow="Attached to this host"
        title="Sensors"
        meta={
          sensors ? (
            <span className="font-mono text-sm text-muted-foreground tabular-nums">
              {sensors.length} configured
            </span>
          ) : null
        }
        actions={
          <Button
            size="sm"
            onClick={() => {
              setEditing(null);
              setDialogOpen(true);
            }}
            className="font-mono text-xs uppercase tracking-widest"
          >
            <Plus className="size-3.5" />
            Add sensor
          </Button>
        }
      />

      {loading ? <SensorsSkeleton /> : null}
      {error ? <LoadErrorAlert message={error} onRetry={reload} /> : null}

      {!loading && !error && sensors ? (
        sensors.length === 0 ? (
          <section className="panel px-6 py-16 text-center">
            <CircleDashed className="size-8 mx-auto mb-3 text-muted-foreground/40" />
            <p className="font-mono text-sm uppercase tracking-widest text-muted-foreground">
              No sensors yet
            </p>
            <p className="mt-2 text-xs text-muted-foreground/70">
              Add one to read temperature, pressure or anything else this machine exposes.
            </p>
            <Button
              size="sm"
              onClick={() => {
                setEditing(null);
                setDialogOpen(true);
              }}
              className="mt-4 font-mono text-xs uppercase tracking-widest"
            >
              <Plus className="size-3.5" />
              Add sensor
            </Button>
          </section>
        ) : (
          <div className="space-y-4">
            {sensors.map((s) => (
              <SensorPanel
                key={s.id}
                sensor={s}
                all={sensors}
                kinds={kinds}
                confirming={confirmRemove === s.id}
                onEdit={() => {
                  setEditing(s);
                  setDialogOpen(true);
                }}
                onAskRemove={() => setConfirmRemove(s.id)}
                onCancel={() => setConfirmRemove(null)}
                onConfirm={() => remove(s)}
              />
            ))}
          </div>
        )
      ) : null}

      <SensorDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        editing={editing}
        sensors={sensors ?? []}
        kinds={kinds}
        kindsError={kindsError}
        onRetryKinds={reloadKinds}
        onSaved={() => {
          setDialogOpen(false);
          refresh();
        }}
      />
    </div>
  );
}

function SensorPanel({
  sensor,
  all,
  kinds,
  confirming,
  onEdit,
  onAskRemove,
  onCancel,
  onConfirm,
}: {
  sensor: Sensor;
  // all is every configured sensor, so a derived one can name what it reads rather than its ids.
  all: Sensor[];
  // kinds is the catalogue, which declares which options say where a sensor lives.
  kinds: SensorKind[] | null;
  confirming: boolean;
  onEdit: () => void;
  onAskRemove: () => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const failing = Boolean(sensor.error);
  // A derived sensor shows what it reads; everything else shows the options its kind identifies by.
  const kind = kinds?.find((k) => k.provider === sensor.provider && k.kind === sensor.kind);
  const where = useMemo(() => {
    if (sensor.bindings.length) {
      const names = sensor.bindings.map(
        (b) => all.find((s) => s.id === b.sensorId)?.name ?? `sensor ${b.sensorId}`,
      );
      return `reads ${[...new Set(names)].join(", ")}`;
    }
    const keys = kind?.fields.filter((f) => f.identifies).map((f) => f.key) ?? [];
    // Before the catalogue lands, every value beats none: the card still has to identify the part.
    const values = keys.length
      ? keys.map((k) => sensor.options[k]).filter(Boolean)
      : Object.values(sensor.options);
    return values.join(" ");
  }, [sensor.bindings, sensor.options, all, kind]);

  return (
    <article className="panel">
      <div className="flex items-center gap-3 px-4 py-3">
        <div className="min-w-0 flex-1">
          <h3 title={sensor.name} className="font-mono text-sm font-medium tracking-[0.06em] truncate">
            {sensor.name}
          </h3>
          {/* The part as it is printed on it; the raw ids only stand in until the catalogue lands. */}
          <code className="font-mono text-[11px] sm:text-[10px] text-muted-foreground wrap-break-word block">
            {kind ? kind.label : `${sensor.provider} / ${sensor.kind}`}
            {where ? ` ${where}` : ""}
          </code>
        </div>

        {/* Hidden while confirming, or the name column is crushed to nothing on a phone. */}
        {confirming ? null : (
          <>
            <ReadAge at={sensor.at} failing={failing} />
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={onEdit}
              aria-label={`Edit ${sensor.name}`}
              className="shrink-0 text-muted-foreground hover:text-foreground"
            >
              <Pencil className="size-3.5" />
            </Button>
          </>
        )}

        <div className="shrink-0">
          <InlineConfirm
            confirming={confirming}
            onAskRemove={onAskRemove}
            onCancel={onCancel}
            onConfirm={onConfirm}
            iconOnly
            ariaLabel={`Remove ${sensor.name}`}
          />
        </div>
      </div>

      {confirming ? (
        <p className="border-t border-border px-4 py-2 text-mono-xs text-muted-foreground">
          Removing it also takes it off every node&apos;s published channels.
        </p>
      ) : null}

      {sensor.readings.length > 0 ? (
        // A tile is never narrower than its value, so a long one wraps the row instead of running under the next.
        <div className="flex flex-wrap gap-px border-t border-border bg-border">
          {sensor.readings.map((r, i) => {
            const shown = displayReading(r);
            return (
              <div
                key={`${r.metric}-${r.label ?? i}`}
                className="bg-card grow basis-36 max-w-full px-4 py-4"
              >
                <span className="label-overline block">{r.label || r.metric}</span>
                <div className="mt-2 flex items-baseline gap-1.5">
                  <span
                    className={cn(
                      "min-w-0 wrap-break-word font-mono text-3xl font-semibold tabular-nums leading-none",
                      failing && "text-muted-foreground",
                    )}
                  >
                    {shown.value}
                  </span>
                  {shown.unit ? (
                    <span className="shrink-0 font-mono text-sm text-muted-foreground">
                      {shown.unit}
                    </span>
                  ) : null}
                </div>
              </div>
            );
          })}
        </div>
      ) : null}

      {/* The two absences read differently: never polled is normal, a failure is not. */}
      {sensor.readings.length === 0 && !failing ? (
        <p className="border-t border-border px-4 py-4 text-mono-xs text-muted-foreground/70">
          Waiting for the first read.
        </p>
      ) : null}

      {failing ? (
        <p className="flex items-start gap-2 border-t border-border bg-warning/5 px-4 py-3 font-mono text-[11px] sm:text-[10px] leading-relaxed text-warning">
          <TriangleAlert className="size-3.5 shrink-0 mt-px" strokeWidth={1.8} />
          <span className="min-w-0 wrap-break-word">{sensor.error}</span>
        </p>
      ) : null}
    </article>
  );
}

// The poll is 30 s (app.sensorPollInterval); a reading three polls old is one the mesh has stopped sending.
const STALE_SECS = 90;

// ReadAge is the one thing on the row that says the numbers can be trusted; ponytail: browser clock against the server's stamp, so skew reads as age.
function ReadAge({ at, failing }: { at: string | null; failing: boolean }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 5000);
    return () => clearInterval(id);
  }, []);
  const secs = at === null ? null : Math.max(0, Math.round((now - Date.parse(at)) / 1000));
  const stale = secs !== null && secs > STALE_SECS;
  return (
    <span
      title={stale ? "No new reading for three polls" : undefined}
      className={cn(
        "shrink-0 font-mono text-[10px] uppercase tracking-[0.12em] tabular-nums",
        failing || stale ? "text-warning" : "text-muted-foreground/70",
      )}
    >
      {secs === null
        ? "never read"
        : secs < 5
          ? "just now"
          : secs < 120
            ? `${secs}s ago`
            : `${timeAgo(at!)} ago`}
    </span>
  );
}

// displayReading reads a flag as yes or no and a count as a whole number, and scales ohms to kΩ or MΩ.
function displayReading(r: SensorReading): { value: string; unit?: string } {
  if (r.format === "flag") return { value: r.value ? "yes" : "no" };
  if (r.format === "count") return { value: String(Math.round(r.value)), unit: r.unit };
  if (r.unit === "Ω" && Math.abs(r.value) >= 1000) {
    const [scale, unit] = Math.abs(r.value) >= 1e6 ? [1e6, "MΩ"] : [1e3, "kΩ"];
    return { value: formatNumber(r.value / scale), unit };
  }
  return { value: formatNumber(r.value), unit: r.unit };
}

// Four significant figures, at least two decimals below 100: 0.120125 V and 0.120375 V stay apart, and 0.00004 is not 0.
function formatNumber(v: number): string {
  if (v === 0) return "0";
  const mag = Math.floor(Math.log10(Math.abs(v)));
  return v.toFixed(Math.min(6, Math.max(mag < 2 ? 2 : 0, 3 - mag)));
}

function SensorsSkeleton() {
  return (
    <div className="space-y-4">
      {[...Array(2)].map((_, i) => (
        <div key={i} className="panel">
          <div className="px-4 py-3 space-y-2">
            <Skeleton className="h-3.5 w-32" />
            <Skeleton className="h-2.5 w-48" />
          </div>
          <div className="flex gap-px border-t border-border bg-border">
            {[...Array(2)].map((_, j) => (
              <div key={j} className="bg-card flex-1 px-4 py-4 space-y-2">
                <Skeleton className="h-2.5 w-16" />
                <Skeleton className="h-7 w-24" />
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
