/**
 * The platform-wide audit feed.
 *
 * Every row here is an event this service consumed, kept because a read model
 * that cannot say *why* a figure changed is half a console. It is the same
 * table the projection consumer writes to first in each transaction, which is
 * what makes the projection idempotent — see the header of 001_admin.sql.
 *
 * The feed is strictly newest-first and paged by keyset on
 * `(occurred_at, event_id)`. Both indexes carry the event id as the tiebreaker
 * for that reason: two events can share a millisecond, and a cursor over
 * timestamp alone would either repeat or skip one of them.
 */
import { Inject, Injectable } from '@nestjs/common';
import type { Cursor } from '@karmarat/nestshared';
import type { Pool } from 'pg';

import { Params, whereClause } from '../common/sql';
import { PG_POOL } from '../common/tokens';

export interface ActivityRow {
  eventId: string;
  subject: string;
  domain: string;
  action: string;
  companyId: string | null;
  actorId: string;
  correlationId: string;
  occurredAt: Date;
  payload: Record<string, unknown>;
}

export interface ActivityFilters {
  domain?: string;
  action?: string;
  companyId?: string;
  from?: Date;
  to?: Date;
}

interface RawActivity {
  event_id: string;
  subject: string;
  domain: string;
  action: string;
  company_id: string | null;
  actor_id: string;
  correlation_id: string;
  occurred_at: Date;
  payload: Record<string, unknown> | null;
}

@Injectable()
export class ActivityRepository {
  constructor(@Inject(PG_POOL) private readonly db: Pool) {}

  /** One page of the feed. Fetches limit+1 rows to detect a next page. */
  async list(filters: ActivityFilters, limit: number, cursor: Cursor | null): Promise<ActivityRow[]> {
    const params = new Params();
    const conditions = this.conditions(filters, params);

    if (cursor) {
      conditions.push(
        `(occurred_at, event_id) < (${params.add(cursor.createdAt)}::timestamptz, ${params.add(cursor.id)})`,
      );
    }

    const { rows } = await this.db.query<RawActivity>(
      `SELECT event_id, subject, domain, action, company_id, actor_id, correlation_id, occurred_at, payload
         FROM activity
         ${whereClause(conditions)}
        ORDER BY occurred_at DESC, event_id DESC
        LIMIT ${params.add(limit + 1)}`,
      params.all(),
    );

    return rows.map(toActivityRow);
  }

  /**
   * The whole filtered feed for an export, bounded by a hard ceiling.
   *
   * Not cursor-paged: an export is one file. The ceiling is what keeps a
   * mis-typed filter from streaming a year of platform history into a browser,
   * and the caller reports when it truncated rather than silently cutting off.
   */
  async forExport(filters: ActivityFilters, maxRows: number): Promise<ActivityRow[]> {
    const params = new Params();
    const conditions = this.conditions(filters, params);

    const { rows } = await this.db.query<RawActivity>(
      `SELECT event_id, subject, domain, action, company_id, actor_id, correlation_id, occurred_at, payload
         FROM activity
         ${whereClause(conditions)}
        ORDER BY occurred_at DESC, event_id DESC
        LIMIT ${params.add(maxRows + 1)}`,
      params.all(),
    );

    return rows.map(toActivityRow);
  }

  /** The tail of one tenant's history, for the company detail page. */
  async recentForCompany(companyId: string, limit: number): Promise<ActivityRow[]> {
    const { rows } = await this.db.query<RawActivity>(
      `SELECT event_id, subject, domain, action, company_id, actor_id, correlation_id, occurred_at, payload
         FROM activity
        WHERE company_id = $1::uuid
        ORDER BY occurred_at DESC, event_id DESC
        LIMIT $2`,
      [companyId, limit],
    );

    return rows.map(toActivityRow);
  }

  /** The domains actually present, so the console's filter offers real options. */
  async domains(): Promise<string[]> {
    const { rows } = await this.db.query<{ domain: string }>(
      `SELECT DISTINCT domain FROM activity ORDER BY domain`,
    );
    return rows.map((row) => row.domain);
  }

  private conditions(filters: ActivityFilters, params: Params): string[] {
    const conditions: string[] = [];
    if (filters.domain) conditions.push(`domain = ${params.add(filters.domain)}`);
    if (filters.action) conditions.push(`action = ${params.add(filters.action)}`);
    if (filters.companyId) conditions.push(`company_id = ${params.add(filters.companyId)}::uuid`);
    if (filters.from) conditions.push(`occurred_at >= ${params.add(filters.from)}`);
    if (filters.to) conditions.push(`occurred_at <= ${params.add(filters.to)}`);
    return conditions;
  }
}

function toActivityRow(row: RawActivity): ActivityRow {
  return {
    eventId: row.event_id,
    subject: row.subject,
    domain: row.domain,
    action: row.action,
    companyId: row.company_id,
    actorId: row.actor_id,
    correlationId: row.correlation_id,
    occurredAt: row.occurred_at,
    payload: row.payload ?? {},
  };
}
