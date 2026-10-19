/**
 * Wires the consumer to the bus, and keeps the audit table from growing forever.
 *
 * The durable consumer is created here rather than in the consumer class so the
 * class stays a pure function of an envelope — which is what makes it testable
 * without NATS.
 *
 * Subscribing at module init means the service is consuming before it accepts
 * HTTP traffic. That ordering is deliberate: a console that answers requests
 * while its read model is still catching up shows an operator a figure that is
 * about to change under them, and JetStream's backlog means "catching up" can
 * be thousands of events after a deploy.
 */
import {
  Inject,
  Logger,
  Module,
  type OnApplicationShutdown,
  type OnModuleInit,
} from '@nestjs/common';
import { EventBus } from '@karmarat/nestshared';

import { ADMIN_CONFIG, EVENT_BUS } from '../common/tokens';
import type { AdminConfig } from '../config';
import { ProjectionConsumer, subscribedSubjects } from './projection.consumer';
import { ProjectionRepository } from './projection.repository';

/** The one thing a subscription handle is used for after it is created. */
interface Stoppable {
  stop(): void;
}

@Module({
  providers: [ProjectionConsumer, ProjectionRepository],
  exports: [ProjectionRepository],
})
export class ProjectionModule implements OnModuleInit, OnApplicationShutdown {
  private readonly logger = new Logger('projection');
  private subscription: Stoppable | null = null;
  private pruner: NodeJS.Timeout | null = null;

  constructor(
    @Inject(EVENT_BUS) private readonly bus: EventBus,
    @Inject(ADMIN_CONFIG) private readonly config: AdminConfig,
    private readonly consumer: ProjectionConsumer,
    private readonly repository: ProjectionRepository,
  ) {}

  async onModuleInit(): Promise<void> {
    this.subscription = await this.bus.subscribe('projection', subscribedSubjects(), (envelope) =>
      this.consumer.handle(envelope),
    );

    this.pruner = setInterval(() => void this.prune(), this.config.activityPruneIntervalMs);
    // The pruner must never be the reason the process stays alive; without this
    // a SIGTERM'd service waits out the interval before exiting.
    this.pruner.unref();
  }

  onApplicationShutdown(): void {
    if (this.pruner) clearTimeout(this.pruner);
    // Stops the consume loop so in-flight messages are not acked against a pool
    // that is about to close. Unacked messages are simply redelivered, which is
    // what the whole consumer is built to tolerate.
    this.subscription?.stop();
  }

  private async prune(): Promise<void> {
    try {
      const removed = await this.repository.pruneActivity(this.config.activityRetentionDays);
      if (removed > 0) {
        this.logger.log(`pruned ${removed} audit rows older than ${this.config.activityRetentionDays} days`);
      }
    } catch (error) {
      // A failed prune is a disk-space problem eventually, never a correctness
      // one, so it is logged and retried on the next tick rather than escalated.
      this.logger.warn(`audit prune failed: ${(error as Error).message}`);
    }
  }
}
