/**
 * The platform plan catalogue.
 *
 * Every route is platform-principal only and carries a permission key that
 * already exists in services/identity/internal/rbac/registry.go. Plans are not
 * tenant data — there is deliberately no company filter here, and no company
 * principal can reach these routes at all.
 */
import { Body, Controller, Delete, Get, HttpCode, Param, Patch, Post, Query } from '@nestjs/common';
import { Public, RequirePermission, RequirePrincipalType } from '@karmarat/nestshared';

import { CreatePlanDto, ListPlansQueryDto, UpdatePlanDto } from './dto/plan.dto';
import { toPlanView, toPublicPlanView } from './plan.entity';
import { PlansService } from './plans.service';

@Controller('v1/plans')
@RequirePrincipalType('platform')
export class PlansController {
  constructor(private readonly plans: PlansService) {}

  @Get()
  @RequirePermission('plans.read')
  async list(@Query() query: ListPlansQueryDto) {
    const page = await this.plans.list(query);
    return { items: page.items.map(toPlanView), nextCursor: page.nextCursor };
  }

  @Post()
  @RequirePermission('plans.create')
  async create(@Body() dto: CreatePlanDto) {
    return toPlanView(await this.plans.create(dto));
  }

  @Get(':id')
  @RequirePermission('plans.read')
  async get(@Param('id') id: string) {
    return toPlanView(await this.plans.get(id));
  }

  @Patch(':id')
  @RequirePermission('plans.update')
  async update(@Param('id') id: string, @Body() dto: UpdatePlanDto) {
    return toPlanView(await this.plans.update(id, dto));
  }

  @Delete(':id')
  @HttpCode(204)
  @RequirePermission('plans.delete')
  async remove(@Param('id') id: string): Promise<void> {
    await this.plans.delete(id);
  }

  @Post(':id/publish')
  @RequirePermission('plans.publish')
  async publish(@Param('id') id: string) {
    return toPlanView(await this.plans.publish(id));
  }

  /**
   * Retiring is guarded by plans.publish rather than a key of its own.
   *
   * The registry has no `plans.retire`, and a service may not invent permission
   * strings — an invented key can never be granted to anyone, so the route
   * would be unreachable for every role including the superadmin. Publishing
   * and retiring are the same decision in opposite directions ("is this plan on
   * offer?"), so the publish permission is the honest fit until the registry
   * gains a retire entry.
   */
  @Post(':id/retire')
  @RequirePermission('plans.publish')
  async retire(@Param('id') id: string) {
    return toPlanView(await this.plans.retire(id));
  }
}

/**
 * The pricing page.
 *
 * Anonymous, published plans only, and a deliberately narrower projection: an
 * unauthenticated visitor gets what they need to choose a plan and nothing
 * about how the catalogue is run.
 */
@Controller('v1/public/plans')
export class PublicPlansController {
  constructor(private readonly plans: PlansService) {}

  @Get()
  @Public()
  async list() {
    const plans = await this.plans.listPublic();
    // Not cursor-paginated: the catalogue is a handful of rows by construction,
    // and a pricing page that arrived in pages would be a worse pricing page.
    return { items: plans.map(toPublicPlanView) };
  }
}
