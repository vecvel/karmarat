/**
 * Company registration.
 *
 * The only routes on this service that a caller reaches without a token, and
 * the only ones that create a tenant. Both are rate limited, and neither reads
 * a company id from anywhere — there is no tenant yet, which is the point.
 */
import { Body, Controller, Get, HttpCode, Post, Query, UseGuards } from '@nestjs/common';
import { Public } from '@karmarat/nestshared';

import { RegistrationRateLimitGuard, SlugCheckRateLimitGuard } from '../common/rate-limit.guard';
import { CompaniesService } from './companies.service';
import { RegisterCompanyDto, SlugAvailableQueryDto } from './dto/register.dto';

@Controller('v1/register')
export class RegisterController {
  constructor(private readonly companies: CompaniesService) {}

  /**
   * Registers a company and provisions its tenant.
   *
   * 201 with the ids the caller needs to continue — the owner is told to sign
   * in at their new portal, and support can quote the company id. Nothing about
   * the review queue's internals is returned beyond the state itself, which the
   * owner is entitled to know because it is why they cannot sign in yet.
   */
  @Post('company')
  @Public()
  @UseGuards(RegistrationRateLimitGuard)
  @HttpCode(201)
  async register(@Body() dto: RegisterCompanyDto) {
    return this.companies.register(dto);
  }

  /**
   * Live feedback for the address field on the registration form.
   *
   * An absent or empty slug is answered as unavailable rather than as a 422:
   * the form asks on every keystroke, including the first, and a validation
   * error for "the user has not finished typing" is noise.
   */
  @Get('slug-available')
  @Public()
  @UseGuards(SlugCheckRateLimitGuard)
  async slugAvailable(@Query() query: SlugAvailableQueryDto) {
    return this.companies.slugAvailability(query.slug ?? '');
  }
}
