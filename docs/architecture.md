# Karmarat Platform Architecture

A multi-tenant recruitment SaaS. Companies get isolated careers portals,
candidates get one central job portal, and the platform team operates both from a
separate admin console.

## Portals

Routing is by hostname, so a portal boundary cannot be crossed by editing a path.

| Host | Portal | Who |
| --- | --- | --- |
| `{hostname}` | Public | Marketing site, company and candidate sign-up |
| `root.{hostname}` | Root | Platform staff: companies, plans, payments, support |
| `jobs.{hostname}` | Jobs | Candidates: profile, discovery, applications |
| `{slug}.{hostname}` | Company | A tenant's careers portal and recruitment workspace |

`root`, `jobs`, `www`, `api`, and ~20 others are reserved and cannot be claimed
as a company slug — the reserved list and the host router read the same constant,
so they can never disagree.

## Services

Go handles the latency- and concurrency-sensitive paths; NestJS handles the
integration- and business-rule-heavy ones.

| Service | Language | Owns | Status |
| --- | --- | --- | --- |
| `gateway` | Go | Host routing, tenant resolution, token verification, rate limiting | **Built** |
| `identity` | Go | Accounts, sessions, roles, permissions, memberships | **Built** |
| `jobs` | Go | Requisitions, dual visibility, custom application forms | **Built** |
| `applications` | Go | Pipeline, dynamic stages, one-application-per-job, rejections | **Built** |
| `candidates` | Go | Candidate profiles, resumes, discoverability, talent search | **Built** |
| `messaging` | Go | Recruiter ↔ candidate conversations | **Built** |
| `notifications` | Go | Real-time delivery over SSE, email fan-out | **Built** |
| `companies` | NestJS | Company registration, profile, careers portal settings | **Built** |
| `subscriptions` | NestJS | Plans, subscriptions, entitlements | **Built** |
| `payments` | NestJS | Provider-agnostic billing (Stripe first), invoices, webhooks | **Built** |
| `support` | NestJS | Company support tickets and platform replies | **Built** |
| `admin` | NestJS | Root console aggregation | **Built** |

Each service owns its own Postgres database. There are no cross-service joins:
a service that needs another's data either calls its API or keeps a projection
fed by events.

## Request path

```
Browser
  │  Host: acme.karmarat.com
  │  Authorization: Bearer <access token>
  ▼
Gateway
  1. Strip every trust header the client sent
  2. Resolve host  → portal = company, slug = acme
  3. Resolve slug  → tenant (Redis-cached, 30s)
  4. Match route   → is it exposed on this portal?
  5. Verify token  → RS256, issuer, audience, expiry
  6. Check principal type against the route
  7. Check token.companyId == host tenant     ← the tenant boundary
  8. Check subscription entitlement
  9. Rate limit (per account, or per IP when anonymous)
 10. Inject X-Principal-*, X-Company-ID, X-Permissions
  ▼
Service — trusts the headers, filters every query by company id
```

Steps 1 and 7 are what make the tenancy real. A client cannot assert an identity
because its headers are discarded, and a valid token for one company is refused
on another company's hostname.

## Identity model

Three **realms**, so the same person can hold a candidate account and a recruiter
account on the same email without the two colliding:

```
accounts (realm, email)         unique per realm
  └─ company_memberships        one account may work for several companies
       └─ membership_roles      roles are held through the membership
  └─ account_roles              platform and candidate roles
```

Revoking access at one employer therefore leaves the other untouched.

**Permissions** are `<feature>.<action>` — `jobs.publish_network`,
`offers.view_compensation`, `applications.advance_stage`. They are declared once
in `services/identity/internal/rbac/registry.go`; the role editor, the token
issuer, and every guard read from that one list. Scopes never overlap, so a
platform role cannot carry a company permission.

**Tokens**: access tokens are 15-minute RS256 JWTs carrying the resolved
permission list, so no service needs a lookup to authorize. Refresh tokens are
opaque, stored hashed, and rotated on every use — which is what makes revocation
real and token theft detectable.

## Events

NATS JetStream, subject `karmarat.<domain>.<event>`, 30-day retention, durable
consumers with backoff. Publishing is de-duplicated by event id so a retry after
a network blip does not deliver the same fact twice.

Identity consumes `company.*` and `subscription.*` to maintain its tenant
projection — which is how a lapsed subscription closes a portal without putting
two services on the sign-in path.

## Data

- **PostgreSQL 17** — one database per service
- **Redis 7** — tenant cache, rate limiting, sessions, real-time fan-out
- **NATS JetStream** — durable domain events
- **MinIO / S3** — resumes, offer letters, company assets
- **Jaeger** — distributed tracing

## Security posture

| Concern | Measure |
| --- | --- |
| Password storage | Argon2id, 64 MiB / 3 passes, per-password salt |
| Credential enumeration | Same response and same hashing cost for unknown emails |
| Brute force | 5 attempts then a 15-minute lock, plus per-IP rate limits |
| Token forgery | RS256 with a pinned algorithm; only identity holds the private key |
| Token theft | 15-minute access tokens; refresh rotation with replay detection |
| Cross-tenant access | Token tenant must match the host tenant at the gateway |
| Header spoofing | Trust headers stripped from every inbound request |
| Privilege escalation | You may only grant permissions you hold yourself |
| Lapsed billing | Entitlement checked per request; sessions revoked on lapse |

## What is built

```
packages/goshared/     config, logging, postgres (+migrations), redis (+rate limiting),
                       events, httpx (server, middleware, problem+json, auth),
                       tokens, tenancy, idgen
services/identity/     schema, store, RBAC registry, auth flows, provisioning,
                       projection consumer, HTTP API, migration command
services/gateway/      routing table, tenant resolver, proxy pool, security chain
deploy/                docker-compose: Postgres, Redis, NATS, MinIO, Mailpit, Jaeger
scripts/               dev.sh (run the stack), smoke.sh (boundary tests)
apps/web-company/      the existing Next.js company portal, moved into the monorepo
```

All twelve services run: gateway, identity, jobs, applications, candidates,
messaging and notifications in Go; companies, subscriptions, payments, support
and admin on NestJS. Every gateway route reaches a live service.

Two suites assert what this document claims, both against a running stack:

- `scripts/smoke.sh` — the security boundaries: cross-tenant token use, header
  spoofing, portal isolation, principal-type mismatch, credential enumeration,
  refresh replay.
- `scripts/smoke-product.sh` — the product itself: a company posts a job,
  publishes it to both boards, a candidate finds it and applies, the pipeline
  advances and rejects, and talent discovery respects the candidate's switch.

## Honest status

| Capability | State |
| --- | --- |
| Accounts, sessions, roles, permissions | Built |
| Host routing, tenant isolation, rate limiting | Built |
| Job requisitions, dual visibility, custom forms | Built |
| Applications, dynamic stages, one-application-per-job | Built |
| Candidate profiles, resumes, discoverability, talent search | Built |
| Distributed tracing across every service | Built |
| Company self-registration | Built |
| Plans, subscriptions, entitlements | Built — the gateway's subscription gate is now a product feature |
| Recruiter ↔ candidate messaging | Built |
| Notifications: in-app, SSE stream, email | Built |
| Payments, invoices, provider-agnostic checkout | Built |
| Support desk, both sides, with internal notes hidden in SQL | Built |
| Platform admin console over projections | Built |
| Jobs / admin / landing front ends | Not started |
| Company front end | Built, but on its own database and not yet behind the gateway |

## Next

1. `companies` (NestJS) — registration, profile, calls identity to provision a tenant
2. `subscriptions` + `payments` (NestJS) — plans, entitlements, Stripe behind a
   `PaymentProvider` interface
3. `jobs` + `applications` (Go) — requisitions, dual visibility, custom forms,
   one-application-per-candidate
4. Point `apps/web-company` at the gateway instead of its own database
5. `apps/web-jobs`, `apps/web-admin`, `apps/web-landing`
6. `messaging` + `notifications` (Go) — real-time delivery
