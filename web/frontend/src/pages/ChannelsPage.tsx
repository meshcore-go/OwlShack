import { useCallback, useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { CircleDashed, Hash, Lock, Plus, Radio } from "lucide-react";
import { toast } from "sonner";
import { BackLink } from "@/components/BackLink";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useApiList } from "@/hooks/useApiList";
import { InlineConfirm } from "@/components/InlineConfirm";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { PageHeader } from "@/components/PageHeader";

interface Channel {
  name: string;
}

const HEX32 = /^[0-9a-fA-F]{32}$/;

export function ChannelsPage() {
  const { name } = useParams();
  const companion = decodeURIComponent(name ?? "");

  const {
    items: channels,
    loading,
    error,
    reload: load,
  } = useApiList<Channel>(
    companion
      ? `/api/companions/${encodeURIComponent(companion)}/channels`
      : null,
    "Failed to load channels",
  );
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);

  const addChannel = useCallback(
    async (channelName: string, privateKey?: string) => {
      try {
        const body: { name: string; privateKey?: string } = {
          name: channelName,
        };
        if (privateKey) body.privateKey = privateKey;
        const res = await fetch(
          `/api/companions/${encodeURIComponent(companion)}/channels`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          },
        );
        if (!res.ok) {
          const err = await res.json().catch(() => ({}));
          throw new Error(err.error || `HTTP ${res.status}`);
        }
        toast.success(`Channel "${channelName}" added`);
        setDialogOpen(false);
        load();
      } catch (e) {
        const msg = e instanceof Error ? e.message : "failed";
        toast.error(`Failed to add channel: ${msg}`);
      }
    },
    [companion, load],
  );

  const removeChannel = useCallback(
    async (channelName: string) => {
      try {
        const res = await fetch(
          `/api/companions/${encodeURIComponent(
            companion,
          )}/channels/${encodeURIComponent(channelName)}`,
          { method: "DELETE" },
        );
        if (!res.ok) {
          const err = await res.json().catch(() => ({}));
          throw new Error(err.error || `HTTP ${res.status}`);
        }
        toast.success(`Channel "${channelName}" removed`);
        setConfirmRemove(null);
        load();
      } catch (e) {
        const msg = e instanceof Error ? e.message : "failed";
        toast.error(`Failed to remove channel: ${msg}`);
      }
    },
    [companion, load],
  );

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3">
        <BackLink
          to={`/companions/${encodeURIComponent(companion)}`}
          label={companion || "companion"}
        />

        <PageHeader
          title="Channels"
          meta={
            channels && (
              <span className="font-mono text-sm text-muted-foreground tabular-nums">
                {channels.length} configured
              </span>
            )
          }
          actions={
            <Button
              variant="default"
              size="sm"
              onClick={() => setDialogOpen(true)}
              className="font-mono text-xs uppercase tracking-widest"
            >
              <Plus className="size-3.5" />
              Add channel
            </Button>
          }
          className="mb-0"
        />
      </div>

      {loading && <ChannelsSkeleton />}

      {error && <LoadErrorAlert message={error} onRetry={load} />}

      {!loading && !error && channels && (
        <section className="panel overflow-hidden">
          <div className="flex items-center justify-between px-4 py-3 border-b border-border">
            <div className="space-y-0.5">
              <span className="label-overline block">Frequencies</span>
              <h2 className="font-mono text-sm uppercase tracking-widest">
                Subscribed channels
              </h2>
            </div>
            <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground/70 tabular-nums">
              {channels.length}
            </span>
          </div>

          {channels.length === 0 ? (
            <div className="px-6 py-16 text-center">
              <Radio className="size-8 mx-auto mb-3 text-muted-foreground/40" />
              <p className="font-mono text-sm uppercase tracking-widest text-muted-foreground">
                No channels yet
              </p>
              <p className="mt-2 text-xs text-muted-foreground/70">
                Subscribe to public or private channels to receive traffic.
              </p>
              <Button
                variant="default"
                size="sm"
                onClick={() => setDialogOpen(true)}
                className="mt-4 font-mono text-xs uppercase tracking-widest"
              >
                <Plus className="size-3.5" />
                Add channel
              </Button>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {channels.map((ch) => (
                <ChannelRow
                  key={ch.name}
                  name={ch.name}
                  confirming={confirmRemove === ch.name}
                  onAskRemove={() => setConfirmRemove(ch.name)}
                  onCancel={() => setConfirmRemove(null)}
                  onConfirm={() => removeChannel(ch.name)}
                />
              ))}
            </div>
          )}
        </section>
      )}

      <AddChannelDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        existing={channels || []}
        onAdd={addChannel}
      />
    </div>
  );
}

function ChannelRow({
  name,
  confirming,
  onAskRemove,
  onCancel,
  onConfirm,
}: {
  name: string;
  confirming: boolean;
  onAskRemove: () => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div className="flex items-center gap-3 px-4 py-3 hover:bg-muted/40 transition-colors">
      <div className="size-9 grid place-items-center rounded-sm border border-border bg-muted/40 text-muted-foreground shrink-0">
        <Hash className="size-4" strokeWidth={1.6} />
      </div>

      <div className="min-w-0 flex-1">
        <span className="font-mono text-sm font-medium tracking-[0.06em] truncate block">
          {name}
        </span>
        <span className="text-mono-xs text-muted-foreground">channel</span>
      </div>

      <div className="shrink-0">
        <InlineConfirm
          confirming={confirming}
          onAskRemove={onAskRemove}
          onCancel={onCancel}
          onConfirm={onConfirm}
        />
      </div>
    </div>
  );
}

type ChannelMode = "public" | "private";

function AddChannelDialog({
  open,
  onOpenChange,
  existing,
  onAdd,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  existing: Channel[];
  onAdd: (name: string, privateKey?: string) => Promise<void>;
}) {
  const [mode, setMode] = useState<ChannelMode>("public");
  const [channelName, setChannelName] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!open) {
      setMode("public");
      setChannelName("");
      setPrivateKey("");
      setSubmitting(false);
    }
  }, [open]);

  const trimmedName = channelName.trim();
  const trimmedKey = privateKey.trim();
  // Channel names are case-sensitive (the key derives from the exact name), so
  // "#Foo" and "#foo" are distinct channels — match exactly, like the backend.
  const nameTaken = existing.some((c) => c.name === trimmedName);
  const nameValid = trimmedName.length > 0 && !nameTaken;
  const keyValid = mode === "public" || HEX32.test(trimmedKey);

  const canSubmit = nameValid && keyValid && !submitting;

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    if (mode === "private") {
      await onAdd(trimmedName, trimmedKey);
    } else {
      await onAdd(trimmedName);
    }
    setSubmitting(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="rounded-none border-border bg-card max-w-md">
        <DialogHeader>
          <DialogTitle className="font-mono uppercase tracking-[0.08em] text-sm">
            Add channel
          </DialogTitle>
          <DialogDescription className="text-xs text-muted-foreground">
            Subscribe this companion to a public or private channel.
          </DialogDescription>
        </DialogHeader>

        <Tabs
          value={mode}
          onValueChange={(v) => setMode(v as ChannelMode)}
          className="gap-3"
        >
          <TabsList className="rounded-none bg-muted h-9 grid grid-cols-2 w-full">
            <TabsTrigger
              value="public"
              className="rounded-none font-mono text-[11px] uppercase tracking-widest gap-1.5"
            >
              <Hash className="size-3" />
              public
            </TabsTrigger>
            <TabsTrigger
              value="private"
              className="rounded-none font-mono text-[11px] uppercase tracking-widest gap-1.5"
            >
              <Lock className="size-3" />
              private
            </TabsTrigger>
          </TabsList>

          <TabsContent value="public" className="mt-0 space-y-3">
            <ChannelNameField
              value={channelName}
              onChange={setChannelName}
              taken={nameTaken && trimmedName.length > 0}
            />
            <p className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground/70">
              Public channels use the standard mesh broadcast key.
            </p>
          </TabsContent>

          <TabsContent value="private" className="mt-0 space-y-3">
            <ChannelNameField
              value={channelName}
              onChange={setChannelName}
              taken={nameTaken && trimmedName.length > 0}
            />
            <div className="space-y-1.5">
              <Label
                htmlFor="channel-key"
                className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground"
              >
                Private key
              </Label>
              <Input
                id="channel-key"
                value={privateKey}
                onChange={(e) => setPrivateKey(e.target.value)}
                placeholder="32-character hex…"
                spellCheck={false}
                autoCorrect="off"
                autoCapitalize="off"
                aria-invalid={trimmedKey.length > 0 && !HEX32.test(trimmedKey)}
                className="rounded-none font-mono text-xs h-8"
              />
              {trimmedKey.length > 0 && !HEX32.test(trimmedKey) && (
                <p className="text-[10px] text-destructive font-mono">
                  must be 32 hex characters
                </p>
              )}
              {trimmedKey.length === 0 && (
                <p className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground/70">
                  Required for encrypted channels.
                </p>
              )}
            </div>
          </TabsContent>
        </Tabs>

        <DialogFooter className="mt-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onOpenChange(false)}
            className="font-mono uppercase tracking-widest"
          >
            cancel
          </Button>
          <Button
            variant="default"
            size="sm"
            disabled={!canSubmit}
            onClick={handleSubmit}
            className="font-mono uppercase tracking-widest"
          >
            {submitting ? (
              <>
                <CircleDashed className="size-3 animate-spin" />
                adding…
              </>
            ) : (
              <>
                <Plus className="size-3" />
                add channel
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ChannelNameField({
  value,
  onChange,
  taken,
}: {
  value: string;
  onChange: (v: string) => void;
  taken: boolean;
}) {
  return (
    <div className="space-y-1.5">
      <Label
        htmlFor="channel-name"
        className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground"
      >
        Channel name
      </Label>
      <Input
        id="channel-name"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="general"
        autoCapitalize="off"
        spellCheck={false}
        aria-invalid={taken}
        className="rounded-none font-mono text-xs h-8"
      />
      {taken && (
        <p className="text-[10px] text-destructive font-mono">
          channel already configured
        </p>
      )}
    </div>
  );
}

function ChannelsSkeleton() {
  return (
    <div className="panel">
      <div className="px-4 py-3 border-b border-border">
        <Skeleton className="h-3 w-24 mb-2" />
        <Skeleton className="h-4 w-40" />
      </div>
      <div className="divide-y divide-border">
        {[...Array(3)].map((_, i) => (
          <div key={i} className="flex items-center gap-3 px-4 py-3">
            <Skeleton className="size-9" />
            <div className="flex-1 space-y-2">
              <Skeleton className="h-3 w-24" />
              <Skeleton className="h-3 w-12" />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
