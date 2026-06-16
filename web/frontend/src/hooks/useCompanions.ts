import { useEffect, useState } from "react";

// A companion reference as exposed by GET /api/companions — enough to label and
// route to a companion. Pages previously each re-declared this shape.
export interface CompanionRef {
  name: string;
  pubkey?: string;
}

// Module-level stale-while-revalidate cache for /api/companions. Six pages
// fetched this endpoint independently; this dedups concurrent requests (one
// in-flight at a time), serves cached data instantly on navigation, and
// self-heals after a mutation by revalidating on every mount.
let cache: CompanionRef[] | null = null;
let inflight: Promise<CompanionRef[]> | null = null;
const subscribers = new Set<(c: CompanionRef[]) => void>();

function fetchCompanions(): Promise<CompanionRef[]> {
  if (inflight) return inflight;
  inflight = fetch("/api/companions")
    .then((r) => (r.ok ? r.json() : []))
    .then((cs: CompanionRef[]) => {
      cache = cs || [];
      subscribers.forEach((fn) => fn(cache!));
      return cache;
    })
    .catch(() => cache ?? [])
    .finally(() => {
      inflight = null;
    });
  return inflight;
}

export function useCompanions(): CompanionRef[] {
  const [companions, setCompanions] = useState<CompanionRef[]>(cache ?? []);

  useEffect(() => {
    let active = true;
    const update = (c: CompanionRef[]) => {
      if (active) setCompanions(c);
    };
    subscribers.add(update);
    // Revalidate on every mount; cached value already rendered for instant paint.
    fetchCompanions().then(update);
    return () => {
      subscribers.delete(update);
      active = false;
    };
  }, []);

  return companions;
}
