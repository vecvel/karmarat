"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { MessageSquare } from "lucide-react";
import { useApi } from "@karmarat/ui/react";

import { EmptyState, ListSkeleton, ProblemAlert } from "@/components/feedback";
import { useSession } from "@/components/session-provider";
import { Badge } from "@/components/ui/badge";
import type { Conversation, DataPage } from "@/lib/api-types";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

/**
 * A candidate cannot start a conversation — the service has no endpoint for it
 * on purpose, because a recruiter's inbox would otherwise be open to anyone.
 * Threads appear here when a company writes first, which is why the empty
 * state explains rather than offering a button that cannot exist.
 */
export function ConversationList({ activeId }: { activeId?: string }) {
  const api = useApi();
  const { ready, session } = useSession();
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    if (!ready || !session) return;

    let cancelled = false;
    void (async () => {
      try {
        const page = await api.get<DataPage<Conversation>>(
          "/api/v1/my-conversations?limit=50",
        );
        if (!cancelled) {
          setConversations(page.data ?? []);
          setError(null);
        }
      } catch (cause) {
        if (!cancelled) setError(cause);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [api, ready, session]);

  if (!ready || loading) return <ListSkeleton rows={3} />;
  if (error) return <ProblemAlert error={error} />;

  if (conversations.length === 0) {
    return (
      <EmptyState
        icon={MessageSquare}
        title="No messages yet"
        description="Companies start the conversation. When a recruiter writes to you about an application, the thread appears here."
      />
    );
  }

  return (
    <ul className="space-y-2">
      {conversations.map((conversation) => (
        <li key={conversation.id}>
          <Link
            href={`/messages/${conversation.id}`}
            aria-current={conversation.id === activeId ? "page" : undefined}
            className={cn(
              "surface block p-3 transition-colors hover:bg-muted/50",
              conversation.id === activeId ? "border-accent bg-accent/5" : "",
            )}
          >
            <div className="flex items-start justify-between gap-2">
              <p className="min-w-0 truncate text-sm font-medium">
                {conversation.subject || "Conversation"}
              </p>
              {conversation.unreadCount > 0 ? (
                <Badge variant="accent">{conversation.unreadCount}</Badge>
              ) : null}
            </div>
            {conversation.lastMessagePreview ? (
              <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">
                {conversation.lastMessageSender === "candidate" ? "You: " : ""}
                {conversation.lastMessagePreview}
              </p>
            ) : null}
            <p className="mt-1.5 text-[11px] text-muted-foreground">
              {formatRelative(conversation.lastActivityAt)}
              {conversation.closedAt ? " · closed" : ""}
            </p>
          </Link>
        </li>
      ))}
    </ul>
  );
}
