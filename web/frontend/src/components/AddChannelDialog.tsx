import { useEffect, useState } from "react";
import { CircleDashed, Hash, Lock, Plus } from "lucide-react";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

export interface Channel {
  name: string;
}

const HEX32 = /^[0-9a-fA-F]{32}$/;

type ChannelMode = "public" | "private";

/**
 * Subscribe a companion to a public or private channel. Presentational: the
 * caller supplies `onAdd` (the POST) and the existing channels for the
 * duplicate-name guard. `prefillName` seeds the name field when the dialog
 * opens (used by the #hashtag chip in chat, which can only know the name).
 */
export function AddChannelDialog({
  open,
  onOpenChange,
  existing,
  onAdd,
  prefillName,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  existing: Channel[];
  onAdd: (name: string, privateKey?: string) => Promise<void>;
  prefillName?: string;
}) {
  const [mode, setMode] = useState<ChannelMode>("public");
  const [channelName, setChannelName] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (open) {
      setMode("public");
      setChannelName(prefillName ?? "");
      setPrivateKey("");
      setSubmitting(false);
    }
  }, [open, prefillName]);

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
