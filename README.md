# Karmarat

[![CI](https://github.com/vecvel/karmarat/actions/workflows/ci.yml/badge.svg)](https://github.com/vecvel/karmarat/actions/workflows/ci.yml)
[![Promotion Pipeline](https://github.com/vecvel/karmarat/actions/workflows/deploy.yml/badge.svg)](https://github.com/vecvel/karmarat/actions/workflows/deploy.yml)
![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go&logoColor=white)
![Node](https://img.shields.io/badge/Node-20+-339933?style=flat&logo=node.js&logoColor=white)
![Next.js](https://img.shields.io/badge/Next.js-15-000000?style=flat&logo=next.js&logoColor=white)
![pnpm](https://img.shields.io/badge/pnpm-11.x-F69220?style=flat&logo=pnpm&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16%2F17-4169E1?style=flat&logo=postgresql&logoColor=white)
![NATS](https://img.shields.io/badge/NATS-JetStream-27AAE1?style=flat&logo=nats.io&logoColor=white)
![Ingress](https://img.shields.io/badge/Ingress-Caddy%20TLS-1F88C0?style=flat&logo=caddy&logoColor=white)
![Architecture](https://img.shields.io/badge/Architecture-Event--Driven%20Microservices-7B2CBF?style=flat)
![Multi-Tenancy](https://img.shields.io/badge/Tenancy-Subdomain%20Isolated-blueviolet?style=flat)
![License](https://img.shields.io/badge/License-Proprietary-red.svg?style=flat)

A multi-tenant recruitment platform. Companies get isolated careers portals,
candidates get one central job portal, and the platform is operated from a
separate administration console.

## Layout

```
apps/
  web-company/      A company's own recruiting portal — {slug}.{host}  (Next.js)
  web-jobs/         The candidate job portal — jobs.{host}             (Next.js)
  web-admin/        The platform administration console — root.{host}  (Next.js)
  web-landing/      Marketing, pricing and both sign-ups — {host}      (Next.js)
services/
  gateway/          API gateway — host routing, tenancy, token verification (Go)
  identity/         Accounts, sessions, roles, permissions, a company's team (Go)
  jobs/             Requisitions, dual visibility, custom application forms (Go)
  applications/     Pipeline, dynamic stages, one-application-per-job (Go)
  candidates/       Profiles, resumes, discoverability, talent search (Go)
  messaging/        Recruiter ↔ candidate conversations (Go)
  notifications/    In-app, SSE stream, email fan-out (Go)
  interviews/       Rounds, panels, scorecards (Go)
  offers/           Packages, approval, dispatch, response (Go)
  audit/            Every platform event, per tenant and platform-wide (Go)
  companies/        Registration, profile, careers portal settings (NestJS)
  subscriptions/    Plans, subscriptions, entitlements (NestJS)
  payments/         Provider-agnostic billing, invoices, webhooks (NestJS)
  support/          Support tickets, company and platform sides (NestJS)
  admin/            Root console aggregation over projections (NestJS)
packages/
  goshared/         Shared Go platform library
  nestshared/       Shared TypeScript platform library (the NestJS counterpart)
  ui/               Shared front-end layer: API client, access evaluator, transport
scripts/            dev.sh (run the stack), smoke.sh + smoke-product.sh (live suites)
deploy/             Infrastructure, service images, local orchestration
docs/               Architecture, service contract, front ends, RBAC, production plan
```

Both shared libraries expose the same concepts — problem+json, the gateway
principal, the permission guard, cursor pagination, the migration runner, the
event envelope — so a service behaves the same way whichever runtime it is
written in.

**No front end holds a database.** There is no connection string in any app,
and there is not meant to be one: a portal that could read a table could read
another tenant's rows, and the boundary that stops that lives at the gateway.

## Getting started

Requires Go 1.26+, Node 20+, pnpm, and Docker.

```bash
make setup      # signing keys, .env, and the infrastructure containers
make dev        # infrastructure, every service, and the company portal
```

`make dev` runs the backend and `web-company` on :3000. The other three portals
are separate Next servers you start yourself when you need them:

```bash
pnpm --filter @karmarat/web-jobs dev      # :3001
pnpm --filter @karmarat/web-admin dev     # :3002
pnpm --filter @karmarat/web-landing dev   # :3003
```

`make dev` discovers every service in the tree, builds it, and waits for its
readiness endpoint before starting the next. Three behaviours are deliberate:

- **A service that does not compile or start is skipped, loudly, by name.** One
  service still being written then costs you its own routes — which answer 502,
  exactly as a deployed-but-down service would — instead of the whole platform.
- **A port held by something `dev.sh` did not start is left alone**, and that
  service is skipped. Killing an unknown process would be worse than not
  starting one.
- **`make dev-down` sweeps those ports anyway.** Stopping is explicit intent, so
  it clears stale listeners a crash or a hand-started process left behind.
  Without that sweep the skip above quietly leaves an *old binary* serving while
  a fresh `make dev` reports success — a confusing way to lose an afternoon.

```bash
make status        # what is running, and on which port
make smoke-all     # both test suites against the running stack
make dev-down      # stop the services (infrastructure keeps running)
```

Two suites run against the live stack:

| | |
| --- | --- |
| `make smoke` | Security boundaries: cross-tenant tokens, header spoofing, portal isolation, credential enumeration, refresh replay |
| `make smoke-product` | The product: post a job, publish it, apply, advance the pipeline, reject, discover talent, schedule a round, file scorecards, draft an offer, read the audit trail |

Migrations run automatically at service boot, so `make migrate` is only needed
to apply them without starting anything. It depends on `make db`, which creates
any service database a running Postgres is missing — the container's init script
only runs on a fresh volume, so a service added later would otherwise fail to
start with an error that says nothing about the cause.

To run the services by hand instead, in separate terminals:

```bash
make run-identity   # :8081
make run-gateway    # :8080
```

### First sign-in

`make setup` writes bootstrap credentials into `.env`. The identity service
creates that administrator on first boot and logs a warning; sign in, change the
password, then remove the `BOOTSTRAP_ADMIN_*` lines.

```bash
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -H 'Host: root.karmarat.local' \
  -d '{"realm":"platform","email":"admin@karmarat.local","password":"..."}'
```

### Local hostnames

Portals are routed by hostname. For browser testing, add them to `/etc/hosts`:

```
127.0.0.1  karmarat.local root.karmarat.local jobs.karmarat.local acme.karmarat.local
```

With `curl`, a `Host:` header is enough. `web-company` does not need the hosts
entry at all: reached at `localhost:3000` its hostname carries no slug, so
`COMPANY_SLUG` in its `.env` says which company the dev server is the portal
for.

Append the line as its own line. Adding it to the end of an existing one —
which is what `echo ... >> /etc/hosts` does when the file has no trailing
newline — makes the whole line a comment, and nothing resolves.

## Commands

| Command | Description |
| --- | --- |
| `make setup` | Keys, `.env`, and infrastructure |
| `make dev` / `make dev-down` | Start or stop the whole stack |
| `make status` | Show what is running |
| `make smoke` / `make smoke-product` / `make smoke-all` | Test suites against a running stack |
| `make infra` / `make infra-down` | Start or stop the containers |
| `make infra-reset` | Destroy the volumes and start clean |
| `make db` | Create any service database a running Postgres is missing |
| `make migrate` | Apply migrations for every service |
| `make build` / `make test` | Build and test the Go services |
| `make verify` | Everything CI runs: build, vet, gofmt, tests, and the two generated-file checks |
| `make images` | Build every service image, to prove the deployment path still works |
| `make stack-up` / `make stack-down` / `make stack-logs` | Run the whole platform in containers |
| `make prod-build` / `make prod-up` / `make prod-down` | Hardened production stack with Caddy edge proxy |
| `make env-check` | Validate `.env` configuration invariants |
| `make seed-uat` | Seed demo tenants and candidate pipelines for UAT |
| `make backup` | Create snapshot backup of Postgres and MinIO |
| `make db-clean` / `make db-clean-seed` | Reset service databases and optionally seed UAT demo data |
| `make db-pull` / `make db-push` | Pull or push database snapshots between local and remote environments |
| `make clean` | Remove compiled binaries, local runtime logs, and test artifacts |
| `make rebrand` | Rebrand platform (e.g. `make rebrand OLD=reqruitbook NEW=Karmarat [DRY=1]`) |
| `make run-gateway` / `make run-identity` | Run a single service |

## Local services

| Service | Address |
| --- | --- |
| Gateway | http://localhost:8080 |
| Company portal | http://localhost:3000 |
| Candidate portal | http://localhost:3001 |
| Admin console | http://localhost:3002 |
| Marketing site | http://localhost:3003 |
| Identity | http://localhost:8081 (services run on 8081–8094) |
| Postgres | localhost:5432 |
| Redis | localhost:6379 |
| NATS | localhost:4222 (monitor :8222) |
| MinIO | http://localhost:9001 |
| Mailpit | http://localhost:8025 |
| Jaeger | http://localhost:16686 |

## Documentation

- [Platform architecture](docs/architecture.md) — services, tenancy, request path, security
- [Environment lifecycle & multi-tier promotion](docs/environment-lifecycle.md) — Local, Staging, UAT, Production progression, wildcard SSL, and run commands
- [Production readiness blueprint](docs/production-plan.md) — self-hosting architecture, hardening, and disaster recovery
- [Service contract](docs/contracts.md) — what every service must do to compose with the platform, and the traps that cost an hour each
- [Front ends](docs/frontends.md) — the four portals, the Host-header rule they all depend on, and what they still cannot do
- [Feature-based RBAC](docs/rbac.md) — the permission model used by the company portal
