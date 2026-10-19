/**
 * Rate limits for the two routes that are reachable without a token.
 *
 * Everything else on this service sits behind the gateway's authentication, so
 * abuse is attributable to an account and can be dealt with as such. These two
 * cannot be: they exist precisely so that someone who has no account can get
 * one.
 */
import { CanActivate, ExecutionContext, Inject, Injectable } from '@nestjs/common';
import { tooManyRequests } from '@karmarat/nestshared';
import type { Request } from 'express';

import type { CompaniesConfig } from '../config';
import { SERVICE_CONFIG } from '../companies/companies.service';
import { FixedWindowLimiter, clientKey } from './rate-limit';

abstract class ClientRateLimitGuard implements CanActivate {
  protected constructor(
    private readonly limiter: FixedWindowLimiter,
    private readonly message: string,
  ) {}

  canActivate(context: ExecutionContext): boolean {
    const request = context.switchToHttp().getRequest<Request>();

    if (!this.limiter.allow(clientKey(request.headers, request.ip ?? 'unknown'))) {
      throw tooManyRequests(this.message);
    }
    return true;
  }
}

/**
 * Caps slug-availability checks.
 *
 * Without a cap this endpoint is an enumeration oracle: a script walking a
 * word list learns every tenant on the platform, and a company's presence here
 * is not public information. The limit is generous enough that a person typing
 * a name into a form never meets it.
 */
@Injectable()
export class SlugCheckRateLimitGuard extends ClientRateLimitGuard {
  constructor(@Inject(SERVICE_CONFIG) config: CompaniesConfig) {
    super(
      new FixedWindowLimiter(config.slugCheckLimit),
      'Too many availability checks. Please wait a moment and try again.',
    );
  }
}

/**
 * Caps registrations.
 *
 * Not asked for, but a public endpoint that writes a row and provisions a
 * tenant in another service is worth a budget of its own — a loop against it
 * fills the companies table and the identity database together, and no
 * legitimate visitor registers more than a couple of companies in a minute.
 */
@Injectable()
export class RegistrationRateLimitGuard extends ClientRateLimitGuard {
  constructor() {
    super(new FixedWindowLimiter(5), 'Too many registration attempts. Please wait a moment and try again.');
  }
}
