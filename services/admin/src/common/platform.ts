/**
 * The one authorization rule this service cannot get wrong.
 *
 * Admin is the only service that can see every tenant at once. Everywhere else
 * a missed check leaks one company's data to one other company; here it leaks
 * all of them. So the check is made twice, deliberately, and by two different
 * mechanisms:
 *
 *   1. `@PlatformOnly(...)` on the controller — metadata the global
 *      `AuthorizationGuard` enforces before a handler is entered. This is the
 *      real gate.
 *   2. `assertPlatform(principal)` inside the handler — a runtime assertion on
 *      the principal the request actually carries.
 *
 * The second is redundant while the guard is registered. That is the point: a
 * refactor that drops `APP_GUARD` from a module, or a controller copied without
 * its decorator, turns a silent cross-tenant disclosure into a 403. Redundant
 * checks are cheap; this one is a few nanoseconds against a database round trip.
 *
 * Note also what is *not* here. The platform-wide company filters on these
 * routes (`?company=`, `/companies/{id}`) read a tenant id from the request,
 * which everywhere else in this platform is forbidden. It is safe here for
 * exactly one reason: a platform principal has no tenant of its own, so the
 * parameter is selecting a subject to read *about*, not asserting an identity.
 * The moment a non-platform principal could reach these handlers that reasoning
 * collapses — which is why the principal type is checked twice and the
 * permission never stands in for it.
 */
import { applyDecorators } from '@nestjs/common';
import { RequirePermission, RequirePrincipalType, forbidden } from '@karmarat/nestshared';
import type { Principal } from '@karmarat/nestshared';

/**
 * Restricts a route to platform staff holding every listed permission.
 *
 * Permissions must already exist in services/identity/internal/rbac/registry.go.
 * The ones this service uses are all platform-scoped, which the identity
 * service's scope rules mean a company or candidate role can never be granted —
 * a second, independent reason a tenant cannot reach these routes.
 */
export function PlatformOnly(...permissions: string[]) {
  return applyDecorators(RequirePrincipalType('platform'), RequirePermission(...permissions));
}

/** Backstop for the guard. See the note above on why this exists twice. */
export function assertPlatform(principal: Principal): void {
  if (principal.type !== 'platform') {
    throw forbidden('This endpoint is not available to your account type.');
  }
}
