import { Link } from "react-router-dom";
import { Crosshair, Hash, MessagesSquare, Users } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { useApiList } from "@/hooks/useApiList";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { PageHeader } from "@/components/PageHeader";
import { truncateMid } from "@/lib/format";

interface Companion {
  name: string;
  pubkey: string;
  peerCount: number;
  channels?: { name: string }[];
}

export function CompanionsPage() {
  const {
    items: companions,
    loading,
    error,
    reload,
  } = useApiList<Companion>("/api/companions", "Failed to load companions");

  const total = companions?.length ?? 0;
  const totalChannels =
    companions?.reduce((s, c) => s + (c.channels?.length || 0), 0) ?? 0;
  const totalPeers =
    companions?.reduce((s, c) => s + (c.peerCount || 0), 0) ?? 0;

  return (
    <div className="space-y-4">
      <PageHeader
        title="Companions"
        meta={
          companions && (
            <span className="font-mono text-sm text-muted-foreground tabular-nums">
              {total} configured · {totalPeers} peers · {totalChannels} ch
            </span>
          )
        }
      />

      {loading && <CompanionsSkeleton />}

      {error && <LoadErrorAlert message={error} onRetry={reload} />}

      {!loading && !error && companions && (
        <section className="panel overflow-hidden">
          <div className="flex items-center justify-between px-4 py-3 border-b border-border">
            <div className="space-y-0.5">
              <span className="label-overline block">Roster</span>
              <h2 className="font-mono text-sm uppercase tracking-[0.1em]">
                Configured nodes
              </h2>
            </div>
          </div>

          {companions.length === 0 ? (
            <div className="px-6 py-16 text-center">
              <MessagesSquare className="size-8 mx-auto mb-3 text-muted-foreground/40" />
              <p className="font-mono text-sm uppercase tracking-[0.1em] text-muted-foreground">
                No companions configured
              </p>
              <p className="mt-2 text-xs text-muted-foreground/70">
                Define companions in your bot configuration to begin.
              </p>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {companions.map((c) => (
                <Link
                  key={c.name}
                  to={`/companions/${encodeURIComponent(c.name)}`}
                  className="group flex items-center gap-4 px-4 py-4 hover:bg-muted/40 transition-colors"
                >
                  <div className="size-10 grid place-items-center rounded-sm border border-primary/30 bg-primary/10 text-primary shrink-0">
                    <MessagesSquare
                      className="size-4"
                      strokeWidth={1.6}
                    />
                  </div>

                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex items-baseline gap-2">
                      <h3 className="font-mono text-sm font-semibold uppercase tracking-[0.08em] truncate">
                        {c.name}
                      </h3>
                    </div>
                    <code className="font-mono text-xs text-muted-foreground block truncate">
                      {c.pubkey ? truncateMid(c.pubkey, 8, 6) : "—"}
                    </code>
                  </div>

                  <div className="hidden sm:flex items-center gap-5 shrink-0">
                    <Stat
                      icon={<Users className="size-3" strokeWidth={1.6} />}
                      label="peers"
                      value={c.peerCount.toString()}
                    />
                    <Stat
                      icon={<Hash className="size-3" strokeWidth={1.6} />}
                      label="ch"
                      value={(c.channels?.length || 0).toString()}
                    />
                  </div>

                  <Crosshair className="size-3.5 text-muted-foreground/40 group-hover:text-primary transition-colors shrink-0" />
                </Link>
              ))}
            </div>
          )}
        </section>
      )}
    </div>
  );
}

function Stat({
  icon,
  label,
  value,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
}) {
  return (
    <div className="flex flex-col items-end gap-0.5">
      <span className="font-mono text-sm font-semibold tabular-nums leading-none">
        {value}
      </span>
      <span className="inline-flex items-center gap-1 font-mono text-[9px] uppercase tracking-[0.14em] text-muted-foreground/70">
        {icon}
        {label}
      </span>
    </div>
  );
}

function CompanionsSkeleton() {
  return (
    <div className="panel">
      <div className="px-4 py-3 border-b border-border">
        <Skeleton className="h-3 w-24 mb-2" />
        <Skeleton className="h-4 w-40" />
      </div>
      <div className="divide-y divide-border">
        {[...Array(3)].map((_, i) => (
          <div key={i} className="flex items-center gap-4 px-4 py-4">
            <Skeleton className="size-10" />
            <div className="flex-1 space-y-2">
              <Skeleton className="h-4 w-32" />
              <Skeleton className="h-3 w-48" />
            </div>
            <Skeleton className="h-8 w-20" />
          </div>
        ))}
      </div>
    </div>
  );
}
