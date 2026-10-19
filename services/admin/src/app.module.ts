import { MiddlewareConsumer, Module, type NestModule } from '@nestjs/common';
import { APP_GUARD } from '@nestjs/core';
import { AuthorizationGuard, GatewayPrincipalMiddleware } from '@karmarat/nestshared';

import { ActivityModule } from './activity/activity.module';
import { InfrastructureModule } from './common/infrastructure.module';
import { AdminCompaniesModule } from './companies/companies.module';
import { AdminHealthModule } from './health/health.module';
import { OverviewModule } from './overview/overview.module';
import { ProjectionModule } from './projection/projection.module';

@Module({
  imports: [
    InfrastructureModule,
    ProjectionModule,
    OverviewModule,
    AdminCompaniesModule,
    ActivityModule,
    AdminHealthModule,
  ],
  providers: [
    // Global, so a controller added to this service is guarded by default. This
    // matters more here than anywhere else on the platform: every other service
    // sees one tenant's data, and this one sees all of them, so a route that
    // forgot its decorator would be a platform-wide disclosure rather than a
    // single-tenant one.
    { provide: APP_GUARD, useClass: AuthorizationGuard },
  ],
})
export class AppModule implements NestModule {
  configure(consumer: MiddlewareConsumer): void {
    // Reconstructs the principal from the gateway's headers for every request,
    // including the public probes — the guard decides what to do with it.
    consumer.apply(GatewayPrincipalMiddleware).forRoutes('*');
  }
}
