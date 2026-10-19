# Service implementation contract

Every service in this platform is built the same way. This document is the
contract: follow it and a new service composes with the gateway, the token
model, the event bus and the tenancy rules without further coordination.

It exists because the boundaries that make this system multi-tenant are only
real if *every* service honours them. One service that reads a company id from
a request body instead of the verified principal undoes the isolation the
gateway provides.

## 1. The trust model

A service never sees a raw token. The gateway verifies it, strips whatever
trust headers the client sent, and sets its own:

| Header | Meaning |
| --- | --- |
| `X-Principal-Type` | `platform`, `company` or `candidate` |
| `X-Principal-ID` | Account id |
| `X-Company-ID` | Tenant id — **empty for platform and candidate principals** |
| `X-Company-Slug` | Tenant slug |
| `X-Permissions` | Comma-separated `<feature>.<action>` keys |
| `X-Roles` | Comma-separated role keys |
| `X-Session-ID` | Session id, for revocation and audit |
| `X-Principal-Email` | Email, for audit only — never for authorization |

Reconstruct the principal with `httpx.TrustGatewayHeaders`. Never parse these
headers by hand.

**The rule that matters:** the tenant always comes from
`principal.RequireCompany()`, never from the URL, the body or a query
parameter. A handler that accepts a `companyId` field is a cross-tenant read
waiting to happen.

```go
companyID, err := principal.RequireCompany()
if err != nil {
    httpx.WriteProblem(w, r, httpx.Forbidden("This endpoint requires a company context."))
    return
}
rows, err := store.ListJobs(ctx, companyID)   // every query filters by tenant
```

When a service loads a record by id, it must still assert ownership —
`principal.AssertCompany(record.CompanyID)` — because an id is guessable and a
`WHERE id = $1` without a tenant predicate is the classic IDOR.

Prefer `WHERE id = $1 AND company_id = $2` over a load-then-check: it cannot be
forgotten at a later call site, and it returns "not found" rather than
confirming that another tenant's record exists.

## 2. Layout

Each Go service is laid out exactly like `services/identity`:

```
services/<name>/
  cmd/server/main.go        wiring only — config, deps, routes, Serve
  cmd/migrate/main.go       apply migrations without starting the server
  internal/api/             HTTP handlers, request/response types, guards
  internal/domain/          entities, enums, errors — no I/O
  internal/store/           SQL, one file per aggregate
  internal/events/          publishers and consumers (when the service has them)
  migrations/
    NNN_<name>.sql          forward-only, checksummed
    embed.go                //go:embed *.sql
```

A NestJS service:

```
services/<name>/
  src/
    main.ts                 bootstrap, global pipes, graceful shutdown
    app.module.ts
    common/                 guards, decorators, filters, problem+json
    <domain>/               controller, service, repository, dto, entity
  migrations/               same SQL convention as the Go services
  Dockerfile
  package.json
  tsconfig.json
```

## 3. Database

One database per service, named after the service. A service connects only to
its own. **There are no cross-service joins and no foreign keys across service
boundaries** — a service that needs another's data either calls its API or
keeps a projection fed by events.

Every tenant-scoped table:

- has a `company_id uuid not null` column
- has an index leading with `company_id`
- carries `created_at timestamptz not null default now()` and `updated_at`

Money is `bigint` minor units plus a `char(3)` currency. Never `float`.

Identifiers are `text` holding a prefixed ULID from `idgen.New("job")`, except
`company_id`, which is a `uuid` minted by the companies service.

Migrations are forward-only and content-checksummed: editing an applied
migration makes the service refuse to start. Add a new file instead.

## 4. HTTP

- Errors are RFC 9457 problem+json via `httpx.WriteProblem`. Never write an
  error body by hand, and never put an internal type name, SQL fragment or
  stack frame in `detail`.
- `422` for validation with a `errors` field map; `404` rather than `403` when
  hiding a record's existence from another tenant; `409` for a genuine
  conflict such as applying twice.
- Every list endpoint is paginated with `?limit=&cursor=`, default 25, max 100.
- Every mutation that money or messaging depends on accepts an
  `Idempotency-Key` header.

Guard order in every route:

```go
httpx.TrustGatewayHeaders            // who is this
  → httpx.RequirePrincipal(company)  // right kind of principal
  → httpx.RequirePermission("jobs.update")
  → handler                          // which re-filters by tenant anyway
```

The permission check and the tenant filter are not redundant. The first says
*may this role do this*; the second says *to whose data*.

## 5. Events

Publish with `bus.Publish(ctx, subject, payload, events.PublishOptions{ID: ...})`.
The id de-duplicates retries. Consume with a durable name of
`<service>-<purpose>`.

A consumer must be idempotent and must tolerate out-of-order delivery. Where
ordering matters, carry a monotonic `version` in the payload and ignore an
event older than the row you hold.

Every event payload carries `companyId` when the fact is tenant-scoped, so a
consumer can filter without a lookup.

## 6. Permissions

Permissions live in `services/identity/internal/rbac/registry.go` and nowhere
else. A service does not invent permission strings; it uses keys that already
exist in the registry. Adding a capability means adding a registry entry, which
makes it appear in the role editor automatically.

The catalogue today, by scope:

**company (65)** — `jobs.*` (create, read, update, delete, duplicate, export,
manage_form, publish_portal, publish_network), `applications.*` (create, read,
update, delete, advance_stage, bulk_update, export, manage_stages, reject),
`candidates.*` (create, read, update, delete, download_resume, export),
`talent_search.*` (search, approach), `interviews.*`, `offers.*` (including
`view_compensation`), `messaging.*` (read, read_all, send), `reports.*`,
`company_profile.*`, `recruiters.*`, `company_roles.*`, `billing.*`,
`support.*`, `company_audit.*`

**platform (44)** — `platform_companies.*`, `platform_candidates.*`, `plans.*`,
`subscriptions.*`, `payments.*`, `platform_support.*`, `platform_users.*`,
`platform_roles.*`, `platform_settings.*`, `platform_audit.*`

**candidate (9)** — `candidate_profile.*` (read, update, delete,
manage_visibility), `candidate_applications.*` (create, read, withdraw),
`candidate_messaging.*` (read, send)

### Delegating permissions

A permission check answers "may this actor do this?". An endpoint that hands out
access has to answer a second question — "may they hand out *that*?" — and the
two are not the same. `company_roles.create` says you may author roles; it does
not say which permissions you may put in one.

Four rules govern every such endpoint. They are implemented once, as pure
functions over permission lists, in `services/identity/internal/team/guards.go`:

- **Delegation.** Every permission being granted must be one the actor holds
  (`rbac.Subset`). Without it, anyone who may create a role can create one
  holding the whole scope and assign it to themselves.
- **Authority.** You may only act on a person, or edit a role, whose access is
  contained within your own. Delegation alone does not cover this: a hiring
  administrator who may assign "Recruiter" could otherwise assign it *to the
  owner*, replacing unrestricted access with a recruiter's — a demotion carried
  out entirely with permissions they legitimately hold.
- **Self-modification.** An actor may not change their own roles or their own
  status. Not redundant with delegation: an actor always holds exactly their own
  permissions, so every self-change passes a delegation check.
- **Last administrator.** A tenant may not be left with no active owner, and may
  not be left with nobody holding unrestricted access. These are two different
  invariants — ownership decides who can still sign in while a subscription has
  lapsed, a super-admin role decides who may administer anything — and a tenant
  can lose the second while keeping the first.

Narrowing somebody's access must also end their live sessions for that tenant
(`RevokeMembershipSessions`), because an access token already issued still
carries the old permission list for up to fifteen minutes. *Widening* it does
not: the next refresh picks it up, and revoking would cost a session for no
security gain.

The rule that is easiest to miss is that **one endpoint can quietly do another
endpoint's job**. `POST /v1/recruiters` looks like a create, but an address that
already has a membership makes it reinstate that person and replace their roles
— which is what `PATCH .../status` and `PUT .../roles` exist for. Written the
obvious way it ran only the delegation check, so an administrator holding
`recruiters.create` could un-suspend a colleague and overwrite a suspended
super admin's roles with a recruiter's. When an endpoint can reach a state
another endpoint guards, it owes that endpoint's guards *and* its permission
(`GuardCapability`). A review caught this one; the live suite now pins it, and a
mutation check confirmed the assertion fails without the guard.

A permission that hides a value must hide every representation of it. The
offers service redacts compensation structurally — the money lives behind an
embedded pointer, so a nil omits all of it at once and a field added later is
covered by construction. That was still not enough: the same figures appear in
the offer *letter*, which the portal composes out of them, and in free-text
custom fields where an allowance ends up. Redacting `baseSalary` while shipping
"your annual base salary will be USD 175,000" redacts nothing. When you gate a
value, ask where else it is written down, and test by grepping the encoded
response for the secret rather than by naming the fields you remembered.

Anything a company writes must be a column the company owns. `full_name` lives
on `accounts`, which every company that person recruits for shares, so setting
it from a company-scoped endpoint renamed them inside other tenants — a
cross-tenant write reached through a screen that looks entirely local. The job
title lives on the membership and is genuinely the company's; the name and the
email are the account holder's.

**Known gap, not solved:** adding an address that already has a platform account
attaches that person to the tenant without their consent, and the response
distinguishes a new account from an existing one — so a company can learn
whether an address already recruits on Karmarat. The honest fix is an
invitation the person accepts (`MembershipInvited` already exists in the model
and `resolveCompanyMembership` already refuses it), which needs email delivery
the platform does not have. Recorded here rather than half-fixed.

## 7. Files

Uploads go to object storage, never to the database and never to local disk.
A service issues a presigned PUT and records the resulting key; it does not
proxy the bytes.

Every uploaded object key is prefixed with the owning tenant
(`company/<companyId>/...` or `candidate/<accountId>/...`), so a bucket policy
can enforce what application code also enforces. Validate the declared content
type against an allow-list and cap the size before signing.

Downloads are presigned GETs with a short expiry, issued only after the same
permission and tenant checks a read would get.

## 8. Health, shutdown, observability

Expose `/healthz` (liveness, no dependency checks) and `/readyz` (readiness,
checking postgres, redis and the bus) via `httpx.Health`. Serve with
`httpx.Serve` so shutdown drains in-flight requests.

Instrument with `observability.Init` from `goshared`. Every inbound request,
outbound call, database query and event publish carries the trace.

## 9. Front ends: the Host header

A portal's server-side calls must go through `gatewayRequest` from
`@karmarat/ui/server`, never `fetch`.

The gateway resolves the portal — and therefore which routes exist at all — from
the `Host` header. Node's `fetch` implements the WHATWG forbidden-header list and
`Host` is on it, so a `Host` header set on a fetch is dropped silently and
replaced with the URL's authority. Every such call arrives as `localhost:8080`,
resolves the public portal, and is answered 404.

Nothing about that looks like a bug from either side. The gateway logs an
ordinary 404; the portal, whose reads fall back to an empty value so one dead
panel does not blank a page, renders zeros. Three of the four portals shipped
that way and the symptom read as "this tenant has no data".

`packages/ui/src/server.test.ts` asserts against a real socket that the portal
hostname is what arrives — the only kind of test that can catch it, because the
header is present right up to the moment the bytes go out.

## 10. NestJS services: five things that cost an hour each

Every one of these was discovered the hard way while building `companies` and
`subscriptions`. They are here so the next service does not rediscover them.

**A controller using `@UseGuards(InternalTokenGuard)` must also be `@Public()`.**
`AuthorizationGuard` is registered globally and runs *before* route guards, so
without `@Public()` an internal call is refused for carrying no principal before
the shared secret is ever checked. "Public" here means "outside the gateway's
principal model" — the secret is what actually guards it.

**`InternalTokenGuard` takes no constructor parameters.** Nest instantiates a
guard named in `@UseGuards` from its class, before providers are consulted, so
any constructor parameter — even one with a default value — is an unresolvable
dependency and the service fails to boot. To read the secret from parsed config
instead of the environment, subclass and override `expectedToken()`.

**Set `"incremental": false` in `tsconfig.build.json`.** `nest-cli` deletes
`dist` before every build; an incremental `tsc` then reads its `.tsbuildinfo`,
concludes nothing changed and emits nothing. The build reports success and
produces no output, and the failure surfaces later as `Cannot find module
dist/main`.

**`scripts/dev.sh` runs `node dist/main` directly, not `npm run start:prod`.**
npm forks the real server as a child, so the recorded pid is the wrapper:
stopping it leaves the server alive and holding the port, and the next start
cannot bind. Your `package.json` still needs `start:prod`, and your build must
produce `dist/main.js`.

**Health endpoints are already `@Public()` in `nestshared`.** Do not re-mark
them. A liveness probe that requires credentials is not a liveness probe — an
orchestrator has no token and never will.

## 11. Tests

A service is not done without:

- table-driven tests for domain rules that do not need I/O
- a test proving the tenant filter: seed two companies, act as one, assert the
  other's rows are invisible
- a test for each `409`/`422` path a client can reach

The tenant test is the one that must exist even when time is short — it is the
regression that would be a breach rather than a bug.
