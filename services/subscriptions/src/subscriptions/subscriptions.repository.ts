/**
 * Subscription persistence.
 *
 * Every tenant-scoped statement takes the company id as an explicit argument
 * and puts it in the WHERE clause — never a load-then-check. `WHERE id = $1 AND
 * company_id = $2` cannot be forgotten at a later call site, and it answers
 * "not found" instead of confirming that another tenant's row exists.
 *
 * Platform-principal reads are the deliberate exception: an operator listing
 * every company's subscription has no tenant of their own, so those methods
 * take an optional filter rather than a required one. They are separate methods
 * so the difference is visible at the call site rather than hidden in a
 * nullable parameter.
 */
import { Inject, Injectable } from '@nestjs/common';
import { buildPage, conflict, type Page, type PageRequest } from '@karmarat/nestshared';
import type { Pool, PoolClient } from 'pg';

import { PG_POOL } from '../common/infrastructure.module';
import { newId } from '../common/ids';
import { toMinorUnits } from '../common/money';
import { readStoredEntitlements, type Entitlements } from '../entitlements/entitlements';
import type { PlanInterval } from '../entitlements/duration';
import type { Subscription, SubscriptionState } from './subscription.entity';

const COLUMNS = `
  id, company_id, plan_id, state, started_at, current_period_start, current_period_end,
  expires_at, trial_ends_at, cancel_at_period_end, cancelled_at, entitlements,
  price_amount, price_currency, plan_interval, plan_interval_count,
  created_at, updated_at`;

interface Row {
  id: string;
  company_id: string;
  plan_id: string;
  state: SubscriptionState;
  started_at: Date | null;
  current_period_start: Date | null;
  current_period_end: Date | null;
  expires_at: Date | null;
  trial_ends_at: Date | null;
  cancel_at_period_end: boolean;
  cancelled_at: Date | null;
  entitlements: unknown;
  price_amount: string;
  price_currency: string;
  plan_interval: PlanInterval;
  plan_interval_count: number;
  created_at: Date;
  updated_at: Date;
}

function toSubscription(row: Row): Subscription {
  return {
    id: row.id,
    companyId: row.company_id,
    planId: row.plan_id,
    state: row.state,
    startedAt: row.started_at,
    currentPeriodStart: row.current_period_start,
    currentPeriodEnd: row.current_period_end,
    expiresAt: row.expires_at,
    trialEndsAt: row.trial_ends_at,
    cancelAtPeriodEnd: row.cancel_at_period_end,
    cancelledAt: row.cancelled_at,
    entitlements: readStoredEntitlements(row.entitlements),
    priceAmount: toMinorUnits(row.price_amount),
    priceCurrency: row.price_currency.trim(),
    planInterval: row.plan_interval,
    planIntervalCount: row.plan_interval_count,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
}

export interface CreateSubscriptionInput {
  companyId: string;
  planId: string;
  state: SubscriptionState;
  startedAt: Date | null;
  currentPeriodStart: Date | null;
  currentPeriodEnd: Date | null;
  expiresAt: Date | null;
  trialEndsAt: Date | null;
  entitlements: Entitlements;
  priceAmount: number;
  priceCurrency: string;
  planInterval: PlanInterval;
  planIntervalCount: number;
  idempotencyKey?: string | null;
}

export interface SubscriptionPatch {
  planId?: string;
  state?: SubscriptionState;
  startedAt?: Date | null;
  currentPeriodStart?: Date | null;
  currentPeriodEnd?: Date | null;
  expiresAt?: Date | null;
  trialEndsAt?: Date | null;
  cancelAtPeriodEnd?: boolean;
  cancelledAt?: Date | null;
  entitlements?: Entitlements;
  priceAmount?: number;
  priceCurrency?: string;
  planInterval?: PlanInterval;
  planIntervalCount?: number;
  idempotencyKey?: string | null;
}

export interface ListSubscriptionsFilter extends PageRequest {
  companyId?: string;
  states?: SubscriptionState[];
}

@Injectable()
export class SubscriptionsRepository {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  async create(input: CreateSubscriptionInput, client?: PoolClient): Promise<Subscription> {
    const executor = client ?? this.pool;
    const id = newId('sub');

    try {
      const { rows } = await executor.query<Row>(
        `INSERT INTO subscriptions (
            id, company_id, plan_id, state, started_at, current_period_start,
            current_period_end, expires_at, trial_ends_at, entitlements,
            price_amount, price_currency, plan_interval, plan_interval_count,
            idempotency_key
         ) VALUES ($1,$2::uuid,$3,$4::subscription_state,$5,$6,$7,$8,$9,$10::jsonb,
                   $11,$12,$13::plan_interval,$14,$15)
         RETURNING ${COLUMNS}`,
        [
          id,
          input.companyId,
          input.planId,
          input.state,
          input.startedAt,
          input.currentPeriodStart,
          input.currentPeriodEnd,
          input.expiresAt,
          input.trialEndsAt,
          JSON.stringify(input.entitlements),
          input.priceAmount,
          input.priceCurrency,
          input.planInterval,
          input.planIntervalCount,
          input.idempotencyKey ?? null,
        ],
      );
      return toSubscription(rows[0]!);
    } catch (error) {
      throw translateConflict(error);
    }
  }

  /** Platform read: any tenant's subscription by id. */
  async findById(id: string, client?: PoolClient): Promise<Subscription | null> {
    const executor = client ?? this.pool;
    const { rows } = await executor.query<Row>(
      `SELECT ${COLUMNS} FROM subscriptions WHERE id = $1`,
      [id],
    );
    return rows[0] ? toSubscription(rows[0]) : null;
  }

  /**
   * Company read: a subscription that must belong to this tenant.
   *
   * The tenant predicate is in the statement rather than checked afterwards, so
   * another company's id returns null — indistinguishable from an id that does
   * not exist, which is the point.
   */
  async findByIdForCompany(id: string, companyId: string): Promise<Subscription | null> {
    const { rows } = await this.pool.query<Row>(
      `SELECT ${COLUMNS} FROM subscriptions WHERE id = $1 AND company_id = $2::uuid`,
      [id, companyId],
    );
    return rows[0] ? toSubscription(rows[0]) : null;
  }

  /** The one subscription currently occupying a company's live slot, if any. */
  async findLiveForCompany(companyId: string, client?: PoolClient): Promise<Subscription | null> {
    const executor = client ?? this.pool;
    const { rows } = await executor.query<Row>(
      `SELECT ${COLUMNS} FROM subscriptions
        WHERE company_id = $1::uuid
          AND state IN ('pending','trialing','active','past_due')
        LIMIT 1`,
      [companyId],
    );
    return rows[0] ? toSubscription(rows[0]) : null;
  }

  /**
   * The subscription a company's billing page should show.
   *
   * Falls back to the most recent terminal one when nothing is live, so a
   * company that just cancelled sees what it had rather than an empty page.
   */
  async findCurrentForCompany(companyId: string): Promise<Subscription | null> {
    const { rows } = await this.pool.query<Row>(
      `SELECT ${COLUMNS} FROM subscriptions
        WHERE company_id = $1::uuid
        ORDER BY
          CASE WHEN state IN ('trialing','active','past_due') THEN 0
               WHEN state = 'pending' THEN 1
               ELSE 2 END,
          created_at DESC, id DESC
        LIMIT 1`,
      [companyId],
    );
    return rows[0] ? toSubscription(rows[0]) : null;
  }

  async findByIdempotencyKey(companyId: string, key: string): Promise<Subscription | null> {
    const { rows } = await this.pool.query<Row>(
      `SELECT ${COLUMNS} FROM subscriptions
        WHERE company_id = $1::uuid AND idempotency_key = $2`,
      [companyId, key],
    );
    return rows[0] ? toSubscription(rows[0]) : null;
  }

  async list(filter: ListSubscriptionsFilter): Promise<Page<Subscription>> {
    const params: unknown[] = [];
    const where: string[] = [];

    if (filter.companyId !== undefined) {
      params.push(filter.companyId);
      where.push(`company_id = $${params.length}::uuid`);
    }

    if (filter.states?.length) {
      params.push(filter.states);
      where.push(`state = ANY($${params.length}::subscription_state[])`);
    }

    if (filter.cursor) {
      params.push(filter.cursor.createdAt, filter.cursor.id);
      where.push(`(created_at, id) < ($${params.length - 1}::timestamptz, $${params.length})`);
    }

    params.push(filter.limit + 1);

    const { rows } = await this.pool.query<Row>(
      `SELECT ${COLUMNS} FROM subscriptions
       ${where.length ? `WHERE ${where.join(' AND ')}` : ''}
       ORDER BY created_at DESC, id DESC
       LIMIT $${params.length}`,
      params,
    );

    return buildPage(rows.map(toSubscription), filter.limit);
  }

  async update(id: string, patch: SubscriptionPatch, client?: PoolClient): Promise<Subscription | null> {
    const executor = client ?? this.pool;
    const assignments: string[] = [];
    const params: unknown[] = [];

    const set = (column: string, value: unknown, cast = '') => {
      params.push(value);
      assignments.push(`${column} = $${params.length}${cast}`);
    };

    if (patch.planId !== undefined) set('plan_id', patch.planId);
    if (patch.state !== undefined) set('state', patch.state, '::subscription_state');
    if (patch.startedAt !== undefined) set('started_at', patch.startedAt);
    if (patch.currentPeriodStart !== undefined) set('current_period_start', patch.currentPeriodStart);
    if (patch.currentPeriodEnd !== undefined) set('current_period_end', patch.currentPeriodEnd);
    if (patch.expiresAt !== undefined) set('expires_at', patch.expiresAt);
    if (patch.trialEndsAt !== undefined) set('trial_ends_at', patch.trialEndsAt);
    if (patch.cancelAtPeriodEnd !== undefined) set('cancel_at_period_end', patch.cancelAtPeriodEnd);
    if (patch.cancelledAt !== undefined) set('cancelled_at', patch.cancelledAt);
    if (patch.entitlements !== undefined) set('entitlements', JSON.stringify(patch.entitlements), '::jsonb');
    if (patch.priceAmount !== undefined) set('price_amount', patch.priceAmount);
    if (patch.priceCurrency !== undefined) set('price_currency', patch.priceCurrency);
    if (patch.planInterval !== undefined) set('plan_interval', patch.planInterval, '::plan_interval');
    if (patch.planIntervalCount !== undefined) set('plan_interval_count', patch.planIntervalCount);
    if (patch.idempotencyKey !== undefined) set('idempotency_key', patch.idempotencyKey);

    if (assignments.length === 0) {
      return this.findById(id, client);
    }

    params.push(id);

    try {
      const { rows } = await executor.query<Row>(
        `UPDATE subscriptions SET ${assignments.join(', ')}, updated_at = now()
          WHERE id = $${params.length}
          RETURNING ${COLUMNS}`,
        params,
      );
      return rows[0] ? toSubscription(rows[0]) : null;
    } catch (error) {
      throw translateConflict(error);
    }
  }

  async delete(id: string): Promise<boolean> {
    const result = await this.pool.query('DELETE FROM subscriptions WHERE id = $1', [id]);
    return (result.rowCount ?? 0) > 0;
  }

  /**
   * Claims every subscription whose expiry has passed, in one statement.
   *
   * A single UPDATE ... RETURNING is what makes the sweep idempotent: the state
   * predicate means a row can only be claimed once, so a second run — or a
   * second replica that slipped past the advisory lock — returns nothing rather
   * than publishing the expiry twice.
   *
   * A subscription that was set to cancel at period end becomes `cancelled`
   * rather than `expired`, because that is what actually happened; the company
   * asked to leave, it did not lapse.
   */
  async claimExpired(now: Date, batchSize: number, client: PoolClient): Promise<Subscription[]> {
    const { rows } = await client.query<Row>(
      `UPDATE subscriptions
          SET state = CASE WHEN cancel_at_period_end THEN 'cancelled'::subscription_state
                           ELSE 'expired'::subscription_state END,
              cancelled_at = CASE WHEN cancel_at_period_end THEN now() ELSE cancelled_at END,
              updated_at = now()
        WHERE id IN (
              SELECT id FROM subscriptions
               WHERE expires_at IS NOT NULL
                 AND expires_at <= $1
                 AND state IN ('trialing','active','past_due')
               ORDER BY expires_at
               LIMIT $2
               FOR UPDATE SKIP LOCKED
        )
        RETURNING ${COLUMNS}`,
      [now, batchSize],
    );
    return rows.map(toSubscription);
  }

  /**
   * Abandons pending subscriptions that never produced a payment.
   *
   * Without this, one abandoned checkout would occupy the company's single live
   * slot forever and every later subscribe attempt would 409 for a reason the
   * company cannot see or fix.
   */
  async expireStalePending(before: Date, client: PoolClient): Promise<number> {
    const result = await client.query(
      `UPDATE subscriptions
          SET state = 'expired', updated_at = now()
        WHERE state = 'pending' AND created_at < $1`,
      [before],
    );
    return result.rowCount ?? 0;
  }
}

/**
 * Maps the database's integrity errors onto the conflicts a client can act on.
 *
 * The driver's message names the index and quotes the row; neither belongs in a
 * response body, so each case gets a written explanation instead.
 */
function translateConflict(error: unknown): unknown {
  const code = (error as { code?: string }).code;
  const constraint = (error as { constraint?: string }).constraint ?? '';

  if (code === '23505' && constraint === 'subscriptions_one_live_per_company') {
    return conflict(
      'subscription_exists',
      'This company already has a subscription in progress. Cancel it before starting another.',
    );
  }

  if (code === '23505' && constraint === 'subscriptions_idempotency_idx') {
    return conflict(
      'idempotency_key_reused',
      'That Idempotency-Key has already been used for a different request.',
    );
  }

  if (code === '23503') {
    return conflict('plan_missing', 'That plan does not exist.');
  }

  return error;
}
