/**
 * NATS JetStream publishing and consumption, wire-compatible with
 * `packages/goshared/events`.
 *
 * Same envelope, same subjects, same de-duplication by event id. A Go consumer
 * must be able to read what a Node service publishes and vice versa, so the
 * JSON field names here are not a style choice — they are the contract.
 */
import {
  AckPolicy,
  DeliverPolicy,
  JSONCodec,
  RetentionPolicy,
  connect,
  type ConsumerMessages,
  type JetStreamClient,
  type JetStreamManager,
  type NatsConnection,
} from 'nats';
import { randomBytes } from 'node:crypto';

/** Must match StreamName in packages/goshared/events/events.go. */
export const STREAM_NAME = 'KARMARAT';
export const ALL_SUBJECTS = 'karmarat.>';

/** Every subject the platform publishes. Keep in step with the Go constants. */
export const Subject = {
  CompanyRegistered: 'karmarat.company.registered',
  CompanyApproved: 'karmarat.company.approved',
  CompanySuspended: 'karmarat.company.suspended',
  CompanyUpdated: 'karmarat.company.updated',

  SubscriptionActivated: 'karmarat.subscription.activated',
  SubscriptionRenewed: 'karmarat.subscription.renewed',
  SubscriptionExpired: 'karmarat.subscription.expired',
  SubscriptionCancelled: 'karmarat.subscription.cancelled',

  PaymentSucceeded: 'karmarat.payment.succeeded',
  PaymentFailed: 'karmarat.payment.failed',

  PlanPublished: 'karmarat.plan.published',
  PlanRetired: 'karmarat.plan.retired',

  JobPublished: 'karmarat.job.published',
  JobUnpublished: 'karmarat.job.unpublished',
  JobClosed: 'karmarat.job.closed',

  ApplicationSubmitted: 'karmarat.application.submitted',
  ApplicationStageChanged: 'karmarat.application.stage_changed',
  ApplicationRejected: 'karmarat.application.rejected',
  ApplicationWithdrawn: 'karmarat.application.withdrawn',
  ApplicationHired: 'karmarat.application.hired',

  InterviewScheduled: 'karmarat.interview.scheduled',
  InterviewCancelled: 'karmarat.interview.cancelled',
  InterviewCompleted: 'karmarat.interview.completed',

  OfferSent: 'karmarat.offer.sent',
  OfferAccepted: 'karmarat.offer.accepted',
  OfferDeclined: 'karmarat.offer.declined',

  CandidateRegistered: 'karmarat.candidate.registered',
  CandidateVisibilityChanged: 'karmarat.candidate.visibility_changed',
  CandidateProfileUpdated: 'karmarat.candidate.profile_updated',
  CandidateApproached: 'karmarat.candidate.approached',

  MessageSent: 'karmarat.message.sent',
  ConversationOpened: 'karmarat.message.conversation_opened',

  UserDeactivated: 'karmarat.user.deactivated',
  SessionRevoked: 'karmarat.session.revoked',

  SupportTicketCreated: 'karmarat.support.ticket_created',
  SupportTicketReplied: 'karmarat.support.ticket_replied',

  NotificationRequested: 'karmarat.notification.requested',
} as const;

export interface Envelope<T = unknown> {
  id: string;
  subject: string;
  occurredAt: string;
  companyId?: string;
  actorId?: string;
  correlationId?: string;
  payload: T;
}

export interface PublishOptions {
  companyId?: string;
  actorId?: string;
  correlationId?: string;
  /** Overrides the generated id; pass a deterministic one to de-duplicate retries. */
  id?: string;
}

export type Handler<T = unknown> = (envelope: Envelope<T>) => Promise<void>;

/** Prefixed ULID-ish id, matching the shape idgen produces in Go. */
function newId(prefix: string): string {
  return `${prefix}_${randomBytes(13).toString('hex').toUpperCase()}`;
}

const codec = JSONCodec();

export class EventBus {
  private constructor(
    private readonly conn: NatsConnection,
    private readonly js: JetStreamClient,
    private readonly serviceName: string,
    private readonly logger: { log: (m: string) => void; error: (m: string) => void },
  ) {}

  static async connect(
    url: string,
    serviceName: string,
    logger: { log: (m: string) => void; error: (m: string) => void } = console,
  ): Promise<EventBus> {
    const conn = await connect({
      servers: url,
      name: serviceName,
      // Reconnect indefinitely: a broker restart should not take every service
      // with it, and JetStream retains what was missed.
      maxReconnectAttempts: -1,
      reconnectTimeWait: 2_000,
    });

    const manager: JetStreamManager = await conn.jetstreamManager();
    await manager.streams
      .add({
        name: STREAM_NAME,
        subjects: [ALL_SUBJECTS],
        retention: RetentionPolicy.Limits,
        max_age: 30 * 24 * 60 * 60 * 1_000_000_000, // 30 days, nanoseconds
        duplicate_window: 2 * 60 * 1_000_000_000, // 2 minutes
      })
      .catch(() => manager.streams.info(STREAM_NAME));

    logger.log(`event bus connected: ${url}`);
    return new EventBus(conn, conn.jetstream(), serviceName, logger);
  }

  async publish(subject: string, payload: unknown, options: PublishOptions = {}): Promise<void> {
    const envelope: Envelope = {
      id: options.id ?? newId('evt'),
      subject,
      occurredAt: new Date().toISOString(),
      ...(options.companyId ? { companyId: options.companyId } : {}),
      ...(options.actorId ? { actorId: options.actorId } : {}),
      ...(options.correlationId ? { correlationId: options.correlationId } : {}),
      payload,
    };

    // msgID is what makes a retry after a network blip deliver the fact once.
    await this.js.publish(subject, codec.encode(envelope), { msgID: envelope.id });
  }

  /**
   * Consumes a durable subscription.
   *
   * A handler that throws nak's the message so JetStream redelivers it, which
   * is why every consumer must be idempotent — a redelivery is normal, not
   * exceptional.
   */
  async subscribe<T>(durable: string, subjects: string[], handler: Handler<T>): Promise<ConsumerMessages> {
    const manager = await this.conn.jetstreamManager();
    const durableName = `${this.serviceName}-${durable}`;

    const config = {
      durable_name: durableName,
      ack_policy: AckPolicy.Explicit,
      deliver_policy: DeliverPolicy.All,
      filter_subjects: subjects,
      max_deliver: 5,
      ack_wait: 30 * 1_000_000_000,
    };

    // Create, then UPDATE if it already exists. Falling back to info() would
    // leave a durable consumer pinned to the subject list it was first created
    // with: adding an event to a service's Subjects() would appear to work and
    // silently never deliver. The Go side uses CreateOrUpdateConsumer for the
    // same reason.
    try {
      await manager.consumers.add(STREAM_NAME, config);
    } catch {
      await manager.consumers.update(STREAM_NAME, durableName, config);
    }

    const consumer = await this.js.consumers.get(STREAM_NAME, durableName);
    const messages = await consumer.consume();

    void (async () => {
      for await (const message of messages) {
        try {
          const envelope = codec.decode(message.data) as Envelope<T>;
          await handler(envelope);
          message.ack();
        } catch (error) {
          this.logger.error(`event handler failed for ${message.subject}: ${(error as Error).message}`);
          // Back off rather than hot-looping a poison message.
          message.nak(5_000);
        }
      }
    })();

    this.logger.log(`subscribed ${durableName} to ${subjects.join(', ')}`);
    return messages;
  }

  healthCheck(): () => Promise<void> {
    return async () => {
      if (this.conn.isClosed()) {
        throw new Error('event bus connection is closed');
      }
      await this.conn.flush();
    };
  }

  async close(): Promise<void> {
    await this.conn.drain();
  }
}
