# apps/web-landing

The public marketing site — served on `{hostname}`, dev port **3003**.

It has no session. It is the front door: it explains the product, shows the real
plan catalogue, and starts both sign-up journeys.

## The rule

**No front end talks to a database.** There is no `DATABASE_URL` in this app, no
Drizzle, no `pg`. Everything comes from the gateway, which is the only thing
that resolves a tenant or verifies a token.

Every endpoint this app touches is public *by route* at the gateway
(`routing.go` marks them `Public: true`), so there is no token to attach and no
refresh flow to implement. What it does share with the other portals is the
error type: `ProblemError` from `@karmarat/ui`, so a 429 here reads the same
way it reads in the company portal.

## Running it

```sh
# the backend, if it is not already up
./scripts/dev.sh up

pnpm install --filter @karmarat/web-landing
pnpm --filter @karmarat/web-landing dev     # http://localhost:3003
```

`/etc/hosts` — needed to browse the *other* portals by hostname, and to reach
this one on its real name:

```
127.0.0.1  karmarat.local root.karmarat.local jobs.karmarat.local acme.karmarat.local
```

This app does **not** require it to function. It sends `Host: $PORTAL_HOST` on
every server-side gateway call, so `http://localhost:3003` reaches the public
portal correctly even with an untouched `/etc/hosts`.

## Environment

See `.env.example`.

| Variable | Default | What it is |
| --- | --- | --- |
| `GATEWAY_URL` | `http://localhost:8080` | The gateway's origin. Server-side only. |
| `PORTAL_HOST` | `karmarat.local` | Sent as `Host` on every gateway call — this is how the gateway knows the request is on the public portal. Also what the sign-up form shows as `{slug}.{PORTAL_HOST}`. |
| `PORTAL_SCHEME` | `http` in dev, `https` in prod | Used to build links to the other portals. |
| `JOBS_PORTAL_URL` | `{scheme}://jobs.{PORTAL_HOST}` | Override in dev, where the jobs portal is on its own port. |
| `ROOT_PORTAL_URL` | `{scheme}://root.{PORTAL_HOST}` | Same, for the platform console. |

## Why the browser never calls the gateway

A browser cannot set the `Host` header, and the gateway decides the portal from
it. A `fetch` straight from a page would arrive as `localhost:3003` in
development and be routed to the wrong portal — or refused by CORS first. So:

- reads (`/api/v1/public/plans`) happen in server components via `src/lib/gateway.ts`
- the registration write is a **server action** (`signup/company/actions.ts`),
  which also keeps the owner's password out of any client-side request
- the live address check is a **route handler** (`src/app/api/slug-available/`),
  same-origin, which forwards with the right `Host`

## Endpoints used

| Endpoint | Where | Notes |
| --- | --- | --- |
| `GET /api/v1/public/plans` | `pricing/plan-grid.tsx` | Published plans only. Cached 60s. |
| `POST /api/v1/register/company` | `signup/company/actions.ts` | 422 field map rendered beside each input. |
| `GET /api/v1/register/slug-available` | `api/slug-available/route.ts` | **Not currently routed by the gateway** — see below. |

## Known gaps in the platform (not in this app)

**1. `/api/v1/register/slug-available` is not exposed by the gateway.**

`services/companies` implements it (`register.controller.ts`, rate limited by
`SlugCheckRateLimitGuard`), but `services/gateway/internal/routing/routing.go`
only lists `/api/v1/register/company`. The routing table matches on
`path == prefix || strings.HasPrefix(path, prefix + "/")`, so
`/api/v1/register/slug-available` matches nothing and answers 404:

```
$ curl -H 'Host: karmarat.local' localhost:8080/api/v1/register/slug-available?slug=acme
{"status":404,"code":"not_found","detail":"No API route matches this path."}
```

The fix is one line in the gateway's routing table — widening the prefix to
`/api/v1/register`, which is already entirely public and rate limited at the
service. That file is outside this app, so it has not been touched.

Until then the form degrades honestly rather than lying:

- **shape and reserved names** are checked locally in `src/lib/slug.ts` (a
  bounded copy of the generated reserved list), so "root" is still rejected as
  a reserved name the moment it is typed
- **whether a name is taken** cannot be known, so the field says *"We will
  confirm this address when you submit"* — never "available"
- the server's 422 on submit is what finally decides, and is rendered beside
  the field

The same path handles a genuine 429: the check backs off for 30 seconds and
falls back to the same "unknown" state, which is why `SlugStatus` has three
values rather than a boolean.

**2. No plans are published in the dev catalogue.**

`GET /api/v1/public/plans` returns `{"items":[]}` on a fresh stack — there is no
seed for the plan catalogue. The pricing page renders an honest empty state
("No plans are published yet") rather than invented prices. Publish one through
the admin API as a platform principal and it appears within 60 seconds.

## Design

Tokens and the shadcn primitives are **copied** from `apps/web-company` —
`globals.css`, `button`, `input`, `label`, `card`, `badge`, `alert`,
`separator`, `skeleton` — so the four portals are one product without importing
across apps.

One deliberate divergence: `apps/web-company`'s `globals.css` suppresses focus
rings entirely (`:focus-visible { box-shadow: none !important }`). This copy
restores a `--ring` outline on `:focus-visible`. A public registration form has
to be usable with a keyboard, and a mouse click still does not match
`:focus-visible`, so nothing gains a ring it did not earn.

## Checks

```sh
pnpm --filter @karmarat/web-landing typecheck
pnpm --filter @karmarat/web-landing build
pnpm --filter @karmarat/web-landing lint
```
