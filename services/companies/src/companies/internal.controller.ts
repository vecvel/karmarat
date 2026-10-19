/**
 * Service-to-service lookups.
 *
 * Other services hold a company id on their own rows and need a name and a logo
 * to render it. They read it here rather than keeping a copy of this table,
 * because there are no cross-service joins on this platform.
 *
 * `@Public()` marks these as outside the gateway's principal model — they are
 * never routed from the internet at all — and the internal-token guard is what
 * actually admits the caller. The response is a `CompanySummary` and nothing
 * more: an internal caller is still not entitled to the owner's email.
 */
import { Controller, Get, Param, ParseUUIDPipe, UseGuards } from '@nestjs/common';
import { Public } from '@karmarat/nestshared';

import { ServiceTokenGuard } from '../common/internal-guard';
import { CompaniesService } from './companies.service';

@Controller('internal/companies')
@Public()
@UseGuards(ServiceTokenGuard)
export class InternalCompaniesController {
  constructor(private readonly companies: CompaniesService) {}

  @Get(':id')
  async byId(@Param('id', new ParseUUIDPipe()) id: string) {
    return this.companies.summaryById(id);
  }

  /**
   * Resolving by slug exists for the gateway's tenant cache and for the sign-in
   * screen, both of which know the hostname before they know the id.
   */
  @Get('by-slug/:slug')
  async bySlug(@Param('slug') slug: string) {
    return this.companies.summaryBySlug(slug);
  }
}
