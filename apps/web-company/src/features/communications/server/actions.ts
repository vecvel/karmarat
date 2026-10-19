"use server";

import { revalidatePath } from "next/cache";
import { ProblemError } from "@karmarat/ui";

import { gatewayFetch } from "@/lib/gateway/client";
import { unwrap } from "@/lib/gateway/list";
import type { PlatformConversation, PlatformMessage } from "@/lib/gateway/types";
import { unavailable } from "@/lib/gateway/unavailable";
import { requirePermission } from "@/lib/rbac/guard";
import { recordAuditLog } from "@/lib/security/audit";

/**
 * Recruiter ↔ candidate conversations, served by the messaging service.
 *
 * This screen used to do two different things with one table: store email
 * templates, and send one-off mail through the company's own SMTP server. The
 * platform models the second as a *conversation* — a thread the candidate can
 * read and reply to in their own portal — which is a better thing to have, and
 * models the first not at all.
 *
 * So: sending works and is now two-way. Templates do not, because no service
 * stores them. Both are reported.
 */

/* -------------------------------------------------------------------------- */
/* Templates — no backing endpoint                                            */
/* -------------------------------------------------------------------------- */

export interface CommunicationTemplate {
  id: string;
  orgId: string;
  name: string;
  triggerEvent: string;
  subject: string;
  bodyTemplate: string;
  isActive: boolean;
  createdAt: Date;
  updatedAt: Date;
}

/**
 * Returns nothing, because nothing stores templates.
 *
 * An empty list rather than a throw: the communications page shows the
 * conversation history beside the template list, and one unavailable half must
 * not take the other down with it. The page renders the notice from
 * `templatesUnavailable()`.
 */
export async function getCommunicationTemplates(): Promise<CommunicationTemplate[]> {
  await requirePermission("communications.read");
  return [];
}

export async function templatesUnavailable() {
  return unavailable("email-templates");
}

function templatesNotAvailable(): never {
  const feature = unavailable("email-templates");
  throw new ProblemError({
    type: "about:blank",
    title: "Not available",
    status: 501,
    detail: `${feature.title} are not yet available through the platform API. ${feature.blockedOn}`,
    code: "not_implemented",
  });
}

export async function createCommunicationTemplate(_data: {
  name: string;
  triggerEvent: string;
  subject: string;
  bodyTemplate: string;
}) {
  await requirePermission("communications.create");
  templatesNotAvailable();
}

export async function updateCommunicationTemplate(
  _id: string,
  _data: Partial<{ name: string; triggerEvent: string; subject: string; bodyTemplate: string; isActive: boolean }>,
) {
  await requirePermission("communications.update");
  templatesNotAvailable();
}

export async function deleteCommunicationTemplate(_id: string) {
  await requirePermission("communications.delete");
  templatesNotAvailable();
}

/* -------------------------------------------------------------------------- */
/* Conversations                                                              */
/* -------------------------------------------------------------------------- */

export async function getConversations() {
  await requirePermission("communications.read");

  const payload = await gatewayFetch<unknown>("/api/v1/messages/conversations", {
    query: { limit: 50 },
  });
  return unwrap<PlatformConversation>(payload, "data");
}

export async function getConversation(id: string) {
  await requirePermission("communications.read");

  const [conversation, messagesPayload] = await Promise.all([
    gatewayFetch<PlatformConversation>(`/api/v1/messages/conversations/${encodeURIComponent(id)}`),
    gatewayFetch<unknown>(`/api/v1/messages/conversations/${encodeURIComponent(id)}/messages`, {
      query: { limit: 100 },
    }),
  ]);

  return { conversation, messages: unwrap<PlatformMessage>(messagesPayload, "data") };
}

/**
 * Candidate names for a set of threads.
 *
 * A conversation carries the candidate's *account id* and nothing else — the
 * messaging service deliberately does not duplicate profile data. The names the
 * list shows therefore come from the applications this company already holds,
 * which is the only place it legitimately knows a candidate's name. Anyone with
 * no application shows as their thread subject instead of a wrong name.
 */
async function candidateNames(conversations: PlatformConversation[]): Promise<Map<string, { name: string; email: string }>> {
  const names = new Map<string, { name: string; email: string }>();
  if (conversations.length === 0) return names;

  try {
    const payload = await gatewayFetch<unknown>("/api/v1/applications", { query: { limit: 100 } });
    for (const application of unwrap<{ candidateId: string; candidateName: string; candidateEmail: string }>(
      payload,
      "applications",
    )) {
      if (application.candidateId) {
        names.set(application.candidateId, {
          name: application.candidateName ?? "",
          email: application.candidateEmail ?? "",
        });
      }
    }
  } catch {
    // The thread list is still useful without names.
  }
  return names;
}

/**
 * Delivery history, as the conversation list.
 *
 * The old screen listed individual outbound emails. The nearest true thing the
 * platform has is the latest message on each thread, which is what is returned
 * here in the shape that screen already reads.
 */
export async function getCandidateMessages() {
  await requirePermission("communications.view_history");

  const conversations = await getConversations();
  const names = await candidateNames(conversations);

  return conversations.map((conversation) => {
    const who = names.get(conversation.candidateAccountId ?? "");
    return {
      id: conversation.id,
      candidateId: conversation.candidateAccountId ?? "",
      recipientEmail: who?.email ?? "",
      subject: conversation.subject ?? "",
      body: conversation.lastMessagePreview ?? "",
      // `unreadCount` on a recruiter's view counts what the recruiter has not
      // read, so a thread with unread replies is the one needing attention.
      status: conversation.unreadCount > 0 ? "unread" : "read",
      sentAt: conversation.lastActivityAt ? new Date(conversation.lastActivityAt) : null,
      candidateName: who?.name ?? "",
      senderName: conversation.lastMessageSender ?? "",
    };
  });
}

export async function markConversationRead(id: string) {
  await requirePermission("communications.read");
  await gatewayFetch<void>(`/api/v1/messages/conversations/${encodeURIComponent(id)}/read`, {
    method: "POST",
    body: {},
  });
  revalidatePath("/communications");
  return { success: true };
}

/* -------------------------------------------------------------------------- */
/* Sending                                                                    */
/* -------------------------------------------------------------------------- */

export async function sendMessageToCandidate(data: {
  candidateId: string;
  conversationId?: string;
  templateId?: string;
  recipientEmail?: string;
  subject: string;
  body: string;
}) {
  const { user } = await requirePermission("communications.send");

  // An existing thread is replied to; a new one is opened against the
  // candidate. Messaging fans the message out to email itself, so there is no
  // SMTP transport here any more — and no company credentials to hold.
  let conversationId = data.conversationId;

  if (!conversationId) {
    const opened = await gatewayFetch<{ conversation: PlatformConversation }>(
      "/api/v1/messages/conversations",
      {
        method: "POST",
        body: {
          // The messaging service keys a thread on the candidate's identity
          // account, which is what an application carries as candidateId.
          candidateAccountId: data.candidateId,
          subject: data.subject,
          body: data.body,
        },
        // A double-submitted send must not open two threads.
        idempotencyKey: `open:${data.candidateId}:${data.subject}`,
      },
    );
    conversationId = opened.conversation.id;
  } else {
    await gatewayFetch<PlatformMessage>(
      `/api/v1/messages/conversations/${encodeURIComponent(conversationId)}/messages`,
      {
        method: "POST",
        body: { body: data.body },
        idempotencyKey: `msg:${conversationId}:${data.body.slice(0, 64)}`,
      },
    );
  }

  await recordAuditLog({
    actorId: user.id,
    orgId: user.orgId,
    action: "communications.message_sent",
    entityType: "conversation",
    entityId: conversationId ?? "",
    metadata: { candidateId: data.candidateId },
  });

  revalidatePath("/communications");
  return { success: true, sentViaSmtp: false, conversationId };
}
