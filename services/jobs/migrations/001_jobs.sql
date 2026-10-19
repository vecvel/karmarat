-- Jobs service schema.
--
-- The jobs service owns *what a company is hiring for and where that opening is
-- visible*. It holds no accounts, no applications and no company profile; the
-- only foreign identifier it stores is company_id, minted by the companies
-- service, and it carries no foreign key because the two live in separate
-- databases.

CREATE TYPE job_status AS ENUM ('draft', 'open', 'on_hold', 'closed', 'archived');
CREATE TYPE employment_type AS ENUM (
    'full_time', 'part_time', 'contract', 'temporary', 'internship', 'volunteer');
CREATE TYPE seniority_level AS ENUM (
    'intern', 'entry', 'junior', 'mid', 'senior', 'lead', 'principal', 'director', 'executive');
CREATE TYPE work_mode AS ENUM ('onsite', 'hybrid', 'remote');

CREATE TABLE jobs (
    id                  text PRIMARY KEY,
    company_id          uuid NOT NULL,
    -- The public URL segment. Stable once published: a link in a candidate's
    -- inbox has to keep working after the title is edited.
    slug                text NOT NULL,

    title               text NOT NULL,
    department          text NOT NULL DEFAULT '',
    locations           text[] NOT NULL DEFAULT '{}',
    work_mode           work_mode NOT NULL DEFAULT 'onsite',
    employment_type     employment_type NOT NULL,
    seniority           seniority_level NOT NULL,

    description         text NOT NULL DEFAULT '',
    requirements        text NOT NULL DEFAULT '',

    -- Money is minor units in a named currency. A salary band that crosses a
    -- float would eventually print an offer one cent short of what was agreed.
    salary_min          bigint,
    salary_max          bigint,
    salary_currency     char(3),
    salary_is_public    boolean NOT NULL DEFAULT false,

    headcount           integer NOT NULL DEFAULT 1,

    -- Identity account ids. Internal: never serialized to a public surface.
    hiring_manager_id   text NOT NULL DEFAULT '',
    recruiter_id        text NOT NULL DEFAULT '',
    internal_notes      text NOT NULL DEFAULT '',

    status              job_status NOT NULL DEFAULT 'draft',

    -- The two publication surfaces are independent. A job may be on the
    -- company's own careers portal, on the shared Karmarat board, on both, or
    -- on neither; each is a separate permission to switch on.
    visible_on_portal   boolean NOT NULL DEFAULT false,
    visible_on_network  boolean NOT NULL DEFAULT false,

    -- The custom application form, validated in Go before it is written here.
    -- Postgres would accept any JSON; a candidate discovering a malformed field
    -- at submission time is the failure this guards against.
    form                jsonb NOT NULL DEFAULT '{"fields":[],"version":1}'::jsonb,

    opened_at           timestamptz,
    closed_at           timestamptz,
    created_by          text NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT jobs_headcount_positive CHECK (headcount BETWEEN 1 AND 10000),
    CONSTRAINT jobs_salary_ordered CHECK (
        salary_min IS NULL OR salary_max IS NULL OR salary_min <= salary_max),
    CONSTRAINT jobs_salary_non_negative CHECK (
        (salary_min IS NULL OR salary_min >= 0) AND (salary_max IS NULL OR salary_max >= 0)),
    -- A declared band without a currency is an amount of nothing.
    CONSTRAINT jobs_salary_currency CHECK (
        (salary_min IS NULL AND salary_max IS NULL) OR salary_currency IS NOT NULL)
);

-- The tenant's own link namespace. Two companies may both hire a
-- "senior-engineer"; neither may hold the slug twice.
CREATE UNIQUE INDEX jobs_company_slug_idx ON jobs (company_id, slug);

-- The shared board resolves a job by slug alone, so among network-visible jobs
-- the slug must be globally unique. Enforcing it here rather than in Go means a
-- race between two simultaneous publishes still ends with one 409 rather than
-- two jobs answering the same public URL.
CREATE UNIQUE INDEX jobs_network_slug_idx ON jobs (slug) WHERE visible_on_network;

-- Every tenant-scoped read leads with company_id, so every index does too.
CREATE INDEX jobs_company_status_idx ON jobs (company_id, status, id DESC);
CREATE INDEX jobs_company_department_idx ON jobs (company_id, lower(department))
    WHERE department <> '';

-- The public board's only query: open and on the network, newest first.
CREATE INDEX jobs_network_board_idx ON jobs (id DESC)
    WHERE visible_on_network AND status = 'open';
CREATE INDEX jobs_portal_board_idx ON jobs (company_id, id DESC)
    WHERE visible_on_portal AND status = 'open';

-- Free-text search over the fields a candidate actually scans. A GIN index over
-- a computed tsvector keeps `q=` from degrading into a sequential scan once a
-- tenant has thousands of requisitions.
CREATE INDEX jobs_search_idx ON jobs USING gin (
    to_tsvector('english', title || ' ' || department || ' ' || description));

CREATE INDEX jobs_locations_idx ON jobs USING gin (locations);

COMMENT ON COLUMN jobs.form IS
    'Ordered application form: {"version":N,"fields":[{key,label,type,required,...}]}';
