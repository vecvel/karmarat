/**
 * Invoice persistence.
 *
 * An invoice is immutable once issued: it records what was billed at a moment
 * in time, so nothing here updates lines or totals. The only write is the
 * insert that accompanies a settled payment.
 */
import { Inject, Injectable } from '@nestjs/common';
import { buildPage, type Page, type PageRequest } from '@karmarat/nestshared';
import type { Pool, PoolClient } from 'pg';

import { PG_POOL } from '../common/infrastructure.module';
import { InvoiceIdPrefix, newId } from '../common/ids';
import { toMinor, toParam } from '../common/money';
import type { Invoice, InvoiceLine } from './payment.entity';

type Executor = Pool | PoolClient;

const COLUMNS = `
  id, number, company_id, payment_id, lines, subtotal_minor, tax_minor,
  total_minor, currency, issued_at, pdf_key, created_at, updated_at`;

interface Row {
  id: string;
  number: string;
  company_id: string;
  payment_id: string | null;
  lines: unknown;
  subtotal_minor: string;
  tax_minor: string;
  total_minor: string;
  currency: string;
  issued_at: Date;
  pdf_key: string;
  created_at: Date;
  updated_at: Date;
}

function toInvoice(row: Row): Invoice {
  return {
    id: row.id,
    number: row.number,
    companyId: row.company_id,
    paymentId: row.payment_id,
    lines: Array.isArray(row.lines) ? (row.lines as InvoiceLine[]) : [],
    subtotalMinor: toMinor(row.subtotal_minor),
    taxMinor: toMinor(row.tax_minor),
    totalMinor: toMinor(row.total_minor),
    currency: row.currency.trim(),
    issuedAt: row.issued_at,
    pdfKey: row.pdf_key,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
}

export interface CreateInvoiceInput {
  companyId: string;
  paymentId: string;
  lines: InvoiceLine[];
  subtotalMinor: bigint;
  taxMinor: bigint;
  totalMinor: bigint;
  currency: string;
}

export interface ListInvoicesFilter extends PageRequest {
  companyId: string;
}

@Injectable()
export class InvoicesRepository {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  /**
   * Issues an invoice, or returns the one this payment already has.
   *
   * `ON CONFLICT DO NOTHING` on the per-payment unique index, then a read: a
   * webhook redelivery that slipped past the event ledger must produce the same
   * invoice rather than a second document for one charge. The number comes from
   * a sequence — `count(*) + 1` would hand the same number to two concurrent
   * settlements and turn a billing event into a failed webhook.
   */
  async issue(input: CreateInvoiceInput, client: Executor): Promise<Invoice> {
    const id = newId(InvoiceIdPrefix);

    const { rows } = await client.query<Row>(
      `INSERT INTO invoices (
          id, number, company_id, payment_id, lines,
          subtotal_minor, tax_minor, total_minor, currency
       ) VALUES (
          $1,
          'INV-' || to_char(now(), 'YYYY') || '-' || lpad(nextval('invoice_number_seq')::text, 6, '0'),
          $2::uuid, $3, $4::jsonb, $5, $6, $7, $8
       )
       ON CONFLICT (payment_id) WHERE payment_id IS NOT NULL DO NOTHING
       RETURNING ${COLUMNS}`,
      [
        id,
        input.companyId,
        input.paymentId,
        JSON.stringify(input.lines),
        toParam(input.subtotalMinor),
        toParam(input.taxMinor),
        toParam(input.totalMinor),
        input.currency,
      ],
    );

    if (rows[0]) {
      return toInvoice(rows[0]);
    }

    const existing = await this.findByPaymentId(input.paymentId, client);
    if (!existing) {
      // The insert was skipped but nothing is there: the only way that happens
      // is a conflict on a different constraint, which is a bug worth surfacing
      // rather than swallowing into a silent "no invoice".
      throw new Error(`invoice for payment ${input.paymentId} could neither be issued nor read back`);
    }
    return existing;
  }

  async findByPaymentId(paymentId: string, client?: Executor): Promise<Invoice | null> {
    const executor = client ?? this.pool;
    const { rows } = await executor.query<Row>(
      `SELECT ${COLUMNS} FROM invoices WHERE payment_id = $1`,
      [paymentId],
    );
    return rows[0] ? toInvoice(rows[0]) : null;
  }

  /** Company read. The tenant predicate is in the statement, never after it. */
  async listForCompany(filter: ListInvoicesFilter): Promise<Page<Invoice>> {
    const params: unknown[] = [filter.companyId];
    const where = ['company_id = $1::uuid'];

    if (filter.cursor) {
      params.push(filter.cursor.createdAt, filter.cursor.id);
      where.push(`(created_at, id) < ($${params.length - 1}::timestamptz, $${params.length})`);
    }

    params.push(filter.limit + 1);

    const { rows } = await this.pool.query<Row>(
      `SELECT ${COLUMNS} FROM invoices
        WHERE ${where.join(' AND ')}
        ORDER BY created_at DESC, id DESC
        LIMIT $${params.length}`,
      params,
    );

    return buildPage(rows.map(toInvoice), filter.limit);
  }
}
