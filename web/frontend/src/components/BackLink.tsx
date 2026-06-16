import { Link } from "react-router-dom";
import { ArrowLeft } from "lucide-react";

// The "‹ back to X" link that sits above a detail page's header. Standardises
// the eyebrow styling that was copy-pasted atop Contacts/Channels/Repeaters/
// Monitoring detail pages.
export function BackLink({ to, label }: { to: string; label: string }) {
  return (
    <Link
      to={to}
      className="inline-flex w-fit items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground transition-colors hover:text-primary"
    >
      <ArrowLeft className="size-3" /> {label}
    </Link>
  );
}
