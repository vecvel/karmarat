-- Applications service schema.
--
-- This service owns the recruitment pipeline: who applied to what, where they
-- are in the process, and every move they made along the way. Jobs, candidate
-- profiles and company profiles belong to other services; what is kept here is
-- the application itself plus the few snapshot fields a pipeline view needs so
-- that listing a thousand applications does not fan out into a thousand calls.

-- ---------------------------------------------------------------------------
-- Pipeline stages
-- ---------------------------------------------------------------------------
-- Stages are per-company data rather than an enum, because every company runs a
-- different process: an agency screens then submits, a startup interviews then
-- offers. A schema change is the wrong unit of work for "we added a take-home
-- round", so the shape of the pipeline is a table a company edits.
--
-- `type` is the fixed vocabulary behind the editable label: reporting, the
-- candidate-facing status and the hired/rejected transitions key off the type,
-- so renaming "Screening" to "Recruiter Call" changes the word and nothing else.
CREATE TYPE stage_type AS ENUM ('applied', 'screening', 'interview', 'offer', 'hired', 'rejected');

CREATE TABLE pipeline_stages (
    id           text PRIMARY KEY,
    company_id   uuid NOT NULL,
    key          text NOT NULL,
    name         text NOT NULL,
    sort_order   integer NOT NULL DEFAULT 0,
    type         stage_type NOT NULL,
    -- A terminal stage ends the process; an application in one is not "in flight".
    is_terminal  boolean NOT NULL DEFAULT false,
    color        text NOT NULL DEFAULT '#64748b',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX pipeline_stages_company_key_idx ON pipeline_stages (company_id, lower(key));
CREATE INDEX pipeline_stages_company_order_idx ON pipeline_stages (company_id, sort_order, id);

-- ---------------------------------------------------------------------------
-- Rejection reasons
-- ---------------------------------------------------------------------------
-- Also company-managed. A free-text rejection reason cannot be reported on and
-- cannot be shown to a candidate safely; a curated list can be both.
CREATE TABLE rejection_reasons (
    id           text PRIMARY KEY,
    company_id   uuid NOT NULL,
    label        text NOT NULL,
    sort_order   integer NOT NULL DEFAULT 0,
    -- Retiring a reason keeps the applications that already cite it readable.
    is_active    boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX rejection_reasons_company_label_idx ON rejection_reasons (company_id, lower(label));
CREATE INDEX rejection_reasons_company_order_idx ON rejection_reasons (company_id, sort_order, id);

-- ---------------------------------------------------------------------------
-- Applications
-- ---------------------------------------------------------------------------
CREATE TYPE application_status AS ENUM ('active', 'rejected', 'withdrawn', 'hired');

CREATE TABLE applications (
    id                   text PRIMARY KEY,
    company_id           uuid NOT NULL,
    job_id               text NOT NULL,
    candidate_id         text NOT NULL,

    -- Snapshots taken at submission. They belong to the jobs and candidates
    -- services, but an applicant's name as it was when they applied is part of
    -- the application record, and copying four fields is what lets the pipeline
    -- list, search and export without a cross-service call per row.
    candidate_name       text NOT NULL DEFAULT '',
    candidate_email      text NOT NULL DEFAULT '',
    job_title            text NOT NULL DEFAULT '',
    company_name         text NOT NULL DEFAULT '',

    -- The submitted answers to the job's custom form, validated server-side
    -- against that form before they were stored.
    answers              jsonb NOT NULL DEFAULT '{}'::jsonb,
    resume_key           text,
    source               text NOT NULL DEFAULT 'portal',

    stage_id             text NOT NULL REFERENCES pipeline_stages (id) ON DELETE RESTRICT,
    status               application_status NOT NULL DEFAULT 'active',
    rating               smallint,

    rejection_reason_id  text REFERENCES rejection_reasons (id) ON DELETE RESTRICT,
    -- The internal note is never shown to the candidate; the reason label is.
    rejection_note       text,
    rejected_by          text,
    rejected_at          timestamptz,
    withdrawn_at         timestamptz,
    -- Set when the requisition closes, so a candidate sees why nothing moved.
    job_closed_at        timestamptz,

    submitted_at         timestamptz NOT NULL DEFAULT now(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- The product's central rule: one candidate, one job, one application, ever.
--
-- It is a database constraint rather than a check in the handler because two
-- concurrent submits both pass a SELECT-then-INSERT check and both insert. A
-- withdrawn application still occupies the slot, which is the intended reading:
-- withdrawing is not a way to start over.
CREATE UNIQUE INDEX applications_job_candidate_idx ON applications (job_id, candidate_id);

CREATE INDEX applications_company_recent_idx ON applications (company_id, id DESC);
CREATE INDEX applications_company_stage_idx ON applications (company_id, stage_id, id DESC);
CREATE INDEX applications_company_job_idx ON applications (company_id, job_id, id DESC);
CREATE INDEX applications_candidate_idx ON applications (candidate_id, id DESC);
CREATE INDEX applications_job_idx ON applications (job_id) WHERE job_closed_at IS NULL;
CREATE INDEX applications_stage_idx ON applications (stage_id);

-- ---------------------------------------------------------------------------
-- Pipeline history
-- ---------------------------------------------------------------------------
-- Append-only. "Who moved this candidate to Offer, and when?" is an audit
-- question, and an audit trail that can be edited answers nothing — so rows are
-- inserted and never updated. There is deliberately no updated_at and no touch
-- trigger on this table; the absence is the invariant.
CREATE TYPE application_event_type AS ENUM (
    'submitted', 'stage_changed', 'rejected', 'withdrawn', 'hired', 'job_closed', 'updated'
);

CREATE TABLE application_events (
    id              text PRIMARY KEY,
    company_id      uuid NOT NULL,
    -- Deleting an application deletes its history with it: a company that erases
    -- an applicant on request must not leave a trail naming them.
    application_id  text NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    type            application_event_type NOT NULL,
    actor_id        text,
    actor_type      text NOT NULL DEFAULT 'system',
    from_stage_id   text,
    to_stage_id     text,
    reason_id       text,
    note            text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX application_events_application_idx ON application_events (application_id, created_at, id);
CREATE INDEX application_events_company_idx ON application_events (company_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Event outbox
-- ---------------------------------------------------------------------------
-- Domain events are written in the same transaction as the change they describe
-- and published from here by a background worker. Publishing inline would mean
-- that a broker blip after the commit loses the fact permanently — the pipeline
-- would move and no notification, report or projection would ever hear about it.
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

CREATE TRIGGER pipeline_stages_touch BEFORE UPDATE ON pipeline_stages
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER rejection_reasons_touch BEFORE UPDATE ON rejection_reasons
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER applications_touch BEFORE UPDATE ON applications
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
