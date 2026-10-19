/**
 * Plan persistence.
 *
 * Plans are the platform's own catalogue, not tenant data, so these queries
 * carry no company_id — the only tables in this service that do not. The
 * tenant filter appears one layer down, in subscriptions.repository.ts, where
 * every statement has one.
 */
import { Inject, Injectable } from '@nestjs/common';
import { buildPage, conflict, type Page, type PageRequest } from '@karmarat/nestshared';
import type { Pool, PoolClient } from 'pg';

import { PG_POOL } from '../common/infrastructure.module';
import { newId } from '../common/ids';
import { toMinorUnits } from '../common/money';
import { readStoredEntitlements, type Entitlements } from '../entitlements/entitlements';
import type { PlanInterval } from '../entitlements/duration';
import type { Plan, PlanState } from './plan.entity';

const COLUMNS = `
  id, key, name, description, price_amount, price_currency,
  interval, interval_count, trial_days, entitlements, state, sort_order,
  published_at, retired_at, created_at, updated_at`;

interface Row {
  id: string;
  key: string;
  name: string;
  description: string;
  price_amount: string;
  price_currency: string;
  interval: PlanInterval;
  interval_count: number;
  trial_days: number;
  entitlements: unknown;
  state: PlanState;
  sort_order: number;
  published_at: Date | null;
  retired_at: Date | null;
  created_at: Date;
  updated_at: Date;
}

function toPlan(row: Row): Plan {
  return {
    id: row.id,
    key: row.key,
    name: row.name,
    description: row.description,
    priceAmount: toMinorUnits(row.price_amount),
    // char(3) pads with spaces on some drivers; trim before it reaches a client.
    priceCurrency: row.price_currency.trim(),
    interval: row.interval,
    intervalCount: row.interval_count,
    trialDays: row.trial_days,
    entitlements: readStoredEntitlements(row.entitlements),
    state: row.state,
    sortOrder: row.sort_order,
    publishedAt: row.published_at,
    retiredAt: row.retired_at,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
}

export interface CreatePlanInput {
  key: string;
  name: string;
  description: string;
  priceAmount: number;
  priceCurrency: string;
  interval: PlanInterval;
  intervalCount: number;
  trialDays: number;
  entitlements: Entitlements;
  sortOrder: number;
}

export type UpdatePlanInput = Partial<CreatePlanInput>;

export interface ListPlansFilter extends PageRequest {
  states?: PlanState[];
}

@Injectable()
export class PlansRepository {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  async create(input: CreatePlanInput): Promise<Plan> {
    const id = newId('plan');

    try {
      const { rows } = await this.pool.query<Row>(
        `INSERT INTO plans (
            id, key, name, description, price_amount, price_currency,
            interval, interval_count, trial_days, entitlements, sort_order
         ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11)
         RETURNING ${COLUMNS}`,
        [
          id,
          input.key,
          input.name,
          input.description,
          input.priceAmount,
          input.priceCurrency,
          input.interval,
          input.intervalCount,
          input.trialDays,
          JSON.stringify(input.entitlements),
          input.sortOrder,
        ],
      );
      return toPlan(rows[0]!);
    } catch (error) {
      throw translateUniqueViolation(error, input.key);
    }
  }

  async findById(id: string): Promise<Plan | null> {
    const { rows } = await this.pool.query<Row>(`SELECT ${COLUMNS} FROM plans WHERE id = $1`, [id]);
    return rows[0] ? toPlan(rows[0]) : null;
  }

  async findByKey(key: string): Promise<Plan | null> {
    const { rows } = await this.pool.query<Row>(
      `SELECT ${COLUMNS} FROM plans WHERE lower(key) = lower($1)`,
      [key],
    );
    return rows[0] ? toPlan(rows[0]) : null;
  }

  /**
   * Lists plans newest first, over the same opaque cursor every list endpoint
   * on this platform uses.
   *
   * The tuple comparison `(created_at, id) < ($2, $3)` is what makes the cursor
   * stable when two plans share a created_at: ordering by created_at alone
   * would let a page boundary fall between two equal timestamps and either skip
   * or repeat a row.
   */
  async list(filter: ListPlansFilter): Promise<Page<Plan>> {
    const params: unknown[] = [];
    const where: string[] = [];

    if (filter.states?.length) {
      params.push(filter.states);
      where.push(`state = ANY($${params.length}::plan_state[])`);
    }

    if (filter.cursor) {
      params.push(filter.cursor.createdAt, filter.cursor.id);
      where.push(`(created_at, id) < ($${params.length - 1}::timestamptz, $${params.length})`);
    }

    // One row beyond the page, so a next cursor can be issued without a count.
    params.push(filter.limit + 1);

    const { rows } = await this.pool.query<Row>(
      `SELECT ${COLUMNS} FROM plans
       ${where.length ? `WHERE ${where.join(' AND ')}` : ''}
       ORDER BY created_at DESC, id DESC
       LIMIT $${params.length}`,
      params,
    );

    return buildPage(rows.map(toPlan), filter.limit);
  }

  /** Published plans for the pricing page, cheapest first. */
  async listPublished(): Promise<Plan[]> {
    const { rows } = await this.pool.query<Row>(
      `SELECT ${COLUMNS} FROM plans
       WHERE state = 'published'
       ORDER BY sort_order ASC, price_amount ASC, name ASC`,
    );
    return rows.map(toPlan);
  }

  async update(id: string, input: UpdatePlanInput): Promise<Plan | null> {
    const assignments: string[] = [];
    const params: unknown[] = [];

    const set = (column: string, value: unknown, cast = '') => {
      params.push(value);
      assignments.push(`${column} = $${params.length}${cast}`);
    };

    if (input.key !== undefined) set('key', input.key);
    if (input.name !== undefined) set('name', input.name);
    if (input.description !== undefined) set('description', input.description);
    if (input.priceAmount !== undefined) set('price_amount', input.priceAmount);
    if (input.priceCurrency !== undefined) set('price_currency', input.priceCurrency);
    if (input.interval !== undefined) set('interval', input.interval, '::plan_interval');
    if (input.intervalCount !== undefined) set('interval_count', input.intervalCount);
    if (input.trialDays !== undefined) set('trial_days', input.trialDays);
    if (input.entitlements !== undefined) set('entitlements', JSON.stringify(input.entitlements), '::jsonb');
    if (input.sortOrder !== undefined) set('sort_order', input.sortOrder);

    if (assignments.length === 0) {
      return this.findById(id);
    }

    params.push(id);

    try {
      const { rows } = await this.pool.query<Row>(
        `UPDATE plans SET ${assignments.join(', ')}, updated_at = now()
         WHERE id = $${params.length}
         RETURNING ${COLUMNS}`,
        params,
      );
      return rows[0] ? toPlan(rows[0]) : null;
    } catch (error) {
      throw translateUniqueViolation(error, input.key ?? '');
    }
  }

  /** Moves a plan's lifecycle state, stamping the matching timestamp. */
  async setState(id: string, state: PlanState): Promise<Plan | null> {
    const { rows } = await this.pool.query<Row>(
      `UPDATE plans
          SET state        = $2::plan_state,
              published_at = CASE WHEN $2 = 'published' THEN now() ELSE published_at END,
              retired_at   = CASE WHEN $2 = 'retired'   THEN now() ELSE NULL END,
              updated_at   = now()
        WHERE id = $1
        RETURNING ${COLUMNS}`,
      [id, state],
    );
    return rows[0] ? toPlan(rows[0]) : null;
  }

  async delete(id: string): Promise<boolean> {
    const result = await this.pool.query('DELETE FROM plans WHERE id = $1', [id]);
    return (result.rowCount ?? 0) > 0;
  }

  /**
   * How many companies are attached to a plan, and how many of those are live.
   *
   * Both numbers matter: a plan with only cancelled subscribers can be deleted,
   * while one with a live subscriber must be retired instead, and the 409 says
   * which case it is.
   */
  async subscriberCounts(planId: string, client?: PoolClient): Promise<{ total: number; live: number }> {
    const executor = client ?? this.pool;
    const { rows } = await executor.query<{ total: string; live: string }>(
      `SELECT count(*)::text AS total,
              count(*) FILTER (
                WHERE state IN ('pending','trialing','active','past_due')
              )::text AS live
         FROM subscriptions
        WHERE plan_id = $1`,
      [planId],
    );
    return { total: Number(rows[0]?.total ?? 0), live: Number(rows[0]?.live ?? 0) };
  }
}

/**
 * Turns the unique-key violation into the 409 a client can act on.
 *
 * The driver's message carries the index name and the offending value; neither
 * belongs in a response body, so only the key the caller already sent is
 * echoed back.
 */
function translateUniqueViolation(error: unknown, key: string): unknown {
  if ((error as { code?: string }).code === '23505') {
    return conflict('plan_key_taken', `A plan with the key "${key}" already exists.`);
  }
  return error;
}
