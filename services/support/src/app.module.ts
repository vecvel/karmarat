/**
 * The application root.
 *
 * Two things are wired globally on purpose. `GatewayPrincipalMiddleware` runs
 * for every path so no route can be written that forgets to establish who the
 * caller is, and `AuthorizationGuard` is registered as a global guard so the
 * default for a new route is *denied* — a handler becomes reachable by opting
 * out with `@Public()`, never by someone remembering to add a guard.
 */
import { MiddlewareConsumer, Module, type NestModule } from '@nestjs/common';
import { APP_GUARD } from '@nestjs/core';
import { AuthorizationGuard, GatewayPrincipalMiddleware } from '@karmarat/nestshared';

import { InfrastructureModule } from './common/infrastructure.module';
import { SupportModule } from './support/support.module';

@Module({
  imports: [InfrastructureModule, SupportModule],
  providers: [{ provide: APP_GUARD, useClass: AuthorizationGuard }],
})
export class AppModule implements NestModule {
  configure(consumer: MiddlewareConsumer): void {
    // '*splat' rather than '*': Express 5 requires a wildcard to be named, and
    // a bare '*' throws at startup rather than matching everything.
    consumer.apply(GatewayPrincipalMiddleware).forRoutes('*splat');
  }
}
