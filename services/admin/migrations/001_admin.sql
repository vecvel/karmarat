-- Admin console read model.
--
-- This service owns nothing. Every table below is a *projection* of a fact
-- another service published, kept here so the console can answer "how is the
-- platform doing?" without fanning out to ten services on every page load.
--
-- Two consequences follow, and they shape every table:
--
--   1. Nothing here is a source of truth. A row is stale by definition — it is
--      as current as the last event delivered. Never write back to it from a
--      console action; publish to the owning service instead.
--   2. Delivery is at-least-once and out-of-order. Every state table therefore
--      carries `occurred_at`, the timestamp of the newest event applied to it,
--      and an upsert only lands when the incoming event is at least that new.
--      `activity.event_id` is the second half of the story: it is the
--      de-duplication gate every consumer passes through, which is what makes
--      the counters below safe to increment.
--
-- There are no foreign keys between these tables. They are fed by independent
-- streams and a payment can legitimately arrive before the company that paid
-- it has been projected.

-- ---------------------------------------------------------------- activity --

-- The raw event stream, and the audit feed served from it.
--
-- Written first in every consumer transaction: an INSERT ... ON CONFLICT DO
-- NOTHING that affects zero rows means this event has already been applied, so
-- the rest of the transaction is skipped. That is what makes an increment
-- idempotent under redelivery without every payload needing a natural key.
CREATE TABLE activity (
    event_id        text PRIMARY KEY,
    subject         text NOT NULL,
    -- `karmarat.<domain>.<action>` split out, so the feed can be filtered
    -- without a LIKE over the subject.
    domain          text NOT NULL,
    action          text NOT NULL,
    -- NULL for platform-wide facts: a candidate registration belongs to no
    -- tenant, and storing a zero uuid instead would make it look like one.
    company_id      uuid,
    actor_id        text NOT NULL DEFAULT '',
    correlation_id  text NOT NULL DEFAULT '',
    occurred_at     timestamptz NOT NULL,
    payload         jsonb NOT NULL DEFAULT '{}'::jsonb,
    received_at     timestamptz NOT NULL DEFAULT now()
);

-- The feed is always ordered newest-first and paged by keyset, so every index
-- carries event_id as the tiebreaker the cursor compares on.
CREATE INDEX activity_feed_idx ON activity (occurred_at DESC, event_id DESC);
CREATE INDEX activity_company_idx ON activity (company_id, occurred_at DESC, event_id DESC);
CREATE INDEX activity_domain_idx ON activity (domain, occurred_at DESC, event_id DESC);

-- --------------------------------------------------------------- companies --

-- The column is `company_id` rather than `id` because every other table in the
-- platform names the tenant that way, and a join written from memory should not
-- have to remember that this one table is different.
CREATE TABLE companies (
    company_id      uuid PRIMARY KEY,
    slug            text NOT NULL DEFAULT '',
    name            text NOT NULL DEFAULT '',
    -- Mirrors the companies service's own lifecycle vocabulary. Kept as text,
    -- not an enum: this service must not refuse to project a state the owning
    -- service has since added.
    state           text NOT NULL DEFAULT 'pending',
    contact_email   text NOT NULL DEFAULT '',
    country         text NOT NULL DEFAULT '',
    industry        text NOT NULL DEFAULT '',
    registered_at   timestamptz NOT NULL DEFAULT now(),
    approved_at     timestamptz,
    suspended_at    timestamptz,
    occurred_at     timestamptz NOT NULL DEFAULT '-infinity',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX companies_state_idx ON companies (state, registered_at DESC, company_id DESC);
-- The console's default listing is "newest tenants first", unfiltered.
CREATE INDEX companies_registered_idx ON companies (registered_at DESC, company_id DESC);
-- Signups-over-time is a date_trunc on this column over a bounded window.
CREATE INDEX companies_signups_idx ON companies (registered_at);
-- Search is a case-insensitive contains over name and slug. A contains match
-- cannot use a btree, so these help only the prefix case; when the tenant count
-- makes that matter the answer is pg_trgm, which needs an extension this
-- migration deliberately does not create on an operator's behalf.
CREATE INDEX companies_name_lower_idx ON companies (lower(name));
CREATE INDEX companies_slug_lower_idx ON companies (lower(slug));

-- ----------------------------------------------------------- subscriptions --

-- One row per tenant: the console shows the subscription a company is on now,
-- and the subscriptions service remains the place to go for its history.
CREATE TABLE subscriptions (
    company_id      uuid PRIMARY KEY,
    subscription_id text NOT NULL DEFAULT '',
    plan_id         text NOT NULL DEFAULT '',
    plan_name       text NOT NULL DEFAULT '',
    -- The billing period in whole months, which is what MRR normalises by.
    -- Three cases, all of which src/overview/mrr.ts explains:
    --    >= 1  a recurring plan; price_minor / interval_months is its MRR
    --       0  a lifetime or one-off plan; contributes no MRR
    --      -1  a period that is not a whole number of months (weekly, daily);
    --          reported separately rather than approximated
    interval_months integer NOT NULL DEFAULT 1,
    -- Minor units in `currency`. Never a float: a rate that cannot represent
    -- 0.1 exactly has no business in a revenue figure.
    price_minor     bigint NOT NULL DEFAULT 0,
    currency        char(3) NOT NULL DEFAULT 'USD',
    state           text NOT NULL DEFAULT 'inactive',
    started_at      timestamptz,
    expires_at      timestamptz,
    cancelled_at    timestamptz,
    occurred_at     timestamptz NOT NULL DEFAULT '-infinity',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT subscriptions_interval_months_sane CHECK (interval_months BETWEEN -1 AND 120),
    CONSTRAINT subscriptions_price_not_negative CHECK (price_minor >= 0)
);

CREATE INDEX subscriptions_state_idx ON subscriptions (state, plan_id);
-- "Overdue" reads this: an active subscription whose expiry has passed.
CREATE INDEX subscriptions_expires_idx ON subscriptions (expires_at) WHERE expires_at IS NOT NULL;

-- ---------------------------------------------------------------- payments --

-- Payments are immutable facts, so this table is insert-only and needs no
-- watermark: a redelivery collides on the primary key and is discarded.
CREATE TABLE payments (
    id              text PRIMARY KEY,
    company_id      uuid NOT NULL,
    subscription_id text NOT NULL DEFAULT '',
    amount_minor    bigint NOT NULL DEFAULT 0,
    currency        char(3) NOT NULL DEFAULT 'USD',
    status          text NOT NULL,
    -- Present on a failure. Carried from the payments service verbatim and
    -- shown only to platform staff; it is never surfaced to a tenant from here.
    failure_reason  text NOT NULL DEFAULT '',
    paid_at         timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX payments_company_idx ON payments (company_id, paid_at DESC, id DESC);

-- --------------------------------------------------------- support tickets --

CREATE TABLE support_tickets (
    id              text PRIMARY KEY,
    company_id      uuid NOT NULL,
    subject         text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'open',
    priority        text NOT NULL DEFAULT 'normal',
    opened_at       timestamptz NOT NULL DEFAULT now(),
    last_reply_at   timestamptz,
    closed_at       timestamptz,
    occurred_at     timestamptz NOT NULL DEFAULT '-infinity',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX support_tickets_company_idx ON support_tickets (company_id, opened_at DESC, id DESC);
-- The dashboard counts open tickets platform-wide; the partial index keeps that
-- count proportional to the backlog rather than to the archive.
CREATE INDEX support_tickets_open_idx ON support_tickets (opened_at DESC)
    WHERE status <> 'closed';

-- --------------------------------------------------------- published jobs --

-- A row per published requisition rather than a counter, because a job can be
-- published, unpublished and published again: counting the events would drift
-- upward, counting the rows cannot.
CREATE TABLE published_jobs (
    id              text PRIMARY KEY,
    company_id      uuid NOT NULL,
    title           text NOT NULL DEFAULT '',
    published_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX published_jobs_company_idx ON published_jobs (company_id, published_at DESC, id DESC);

-- ---------------------------------------------------------------- counters --

-- Applications are the one high-cardinality fact the console needs, and it only
-- ever needs the count. Projecting a row per application would copy the
-- applications service's largest table into a dashboard's database for no
-- benefit, so this is a counter — safe to increment because `application.
-- submitted` is emitted once per application and the activity gate discards a
-- redelivery before the increment runs.
CREATE TABLE company_counters (
    company_id          uuid PRIMARY KEY,
    application_count   bigint NOT NULL DEFAULT 0,
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT company_counters_not_negative CHECK (application_count >= 0)
);

-- Facts that belong to the platform rather than to a tenant. Candidates are the
-- first: they register against the jobs portal and have no company at all.
CREATE TABLE platform_counters (
    metric      text PRIMARY KEY,
    value       bigint NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT platform_counters_not_negative CHECK (value >= 0)
);

INSERT INTO platform_counters (metric, value) VALUES ('candidates_total', 0);
