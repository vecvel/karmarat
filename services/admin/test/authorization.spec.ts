/**
 * Authorization, proved end to end.
 *
 * This service is the one place on the platform where every tenant's data is in
 * one database. A missed check in companies leaks one company to one other
 * company; a missed check here leaks all of them to anyone with an account. So
 * the rule is not asserted by reading the decorators — it is exercised through
 * the real guard, on the real routes, with the headers the gateway actually
 * sets.
 *
 * The services behind the controllers are stubs. That is deliberate: this suite
 * is about who may call, and a stub that returns data on every call makes a
 * leak *louder* — a route that wrongly admits a tenant returns a body full of
 * other people's companies rather than an empty list that might have passed for
 * a pass.
 */
import { INestApplication } from '@nestjs/common';
import { APP_GUARD } from '@nestjs/core';
import { Test } from '@nestjs/testing';
import { AuthorizationGuard, GatewayPrincipalMiddleware, ProblemFilter } from '@karmarat/nestshared';
import request from 'supertest';

import { ActivityController } from '../src/activity/activity.controller';
import { ActivityService } from '../src/activity/activity.service';
import { AdminCompaniesController } from '../src/companies/companies.controller';
import { CompaniesService } from '../src/companies/companies.service';
import { AdminHealthController } from '../src/health/health.controller';
import { DownstreamProber } from '../src/health/downstream.prober';
import { OverviewController } from '../src/overview/overview.controller';
import { OverviewService } from '../src/overview/overview.service';

const COMPANY_ID = '11111111-2222-3333-4444-555555555555';

/** Every route group this service exposes, with the permission it is gated on. */
const ROUTES = [
  { name: 'overview', path: '/v1/admin/overview', permission: 'platform_companies.read' },
  { name: 'companies list', path: '/v1/admin/companies', permission: 'platform_companies.read' },
  { name: 'company detail', path: `/v1/admin/companies/${COMPANY_ID}`, permission: 'platform_companies.read' },
  { name: 'activity feed', path: '/v1/admin/activity', permission: 'platform_audit.read' },
  { name: 'activity export', path: '/v1/admin/activity/export', permission: 'platform_audit.export' },
  { name: 'platform health', path: '/v1/admin/health', permission: 'platform_settings.read' },
] as const;

/** Every permission any route here checks — handed out in full to the tenants below. */
const ALL_PERMISSIONS = [...new Set(ROUTES.map((route) => route.permission))].join(',');

function headers(over: Record<string, string>): Record<string, string> {
  return { 'x-principal-id': 'acct_1', ...over };
}

const platform = (permissions: string): Record<string, string> =>
  headers({ 'x-principal-type': 'platform', 'x-permissions': permissions });

const companyPrincipal = (permissions: string = ALL_PERMISSIONS): Record<string, string> =>
  headers({ 'x-principal-type': 'company', 'x-company-id': COMPANY_ID, 'x-permissions': permissions });

const candidate = (): Record<string, string> =>
  headers({ 'x-principal-type': 'candidate', 'x-permissions': ALL_PERMISSIONS });

describe('admin console authorization', () => {
  let app: INestApplication;

  beforeAll(async () => {
    const moduleRef = await Test.createTestingModule({
      controllers: [OverviewController, AdminCompaniesController, ActivityController, AdminHealthController],
      providers: [
        { provide: APP_GUARD, useClass: AuthorizationGuard },
        { provide: OverviewService, useValue: { snapshot: async () => snapshot() } },
        {
          provide: CompaniesService,
          useValue: {
            list: async () => ({ items: [companyRow()], nextCursor: null }),
            detail: async () => ({ company: companyRow(), payments: [], tickets: [], activity: [] }),
          },
        },
        {
          provide: ActivityService,
          useValue: {
            feed: async () => ({ items: [], nextCursor: null }),
            domains: async () => [],
            exportCsv: async () => ({ csv: 'event_id\r\n', rows: 0, truncated: false }),
          },
        },
        { provide: DownstreamProber, useValue: { report: async () => ({ status: 'ok', services: [] }) } },
      ],
    }).compile();

    app = moduleRef.createNestApplication();
    // The same middleware and filter main.ts installs. Without the middleware
    // there is no principal at all and every case below would pass for the
    // wrong reason.
    const middleware = new GatewayPrincipalMiddleware();
    app.use(middleware.use.bind(middleware));
    app.useGlobalFilters(new ProblemFilter());
    await app.init();
  });

  afterAll(async () => {
    await app.close();
  });

  describe.each(ROUTES)('$name', (route) => {
    it('refuses a company principal, even one holding every permission the route checks', async () => {
      // The permission is not the gate that matters here. A company role can
      // never legitimately be granted a platform-scoped key — identity's scope
      // rules see to that — so this request is the shape of a forged or
      // mis-issued token, and the principal type is what stops it.
      const response = await request(app.getHttpServer()).get(route.path).set(companyPrincipal());

      expect(response.status).toBe(403);
      expect(response.body.code).toBe('forbidden');
    });

    it('refuses a candidate principal', async () => {
      const response = await request(app.getHttpServer()).get(route.path).set(candidate());
      expect(response.status).toBe(403);
    });

    it('refuses an anonymous caller', async () => {
      const response = await request(app.getHttpServer()).get(route.path);
      expect(response.status).toBe(401);
    });

    it('refuses platform staff without the permission', async () => {
      const response = await request(app.getHttpServer()).get(route.path).set(platform('plans.read'));
      expect(response.status).toBe(403);
    });

    it('admits platform staff holding the permission', async () => {
      const response = await request(app.getHttpServer()).get(route.path).set(platform(route.permission));
      expect(response.status).toBe(200);
    });
  });

  it('never tells a refused caller what the route would have returned', async () => {
    const response = await request(app.getHttpServer()).get('/v1/admin/companies').set(companyPrincipal());

    const body = JSON.stringify(response.body);
    expect(body).not.toContain('Acme');
    expect(body).not.toContain(COMPANY_ID);
  });

  describe('export is a permission of its own', () => {
    it('does not let the read permission download the whole audit trail', async () => {
      // Reading the feed shows an operator what happened; exporting it puts
      // every tenant's activity into a file that leaves the platform.
      const reader = platform('platform_audit.read');

      await request(app.getHttpServer()).get('/v1/admin/activity').set(reader).expect(200);
      await request(app.getHttpServer()).get('/v1/admin/activity/export').set(reader).expect(403);
    });

    it('does not let the export permission stand in for reading', async () => {
      const exporter = platform('platform_audit.export');

      await request(app.getHttpServer()).get('/v1/admin/activity/export').set(exporter).expect(200);
      await request(app.getHttpServer()).get('/v1/admin/activity').set(exporter).expect(403);
    });
  });

  describe('money is redacted per principal, not per route', () => {
    it('withholds revenue from an operator without the billing permission', async () => {
      const response = await request(app.getHttpServer())
        .get('/v1/admin/overview')
        .set(platform('platform_companies.read'))
        .expect(200);

      // The page still renders — a support agent is not locked out of the
      // console because one card on it shows money.
      expect(response.body.mrr).toBeNull();
      expect(response.body.subscriptions).toBeNull();
      expect(response.body.companies.total).toBe(1);
    });

    it('shows revenue to an operator who may see subscriptions', async () => {
      const response = await request(app.getHttpServer())
        .get('/v1/admin/overview')
        .set(platform('platform_companies.read,subscriptions.read'))
        .expect(200);

      expect(response.body.mrr.byCurrency).toEqual([{ currency: 'USD', monthlyMinor: 10_000, subscriptions: 1 }]);
    });

    it('withholds a tenant’s plan and last payment on the list as well', async () => {
      const response = await request(app.getHttpServer())
        .get('/v1/admin/companies')
        .set(platform('platform_companies.read'))
        .expect(200);

      expect(response.body.items[0].subscription).toBeNull();
      expect(response.body.items[0].lastPayment).toBeNull();
      expect(response.body.items[0].name).toBe('Acme');
    });

    it('shows them to an operator who may see billing and payments', async () => {
      const response = await request(app.getHttpServer())
        .get('/v1/admin/companies')
        .set(platform('platform_companies.read,subscriptions.read,payments.read'))
        .expect(200);

      expect(response.body.items[0].subscription.planName).toBe('Pro');
      expect(response.body.items[0].lastPayment.amountMinor).toBe(10_000);
    });
  });

  describe('input the console itself supplies', () => {
    it('refuses a company id that is not a uuid rather than letting it reach Postgres', async () => {
      const response = await request(app.getHttpServer())
        .get('/v1/admin/companies/not-a-uuid')
        .set(platform('platform_companies.read'));

      expect(response.status).toBe(400);
      expect(response.body.detail).toBe('id must be a UUID.');
    });

    it('refuses a nonsensical page size', async () => {
      await request(app.getHttpServer())
        .get('/v1/admin/activity?limit=0')
        .set(platform('platform_audit.read'))
        .expect(400);
    });
  });
});

function snapshot() {
  return {
    generatedAt: '2026-03-01T12:00:00.000Z',
    staleAfter: '2026-03-01T12:00:30.000Z',
    projectionUpToDate: '2026-03-01T11:59:00.000Z',
    companies: { total: 1, byState: [{ state: 'active', count: 1 }] },
    subscriptions: { billableTotal: 1, byPlan: [{ planId: 'plan_pro', planName: 'Pro', count: 1 }] },
    mrr: {
      byCurrency: [{ currency: 'USD', monthlyMinor: 10_000, subscriptions: 1 }],
      nonRecurring: { subscriptions: 0, byCurrency: [] },
      unnormalised: { subscriptions: 0 },
      normalisation: [],
    },
    signups: { days: 30, points: [], total: 0 },
    candidates: { total: 3 },
    jobs: { published: 2 },
    applications: { total: 7 },
    support: { openTickets: 1 },
  };
}

function companyRow() {
  return {
    companyId: COMPANY_ID,
    slug: 'acme',
    name: 'Acme',
    state: 'active',
    contactEmail: 'ops@acme.example',
    country: 'GB',
    industry: 'software',
    registeredAt: new Date('2026-01-01T00:00:00Z'),
    approvedAt: new Date('2026-01-02T00:00:00Z'),
    suspendedAt: null,
    subscription: {
      subscriptionId: 'sub_1',
      planId: 'plan_pro',
      planName: 'Pro',
      intervalMonths: 1,
      priceMinor: 10_000,
      currency: 'USD',
      state: 'active',
      startedAt: new Date('2026-01-02T00:00:00Z'),
      expiresAt: null,
      cancelledAt: null,
    },
    lastPayment: {
      id: 'pay_1',
      amountMinor: 10_000,
      currency: 'USD',
      status: 'succeeded',
      failureReason: '',
      paidAt: new Date('2026-02-01T00:00:00Z'),
    },
    openTickets: 1,
    applications: 12,
    publishedJobs: 3,
  };
}
