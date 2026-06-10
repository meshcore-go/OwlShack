export function SectionTitle({
  eyebrow,
  title,
  trailing,
}: {
  eyebrow?: string;
  title: string;
  trailing?: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between px-4 py-3 border-b border-border">
      <div className="space-y-0.5">
        {eyebrow && <span className="label-overline block">{eyebrow}</span>}
        <h2 className="font-mono text-sm uppercase tracking-[0.1em]">
          {title}
        </h2>
      </div>
      {trailing}
    </div>
  );
}
