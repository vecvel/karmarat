/**
 * Plan lifecycle rules.
 *
 * The rule worth stating out loud: a plan somebody has bought is not deletable.
 * Deleting it would orphan every subscription's plan_id and destroy the record
 * of what was sold, which is the one thing a billing dispute turns on. Retiring
 * withdraws the plan from sale and leaves existing subscribers exactly as they
 * were — and because each subscription snapshots its own entitlements, retiring
 * does not change what any of them may do.
 */
import { Inject, Injectable, Logger } from '@nestjs/common';
import {
  EventBus,
  Subject,
  badRequest,
  conflict,
  notFound,
  parsePageRequest,
  validationFailed,
  type Page,
} from '@karmarat/nestshared';

import { EVENT_BUS } from '../common/infrastructure.module';
import { normaliseCurrency } from '../common/money';
import { isPlanInterval, type PlanInterval } from '../entitlements/duration';
import { parseEntitlements } from '../entitlements/entitlements';
import type { CreatePlanDto, ListPlansQueryDto, UpdatePlanDto } from './dto/plan.dto';
import type { Plan, PlanState } from './plan.entity';
import { PlansRepository } from './plans.repository';

@Injectable()
export class PlansService {
  private readonly logger = new Logger(PlansService.name);

  constructor(
    private readonly plans: PlansRepository,
    @Inject(EVENT_BUS) private readonly bus: EventBus,
  ) {}

  async create(dto: CreatePlanDto): Promise<Plan> {
    const interval = this.readInterval(dto.interval);
    const intervalCount = this.readIntervalCount(interval, dto.intervalCount);

    return this.plans.create({
      key: dto.key.toLowerCase(),
      name: dto.name.trim(),
      description: dto.description?.trim() ?? '',
      priceAmount: dto.priceAmount,
      priceCurrency: normaliseCurrency(dto.currency),
      interval,
      intervalCount,
      trialDays: dto.trialDays ?? 0,
      entitlements: parseEntitlements(dto.entitlements),
      sortOrder: dto.sortOrder ?? 0,
    });
  }

  async get(id: string): Promise<Plan> {
    const plan = await this.plans.findById(id);
    if (!plan) throw notFound('That plan does not exist.');
    return plan;
  }

  async list(query: ListPlansQueryDto): Promise<Page<Plan>> {
    const page = parsePageRequest({
      ...(query.limit !== undefined ? { limit: query.limit } : {}),
      ...(query.cursor !== undefined ? { cursor: query.cursor } : {}),
    });

    return this.plans.list({
      ...page,
      ...(query.state ? { states: [query.state as PlanState] } : {}),
    });
  }

  async listPublic(): Promise<Plan[]> {
    return this.plans.listPublished();
  }

  /**
   * Edits a plan.
   *
   * Editing a published plan is allowed: the price on the pricing page has to
   * be changeable without withdrawing the product. It is safe precisely because
   * existing subscriptions hold their own entitlement and price snapshots, so
   * the edit governs who buys next and nobody who already did.
   */
  async update(id: string, dto: UpdatePlanDto): Promise<Plan> {
    const existing = await this.get(id);

    const interval = dto.interval !== undefined ? this.readInterval(dto.interval) : existing.interval;
    const intervalCount =
      dto.intervalCount !== undefined || dto.interval !== undefined
        ? this.readIntervalCount(interval, dto.intervalCount ?? existing.intervalCount)
        : undefined;

    const updated = await this.plans.update(id, {
      ...(dto.key !== undefined ? { key: dto.key.toLowerCase() } : {}),
      ...(dto.name !== undefined ? { name: dto.name.trim() } : {}),
      ...(dto.description !== undefined ? { description: dto.description.trim() } : {}),
      ...(dto.priceAmount !== undefined ? { priceAmount: dto.priceAmount } : {}),
      ...(dto.currency !== undefined ? { priceCurrency: normaliseCurrency(dto.currency) } : {}),
      ...(dto.interval !== undefined ? { interval } : {}),
      ...(intervalCount !== undefined ? { intervalCount } : {}),
      ...(dto.trialDays !== undefined ? { trialDays: dto.trialDays } : {}),
      ...(dto.entitlements !== undefined ? { entitlements: parseEntitlements(dto.entitlements) } : {}),
      ...(dto.sortOrder !== undefined ? { sortOrder: dto.sortOrder } : {}),
    });

    if (!updated) throw notFound('That plan does not exist.');
    return updated;
  }

  /** Offers a plan to companies. */
  async publish(id: string): Promise<Plan> {
    const plan = await this.get(id);

    if (plan.state === 'published') {
      return plan; // Idempotent: publishing twice is not an error worth a 409.
    }

    // A plan with no entitlements at all would open a portal that can do
    // nothing, and the company would blame the product rather than the
    // configuration. Refuse it here, while an operator is looking at the form.
    const entitlements = plan.entitlements;
    if (entitlements.maxJobs === 0 && entitlements.maxRecruiters === 0) {
      throw validationFailed({
        entitlements: ['a published plan must allow at least one job or one recruiter.'],
      });
    }

    const published = await this.plans.setState(id, 'published');
    if (!published) throw notFound('That plan does not exist.');

    await this.publishEvent(Subject.PlanPublished, published);
    return published;
  }

  /**
   * Withdraws a plan from sale.
   *
   * Companies already on it keep it until they cancel or it lapses; only the
   * pricing page and the subscribe endpoint change their minds.
   */
  async retire(id: string): Promise<Plan> {
    const plan = await this.get(id);

    if (plan.state === 'retired') {
      return plan;
    }

    const retired = await this.plans.setState(id, 'retired');
    if (!retired) throw notFound('That plan does not exist.');

    await this.publishEvent(Subject.PlanRetired, retired);
    return retired;
  }

  /**
   * Deletes a plan, which is only ever allowed for one nobody has bought.
   *
   * The check and the delete are not in a transaction, and they do not need to
   * be: subscribing requires the plan to be published, and a published plan is
   * already refused here, so the only window is on a draft plan that no
   * company can reach.
   */
  async delete(id: string): Promise<void> {
    const plan = await this.get(id);
    const { total, live } = await this.plans.subscriberCounts(id);

    if (live > 0) {
      throw conflict(
        'plan_in_use',
        `${live} ${live === 1 ? 'company is' : 'companies are'} subscribed to this plan. ` +
          'Retire it instead — retiring withdraws it from sale and leaves existing subscribers unaffected.',
      );
    }

    if (total > 0) {
      throw conflict(
        'plan_has_history',
        'This plan has past subscriptions and cannot be deleted, because doing so would erase ' +
          'the record of what those companies bought. Retire it instead.',
      );
    }

    if (plan.state === 'published') {
      throw conflict(
        'plan_published',
        'A published plan cannot be deleted while it is on offer. Retire it first.',
      );
    }

    const deleted = await this.plans.delete(id);
    if (!deleted) throw notFound('That plan does not exist.');

    this.logger.log(`deleted plan ${plan.key}`);
  }

  private readInterval(raw: string): PlanInterval {
    if (!isPlanInterval(raw)) {
      throw badRequest('interval must be one of month, year, days or lifetime.');
    }
    return raw;
  }

  /**
   * A lifetime plan has nothing to count, so an interval count on one is a
   * number that would later be believed by somebody computing a renewal.
   */
  private readIntervalCount(interval: PlanInterval, count: number | undefined): number {
    if (interval === 'lifetime') {
      if (count !== undefined && count !== 1) {
        throw validationFailed({
          intervalCount: ['must be omitted for a lifetime plan, which never recurs.'],
        });
      }
      return 1;
    }
    return count ?? 1;
  }

  private async publishEvent(subject: string, plan: Plan): Promise<void> {
    await this.bus.publish(
      subject,
      {
        planId: plan.id,
        key: plan.key,
        name: plan.name,
        state: plan.state,
        price: { amount: plan.priceAmount, currency: plan.priceCurrency },
        interval: plan.interval,
        intervalCount: plan.intervalCount,
        entitlements: plan.entitlements,
      },
      // Deterministic within JetStream's de-duplication window, so a retried
      // publish after a network blip delivers the fact once.
      { id: `plan-${plan.state}-${plan.id}-${plan.updatedAt.getTime()}` },
    );
  }
}
