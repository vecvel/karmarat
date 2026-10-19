#!/usr/bin/env bash
# ==============================================================================
# Karmarat UAT / Staging Data Seeder
#
# Provisions realistic demo organizations, hiring teams, job requisitions,
# candidates, and pipeline states for testing and demonstrations.
#
# Usage:
#   ./scripts/seed-uat.sh [gateway_url]
# ==============================================================================

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if [[ -f .env ]]; then
  set -a && source .env && set +a
fi

GW="${1:-${GATEWAY_URL:-http://localhost:8080}}"
HOST_BASE="${PLATFORM_HOSTNAME:-karmarat.local}"
INTERNAL_TOKEN="${INTERNAL_SERVICE_TOKEN:-local-internal-dev-token-secret-32-chars}"
ID_URL="${IDENTITY_URL:-http://localhost:8081}"

echo "=========================================================="
echo " Seeding Karmarat Demo Data"
echo " Gateway:  $GW"
echo " Host:     $HOST_BASE"
echo "=========================================================="

RESP=$(mktemp)
trap 'rm -f "$RESP"' EXIT

call() {
  local args=(-s -o "$RESP" -w '%{http_code}' -X "$1" "$GW$2" -H "Host: $3")
  [[ -n "${4:-}" ]] && args+=(-H "Authorization: Bearer $4")
  [[ -n "${5:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$5")
  curl "${args[@]}"
}

call_internal() {
  curl -s -o "$RESP" -w '%{http_code}' -X "$1" "$2" \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $INTERNAL_TOKEN" \
    -d "$3"
}

jq_() {
  python3 -c "
import sys, json
try: d = json.load(open('$RESP'))
except Exception: print(''); sys.exit(0)
for k in '$1'.split('.'):
    if isinstance(d, dict): d = d.get(k, '')
    elif isinstance(d, list) and k.isdigit() and int(k) < len(d): d = d[int(k)]
    else: d = ''
print(d if not isinstance(d, (list, dict)) else (len(d) if isinstance(d, list) else json.dumps(d)))"
}

# 1. Provision Demo Tenants (Acme and Globex)
echo "[1/4] Provisioning Enterprise Tenants..."

ACME_JSON=$(cat <<EOF
{
  "companyId": "cmp_acme_prod_001",
  "slug": "acme",
  "name": "Acme Technologies",
  "ownerEmail": "owner@acme.com",
  "ownerName": "Sarah Connor",
  "ownerPassword": "Password123!"
}
EOF
)
call_internal POST "$ID_URL/internal/provision/company" "$ACME_JSON" > /dev/null || true
echo "      - Acme Technologies (acme.$HOST_BASE) ready"

GLOBEX_JSON=$(cat <<EOF
{
  "companyId": "cmp_globex_prod_002",
  "slug": "globex",
  "name": "Globex Robotics",
  "ownerEmail": "owner@globex.com",
  "ownerName": "Hank Scorpio",
  "ownerPassword": "Password123!"
}
EOF
)
call_internal POST "$ID_URL/internal/provision/company" "$GLOBEX_JSON" > /dev/null || true
echo "      - Globex Robotics (globex.$HOST_BASE) ready"

# 2. Login as Acme Recruiter
echo "[2/4] Authenticating Acme Hiring Team..."
LOGIN_JSON='{"realm":"company","email":"owner@acme.com","password":"Password123!"}'
call POST /api/v1/auth/login "acme.$HOST_BASE" '' "$LOGIN_JSON" > /dev/null
ACME_TOKEN=$(jq_ tokens.accessToken)

if [[ -z "$ACME_TOKEN" ]]; then
  echo "[-] Failed to authenticate as Acme owner. Check identity service."
  exit 1
fi

# 3. Create Job Requisitions
echo "[3/4] Publishing Job Requisitions..."
JOB1_PAYLOAD=$(cat <<EOF
{
  "title": "Staff Backend Engineer (Go & Distributed Systems)",
  "department": "Infrastructure",
  "location": "Remote / San Francisco",
  "employmentType": "full_time",
  "description": "We are seeking a Staff Backend Engineer to scale our low-latency distributed pipeline.",
  "status": "published",
  "visibility": "network"
}
EOF
)
call POST /api/v1/jobs "acme.$HOST_BASE" "$ACME_TOKEN" "$JOB1_PAYLOAD" > /dev/null
JOB1_ID=$(jq_ id)
echo "      - Published: Staff Backend Engineer (ID: $JOB1_ID)"

JOB2_PAYLOAD=$(cat <<EOF
{
  "title": "Senior Product Designer",
  "department": "Design",
  "location": "New York, NY",
  "employmentType": "full_time",
  "description": "Design sleek, intuitive interfaces for enterprise workflows.",
  "status": "published",
  "visibility": "network"
}
EOF
)
call POST /api/v1/jobs "acme.$HOST_BASE" "$ACME_TOKEN" "$JOB2_PAYLOAD" > /dev/null
JOB2_ID=$(jq_ id)
echo "      - Published: Senior Product Designer (ID: $JOB2_ID)"

# 4. Register Sample Candidates & Applications
echo "[4/4] Generating Candidate Applications & Pipelines..."
CAND1_REG='{"realm":"candidate","email":"alice.engineer@example.com","password":"Password123!","name":"Alice Chen"}'
call POST /api/v1/auth/register "jobs.$HOST_BASE" '' "$CAND1_REG" > /dev/null || true

call POST /api/v1/auth/login "jobs.$HOST_BASE" '' '{"realm":"candidate","email":"alice.engineer@example.com","password":"Password123!"}' > /dev/null
ALICE_TOKEN=$(jq_ tokens.accessToken)

if [[ -n "$ALICE_TOKEN" && -n "$JOB1_ID" ]]; then
  APP_PAYLOAD=$(cat <<EOF
{
  "jobId": "$JOB1_ID",
  "resumeSummary": "Senior Go Engineer with 8 years building distributed systems and Kubernetes operators.",
  "linkedinUrl": "https://linkedin.com/in/alice-chen-demo"
}
EOF
)
  call POST "/api/v1/jobs/$JOB1_ID/apply" "jobs.$HOST_BASE" "$ALICE_TOKEN" "$APP_PAYLOAD" > /dev/null || true
  echo "      - Alice Chen applied to Staff Backend Engineer"
fi

echo "=========================================================="
echo " UAT / Staging Seeding Complete!"
echo " Demo Logins:"
echo "   Company Owner: owner@acme.com / Password123!"
echo "   Portal URL:    https://acme.$HOST_BASE"
echo "   Candidate:     alice.engineer@example.com / Password123!"
echo "   Portal URL:    https://jobs.$HOST_BASE"
echo "=========================================================="
