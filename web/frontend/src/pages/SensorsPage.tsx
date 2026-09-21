import { useCallback, useState } from "react";
import { CircleDashed, Cpu, Plus, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { InlineConfirm } from "@/components/InlineConfirm";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { PageHeader } from "@/components/PageHeader";
import { AddSensorDialog } from "@/components/AddSensorDialog";
import { useApiList } from "@/hooks/useApiList";
import { useWebSocket } from "@/hooks/useWebSocket";
import { timeAgo } from "@/lib/format";
import { deleteSensor, type Sensor } from "@/lib/sensorsApi";

export function SensorsPage() {
  const {
    items: sensors,
    loading,
    error,
    reload,
    setItems,
  } = useApiList<Sensor>("/api/sensors", "Failed to load sensors");
  const [confirmRemove, setConfirmRemove] = useState<number | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);

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

  const remove = useCallback(
    async (s: Sensor) => {
      try {
        await deleteSensor(s.id);
        toast.success(`Removed ${s.name}`);
        setConfirmRemove(null);
        reload();
      } catch (e) {
        toast.error(e instanceof Error ? e.message : "Failed to remove sensor");
      }
    },
    [reload],
  );

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow="Instrumentation"
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
            onClick={() => setDialogOpen(true)}
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
        <section className="panel overflow-hidden">
          <div className="flex items-center justify-between px-4 py-3 border-b border-border">
            <div className="space-y-0.5">
              <span className="label-overline block">Attached to this host</span>
              <h2 className="font-mono text-sm uppercase tracking-widest">
                Readings
              </h2>
            </div>
            <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground/70 tabular-nums">
              {sensors.length}
            </span>
          </div>

          {sensors.length === 0 ? (
            <div className="px-6 py-16 text-center">
              <CircleDashed className="size-8 mx-auto mb-3 text-muted-foreground/40" />
              <p className="font-mono text-sm uppercase tracking-widest text-muted-foreground">
                No sensors yet
              </p>
              <p className="mt-2 text-xs text-muted-foreground/70">
                Add one to read temperature, load or anything else this machine exposes.
              </p>
              <Button
                size="sm"
                onClick={() => setDialogOpen(true)}
                className="mt-4 font-mono text-xs uppercase tracking-widest"
              >
                <Plus className="size-3.5" />
                Add sensor
              </Button>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {sensors.map((s) => (
                <SensorRow
                  key={s.id}
                  sensor={s}
                  confirming={confirmRemove === s.id}
                  onAskRemove={() => setConfirmRemove(s.id)}
                  onCancel={() => setConfirmRemove(null)}
                  onConfirm={() => remove(s)}
                />
              ))}
            </div>
          )}
        </section>
      ) : null}

      <AddSensorDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onAdded={() => {
          setDialogOpen(false);
          reload();
        }}
      />
    </div>
  );
}

function SensorRow({
  sensor,
  confirming,
  onAskRemove,
  onCancel,
  onConfirm,
}: {
  sensor: Sensor;
  confirming: boolean;
  onAskRemove: () => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const failing = Boolean(sensor.error);
  return (
    <div className="px-4 py-3 hover:bg-muted/40 transition-colors">
      <div className="flex items-center gap-3">
        <div className="size-9 grid place-items-center rounded-sm border border-border bg-muted/40 text-muted-foreground shrink-0">
          <Cpu className="size-4" strokeWidth={1.6} />
        </div>

        <div className="min-w-0 flex-1">
          <span className="font-mono text-sm font-medium tracking-[0.06em] truncate block">
            {sensor.name}
          </span>
          <code className="text-mono-xs text-muted-foreground truncate block">
            {sensor.provider} / {sensor.kind}
          </code>
        </div>

        <span className="shrink-0 font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground/70 tabular-nums">
          {sensor.at ? timeAgo(sensor.at) : "no reading"}
        </span>

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

      <div className="mt-2 pl-12 space-y-2">
        {sensor.readings.length > 0 ? (
          <div className="flex flex-wrap gap-px bg-border border border-border w-fit max-w-full">
            {sensor.readings.map((r, i) => (
              <div
                key={`${r.metric}-${r.label ?? i}`}
                className="bg-card px-3 py-1.5 min-w-20"
              >
                <span className="label-overline block text-muted-foreground/70">
                  {r.label || r.metric}
                </span>
                <span
                  className={`font-mono text-sm tabular-nums ${
                    failing ? "text-muted-foreground" : ""
                  }`}
                >
                  {formatValue(r.value)}
                  {r.unit ? (
                    <span className="text-muted-foreground"> {r.unit}</span>
                  ) : null}
                </span>
              </div>
            ))}
          </div>
        ) : null}

        {/* The two absences read differently: never polled is normal, a failure is not. */}
        {sensor.readings.length === 0 && !failing ? (
          <p className="text-mono-xs text-muted-foreground/70">
            Waiting for the first read.
          </p>
        ) : null}

        {failing ? (
          <p className="flex items-start gap-1.5 font-mono text-[11px] sm:text-[10px] leading-relaxed text-warning">
            <TriangleAlert className="size-3.5 shrink-0 mt-px" strokeWidth={1.8} />
            <span className="min-w-0 break-words">
              {sensor.error}
              {sensor.at ? ` (values above last read ${timeAgo(sensor.at)} ago)` : null}
            </span>
          </p>
        ) : null}
      </div>
    </div>
  );
}

// Sensor values are decimal quantities, not counts: three decimals keeps a millidegree reading
// honest without turning a load average into noise.
function formatValue(v: number): string {
  if (Number.isInteger(v)) return String(v);
  return v.toFixed(Math.abs(v) < 10 ? 2 : 1);
}

function SensorsSkeleton() {
  return (
    <div className="panel">
      <div className="px-4 py-3 border-b border-border">
        <Skeleton className="h-3 w-32 mb-2" />
        <Skeleton className="h-4 w-24" />
      </div>
      <div className="divide-y divide-border">
        {[...Array(2)].map((_, i) => (
          <div key={i} className="px-4 py-3 space-y-3">
            <div className="flex items-center gap-3">
              <Skeleton className="size-9" />
              <div className="flex-1 space-y-2">
                <Skeleton className="h-3 w-28" />
                <Skeleton className="h-3 w-20" />
              </div>
            </div>
            <Skeleton className="h-10 w-40 ml-12" />
          </div>
        ))}
      </div>
    </div>
  );
}
