"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ProblemError, isProblem } from "@karmarat/ui";
import { useApi } from "@karmarat/ui/react";

import { useSession } from "./session-provider";

export interface Resource<T> {
  data: T | null;
  /** True while the first load, or a reload, is in flight. */
  loading: boolean;
  /** Always a ProblemError, so every page renders `.detail` the same way. */
  error: ProblemError | null;
  reload: () => void;
}

/**
 * Reads one endpoint and gives a page the three states it has to render.
 *
 * Waits for the session to be ready rather than firing an unauthenticated
 * request that would 401, trigger a refresh, and replay — a page load should
 * not cost a token rotation just because the component mounted first.
 */
export function useResource<T>(path: string | null, deps: unknown[] = []): Resource<T> {
  const api = useApi();
  const { ready } = useSession();

  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ProblemError | null>(null);
  const [nonce, setNonce] = useState(0);

  // Guards against a slow first response overwriting a fast second one.
  const requestId = useRef(0);

  useEffect(() => {
    if (!ready || path === null) return;

    const id = ++requestId.current;
    setLoading(true);
    setError(null);

    void (async () => {
      try {
        const result = await api.get<T>(path);
        if (requestId.current !== id) return;
        setData(result);
      } catch (caught) {
        if (requestId.current !== id) return;
        setData(null);
        setError(asProblem(caught));
      } finally {
        if (requestId.current === id) setLoading(false);
      }
    })();
    // `deps` is spread so a caller can key the request on its own filters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api, path, ready, nonce, ...deps]);

  const reload = useCallback(() => setNonce((value) => value + 1), []);

  return { data, loading: loading && ready, error, reload };
}

/** Everything a page catches is rendered as a problem, including a thrown Error. */
export function asProblem(caught: unknown): ProblemError {
  if (isProblem(caught)) return caught;

  return new ProblemError({
    type: "about:blank",
    title: "Unexpected error",
    status: 0,
    detail:
      caught instanceof Error && caught.message
        ? caught.message
        : "Something went wrong in the console. Please try again.",
    code: "client_error",
  });
}
