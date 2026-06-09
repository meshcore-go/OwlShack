import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft, Check, CircleDashed, Trash2, UserPlus, X } from "lucide-react";
import { toast } from "sonner";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { PageHeader } from "@/components/PageHeader";
import { PeerAvatar } from "@/components/PeerAvatar";
import { PeerTypePill } from "@/components/StatusIndicator";
import { AddContactDialog } from "@/components/AddContactDialog";
import { timeAgo, truncateMid } from "@/lib/format";

interface Contact {
  peerPubkey: string;
  name: string;
  type?: string;
  addedAt: string;
}

export function ContactsPage() {
  const { name } = useParams();
  const companion = decodeURIComponent(name ?? "");

  const [contacts, setContacts] = useState<Contact[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);

  const load = useCallback(() => {
    if (!companion) return;
    setLoading(true);
    setError(null);
    fetch(`/api/companions/${encodeURIComponent(companion)}/contacts`)
      .then((r) => {
        if (!r.ok) throw new Error("contacts");
        return r.json();
      })
      .then((cs) => setContacts(cs || []))
      .catch(() => setError("Failed to load contacts"))
      .finally(() => setLoading(false));
  }, [companion]);

  useEffect(() => {
    load();
  }, [load]);

  const removeContact = useCallback(
    async (pubkey: string) => {
      try {
        const res = await fetch(
          `/api/companions/${encodeURIComponent(companion)}/contacts/${pubkey}`,
          { method: "DELETE" },
        );
        if (!res.ok) {
          const err = await res.json().catch(() => ({}));
          throw new Error(err.error || `HTTP ${res.status}`);
        }
        toast.success("Contact removed");
        setConfirmRemove(null);
        load();
      } catch (e) {
        const msg = e instanceof Error ? e.message : "failed";
        toast.error(`Failed to remove contact: ${msg}`);
      }
    },
    [companion, load],
  );

  const contactsSorted = useMemo(() => {
    if (!contacts) return [];
    return [...contacts].sort((a, b) =>
      (a.name || "").localeCompare(b.name || ""),
    );
  }, [contacts]);

  const existingPubkeys = useMemo(
    () => (contacts || []).map((c) => c.peerPubkey),
    [contacts],
  );

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3">
        <Link
          to={`/companions/${encodeURIComponent(companion)}`}
          className="inline-flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground hover:text-primary transition-colors w-fit"
        >
          <ArrowLeft className="size-3" />
          {companion || "companion"}
        </Link>

        <PageHeader
          title="Contacts"
          meta={
            contacts && (
              <span className="font-mono text-sm text-muted-foreground tabular-nums">
                {contacts.length} configured
              </span>
            )
          }
          actions={
            <Button
              variant="default"
              size="sm"
              onClick={() => setDialogOpen(true)}
              className="font-mono text-xs uppercase tracking-[0.1em]"
            >
              <UserPlus className="size-3.5" />
              Add contact
            </Button>
          }
          className="mb-0"
        />
      </div>

      {loading && <ContactsSkeleton />}

      {error && (
        <Alert variant="destructive">
          <AlertTitle className="font-mono uppercase tracking-[0.1em]">
            Error
          </AlertTitle>
          <AlertDescription>
            {error}
            <Button
              variant="ghost"
              size="sm"
              onClick={load}
              className="ml-2 h-7 text-xs uppercase tracking-[0.1em]"
            >
              retry
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {!loading && !error && contacts && (
        <section className="panel overflow-hidden">
          <div className="flex items-center justify-between px-4 py-3 border-b border-border">
            <div className="space-y-0.5">
              <span className="label-overline block">Roster</span>
              <h2 className="font-mono text-sm uppercase tracking-[0.1em]">
                Allowed peers
              </h2>
            </div>
            <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground/70 tabular-nums">
              {contacts.length}
            </span>
          </div>

          {contactsSorted.length === 0 ? (
            <div className="px-6 py-16 text-center">
              <CircleDashed className="size-8 mx-auto mb-3 text-muted-foreground/40" />
              <p className="font-mono text-sm uppercase tracking-[0.1em] text-muted-foreground">
                No contacts yet
              </p>
              <p className="mt-2 text-xs text-muted-foreground/70">
                Add peers to enable direct messaging.
              </p>
              <Button
                variant="default"
                size="sm"
                onClick={() => setDialogOpen(true)}
                className="mt-4 font-mono text-xs uppercase tracking-[0.1em]"
              >
                <UserPlus className="size-3.5" />
                Add contact
              </Button>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {contactsSorted.map((c) => (
                <ContactRow
                  key={c.peerPubkey}
                  companion={companion}
                  contact={c}
                  confirming={confirmRemove === c.peerPubkey}
                  onAskRemove={() => setConfirmRemove(c.peerPubkey)}
                  onCancel={() => setConfirmRemove(null)}
                  onConfirm={() => removeContact(c.peerPubkey)}
                />
              ))}
            </div>
          )}
        </section>
      )}

      <AddContactDialog
        companion={companion}
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        existingPubkeys={existingPubkeys}
        onAdded={load}
      />
    </div>
  );
}

function ContactRow({
  companion,
  contact,
  confirming,
  onAskRemove,
  onCancel,
  onConfirm,
}: {
  companion: string;
  contact: Contact;
  confirming: boolean;
  onAskRemove: () => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const displayName = contact.name || "unknown peer";
  // Repeaters have their own admin page; everything else opens contact detail.
  const detailTo =
    contact.type?.toUpperCase() === "REPEATER"
      ? `/companions/${encodeURIComponent(companion)}/repeaters/${contact.peerPubkey}`
      : `/companions/${encodeURIComponent(companion)}/contacts/${contact.peerPubkey}`;
  return (
    <div className="flex items-center gap-3 px-4 py-3 hover:bg-muted/40 transition-colors">
      <Link to={detailTo} className="flex items-center gap-3 min-w-0 flex-1">
        <PeerAvatar name={displayName} size="md" />

        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium truncate">
              {contact.name || (
                <span className="text-muted-foreground italic">unknown</span>
              )}
            </span>
            {contact.type && <PeerTypePill type={contact.type} />}
          </div>
          <div className="flex items-center gap-3 text-mono-xs text-muted-foreground">
            <code className="font-mono text-xs text-muted-foreground">
              {truncateMid(contact.peerPubkey, 8, 6)}
            </code>
            <span className="text-muted-foreground/50">·</span>
            <span className="tabular-nums">
              added {timeAgo(contact.addedAt)}
            </span>
          </div>
        </div>
      </Link>

      <div className="shrink-0">
        {confirming ? (
          <div className="inline-flex items-center gap-1">
            <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground mr-1">
              Remove?
            </span>
            <Button
              variant="destructive"
              size="xs"
              onClick={onConfirm}
              className="font-mono uppercase tracking-[0.1em]"
            >
              <Check className="size-3" /> yes
            </Button>
            <Button
              variant="ghost"
              size="xs"
              onClick={onCancel}
              className="font-mono uppercase tracking-[0.1em]"
            >
              <X className="size-3" /> no
            </Button>
          </div>
        ) : (
          <Button
            variant="ghost"
            size="sm"
            onClick={onAskRemove}
            className="text-muted-foreground hover:text-destructive font-mono text-[10px] uppercase tracking-[0.12em]"
          >
            <Trash2 className="size-3" />
            remove
          </Button>
        )}
      </div>
    </div>
  );
}

function ContactsSkeleton() {
  return (
    <div className="panel">
      <div className="px-4 py-3 border-b border-border">
        <Skeleton className="h-3 w-20 mb-2" />
        <Skeleton className="h-4 w-32" />
      </div>
      <div className="divide-y divide-border">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="flex items-center gap-3 px-4 py-3">
            <Skeleton className="size-9" />
            <div className="flex-1 space-y-2">
              <Skeleton className="h-3 w-32" />
              <Skeleton className="h-3 w-48" />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
