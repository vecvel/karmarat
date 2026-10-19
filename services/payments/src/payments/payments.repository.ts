/**
 * Payment and refund persistence.
 *
 * Two rules hold in every statement below, and both are load-bearing:
 *
 *  - a tenant-scoped read carries `company_id` **inside** the statement, never
 *    as a check after the row has been fetched, so another tenant's id returns
 *    nothing rather than returning a row we then have to remember to reject;
 *  - amounts cross the driver boundary as strings. `pg` returns `bigint` as a
 *    string and would parse a JS number back as one, so a `number` anywhere in
 *    this file is a rounding bug waiting for a large enough aggregate.
 */
import { Inject, Injectable } from '@nestjs/common';
import { buildPage, conflict, type Page, type PageRequest } from '@karmarat/nestshared';
import type { Pool, PoolClient } from 'pg';

import { PG_POOL } from '../common/infrastructure.module';
import { newId, PaymentIdPrefix, RefundIdPrefix } from '../common/ids';
import { toMinor, toParam } from '../common/money';
import type { Payment, PaymentState, Refund, RefundState } from './payment.entity';

type Executor = Pool | PoolClient;

const PAYMENT_COLUMNS = `
  id, company_id, provider, provider_checkout_id, provider_payment_id, plan_id,
  subscription_id, amount_minor, currency, refunded_minor, state, failure_reason,
  card_brand, card_last4, idempotency_key, metadata, created_at, updated_at`;

const REFUND_COLUMNS = `
  id, payment_id, company_id, provider, provider_refund_id, amount_minor,
  currency, state, reason, requested_by, idempotency_key, created_at, updated_at`;

interface PaymentRow {
  id: string;
  company_id: string;
  provider: string;
  provider_checkout_id: string | null;
  provider_payment_id: string | null;
  plan_id: string;
  subscription_id: string | null;
  amount_minor: string;
  currency: string;
  refunded_minor: string;
  state: PaymentState;
  failure_reason: string;
  card_brand: string;
  card_last4: string;
  idempotency_key: string;
  metadata: unknown;
  created_at: Date;
  updated_at: Date;
}

interface RefundRow {
  id: string;
  payment_id: string;
  company_id: string;
  provider: string;
  provider_refund_id: string | null;
  amount_minor: string;
  currency: string;
  state: RefundState;
  reason: string;
  requested_by: string;
  idempotency_key: string;
  created_at: Date;
  updated_at: Date;
}

function toPayment(row: PaymentRow): Payment {
  return {
    id: row.id,
    companyId: row.company_id,
    provider: row.provider,
    providerCheckoutId: row.provider_checkout_id,
    providerPaymentId: row.provider_payment_id,
    planId: row.plan_id,
    subscriptionId: row.subscription_id,
    amountMinor: toMinor(row.amount_minor),
    // char(3) pads with spaces on some drivers; trim before it reaches a client.
    currency: row.currency.trim(),
    refundedMinor: toMinor(row.refunded_minor),
    state: row.state,
    failureReason: row.failure_reason,
    cardBrand: row.card_brand,
    cardLast4: row.card_last4,
    idempotencyKey: row.idempotency_key,
    metadata: (row.metadata as Record<string, unknown> | null) ?? {},
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
}

function toRefund(row: RefundRow): Refund {
  return {
    id: row.id,
    paymentId: row.payment_id,
    companyId: row.company_id,
    provider: row.provider,
    providerRefundId: row.provider_refund_id,
    amountMinor: toMinor(row.amount_minor),
    currency: row.currency.trim(),
    state: row.state,
    reason: row.reason,
    requestedBy: row.requested_by,
    idempotencyKey: row.idempotency_key,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
}

export interface CreatePaymentInput {
  companyId: string;
  provider: string;
  planId: string;
  amountMinor: bigint;
  currency: string;
  idempotencyKey: string;
  metadata: Record<string, unknown>;
}

export interface PaymentPatch {
  state?: PaymentState;
  providerCheckoutId?: string;
  providerPaymentId?: string;
  subscriptionId?: string;
  failureReason?: string;
  cardBrand?: string;
  cardLast4?: string;
  refundedMinor?: bigint;
}

export interface ListPaymentsFilter extends PageRequest {
  companyId?: string;
  states?: PaymentState[];
}

export interface CreateRefundInput {
  paymentId: string;
  companyId: string;
  provider: string;
  amountMinor: bigint;
  currency: string;
  reason: string;
  requestedBy: string;
  idempotencyKey: string;
}

@Injectable()
export class PaymentsRepository {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  async create(input: CreatePaymentInput, client?: Executor): Promise<Payment> {
    const executor = client ?? this.pool;
    const id = newId(PaymentIdPrefix);

    const { rows } = await executor.query<PaymentRow>(
      `INSERT INTO payments (
          id, company_id, provider, plan_id, amount_minor, currency, idempotency_key, metadata
       ) VALUES ($1,$2::uuid,$3,$4,$5,$6,$7,$8::jsonb)
       RETURNING ${PAYMENT_COLUMNS}`,
      [
        id,
        input.companyId,
        input.provider,
        input.planId,
        toParam(input.amountMinor),
        input.currency,
        input.idempotencyKey,
        JSON.stringify(input.metadata),
      ],
    );
    return toPayment(rows[0]!);
  }

  /** Platform read: any tenant's payment by id. */
  async findById(id: string, client?: Executor): Promise<Payment | null> {
    const executor = client ?? this.pool;
    const { rows } = await executor.query<PaymentRow>(
      `SELECT ${PAYMENT_COLUMNS} FROM payments WHERE id = $1`,
      [id],
    );
    return rows[0] ? toPayment(rows[0]) : null;
  }

  /**
   * Platform read, but locked for update.
   *
   * Webhook deliveries for one payment can arrive concurrently — Stripe fans out
   * three events for a single card charge — and two handlers reading the same
   * row before either writes would both decide "not yet succeeded" and both
   * issue an invoice.
   */
  async findByIdForUpdate(id: string, client: PoolClient): Promise<Payment | null> {
    const { rows } = await client.query<PaymentRow>(
      `SELECT ${PAYMENT_COLUMNS} FROM payments WHERE id = $1 FOR UPDATE`,
      [id],
    );
    return rows[0] ? toPayment(rows[0]) : null;
  }

  /**
   * Company read: a payment that must belong to this tenant.
   *
   * The predicate is in the statement, so another company's id returns null —
   * indistinguishable from an id that does not exist, which is the point.
   */
  async findByIdForCompany(id: string, companyId: string): Promise<Payment | null> {
    const { rows } = await this.pool.query<PaymentRow>(
      `SELECT ${PAYMENT_COLUMNS} FROM payments WHERE id = $1 AND company_id = $2::uuid`,
      [id, companyId],
    );
    return rows[0] ? toPayment(rows[0]) : null;
  }

  async findByIdempotencyKey(companyId: string, key: string): Promise<Payment | null> {
    const { rows } = await this.pool.query<PaymentRow>(
      `SELECT ${PAYMENT_COLUMNS} FROM payments
        WHERE company_id = $1::uuid AND idempotency_key = $2`,
      [companyId, key],
    );
    return rows[0] ? toPayment(rows[0]) : null;
  }

  /**
   * Resolves the payment a provider event is talking about.
   *
   * Our own id (carried as the provider's reference) is tried first because it
   * is the only identifier we minted; the provider's ids are the fallback for
   * events that carry no reference. Nothing here reads a company id from the
   * event — the tenant is whatever the matched row says it is.
   */
  async findForProviderEvent(
    provider: string,
    reference: string,
    providerPaymentId: string,
    providerCheckoutId: string,
    client: PoolClient,
  ): Promise<Payment | null> {
    if (reference !== '') {
      const direct = await this.findByIdForUpdate(reference, client);
      if (direct && direct.provider === provider) {
        return direct;
      }
    }

    const { rows } = await client.query<PaymentRow>(
      `SELECT ${PAYMENT_COLUMNS} FROM payments
        WHERE provider = $1
          AND ( ($2 <> '' AND provider_payment_id = $2)
             OR ($3 <> '' AND provider_checkout_id = $3) )
        ORDER BY created_at DESC
        LIMIT 1
        FOR UPDATE`,
      [provider, providerPaymentId, providerCheckoutId],
    );
    return rows[0] ? toPayment(rows[0]) : null;
  }

  async update(id: string, patch: PaymentPatch, client?: Executor): Promise<Payment | null> {
    const executor = client ?? this.pool;
    const assignments: string[] = [];
    const params: unknown[] = [];

    const set = (column: string, value: unknown): void => {
      params.push(value);
      assignments.push(`${column} = $${params.length}`);
    };

    if (patch.state !== undefined) set('state', patch.state);
    if (patch.providerCheckoutId !== undefined) set('provider_checkout_id', patch.providerCheckoutId);
    if (patch.providerPaymentId !== undefined) set('provider_payment_id', patch.providerPaymentId);
    if (patch.subscriptionId !== undefined) set('subscription_id', patch.subscriptionId);
    if (patch.failureReason !== undefined) set('failure_reason', patch.failureReason);
    if (patch.cardBrand !== undefined) set('card_brand', patch.cardBrand);
    if (patch.cardLast4 !== undefined) set('card_last4', patch.cardLast4);
    if (patch.refundedMinor !== undefined) set('refunded_minor', toParam(patch.refundedMinor));

    if (assignments.length === 0) {
      return this.findById(id, executor);
    }

    params.push(id);
    const { rows } = await executor.query<PaymentRow>(
      `UPDATE payments SET ${assignments.join(', ')}
        WHERE id = $${params.length}
        RETURNING ${PAYMENT_COLUMNS}`,
      params,
    );
    return rows[0] ? toPayment(rows[0]) : null;
  }

  async list(filter: ListPaymentsFilter): Promise<Page<Payment>> {
    const params: unknown[] = [];
    const where: string[] = [];

    if (filter.companyId !== undefined) {
      params.push(filter.companyId);
      where.push(`company_id = $${params.length}::uuid`);
    }

    if (filter.states?.length) {
      params.push(filter.states);
      where.push(`state = ANY($${params.length}::payment_state[])`);
    }

    if (filter.cursor) {
      params.push(filter.cursor.createdAt, filter.cursor.id);
      where.push(`(created_at, id) < ($${params.length - 1}::timestamptz, $${params.length})`);
    }

    // limit + 1, so buildPage can tell there is another page without a count.
    params.push(filter.limit + 1);

    const { rows } = await this.pool.query<PaymentRow>(
      `SELECT ${PAYMENT_COLUMNS} FROM payments
       ${where.length ? `WHERE ${where.join(' AND ')}` : ''}
       ORDER BY created_at DESC, id DESC
       LIMIT $${params.length}`,
      params,
    );

    return buildPage(rows.map(toPayment), filter.limit);
  }

  // ----------------------------------------------------------------- refunds --

  async createRefund(input: CreateRefundInput, client?: Executor): Promise<Refund> {
    const executor = client ?? this.pool;
    const id = newId(RefundIdPrefix);

    try {
      const { rows } = await executor.query<RefundRow>(
        `INSERT INTO refunds (
            id, payment_id, company_id, provider, amount_minor, currency,
            reason, requested_by, idempotency_key
         ) VALUES ($1,$2,$3::uuid,$4,$5,$6,$7,$8,$9)
         RETURNING ${REFUND_COLUMNS}`,
        [
          id,
          input.paymentId,
          input.companyId,
          input.provider,
          toParam(input.amountMinor),
          input.currency,
          input.reason,
          input.requestedBy,
          input.idempotencyKey,
        ],
      );
      return toRefund(rows[0]!);
    } catch (error) {
      if (isUniqueViolation(error, 'refunds_idempotency_idx')) {
        throw conflict(
          'refund_in_progress',
          'A refund with this idempotency key is already being processed for this payment.',
        );
      }
      throw error;
    }
  }

  async findRefundByIdempotencyKey(paymentId: string, key: string): Promise<Refund | null> {
    const { rows } = await this.pool.query<RefundRow>(
      `SELECT ${REFUND_COLUMNS} FROM refunds WHERE payment_id = $1 AND idempotency_key = $2`,
      [paymentId, key],
    );
    return rows[0] ? toRefund(rows[0]) : null;
  }

  async findRefundByProviderId(
    provider: string,
    providerRefundId: string,
    client?: Executor,
  ): Promise<Refund | null> {
    const executor = client ?? this.pool;
    const { rows } = await executor.query<RefundRow>(
      `SELECT ${REFUND_COLUMNS} FROM refunds
        WHERE provider = $1 AND provider_refund_id = $2`,
      [provider, providerRefundId],
    );
    return rows[0] ? toRefund(rows[0]) : null;
  }

  /**
   * Matches a provider refund event to a refund row we already created.
   *
   * A provider that echoes no refund id falls back to the oldest still-pending
   * refund of the right amount against the same payment, which is the only
   * ordering that cannot settle a later refund with an earlier event.
   */
  async findPendingRefund(
    paymentId: string,
    amountMinor: bigint,
    client: PoolClient,
  ): Promise<Refund | null> {
    const { rows } = await client.query<RefundRow>(
      `SELECT ${REFUND_COLUMNS} FROM refunds
        WHERE payment_id = $1 AND amount_minor = $2 AND state = 'pending'
        ORDER BY created_at ASC
        LIMIT 1
        FOR UPDATE`,
      [paymentId, toParam(amountMinor)],
    );
    return rows[0] ? toRefund(rows[0]) : null;
  }

  async updateRefund(
    id: string,
    patch: { state?: RefundState; providerRefundId?: string },
    client?: Executor,
  ): Promise<Refund | null> {
    const executor = client ?? this.pool;
    const assignments: string[] = [];
    const params: unknown[] = [];

    if (patch.state !== undefined) {
      params.push(patch.state);
      assignments.push(`state = $${params.length}`);
    }
    if (patch.providerRefundId !== undefined) {
      params.push(patch.providerRefundId);
      assignments.push(`provider_refund_id = $${params.length}`);
    }
    if (assignments.length === 0) {
      return null;
    }

    params.push(id);
    const { rows } = await executor.query<RefundRow>(
      `UPDATE refunds SET ${assignments.join(', ')}
        WHERE id = $${params.length}
        RETURNING ${REFUND_COLUMNS}`,
      params,
    );
    return rows[0] ? toRefund(rows[0]) : null;
  }

  async listRefundsForPayment(paymentId: string): Promise<Refund[]> {
    const { rows } = await this.pool.query<RefundRow>(
      `SELECT ${REFUND_COLUMNS} FROM refunds WHERE payment_id = $1 ORDER BY created_at DESC`,
      [paymentId],
    );
    return rows.map(toRefund);
  }

  /**
   * The total that has actually left our account for this payment.
   *
   * Recomputed from the refund rows rather than incremented on the payment,
   * because a failed refund must give its money back to the outstanding
   * balance and an increment cannot be un-done reliably under retries.
   */
  async settledRefundTotal(paymentId: string, client: Executor): Promise<bigint> {
    const { rows } = await client.query<{ total: string }>(
      `SELECT COALESCE(SUM(amount_minor), 0)::text AS total FROM refunds
        WHERE payment_id = $1 AND state IN ('pending','succeeded')`,
      [paymentId],
    );
    return toMinor(rows[0]?.total ?? '0');
  }
}

/** pg surfaces a unique violation as SQLSTATE 23505 with the index name. */
function isUniqueViolation(error: unknown, constraint: string): boolean {
  const candidate = error as { code?: string; constraint?: string };
  return candidate?.code === '23505' && candidate?.constraint === constraint;
}
