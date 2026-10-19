/**
 * The audit feed, paged and exported.
 *
 * The service owns the page/export shaping; the repository owns the SQL. The
 * split matters for the export, which is the one place where "how many rows"
 * is a policy decision rather than a query detail: it is bounded by
 * configuration, and the caller is told when the bound bit rather than being
 * handed a silently short file.
 */
import { Inject, Injectable } from '@nestjs/common';
import { encodeCursor, type Cursor } from '@karmarat/nestshared';

import { ADMIN_CONFIG } from '../common/tokens';
import type { AdminConfig } from '../config';
import { ActivityRepository, type ActivityFilters, type ActivityRow } from './activity.repository';
import { csvDocument } from './csv';

export interface ActivityPage {
  items: ActivityRow[];
  nextCursor: string | null;
}

export interface ActivityExport {
  csv: string;
  rows: number;
  /** True when more rows matched than the export ceiling allows. */
  truncated: boolean;
}

export const EXPORT_HEADER = [
  'occurred_at',
  'event_id',
  'domain',
  'action',
  'subject',
  'company_id',
  'actor_id',
  'correlation_id',
  'payload',
] as const;

@Injectable()
export class ActivityService {
  constructor(
    private readonly repository: ActivityRepository,
    @Inject(ADMIN_CONFIG) private readonly config: AdminConfig,
  ) {}

  async feed(filters: ActivityFilters, limit: number, cursor: Cursor | null): Promise<ActivityPage> {
    const rows = await this.repository.list(filters, limit, cursor);
    return toPage(rows, limit);
  }

  async recentForCompany(companyId: string, limit: number): Promise<ActivityRow[]> {
    return this.repository.recentForCompany(companyId, limit);
  }

  async domains(): Promise<string[]> {
    return this.repository.domains();
  }

  async exportCsv(filters: ActivityFilters): Promise<ActivityExport> {
    const max = this.config.exportMaxRows;
    // The repository fetches max+1, so one extra row is the signal that the
    // ceiling bit — no second COUNT(*) over the same predicate.
    const fetched = await this.repository.forExport(filters, max);
    const truncated = fetched.length > max;
    const rows = truncated ? fetched.slice(0, max) : fetched;

    const csv = csvDocument(
      EXPORT_HEADER,
      rows.map((row) => [
        row.occurredAt,
        row.eventId,
        row.domain,
        row.action,
        row.subject,
        row.companyId ?? '',
        row.actorId,
        row.correlationId,
        row.payload,
      ]),
    );

    return { csv, rows: rows.length, truncated };
  }
}

/** limit+1 rows in, one page and a cursor out. */
export function toPage(rows: ActivityRow[], limit: number): ActivityPage {
  if (rows.length <= limit) {
    return { items: rows, nextCursor: null };
  }
  const items = rows.slice(0, limit);
  const last = items[items.length - 1]!;
  return {
    items,
    nextCursor: encodeCursor({ createdAt: last.occurredAt.toISOString(), id: last.eventId }),
  };
}
