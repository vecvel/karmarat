/**
 * The shared internal-token guard, made injectable.
 *
 * The shared guard reads INTERNAL_SERVICE_TOKEN from the environment. This
 * subclass takes it from the parsed configuration instead, which keeps every
 * setting this service depends on flowing through one place rather than having
 * a guard reach around the config layer to read the environment again.
 */
import { Inject, Injectable } from '@nestjs/common';
import { InternalTokenGuard } from '@karmarat/nestshared';

import type { CompaniesConfig } from '../config';
import { SERVICE_CONFIG } from '../companies/companies.service';

@Injectable()
export class ServiceTokenGuard extends InternalTokenGuard {
  constructor(@Inject(SERVICE_CONFIG) private readonly config: CompaniesConfig) {
    super();
  }

  protected override expectedToken(): string {
    return this.config.internalToken;
  }
}
