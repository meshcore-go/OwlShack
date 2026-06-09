import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft, Check, Copy, MessageSquare } from "lucide-react";
import { toast } from "sonner";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { PeerAvatar } from "@/components/PeerAvatar";
import { PeerTypePill } from "@/components/StatusIndicator";
import { TelemetryPanel } from "@/components/TelemetryPanel";
import { timeAgo, truncateMid } from "@/lib/format";

interface Contact {
  peerPubkey: string;
  name: string;
  type?: string;
  addedAt: string;
}

interface PathInfo {
  outPath: string;
  hops: number;
  hasPath: boolean;
  directNeighbor: boolean;
  pathHashSize: number;
}

export function ContactDetailPage() {
  const { name, pubkey } = useParams();
  const companion = decodeURIComponent(name ?? "");
  const contactPubkey = decodeURIComponent(pubkey ?? "");

  const apiBase = useMemo(
    () =>
      `/api/companions/${encodeURIComponent(companion)}/contacts/${encodeURIComponent(contactPubkey)}`,
    [companion, contactPubkey],
  );

  const [contact, setContact] = useState<Contact | null>(null);
  const [path, setPath] = useState<PathInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!companion || !contactPubkey) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetch(`/api/companions/${encodeURIComponent(companion)}/contacts`)
      .then((r) => {
        if (!r.ok) throw new Error("contacts");
        return r.json();
      })
      .then((cs: Contact[]) => {
        if (cancelled) return;
        const found = (cs || []).find((c) => c.peerPubkey === contactPubkey);
        if (!found) throw new Error("Contact not found");
        setContact(found);
      })
      .catch((e) => {
        if (!cancelled)
          setError(e instanceof Error ? e.message : "Failed to load contact");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [companion, contactPubkey]);

  // Path is best-effort context; its absence is not a page error.
  useEffect(() => {
    let cancelled = false;
    fetch(`${apiBase}/path`)
      .then((r) => (r.ok ? r.json() : null))
      .then((p: PathInfo | null) => {
        if (!cancelled) setPath(p);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [apiBase]);

  const copyKey = useCallback(() => {
    navigator.clipboard.writeText(contactPubkey).then(() => {
      setCopied(true);
      toast.success("Public key copied");
      window.setTimeout(() => setCopied(false), 1500);
    });
  }, [contactPubkey]);

  const displayName = contact?.name || "unknown peer";
  const routeLabel =
    !path || !path.hasPath
      ? "Flood"
      : path.directNeighbor
        ? "Direct"
        : `${path.hops} hop${path.hops === 1 ? "" : "s"}`;

  return (
    <div className="space-y-8">
      <div className="flex flex-col gap-3">
        <Link
          to={`/companions/${encodeURIComponent(companion)}/contacts`}
          className="inline-flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground hover:text-primary transition-colors w-fit"
        >
          <ArrowLeft className="size-3" />
          Contacts
        </Link>

        <div className="flex items-start gap-4">
          <PeerAvatar name={displayName} size="lg" />
          <div className="min-w-0 space-y-1">
            <div className="flex items-center gap-2">
              <h1 className="text-lg font-semibold truncate">{displayName}</h1>
              {contact?.type && <PeerTypePill type={contact.type} />}
            </div>
            <code className="font-mono text-xs text-muted-foreground break-all">
              {truncateMid(contactPubkey, 10, 8)}
            </code>
          </div>
          <div className="ml-auto shrink-0">
            <Button
              asChild
              variant="default"
              size="sm"
              className="font-mono text-xs uppercase tracking-[0.1em]"
            >
              <Link
                to={`/companions/${encodeURIComponent(companion)}?channel=${encodeURIComponent(`dm:${contactPubkey}`)}`}
              >
                <MessageSquare className="size-3.5" />
                Message
              </Link>
            </Button>
          </div>
        </div>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertTitle className="font-mono uppercase tracking-[0.1em]">
            Error
          </AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {loading && (
        <div className="space-y-2">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      )}

      {!loading && !error && contact && (
        <>
          <section className="panel overflow-hidden">
            <PanelHeader eyebrow="Contact" title="Identity" />
            <div className="divide-y divide-border">
              <InfoRow label="Public key">
                <button
                  type="button"
                  onClick={copyKey}
                  className="inline-flex items-center gap-1.5 font-mono text-xs hover:text-primary transition-colors"
                >
                  <span className="break-all">
                    {truncateMid(contactPubkey, 10, 10)}
                  </span>
                  {copied ? (
                    <Check className="size-3 text-success" />
                  ) : (
                    <Copy className="size-3" />
                  )}
                </button>
              </InfoRow>
              <InfoRow label="Type">
                <span className="font-mono text-xs uppercase tracking-[0.08em]">
                  {contact.type ?? "—"}
                </span>
              </InfoRow>
              <InfoRow label="Added">
                <span className="tabular-nums">{timeAgo(contact.addedAt)}</span>
              </InfoRow>
            </div>
          </section>

          <section className="panel overflow-hidden">
            <PanelHeader eyebrow="Routing" title="Path" />
            <div className="divide-y divide-border">
              <InfoRow label="Route">
                <span className="font-mono text-xs uppercase tracking-[0.08em]">
                  {routeLabel}
                </span>
              </InfoRow>
              {path?.hasPath && !path.directNeighbor && path.outPath && (
                <InfoRow label="Out path">
                  <code className="font-mono text-xs break-all">
                    {path.outPath}
                  </code>
                </InfoRow>
              )}
              <InfoRow label="Path hash size">
                <span className="tabular-nums">
                  {path?.pathHashSize ?? 1}-byte
                </span>
              </InfoRow>
            </div>
          </section>

          <section className="panel p-4">
            <TelemetryPanel apiBase={apiBase} />
          </section>
        </>
      )}
    </div>
  );
}

function PanelHeader({ eyebrow, title }: { eyebrow: string; title: string }) {
  return (
    <div className="px-4 py-3 border-b border-border space-y-0.5">
      <span className="label-overline block">{eyebrow}</span>
      <h2 className="font-mono text-sm uppercase tracking-[0.1em]">{title}</h2>
    </div>
  );
}

function InfoRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 px-4 py-3">
      <span className="label-overline shrink-0">{label}</span>
      <div className="min-w-0 text-right text-sm">{children}</div>
    </div>
  );
}
