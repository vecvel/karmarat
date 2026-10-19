/**
 * Platform administration of tenants.
 *
 * These routes address a company by id, which is safe precisely because they
 * are platform-principal only: a platform administrator has no tenant of their
 * own, so there is no tenant boundary for a path parameter to cross. Every
 * route carries a `platform_companies.*` key that already exists in the RBAC
 * registry, and the guard rejects a company or candidate principal outright —
 * a recruiter who somehow held `platform_companies.read` still cannot reach
 * them, because the gateway only routes the root portal here.
 */
import {
  Body,
  Controller,
  Delete,
  Get,
  HttpCode,
  Param,
  ParseUUIDPipe,
  Patch,
  Post,
  Query,
} from '@nestjs/common';
import { RequirePermission, RequirePrincipalType } from '@karmarat/nestshared';

import { CompaniesService } from './companies.service';
import { ListCompaniesQueryDto, PlatformUpdateCompanyDto, SuspendCompanyDto } from './dto/platform.dto';

/**
 * A malformed id would otherwise reach Postgres and come back as an unreadable
 * cast error; rejecting it here keeps that off the wire and out of the logs.
 */
const CompanyIdParam = (): ParameterDecorator => Param('id', new ParseUUIDPipe());

@Controller('v1/platform/companies')
@RequirePrincipalType('platform')
export class PlatformCompaniesController {
  constructor(private readonly companies: CompaniesService) {}

  @Get()
  @RequirePermission('platform_companies.read')
  async list(@Query() query: ListCompaniesQueryDto) {
    return this.companies.list(query);
  }

  @Get(':id')
  @RequirePermission('platform_companies.read')
  async get(@CompanyIdParam() id: string) {
    return this.companies.platformGet(id);
  }

  @Patch(':id')
  @RequirePermission('platform_companies.update')
  async update(@CompanyIdParam() id: string, @Body() dto: PlatformUpdateCompanyDto) {
    return this.companies.platformUpdate(id, dto);
  }

  // 200, not Nest's default 201 for a POST: approving creates nothing, it moves
  // an existing company, and the response is that company.
  @Post(':id/approve')
  @HttpCode(200)
  @RequirePermission('platform_companies.approve')
  async approve(@CompanyIdParam() id: string) {
    return this.companies.approve(id);
  }

  @Post(':id/suspend')
  @HttpCode(200)
  @RequirePermission('platform_companies.suspend')
  async suspend(@CompanyIdParam() id: string, @Body() dto: SuspendCompanyDto) {
    return this.companies.suspend(id, dto.reason);
  }

  /**
   * Soft delete. The row stays, the tenant stops being served.
   *
   * 204 rather than the deleted record: there is nothing useful to return, and
   * echoing the company back invites a console to keep rendering it.
   */
  @Delete(':id')
  @HttpCode(204)
  @RequirePermission('platform_companies.delete')
  async remove(@CompanyIdParam() id: string): Promise<void> {
    await this.companies.softDelete(id);
  }
}
