-- Interviews service schema.
--
-- This service owns the interview loop: which rounds are booked against an
-- application, who sits on the panel, and what each interviewer concluded. The
-- application, the job and the candidate belong to other services; what is kept
-- here is the round itself plus the two snapshot fields a schedule view needs,
-- so rendering a week of interviews does not fan out one call per row.

-- ---------------------------------------------------------------------------
-- Interviews
-- ---------------------------------------------------------------------------
-- `format` is an enum rather than free text because the meeting link is only
-- meaningful for one of the values, and a typo'd "vidoe" would silently produce
-- a round nobody can join.
CREATE TYPE interview_format AS ENUM ('onsite', 'video', 'phone');

-- `no_show` is a distinct status rather than a flavour of cancelled: cancelling
-- is the company's decision and reflects on nobody, while a no-show is a fact
-- about the candidate that reporting has to be able to count separately.
CREATE TYPE interview_status AS ENUM ('scheduled', 'completed', 'cancelled', 'no_show');

CREATE TABLE interviews (
    id                 text PRIMARY KEY,
    company_id         uuid NOT NULL,

    -- Owned by the applications and candidates services. No foreign key exists
    -- or could exist: these ids point across a service boundary.
    application_id     text NOT NULL,
    candidate_id       text NOT NULL,

    -- Denormalized from application events. A schedule lists the candidate and
    -- the role they are interviewing for on every row; resolving those live
    -- would make one screen depend on two other services being up.
    candidate_name     text NOT NULL DEFAULT '',
    job_title          text NOT NULL DEFAULT '',

    -- The high-water mark of the application event that last refreshed the two
    -- fields above. Events can arrive out of order, and without this an older
    -- redelivery would overwrite a newer name with a stale one.
    projection_at      timestamptz,

    round_title        text NOT NULL,
    -- Free text on purpose: "take-home debrief" and "bar raiser" are real rounds
    -- that no fixed vocabulary anticipates, and nothing keys off this value.
    round_type         text NOT NULL DEFAULT '',
    scheduled_start    timestamptz NOT NULL,
    duration_minutes   integer NOT NULL DEFAULT 60,
    format             interview_format NOT NULL DEFAULT 'video',
    meeting_link       text,
    notes              text,

    status             interview_status NOT NULL DEFAULT 'scheduled',
    outcome_note       text,
    cancellation_reason text,
    completed_at       timestamptz,
    cancelled_at       timestamptz,

    created_by         text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX interviews_company_recent_idx ON interviews (company_id, id DESC);
CREATE INDEX interviews_company_schedule_idx ON interviews (company_id, scheduled_start DESC, id DESC);
CREATE INDEX interviews_company_application_idx ON interviews (company_id, application_id, id DESC);
CREATE INDEX interviews_company_candidate_idx ON interviews (company_id, candidate_id, id DESC);
-- The consumer's two writes both start from an application id that arrived on an
-- event, so they are keyed the same way rather than by tenant.
CREATE INDEX interviews_application_idx ON interviews (application_id);
-- Cancelling the rounds behind a rejected application only ever touches rows
-- that are still scheduled, so the partial index is the whole working set.
CREATE INDEX interviews_open_idx ON interviews (application_id) WHERE status = 'scheduled';

-- ---------------------------------------------------------------------------
-- Panel
-- ---------------------------------------------------------------------------
-- Membership is what grants the right to file a scorecard for a round, so it is
-- a row rather than a jsonb array on the interview: an authorization decision
-- read out of a document is one migration away from being unqueryable.
--
-- account_id names an account in the identity service; company_id is carried so
-- every read stays tenant-filtered without joining back to the interview.
CREATE TABLE interview_panel (
    interview_id  text NOT NULL REFERENCES interviews (id) ON DELETE CASCADE,
    company_id    uuid NOT NULL,
    account_id    text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (interview_id, account_id)
);

CREATE INDEX interview_panel_account_idx ON interview_panel (company_id, account_id);

-- ---------------------------------------------------------------------------
-- Scorecards
-- ---------------------------------------------------------------------------
CREATE TYPE interview_recommendation AS ENUM ('strong_hire', 'hire', 'no_hire', 'strong_no_hire');

CREATE TABLE interview_scorecards (
    id                   text PRIMARY KEY,
    company_id           uuid NOT NULL,
    interview_id         text NOT NULL REFERENCES interviews (id) ON DELETE CASCADE,
    -- The account that filed it. Reading a scorecard back is decided against
    -- this column: an interviewer holding only `interviews.submit_scorecard`
    -- sees the rows where it matches them and nothing else.
    author_id            text NOT NULL,

    overall_rating       smallint NOT NULL,
    recommendation       interview_recommendation NOT NULL,
    technical_score      smallint,
    communication_score  smallint,
    culture_score        smallint,

    strengths            text,
    concerns             text,
    feedback_notes       text,

    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),

    -- Ranges are pinned in the schema as well as in the handler, because a
    -- report that averages these columns is arithmetic over whatever got in.
    CONSTRAINT interview_scorecards_overall_range CHECK (overall_rating BETWEEN 1 AND 5),
    CONSTRAINT interview_scorecards_technical_range CHECK (technical_score IS NULL OR technical_score BETWEEN 1 AND 5),
    CONSTRAINT interview_scorecards_communication_range CHECK (communication_score IS NULL OR communication_score BETWEEN 1 AND 5),
    CONSTRAINT interview_scorecards_culture_range CHECK (culture_score IS NULL OR culture_score BETWEEN 1 AND 5)
);

-- One scorecard per interviewer per round. Two browser tabs submitting at once
-- both pass a SELECT-then-INSERT check, so the rule is the index's to keep.
CREATE UNIQUE INDEX interview_scorecards_author_idx ON interview_scorecards (interview_id, author_id);
CREATE INDEX interview_scorecards_company_idx ON interview_scorecards (company_id, interview_id, id);

-- ---------------------------------------------------------------------------
-- Event outbox
-- ---------------------------------------------------------------------------
-- Domain events are written in the same transaction as the change they describe
-- and relayed from here by a background worker. Publishing inline would mean a
-- broker blip after the commit loses the fact permanently: the round would be
-- booked and no calendar invite, reminder or report would ever hear about it.
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

CREATE TRIGGER interviews_touch BEFORE UPDATE ON interviews
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER interview_scorecards_touch BEFORE UPDATE ON interview_scorecards
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
