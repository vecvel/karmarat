/**
 * A company's own record.
 *
 * Every route here takes its tenant from `@CompanyId()`, which reads the
 * verified principal and throws when there is none. No handler accepts a
 * company id, and there is no route shaped `/v1/company/:id` — the only company
 * a company principal can address is its own.
 */
import { Body, Controller, Get, HttpCode, Patch, Post } from '@nestjs/common';
import { CompanyId, RequirePermission, RequirePrincipalType } from '@karmarat/nestshared';

import { CompaniesService } from './companies.service';
import { UpdateProfileDto, UploadUrlDto } from './dto/profile.dto';

@Controller('v1/company')
@RequirePrincipalType('company')
export class CompanyController {
  constructor(private readonly companies: CompaniesService) {}

  @Get('profile')
  @RequirePermission('company_profile.read')
  async profile(@CompanyId() companyId: string) {
    return this.companies.profile(companyId);
  }

  /**
   * Edits the profile and the careers portal together.
   *
   * One permission covers both because they are one thing to the people who
   * hold it: the registry has `company_profile.update` and no separate careers
   * key, and inventing one would give every role a permission no administrator
   * could grant.
   */
  @Patch('profile')
  @RequirePermission('company_profile.update')
  async update(@CompanyId() companyId: string, @Body() dto: UpdateProfileDto) {
    return this.companies.updateProfile(companyId, dto);
  }

  /**
   * Signs a logo upload.
   *
   * Guarded by update rather than read: the URL is a write capability, and
   * handing one to a role that may only look at the profile would let it change
   * the company's branding through the bucket.
   *
   * 200 rather than Nest's default 201, because signing creates nothing — a 201
   * would invite a client to treat the key as an object that already exists.
   */
  @Post('logo/upload-url')
  @HttpCode(200)
  @RequirePermission('company_profile.update')
  async logoUploadUrl(@CompanyId() companyId: string, @Body() dto: UploadUrlDto) {
    return this.companies.uploadUrl(companyId, 'logo', dto);
  }

  @Post('hero/upload-url')
  @HttpCode(200)
  @RequirePermission('company_profile.update')
  async heroUploadUrl(@CompanyId() companyId: string, @Body() dto: UploadUrlDto) {
    return this.companies.uploadUrl(companyId, 'hero', dto);
  }
}
