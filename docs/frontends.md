# Front ends

Four Next.js applications, one per portal. They are separate deployments because
they are separate audiences with separate trust levels — a bug in the public
marketing site should not be able to reach the platform console, and hostname
routing already draws that line at the gateway.

| App | Host | Audience |
| --- | --- | --- |
| `apps/web-landing` | `{hostname}` | Visitors: marketing, pricing, company and candidate sign-up |
| `apps/web-jobs` | `jobs.{hostname}` | Candidates: profile, job discovery, applications, messages |
| `apps/web-company` | `{slug}.{hostname}` | Recruiters: the existing workspace, plus the public careers portal |
| `apps/web-admin` | `root.{hostname}` | Platform staff: tenants, plans, payments, support |

## The rule that shapes all of them

**No front end talks to a database.** Every read and write goes through the
gateway, which is the only component that verifies a token and resolves a
tenant. `apps/web-company` currently violates this — it holds its own Drizzle
schema and a `DATABASE_URL` pointing at Neon — and migrating it is step 4 of the
build order for exactly that reason.

A front end therefore has no authorization logic of its own that matters. It
hides what the user cannot do so the UI is not a maze of dead ends, but the
answer that counts comes from the API. This is already how the RBAC layer in
`apps/web-company` is built: `AccessEvaluator` decides what to render, and the
server re-checks on every call.

## Shared pieces

`packages/ui` holds what more than one app needs. It is built and tested (34
tests), and every portal imports it as `@karmarat/ui`:

| Module | What it is |
| --- | --- |
| `client.ts` | `ApiClient` — attaches the access token, refreshes once on 401, replays only safe methods, signs out when refresh fails |
| `problem.ts` | `ProblemError` — one error type for both backend runtimes, with `.fieldError(name)` for 422s |
| `access.ts` | `AccessEvaluator` and `snapshotFromSession` |
| `react.tsx` | `AccessProvider`, `ApiProvider`, `useAccess`, `useApi`, `<Can>` |
| `server.ts` | `gatewayRequest` — the server-side transport, imported as `@karmarat/ui/server` |

The API client is the piece worth getting right once. Three rules in it are the
ones that are easy to get *almost* right:

- **One refresh, however many requests are waiting.** Refresh tokens rotate on
  use, so a second concurrent refresh presents a spent token — which identity
  treats as theft and answers by revoking every session the account holds. A
  shared in-flight promise is what stops one page load signing the user out
  everywhere.
- **Replay only GET, HEAD and OPTIONS.** A POST that reached the service and
  failed to *respond* would be applied twice. Asking the user to sign in again
  is annoying; a duplicate charge is not.
- **The access token never touches `localStorage`.** It lives in memory; the
  refresh token is an httpOnly cookie exchanged through a same-origin route
  handler. That makes an XSS a defacement rather than a credential theft.

The design system is *not* shared. Each app carries its own copy of the shadcn
primitives and tokens it needs, copied from `apps/web-company`. Lifting them
into a package would mean restructuring the company portal's components, and
that app is in use — the cost is a little duplication, the benefit is that
nothing already working gets disturbed.

## Auth flow

1. Sign-in posts to `/api/v1/auth/login` through the gateway on the app's own
   hostname, so the portal and realm are established by where the request
   arrives rather than by a field the client sets.
2. The access token (15 min) is held in memory; the refresh token goes in an
   httpOnly, secure, SameSite=Lax cookie scoped to the portal's hostname.
   Keeping the access token out of `localStorage` means an XSS cannot exfiltrate
   a long-lived credential.
3. A Next.js route handler is the only thing that reads the refresh cookie and
   exchanges it — the browser never sees the refresh token.
4. Server components read the session server-side and act as route guards, as
   `apps/web-company` already does.

## The Host header, and the bug it caused

Portals are told apart by hostname. The gateway resolves the portal — and
therefore which routes exist at all — from the `Host` header, before any handler
runs. So every app keeps two separate settings:

- `GATEWAY_URL` — **where to connect**. `localhost:8080` in development.
- `PORTAL_HOST` — **who we say we are**. `acme.karmarat.local`, and so on.

Server-side calls must go through `gatewayRequest` from `@karmarat/ui/server`,
never `fetch`. Node's `fetch` implements the WHATWG forbidden-header list and
`Host` is on it: a `Host` header set on a fetch is dropped silently and replaced
with the URL's authority. Three of the four portals were built with `fetch`, so
every server-side call arrived claiming to be `localhost:8080`, resolved the
public portal, and was answered 404.

It is worth dwelling on why that survived so long. The failure produces no error
anywhere — the gateway logs an ordinary 404, and the portals wrap their reads in
"fall back to an empty value", which is right for a dashboard composed of a dozen
independent panels. The result was a company portal that signed in perfectly and
then showed a dashboard of zeros, an empty pipeline and a settings page with no
roles. Every one of those looks exactly like a tenant with no data.

Two things now make it hard to reintroduce. `gatewayRequest` sends over
`node:http`, and `packages/ui/src/server.test.ts` asserts against a real socket
that the portal hostname is what arrives — the only kind of test that could catch
it, since the header is present right up until the bytes go out. And
`gatewayRead` now logs what it swallowed, so an endpoint returning 404 and a list
that is genuinely empty no longer look alike.

## Running them locally

| App | Dev port | Hostname |
| --- | --- | --- |
| `web-company` | 3000 | `acme.karmarat.local` |
| `web-jobs` | 3001 | `jobs.karmarat.local` |
| `web-admin` | 3002 | `root.karmarat.local` |
| `web-landing` | 3003 | `karmarat.local` |

To browse a portal *by hostname*, those names have to resolve. That needs sudo,
so it is a step you run yourself:

```
127.0.0.1  karmarat.local root.karmarat.local jobs.karmarat.local acme.karmarat.local
```

Append it as its own line. Adding it to the end of an existing line — which is
what `echo ... >> /etc/hosts` does when the file has no trailing newline — makes
the whole line a comment, and nothing resolves.

You do not need it for `web-company`: reached at `localhost:3000` the hostname
carries no slug, so `COMPANY_SLUG` in its `.env` says which company the dev
server is the portal for. That only chooses the name sent to the gateway; the
gateway still resolves the tenant itself.

## What the portals cannot do yet

Every feature in the platform's own permission catalogue now has a service
behind it. Interviews, offers and the tenant audit trail were the last three,
and `apps/web-company/src/lib/gateway/unavailable.ts` is down to screens for
things the catalogue never defined:

| Screen | Why there is nothing to call |
| --- | --- |
| Email templates | Messaging serves conversations, not stored templates. No service owns one. |
| Email delivery settings | Notifications owns email fan-out centrally. There is no per-tenant SMTP, by design. |
| Integrations | No outbound-webhook or integrations endpoint exists on any service. |
| Feature access | Per-tenant feature switches were this app's idea; the platform gates on subscription entitlements, which the billing screen already shows. |
| Master data | Not a gap. Work modes, employment types, seniority bands, job statuses, currencies and pay frequencies are enums the jobs service enforces, so a dropdown can only offer a value the API accepts. Departments and locations are free text. |
| Setting another member's password | Declined. Identity owns credentials and serves no endpoint for one member to overwrite another's — it is an account takeover carried out with a permission that otherwise edits a job title. |

These are not unfinished platform features. They are screens the old
database-backed app carried for concepts this platform decided differently
about, and each one says so rather than rendering an empty list.

One thing the reports screen used to do is worth naming, because it is the
failure mode a "finished" product hides best: every headline figure had an
invented fallback — `data?.avgTimeToHireDays || 18`, `|| 92` for acceptance
rate, and a four-row sourcing table with made-up conversion percentages. Nothing
supplied those fields, so the fallbacks always rendered. A tenant with an empty
pipeline was shown "18 days average time to hire" and "48 applicants via Careers
Website". They are computed now, and report an em dash where there is nothing to
measure — because a zero reads as "instant", not "unknown".

## Build order

1. `packages/ui` — the API client and the ported access layer
2. `apps/web-jobs` — the candidate portal; it exercises the most new services
   (candidates, jobs, applications, messaging, notifications) and so proves the
   whole backend end to end
3. `apps/web-admin` — the platform console over companies, subscriptions,
   payments, support, admin
4. Migrate `apps/web-company` from Drizzle to the gateway, feature by feature,
   keeping its RBAC layer intact
5. `apps/web-landing` — marketing, pricing from the public plans endpoint, and
   the two sign-up flows

`web-jobs` comes before `web-admin` because it is the path a real user walks:
register, build a profile, find a job, apply, get a message. Anything broken in
the backend shows up there first.
