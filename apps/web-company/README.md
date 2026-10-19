# apps/web-company

A company's recruiting portal — served on `{slug}.{hostname}`, dev port **3000**.

This is where a company's own people work: requisitions, the pipeline, candidates,
conversations, the team, and the roles that decide who may do what.

## The rule

**No front end talks to a database.** There is no `DATABASE_URL` in this app, no
Drizzle, no `pg`. Everything comes from the gateway, which is the only thing that
resolves a tenant or verifies a token. A connection string here would be a second
way into a tenant's data, and it would not pass through any of the checks the
gateway makes.

## Two hostnames, and why they are separate

`GATEWAY_URL` is **where to connect**: `localhost:8080` in development, a cluster
service in production.

`PORTAL_HOST` — or `{COMPANY_SLUG}.{PLATFORM_HOSTNAME}` when it is not set — is
**who we say we are**. The gateway decides the portal, and therefore which routes
exist at all, from the `Host` header. A request that arrives claiming to be
`localhost:8080` resolves the public portal and is answered 404 for every company
route.

Those two being different is why the server-side calls go through
`gatewayRequest` from `@karmarat/ui/server` rather than `fetch`. Node's `fetch`
implements the WHATWG forbidden-header list, and `Host` is on it: a `Host` header
set on a fetch is dropped silently and replaced with the URL's authority. The
symptom is not an error — it is a dashboard of zeros and a settings page with no
roles, because every read falls back to an empty value when the call fails.

## Running it

```sh
# the backend, if it is not already up
./scripts/dev.sh up

pnpm install --filter @karmarat/web-company
pnpm --filter @karmarat/web-company dev     # http://localhost:3000
```

Reached at `localhost:3000` the hostname carries no slug, so `COMPANY_SLUG` in
`.env` says which company this dev server is the portal for. Browsing
`{slug}.{hostname}` instead takes the slug from the URL and ignores it.

You need a tenant to sign in to. Provision one against the running stack:

```bash
ID=$(uuidgen | tr 'A-Z' 'a-z'); . ./.env; curl -s -X POST http://localhost:8081/internal/companies -H "X-Internal-Token: $INTERNAL_SERVICE_TOKEN" -H 'Content-Type: application/json' -d "{\"companyId\":\"$ID\",\"slug\":\"acme\",\"name\":\"Acme Corporation\",\"ownerEmail\":\"dev@acme.test\",\"ownerName\":\"Dev Owner\",\"ownerPassword\":\"Acme-Dev-Pass1!\",\"state\":\"active\"}" && curl -s -X PATCH "http://localhost:8081/internal/companies/$ID/subscription" -H "X-Internal-Token: $INTERNAL_SERVICE_TOKEN" -H 'Content-Type: application/json' -d '{"state":"active","entitlements":{"maxJobs":100,"maxRecruiters":25,"canUseTalentSearch":true}}'
```

The company id must be a UUID. Identity stores the tenant key as text but every
other service stores it as a `uuid` column, so a tenant created with anything
else signs in perfectly and then gets a 500 from jobs, applications and
notifications. Provisioning refuses a non-UUID id for that reason.

The subscription matters too: the portal is closed to everyone but the owner
until billing says otherwise, which is the `402` you will see if you skip the
second call.

## Authorization

Two vocabularies meet in this app and it is worth knowing which is which.

- **The platform's keys** (`recruiters.read`, `company_roles.assign_permissions`)
  are what identity issues in a token and what every service re-checks. They are
  the only thing that decides whether a call succeeds.
- **This app's keys** (`users.read`, `roles.assign_permissions`) come from its own
  feature registry and decide what to render. `lib/gateway/permissions.ts` maps
  one onto the other, and says where they disagree.

The role editor renders the *platform's* catalogue (`lib/rbac/catalogue.ts`),
because a role's permissions live in identity. Everything else — navigation,
route rules, settings tabs — is this app's own structure and comes from
`lib/rbac/registry.ts`.

Nothing in this app is a security decision. `lib/gateway/unavailable.ts` lists
the screens the platform has no endpoint for, each with its reason.
