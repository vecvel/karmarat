-- Offers service schema.
--
-- This service owns the compensation package a company puts in front of a
-- candidate: what was offered, who signed it off, when it went out, and what
-- came back. The application and the candidate live in other services; what is
-- kept here is the offer itself plus the two snapshot fields an offers list
-- needs, so rendering a hundred offers does not fan out into a hundred calls.
--
-- Two decisions are encoded in the columns and are worth stating once:
--
--   * Money is bigint minor units plus a char(3) currency. A float cannot
--     represent 1234.05 exactly, and a salary that drifts by a cent between the
--     letter and the payroll export is a dispute, not a rounding artefact.
--   * The approval trail (submitted_by, approved_by, sent_by) is stored rather
--     than derived, because "who approved this package?" has to stay answerable
--     after the person leaves the company and their membership is gone.

-- ---------------------------------------------------------------------------
-- Offers
-- ---------------------------------------------------------------------------
-- The lifecycle is an enum rather than free text: every transition in this
-- service is checked against it, and a status Postgres would not accept is one
-- no bug in this process can write.
CREATE TYPE offer_status AS ENUM (
    'draft', 'pending_approval', 'approved', 'sent', 'accepted', 'declined', 'expired'
);

CREATE TABLE offers (
    id                    text PRIMARY KEY,
    company_id            uuid NOT NULL,

    -- Owned by the applications and candidates services. Deliberately not
    -- foreign keys: there is one database per service, and a constraint across
    -- that boundary would make this service refuse writes whenever another
    -- service's data moved.
    application_id        text NOT NULL,
    candidate_id          text NOT NULL,

    -- Snapshots kept current by the application.* consumer. They exist so a
    -- list renders without a cross-service call per row, and they are refreshed
    -- rather than frozen because an offer letter naming a stale job title is
    -- wrong in a way a candidate will notice.
    candidate_name        text NOT NULL DEFAULT '',
    job_title             text NOT NULL DEFAULT '',

    status                offer_status NOT NULL DEFAULT 'draft',

    -- Terms
    designation           text NOT NULL,
    department_name       text NOT NULL,
    grade_level           text NOT NULL DEFAULT '',

    -- Compensation. Minor units, always: 175000.00 USD is stored as 17500000.
    base_salary           bigint NOT NULL,
    sign_on_bonus         bigint NOT NULL DEFAULT 0,
    currency              char(3) NOT NULL,
    pay_frequency         text NOT NULL DEFAULT 'annual',
    -- Free text on purpose: "15% of base, paid in March" is a policy, not an
    -- amount, and storing it as a number would force a false precision.
    annual_bonus          text NOT NULL DEFAULT '',
    equity_shares         text NOT NULL DEFAULT '',

    joining_date          date NOT NULL,
    reporting_manager     text NOT NULL DEFAULT '',
    work_location         text NOT NULL DEFAULT '',
    probation_period      text NOT NULL DEFAULT '',
    notice_period         text NOT NULL DEFAULT '',
    benefits_summary      text NOT NULL DEFAULT '',
    template_type         text NOT NULL DEFAULT '',
    -- An array of {key, value} objects: the clauses a company adds that the
    -- platform has no column for. A jsonb column keeps them queryable without
    -- turning every bespoke clause into a migration.
    custom_fields         jsonb NOT NULL DEFAULT '[]'::jsonb,
    offer_letter_content  text NOT NULL DEFAULT '',
    expires_at            timestamptz,

    -- Approval trail. Separation of duties is enforced in the service by
    -- comparing submitted_by with the approver; self_approved records the
    -- occasions the actor deliberately overrode it, so the exception is
    -- auditable rather than invisible.
    created_by            text NOT NULL DEFAULT '',
    submitted_by          text,
    submitted_at          timestamptz,
    approved_by           text,
    approved_at           timestamptz,
    self_approved         boolean NOT NULL DEFAULT false,

    sent_by               text,
    sent_at               timestamptz,
    -- The client's Idempotency-Key for the send. A retried dispatch must not put
    -- a second letter in front of a candidate, and the network is where a
    -- response goes missing, so the key is what makes the retry safe.
    send_idempotency_key  text,

    responded_at          timestamptz,
    decline_reason        text NOT NULL DEFAULT '',

    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

-- Every index leads with company_id: a tenant's list is the only access path
-- that exists, so an index that did not lead with it would be unused by design.
CREATE INDEX offers_company_recent_idx ON offers (company_id, id DESC);
CREATE INDEX offers_company_status_idx ON offers (company_id, status, id DESC);
CREATE INDEX offers_company_application_idx ON offers (company_id, application_id, id DESC);
CREATE INDEX offers_company_candidate_idx ON offers (company_id, candidate_id, id DESC);

-- Fed by the consumer, which resolves an application id from another service's
-- event and has no offer id to go on.
CREATE INDEX offers_application_idx ON offers (application_id);

-- Drives the expiry sweeper without scanning settled offers.
CREATE INDEX offers_expiring_idx ON offers (expires_at)
    WHERE expires_at IS NOT NULL AND status IN ('draft', 'pending_approval', 'approved', 'sent');

-- Scoped to the tenant, because two companies choosing the same key is a
-- coincidence, not a replay. Partial, so the many offers that were never sent
-- do not collide on a shared NULL.
CREATE UNIQUE INDEX offers_send_idempotency_idx ON offers (company_id, send_idempotency_key)
    WHERE send_idempotency_key IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Event outbox
-- ---------------------------------------------------------------------------
-- Domain events are written in the same transaction as the change they describe
-- and published from here by a background worker. Publishing inline would mean
-- a broker blip after the commit loses the fact permanently: the candidate would
-- hold a signed offer that no notification, report or projection ever heard of.
CREATE TABLE IF NOT EXISTS event_outbox (
    id            text PRIMARY KEY,
    subject       text NOT NULL,
    company_id    uuid,
    actor_id      text,
    payload       jsonb NOT NULL,
    attempts      integer NOT NULL DEFAULT 0,
    last_error    text,
    published_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS event_outbox_pending_idx ON event_outbox (created_at, id) WHERE published_at IS NULL;

-- ---------------------------------------------------------------------------
-- updated_at maintenance
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER offers_touch BEFORE UPDATE ON offers
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
