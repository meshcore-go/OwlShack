import { useState } from "react";
import { HardDriveDownload, Info } from "lucide-react";
import { SectionTitle } from "@/components/SectionTitle";
import { BackupWizard } from "@/components/BackupWizard";
import { Button } from "@/components/ui/button";

// BackupPanel is export only. Restoring is offered on the first-run screen:
// applying a backup to a node that is already configured and on the air is
// what breaks — identities collide and live sessions dangle.
export function BackupPanel() {
  const [open, setOpen] = useState(false);

  return (
    <section className="panel">
      <SectionTitle eyebrow="move or copy this node" title="Backup" />
      <div className="p-4 space-y-4">
        <p className="font-mono text-xs leading-relaxed text-muted-foreground">
          Save this node's setup to a file so you can move it to new hardware or
          keep a copy. You choose what goes in — settings, contacts, and
          optionally the chat and packet history.
        </p>

        <Button
          onClick={() => setOpen(true)}
          className="font-mono uppercase tracking-widest"
        >
          <HardDriveDownload className="size-4" />
          create a backup
        </Button>

        <p className="flex items-start gap-2 font-mono text-[11px] leading-relaxed text-muted-foreground/70">
          <Info className="size-3.5 shrink-0 mt-px" />
          <span>
            To restore one, run OwlShack on the new machine and pick “Restore a
            backup” on its first-run screen. A backup can't be applied to a node
            that is already set up.
          </span>
        </p>
      </div>

      <BackupWizard open={open} onOpenChange={setOpen} />
    </section>
  );
}
