/**
 * Authorization guards, mirroring `packages/goshared/httpx`.
 *
 * The order matters and it is the same in every service: establish who the
 * caller is, then whether they are the right *kind* of caller for this portal,
 * then whether their role carries the permission. The tenant filter in the
 * repository is a fourth, separate check — the permission says what a role may
 * do, the tenant filter says whose data they may do it to, and neither
 * substitutes for the other.
 */
import {
  CanActivate,
  ExecutionContext,
  Injectable,
  NestMiddleware,
  SetMetadata,
  createParamDecorator,
  type CustomDecorator,
} from '@nestjs/common';
import { Reflector } from '@nestjs/core';
import { timingSafeEqual } from 'node:crypto';
import type { NextFunction, Request, Response } from 'express';

import { Principal } from './tenancy';
import type { PrincipalType } from './tenancy';
import { forbidden, unauthorized } from './problem';

/**
 * Express's Request with the principal the gateway middleware attaches.
 *
 * A local type rather than a module augmentation: augmenting express's internals
 * from a library leaks a global change into every consumer, and a service that
 * pins a different @types/express would silently stop compiling.
 */
export interface PrincipalRequest extends Request {
  principal?: Principal;
}

/**
 * Reconstructs the principal from the gateway's headers.
 *
 * Applied as global middleware so no route can forget it. An unauthenticated
 * request continues as anonymous rather than being rejected here — public job
 * boards and authenticated portals share the same pipeline, and the route
 * decides what public means.
 */
@Injectable()
export class GatewayPrincipalMiddleware implements NestMiddleware {
  use(req: PrincipalRequest, _res: Response, next: NextFunction): void {
    req.principal = Principal.fromHeaders(req.headers);
    next();
  }
}

/** Reads the principal a route is acting for. */
export const CurrentPrincipal = createParamDecorator((_data: unknown, ctx: ExecutionContext): Principal => {
  const request = ctx.switchToHttp().getRequest<PrincipalRequest>();
  return request.principal ?? Principal.anonymous();
});

/**
 * Reads the tenant, throwing when the caller has none.
 *
 * Using this instead of `@CurrentPrincipal()` plus a manual read is what keeps
 * a handler from ever seeing an empty company id.
 */
export const CompanyId = createParamDecorator((_data: unknown, ctx: ExecutionContext): string => {
  const request = ctx.switchToHttp().getRequest<PrincipalRequest>();
  return (request.principal ?? Principal.anonymous()).requireCompany();
});

const PERMISSIONS_KEY = 'karmarat:permissions';
const PRINCIPAL_TYPES_KEY = 'karmarat:principal_types';
const PUBLIC_KEY = 'karmarat:public';

/**
 * Requires every listed permission.
 *
 * Keys must already exist in services/identity/internal/rbac/registry.go; a
 * permission invented at the call site can never be granted to anyone, so the
 * route becomes silently unreachable.
 */
export const RequirePermission = (...permissions: string[]): CustomDecorator =>
  SetMetadata(PERMISSIONS_KEY, permissions);

/** Restricts a route to one or more principal kinds. */
export const RequirePrincipalType = (...types: PrincipalType[]): CustomDecorator =>
  SetMetadata(PRINCIPAL_TYPES_KEY, types);

/** Marks a route as reachable without authentication. */
export const Public = (): CustomDecorator => SetMetadata(PUBLIC_KEY, true);

@Injectable()
export class AuthorizationGuard implements CanActivate {
  constructor(private readonly reflector: Reflector) {}

  canActivate(context: ExecutionContext): boolean {
    const targets = [context.getHandler(), context.getClass()];

    if (this.reflector.getAllAndOverride<boolean>(PUBLIC_KEY, targets)) {
      return true;
    }

    const request = context.switchToHttp().getRequest<PrincipalRequest>();
    const principal = request.principal ?? Principal.anonymous();

    if (!principal.isAuthenticated) {
      throw unauthorized('You must be signed in to perform this action.');
    }

    const allowedTypes = this.reflector.getAllAndOverride<PrincipalType[]>(PRINCIPAL_TYPES_KEY, targets);
    if (allowedTypes?.length && !allowedTypes.includes(principal.type as PrincipalType)) {
      throw forbidden('This endpoint is not available to your account type.');
    }

    const required = this.reflector.getAllAndOverride<string[]>(PERMISSIONS_KEY, targets);
    if (required?.length) {
      const missing = required.filter((permission) => !principal.can(permission));
      if (missing.length) {
        throw forbidden(`You do not have permission to perform this action (${missing.join(', ')}).`);
      }
    }

    return true;
  }
}

/**
 * Guards service-to-service endpoints with a shared secret.
 *
 * The comparison is constant time: a byte-by-byte compare leaks the secret to
 * anyone patient enough to measure it. An unset secret denies everything rather
 * than allowing everything, so a misconfigured deploy fails closed.
 *
 * A controller using it must ALSO be marked `@Public()`. AuthorizationGuard is
 * registered globally and runs before route-level guards, so without `@Public()`
 * an internal call is refused for carrying no principal before this guard ever
 * inspects the secret.
 *
 * It deliberately has NO constructor parameters. Nest instantiates a guard named
 * in `@UseGuards(SomeGuard)` from the class itself, before providers are
 * consulted, so a constructor parameter — even one with a default value —
 * becomes an unresolvable dependency and the service fails to boot with
 * "can't resolve dependencies of the InternalTokenGuard (?)". A service that
 * would rather take the secret from its parsed configuration than from the
 * environment overrides `expectedToken()`.
 */
@Injectable()
export class InternalTokenGuard implements CanActivate {
  /** The secret this guard checks against. Override to read parsed config. */
  protected expectedToken(): string {
    return process.env.INTERNAL_SERVICE_TOKEN ?? '';
  }

  canActivate(context: ExecutionContext): boolean {
    const expected = Buffer.from(this.expectedToken(), 'utf8');
    if (expected.length === 0) {
      throw unauthorized('This endpoint is not available.');
    }

    const request = context.switchToHttp().getRequest<PrincipalRequest>();
    const header = request.headers['x-internal-token'];
    const supplied = Buffer.from((Array.isArray(header) ? header[0] : header) ?? '', 'utf8');

    // timingSafeEqual throws on a length mismatch, which would itself leak the
    // secret's length; compare lengths separately and still run the compare.
    const sameLength = supplied.length === expected.length;
    const padded = sameLength ? supplied : Buffer.alloc(expected.length);
    if (!timingSafeEqual(padded, expected) || !sameLength) {
      throw unauthorized('This endpoint is not available.');
    }

    return true;
  }
}
