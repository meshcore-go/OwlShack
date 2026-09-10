import type { ReactNode } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export function StringListField({
  label,
  values,
  onChange,
  placeholder,
  addLabel,
  emptyHint,
  hint,
  action,
}: {
  label: string;
  values: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
  addLabel: string;
  emptyHint?: string;
  hint?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between gap-2">
        <Label className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
          {label}
        </Label>
        {action}
      </div>
      <div className="space-y-2">
        {values.length === 0 && emptyHint && (
          <p className="font-mono text-xs text-muted-foreground/50">
            {emptyHint}
          </p>
        )}
        {values.map((value, i) => (
          <div key={i} className="flex items-center gap-2">
            <Input
              value={value}
              onChange={(e) =>
                onChange(values.map((v, j) => (j === i ? e.target.value : v)))
              }
              placeholder={placeholder}
              className="h-9 flex-1 rounded-none border-border bg-background font-mono text-sm"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={() => onChange(values.filter((_, j) => j !== i))}
              aria-label={`Remove ${label.toLowerCase()} entry`}
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
          onClick={() => onChange([...values, ""])}
          className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
        >
          <Plus className="size-3.5" />
          {addLabel}
        </Button>
      </div>
      {hint && (
        <p className="font-mono text-[10px] text-muted-foreground/60">{hint}</p>
      )}
    </div>
  );
}
