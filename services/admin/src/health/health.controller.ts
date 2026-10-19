/**
 * The operator's view of platform health.
 *
 * Distinct from `/healthz` and `/readyz`, which belong to the orchestrator and
 * are unauthenticated because a probe carries no token. This one is a console
 * page: platform staff only, and it reports on *other* services.
 *
 * It answers 200 even when everything it probed is down. The status code
 * describes this endpoint's own success — it did probe, and here is what it
 * found — and an operator tool that 503s during an outage is a tool that
 * disappears exactly when it is needed. The body carries the verdict.
 *
 * `platform_settings.read` is the permission. The registry has no "platform
 * health" key and a service may not invent one: an invented key can never be
 * granted to any role, so the route would be unreachable for everyone including
 * the superadmin. Viewing the platform's own operational state is the closest
 * thing the registry describes.
 */
import { Controller, Get } from '@nestjs/common';
import { CurrentPrincipal, Principal } from '@karmarat/nestshared';

import { PlatformOnly, assertPlatform } from '../common/platform';
import { DownstreamProber, type ProbeReport } from './downstream.prober';

@Controller('v1/admin/health')
@PlatformOnly('platform_settings.read')
export class AdminHealthController {
  constructor(private readonly prober: DownstreamProber) {}

  @Get()
  async report(@CurrentPrincipal() principal: Principal): Promise<ProbeReport> {
    assertPlatform(principal);
    return this.prober.report();
  }
}
