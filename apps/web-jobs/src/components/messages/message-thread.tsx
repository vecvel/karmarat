"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Send } from "lucide-react";
import { useApi } from "@karmarat/ui/react";

import { ListSkeleton, ProblemAlert } from "@/components/feedback";
import { useSession } from "@/components/session-provider";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { Conversation, DataPage, Message } from "@/lib/api-types";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

export function MessageThread({ conversationId }: { conversationId: string }) {
  const api = useApi();
  const { ready, session } = useSession();

  const [conversation, setConversation] = useState<Conversation | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState<unknown>(null);

  const bottom = useRef<HTMLDivElement>(null);

  const load = useCallback(async () => {
    try {
      const [thread, page] = await Promise.all([
        api.get<Conversation>(`/api/v1/my-conversations/${conversationId}`),
        api.get<DataPage<Message>>(
          `/api/v1/my-conversations/${conversationId}/messages?limit=100`,
        ),
      ]);
      setConversation(thread);
      // The service returns newest first; a thread reads oldest first.
      setMessages([...(page.data ?? [])].reverse());
      setError(null);
    } catch (cause) {
      setError(cause);
    } finally {
      setLoading(false);
    }
  }, [api, conversationId]);

  useEffect(() => {
    if (!ready || !session) return;
    void load();
  }, [ready, session, load]);

  // Marking read is what stops the recruiter's "delivered but unread" signal
  // from being permanently wrong. A failure is not worth telling anyone about.
  useEffect(() => {
    if (!conversation || conversation.unreadCount === 0) return;
    void api.post(`/api/v1/my-conversations/${conversationId}/read`).catch(() => {});
  }, [api, conversation, conversationId]);

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" });
  }, [messages.length]);

  const send = async (event: React.FormEvent) => {
    event.preventDefault();
    const body = draft.trim();
    if (!body) return;

    setSending(true);
    setSendError(null);
    try {
      const message = await api.post<Message>(
        `/api/v1/my-conversations/${conversationId}/messages`,
        { body },
      );
      setMessages((current) => [...current, message]);
      setDraft("");
    } catch (cause) {
      setSendError(cause);
    } finally {
      setSending(false);
    }
  };

  if (!ready || loading) return <ListSkeleton rows={3} />;
  if (error) return <ProblemAlert error={error} />;

  const closed = Boolean(conversation?.closedAt);

  return (
    <div className="flex h-full min-h-[28rem] flex-col">
      <div className="border-b border-border pb-3">
        <h2 className="font-medium">{conversation?.subject || "Conversation"}</h2>
        <p className="text-xs text-muted-foreground">
          {closed
            ? "This conversation has been closed by the company."
            : `Last activity ${formatRelative(conversation?.lastActivityAt)}`}
        </p>
      </div>

      <ol className="flex-1 space-y-3 overflow-y-auto py-4 scrollbar-thin">
        {messages.length === 0 ? (
          <li className="text-sm text-muted-foreground">
            No messages in this conversation yet.
          </li>
        ) : (
          messages.map((message) => {
            const mine = message.senderType === "candidate";
            return (
              <li
                key={message.id}
                className={cn("flex", mine ? "justify-end" : "justify-start")}
              >
                <div
                  className={cn(
                    "max-w-[85%] rounded-xs border p-3 text-sm",
                    mine
                      ? "border-accent/30 bg-accent/10"
                      : "border-border bg-card",
                  )}
                >
                  <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                    {/* A candidate is told which company wrote, never which
                        individual recruiter — the service omits that on
                        purpose, and so does this. */}
                    {mine ? "You" : "The company"}
                  </p>
                  <p className="mt-1 whitespace-pre-wrap">{message.body}</p>
                  {message.attachments?.length ? (
                    <ul className="mt-2 space-y-1 text-xs text-muted-foreground">
                      {message.attachments.map((attachment) => (
                        <li key={attachment.key}>{attachment.filename}</li>
                      ))}
                    </ul>
                  ) : null}
                  <p className="mt-1.5 text-[11px] text-muted-foreground">
                    <time dateTime={message.sentAt}>
                      {formatDateTime(message.sentAt)}
                    </time>
                  </p>
                </div>
              </li>
            );
          })
        )}
        <div ref={bottom} />
      </ol>

      {closed ? (
        <p className="border-t border-border pt-3 text-sm text-muted-foreground">
          You can still read this thread, but no new messages can be sent.
        </p>
      ) : (
        <form onSubmit={send} className="space-y-2 border-t border-border pt-3">
          {sendError ? <ProblemAlert error={sendError} /> : null}
          <label htmlFor="message-body" className="sr-only">
            Your reply
          </label>
          <Textarea
            id="message-body"
            rows={3}
            value={draft}
            disabled={sending}
            placeholder="Write a reply…"
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              // Enter sends, Shift+Enter makes a new line — the convention
              // every messaging app has trained people on.
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                void send(event as unknown as React.FormEvent);
              }
            }}
          />
          <div className="flex justify-end">
            <Button
              type="submit"
              variant="accent"
              size="sm"
              className="gap-2"
              disabled={sending || draft.trim() === ""}
            >
              <Send aria-hidden="true" />
              {sending ? "Sending…" : "Send"}
            </Button>
          </div>
        </form>
      )}
    </div>
  );
}
