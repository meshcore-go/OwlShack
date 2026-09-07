import { useCallback, useEffect, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";

interface ApiObject<T> {
  /** null until the first successful load. */
  item: T | null;
  setItem: Dispatch<SetStateAction<T | null>>;
  loading: boolean;
  error: string | null;
  reload: () => void;
}

// Single-object sibling of useApiList; pass url=null to defer.
export function useApiObject<T>(
  url: string | null,
  errorMessage: string,
): ApiObject<T> {
  const [item, setItem] = useState<T | null>(null);
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
      .then((data: T) => {
        if (seq.current === id) setItem(data);
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

  return { item, setItem, loading, error, reload };
}
