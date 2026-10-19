# Feature-Based RBAC

Authorization in Karmarat is derived from a single registry of features. A
feature declares the actions it supports; everything else — the Super Admin
permission matrix, the sidebar, route protection, server actions, and API
endpoints — reads from that declaration. Adding a feature never means editing the
RBAC system.

## Permission model

A permission is always `<feature>.<action>`:

```
jobs.create          applications.advance_stage      offers.view_compensation
jobs.read            applications.reject             offers.approve
jobs.update          candidates.manage_talent_pool   offers.sync_hrm
jobs.delete          interviews.submit_scorecard     roles.assign_permissions
jobs.publish         interviews.view_scorecards      feature-access.update
```

Every feature supports Create, Read, Update and Delete; features add their own
custom actions on top (publish, approve, export, bulk actions, HRM sync, …).

## Registering a feature

`src/features/<feature>/feature.ts`:

```ts
import { defineFeature } from "@/lib/rbac/define";

export const jobsFeature = defineFeature({
  key: "jobs",
  name: "Job Requisitions",
  description: "Openings, job descriptions, salary bands, and the publish workflow",
  icon: "Briefcase",
  group: "recruitment",
  order: 2,
  crud: true,                           // generates jobs.create/read/update/delete
  actions: [                            // feature-specific capabilities
    { action: "publish", label: "Publish to Careers Portal", sensitive: true },
  ],
  nav: [{ label: "Jobs", href: "/jobs", icon: "Briefcase" }],
  routes: [
    { path: "/jobs/new", exact: true, requires: ["jobs.create"] },
    { path: "/jobs", requires: ["jobs.read"] },
  ],
  // Optional: contribute a tab to the Settings screen
  // settings: { tab: "departments", label: "Departments", order: 110 },
});
```

Then add it to `src/features/registry.ts`. That is the whole integration step.
Its permissions appear in the Super Admin matrix, its nav entry is filtered by
those permissions, and its routes are enforced server-side.

## Where it is enforced

| Layer | Mechanism |
| --- | --- |
| Navigation & sidebar | `buildNavigation(access, badges)` in the authenticated layout |
| Settings tabs | `visibleSettingsTabs(access)` — generated from feature `settings` entries |
| Pages / routes | `requireRouteAccess(pathname)` in each `page.tsx` (a server component) |
| Client navigation | `<RouteGuard>` mirrors the server rule to avoid rendering a forbidden screen |
| Buttons, tables, modals, bulk actions | `<Can permission="jobs.create">` / `useAccess()` |
| Server actions | `requirePermission("jobs.create")` at the top of every action |
| API routes | `requireApiPermission` / `withPermission` from `src/lib/rbac/api.ts` |
| Field level | `redactCompensation` strips salary columns without `offers.view_compensation` |

The browser and the server evaluate through the same `AccessEvaluator` class, so
a visible button and the action behind it cannot disagree. UI checks are an
affordance; the server guard is the boundary, and it rejects direct URL access,
hand-made API calls, and modified requests alike.

## Roles and users

- Roles live in the `roles` table and own a list of permission keys, validated
  against the registry on write (unknown keys are dropped).
- Exactly one role per organization carries `is_super_admin`. It short-circuits
  every check and is neither editable nor deletable.
- Users hold **one or more** roles through `user_roles`. Effective access is the
  union of those roles. `users.role` is a denormalized primary-role slug kept in
  sync for display.

### Escalation guards

- A non-Super-Admin can only grant permissions they hold themselves, and can only
  assign roles entirely covered by their own permissions.
- Nobody can change the roles on, deactivate, or delete their own account.
- Only a Super Admin can manage another Super Admin or grant that role.
- The last active Super Admin cannot be removed, demoted, or deactivated.
- Deactivating a user or resetting their password revokes their live sessions.

## Feature access

`organization_features` switches whole modules on or off for the organization. A
disabled feature disappears from navigation and its permissions stop resolving.
Core modules (`users`, `roles`, `feature-access`, `organization`) are marked
`alwaysEnabled` so an administrator cannot lock themselves out of the screen that
turns things back on.

## Code layout

```
src/
  lib/rbac/            RBAC kernel — types, defineFeature, registry, evaluator,
                       navigation, routes, server guard, API guard, redaction
  components/rbac/     AccessProvider, useAccess, <Can>, <RouteGuard>, AccessDenied
  features/
    registry.ts        the one place features are registered
    <feature>/
      feature.ts       permissions, navigation, routes, settings tab
      server/          guarded server actions and queries
      components/      the feature's screens
  app/                 thin route files: guard, then render the feature component
  db/schema/           tables grouped by feature, re-exported from index.ts
```

The public careers portal is the only unauthenticated surface. Its server actions
live in `src/features/careers/server/public-actions.ts` and
`public-queries.ts` — kept apart precisely so that skipping the guard is a
deliberate, reviewable decision rather than an oversight.

## Migration

For an existing database:

```bash
npm run db:migrate:rbac
```

The script is idempotent. It adds `roles.is_super_admin`, `user_roles` and
`organization_features`, translates legacy `canDoThing` permissions into
`<feature>.<action>` keys, promotes the old `system_admin` role to the single
`super_admin` role, and backfills role assignments. Pre-existing roles also
receive read access to configuration lookups so existing forms keep working.
