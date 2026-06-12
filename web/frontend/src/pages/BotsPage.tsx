import { useMemo, useState } from "react";
import { Bot, CircleDashed, Loader2, Pencil, Plus, Save } from "lucide-react";
import { PageHeader } from "@/components/PageHeader";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { SectionTitle } from "@/components/SectionTitle";
import { InlineConfirm } from "@/components/InlineConfirm";
import { Field, SelectField, TextField } from "@/components/ConfigFields";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
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
  type ChannelRef,
  type TriggerConfig,
} from "@/hooks/useConfig";

interface BotRow {
  companion: string;
  compIdx: number;
  trigIdx: number;
  trigger: TriggerConfig;
}

const TYPE_OPTS = [
  { value: "group", label: "Group message (match & reply)" },
  { value: "cron", label: "Cron (scheduled broadcast)" },
];

export function BotsPage() {
  const { config, loading, error, saving, save, reload } = useConfig();
  const [editing, setEditing] = useState<BotRow | "new" | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);

  const rows = useMemo<BotRow[]>(() => {
    if (!config) return [];
    return config.companions.flatMap((comp, compIdx) =>
      (comp.triggers ?? []).map((trigger, trigIdx) => ({
        companion: comp.name,
        compIdx,
        trigIdx,
        trigger,
      })),
    );
  }, [config]);

  const removeBot = async (row: BotRow) => {
    if (!config) return;
    const next = structuredClone(config) as AppConfig;
    const triggers = next.companions[row.compIdx]?.triggers;
    if (!triggers) return;
    triggers.splice(row.trigIdx, 1);
    if (triggers.length === 0) next.companions[row.compIdx].triggers = null;
    setConfirming(null);
    await save(next);
  };

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow="comms"
        title="Bots"
        meta={
          config && (
            <span className="font-mono text-sm text-muted-foreground tabular-nums">
              {rows.length} configured
            </span>
          )
        }
        actions={
          <Button
            size="sm"
            onClick={() => setEditing("new")}
            disabled={loading || !config}
            className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
          >
            <Plus className="size-3.5" />
            add bot
          </Button>
        }
      />

      {error && <LoadErrorAlert message={error} onRetry={reload} />}

      {loading ? (
        <Skeleton className="h-48 w-full rounded-none" />
      ) : config ? (
        <section className="panel overflow-hidden">
          <SectionTitle eyebrow="triggers" title="Configured bots" />
          {rows.length === 0 ? (
            <div className="px-6 py-16 text-center space-y-3">
              <CircleDashed className="size-8 mx-auto text-muted-foreground/40" />
              <p className="font-mono text-xs uppercase tracking-[0.12em] text-muted-foreground">
                No bots configured
              </p>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {rows.map((row) => {
                const key = `${row.compIdx}:${row.trigIdx}`;
                const t = row.trigger;
                return (
                  <div key={key} className="flex items-start gap-4 px-4 py-3">
                    <div className="size-9 grid place-items-center rounded-sm border border-primary/30 bg-primary/10 text-primary shrink-0">
                      <Bot className="size-4" strokeWidth={1.6} />
                    </div>
                    <div className="min-w-0 flex-1 space-y-1">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-mono text-[10px] uppercase tracking-[0.1em] px-1.5 py-0.5 border border-border bg-muted/40">
                          {t.type}
                        </span>
                        <span className="font-mono text-xs text-muted-foreground truncate">
                          {row.companion}
                        </span>
                        {t.type === "cron" && t.schedule && (
                          <code className="font-mono text-xs text-info">
                            {t.schedule}
                          </code>
                        )}
                        {(t.channels ?? []).length > 0 && (
                          <span className="font-mono text-xs text-muted-foreground/70 truncate">
                            {(t.channels ?? []).map((c) => c.name).join(", ")}
                          </span>
                        )}
                      </div>
                      {t.match && t.match.length > 0 && (
                        <div className="font-mono text-xs text-muted-foreground/70 truncate">
                          match: {t.match.join("  ·  ")}
                        </div>
                      )}
                      <code className="font-mono text-xs text-foreground/80 block truncate">
                        {t.template}
                      </code>
                    </div>
                    <div className="flex items-center gap-1 shrink-0">
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => setEditing(row)}
                        aria-label="Edit bot"
                        className="text-muted-foreground/60 hover:text-foreground"
                      >
                        <Pencil className="size-3.5" />
                      </Button>
                      <InlineConfirm
                        iconOnly
                        confirming={confirming === key}
                        onAskRemove={() => setConfirming(key)}
                        onCancel={() => setConfirming(null)}
                        onConfirm={() => removeBot(row)}
                        ariaLabel="Remove bot"
                      />
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>
      ) : null}

      {editing && config && (
        <BotEditor
          config={config}
          row={editing === "new" ? null : editing}
          saving={saving}
          onClose={() => setEditing(null)}
          onSave={async (next) => {
            if (await save(next)) setEditing(null);
          }}
        />
      )}
    </div>
  );
}

function BotEditor({
  config,
  row,
  saving,
  onClose,
  onSave,
}: {
  config: AppConfig;
  row: BotRow | null;
  saving: boolean;
  onClose: () => void;
  onSave: (next: AppConfig) => Promise<void>;
}) {
  const t = row?.trigger;
  const [companion, setCompanion] = useState(
    row?.companion ?? config.companions[0]?.name ?? "",
  );
  const [type, setType] = useState(t?.type === "channel" ? "group" : (t?.type ?? "group"));
  const [template, setTemplate] = useState(t?.template ?? "");
  const [channels, setChannels] = useState(
    (t?.channels ?? []).map((c) => c.name).join(", "),
  );
  const [match, setMatch] = useState((t?.match ?? []).join("\n"));
  const [schedule, setSchedule] = useState(t?.schedule ?? "");
  const [maxRetries, setMaxRetries] = useState(
    t?.maxRetries != null ? String(t.maxRetries) : "3",
  );
  const [retryTimeout, setRetryTimeout] = useState(
    t?.retryTimeout != null ? String(t.retryTimeout) : "5",
  );
  const [pathHashSize, setPathHashSize] = useState(
    t?.pathHashSize != null ? String(t.pathHashSize) : "default",
  );

  const submit = async () => {
    const next = structuredClone(config) as AppConfig;
    const compIdx = next.companions.findIndex((c) => c.name === companion);
    if (compIdx < 0) return;

    // Reuse original ChannelRefs by name so a configured privateKey survives
    // a round-trip through the comma-separated names input.
    const origByName = new Map<string, ChannelRef>(
      (t?.channels ?? []).map((c) => [c.name, c]),
    );
    const channelRefs = channels
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean)
      .map((name) => origByName.get(name) ?? { name });

    const patterns = match
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);

    const built: TriggerConfig = {
      ...(t ?? {}),
      type,
      template,
      channels: channelRefs.length > 0 ? channelRefs : null,
      match: type === "group" && patterns.length > 0 ? patterns : null,
      schedule: type === "cron" ? schedule : undefined,
      maxRetries: parseInt(maxRetries, 10) || 3,
      retryTimeout: parseInt(retryTimeout, 10) || 5,
      pathHashSize:
        pathHashSize === "default" ? null : parseInt(pathHashSize, 10),
    };

    if (row) {
      const sameCompanion = row.compIdx === compIdx;
      if (sameCompanion) {
        (next.companions[compIdx].triggers ?? [])[row.trigIdx] = built;
      } else {
        // Moved to another companion: remove from the old, append to the new.
        const old = next.companions[row.compIdx].triggers ?? [];
        old.splice(row.trigIdx, 1);
        if (old.length === 0) next.companions[row.compIdx].triggers = null;
        next.companions[compIdx].triggers = [
          ...(next.companions[compIdx].triggers ?? []),
          built,
        ];
      }
    } else {
      next.companions[compIdx].triggers = [
        ...(next.companions[compIdx].triggers ?? []),
        built,
      ];
    }

    await onSave(next);
  };

  const valid =
    template.trim() !== "" &&
    channels.trim() !== "" &&
    (type !== "cron" || schedule.trim() !== "");

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="rounded-none sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="font-mono text-sm uppercase tracking-[0.1em]">
            {row ? "Edit bot" : "Add bot"}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <SelectField
              label="Companion"
              value={companion}
              options={config.companions.map((c) => ({
                value: c.name,
                label: c.name,
              }))}
              onChange={setCompanion}
            />
            <SelectField
              label="Type"
              value={type}
              options={TYPE_OPTS}
              onChange={setType}
            />
          </div>

          {type === "cron" && (
            <TextField
              label="Schedule"
              value={schedule}
              onChange={setSchedule}
              placeholder='"*/30 * * * *" or "@hourly"'
              hint="standard 5-field cron"
            />
          )}

          <TextField
            label="Channels"
            value={channels}
            onChange={setChannels}
            placeholder="#testing, Public"
            hint={
              type === "cron"
                ? "broadcast targets, comma-separated"
                : "channels to listen on, comma-separated"
            }
          />

          {type === "group" && (
            <Field
              label="Match patterns"
              hint="one regex per line — fires when a channel message matches"
            >
              <Textarea
                value={match}
                onChange={(e) => setMatch(e.target.value)}
                placeholder="(?i)^!bot"
                rows={2}
                className="resize-none rounded-none border-border font-mono text-sm bg-background"
              />
            </Field>
          )}

          <Field
            label="Reply template"
            hint="Go template — group: {{.Sender}} {{.Message}} {{.Match}} {{.SNR}} {{.Hops}}; cron: {{.Time}}"
          >
            <Textarea
              value={template}
              onChange={(e) => setTemplate(e.target.value)}
              placeholder="@[{{.Sender}}] pong"
              rows={3}
              className="resize-none rounded-none border-border font-mono text-sm bg-background"
            />
          </Field>

          <div className="grid grid-cols-3 gap-4">
            <TextField
              label="Max retries"
              value={maxRetries}
              onChange={setMaxRetries}
            />
            <TextField
              label="Retry timeout (s)"
              value={retryTimeout}
              onChange={setRetryTimeout}
            />
            <SelectField
              label="Path hash size"
              value={pathHashSize}
              options={[
                { value: "default", label: "default (1)" },
                { value: "0", label: "mirror incoming" },
                { value: "1", label: "1 byte" },
                { value: "2", label: "2 bytes" },
                { value: "4", label: "4 bytes" },
              ]}
              onChange={setPathHashSize}
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
