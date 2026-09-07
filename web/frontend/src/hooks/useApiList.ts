import { useCallback, useEffect, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";

interface ApiList<T> {
  /** null until the first successful load, then always an array. */
  items: T[] | null;
  setItems: Dispatch<SetStateAction<T[] | null>>;
  loading: boolean;
  error: string | null;
  reload: () => void;
}

// Refetches whenever the URL changes; pass url=null to defer (e.g. a missing route param).
export function useApiList<T>(
  url: string | null,
  errorMessage: string,
): ApiList<T> {
  const [items, setItems] = useState<T[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const seq = useRef(0);
  const reload = useCallback(() => {
    if (!url) return;
    const id = ++seq.current;
    setLoading(true);
    setError(null);
    fetch(url)
      .then((r) => {
        if (!r.ok) throw new Error(errorMessage);
        return r.json();
      })
      .then((data: T[] | null) => {
        if (seq.current === id) setItems(data || []);
      })
      .catch(() => {
        if (seq.current === id) setError(errorMessage);
      })
      .finally(() => {
        if (seq.current === id) setLoading(false);
      });
  }, [url, errorMessage]);

  useEffect(() => {
    reload();
    return () => {
      seq.current++;
    };
  }, [reload]);

  return { items, setItems, loading, error, reload };
}
