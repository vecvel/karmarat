"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ProblemError } from "@karmarat/ui";
import { useApi } from "@karmarat/ui/react";

import { useSession } from "./session-provider";
import { asProblem } from "./use-resource";

/**
 * A paginated list endpoint, as the three states a page has to render plus a
 * cursor trail.
 *
 * The platform's list contract is `?limit=&cursor=` returning `nextCursor`, and
 * only forwards. Keeping the cursors already visited in state is what lets the
 * console offer a Previous button without the API inventing a backwards cursor.
 *
 * `itemsKey` exists because the services disagree: most return `items`, the
 * subscriptions list returns `data`. Rather than normalising that away
 * silently, each caller names the key it expects, so a service changing its
 * shape shows up as an empty list on one page instead of a mystery.
 */
export interface CursorList<T> {
  items: T[];
  loading: boolean;
  error: ProblemError | null;
  nextCursor: string | null;
  history: (string | null)[];
  next: () => void;
  back: () => void;
  reload: () => void;
}

export function useCursorList<T>(
  path: string,
  query: Record<string, string | undefined>,
  options: { itemsKey?: string; limit?: number } = {},
): CursorList<T> {
  const api = useApi();
  const { ready } = useSession();
  const itemsKey = options.itemsKey ?? "items";
  const limit = options.limit ?? 25;

  const [history, setHistory] = useState<(string | null)[]>([null]);
  const [items, setItems] = useState<T[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ProblemError | null>(null);
  const [nonce, setNonce] = useState(0);

  // Serialised so the effect keys on the filter *values*, not on the object
  // literal a parent re-creates every render.
  const queryKey = useMemo(() => JSON.stringify(query), [query]);
  const cursor = history[history.length - 1] ?? null;
  const requestId = useRef(0);

  // A filter change invalidates the trail: page 3 of "all companies" is not
  // page 3 of "suspended companies".
  const previousQueryKey = useRef(queryKey);
  useEffect(() => {
    if (previousQueryKey.current !== queryKey) {
      previousQueryKey.current = queryKey;
      setHistory([null]);
    }
  }, [queryKey]);

  useEffect(() => {
    if (!ready) return;

    const id = ++requestId.current;
    setLoading(true);
    setError(null);

    const params = new URLSearchParams();
    params.set("limit", String(limit));
    if (cursor) params.set("cursor", cursor);
    for (const [key, value] of Object.entries(JSON.parse(queryKey) as Record<string, string | undefined>)) {
      if (value !== undefined && value !== "") params.set(key, value);
    }

    void (async () => {
      try {
        const result = await api.get<Record<string, unknown>>(`${path}?${params.toString()}`);
        if (requestId.current !== id) return;
        const rows = result[itemsKey];
        setItems(Array.isArray(rows) ? (rows as T[]) : []);
        setNextCursor(typeof result["nextCursor"] === "string" ? (result["nextCursor"] as string) : null);
      } catch (caught) {
        if (requestId.current !== id) return;
        setItems([]);
        setNextCursor(null);
        setError(asProblem(caught));
      } finally {
        if (requestId.current === id) setLoading(false);
      }
    })();
  }, [api, path, itemsKey, limit, cursor, queryKey, ready, nonce]);

  const next = useCallback(() => {
    setHistory((trail) => (nextCursor ? [...trail, nextCursor] : trail));
  }, [nextCursor]);

  const back = useCallback(() => {
    setHistory((trail) => (trail.length > 1 ? trail.slice(0, -1) : trail));
  }, []);

  const reload = useCallback(() => setNonce((value) => value + 1), []);

  return { items, loading: loading && ready, error, nextCursor, history, next, back, reload };
}
