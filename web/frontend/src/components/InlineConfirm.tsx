import { Check, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";

// Inline confirm-to-remove: a trash trigger that swaps to "Remove? yes/no" in
// place. The parent owns `confirming` so only one row confirms at a time.
export function InlineConfirm({
  confirming,
  onAskRemove,
  onCancel,
  onConfirm,
  triggerLabel = "remove",
  iconOnly = false,
  ariaLabel,
}: {
  confirming: boolean;
  onAskRemove: () => void;
  onCancel: () => void;
  onConfirm: () => void;
  triggerLabel?: string;
  iconOnly?: boolean;
  ariaLabel?: string;
}) {
  if (confirming) {
    return (
      <div className="inline-flex items-center gap-1">
        <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground mr-1">
          Remove?
        </span>
        <Button
          variant="destructive"
          size="xs"
          onClick={onConfirm}
          className="font-mono uppercase tracking-widest"
        >
          <Check className="size-3" /> yes
        </Button>
        <Button
          variant="ghost"
          size="xs"
          onClick={onCancel}
          className="font-mono uppercase tracking-widest"
        >
          <X className="size-3" /> no
        </Button>
      </div>
    );
  }
  if (iconOnly) {
    return (
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={onAskRemove}
        className="text-muted-foreground/60 hover:text-destructive"
        aria-label={ariaLabel ?? "Remove"}
      >
        <Trash2 className="size-3.5" />
      </Button>
    );
  }
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={onAskRemove}
      className="text-muted-foreground hover:text-destructive font-mono text-[10px] uppercase tracking-[0.12em]"
    >
      <Trash2 className="size-3" />
      {triggerLabel}
    </Button>
  );
}
