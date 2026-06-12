import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";

export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1">
      <Label className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
        {label}
      </Label>
      {children}
      {hint && (
        <p className="font-mono text-[10px] text-muted-foreground/60">{hint}</p>
      )}
    </div>
  );
}

export function TextField({
  label,
  value,
  onChange,
  placeholder,
  hint,
  type = "text",
  disabled,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  hint?: React.ReactNode;
  type?: string;
  disabled?: boolean;
}) {
  return (
    <Field label={label} hint={hint}>
      <Input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        disabled={disabled}
        className="h-9 font-mono text-sm rounded-none border-border bg-background"
      />
    </Field>
  );
}

export function SelectField({
  label,
  value,
  options,
  onChange,
  hint,
  disabled,
}: {
  label: string;
  value: string;
  options: { value: string; label: string }[];
  onChange: (v: string) => void;
  hint?: React.ReactNode;
  disabled?: boolean;
}) {
  // Tolerate a device/config value outside the curated set.
  const known = options.some((o) => o.value === value);
  return (
    <Field label={label} hint={hint}>
      <Select value={value} onValueChange={onChange} disabled={disabled}>
        <SelectTrigger className="h-9 w-full font-mono text-sm rounded-none border-border bg-background">
          <SelectValue />
        </SelectTrigger>
        <SelectContent className="rounded-sm">
          {!known && value !== "" && (
            <SelectItem value={value} className="font-mono text-sm">
              {value} (custom)
            </SelectItem>
          )}
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value} className="font-mono text-sm">
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  );
}

export function SwitchRow({
  label,
  hint,
  checked,
  onChange,
  disabled,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <label className="flex items-center justify-between gap-3 px-3 py-2 bg-card border border-border cursor-pointer">
      <div className="min-w-0">
        <div className="font-mono text-xs uppercase tracking-[0.08em]">
          {label}
        </div>
        {hint && (
          <div className="font-mono text-[10px] text-muted-foreground/60 truncate">
            {hint}
          </div>
        )}
      </div>
      <Switch checked={checked} onCheckedChange={onChange} disabled={disabled} />
    </label>
  );
}
