#!/usr/bin/env bash
#
# End-to-end product flow against a running stack (scripts/dev.sh up).
#
# scripts/smoke.sh proves the security boundaries. This one proves the product
# actually works: a company posts a job, a candidate finds it and applies, the
# pipeline moves, and the rules that matter — apply once, see only your own
# data, never see another tenant's — hold under real requests.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GW="${GATEWAY_URL:-http://localhost:8080}"
[[ -f .env ]] && { set -a; . ./.env; set +a; }
HOST_BASE="${PLATFORM_HOSTNAME:-karmarat.local}"
INTERNAL_TOKEN="${INTERNAL_SERVICE_TOKEN:-}"
ID_URL="${IDENTITY_URL:-http://localhost:8081}"

c_red=$'\033[31m'; c_grn=$'\033[32m'; c_dim=$'\033[2m'; c_off=$'\033[0m'
pass=0; fail=0
RESP=$(mktemp); trap 'rm -f "$RESP"' EXIT

check() {
  if [[ "$2" == "$3" ]]; then
    printf '  %s✓%s %-52s %s\n' "$c_grn" "$c_off" "$1" "$3"; pass=$((pass + 1))
  else
    printf '  %s✗%s %-52s expected %s, got %s\n' "$c_red" "$c_off" "$1" "$2" "$3"
    [[ -s "$RESP" ]] && printf '      %s%s%s\n' "$c_dim" "$(head -c 300 "$RESP")" "$c_off"
    fail=$((fail + 1))
  fi
}
section() { printf '\n%s%s%s\n' "$c_dim" "$1" "$c_off"; }

# call <method> <path> <host> <token> [body] -> prints status; body in $RESP
call() {
  local args=(-s -o "$RESP" -w '%{http_code}' -X "$1" "$GW$2" -H "Host: $3")
  [[ -n "${4:-}" ]] && args+=(-H "Authorization: Bearer $4")
  [[ -n "${5:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$5")
  curl "${args[@]}"
}

jq_() { python3 -c "
import sys, json
try: d = json.load(open('$RESP'))
except Exception: print(''); raise SystemExit
for k in '$1'.split('.'):
    if isinstance(d, dict): d = d.get(k, '')
    elif isinstance(d, list) and k.isdigit() and int(k) < len(d): d = d[int(k)]
    else: d = ''
print(d if not isinstance(d, (list, dict)) else (len(d) if isinstance(d, list) else json.dumps(d)))"; }

# Whether THIS run's candidate profile appears in the search results currently
# in $RESP. Counting rows would make the assertion describe every previous run's
# leftovers instead of this one's candidate.
seen_self() {
  python3 -c "
import json
try: rows = json.load(open('$RESP')).get('data', [])
except Exception: rows = []
print('visible' if any(r.get('id') == '$CAND_PROFILE_ID' for r in rows) else 'hidden')"
}

login() {
  local status
  status=$(call POST /api/v1/auth/login "$1" '' "$2")
  if [[ "$status" == "429" ]]; then
    printf '  %s…%s login rate limit hit — waiting 65s\n' "$c_dim" "$c_off" >&2
    sleep 65
    status=$(call POST /api/v1/auth/login "$1" '' "$2")
  fi
  jq_ tokens.accessToken
}

printf 'Karmarat product flow — %s\n' "$GW"

[[ -z "$INTERNAL_TOKEN" ]] && { echo "INTERNAL_SERVICE_TOKEN unset"; exit 1; }

# ------------------------------------------------------------------ tenants --
section 'setup'
RUN=$(date +%s)
SUFFIX=$(printf '%012d' "$((RUN % 1000000000000))")
ACME_ID="00000000-0000-4000-8000-$SUFFIX"
RIVAL_ID="00000000-0000-4000-8001-$SUFFIX"
ACME_SLUG="flow-acme-$RUN"
RIVAL_SLUG="flow-rival-$RUN"
provision() {
  curl -s -o /dev/null -X POST "$ID_URL/internal/companies" \
    -H "X-Internal-Token: $INTERNAL_TOKEN" -H 'Content-Type: application/json' \
    -d "{\"companyId\":\"$1\",\"slug\":\"$2\",\"name\":\"$3\",\"ownerEmail\":\"$4\",\"ownerName\":\"$5\",\"ownerPassword\":\"$6\",\"state\":\"active\"}"
}
# A portal stays closed until billing says otherwise — that gate is exactly what
# scripts/smoke.sh asserts. Here it is satisfied rather than tested, so the
# product flow is not drowned in 402s.
activate() {
  curl -s -o /dev/null -X PATCH "$ID_URL/internal/companies/$1/subscription" \
    -H "X-Internal-Token: $INTERNAL_TOKEN" -H 'Content-Type: application/json' \
    -d '{"state":"active","entitlements":{"maxJobs":100,"canUseTalentSearch":true}}'
}

provision "$ACME_ID"  "$ACME_SLUG"  'Flow Acme'  "owner@$ACME_SLUG.test"  'Acme Owner'  'Fl0w-Acme-Pass!'
provision "$RIVAL_ID" "$RIVAL_SLUG" 'Flow Rival' "owner@$RIVAL_SLUG.test" 'Rival Owner' 'Fl0w-Rival-Pass!'
activate "$ACME_ID"
activate "$RIVAL_ID"

ACME=$(login "$ACME_SLUG.$HOST_BASE" "$(printf '{"realm":"company","email":"owner@%s.test","password":"Fl0w-Acme-Pass!","companySlug":"%s"}' "$ACME_SLUG" "$ACME_SLUG")")
RIVAL=$(login "$RIVAL_SLUG.$HOST_BASE" "$(printf '{"realm":"company","email":"owner@%s.test","password":"Fl0w-Rival-Pass!","companySlug":"%s"}' "$RIVAL_SLUG" "$RIVAL_SLUG")")
check 'acme recruiter signed in'  yes "$([[ -n $ACME  ]] && echo yes || echo no)"
# Needed later to seat the owner on an interview panel.
ACME_OWNER_ACC=$(call GET /api/v1/recruiters "$ACME_SLUG.$HOST_BASE" "$ACME" >/dev/null; python3 -c "
import json
try: rows = json.load(open('$RESP')).get('recruiters', [])
except Exception: rows = []
print(next((m['accountId'] for m in rows if m.get('isOwner')), ''))")
check 'rival recruiter signed in' yes "$([[ -n $RIVAL ]] && echo yes || echo no)"

CAND_EMAIL="flow-candidate-$(date +%s)@example.test"
call POST /api/v1/auth/candidate/register "jobs.$HOST_BASE" '' \
  "{\"email\":\"$CAND_EMAIL\",\"password\":\"Fl0w-Cand-Pass!\",\"fullName\":\"Flow Candidate\"}" >/dev/null
CAND=$(login "jobs.$HOST_BASE" \
  "{\"realm\":\"candidate\",\"email\":\"$CAND_EMAIL\",\"password\":\"Fl0w-Cand-Pass!\"}")
check 'candidate signed in' yes "$([[ -n $CAND ]] && echo yes || echo no)"

# Seeding a pipeline is an event consumer's job, so it is eventually consistent
# by design. Wait for it rather than asserting against a race.
for _ in $(seq 1 20); do
  call GET /api/v1/applications/settings/stages "$ACME_SLUG.$HOST_BASE" "$ACME" >/dev/null
  [[ "$(jq_ stages)" != "0" ]] && break
  sleep 0.5
done

# --------------------------------------------------------------------- jobs --
section 'a company posts a job'
JOB_SLUG="principal-engineer-$(date +%s)"
JOB_BODY='{"title":"Principal Engineer","slug":"'"$JOB_SLUG"'","department":"Engineering","locations":["Remote"],"workMode":"remote","employmentType":"full_time","seniority":"principal","description":"Build things.","requirements":"Experience.","salary":{"min":18000000,"max":24000000,"currency":"USD","isPublic":true},"internalNotes":"stretch budget approved"}'
check 'create job' 201 "$(call POST /api/v1/jobs "$ACME_SLUG.$HOST_BASE" "$ACME" "$JOB_BODY")"
JOB_ID=$(jq_ id)

check 'job has a default application form' 200 \
  "$(call GET "/api/v1/jobs/$JOB_ID/form" "$ACME_SLUG.$HOST_BASE" "$ACME")"

check 'publish to portal and network' 200 \
  "$(call POST "/api/v1/jobs/$JOB_ID/publish" "$ACME_SLUG.$HOST_BASE" "$ACME" '{"portal":true,"network":true}')"

check "rival cannot read acme's job" 404 \
  "$(call GET "/api/v1/jobs/$JOB_ID" "$RIVAL_SLUG.$HOST_BASE" "$RIVAL")"

# ------------------------------------------------------------------- public --
section 'the job is discoverable'
check 'job appears on the public network board' 200 \
  "$(call GET /api/v1/public/jobs "jobs.$HOST_BASE" '')"
check 'public job detail' 200 \
  "$(call GET "/api/v1/public/jobs/$JOB_SLUG" "jobs.$HOST_BASE" '')"
LEAKED=$(python3 -c "
import json
try: d = json.load(open('$RESP'))
except Exception: d = {}
bad = [k for k in ('internalNotes','hiringManagerId','recruiterId','headcount') if d.get(k)]
print(','.join(bad) if bad else 'none')")
check 'public view hides internal fields' none "$LEAKED"

# -------------------------------------------------------------- application --
section 'a candidate applies'
# Every field the default form marks required. Leaving one out is a legitimate
# 422, which is what the previous run was actually demonstrating.
APPLY_BODY=$(printf '{"jobId":"%s","answers":{"full_name":"Flow Candidate","email":"%s","resume":"candidate/flow/resume.pdf","work_authorized":true},"source":"network"}' \
  "$JOB_ID" "$CAND_EMAIL")

# An incomplete submission must be refused before anything is stored.
INCOMPLETE_BODY=$(printf '{"jobId":"%s","answers":{"full_name":"Flow Candidate"},"source":"network"}' "$JOB_ID")
check 'an incomplete application is refused' 422 \
  "$(call POST /api/v1/public/apply "jobs.$HOST_BASE" "$CAND" "$INCOMPLETE_BODY")"

check 'apply' 201 "$(call POST /api/v1/public/apply "jobs.$HOST_BASE" "$CAND" "$APPLY_BODY")"
APP_ID=$(jq_ id)

check 'applying twice is refused' 409 \
  "$(call POST /api/v1/public/apply "jobs.$HOST_BASE" "$CAND" "$APPLY_BODY")"

check 'candidate sees their application' 200 \
  "$(call GET /api/v1/my-applications "jobs.$HOST_BASE" "$CAND")"

check 'recruiter sees the application' 200 \
  "$(call GET /api/v1/applications "$ACME_SLUG.$HOST_BASE" "$ACME")"
FOUND=$(jq_ applications)
check 'exactly one application in the pipeline' 1 "$FOUND"

check "rival sees no applications" 200 \
  "$(call GET /api/v1/applications "$RIVAL_SLUG.$HOST_BASE" "$RIVAL")"
check "rival's pipeline is empty" 0 "$(jq_ applications)"

# ----------------------------------------------------------------- pipeline --
section 'the pipeline moves'
check 'default stages were seeded' 200 \
  "$(call GET /api/v1/applications/settings/stages "$ACME_SLUG.$HOST_BASE" "$ACME")"
STAGE_COUNT=$(jq_ stages)
check 'more than one stage exists' yes "$([[ ${STAGE_COUNT:-0} -gt 1 ]] && echo yes || echo no)"
SECOND_STAGE=$(python3 -c "
import json
try: d = json.load(open('$RESP'))['stages']
except Exception: d = []
print(d[1]['id'] if len(d) > 1 else '')")

ADVANCE_BODY=$(printf '{"stageId":"%s","note":"looks strong"}' "$SECOND_STAGE")

check 'advance to the next stage' 200 \
  "$(call POST "/api/v1/applications/$APP_ID/advance" "$ACME_SLUG.$HOST_BASE" "$ACME" "$ADVANCE_BODY")"

check 'rival cannot advance it' 404 \
  "$(call POST "/api/v1/applications/$APP_ID/advance" "$RIVAL_SLUG.$HOST_BASE" "$RIVAL" "$ADVANCE_BODY")"

check 'stage history is recorded' 200 \
  "$(call GET "/api/v1/applications/$APP_ID/events" "$ACME_SLUG.$HOST_BASE" "$ACME")"

# ---------------------------------------------------------------- rejection --
section 'rejection carries a reason, not the note'
check 'default rejection reasons were seeded' 200 \
  "$(call GET /api/v1/applications/settings/rejection-reasons "$ACME_SLUG.$HOST_BASE" "$ACME")"
REASON=$(python3 -c "
import json
try: d = json.load(open('$RESP'))['rejectionReasons']
except Exception: d = []
print(d[0]['id'] if d else '')")

REJECT_BODY=$(printf '{"reasonId":"%s","note":"internal: salary mismatch"}' "$REASON")

check 'reject with a private note' 200 \
  "$(call POST "/api/v1/applications/$APP_ID/reject" "$ACME_SLUG.$HOST_BASE" "$ACME" "$REJECT_BODY")"

call GET /api/v1/my-applications "jobs.$HOST_BASE" "$CAND" >/dev/null
NOTE_LEAK=$(python3 -c "
import json
raw = open('$RESP').read()
print('leaked' if 'salary mismatch' in raw else 'hidden')")
check "candidate never sees the recruiter's private note" hidden "$NOTE_LEAK"

# ------------------------------------------------------------------- talent --
section 'talent discovery respects the candidate'
check 'candidate profile exists' 200 "$(call GET /api/v1/me "jobs.$HOST_BASE" "$CAND")"
CAND_PROFILE_ID=$(jq_ id)
check 'search returns nothing while not discoverable' 200 \
  "$(call GET /api/v1/talent/search "$ACME_SLUG.$HOST_BASE" "$ACME")"
check "this run's candidate is not yet discoverable" hidden "$(seen_self)"

check 'candidate opts in' 200 \
  "$(call PUT /api/v1/me/visibility "jobs.$HOST_BASE" "$CAND" '{"discoverable":true}')"
check 'now discoverable to companies' 200 \
  "$(call GET /api/v1/talent/search "$ACME_SLUG.$HOST_BASE" "$ACME")"
check "this run's candidate is now discoverable" visible "$(seen_self)"
CONTACT_LEAK=$(python3 -c "
import json
raw = open('$RESP').read()
print('leaked' if '$CAND_EMAIL' in raw else 'hidden')")
check 'search never exposes contact details' hidden "$CONTACT_LEAK"

# --------------------------------------------------------------- interviews --
section 'interviews, and the feedback nobody else may read'
IV_BODY=$(printf '{"applicationId":"%s","candidateId":"%s","roundTitle":"System Design","roundType":"technical","scheduledStart":"2026-10-15T10:00:00Z","durationMinutes":60,"format":"video","panelMemberIds":["%s"]}' "$APP_ID" "$CAND_PROFILE_ID" "$ACME_OWNER_ACC")
check 'schedule an interview' 201 "$(call POST /api/v1/interviews "$ACME_SLUG.$HOST_BASE" "$ACME" "$IV_BODY")"
IV_ID=$(jq_ id)
check 'it appears on the round list' 200 "$(call GET /api/v1/interviews "$ACME_SLUG.$HOST_BASE" "$ACME")"

# A day-bounded range has to include the day. `to` is applied inclusively, so a
# bare date resolved to midnight returned nothing for a day with rounds booked —
# an empty schedule rather than a bad query, which nobody reports as a bug.
check 'a one-day range finds the round booked that day' 1 \
  "$(call GET "/api/v1/interviews?from=2026-10-15&to=2026-10-15" "$ACME_SLUG.$HOST_BASE" "$ACME" >/dev/null; jq_ interviews)"
# ...without spilling into the next one. Widening the bound to cover the day is
# only right if it stops at the day: the round above is on the 15th, so a query
# for the 14th must not find it.
check '  ...and the day before does not find it' 0 \
  "$(call GET "/api/v1/interviews?from=2026-10-14&to=2026-10-14" "$ACME_SLUG.$HOST_BASE" "$ACME" >/dev/null; jq_ interviews)"
# A range that ends before it starts is a mistake worth naming, not an empty list.
check '  ...and a backwards range is refused' 422 \
  "$(call GET "/api/v1/interviews?from=2026-10-15&to=2026-10-14" "$ACME_SLUG.$HOST_BASE" "$ACME")"

# Scheduling invites the candidate and the panel. A create retried after a
# timeout — which is what a client does — must not book a second round and
# invite everyone to it twice.
IDEM_KEY="smoke-$RUN"
IDEM_BODY=$(printf '{"applicationId":"%s","candidateId":"%s","roundTitle":"Retry Probe","scheduledStart":"2026-10-21T10:00:00Z","durationMinutes":30,"format":"video","panelMemberIds":["%s"]}' "$APP_ID" "$CAND_PROFILE_ID" "$ACME_OWNER_ACC")
idem_call() {
  curl -s -o "$RESP" -w '%{http_code}' -X POST "$GW/api/v1/interviews" \
    -H "Host: $ACME_SLUG.$HOST_BASE" -H "Authorization: Bearer $ACME" \
    -H "Idempotency-Key: $IDEM_KEY" -H 'Content-Type: application/json' -d "$IDEM_BODY"
}
check 'scheduling with an idempotency key' 201 "$(idem_call)"
FIRST_ROUND=$(jq_ id)
check '  ...replayed returns the same round, not a new one' 200 "$(idem_call)"
check '  ...and it is the same round' "$FIRST_ROUND" "$(jq_ id)"
check "rival cannot read acme's round" 404 \
  "$(call GET "/api/v1/interviews/$IV_ID" "$RIVAL_SLUG.$HOST_BASE" "$RIVAL")"

SCORE=$(printf '{"overallRating":4,"recommendation":"hire","strengths":"PANEL-PRIVATE-NOTE"}')
check 'a panel member files a scorecard' 201 \
  "$(call POST "/api/v1/interviews/$IV_ID/scorecard" "$ACME_SLUG.$HOST_BASE" "$ACME" "$SCORE")"

# The whole reason interviews.submit_scorecard and interviews.view_scorecards are
# two keys. A panel that reads each other first produces one opinion with four
# signatures on it, so the narrow holder sees their own card and — just as
# importantly — a total that does not betray how many colleagues have filed.
READER_ROLE=$(printf '{"name":"Panelist %s","permissions":["interviews.read","interviews.submit_scorecard"]}' "$RUN")
check 'owner creates a submit-only panel role' 201 \
  "$(call POST /api/v1/company-roles "$ACME_SLUG.$HOST_BASE" "$ACME" "$READER_ROLE")"
PANEL_ROLE=$(jq_ id)
PANELIST=$(printf '{"email":"panelist-%s@%s.test","fullName":"Pat Panelist","password":"Pat-Panel-Pass1!","roleIds":["%s"]}' "$RUN" "$ACME_SLUG" "$PANEL_ROLE")
check 'owner adds the panelist' 201 \
  "$(call POST /api/v1/recruiters "$ACME_SLUG.$HOST_BASE" "$ACME" "$PANELIST")"
PANEL_ACC=$(jq_ member.accountId)

ADD_TO_PANEL=$(printf '{"panelMemberIds":["%s","%s"]}' "$ACME_OWNER_ACC" "$PANEL_ACC")
check 'the panelist joins the round' 200 \
  "$(call PATCH "/api/v1/interviews/$IV_ID" "$ACME_SLUG.$HOST_BASE" "$ACME" "$ADD_TO_PANEL")"

PANEL_LOGIN=$(printf '{"realm":"company","email":"panelist-%s@%s.test","password":"Pat-Panel-Pass1!","companySlug":"%s"}' "$RUN" "$ACME_SLUG" "$ACME_SLUG")
PANEL_TOKEN=$(login "$ACME_SLUG.$HOST_BASE" "$PANEL_LOGIN")
check 'the panelist can sign in' yes "$([[ -n $PANEL_TOKEN ]] && echo yes || echo no)"

MY_SCORE=$(printf '{"overallRating":3,"recommendation":"no_hire","strengths":"MY-OWN-NOTE"}')
check 'the panelist files their own' 201 \
  "$(call POST "/api/v1/interviews/$IV_ID/scorecard" "$ACME_SLUG.$HOST_BASE" "$PANEL_TOKEN" "$MY_SCORE")"

call GET "/api/v1/interviews/$IV_ID/scorecards" "$ACME_SLUG.$HOST_BASE" "$PANEL_TOKEN" >/dev/null
check 'submit-only sees exactly one card' 1 "$(jq_ scorecards)"
check "  ...and never the panel's private note" hidden \
  "$(python3 -c "print('leaked' if 'PANEL-PRIVATE-NOTE' in open('$RESP').read() else 'hidden')")"
check '  ...and the total does not betray the others' 1 "$(jq_ total)"

call GET "/api/v1/interviews/$IV_ID/scorecards" "$ACME_SLUG.$HOST_BASE" "$ACME" >/dev/null
check 'view_scorecards sees the whole panel' 2 "$(jq_ scorecards)"

# ------------------------------------------------------------------- offers --
section 'offers, and the money only some may see'
# The letter and the custom field carry the package in prose, which is how the
# portal actually sends it — and is where the first version of this check was
# blind: it asserted the four structured fields were absent and passed while the
# salary went out in the letter text.
OFFER_BODY=$(printf '{"applicationId":"%s","candidateId":"%s","designation":"Principal Engineer","departmentName":"Engineering","baseSalary":185000,"currency":"USD","payFrequency":"annual","signOnBonus":20000,"joiningDate":"2026-11-02","offerLetterContent":"Your annual base salary will be USD 185,000 with a sign-on bonus of USD 20,000.","customFields":[{"key":"Relocation allowance","value":"USD 40,000"}]}' "$APP_ID" "$CAND_PROFILE_ID")
check 'draft an offer' 201 "$(call POST /api/v1/offers "$ACME_SLUG.$HOST_BASE" "$ACME" "$OFFER_BODY")"
OFFER_ID=$(jq_ id)
check '  ...and the salary round-trips in major units' 185000 "$(jq_ baseSalary)"
check "rival cannot read acme's offer" 404 \
  "$(call GET "/api/v1/offers/$OFFER_ID" "$RIVAL_SLUG.$HOST_BASE" "$RIVAL")"

# offers.view_compensation gates the money, not the offer. The panelist role
# above holds neither, so it is given offers.read alone.
READ_ONLY=$(printf '{"permissions":["interviews.read","interviews.submit_scorecard","offers.read"]}')
check 'the panelist is given offers.read and nothing more' 200 \
  "$(call PATCH "/api/v1/company-roles/$PANEL_ROLE" "$ACME_SLUG.$HOST_BASE" "$ACME" "$READ_ONLY")"
PANEL_TOKEN=$(login "$ACME_SLUG.$HOST_BASE" "$PANEL_LOGIN")

call GET "/api/v1/offers/$OFFER_ID" "$ACME_SLUG.$HOST_BASE" "$PANEL_TOKEN" >/dev/null
check 'they can see the offer exists' "Principal Engineer" "$(jq_ designation)"
# Absent, not zeroed and not null: a "baseSalary": 0 is a lie a client renders.
check '  ...with the salary absent entirely' absent \
  "$(python3 -c "
import json
d = json.load(open('$RESP'))
print('present' if any(k in d for k in ('baseSalary','signOnBonus','currency','payFrequency')) else 'absent')")"
# Structured or prose, it is the same secret. Grepping the whole response rather
# than naming fields is the point: a money field added later is covered without
# anyone remembering to extend this list.
check '  ...and nowhere in the response at all' absent \
  "$(python3 -c "
raw = open('$RESP').read()
leaks = [s for s in ('185,000', '20,000', '40,000', 'Relocation allowance') if s in raw]
print('leaked: ' + ', '.join(leaks) if leaks else 'absent')")"

check 'the owner still sees it' 185000 \
  "$(call GET "/api/v1/offers/$OFFER_ID" "$ACME_SLUG.$HOST_BASE" "$ACME" >/dev/null; jq_ baseSalary)"

# -------------------------------------------------------------------- audit --
section 'the audit trail is the tenant own'
sleep 3
check "acme's trail is readable" 200 "$(call GET /api/v1/company-audit "$ACME_SLUG.$HOST_BASE" "$ACME")"
check '  ...and it recorded the round we scheduled' present \
  "$(python3 -c "print('present' if '$IV_ID' in open('$RESP').read() else 'absent')")"

call GET /api/v1/company-audit "$RIVAL_SLUG.$HOST_BASE" "$RIVAL" >/dev/null
# An audit trail that can be made to show another tenant's activity is worse
# than no audit trail, because it is believed.
check "rival's trail never mentions acme's round" absent \
  "$(python3 -c "print('present' if '$IV_ID' in open('$RESP').read() else 'absent')")"
check 'the platform trail is closed to a company' 404 \
  "$(call GET /api/v1/platform/audit "$ACME_SLUG.$HOST_BASE" "$ACME")"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ $fail -eq 0 ]]
