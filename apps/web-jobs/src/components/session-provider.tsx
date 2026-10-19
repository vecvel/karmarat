"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useRouter } from "next/navigation";
import { createApiClient, snapshotFromSession, type Session } from "@karmarat/ui";
import { AccessProvider, ApiProvider } from "@karmarat/ui/react";

interface SessionState {
  session: Session | null;
  /** False until the first refresh has settled, one way or the other. */
  ready: boolean;
  signOut: () => Promise<void>;
  /** Adopts a session a sign-in or registration just produced. */
  adopt: (session: Session) => void;
}

const SessionContext = createContext<SessionState | null>(null);

export function useSession(): SessionState {
  const state = useContext(SessionContext);
  if (!state) {
    throw new Error("useSession must be used inside <SessionProvider>");
  }
  return state;
}

/**
 * Holds the access token for the whole app, in memory and nowhere else.
 *
 * On mount it asks the same-origin refresh route for a token. That route is
 * the only thing that touches the httpOnly refresh cookie; the browser never
 * sees the refresh token, so an XSS here can steal at most fifteen minutes.
 *
 * `expectSession` comes from the server layout, which read the identity cookie.
 * When there is no cookie there is nothing to exchange, and skipping the call
 * keeps the public job board from making a pointless round trip on every load.
 */
export function SessionProvider({
  expectSession,
  children,
}: {
  expectSession: boolean;
  children: ReactNode;
}) {
  const router = useRouter();
  const [session, setSession] = useState<Session | null>(null);
  const [ready, setReady] = useState(!expectSession);

  // The router is a dependency of the sign-out callback but must not rebuild
  // the client, which would drop the access token it is holding.
  const routerRef = useRef(router);
  routerRef.current = router;

  const client = useMemo(
    () =>
      createApiClient({
        refresh: async () => {
          const response = await fetch("/api/auth/refresh", { method: "POST" });
          if (!response.ok) return null;
          const next = (await response.json()) as Session;
          setSession(next);
          return next;
        },
        onSignedOut: () => {
          setSession(null);
          // Replace, not push: a back button that returns to a page the user is
          // no longer allowed to see is a dead end.
          routerRef.current.replace("/sign-in");
        },
      }),
    [],
  );

  useEffect(() => {
    if (!expectSession) return;

    let cancelled = false;
    void (async () => {
      const response = await fetch("/api/auth/refresh", { method: "POST" });
      if (cancelled) return;

      if (response.ok) {
        const next = (await response.json()) as Session;
        client.setSession(next);
        setSession(next);
      } else {
        // The cookie is gone, expired or revoked. The server guard will send
        // them to sign in on the next navigation; here we simply stop
        // pretending there is a session.
        setSession(null);
      }
      setReady(true);
    })();

    return () => {
      cancelled = true;
    };
  }, [client, expectSession]);

  // setSession on the client and in React state are two different stores; this
  // keeps the client authoritative whenever the state changes for any reason.
  useEffect(() => {
    client.setSession(session);
  }, [client, session]);

  const signOut = useCallback(async () => {
    await fetch("/api/auth/logout", { method: "POST" });
    setSession(null);
    client.setSession(null);
    router.replace("/");
    router.refresh();
  }, [client, router]);

  const adopt = useCallback(
    (next: Session) => {
      client.setSession(next);
      setSession(next);
      setReady(true);
    },
    [client],
  );

  const value = useMemo<SessionState>(
    () => ({ session, ready, signOut, adopt }),
    [session, ready, signOut, adopt],
  );

  return (
    <SessionContext.Provider value={value}>
      <ApiProvider client={client}>
        <AccessProvider snapshot={snapshotFromSession(session)}>{children}</AccessProvider>
      </ApiProvider>
    </SessionContext.Provider>
  );
}
