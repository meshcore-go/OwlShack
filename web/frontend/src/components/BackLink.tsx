import { Link } from "react-router-dom";
import { ArrowLeft } from "lucide-react";

// The "back to X" eyebrow link above a detail page's header.
export function BackLink({ to, label }: { to: string; label: string }) {
  return (
    <Link
      to={to}
      className="inline-flex w-fit items-center gap-1.5 py-3.5 -my-3.5 sm:py-0 sm:my-0 font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground transition-colors hover:text-primary"
    >
      <ArrowLeft className="size-3" /> {label}
    </Link>
  );
}
