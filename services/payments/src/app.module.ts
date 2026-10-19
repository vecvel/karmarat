import { MiddlewareConsumer, Module, type NestModule } from '@nestjs/common';
import { APP_GUARD } from '@nestjs/core';
import { AuthorizationGuard, GatewayPrincipalMiddleware } from '@karmarat/nestshared';

import { InfrastructureModule } from './common/infrastructure.module';
import { PaymentsModule } from './payments/payments.module';
import { ProvidersModule } from './providers/providers.module';
import { WebhooksModule } from './webhooks/webhooks.module';

@Module({
  imports: [InfrastructureModule, ProvidersModule, PaymentsModule, WebhooksModule],
  providers: [
    // Global, so a new controller is guarded by default. A route that should be
    // reachable without a token has to say so with @Public(), which makes the
    // exception visible in review rather than implied by omission.
    { provide: APP_GUARD, useClass: AuthorizationGuard },
  ],
})
export class AppModule implements NestModule {
  configure(consumer: MiddlewareConsumer): void {
    // Reconstructs the principal from the gateway's headers for every request,
    // including the public ones — the guard decides what to do with it.
    consumer.apply(GatewayPrincipalMiddleware).forRoutes('*');
  }
}
