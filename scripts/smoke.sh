#!/usr/bin/env bash
#
# End-to-end smoke test against a running stack (scripts/dev.sh up).
#
# This asserts the security boundaries, not the happy path alone: a regression
# that opens a tenant boundary is the one that matters, and it is invisible to a
# test that only checks that sign-in works.
#
#   scripts/smoke.sh

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GW="${GATEWAY_URL:-http://localhost:8080}"
HOSTNAME_BASE="${PLATFORM_HOSTNAME:-karmarat.local}"

[[ -f .env ]] && { set -a; . ./.env; set +a; }
INTERNAL_TOKEN="${INTERNAL_SERVICE_TOKEN:-}"
ADMIN_EMAIL="${BOOTSTRAP_ADMIN_EMAIL:-admin@karmarat.local}"
ADMIN_PASSWORD="${BOOTSTRAP_ADMIN_PASSWORD:-}"

c_red=$'\033[31m'; c_grn=$'\033[32m'; c_dim=$'\033[2m'; c_off=$'\033[0m'
pass=0; fail=0

RESP=$(mktemp)
trap 'rm -f "$RESP"' EXIT

# check <description> <expected> <actual>
#
# On a mismatch the last response body is printed. A bare status code rarely
# says why a boundary check failed — an expired lockout, a rate limit and a real
# regression all look the same until you can see the problem document.
check() {
  if [[ "$2" == "$3" ]]; then
    printf '  %s✓%s %-52s %s\n' "$c_grn" "$c_off" "$1" "$3"
    pass=$((pass + 1))
  else
    printf '  %s✗%s %-52s expected %s, got %s\n' "$c_red" "$c_off" "$1" "$2" "$3"
    [[ -s "$RESP" ]] && printf '      %s%s%s\n' "$c_dim" "$(head -c 300 "$RESP")" "$c_off"
    fail=$((fail + 1))
  fi
}

section() { printf '\n%s%s%s\n' "$c_dim" "$1" "$c_off"; }

# status <method> <path> <host> [auth] [body]
status() {
  local method=$1 path=$2 host=$3 auth=${4:-} body=${5:-}
  local args=(-s -o "$RESP" -w '%{http_code}' -X "$method" "$GW$path" -H "Host: $host")
  [[ -n "$auth" ]] && args+=(-H "Authorization: Bearer $auth")
  [[ -n "$body" ]] && args+=(-H 'Content-Type: application/json' -d "$body")
  curl "${args[@]}"
}

# Sign-in is rate limited to 10/minute, which this script can exhaust on a rerun.
# A 429 says nothing about the boundary under test, so wait the window out once
# rather than reporting a spurious failure.
login_status() {
  local code
  code=$(status POST /api/v1/auth/login "$1" '' "$2")
  if [[ "$code" == "429" ]]; then
    printf '  %s…%s login rate limit hit — waiting 65s for the window to clear\n' "$c_dim" "$c_off" >&2
    sleep 65
    code=$(status POST /api/v1/auth/login "$1" '' "$2")
  fi
  printf '%s' "$code"
}

# login_token <host> <body> -> the access token, waiting out the rate limit once.
#
# The sign-in limit is ten a minute per address, and this suite now signs in as
# five different people. Every login therefore goes through a helper that can
# absorb a 429 — a direct curl would report the limit as a failed boundary.
login_token() {
  local body
  body=$(curl -s -X POST "$GW/api/v1/auth/login" -H "Host: $1" \
    -H 'Content-Type: application/json' -d "$2")
  if printf '%s' "$body" | grep -q '"status":429'; then
    printf '  %s…%s login rate limit hit — waiting 65s for the window to clear\n' "$c_dim" "$c_off" >&2
    sleep 65
    body=$(curl -s -X POST "$GW/api/v1/auth/login" -H "Host: $1" \
      -H 'Content-Type: application/json' -d "$2")
  fi
  printf '%s' "$body"
}

jpath() { python3 -c "import sys,json
try:
    d = json.load(sys.stdin)
except Exception:
    print(''); raise SystemExit
for k in '$1'.split('.'):
    if isinstance(d, dict): d = d.get(k, '')
    else: d = ''
print(d if not isinstance(d, (list, dict)) else len(d))"; }

printf 'Karmarat smoke test — %s\n' "$GW"

# ---------------------------------------------------------------- reachable --
section 'reachability'
check 'gateway healthz' 200 "$(curl -s -o /dev/null -w '%{http_code}' "$GW/healthz")"
check 'identity readyz' 200 "$(curl -s -o /dev/null -w '%{http_code}' "${IDENTITY_URL:-http://localhost:8081}/readyz")"

# ---------------------------------------------------------------------- auth --
section 'platform authentication'
if [[ -z "$ADMIN_PASSWORD" ]]; then
  printf '  %s!%s BOOTSTRAP_ADMIN_PASSWORD unset — skipping authenticated checks\n' "$c_red" "$c_off"
  exit 1
fi

login_body() { printf '{"realm":"%s","email":"%s","password":"%s"}' "$1" "$2" "$3"; }

ADMIN_GOOD=$(login_body platform "$ADMIN_EMAIL" "$ADMIN_PASSWORD")
ADMIN_BAD=$(login_body platform "$ADMIN_EMAIL" 'definitely-not-it')
NOBODY_BAD=$(login_body platform 'nobody@example.com' 'definitely-not-it')

LOGIN=$(login_token "root.$HOSTNAME_BASE" "$ADMIN_GOOD")
TOKEN=$(printf '%s' "$LOGIN" | jpath 'tokens.accessToken')
REFRESH=$(printf '%s' "$LOGIN" | jpath 'tokens.refreshToken')
PERM_COUNT=$(printf '%s' "$LOGIN" | jpath 'identity.permissions')

check 'admin login returns an access token' yes "$([[ -n $TOKEN ]] && echo yes || echo no)"
check 'admin holds the full platform permission set' 44 "$PERM_COUNT"
check 'wrong password rejected' 401 "$(login_status "root.$HOSTNAME_BASE" "$ADMIN_BAD")"
check 'unknown email rejected identically' 401 "$(login_status "root.$HOSTNAME_BASE" "$NOBODY_BAD")"
check 'authenticated request accepted' 200 \
  "$(status GET /api/v1/rbac/catalogue "root.$HOSTNAME_BASE" "$TOKEN")"

# ------------------------------------------------------------------ spoofing --
section 'header spoofing'
SPOOF=$(curl -s -o /dev/null -w '%{http_code}' "$GW/api/v1/rbac/catalogue" \
  -H "Host: root.$HOSTNAME_BASE" \
  -H 'X-Principal-Type: platform' \
  -H 'X-Permissions: platform_companies.read' \
  -H 'X-Company-ID: 00000000-0000-0000-0000-000000000000')
check 'client-supplied trust headers are stripped' 401 "$SPOOF"

# ------------------------------------------------------------------- tenancy --
section 'tenant isolation'
if [[ -z "$INTERNAL_TOKEN" ]]; then
  printf '  %s!%s INTERNAL_SERVICE_TOKEN unset — skipping tenant checks\n' "$c_red" "$c_off"
else
  ID_URL="${IDENTITY_URL:-http://localhost:8081}"
  # The company id is minted by the companies service, not by identity, so the
  # caller supplies it. Reusing a fixed id keeps repeat runs idempotent.
  provision() {
    curl -s -X POST "$ID_URL/internal/companies" \
      -H "X-Internal-Token: $INTERNAL_TOKEN" -H 'Content-Type: application/json' \
      -d "{\"companyId\":\"$1\",\"slug\":\"$2\",\"name\":\"$3\",\"ownerEmail\":\"$4\",\"ownerName\":\"$5\",\"ownerPassword\":\"$6\",\"state\":\"active\"}"
  }
  # An entitled tenant is a precondition of the checks below, not one of them:
  # the subscription gate has its own assertions, and leaving these tenants
  # unentitled would turn every boundary check into a 402 that proves nothing.
  # It must happen before the first request for these hostnames — the gateway
  # caches a resolved tenant for thirty seconds, so activating later would leave
  # a stale "inactive" behind.
  entitle() {
    curl -s -o /dev/null -X PATCH "$ID_URL/internal/companies/$1/subscription" \
      -H "X-Internal-Token: $INTERNAL_TOKEN" -H 'Content-Type: application/json' \
      -d '{"state":"active","entitlements":{"maxJobs":100,"canUseTalentSearch":true}}'
  }
  ALPHA_ID=00000000-0000-4000-8000-00000000a1fa
  BETA_ID=00000000-0000-4000-8000-00000000be7a
  provision "$ALPHA_ID" smoke-alpha 'Smoke Alpha' owner@smoke-alpha.test 'Alpha Owner' 'Sm0ke-Alpha-Pass!' >/dev/null
  provision "$BETA_ID"  smoke-beta  'Smoke Beta'  owner@smoke-beta.test  'Beta Owner'  'Sm0ke-Beta-Pass!'  >/dev/null
  entitle "$ALPHA_ID"
  entitle "$BETA_ID"

  check 'internal endpoint rejects an unauthenticated call' 401 \
    "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$ID_URL/internal/companies" \
       -H 'Content-Type: application/json' -d '{"slug":"nope","name":"Nope"}')"
  check 'reserved slug refused' 422 \
    "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$ID_URL/internal/companies" \
       -H "X-Internal-Token: $INTERNAL_TOKEN" -H 'Content-Type: application/json' \
       -d '{"companyId":"00000000-0000-4000-8000-0000000000ff","slug":"root","name":"Root","ownerEmail":"a@b.test","ownerName":"A","ownerPassword":"Str0ng-Pass!"}')"

  ALPHA=$(login_token "smoke-alpha.$HOSTNAME_BASE" \
    '{"realm":"company","email":"owner@smoke-alpha.test","password":"Sm0ke-Alpha-Pass!","companySlug":"smoke-alpha"}')
  ALPHA_TOKEN=$(printf '%s' "$ALPHA" | jpath 'tokens.accessToken')

  check 'company owner can sign in to its own portal' yes \
    "$([[ -n $ALPHA_TOKEN ]] && echo yes || echo no)"
  check "alpha's token is refused on beta's hostname" 403 \
    "$(status GET /api/v1/company/profile "smoke-beta.$HOSTNAME_BASE" "$ALPHA_TOKEN")"
  check 'company token refused on the root portal' 403 \
    "$(status GET /api/v1/admin/companies "root.$HOSTNAME_BASE" "$ALPHA_TOKEN")"
  check 'platform token refused on a company portal' 403 \
    "$(status GET /api/v1/company/profile "smoke-alpha.$HOSTNAME_BASE" "$TOKEN")"
  check 'company route not exposed on the jobs portal' 404 \
    "$(status GET /api/v1/company/profile "jobs.$HOSTNAME_BASE" "$ALPHA_TOKEN")"
fi

# ------------------------------------------------------ provisioning again --
section 'provisioning is safe to retry'
if [[ -z "$INTERNAL_TOKEN" ]]; then
  printf '  %s!%s INTERNAL_SERVICE_TOKEN unset — skipping\n' "$c_red" "$c_off"
else
  # A client that times out retries. Provisioning is an upsert on the company
  # id, so the retry has to be harmless — and it was not: it rewrote the
  # subscription to "none", which shuts every non-owner out of the portal, and
  # then failed anyway on the owner's existing membership.
  RETRY_ID=00000000-0000-4000-8000-0000000c0ffe
  provision "$RETRY_ID" smoke-retry 'Smoke Retry' owner@smoke-retry.test 'Retry Owner' 'Sm0ke-Retry-Pass!' >/dev/null
  entitle "$RETRY_ID"

  check 'the tenant is entitled' active \
    "$(curl -s "$ID_URL/internal/portal/resolve?slug=smoke-retry" -H "X-Internal-Token: $INTERNAL_TOKEN" | jpath 'subscriptionState')"

  RETRY_BODY=$(printf '{"companyId":"%s","slug":"smoke-retry","name":"Smoke Retry","ownerEmail":"owner@smoke-retry.test","ownerName":"Retry Owner","ownerPassword":"Sm0ke-Retry-Pass!","state":"active"}' "$RETRY_ID")
  check 'the same call again succeeds' 201 \
    "$(curl -s -o "$RESP" -w '%{http_code}' -X POST "$ID_URL/internal/companies" \
       -H "X-Internal-Token: $INTERNAL_TOKEN" -H 'Content-Type: application/json' -d "$RETRY_BODY")"
  check '  ...and the subscription survived it' active \
    "$(curl -s "$ID_URL/internal/portal/resolve?slug=smoke-retry" -H "X-Internal-Token: $INTERNAL_TOKEN" | jpath 'subscriptionState')"

  # A slug another tenant already answers on is a conflict the caller can act
  # on, not the 500 an unwrapped constraint violation used to produce.
  TAKEN=$(printf '{"companyId":"00000000-0000-4000-8000-0000000c0ff1","slug":"smoke-retry","name":"Impostor","ownerEmail":"nope@smoke-retry.test","ownerName":"Nope","ownerPassword":"Sm0ke-Nope-Pass!"}')
  check 'a slug another tenant holds is a conflict' 409 \
    "$(curl -s -o "$RESP" -w '%{http_code}' -X POST "$ID_URL/internal/companies" \
       -H "X-Internal-Token: $INTERNAL_TOKEN" -H 'Content-Type: application/json' -d "$TAKEN")"

  check 'a company id that is not a UUID is refused' 422 \
    "$(curl -s -o "$RESP" -w '%{http_code}' -X POST "$ID_URL/internal/companies" \
       -H "X-Internal-Token: $INTERNAL_TOKEN" -H 'Content-Type: application/json' \
       -d '{"companyId":"co_not_a_uuid","slug":"smoke-badid","name":"Bad","ownerEmail":"a@b.test","ownerName":"A","ownerPassword":"Sm0ke-Bad-Pass!"}')"
fi

# --------------------------------------------------------- team and roles --
section 'company team administration'
if [[ -z "$INTERNAL_TOKEN" || -z "${ALPHA_TOKEN:-}" ]]; then
  printf '  %s!%s no company session — skipping team checks\n' "$c_red" "$c_off"
else
  ALPHA_HOST="smoke-alpha.$HOSTNAME_BASE"
  RUN=$(date +%s)

  # Bodies are built with printf and held in variables. An escaped JSON literal
  # written inline in "$(status ... )" has its backslashes stripped by the outer
  # quoting context first, and curl then receives five words instead of one body.
  ROLES=$(curl -s "$GW/api/v1/company-roles" -H "Host: $ALPHA_HOST" -H "Authorization: Bearer $ALPHA_TOKEN")
  role_id() { printf '%s' "$ROLES" | python3 -c "
import sys, json
rows = json.load(sys.stdin).get('roles', [])
print(next((r['id'] for r in rows if r['slug'] == '$1'), ''))"; }
  ADMIN_ROLE=$(role_id hiring_admin)
  RECRUITER_ROLE=$(role_id recruiter)
  OWNER_ROLE=$(role_id owner)

  # The problem code, not just the status. A refusal to escalate and a missing
  # permission are both 403, and a delegation test that cannot tell them apart
  # passes just as happily when delegation is not the thing doing the refusing.
  problem_code() { python3 -c "
import json
try: print(json.load(open('$RESP')).get('code', ''))
except Exception: print('')"; }

  check 'owner can list the company roles' 200 \
    "$(status GET /api/v1/company-roles "$ALPHA_HOST" "$ALPHA_TOKEN")"
  check 'the seeded roles are present' yes \
    "$([[ -n $ADMIN_ROLE && -n $RECRUITER_ROLE && -n $OWNER_ROLE ]] && echo yes || echo no)"
  check 'owner can list the roster' 200 \
    "$(status GET /api/v1/recruiters "$ALPHA_HOST" "$ALPHA_TOKEN")"
  check "beta's portal will not serve alpha's roster" 403 \
    "$(status GET /api/v1/recruiters "smoke-beta.$HOSTNAME_BASE" "$ALPHA_TOKEN")"
  check 'the roster is not exposed on the jobs portal' 404 \
    "$(status GET /api/v1/recruiters "jobs.$HOSTNAME_BASE" "$ALPHA_TOKEN")"

  ADMIN_MAIL="ada-$RUN@smoke-alpha.test"
  SAM_MAIL="sam-$RUN@smoke-alpha.test"
  ADD_ADMIN=$(printf '{"email":"%s","fullName":"Ada Admin","password":"Ada-Admin-Pass1!","jobTitle":"Head of Talent","roleIds":["%s"]}' "$ADMIN_MAIL" "$ADMIN_ROLE")
  ADD_SAM=$(printf '{"email":"%s","fullName":"Sam Sourcer","password":"Sam-Sourcer-Pass1!","roleIds":["%s"]}' "$SAM_MAIL" "$RECRUITER_ROLE")

  check 'owner adds a hiring administrator' 201 \
    "$(status POST /api/v1/recruiters "$ALPHA_HOST" "$ALPHA_TOKEN" "$ADD_ADMIN")"
  ADMIN_ACC=$(python3 -c "import json; print(json.load(open('$RESP'))['member']['accountId'])" 2>/dev/null || echo '')
  check 'owner adds a recruiter' 201 \
    "$(status POST /api/v1/recruiters "$ALPHA_HOST" "$ALPHA_TOKEN" "$ADD_SAM")"
  SAM_ACC=$(python3 -c "import json; print(json.load(open('$RESP'))['member']['accountId'])" 2>/dev/null || echo '')

  # The seeded hiring administrator may read roles but not author them: only an
  # owner can, by default. Delegation is only a meaningful rule for somebody who
  # holds `company_roles.create` *and* less than everything, so the owner builds
  # exactly that person here — otherwise every refusal below would be a missing
  # permission wearing delegation's clothes.
  AUTHOR_PERMS=$(printf '%s' "$ROLES" | python3 -c "
import sys, json
rows = json.load(sys.stdin).get('roles', [])
held = set(next((r['permissions'] for r in rows if r['slug'] == 'hiring_admin'), []))
held.update(['company_roles.create', 'company_roles.read', 'company_roles.update',
             'company_roles.delete', 'company_roles.assign_permissions'])
print(json.dumps({'name': 'Role Author $RUN', 'badge': 'Author',
                  'description': 'Manages the team and authors roles.',
                  'permissions': sorted(held)}))")
  check 'owner creates a role-authoring role' 201 \
    "$(status POST /api/v1/company-roles "$ALPHA_HOST" "$ALPHA_TOKEN" "$AUTHOR_PERMS")"
  AUTHOR_ROLE=$(python3 -c "import json; print(json.load(open('$RESP'))['id'])" 2>/dev/null || echo '')

  GRANT_AUTHOR=$(printf '{"roleIds":["%s","%s"],"primaryRoleId":"%s"}' "$ADMIN_ROLE" "$AUTHOR_ROLE" "$ADMIN_ROLE")
  check 'owner grants it to the administrator' 200 \
    "$(status PUT "/api/v1/recruiters/$ADMIN_ACC/roles" "$ALPHA_HOST" "$ALPHA_TOKEN" "$GRANT_AUTHOR")"

  ADMIN_LOGIN=$(printf '{"realm":"company","email":"%s","password":"Ada-Admin-Pass1!","companySlug":"smoke-alpha"}' "$ADMIN_MAIL")
  ADMIN_TOKEN=$(login_token "$ALPHA_HOST" "$ADMIN_LOGIN" | jpath 'tokens.accessToken')
  check 'the new administrator can sign in' yes "$([[ -n $ADMIN_TOKEN ]] && echo yes || echo no)"

  OWNER_ACC=$(curl -s "$GW/api/v1/recruiters" -H "Host: $ALPHA_HOST" -H "Authorization: Bearer $ADMIN_TOKEN" \
    | python3 -c "
import sys, json
rows = json.load(sys.stdin).get('recruiters', [])
print(next((m['accountId'] for m in rows if m['isOwner']), ''))")

  # The escalation path this whole surface exists to close: somebody who may
  # create roles mints one holding everything, then wears it.
  EVERYTHING=$(printf '%s' "$ROLES" | python3 -c "
import sys, json
rows = json.load(sys.stdin).get('roles', [])
owner = next((r for r in rows if r['isSuperAdmin']), {'permissions': []})
print(json.dumps({'name': 'Shadow Owner $RUN', 'permissions': owner['permissions']}))")
  check 'a role author cannot mint an unrestricted role' 403 \
    "$(status POST /api/v1/company-roles "$ALPHA_HOST" "$ADMIN_TOKEN" "$EVERYTHING")"
  check '  ...and it is delegation that refuses it' not_permitted "$(problem_code)"

  DEMOTE=$(printf '{"roleIds":["%s"]}' "$RECRUITER_ROLE")
  check 'an administrator cannot demote the owner' 403 \
    "$(status PUT "/api/v1/recruiters/$OWNER_ACC/roles" "$ALPHA_HOST" "$ADMIN_TOKEN" "$DEMOTE")"
  check '  ...and it is delegation that refuses it' not_permitted "$(problem_code)"

  PROMOTE_SELF=$(printf '{"roleIds":["%s"]}' "$OWNER_ROLE")
  check 'an administrator cannot change their own roles' 403 \
    "$(status PUT "/api/v1/recruiters/$ADMIN_ACC/roles" "$ALPHA_HOST" "$ADMIN_TOKEN" "$PROMOTE_SELF")"
  check '  ...and it is the self-modification rule' not_permitted "$(problem_code)"

  check 'an administrator cannot suspend the owner' 403 \
    "$(status PATCH "/api/v1/recruiters/$OWNER_ACC/status" "$ALPHA_HOST" "$ADMIN_TOKEN" '{"active":false}')"

  NIGHT_ROLE=$(printf '{"name":"Night Sourcer %s","description":"After-hours sourcing","badge":"Sourcer","permissions":["jobs.read","candidates.read","talent_search.search"]}' "$RUN")
  check 'a role author can create a role within their own access' 201 \
    "$(status POST /api/v1/company-roles "$ALPHA_HOST" "$ADMIN_TOKEN" "$NIGHT_ROLE")"
  NIGHT_ID=$(python3 -c "import json; print(json.load(open('$RESP'))['id'])" 2>/dev/null || echo '')

  ASSIGN_NIGHT=$(printf '{"roleIds":["%s"]}' "$NIGHT_ID")
  check 'an administrator can assign it to a recruiter' 200 \
    "$(status PUT "/api/v1/recruiters/$SAM_ACC/roles" "$ALPHA_HOST" "$ADMIN_TOKEN" "$ASSIGN_NIGHT")"

  check 'a role still held cannot be deleted' 409 \
    "$(status DELETE "/api/v1/company-roles/$NIGHT_ID" "$ALPHA_HOST" "$ADMIN_TOKEN")"
  check 'a built-in role cannot be deleted' 409 \
    "$(status DELETE "/api/v1/company-roles/$RECRUITER_ROLE" "$ALPHA_HOST" "$ALPHA_TOKEN")"

  REPERMISSION_OWNER='{"permissions":["jobs.read"]}'
  check "the owner role's permissions cannot be rewritten" 409 \
    "$(status PATCH "/api/v1/company-roles/$OWNER_ROLE" "$ALPHA_HOST" "$ALPHA_TOKEN" "$REPERMISSION_OWNER")"

  check 'an administrator can suspend a recruiter' 200 \
    "$(status PATCH "/api/v1/recruiters/$SAM_ACC/status" "$ALPHA_HOST" "$ADMIN_TOKEN" '{"active":false}')"
  SAM_LOGIN=$(printf '{"realm":"company","email":"%s","password":"Sam-Sourcer-Pass1!","companySlug":"smoke-alpha"}' "$SAM_MAIL")
  check 'a suspended recruiter can no longer sign in' 403 "$(login_status "$ALPHA_HOST" "$SAM_LOGIN")"

  # Adding an address that already has a membership reinstates that person and
  # rewrites their roles. That is a status change and a role assignment wearing
  # a create request, so it must answer to the same rules — otherwise
  # `recruiters.create` alone is a way to un-suspend somebody and to demote a
  # colleague the actor has no authority over.
  # The target has to be somebody suspended who outranks the administrator, so
  # the owner makes one: a deputy holding the unrestricted role, then suspended.
  # (The owner themselves cannot be suspended — they are the last one.)
  DEPUTY_MAIL="deputy-$RUN@smoke-alpha.test"
  ADD_DEPUTY=$(printf '{"email":"%s","fullName":"Devi Deputy","password":"Devi-Deputy-Pass1!","roleIds":["%s"]}' "$DEPUTY_MAIL" "$OWNER_ROLE")
  check 'owner adds a deputy holding the unrestricted role' 201 \
    "$(status POST /api/v1/recruiters "$ALPHA_HOST" "$ALPHA_TOKEN" "$ADD_DEPUTY")"
  DEPUTY_ACC=$(python3 -c "import json; print(json.load(open('$RESP'))['member']['accountId'])" 2>/dev/null || echo '')
  check 'owner suspends the deputy' 200 \
    "$(status PATCH "/api/v1/recruiters/$DEPUTY_ACC/status" "$ALPHA_HOST" "$ALPHA_TOKEN" '{"active":false}')"

  # Adding an address that already has a membership reinstates that person and
  # rewrites their roles — a status change and a role assignment wearing a create
  # request. It must answer to the same rules, or `recruiters.create` alone is a
  # way to un-suspend somebody and demote a colleague you have no authority over.
  DEMOTE_DEPUTY=$(printf '{"email":"%s","fullName":"Devi Deputy","roleIds":["%s"]}' "$DEPUTY_MAIL" "$RECRUITER_ROLE")
  check 'an administrator cannot demote a suspended superior by re-adding them' 403 \
    "$(status POST /api/v1/recruiters "$ALPHA_HOST" "$ADMIN_TOKEN" "$DEMOTE_DEPUTY")"
  check '  ...and it is the authority rule that refuses it' not_permitted "$(problem_code)"

  # The owner holds every capability and outranks everyone, so for them the same
  # request is a legitimate restore.
  RESTORE_DEPUTY=$(printf '{"email":"%s","fullName":"Devi Deputy","roleIds":["%s"]}' "$DEPUTY_MAIL" "$OWNER_ROLE")
  check 'an owner can reinstate the deputy' 201 \
    "$(status POST /api/v1/recruiters "$ALPHA_HOST" "$ALPHA_TOKEN" "$RESTORE_DEPUTY")"
  check '  ...and it reads as a reinstatement, not a new account' true \
    "$(python3 -c "
import json
try:
    d = json.load(open('$RESP'))
    print(str(d.get('reactivated', False)).lower())
except Exception:
    print('')")"

  check 'an active member cannot be added twice' 422 \
    "$(status POST /api/v1/recruiters "$ALPHA_HOST" "$ALPHA_TOKEN" "$RESTORE_DEPUTY")"

  # A role id belonging to another tenant must read as absent rather than as
  # forbidden: answering "you may not touch that" confirms it exists, which is
  # itself a cross-tenant disclosure. Asked as beta's own owner, on beta's own
  # portal, so nothing but the record's ownership is under test.
  BETA_LOGIN='{"realm":"company","email":"owner@smoke-beta.test","password":"Sm0ke-Beta-Pass!","companySlug":"smoke-beta"}'
  BETA_TOKEN=$(login_token "smoke-beta.$HOSTNAME_BASE" "$BETA_LOGIN" | jpath 'tokens.accessToken')

  check "beta cannot delete alpha's role" 404 \
    "$(status DELETE "/api/v1/company-roles/$OWNER_ROLE" "smoke-beta.$HOSTNAME_BASE" "$BETA_TOKEN")"
  RETITLE='{"jobTitle":"Retitled By A Stranger"}'
  check "beta cannot edit alpha's member" 404 \
    "$(status PATCH "/api/v1/recruiters/$ADMIN_ACC" "smoke-beta.$HOSTNAME_BASE" "$BETA_TOKEN" "$RETITLE")"

  # A name belongs to the account, which every company that person recruits for
  # shares. Accepting one here renamed them inside other tenants.
  RENAME='{"fullName":"Renamed By Their Employer"}'
  check "a member's name cannot be set by their company" 400 \
    "$(status PATCH "/api/v1/recruiters/$SAM_ACC" "$ALPHA_HOST" "$ALPHA_TOKEN" "$RENAME")"
fi

# -------------------------------------------------------------------- tokens --
section 'refresh rotation'
R1=$(curl -s -X POST "$GW/api/v1/auth/refresh" -H "Host: root.$HOSTNAME_BASE" \
  -H 'Content-Type: application/json' -d "{\"refreshToken\":\"$REFRESH\"}")
R1_TOKEN=$(printf '%s' "$R1" | jpath 'tokens.accessToken')
check 'refresh returns a new access token' yes "$([[ -n $R1_TOKEN ]] && echo yes || echo no)"
check 'replaying the spent refresh token is refused' 401 \
  "$(status POST /api/v1/auth/refresh "root.$HOSTNAME_BASE" '' "{\"refreshToken\":\"$REFRESH\"}")"

# --------------------------------------------------------------------- total --
printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ $fail -eq 0 ]]
