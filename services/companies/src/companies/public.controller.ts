/**
 * The careers portal an anonymous visitor sees at `{slug}.{hostname}`.
 *
 * There is no principal to ask which company this is, so the tenant comes from
 * `X-Company-Slug` — a header the gateway sets after resolving the hostname,
 * having first stripped whatever the client sent. That is the same trust as the
 * principal headers and the same source jobs uses for its public board; it is
 * not a client-supplied tenant id, which no endpoint on this platform accepts.
 */
import { Controller, Get, Headers } from '@nestjs/common';
import { GatewayHeader, Public, notFound } from '@karmarat/nestshared';

import { CompaniesService } from './companies.service';

@Controller('v1/public')
export class PublicCompanyController {
  constructor(private readonly companies: CompaniesService) {}

  /**
   * Returns the public projection and nothing else.
   *
   * The service hands back a `PublicCompany`, which has no contact email or
   * phone, no owner, no internal notes, no subscription state and no lifecycle
   * field. That is a type, not a filter applied here, so a column added to the
   * company record cannot reach this response by being forgotten.
   */
  @Get('company')
  @Public()
  async profile(@Headers(GatewayHeader.CompanySlug) slug?: string) {
    if (!slug?.trim()) {
      // Reached on the shared portals, where there is no company to show. The
      // same 404 the unpublished and unapproved cases get, for the same reason:
      // a visitor learns nothing about which slugs exist.
      throw notFound('This careers page is not available.');
    }
    return this.companies.publicProfile(slug);
  }
}
