import { useMemo, useState } from "react";
import {
  Bot,
  CircleDashed,
  ExternalLink,
  HelpCircle,
  Loader2,
  Pencil,
  Plus,
  Save,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/PageHeader";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { SectionTitle } from "@/components/SectionTitle";
import { InlineConfirm } from "@/components/InlineConfirm";
import { ChannelMultiSelect } from "@/components/ChannelMultiSelect";
import { Field, SelectField, TextField } from "@/components/ConfigFields";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Textarea } from "@/components/ui/textarea";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useApiList } from "@/hooks/useApiList";
import {
  configApi,
  type ConfigChannel,
  type ConfigCompanion,
  type Trigger,
} from "@/lib/configApi";

const TYPE_OPTS = [
  { value: "group", label: "Group message (match & reply)" },
  { value: "cron", label: "Cron (scheduled broadcast)" },
];

// Practical regex examples for bot authors. Patterns use Go's RE2 engine.
const REGEX_EXAMPLES: { pattern: string; desc: string }[] = [
  { pattern: "(?i)^!bot$", desc: 'exactly "!bot", any case (not "!bottle")' },
  { pattern: "(?i)^ping", desc: 'starts with "ping" — "Ping", "ping me!"' },
  { pattern: "(?i)\\bweather\\b", desc: 'the whole word "weather" anywhere' },
  { pattern: "(?i)^(test|ping)$", desc: 'exactly "test" or "ping"' },
  { pattern: "(?i)help", desc: 'contains "help" anywhere in the message' },
  {
    pattern: "(?i)^!echo (?P<text>.+)",
    desc: "capture the rest as {{.Match.text}} for the reply",
  },
];

// Help popover explaining the regex match patterns with copyable examples and
// a link to regex101 (Go flavour) so users can test patterns against text.
function RegexHelp() {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="inline-flex items-center gap-1 font-mono text-[10px] uppercase tracking-widest text-muted-foreground hover:text-foreground"
        >
          <HelpCircle className="size-3.5" />
          examples
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        className="w-104 max-w-[calc(100vw-2rem)] rounded-none p-0"
      >
        <div className="border-b border-border px-3 py-2">
          <p className="font-mono text-[11px] uppercase tracking-widest text-foreground">
            Match pattern examples
          </p>
          <p className="mt-1 font-mono text-[10px] leading-relaxed text-muted-foreground/70">
            Patterns are regular expressions. They match anywhere in the message
            unless you anchor with ^ (start) and $ (end).
          </p>
        </div>
        <div className="divide-y divide-border">
          {REGEX_EXAMPLES.map((ex) => (
            <div key={ex.pattern} className="px-3 py-2">
              <code className="font-mono text-xs text-primary">{ex.pattern}</code>
              <p className="mt-0.5 font-mono text-[11px] text-muted-foreground">
                {ex.desc}
              </p>
            </div>
          ))}
        </div>
        <div className="space-y-1.5 border-t border-border px-3 py-2">
          <p className="font-mono text-[10px] leading-relaxed text-muted-foreground/70">
            (?i) ignore case · \b word boundary · .* any text · (a|b) a or b
          </p>
          <a
            href="https://regex101.com/?flavor=golang"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 font-mono text-[11px] text-primary underline-offset-2 hover:underline"
          >
            test a pattern on regex101
            <ExternalLink className="size-3" />
          </a>
        </div>
      </PopoverContent>
    </Popover>
  );
}

export function BotsPage() {
  const {
    items: triggers,
    loading,
    error,
    reload,
  } = useApiList<Trigger>("/api/config/triggers", "Failed to load bots");
  // companions + channels only seed the editor's selectors; a trigger write
  // never changes them, so they load once on mount and aren't refetched.
  const { items: companions } = useApiList<ConfigCompanion>(
    "/api/config/companions",
    "Failed to load companions",
  );
  const { items: channels } = useApiList<ConfigChannel>(
    "/api/config/channels",
    "Failed to load channels",
  );

  const [editing, setEditing] = useState<Trigger | "new" | null>(null);
  const [confirming, setConfirming] = useState<number | null>(null);

  const companionName = useMemo(() => {
    const m = new Map<number, string>();
    for (const c of companions ?? []) m.set(c.id, c.name);
    return m;
  }, [companions]);

  const channelName = useMemo(() => {
    const m = new Map<number, string>();
    for (const ch of channels ?? []) m.set(ch.id, ch.name);
    return m;
  }, [channels]);

  const ready = companions != null && channels != null;

  const removeBot = async (t: Trigger) => {
    setConfirming(null);
    try {
      await configApi.deleteTrigger(t.id);
      toast.success("Bot removed");
      reload();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to remove bot");
    }
  };

  const list = triggers ?? [];

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow="comms"
        title="Bots"
        meta={
          triggers && (
            <span className="font-mono text-sm text-muted-foreground tabular-nums">
              {list.length} configured
            </span>
          )
        }
        actions={
          <Button
            size="sm"
            onClick={() => setEditing("new")}
            disabled={loading || !ready}
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
      ) : triggers ? (
        <section className="panel overflow-hidden">
          <SectionTitle eyebrow="triggers" title="Configured bots" />
          {list.length === 0 ? (
            <div className="px-6 py-16 text-center space-y-3">
              <CircleDashed className="size-8 mx-auto text-muted-foreground/40" />
              <p className="font-mono text-xs uppercase tracking-[0.12em] text-muted-foreground">
                No bots configured
              </p>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {list.map((t) => {
                const chNames = (t.channelIds ?? [])
                  .map((id) => channelName.get(id))
                  .filter((n): n is string => !!n);
                return (
                  <div key={t.id} className="flex items-start gap-4 px-4 py-3">
                    <div className="size-9 grid place-items-center rounded-sm border border-primary/30 bg-primary/10 text-primary shrink-0">
                      <Bot className="size-4" strokeWidth={1.6} />
                    </div>
                    <div className="min-w-0 flex-1 space-y-1">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-mono text-[10px] uppercase tracking-widest px-1.5 py-0.5 border border-border bg-muted/40">
                          {t.type}
                        </span>
                        <span className="font-mono text-xs text-muted-foreground truncate">
                          {companionName.get(t.companionId) ?? `#${t.companionId}`}
                        </span>
                        {t.type === "cron" && t.schedule && (
                          <code className="font-mono text-xs text-info">
                            {t.schedule}
                          </code>
                        )}
                        {chNames.length > 0 && (
                          <span className="font-mono text-xs text-muted-foreground/70 truncate">
                            {chNames.join(", ")}
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
                        onClick={() => setEditing(t)}
                        aria-label="Edit bot"
                        className="text-muted-foreground/60 hover:text-foreground"
                      >
                        <Pencil className="size-3.5" />
                      </Button>
                      <InlineConfirm
                        iconOnly
                        confirming={confirming === t.id}
                        onAskRemove={() => setConfirming(t.id)}
                        onCancel={() => setConfirming(null)}
                        onConfirm={() => removeBot(t)}
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

      {editing !== null && ready && (
        <BotEditor
          trigger={editing === "new" ? null : editing}
          companions={companions}
          channels={channels}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      )}
    </div>
  );
}

function BotEditor({
  trigger,
  companions,
  channels,
  onClose,
  onSaved,
}: {
  trigger: Trigger | null;
  companions: ConfigCompanion[];
  channels: ConfigChannel[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const channelById = useMemo(() => {
    const m = new Map<number, ConfigChannel>();
    for (const ch of channels) m.set(ch.id, ch);
    return m;
  }, [channels]);

  const [companionId, setCompanionId] = useState(
    trigger?.companionId ?? companions[0]?.id ?? 0,
  );
  const [type, setType] = useState(
    trigger?.type === "channel" ? "group" : (trigger?.type ?? "group"),
  );
  const [template, setTemplate] = useState(trigger?.template ?? "");
  // Channels are tracked by name (unique per companion) so the picker stays
  // name-based; they are resolved back to channel ids on submit.
  const [selectedChannels, setSelectedChannels] = useState<string[]>(
    (trigger?.channelIds ?? [])
      .map((id) => channelById.get(id)?.name)
      .filter((n): n is string => !!n),
  );
  const [match, setMatch] = useState<string[]>(trigger?.match ?? []);
  const [schedule, setSchedule] = useState(trigger?.schedule ?? "");
  const [maxRetries, setMaxRetries] = useState(
    trigger?.maxRetries != null ? String(trigger.maxRetries) : "3",
  );
  const [retryTimeout, setRetryTimeout] = useState(
    trigger?.retryTimeout != null ? String(trigger.retryTimeout) : "5",
  );
  const [pathHashSize, setPathHashSize] = useState(
    trigger?.pathHashSize != null ? String(trigger.pathHashSize) : "default",
  );
  const [saving, setSaving] = useState(false);

  // A trigger can only target channels its companion already has (Public plus
  // any added on the Channels page). Names are unique within a companion.
  const companionChannels = useMemo(
    () => channels.filter((ch) => ch.companionId === companionId),
    [channels, companionId],
  );
  const channelOptions = companionChannels.map((ch) => ch.name);
  const nameToId = useMemo(() => {
    const m = new Map<string, number>();
    for (const ch of companionChannels) m.set(ch.name, ch.id);
    return m;
  }, [companionChannels]);

  // Switching companion invalidates the picked channels (different rows).
  const changeCompanion = (idStr: string) => {
    setCompanionId(parseInt(idStr, 10));
    setSelectedChannels([]);
  };

  const submit = async () => {
    const channelIds = selectedChannels
      .map((n) => nameToId.get(n))
      .filter((x): x is number => x != null);
    const patterns = match.map((s) => s.trim()).filter(Boolean);

    setSaving(true);
    try {
      await configApi.saveTrigger(
        {
          companionId,
          type,
          template,
          channelIds,
          match: type === "group" && patterns.length > 0 ? patterns : null,
          schedule: type === "cron" ? schedule : null,
          maxRetries: parseInt(maxRetries, 10) || 3,
          retryTimeout: parseInt(retryTimeout, 10) || 5,
          pathHashSize:
            pathHashSize === "default" ? null : parseInt(pathHashSize, 10),
        },
        trigger?.id,
      );
      toast.success(trigger ? "Bot saved" : "Bot added");
      onSaved();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to save bot");
    } finally {
      setSaving(false);
    }
  };

  const valid =
    template.trim() !== "" &&
    selectedChannels.length > 0 &&
    (type !== "cron" || schedule.trim() !== "");

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="rounded-none sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="font-mono text-sm uppercase tracking-widest">
            {trigger ? "Edit bot" : "Add bot"}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <SelectField
              label="Companion"
              value={String(companionId)}
              options={companions.map((c) => ({
                value: String(c.id),
                label: c.name,
              }))}
              onChange={changeCompanion}
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

          <ChannelMultiSelect
            label="Channels"
            selected={selectedChannels}
            options={channelOptions}
            onChange={setSelectedChannels}
            hint={
              type === "cron"
                ? "broadcast targets — pick from the companion's channels"
                : "channels to listen on — pick from the companion's channels"
            }
          />

          {type === "group" && (
            <div className="space-y-1">
              <div className="flex items-center justify-between gap-2">
                <Label className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
                  Match patterns
                </Label>
                <RegexHelp />
              </div>
              <div className="space-y-2">
                {match.length === 0 && (
                  <p className="font-mono text-xs text-muted-foreground/50">
                    no patterns — add one so this bot can fire
                  </p>
                )}
                {match.map((pat, i) => (
                  <div key={i} className="flex items-center gap-2">
                    <Input
                      value={pat}
                      onChange={(e) =>
                        setMatch((m) =>
                          m.map((p, j) => (j === i ? e.target.value : p)),
                        )
                      }
                      placeholder="(?i)^!bot"
                      className="h-9 flex-1 rounded-none border-border bg-background font-mono text-sm"
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      onClick={() =>
                        setMatch((m) => m.filter((_, j) => j !== i))
                      }
                      aria-label="Remove pattern"
                      className="shrink-0 rounded-none text-muted-foreground/60 hover:text-destructive"
                    >
                      <Trash2 className="size-3.5" />
                    </Button>
                  </div>
                ))}
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setMatch((m) => [...m, ""])}
                  className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
                >
                  <Plus className="size-3.5" />
                  add pattern
                </Button>
              </div>
              <p className="font-mono text-[10px] text-muted-foreground/60">
                regular expressions — the bot fires when a message matches any
                pattern.{" "}
                <a
                  href="https://regex101.com/?flavor=golang"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-primary underline-offset-2 hover:underline"
                >
                  test on regex101
                </a>
              </p>
            </div>
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
