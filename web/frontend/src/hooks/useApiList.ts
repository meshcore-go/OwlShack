import { useCallback, useEffect, useState } from "react";
import type { Dispatch, SetStateAction } from "react";

interface ApiList<T> {
  /** null until the first successful load, then always an array. */
  items: T[] | null;
  /** Exposed so pages can merge live WS updates into the loaded list. */
  setItems: Dispatch<SetStateAction<T[] | null>>;
  loading: boolean;
  error: string | null;
  reload: () => void;
}

// Shared fetch scaffold for list endpoints: loading/error/retry state plus
// auto-fetch on mount and whenever the URL changes. Pass url=null to defer
// (e.g. a missing route param).
export function useApiList<T>(
  url: string | null,
  errorMessage: string,
): ApiList<T> {
  const [items, setItems] = useState<T[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(() => {
    if (!url) return;
    setLoading(true);
    setError(null);
    fetch(url)
      .then((r) => {
        if (!r.ok) throw new Error(errorMessage);
        return r.json();
      })
      .then((data: T[] | null) => setItems(data || []))
      .catch(() => setError(errorMessage))
      .finally(() => setLoading(false));
  }, [url, errorMessage]);

  useEffect(() => {
    reload();
  }, [reload]);

  return { items, setItems, loading, error, reload };
}
