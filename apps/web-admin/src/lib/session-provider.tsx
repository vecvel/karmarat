"use client";

import { useRouter } from "next/navigation";
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
import { createApiClient, snapshotFromSession, type ApiClient, type Session } from "@karmarat/ui";
import { AccessProvider, ApiProvider } from "@karmarat/ui/react";

export type ConsoleSession = Session & { roleNames: string[]; isSuperAdmin: boolean };

export type ConsoleIdentity = Omit<ConsoleSession, "accessToken" | "expiresAt">;

interface SessionState {
  /** Who the operator is. Available on the first paint, from the server. */
  identity: ConsoleIdentity | null;
  /** True once an access token is in memory and the API can be called. */
  ready: boolean;
  /** True until the first /api/auth/session answer arrives. */
  loading: boolean;
  signOut: () => Promise<void>;
}

const SessionContext = createContext<SessionState | null>(null);

export function useSession(): SessionState {
  const state = useContext(SessionContext);
  if (!state) throw new Error("useSession must be used inside <SessionProvider>");
  return state;
}

/**
 * Holds the access token in memory and keeps the ApiClient pointed at it.
 *
 * The token is never written to localStorage or to a script-readable cookie:
 * an XSS on this origin can act as the operator for as long as the page is
 * open, but it cannot copy a credential out and use it later.
 *
 * `initialIdentity` comes from the server component that already read the
 * identity cookie, so the shell renders with the operator's name and
 * permissions on the first paint instead of flashing an empty frame while the
 * token is fetched.
 */
export function SessionProvider({
  initialIdentity,
  children,
}: {
  initialIdentity: ConsoleIdentity | null;
  children: ReactNode;
}) {
  const router = useRouter();
  const [session, setSession] = useState<ConsoleSession | null>(null);
  const [loading, setLoading] = useState(true);

  // A ref so the client's callbacks always see the live setter without the
  // client itself being rebuilt — rebuilding it would drop the in-flight
  // refresh promise that stops three parallel 401s becoming three refreshes.
  const sessionRef = useRef<(next: ConsoleSession | null) => void>(setSession);
  sessionRef.current = setSession;

  const signedOutRef = useRef(false);

  const client: ApiClient = useMemo(
    () =>
      createApiClient({
        // Same-origin: the proxy route handler adds the portal's Host header,
        // which the browser is not allowed to set and the gateway routes on.
        baseUrl: "/api/gateway",
        refresh: async () => {
          const response = await fetch("/api/auth/refresh", {
            method: "POST",
            credentials: "include",
          });
          if (!response.ok) return null;
          const next = (await response.json()) as ConsoleSession;
          sessionRef.current(next);
          return next;
        },
        onSignedOut: () => {
          if (signedOutRef.current) return;
          signedOutRef.current = true;
          sessionRef.current(null);
          router.replace("/login?reason=expired");
        },
      }),
    [router],
  );

  // Keep the client's copy of the session in step with React's.
  useEffect(() => {
    client.setSession(session);
  }, [client, session]);

  useEffect(() => {
    let cancelled = false;

    void (async () => {
      try {
        const response = await fetch("/api/auth/session", { credentials: "include" });
        if (cancelled) return;

        if (!response.ok) {
          setSession(null);
          return;
        }

        const next = (await response.json()) as ConsoleSession;
        setSession(next);
        client.setSession(next);
      } catch {
        // A failed bootstrap is indistinguishable from being signed out as far
        // as this component can tell; the pages render their own error states
        // when their calls fail.
        if (!cancelled) setSession(null);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [client]);

  const signOut = useCallback(async () => {
    signedOutRef.current = true;
    try {
      await fetch("/api/auth/logout", { method: "POST", credentials: "include" });
    } finally {
      setSession(null);
      client.setSession(null);
      router.replace("/login");
      router.refresh();
    }
  }, [client, router]);

  const identity = session ?? initialIdentity;
  const snapshot = useMemo(
    () =>
      identity
        ? {
            ...snapshotFromSession({
              accountId: identity.accountId,
              principalType: identity.principalType,
              roles: identity.roles,
              permissions: identity.permissions,
            }),
            isSuperAdmin: identity.isSuperAdmin,
          }
        : null,
    [identity],
  );

  const value = useMemo<SessionState>(
    () => ({ identity, ready: session !== null, loading, signOut }),
    [identity, session, loading, signOut],
  );

  return (
    <SessionContext.Provider value={value}>
      <ApiProvider client={client}>
        <AccessProvider snapshot={snapshot}>{children}</AccessProvider>
      </ApiProvider>
    </SessionContext.Provider>
  );
}
