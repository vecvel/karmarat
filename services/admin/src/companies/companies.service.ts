/**
 * The tenant list and the single-tenant picture.
 *
 * Both are assembled entirely from this service's projections. The detail page
 * deliberately does NOT call companies, subscriptions or support to freshen
 * itself: a console page that fans out to four services is a page that is down
 * whenever any of them is, and the freshness it would buy is already bounded by
 * how fast events arrive. What the page does instead is report how far behind
 * the projection is, so an operator can tell a stale figure from a wrong one.
 */
import { Injectable } from '@nestjs/common';
import { encodeCursor, notFound, type Cursor } from '@karmarat/nestshared';

import { ActivityService } from '../activity/activity.service';
import type { ActivityRow } from '../activity/activity.repository';
import { CompaniesRepository, type CompanyFilters, type CompanyRow, type PaymentRow, type TicketRow } from './companies.repository';

/** How much history one tenant's page shows before an operator has to go to the feed. */
const DETAIL_PAYMENTS = 10;
const DETAIL_TICKETS = 10;
const DETAIL_ACTIVITY = 20;

export interface CompanyPage {
  items: CompanyRow[];
  nextCursor: string | null;
}

export interface CompanyDetail {
  company: CompanyRow;
  payments: PaymentRow[];
  tickets: TicketRow[];
  activity: ActivityRow[];
}

@Injectable()
export class CompaniesService {
  constructor(
    private readonly repository: CompaniesRepository,
    private readonly activity: ActivityService,
  ) {}

  async list(filters: CompanyFilters, limit: number, cursor: Cursor | null): Promise<CompanyPage> {
    const rows = await this.repository.list(filters, limit, cursor);

    if (rows.length <= limit) {
      return { items: rows, nextCursor: null };
    }

    const items = rows.slice(0, limit);
    const last = items[items.length - 1]!;
    return {
      items,
      nextCursor: encodeCursor({ createdAt: last.registeredAt.toISOString(), id: last.companyId }),
    };
  }

  async detail(companyId: string): Promise<CompanyDetail> {
    const company = await this.repository.get(companyId);
    if (!company) {
      // 404 rather than an empty shell: a tenant this console has never
      // projected is either a bad id or a projection that has fallen behind,
      // and an empty page would read as "this company has no subscription".
      throw notFound('No company with that id has been projected into the console.');
    }

    const [payments, tickets, activity] = await Promise.all([
      this.repository.recentPayments(companyId, DETAIL_PAYMENTS),
      this.repository.tickets(companyId, DETAIL_TICKETS),
      this.activity.recentForCompany(companyId, DETAIL_ACTIVITY),
    ]);

    return { company, payments, tickets, activity };
  }
}
