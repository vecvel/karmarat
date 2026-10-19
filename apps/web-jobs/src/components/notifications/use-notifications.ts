"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { isProblem, type ApiClient } from "@karmarat/ui";
import { useApi } from "@karmarat/ui/react";

import type { Notification, NotificationPage } from "@/lib/api-types";
import { useSession } from "@/components/session-provider";

interface NotificationsState {
  notifications: Notification[];
  unreadCount: number;
  loading: boolean;
  error: string | null;
  /** True while a live stream is attached. */
  live: boolean;
  markRead: (id: string) => Promise<void>;
  markAllRead: () => Promise<void>;
  reload: () => Promise<void>;
}

/**
 * The inbox, plus a live stream when one can be held open.
 *
 * The durable copy is already in Postgres before anything is streamed, so a
 * missed frame costs an animation rather than a notification — which is why
 * reconnect here can be simple and why a failed stream is not an error the
 * user is told about. The list is the truth; the stream is a courtesy.
 */
export function useNotifications(enabled: boolean): NotificationsState {
  const api = useApi();
  const { ready, session } = useSession();
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [unreadCount, setUnreadCount] = useState(0);
  const [loading, setLoading] = useState(enabled);
  const [error, setError] = useState<string | null>(null);
  const [live, setLive] = useState(false);

  const reload = useCallback(async () => {
    try {
      const page = await api.get<NotificationPage>("/api/v1/notifications?limit=50");
      setNotifications(page.notifications ?? []);
      setUnreadCount(page.unreadCount ?? 0);
      setError(null);
    } catch (cause) {
      setError(
        isProblem(cause) ? cause.detail : "We could not load your notifications.",
      );
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    if (!enabled || !ready || !session) return;
    void reload();
  }, [enabled, ready, session, reload]);

  const markRead = useCallback(
    async (id: string) => {
      // Optimistic: the row is already read from the person's point of view the
      // moment they open it, and a round trip's delay before the badge moves
      // reads as a bug.
      setNotifications((current) =>
        current.map((item) => (item.id === id ? { ...item, read: true } : item)),
      );
      setUnreadCount((count) => Math.max(0, count - 1));
      try {
        await api.post(`/api/v1/notifications/${id}/read`);
      } catch {
        await reload();
      }
    },
    [api, reload],
  );

  const markAllRead = useCallback(async () => {
    setNotifications((current) => current.map((item) => ({ ...item, read: true })));
    setUnreadCount(0);
    try {
      await api.post("/api/v1/notifications/read-all");
    } catch {
      await reload();
    }
  }, [api, reload]);

  /* --------------------------------------------------------- live stream -- */

  const streamRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!enabled || !ready || !session) return;

    let stopped = false;
    let attempt = 0;

    const run = async () => {
      while (!stopped) {
        const controller = new AbortController();
        streamRef.current = controller;

        const opened = await readStream(api, controller.signal, (notification) => {
          setNotifications((current) => {
            // The same id can arrive twice: a reconnect replays nothing, but a
            // list refresh racing a frame would otherwise duplicate a row.
            if (current.some((item) => item.id === notification.id)) return current;
            return [notification, ...current];
          });
          if (!notification.read) setUnreadCount((count) => count + 1);
        });

        if (stopped) return;
        setLive(false);

        // A stream that lived is a healthy one: the service rotates connections
        // deliberately, so reconnect immediately rather than backing off as if
        // something were wrong.
        attempt = opened ? 0 : attempt + 1;
        const delay = opened ? 500 : Math.min(30_000, 2_000 * 2 ** (attempt - 1));
        await sleep(delay, controller.signal);
      }
    };

    setLive(true);
    void run();

    return () => {
      stopped = true;
      streamRef.current?.abort();
      streamRef.current = null;
    };
  }, [api, enabled, ready, session]);

  return {
    notifications,
    unreadCount,
    loading,
    error,
    live,
    markRead,
    markAllRead,
    reload,
  };
}

/**
 * Reads one SSE connection to completion.
 *
 * `fetch` rather than `EventSource` because EventSource cannot set a header,
 * and the alternatives are both worse: putting the access token in the query
 * string writes a credential into every proxy log, and having a route handler
 * mint its own token would spend the rotating refresh cookie on a schedule
 * that races the client's own refresh.
 *
 * Returns whether the connection was ever established, which is what tells the
 * caller apart a routine rotation from a service that is down.
 */
async function readStream(
  api: ApiClient,
  signal: AbortSignal,
  onNotification: (notification: Notification) => void,
): Promise<boolean> {
  const token = api.getSession()?.accessToken;
  if (!token) return false;

  let response: Response;
  try {
    response = await fetch("/api/v1/notifications/stream", {
      headers: { Authorization: `Bearer ${token}`, Accept: "text/event-stream" },
      signal,
    });
  } catch {
    return false;
  }

  if (response.status === 401) {
    // Lean on the client's own refresh rather than adding a second one: it
    // shares a single in-flight promise, and two refreshes racing would present
    // a spent rotating token, which identity treats as theft.
    try {
      await api.get("/api/v1/notifications?limit=1");
    } catch {
      /* The client has already signed the user out if it could not recover. */
    }
    return false;
  }

  if (!response.ok || !response.body) return false;

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });

      // Frames are separated by a blank line; a partial frame stays in the
      // buffer until the rest of it arrives.
      let split = buffer.indexOf("\n\n");
      while (split !== -1) {
        handleFrame(buffer.slice(0, split), onNotification);
        buffer = buffer.slice(split + 2);
        split = buffer.indexOf("\n\n");
      }
    }
  } catch {
    // An aborted or broken read ends this connection; the caller reconnects.
  } finally {
    try {
      reader.cancel();
    } catch {
      /* already closed */
    }
  }

  return true;
}

function handleFrame(
  frame: string,
  onNotification: (notification: Notification) => void,
): void {
  let event = "message";
  const data: string[] = [];

  for (const line of frame.split("\n")) {
    // A line starting with ':' is a keep-alive comment and carries nothing.
    if (line.startsWith(":")) continue;
    if (line.startsWith("event:")) event = line.slice(6).trim();
    else if (line.startsWith("data:")) data.push(line.slice(5).trim());
  }

  // "reconnect" is the service telling us it is rotating the connection on
  // purpose; there is no payload to render.
  if (event === "reconnect" || data.length === 0) return;

  try {
    const payload = JSON.parse(data.join("\n")) as Notification;
    if (payload && typeof payload.id === "string") onNotification(payload);
  } catch {
    // A frame we cannot read is dropped. The next list request has the row.
  }
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener("abort", () => {
      clearTimeout(timer);
      resolve();
    });
  });
}
