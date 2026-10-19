-- Messaging service schema.
--
-- A conversation has two sides that are scoped by different things, and the
-- schema is shaped so neither filter can be forgotten:
--
--   * the company side is tenant-scoped — every statement a recruiter can reach
--     carries "AND company_id = $n"
--   * the candidate side is account-scoped — a candidate is not a tenant, and
--     their own account id is the only boundary that means anything on the jobs
--     portal, so those statements carry "AND candidate_account_id = $n"
--
-- Both predicates exist on `messages` as well as on `conversations`, denormalised
-- on purpose. A message query that joined up to the conversation to find its
-- tenant would be one missing JOIN condition away from a cross-tenant read; a
-- column on the row itself cannot be left out of a WHERE clause by accident.

CREATE TYPE message_sender_type AS ENUM ('company', 'candidate');

-- How a conversation came to exist. It drives the daily open limit (only the
-- company-initiated kinds are counted) and tells the UI why a thread appeared.
CREATE TYPE conversation_origin AS ENUM ('recruiter', 'approach', 'application');

-- ---------------------------------------------------------------------------
-- Conversations
-- ---------------------------------------------------------------------------
CREATE TABLE conversations (
    id                      text PRIMARY KEY,
    company_id              uuid NOT NULL,
    -- The candidate's *identity account* id, not a profile id: the gateway's
    -- principal carries the account, so scoping the candidate side by the same
    -- value means the filter needs no lookup to be trustworthy.
    candidate_account_id    text NOT NULL,

    -- Optional context. No foreign key: applications and jobs are other
    -- services' data, and a conversation must survive them.
    application_id          text,
    job_id                  text,

    subject                 text NOT NULL DEFAULT '',
    origin                  conversation_origin NOT NULL DEFAULT 'recruiter',
    -- The id of the fact that opened this thread automatically (an approach).
    -- Unique per tenant below, which is what makes the consumer idempotent
    -- under JetStream redelivery without a separate "seen events" table.
    origin_ref              text,
    opened_by_account_id    text NOT NULL DEFAULT '',

    -- One sort key for both sides' lists, never null, so keyset pagination has
    -- no COALESCE in its ORDER BY and can use the indexes below directly.
    last_activity_at        timestamptz NOT NULL DEFAULT now(),
    last_message_preview    text NOT NULL DEFAULT '',
    last_message_sender     message_sender_type,

    -- Unread counters are maintained per side rather than computed, because the
    -- inbox badge is read on every page load and counting unread rows across a
    -- recruiter's whole list is the query that gets slow first.
    company_unread_count    integer NOT NULL DEFAULT 0,
    candidate_unread_count  integer NOT NULL DEFAULT 0,

    closed_at               timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now()
);

-- The company inbox: tenant first, then the sort key, so a recruiter's list is
-- one index range scan.
CREATE INDEX conversations_tenant_idx
    ON conversations (company_id, last_activity_at DESC, id DESC);

-- The candidate inbox. A candidate spans every company they have talked to, so
-- this index deliberately does not lead with company_id.
CREATE INDEX conversations_candidate_idx
    ON conversations (candidate_account_id, last_activity_at DESC, id DESC);

-- One thread per company, candidate and application context. Written as a
-- constraint rather than a check in the handler because two recruiters clicking
-- "Message" at the same moment is a race the database is the only place to win.
CREATE UNIQUE INDEX conversations_subject_idx
    ON conversations (company_id, candidate_account_id, coalesce(application_id, ''));

CREATE UNIQUE INDEX conversations_origin_ref_idx
    ON conversations (company_id, origin_ref) WHERE origin_ref IS NOT NULL;

-- Supports the daily open limit's database backstop.
CREATE INDEX conversations_opened_idx ON conversations (company_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Company-side participants
-- ---------------------------------------------------------------------------
-- This table is what makes `messaging.read` and `messaging.read_all` different
-- in SQL rather than in prose: the narrow permission joins through it, the wide
-- one does not. Only recruiters are listed — the candidate side of a thread is
-- the conversation's own candidate_account_id, and modelling it here would make
-- "is this row a recruiter?" a predicate someone could forget.
CREATE TABLE conversation_participants (
    conversation_id text NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    -- Carried so the participant filter itself is tenant-scoped; a join that
    -- only matched on account_id would trust the conversation row's tenancy.
    company_id      uuid NOT NULL,
    account_id      text NOT NULL,
    joined_at       timestamptz NOT NULL DEFAULT now(),
    last_read_at    timestamptz,
    PRIMARY KEY (conversation_id, account_id)
);

CREATE INDEX conversation_participants_recruiter_idx
    ON conversation_participants (company_id, account_id, conversation_id);

-- ---------------------------------------------------------------------------
-- Messages
-- ---------------------------------------------------------------------------
CREATE TABLE messages (
    id                   text PRIMARY KEY,
    conversation_id      text NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    -- Both owners, denormalised: see the note at the top of this file.
    company_id           uuid NOT NULL,
    candidate_account_id text NOT NULL,

    sender_type          message_sender_type NOT NULL,
    sender_account_id    text NOT NULL,

    body                 text NOT NULL,
    -- Object-storage references only. The bytes never touch this database; each
    -- entry is {objectKey, filename, contentType, sizeBytes} and the key is
    -- prefixed with its owner, which the API checks before it is stored.
    attachments          jsonb NOT NULL DEFAULT '[]'::jsonb,

    -- Idempotency-Key from the sender. A retried send after a timeout must not
    -- post the message twice; the unique index below is the enforcement.
    idempotency_key      text,

    sent_at              timestamptz NOT NULL DEFAULT now(),
    -- When the *other* side read it. Null means unread.
    read_at              timestamptz
);

-- The thread view, newest first.
CREATE INDEX messages_thread_idx ON messages (conversation_id, sent_at DESC, id DESC);

-- Scoped by sender so one participant's key cannot collide with another's.
CREATE UNIQUE INDEX messages_idempotency_idx
    ON messages (conversation_id, sender_account_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Marking a thread read touches only the unread rows from the other side.
CREATE INDEX messages_unread_idx
    ON messages (conversation_id, sender_type) WHERE read_at IS NULL;

-- ---------------------------------------------------------------------------
-- Projections of other services' facts
-- ---------------------------------------------------------------------------
-- Messaging does not own either of the facts below and never joins to the
-- service that does. They are maintained from the event bus, which is what lets
-- the "may this company open a thread?" decision be answered from local state
-- when a candidate has already applied — the common case — without a network
-- hop on the request path.

-- Who a candidate is willing to hear from. The discoverable flag is confirmed
-- against the candidates service on the request path as well; the block list is
-- only available here, because the candidates service's internal profile
-- endpoint does not expose it.
CREATE TABLE candidate_visibility (
    account_id          text PRIMARY KEY,
    discoverable        boolean NOT NULL DEFAULT false,
    hide_from_companies uuid[] NOT NULL DEFAULT '{}',
    deleted             boolean NOT NULL DEFAULT false,
    -- The candidates service stamps a monotonic version on every profile write,
    -- so an event delivered out of order can be discarded instead of resurrecting
    -- a setting the candidate has already changed.
    version             bigint NOT NULL DEFAULT 0,
    updated_at          timestamptz NOT NULL DEFAULT now()
);

-- Candidates who applied to one of a company's jobs. An application is standing
-- permission to be contacted about it, so this table is the first branch of the
-- eligibility check.
CREATE TABLE candidate_company_links (
    company_id           uuid NOT NULL,
    candidate_account_id text NOT NULL,
    application_id       text NOT NULL,
    job_id               text NOT NULL DEFAULT '',
    job_title            text NOT NULL DEFAULT '',
    linked_at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, application_id)
);

CREATE INDEX candidate_company_links_lookup_idx
    ON candidate_company_links (company_id, candidate_account_id);

-- ---------------------------------------------------------------------------
-- Event outbox
-- ---------------------------------------------------------------------------
-- The message row and the event announcing it commit together. Publishing
-- inline would mean a broker blip after the commit leaves a candidate with a
-- message in their inbox and no notification that it arrived — the one failure
-- this domain cannot tolerate, because nobody comes back to check.
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

CREATE TRIGGER conversations_touch BEFORE UPDATE ON conversations
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
