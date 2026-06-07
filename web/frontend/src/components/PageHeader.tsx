import { cn } from "@/lib/utils";

interface PageHeaderProps {
  eyebrow?: string;
  title: string;
  meta?: React.ReactNode;
  trailing?: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
}

export function PageHeader({
  eyebrow,
  title,
  meta,
  trailing,
  actions,
  className,
}: PageHeaderProps) {
  return (
    <div
      className={cn(
        "flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between border-b border-border pb-3 mb-4",
        className,
      )}
    >
      <div className="space-y-0.5 flex-1 min-w-0">
        {eyebrow && (
          <span className="label-overline block">{eyebrow}</span>
        )}
        <div className="flex items-baseline gap-3">
          <h1 className="font-mono text-lg font-semibold tracking-tight uppercase">
            {title}
          </h1>
          {meta}
          {trailing && (
            <span className="ml-auto">{trailing}</span>
          )}
        </div>
      </div>
      {actions && (
        <div className="flex items-center gap-2 shrink-0">{actions}</div>
      )}
    </div>
  );
}

interface PageMetaProps {
  label: string;
  value: React.ReactNode;
  className?: string;
}

export function PageMeta({ label, value, className }: PageMetaProps) {
  return (
    <div className={cn("flex items-baseline gap-1.5", className)}>
      <span className="label-overline">{label}</span>
      <span className="font-mono text-sm tabular-nums">{value}</span>
    </div>
  );
}
