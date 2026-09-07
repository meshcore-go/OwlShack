import { useEffect, useState } from "react";

// A companion reference as exposed by GET /api/companions.
export interface CompanionRef {
  name: string;
  pubkey?: string;
}

// Module-level stale-while-revalidate cache: many pages fetch /api/companions independently.
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
    // Revalidate on every mount, so a mutation elsewhere self-heals.
    fetchCompanions().then(update);
    return () => {
      subscribers.delete(update);
      active = false;
    };
  }, []);

  return companions;
}
