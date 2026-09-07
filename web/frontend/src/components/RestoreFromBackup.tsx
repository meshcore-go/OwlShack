import { useRef, useState } from "react";
import { AlertTriangle, Loader2, RotateCcw, Upload } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { apiErrorMessage } from "@/lib/apiError";

type Result = { kind: string; detail: string; restartRequired: boolean };

// Setup-only: applying a backup to an already-configured node is what breaks things.
export function RestoreFromBackup({ onRestored }: { onRestored: () => void }) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Result | null>(null);

  const restore = async () => {
    if (!file) return;
    setBusy(true);
    setResult(null);
    try {
      const body = new FormData();
      body.append("file", file);
      const r = await fetch("/api/backup/import", { method: "POST", body });
      if (!r.ok) throw new Error(await apiErrorMessage(r));
      const json = (await r.json()) as Result;
      setResult(json);
      setFile(null);
      if (fileRef.current) fileRef.current.value = "";
      if (json.restartRequired) {
        toast.success("Backup accepted — restart to finish");
      } else {
        toast.success("Settings imported");
        onRestored();
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Restore failed");
    } finally {
      setBusy(false);
    }
  };

  if (result) {
    return (
      <div className="space-y-4">
        <div
          className={
            result.restartRequired
              ? "border border-warning/40 bg-warning/5 p-4 space-y-2"
              : "border border-success/40 bg-success/5 p-4 space-y-2"
          }
        >
          <span className="label-overline block">
            {result.restartRequired ? "one more step" : "restored"}
          </span>
          <p className="font-mono text-xs leading-relaxed text-foreground">
            {result.detail}
          </p>
        </div>
        {result.restartRequired && (
          <p className="font-mono text-[11px] leading-relaxed text-muted-foreground">
            Stop OwlShack and start it again. Everything from the backup will be
            in place when it comes back — including your contacts and settings.
          </p>
        )}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <p className="font-mono text-sm leading-relaxed text-muted-foreground">
        Choose the backup file from your other node. It will be named something
        like <code className="text-foreground">owlshack-backup-2026-01-31.db</code>.
      </p>

      <input
        ref={fileRef}
        type="file"
        accept=".db,.json,.toml,.yaml,.yml"
        onChange={(e) => setFile(e.target.files?.[0] ?? null)}
        className="block w-full min-h-10 md:min-h-0 border border-border bg-background px-3 py-2 font-mono text-base md:text-xs file:mr-3 file:border-0 file:bg-muted file:px-2 file:py-1 file:font-mono file:text-xs file:uppercase file:tracking-widest file:text-foreground"
      />

      <p className="flex items-start gap-2 font-mono text-[11px] leading-relaxed text-muted-foreground/70">
        <AlertTriangle className="size-3.5 shrink-0 mt-px" />
        <span>
          An OwlShack config file (.toml, .json, .yaml) works here too, if you
          are coming from an older setup.
        </span>
      </p>

      <Button
        size="sm"
        onClick={restore}
        disabled={!file || busy}
        className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
      >
        {busy ? (
          <Loader2 className="size-3.5 animate-spin" />
        ) : (
          <Upload className="size-3.5" />
        )}
        restore this file
      </Button>
    </div>
  );
}

export function RestoreEntryButton({ onClick }: { onClick: () => void }) {
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={onClick}
      className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground"
    >
      <RotateCcw className="size-3.5" /> restore a backup
    </Button>
  );
}
